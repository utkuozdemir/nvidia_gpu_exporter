package integration_test

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/app"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/capture"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/captures"
)

// update regenerates the expected output files instead of comparing against them.
var update = flag.Bool("update", false, "update the expected output files")

const startupTimeout = 30 * time.Second

// wallClockFamilies are derived from the current time and elapsed durations,
// so they are excluded from the expected outputs and asserted separately.
var wallClockFamilies = map[string]bool{
	"nvidia_smi_last_collect_duration_seconds":          true,
	"nvidia_smi_last_collect_success_timestamp_seconds": true,
}

// fakeBin is the fake nvidia-smi binary, built once for the whole suite.
var fakeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fake-nvidia-smi")
	if err != nil {
		panic(err)
	}

	defer os.RemoveAll(dir)

	fakeBin = filepath.Join(dir, "fake-nvidia-smi")
	if runtime.GOOS == "windows" {
		fakeBin += ".exe"
	}

	build := exec.CommandContext(context.Background(), "go", "build", "-o", fakeBin, "../../cmd/fake-nvidia-smi")

	output, err := build.CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("failed to build the fake nvidia-smi: %v\n%s", err, output))
	}

	// returning instead of os.Exit lets the deferred cleanup run; the test
	// wrapper passes m.Run's result to os.Exit itself (Go 1.15+)
	m.Run()
}

// fakeCommand builds the value for the exporter's nvidia-smi command flag,
// with the paths quoted so spaces in temporary or repository paths survive
// the command splitting.
func fakeCommand(capturePath string, fakeArgs ...string) string {
	parts := make([]string, 0, 3+len(fakeArgs))
	parts = append(parts, quote(fakeBin), "--capture", quote(capturePath))
	parts = append(parts, fakeArgs...)

	return strings.Join(parts, " ")
}

// quote single-quotes a path for the command splitting, escaping any literal
// single quote the POSIX way (closing the quotes, escaping the quote,
// reopening), so even a path like /home/o'connor survives.
func quote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// startExporter runs the real exporter entry in-process with the given flags
// and returns its base URL. Shutdown and error checking happen in cleanup.
func startExporter(t *testing.T, args ...string) string {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	listenCh := make(chan []net.Addr, 1)
	doneCh := make(chan error, 1)

	args = append([]string{"--web.listen-address=127.0.0.1:0", "--log.level=warn"}, args...)

	go func() {
		doneCh <- app.Run(ctx, args, app.Options{
			OnListen: func(addrs []net.Addr) { listenCh <- addrs },
		})
	}()

	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-doneCh)
	})

	select {
	case addrs := <-listenCh:
		require.Len(t, addrs, 1)

		return "http://" + addrs[0].String()
	case err := <-doneCh:
		doneCh <- err // let the cleanup report it

		t.Fatalf("exporter exited before listening: %v", err)
	case <-time.After(startupTimeout):
		t.Fatal("timed out waiting for the exporter to listen")
	}

	return ""
}

// scrape fetches the metrics page.
func scrape(t *testing.T, baseURL string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), startupTimeout)
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/metrics", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return string(body)
}

// familyName extracts the metric family a text-exposition line belongs to,
// returning empty for lines that belong to no family (blanks).
func familyName(line string) string {
	if rest, isMeta := strings.CutPrefix(line, "# HELP "); isMeta {
		return strings.SplitN(rest, " ", 2)[0]
	}

	if rest, isMeta := strings.CutPrefix(line, "# TYPE "); isMeta {
		return strings.SplitN(rest, " ", 2)[0]
	}

	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}

	if cut := strings.IndexAny(line, "{ "); cut >= 0 {
		return line[:cut]
	}

	return line
}

// filterDeterministic keeps the exporter's own families and drops the
// wall-clock-derived ones, leaving exactly the lines whose content is fully
// determined by the replayed capture.
func filterDeterministic(metrics string) string {
	var kept []string

	for line := range strings.SplitSeq(metrics, "\n") {
		name := familyName(line)
		if !strings.HasPrefix(name, "nvidia_smi_") || wallClockFamilies[name] {
			continue
		}

		kept = append(kept, line)
	}

	return strings.Join(kept, "\n") + "\n"
}

// replayCase is one cell of the replay matrix: a capture in one of its
// states, and the file holding the expected scrape output for it.
type replayCase struct {
	captureName  string
	state        string
	expectedFile string
}

// replayCases discovers the replay matrix from the embedded corpus itself:
// every committed capture, in every state it holds sections for.
func replayCases(t *testing.T) []replayCase {
	t.Helper()

	names, err := fs.Glob(captures.FS, "*.txt")
	require.NoError(t, err)
	require.NotEmpty(t, names)

	var cases []replayCase

	for _, name := range names {
		content, err := fs.ReadFile(captures.FS, name)
		require.NoError(t, err)

		capt, err := capture.Parse(string(content))
		require.NoError(t, err)

		for _, state := range []string{"idle", "load"} {
			if capt.Find(state, "query-gpu (csv") == nil {
				continue
			}

			cases = append(cases, replayCase{
				captureName:  name,
				state:        state,
				expectedFile: strings.TrimSuffix(name, ".txt") + "__" + state + ".metrics",
			})
		}
	}

	return cases
}

// TestExpectedMetrics runs the exporter against every committed capture in
// every state the capture holds, and compares the deterministic part of the
// scrape against the expected output file. Run with -update to regenerate them.
func TestExpectedMetrics(t *testing.T) {
	t.Parallel()

	for _, testCase := range replayCases(t) {
		t.Run(testCase.expectedFile, func(t *testing.T) {
			t.Parallel()

			baseURL := startExporter(t,
				"--nvidia-smi-command="+fakeCommand(testCase.captureName, "--state", testCase.state),
				"--collect.compute-apps")

			got := filterDeterministic(scrape(t, baseURL))
			expectedPath := filepath.Join("testdata", testCase.expectedFile)

			if *update {
				require.NoError(t, os.MkdirAll("testdata", 0o755))
				require.NoError(t, os.WriteFile(expectedPath, []byte(got), 0o600))

				return
			}

			want, err := os.ReadFile(expectedPath)
			require.NoError(
				t,
				err,
				"missing expected output for a new capture? generate and review it with: go test ./internal/integration/ -update",
			)

			assert.Equal(t, string(want), got)
		})
	}
}

// TestNoStaleExpectedFiles fails when a committed expected output file corresponds to no
// capture/state pair anymore, e.g. after a capture rename or removal.
func TestNoStaleExpectedFiles(t *testing.T) {
	t.Parallel()

	expected := make(map[string]bool)
	for _, testCase := range replayCases(t) {
		expected[testCase.expectedFile] = true
	}

	files, err := filepath.Glob(filepath.Join("testdata", "*.metrics"))
	require.NoError(t, err)

	for _, file := range files {
		assert.True(t, expected[filepath.Base(file)],
			"stale expected output %s: no capture/state produces it anymore, delete it", file)
	}
}

// TestWallClockFamilies covers the families the expected outputs exclude.
func TestWallClockFamilies(t *testing.T) {
	t.Parallel()

	baseURL := startExporter(t, "--nvidia-smi-command="+fakeCommand(defaultCapture(t)))
	metrics := scrape(t, baseURL)

	assert.Contains(t, metrics, "nvidia_smi_last_collect_duration_seconds ")
	assert.Contains(t, metrics, "nvidia_smi_last_collect_success_timestamp_seconds ")
	assert.Contains(t, metrics, "nvidia_smi_last_collect_success 1")
}

// defaultCapture is the capture used by the targeted tests, an embedded
// capture name.
func defaultCapture(t *testing.T) string {
	t.Helper()

	return captures.Default
}

// TestComputeAppsDisabled proves the per-process families stay absent without
// the opt-in flag.
func TestComputeAppsDisabled(t *testing.T) {
	t.Parallel()

	baseURL := startExporter(t, "--nvidia-smi-command="+fakeCommand(defaultCapture(t)))

	assert.NotContains(t, scrape(t, baseURL), "nvidia_smi_compute_app")
}

// TestValueOverride proves the fake's --set flag reaches a metric through the
// exporter's command splitting and collection pipeline, so a test can drive a
// field's value without a bespoke capture file.
func TestValueOverride(t *testing.T) {
	t.Parallel()

	baseURL := startExporter(t, "--nvidia-smi-command="+
		fakeCommand(defaultCapture(t), "--set", "temperature.gpu=95"))

	assert.Regexp(t, `nvidia_smi_temperature_gpu\{uuid="[^"]+"\} 95\b`, scrape(t, baseURL))
}

// TestValueOverrideQuotedSpaces proves an override value with spaces survives
// the exporter's command splitting when quoted, checked on the gpu_info label.
func TestValueOverrideQuotedSpaces(t *testing.T) {
	t.Parallel()

	baseURL := startExporter(t, "--nvidia-smi-command="+
		fakeCommand(defaultCapture(t), "--set", quote("name=Fake RTX 3090")))

	assert.Regexp(t, `nvidia_smi_gpu_info\{[^}]*name="Fake RTX 3090"`, scrape(t, baseURL))
}

// TestValueRange proves --set-range flows through to a metric within its bounds.
func TestValueRange(t *testing.T) {
	t.Parallel()

	baseURL := startExporter(t, "--nvidia-smi-command="+
		fakeCommand(defaultCapture(t), "--seed", "1", "--set-range", "temperature.gpu=90:95"))

	match := regexp.MustCompile(`nvidia_smi_temperature_gpu\{uuid="[^"]+"\} (\d+(?:\.\d+)?)`).
		FindStringSubmatch(scrape(t, baseURL))
	require.NotNil(t, match)

	v, err := strconv.ParseFloat(match[1], 64)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, v, 90.0)
	assert.LessOrEqual(t, v, 95.0)
}

// TestConfigFile proves a --config yaml drives the capture and a fixed override
// with no --capture flag, so the whole setup can live in the file.
func TestConfigFile(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "fake.yaml")
	body := "capture: " + captures.Default + "\noverrides:\n  temperature.gpu: {value: \"88\"}\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(body), 0o600))

	baseURL := startExporter(t, "--nvidia-smi-command="+quote(fakeBin)+" --config "+quote(cfgPath))

	assert.Regexp(t, `nvidia_smi_temperature_gpu\{uuid="[^"]+"\} 88\b`, scrape(t, baseURL))
}

// TestMultiGPU proves the fake's --gpus replication comes out of the exporter
// as distinct per-GPU series: the uuid is the exporter's identity label, so
// two simulated GPUs must yield two temperature series with different uuids,
// stable across scrapes.
func TestMultiGPU(t *testing.T) {
	t.Parallel()

	baseURL := startExporter(t, "--nvidia-smi-command="+fakeCommand(defaultCapture(t), "--gpus", "2"))

	series := regexp.MustCompile(`nvidia_smi_temperature_gpu\{uuid="([^"]+)"\}`)

	first := series.FindAllStringSubmatch(scrape(t, baseURL), -1)
	require.Len(t, first, 2)
	assert.NotEqual(t, first[0][1], first[1][1], "the two GPUs must have distinct uuids")

	second := series.FindAllStringSubmatch(scrape(t, baseURL), -1)
	assert.Equal(t, first, second, "identities must be stable across scrapes")
}

// TestFluctuate proves the fake's --fluctuate mode moves metrics between
// scrapes while the identity stays put. The fake seeds from the wall clock on
// every invocation, so two scrapes draw independent jitter. A single metric
// could land on the same value twice (power draw has only ~800 formatted
// outcomes in its band), so the whole set of naturally-varying metrics is
// compared: all of them repeating at once is practically impossible.
func TestFluctuate(t *testing.T) {
	t.Parallel()

	baseURL := startExporter(t, "--nvidia-smi-command="+fakeCommand(defaultCapture(t), "--fluctuate"))

	moving := regexp.MustCompile(`^nvidia_smi_(power_draw(_instant)?_watts|temperature_gpu|fan_speed_ratio|` +
		`clocks_current_\w+|memory_used_bytes)\{`)
	uuid := regexp.MustCompile(`nvidia_smi_power_draw_watts\{uuid="([^"]+)"\}`)

	movingLines := func(metrics string) []string {
		var lines []string

		for line := range strings.Lines(metrics) {
			if moving.MatchString(line) {
				lines = append(lines, strings.TrimSpace(line))
			}
		}

		return lines
	}

	first := scrape(t, baseURL)
	second := scrape(t, baseURL)

	firstUUID := uuid.FindStringSubmatch(first)
	secondUUID := uuid.FindStringSubmatch(second)

	require.NotNil(t, firstUUID)
	require.NotNil(t, secondUUID)
	assert.Equal(t, firstUUID[1], secondUUID[1], "the uuid must not move")

	firstMoving := movingLines(first)
	require.NotEmpty(t, firstMoving)
	assert.NotEqual(t, firstMoving, movingLines(second), "the varying metrics should move between scrapes")
}

// TestGPURecoveryActionBadState drives gpu_recovery_action to "Reset" with the
// fake's --set flag, proving a non-zero recovery action flows through the enum
// transform and out as a metric. No real bad-GPU capture exists, so this is the
// end-to-end coverage for the unhealthy path the metric exists to catch.
func TestGPURecoveryActionBadState(t *testing.T) {
	t.Parallel()

	baseURL := startExporter(t, "--nvidia-smi-command="+
		fakeCommand(defaultCapture(t), "--set", "gpu_recovery_action=Reset"))

	assert.Regexp(t, `nvidia_smi_gpu_recovery_action\{uuid="[^"]+"\} 1\b`, scrape(t, baseURL))
}

// TestCachedModeMatchesLive proves background collection serves the same
// deterministic content as the synchronous mode, pinned by the same expected output file.
func TestCachedModeMatchesLive(t *testing.T) {
	if *update {
		t.Skip("expected outputs are being regenerated in this run")
	}

	t.Parallel()

	baseURL := startExporter(t,
		"--nvidia-smi-command="+fakeCommand(defaultCapture(t)),
		"--collect.compute-apps",
		"--collect.interval=100ms")

	want, err := os.ReadFile(filepath.Join("testdata",
		"linux-x86_64__nvidia-geforce-rtx-2080-super__595.71.05__idle.metrics"))
	require.NoError(t, err)

	assert.Equal(t, string(want), filterDeterministic(scrape(t, baseURL)))
}

// TestFieldExclusion proves an excluded field's family disappears while the
// rest stays.
func TestFieldExclusion(t *testing.T) {
	t.Parallel()

	baseURL := startExporter(t,
		"--nvidia-smi-command="+fakeCommand(defaultCapture(t)),
		"--query-field-names-exclude=temperature.*")

	metrics := scrape(t, baseURL)
	assert.NotContains(t, metrics, "nvidia_smi_temperature_gpu")
	assert.Contains(t, metrics, "nvidia_smi_utilization_gpu")
}

// TestExplicitFieldNames covers the explicit field list path, where the
// identity fields are appended to whatever the user asks for.
func TestExplicitFieldNames(t *testing.T) {
	t.Parallel()

	baseURL := startExporter(t,
		"--nvidia-smi-command="+fakeCommand(defaultCapture(t)),
		"--query-field-names=temperature.gpu,uuid")

	metrics := scrape(t, baseURL)
	assert.Contains(t, metrics, "nvidia_smi_temperature_gpu")
	assert.Contains(t, metrics, "nvidia_smi_gpu_info")
	assert.NotContains(t, metrics, "nvidia_smi_utilization_gpu")
}

// TestExplicitUnknownFieldFailsStartup pins the documented contract: an
// explicit field list the setup cannot satisfy fails instead of being
// silently replaced.
func TestExplicitUnknownFieldFailsStartup(t *testing.T) {
	t.Parallel()

	err := app.Run(t.Context(), []string{
		"--web.listen-address=127.0.0.1:0",
		"--log.level=error",
		"--nvidia-smi-command=" + fakeCommand(defaultCapture(t)),
		"--query-field-names=temperature.gpu,bogus_field",
	}, app.Options{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus_field")
}

// TestFailingQueryKeepsServing proves a broken nvidia-smi never breaks the
// metrics endpoint itself: the failure is exported as content.
func TestFailingQueryKeepsServing(t *testing.T) {
	t.Parallel()

	baseURL := startExporter(t,
		"--nvidia-smi-command='"+fakeBin+"' --exit 7")

	metrics := scrape(t, baseURL)
	assert.Contains(t, metrics, "nvidia_smi_command_exit_code 7")
	assert.Contains(t, metrics, "nvidia_smi_failed_scrapes_total 1")
	assert.NotContains(t, metrics, "nvidia_smi_gpu_info")
}

// TestShutdownOnError proves the opt-in crash-on-failure mode makes the whole
// run return once a scrape fails.
func TestShutdownOnError(t *testing.T) {
	t.Parallel()

	listenCh := make(chan []net.Addr, 1)
	doneCh := make(chan error, 1)

	go func() {
		doneCh <- app.Run(t.Context(), []string{
			"--web.listen-address=127.0.0.1:0",
			"--log.level=error",
			"--nvidia-smi-command='" + fakeBin + "' --exit 7",
			"--shutdown-on-error",
		}, app.Options{
			OnListen: func(addrs []net.Addr) { listenCh <- addrs },
		})
	}()

	var baseURL string

	select {
	case addrs := <-listenCh:
		require.Len(t, addrs, 1)

		baseURL = "http://" + addrs[0].String()
	case <-time.After(startupTimeout):
		t.Fatal("timed out waiting for the exporter to listen")
	}

	// the scrape triggers the failing query; the response itself may complete
	// or be cut short by the shutdown, only the run's exit matters
	resp, err := http.Get(baseURL + "/metrics") //nolint:noctx
	if err == nil {
		resp.Body.Close()
	}

	select {
	case err = <-doneCh:
		require.Error(t, err)
	case <-time.After(startupTimeout):
		t.Fatal("timed out waiting for the exporter to shut down")
	}
}

// TestComputeAppsSoftFailure proves a failing per-process query suppresses
// the per-process series and flags the failure, without affecting the GPU
// metrics (the contract from the per-process metrics feature).
func TestComputeAppsSoftFailure(t *testing.T) {
	t.Parallel()

	baseURL := startExporter(t,
		"--nvidia-smi-command="+fakeCommand(defaultCapture(t), "--fail-arg", "--query-compute-apps"),
		"--collect.compute-apps")

	metrics := scrape(t, baseURL)
	assert.Contains(t, metrics, "nvidia_smi_compute_apps_last_collect_success 0")
	assert.NotContains(t, metrics, "nvidia_smi_compute_app_info")
	assert.Contains(t, metrics, "nvidia_smi_gpu_info")
	assert.Contains(t, metrics, "nvidia_smi_last_collect_success 1")
}
