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
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvmlnative"
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

// httpGet fetches a URL and returns the status code and body, with optional
// header key-value pairs.
func httpGet(t *testing.T, url string, headers ...string) (int, string) {
	t.Helper()

	require.Zero(t, len(headers)%2, "headers must be key/value pairs")

	ctx, cancel := context.WithTimeout(t.Context(), startupTimeout)
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)

	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, string(body)
}

// scrape fetches the metrics page.
func scrape(t *testing.T, baseURL string) string {
	t.Helper()

	status, body := httpGet(t, baseURL+"/metrics")
	require.Equal(t, http.StatusOK, status)

	return body
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
// deterministic content as the synchronous mode, pinned by the same expected
// output file. Scrapes do not wait for the first background collection, so
// the test polls until it has been served.
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

	var got string

	require.Eventually(t, func() bool {
		got = filterDeterministic(scrape(t, baseURL))

		return strings.Contains(got, "nvidia_smi_gpu_info")
	}, startupTimeout, 50*time.Millisecond)

	assert.Equal(t, string(want), got)
}

// TestCachedModeFirstScrapeDoesNotBlock proves a scrape arriving while the
// very first background collection is still running gets an immediate answer
// reporting the not-yet-collected state, instead of hanging on the
// collection. The fake's delay is what the scrape must not wait for.
func TestCachedModeFirstScrapeDoesNotBlock(t *testing.T) {
	t.Parallel()

	delay := 5 * time.Second

	baseURL := startExporter(t,
		"--nvidia-smi-command="+fakeCommand(defaultCapture(t), "--delay", delay.String()),
		"--collect.interval=50ms")

	start := time.Now()
	metrics := scrape(t, baseURL)
	elapsed := time.Since(start)

	assert.Less(t, elapsed, delay, "the scrape must not wait for the in-flight collection")
	assert.Contains(t, metrics, "nvidia_smi_last_collect_success 0")
	assert.Contains(t, metrics, "nvidia_smi_failed_scrapes_total 0")
	assert.NotContains(t, metrics, "nvidia_smi_gpu_info")
	// no made-up exit code or duration before the first collection completes
	assert.NotContains(t, metrics, "nvidia_smi_command_exit_code")
	assert.NotContains(t, metrics, "nvidia_smi_last_collect_duration_seconds ")
}

// TestRoutingAndHealth pins the HTTP surface: the landing page only on the
// exact root path, 404 for unknown paths, and the process-level health
// endpoints.
func TestRoutingAndHealth(t *testing.T) {
	t.Parallel()

	baseURL := startExporter(t, "--nvidia-smi-command="+fakeCommand(defaultCapture(t)))

	status, body := httpGet(t, baseURL+"/")
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "Nvidia GPU Exporter")
	assert.Contains(t, body, `href="/metrics"`)

	status, _ = httpGet(t, baseURL+"/-/healthy")
	assert.Equal(t, http.StatusOK, status)

	status, _ = httpGet(t, baseURL+"/-/ready")
	assert.Equal(t, http.StatusOK, status)

	// the handler's own error counter is registered and exposed
	assert.Contains(t, scrape(t, baseURL), "promhttp_metric_handler_errors_total")

	status, _ = httpGet(t, baseURL+"/no-such-path")
	assert.Equal(t, http.StatusNotFound, status)

	// pprof is not enabled, so its paths are unknown too
	status, _ = httpGet(t, baseURL+"/debug/pprof/")
	assert.Equal(t, http.StatusNotFound, status)
}

// TestTelemetryPathValidation pins the startup validation of the telemetry
// path: values that would collide with the exporter's own routes or break
// the route registration fail cleanly instead of panicking or shadowing.
func TestTelemetryPathValidation(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/", "metrics", "/-/healthy", "/debug/pprof/heap", "/metrics/", "/met rics"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			err := app.Run(t.Context(), []string{
				"--web.listen-address=127.0.0.1:0",
				"--log.level=error",
				"--nvidia-smi-command=" + fakeCommand(defaultCapture(t)),
				"--web.telemetry-path=" + path,
			}, app.Options{})

			require.Error(t, err)
			assert.Contains(t, err.Error(), "web.telemetry-path")
		})
	}
}

// TestCustomTelemetryPath proves a valid custom telemetry path serves the
// metrics there and nothing on the default path.
func TestCustomTelemetryPath(t *testing.T) {
	t.Parallel()

	baseURL := startExporter(t,
		"--nvidia-smi-command="+fakeCommand(defaultCapture(t)),
		"--web.telemetry-path=/custom-metrics")

	status, body := httpGet(t, baseURL+"/custom-metrics")
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "nvidia_smi_gpu_info")

	status, _ = httpGet(t, baseURL+"/metrics")
	assert.Equal(t, http.StatusNotFound, status)
}

// TestMaxRequestsLimit proves scrapes beyond the concurrency limit are
// answered with an immediate 503 instead of queueing behind the slow
// collection holding the only slot.
func TestMaxRequestsLimit(t *testing.T) {
	t.Parallel()

	baseURL := startExporter(t,
		"--nvidia-smi-command="+fakeCommand(defaultCapture(t), "--delay", "3s"),
		"--web.max-requests=1")

	first := make(chan int, 1)

	go func() {
		// plain client instead of the helper: test assertions must not run
		// off the test goroutine
		resp, err := http.Get(baseURL + "/metrics") //nolint:noctx
		if err != nil {
			first <- 0

			return
		}

		resp.Body.Close()

		first <- resp.StatusCode
	}()

	// give the first scrape time to occupy the only slot
	time.Sleep(500 * time.Millisecond)

	start := time.Now()
	status, body := httpGet(t, baseURL+"/metrics")

	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Contains(t, body, "limit of 1 concurrent requests")
	assert.Less(t, time.Since(start), 2*time.Second, "the rejection must be immediate, not queued")

	// the health endpoints are not behind the limit
	healthStatus, _ := httpGet(t, baseURL+"/-/healthy")
	assert.Equal(t, http.StatusOK, healthStatus)

	assert.Equal(t, http.StatusOK, <-first, "the scrape holding the slot must still succeed")
}

// TestConcurrentScrapesShareOneCollection proves the single-flight sharing
// through the real HTTP path: several scrapes fired at once against a slow
// collection all succeed in roughly one collection's time, not one
// collection each, and all serve the same snapshot (pinned by the wall-clock
// families, which differ between distinct collections).
func TestConcurrentScrapesShareOneCollection(t *testing.T) {
	t.Parallel()

	const scrapers = 5

	delay := 2 * time.Second

	baseURL := startExporter(t,
		"--nvidia-smi-command="+fakeCommand(defaultCapture(t), "--delay", delay.String()))

	bodies := make(chan string, scrapers)

	start := time.Now()

	for range scrapers {
		go func() {
			// plain client, test assertions must not run off the test goroutine
			resp, err := http.Get(baseURL + "/metrics") //nolint:noctx
			if err != nil {
				bodies <- "error: " + err.Error()

				return
			}

			defer resp.Body.Close()

			body, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				bodies <- "error: " + readErr.Error()

				return
			}

			bodies <- string(body)
		}()
	}

	timestamps := make(map[string]bool)

	for range scrapers {
		body := <-bodies
		require.NotContains(t, body, "error: ")
		assert.Contains(t, body, "nvidia_smi_last_collect_success 1")

		match := regexp.MustCompile(`(?m)^nvidia_smi_last_collect_success_timestamp_seconds .*$`).
			FindString(body)
		require.NotEmpty(t, match)

		timestamps[match] = true
	}

	elapsed := time.Since(start)

	assert.Len(t, timestamps, 1, "all concurrent scrapes must serve the same collection's snapshot")
	assert.Less(t, elapsed, time.Duration(scrapers-1)*delay,
		"the scrapes must not have run one collection each, serialized")
}

// TestShutdownDuringInFlightScrape proves shutting the exporter down while a
// synchronous scrape is mid-collection cancels that collection and exits
// promptly and cleanly, instead of waiting out the collection behind the
// shutdown grace period.
func TestShutdownDuringInFlightScrape(t *testing.T) {
	t.Parallel()

	delay := 6 * time.Second

	ctx, cancel := context.WithCancel(t.Context())

	listenCh := make(chan []net.Addr, 1)
	doneCh := make(chan error, 1)

	go func() {
		doneCh <- app.Run(ctx, []string{
			"--web.listen-address=127.0.0.1:0",
			"--log.level=error",
			"--nvidia-smi-command=" + fakeCommand(defaultCapture(t), "--delay", delay.String()),
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

	scrapeDone := make(chan struct{})

	go func() {
		defer close(scrapeDone)

		// the response may complete or be cut short by the shutdown, only
		// the run's exit matters; plain client, since test assertions must
		// not run off the test goroutine
		resp, err := http.Get(baseURL + "/metrics") //nolint:noctx
		if err == nil {
			resp.Body.Close()
		}
	}()

	// let the scrape reach the collection, then shut down mid-collection
	time.Sleep(time.Second)

	shutdownStart := time.Now()

	cancel()

	select {
	case err := <-doneCh:
		require.NoError(t, err, "shutdown during an in-flight scrape must be clean")
		assert.Less(t, time.Since(shutdownStart), delay-2*time.Second,
			"shutdown must not wait out the in-flight collection")
	case <-time.After(startupTimeout):
		t.Fatal("timed out waiting for the exporter to shut down")
	}

	<-scrapeDone
}

// TestScrapeTimeoutHeaderBoundsCollection proves the timeout Prometheus
// advertises on the scrape bounds the collection: when it fires, the scrape
// is answered right away with the no-result state instead of waiting out the
// slow collection.
func TestScrapeTimeoutHeaderBoundsCollection(t *testing.T) {
	t.Parallel()

	delay := 5 * time.Second

	baseURL := startExporter(t,
		"--nvidia-smi-command="+fakeCommand(defaultCapture(t), "--delay", delay.String()),
		// the startup field detection shares the fake's delay, so the
		// collection timeout must comfortably exceed it
		"--collect.timeout=20s",
		"--web.timeout-offset=100ms")

	start := time.Now()
	status, body := httpGet(t, baseURL+"/metrics", "X-Prometheus-Scrape-Timeout-Seconds", "1")
	elapsed := time.Since(start)

	assert.Equal(t, http.StatusOK, status)
	assert.Less(t, elapsed, delay, "the scrape must not wait out the collection")
	assert.Contains(t, body, "nvidia_smi_last_collect_success 0")
	assert.NotContains(t, body, "nvidia_smi_gpu_info")
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

// TestNVMLBackendRejectsCustomCommand pins the flag-validation contract: a
// custom nvidia-smi command signals intent (ssh wrappers, sudo) the nvml
// backend cannot honor, so combining them must fail at startup.
func TestNVMLBackendRejectsCustomCommand(t *testing.T) {
	t.Parallel()

	err := app.Run(t.Context(), []string{
		"--web.listen-address=127.0.0.1:0",
		"--log.level=error",
		"--collect.backend=nvml",
		"--nvidia-smi-command=" + fakeCommand(defaultCapture(t)),
	}, app.Options{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--nvidia-smi-command cannot be combined")
}

// TestExecBackendRejectsPcieThroughput pins the flag-validation contract in
// the other direction: the PCIe throughput counters only exist in the driver
// library, so requesting them with the exec backend must fail at startup.
func TestExecBackendRejectsPcieThroughput(t *testing.T) {
	t.Parallel()

	err := app.Run(t.Context(), []string{
		"--web.listen-address=127.0.0.1:0",
		"--log.level=error",
		"--collect.pcie-throughput",
		"--nvidia-smi-command=" + fakeCommand(defaultCapture(t)),
	}, app.Options{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--collect.pcie-throughput requires --collect.backend=nvml")
}

// TestNVMLBackendStartupFailureWithoutDriver pins the startup behavior on
// machines without a usable NVML setup: builds without the backend fail with
// the unavailability message, cgo builds fail initializing the absent
// library. Either way startup fails cleanly instead of serving empty data.
// On a machine with a working GPU driver this scenario does not apply, so
// the test skips (it still runs everywhere CI runs).
func TestNVMLBackendStartupFailureWithoutDriver(t *testing.T) {
	t.Parallel()

	err := app.Run(t.Context(), []string{
		"--web.listen-address=127.0.0.1:0",
		"--log.level=error",
		"--collect.backend=nvml",
	}, app.Options{})
	if err == nil {
		t.Skip("a working NVML driver is present; the no-driver startup path does not apply here")
	}

	require.ErrorContains(t, err, "failed to set up the nvml backend")

	if !nvmlnative.Available {
		assert.Contains(t, err.Error(), "not available in this build")
	}
}
