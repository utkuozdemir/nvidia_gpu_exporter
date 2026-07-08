package fakesmi_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// queryRows runs a query-gpu invocation and returns the data rows split into
// cells.
func queryRows(t *testing.T, fields string, args ...string) [][]string {
	t.Helper()

	full := append(append([]string{}, args...), "--query-gpu="+fields, "--format=csv")
	code, stdout, stderr := runFake(t, full...)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.Greater(t, len(lines), 1)

	rows := make([][]string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		rows = append(rows, strings.Split(line, ", "))
	}

	return rows
}

// TestGPUsReplication proves --gpus N replicates the captured GPU row with
// distinct, stable identities: uuid, index, count, and PCI addressing.
func TestGPUsReplication(t *testing.T) {
	t.Parallel()

	rows := queryRows(t, "uuid,index,count,pci.bus_id,pci.bus,name",
		"--capture", rtx2080Capture, "--gpus", "3")
	require.Len(t, rows, 3)

	uuids := map[string]bool{}

	for index, row := range rows {
		assert.True(t, strings.HasPrefix(row[0], "GPU-"), "uuid %q", row[0])
		assert.NotEqual(t, "GPU-00000000-0000-0000-0000-000000000000", row[0], "masked uuid must be replaced")
		uuids[row[0]] = true

		assert.Equal(t, strconv.Itoa(index), row[1], "index")
		assert.Equal(t, "3", row[2], "count")
		assert.Equal(t, "00000000:0"+strconv.Itoa(index+1)+":00.0", row[3], "pci.bus_id")
		assert.Equal(t, "0x0"+strconv.Itoa(index+1), row[4], "pci.bus")
		assert.Contains(t, row[5], "RTX 2080 SUPER", "name must replicate as captured")
	}

	assert.Len(t, uuids, 3, "uuids must be distinct")

	// identities must be stable across runs (and independent of the seed)
	again := queryRows(t, "uuid,index,count,pci.bus_id,pci.bus,name",
		"--capture", rtx2080Capture, "--gpus", "3", "--seed", "77")
	assert.Equal(t, rows, again)
}

// TestGPUsGeneratedUUIDsGolden pins the generated uuids to their exact
// values. They are an external contract: users' Prometheus series are keyed
// by them, so an accidental change to the derivation namespace would churn
// every series on an upgrade. A failure here means the derivation changed —
// that is a breaking change, not a test to update casually.
func TestGPUsGeneratedUUIDsGolden(t *testing.T) {
	t.Parallel()

	rows := queryRows(t, "uuid", "--capture", rtx2080Capture, "--gpus", "2")
	require.Len(t, rows, 2)

	assert.Equal(t, "GPU-1c67088e-9ecb-4564-bb97-6151ecc2ef64", rows[0][0])
	assert.Equal(t, "GPU-ef427e4f-1970-42f2-ab0c-ecf0e56a33b9", rows[1][0])

	// RFC 4122 shape: version nibble 4, variant bits 10
	for _, row := range rows {
		parts := strings.Split(strings.TrimPrefix(row[0], "GPU-"), "-")
		require.Len(t, parts, 5)
		assert.Equal(t, byte('4'), parts[2][0], "version nibble in %q", row[0])
		assert.Contains(t, "89ab", string(parts[3][0]), "variant nibble in %q", row[0])
	}
}

// TestGPUsBusIDHex proves the bus byte renders as uppercase two-digit hex
// once the count passes 9.
func TestGPUsBusIDHex(t *testing.T) {
	t.Parallel()

	rows := queryRows(t, "pci.bus_id,pci.bus", "--capture", rtx2080Capture, "--gpus", "10")
	require.Len(t, rows, 10)
	assert.Equal(t, "00000000:0A:00.0", rows[9][0])
	assert.Equal(t, "0x0A", rows[9][1])
}

// TestGPUsComputeApps proves the per-process rows are replicated once per
// GPU with the owning GPU's identity stamped on.
func TestGPUsComputeApps(t *testing.T) {
	t.Parallel()

	args := []string{"--capture", "linux-x86_64__nvidia-l40s__595.80", "--state", "load", "--gpus", "2"}

	gpuRows := queryRows(t, "uuid", args...)
	require.Len(t, gpuRows, 2)

	code, plain, stderr := runFake(t, "--capture", "linux-x86_64__nvidia-l40s__595.80", "--state", "load",
		"--query-compute-apps=gpu_uuid,gpu_bus_id,pid,process_name", "--format=csv")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	processes := len(strings.Split(strings.TrimSpace(plain), "\n")) - 1
	require.Positive(t, processes)

	code, stdout, stderr := runFake(t,
		append(args, "--query-compute-apps=gpu_uuid,gpu_bus_id,pid,process_name", "--format=csv")...)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.Len(t, lines, 1+2*processes, "each process must appear once per GPU")

	for i, line := range lines[1:] {
		cells := strings.Split(line, ", ")
		gpu := i / processes
		assert.Equal(t, gpuRows[gpu][0], cells[0], "gpu_uuid must match the owning GPU")
		assert.Equal(t, "00000000:0"+strconv.Itoa(gpu+1)+":00.0", cells[1], "gpu_bus_id")
	}
}

// TestGPUsComputeAppsSerial proves an explicit per-GPU serial reaches the
// compute-apps rows too.
func TestGPUsComputeAppsSerial(t *testing.T) {
	t.Parallel()

	cfg := writeConfig(t, `
capture: linux-x86_64__nvidia-l40s__595.80
state: load
gpus:
  - serial: "1111"
  - serial: "2222"
`)

	code, stdout, stderr := runFake(t, "--config", cfg,
		"--query-compute-apps=gpu_serial,pid", "--format=csv")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.Greater(t, len(lines), 2)

	processes := (len(lines) - 1) / 2
	for i, line := range lines[1:] {
		want := "1111"
		if i/processes == 1 {
			want = "2222"
		}

		assert.Equal(t, want, strings.Split(line, ", ")[0], "row %q", line)
	}
}

// TestGPUsHeaderOnlyComputeApps proves a capture state with no processes
// stays header-only under --gpus.
func TestGPUsHeaderOnlyComputeApps(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runFake(t, "--capture", rtx2080Capture, "--gpus", "4",
		"--query-compute-apps=gpu_uuid,pid,process_name,used_gpu_memory", "--format=csv")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Len(t, strings.Split(strings.TrimSpace(stdout), "\n"), 1)
}

// TestGPUsConfigEntries proves per-GPU config entries: explicit identity
// lands verbatim, per-GPU overrides hit only their GPU, top-level overrides
// hit every GPU, and an omitted uuid falls back to the generated one.
func TestGPUsConfigEntries(t *testing.T) {
	t.Parallel()

	cfg := writeConfig(t, `
capture: `+rtx2080Capture+`
overrides:
  compute_mode: Prohibited
gpus:
  - uuid: GPU-1937b558-347d-0f30-105b-893b98985668
    serial: "1320621059033"
    overrides:
      temperature.gpu: {min: 55, max: 80}
  - {}
`)

	rows := queryRows(t, "uuid,serial,temperature.gpu,compute_mode", "--config", cfg, "--seed", "9")
	require.Len(t, rows, 2)

	assert.Equal(t, "GPU-1937b558-347d-0f30-105b-893b98985668", rows[0][0])
	assert.Equal(t, "1320621059033", rows[0][1])

	hot, err := strconv.ParseFloat(rows[0][2], 64)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, hot, 55.0)
	assert.LessOrEqual(t, hot, 80.0)

	// the second GPU: generated uuid, captured serial, captured temperature
	assert.True(t, strings.HasPrefix(rows[1][0], "GPU-"))
	assert.NotEqual(t, rows[0][0], rows[1][0])
	assert.Equal(t, "[N/A]", rows[1][1], "omitted serial keeps the captured cell")
	assert.Equal(t, "40", rows[1][2], "no per-GPU override, no fluctuate: captured value")

	// the top-level override applies to both
	assert.Equal(t, "Prohibited", rows[0][3])
	assert.Equal(t, "Prohibited", rows[1][3])
}

// TestGPUsCountShorthand proves config `gpus: N` equals --gpus N, and the
// flag wins over the config's entries.
func TestGPUsCountShorthand(t *testing.T) {
	t.Parallel()

	scalar := writeConfig(t, "capture: "+rtx2080Capture+"\ngpus: 2\n")
	viaConfig := queryRows(t, "uuid,index,count", "--config", scalar)

	viaFlag := queryRows(t, "uuid,index,count", "--capture", rtx2080Capture, "--gpus", "2")
	assert.Equal(t, viaFlag, viaConfig)

	// the flag replaces the config's entries wholesale, explicit uuid included
	entries := writeConfig(t, "capture: "+rtx2080Capture+
		"\ngpus:\n  - uuid: GPU-1937b558-347d-0f30-105b-893b98985668\n")
	overridden := queryRows(t, "uuid", "--gpus", "1", "--config", entries)
	require.Len(t, overridden, 1)
	assert.NotEqual(t, "GPU-1937b558-347d-0f30-105b-893b98985668", overridden[0][0])
}

// TestGPUsIdentityOverridesRejected proves overrides on identity fields are
// rejected while gpus is active (they would collapse the series), from every
// layer, while staying allowed without gpus.
func TestGPUsIdentityOverridesRejected(t *testing.T) {
	t.Parallel()

	// flag override
	code, _, stderr := runFake(t, "--capture", rtx2080Capture, "--gpus", "2",
		"--set", "uuid=GPU-x", "--query-gpu=uuid", "--format=csv")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "identity")

	// top-level config override
	top := writeConfig(t, "capture: "+rtx2080Capture+"\ngpus: 2\noverrides:\n  pci.bus_id: mybus\n")
	code, _, stderr = runFake(t, "--config", top, "--query-gpu=uuid", "--format=csv")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "identity")

	// per-GPU override
	per := writeConfig(t, "capture: "+rtx2080Capture+"\ngpus:\n  - overrides:\n      index: {value: 5}\n")
	code, _, stderr = runFake(t, "--config", per, "--query-gpu=uuid", "--format=csv")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "identity")

	// without gpus, identity fields stay overridable
	assert.Equal(t, "GPU-x", dataRows(t, "uuid", "--capture", rtx2080Capture, "--set", "uuid=GPU-x")[0])
}

// TestGPUsValidation covers count bounds, duplicate uuids, and malformed
// entries.
func TestGPUsValidation(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"--gpus", "0"},
		{"--gpus", "-1"},
		{"--gpus", "256"},
		{"--gpus", "nope"},
	} {
		code, _, stderr := runFake(t, append(append([]string{"--capture", rtx2080Capture}, args...),
			"--query-gpu=uuid", "--format=csv")...)
		assert.Equalf(t, 2, code, "args %v should be rejected", args)
		assert.NotEmpty(t, stderr)
	}

	for _, body := range []string{
		"gpus: 0\n",
		"gpus: 256\n",
		"gpus: nope\n",
		"gpus: {uuid: x}\n",
		"gpus:\n  - uid: typo\n",
		// same uuid modulo case and prefix: duplicates after normalization
		"gpus:\n  - uuid: GPU-1937b558-347d-0f30-105b-893b98985668\n" +
			"  - uuid: 1937B558-347d-0f30-105b-893b98985668\n",
	} {
		cfg := writeConfig(t, "capture: "+rtx2080Capture+"\n"+body)
		code, _, stderr := runFake(t, "--config", cfg, "--query-gpu=uuid", "--format=csv")
		assert.Equalf(t, 2, code, "config %q should be rejected", body)
		assert.NotEmpty(t, stderr)
	}
}

// TestGPUsMultiRowCaptureRejected proves gpus refuses a capture whose
// query-gpu section already records several GPUs, for the compute-apps query
// too.
func TestGPUsMultiRowCaptureRejected(t *testing.T) {
	t.Parallel()

	fence := strings.Repeat("#", 80)
	capture := fence + `
# idle :: query-gpu (csv, what the exporter parses)
# $ nvidia-smi --query-gpu=uuid,name --format=csv
` + fence + `
uuid, name
GPU-1, a
GPU-2, b

` + fence + `
# idle :: query-compute-apps (per-process)
# $ nvidia-smi --query-compute-apps=gpu_uuid,pid --format=csv
` + fence + `
gpu_uuid, pid
`

	path := filepath.Join(t.TempDir(), "two-gpus.txt")
	require.NoError(t, os.WriteFile(path, []byte(capture), 0o600))

	// without gpus the two recorded rows replay fine
	code, stdout, stderr := runFake(t, "--capture", path, "--query-gpu=uuid", "--format=csv")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Len(t, strings.Split(strings.TrimSpace(stdout), "\n"), 3)

	for _, query := range []string{"--query-gpu=uuid", "--query-compute-apps=gpu_uuid,pid"} {
		code, _, stderr := runFake(t, "--capture", path, "--gpus", "2", query, "--format=csv")
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr, "single-GPU")
	}
}

// TestGPUsFluctuateCombined proves the two features compose: distinct GPUs
// move independently and reproducibly, and a per-GPU fixed override pins one
// GPU while the other keeps fluctuating.
func TestGPUsFluctuateCombined(t *testing.T) {
	t.Parallel()

	cfg := writeConfig(t, `
capture: `+rtx2080Capture+`
fluctuate: true
gpus:
  - {}
  - overrides:
      temperature.gpu: "55"
`)

	rows := queryRows(t, "uuid,temperature.gpu", "--config", cfg, "--seed", "42")
	require.Len(t, rows, 2)

	moving, err := strconv.ParseFloat(rows[0][1], 64)
	require.NoError(t, err)
	assert.InDelta(t, 40, moving, 4.5, "GPU 0 fluctuates around the captured 40")
	assert.Equal(t, "55", rows[1][1], "GPU 1 is pinned by its override")

	again := queryRows(t, "uuid,temperature.gpu", "--config", cfg, "--seed", "42")
	assert.Equal(t, rows, again, "a seed reproduces the combination")
}
