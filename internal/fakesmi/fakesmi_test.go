package fakesmi_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/capture"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/fakesmi"
)

const capturesDir = "../captures"

// runFake runs the fake in-process, returning its exit code and outputs.
func runFake(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	code := fakesmi.Run(args, &stdout, &stderr)

	return code, stdout.String(), stderr.String()
}

// capturePaths returns all committed captures, so every corpus file exercises
// the replay logic without any per-capture code.
func capturePaths(t *testing.T) []string {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(capturesDir, "*.txt"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)

	return paths
}

// TestHelpQueryGPUVerbatim proves the field-detection call is served straight
// from the capture body.
func TestHelpQueryGPUVerbatim(t *testing.T) {
	t.Parallel()

	for _, path := range capturePaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			code, stdout, stderr := runFake(t, "--capture", path, "--help-query-gpu")
			require.Equal(t, 0, code, "stderr: %s", stderr)

			capt, err := capture.Load(path)
			require.NoError(t, err)

			section := capt.Find("capabilities", "help-query-gpu")
			require.NotNil(t, section)
			assert.Equal(t, section.Body+"\n", stdout)
		})
	}
}

// TestFullProjectionRoundTrip asks for exactly the recorded field list of
// each CSV query section and expects the recorded body back byte for byte, in
// every capture and state. This pins the projection to the corpus.
func TestFullProjectionRoundTrip(t *testing.T) {
	t.Parallel()

	for _, path := range capturePaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			capt, err := capture.Load(path)
			require.NoError(t, err)

			for _, section := range capt.Sections {
				fields, queryArg := recordedQuery(section.Command)
				if queryArg == "" {
					continue
				}

				code, stdout, stderr := runFake(t,
					"--capture", path, "--state", section.State,
					queryArg+"="+fields, "--format=csv")

				require.Equal(t, 0, code, "section %q/%q: stderr: %s", section.State, section.Label, stderr)
				assert.Equal(t, section.Body+"\n", stdout, "section %q/%q", section.State, section.Label)
			}
		})
	}
}

// recordedQuery extracts the query argument and field list from a recorded
// CSV query command line, returning an empty query for other sections.
func recordedQuery(command string) (string, string) {
	for token := range strings.FieldsSeq(command) {
		for _, queryArg := range []string{"--query-gpu", "--query-compute-apps"} {
			if fields, isQuery := strings.CutPrefix(token, queryArg+"="); isQuery {
				return fields, queryArg
			}
		}
	}

	return "", ""
}

// TestSubsetProjection asks for two fields in reverse order and checks the
// columns land where requested.
func TestSubsetProjection(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runFake(t,
		"--capture", filepath.Join(capturesDir, "linux-x86_64__nvidia-geforce-rtx-2080-super__595.71.05.txt"),
		"--query-gpu=name,uuid", "--format=csv")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.Len(t, lines, 2)

	assert.Equal(t, "name, uuid", lines[0])

	cells := strings.Split(lines[1], ", ")
	require.Len(t, cells, 2)
	assert.Contains(t, cells[0], "RTX 2080 SUPER")
	assert.True(t, strings.HasPrefix(cells[1], "GPU-"), "uuid cell: %q", cells[1])
}

// TestComputeAppsProjection asks for the exact four fields the exporter
// queries, which is a subset of the eight every capture records.
func TestComputeAppsProjection(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runFake(t,
		"--capture", filepath.Join(capturesDir, "linux-x86_64__nvidia-l40s__595.80.txt"),
		"--state", "load",
		"--query-compute-apps=gpu_uuid,pid,process_name,used_gpu_memory", "--format=csv")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.NotEmpty(t, lines)
	assert.Equal(t, "gpu_uuid, pid, process_name, used_gpu_memory [MiB]", lines[0])
	require.Greater(t, len(lines), 1, "load state should have at least one process")

	cells := strings.Split(lines[1], ", ")
	require.Len(t, cells, 4)
	assert.True(t, strings.HasPrefix(cells[0], "GPU-"), "gpu_uuid cell: %q", cells[0])
}

func TestUnknownFieldRejected(t *testing.T) {
	t.Parallel()

	code, stdout, _ := runFake(t,
		"--capture", filepath.Join(capturesDir, "linux-x86_64__nvidia-geforce-rtx-2080-super__595.71.05.txt"),
		"--query-gpu=uuid,definitely_not_a_field", "--format=csv")

	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, `"definitely_not_a_field" is not a valid field to query`)
}

// TestVerbatimSections spot-checks that invocations beyond the two CSV
// queries are served from their recorded sections.
func TestVerbatimSections(t *testing.T) {
	t.Parallel()

	path := filepath.Join(capturesDir, "linux-x86_64__nvidia-geforce-rtx-2080-super__595.71.05.txt")

	code, stdout, stderr := runFake(t, "--capture", path, "-L")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "GPU 0:")

	code, stdout, stderr = runFake(t, "--capture", path, "--version")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "NVIDIA-SMI version")

	// the default table, recorded with no arguments at all
	code, stdout, stderr = runFake(t, "--capture", path)
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "NVIDIA-SMI")

	// a stateful section resolves through the selected state
	code, stdoutIdle, _ := runFake(t, "--capture", path, "-q")
	require.Equal(t, 0, code)

	code, stdoutLoad, _ := runFake(t, "--capture", path, "--state", "load", "-q")
	require.Equal(t, 0, code)
	assert.NotEqual(t, stdoutIdle, stdoutLoad)
}

// TestEmbeddedCaptures proves --capture resolves embedded capture names (the
// .txt suffix optional) and that the default needs no repository checkout.
func TestEmbeddedCaptures(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runFake(t, "--capture", "linux-x86_64__nvidia-l40s__595.80", "-L")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "GPU 0:")

	code, _, stderr = runFake(t, "--capture", "linux-x86_64__nvidia-l40s__595.80.txt", "-L")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	// no --capture at all: the embedded default answers
	code, stdout, stderr = runFake(t, "--help-query-gpu")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotEmpty(t, stdout)

	code, _, stderr = runFake(t, "--capture", "no-such-capture", "-L")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "neither a file on disk nor an embedded capture")
}

func TestFailureInjection(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runFake(t, "--exit", "9", "--stderr-msg", "injected failure", "-L")
	assert.Equal(t, 9, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "injected failure")
}

// TestSelectiveFailureInjection proves one query can be broken while the
// others keep answering, which is how the exporter's soft-failure paths get
// exercised.
func TestSelectiveFailureInjection(t *testing.T) {
	t.Parallel()

	path := filepath.Join(capturesDir, "linux-x86_64__nvidia-geforce-rtx-2080-super__595.71.05.txt")

	code, _, stderr := runFake(t, "--capture", path, "--fail-arg", "--query-compute-apps",
		"--query-compute-apps=gpu_uuid,pid,process_name,used_gpu_memory", "--format=csv")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "injected failure")

	code, _, stderr = runFake(t, "--capture", path, "--fail-arg", "--query-compute-apps",
		"--query-gpu=uuid", "--format=csv")
	assert.Equal(t, 0, code, "stderr: %s", stderr)
}

func TestErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
	}{
		{
			name: "unknown invocation",
			args: []string{"--totally-unknown-flag"},
		},
		{
			name: "missing capture file",
			args: []string{"--capture", "does-not-exist.txt", "-L"},
		},
		{
			name: "invalid exit value",
			args: []string{"--exit", "nope", "-L"},
		},
		{
			name: "invalid delay value",
			args: []string{"--delay", "nope", "-L"},
		},
		{
			name: "flag without value",
			args: []string{"--capture"},
		},
		{
			name: "unknown state",
			args: []string{"--state", "nope", "--query-gpu=uuid", "--format=csv"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			args := test.args
			if test.name != "missing capture file" && test.name != "flag without value" {
				args = append([]string{"--capture", filepath.Join(capturesDir,
					"linux-x86_64__nvidia-geforce-rtx-2080-super__595.71.05.txt")}, args...)
			}

			code, _, stderr := runFake(t, args...)
			assert.Equal(t, 2, code)
			assert.NotEmpty(t, stderr)
		})
	}
}
