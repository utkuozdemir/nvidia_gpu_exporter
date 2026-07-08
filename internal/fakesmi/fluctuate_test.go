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

const rtx2080Capture = "linux-x86_64__nvidia-geforce-rtx-2080-super__595.71.05"

// numericPart splits a cell like "39.32 W" into its number and unit suffix.
func numericPart(t *testing.T, cell string) (float64, string) {
	t.Helper()

	token, suffix, _ := strings.Cut(cell, " ")

	v, err := strconv.ParseFloat(token, 64)
	require.NoError(t, err, "cell %q", cell)

	return v, suffix
}

// TestFluctuateMovesAndReproduces proves a whitelisted field moves between
// runs with different seeds and reproduces with the same seed.
func TestFluctuateMovesAndReproduces(t *testing.T) {
	t.Parallel()

	one := dataRows(t, "temperature.gpu", "--capture", rtx2080Capture, "--fluctuate", "--seed", "1")
	two := dataRows(t, "temperature.gpu", "--capture", rtx2080Capture, "--fluctuate", "--seed", "2")
	oneAgain := dataRows(t, "temperature.gpu", "--capture", rtx2080Capture, "--fluctuate", "--seed", "1")

	assert.Equal(t, one, oneAgain, "same seed must reproduce")
	assert.NotEqual(t, one, two, "different seeds must differ")
}

// TestFluctuateBoundsAndShape proves jittered values stay within the ±10%
// (min ±1) band around the captured value, keep the unit suffix, and keep the
// captured decimal shape.
func TestFluctuateBoundsAndShape(t *testing.T) {
	t.Parallel()

	// captured: power.draw "39.32 W", temperature.gpu "40", fan.speed "38 %"
	for seed := range 20 {
		args := []string{"--capture", rtx2080Capture, "--fluctuate", "--seed", strconv.Itoa(seed)}

		power, unit := numericPart(t, dataRows(t, "power.draw", args...)[0])
		assert.Equal(t, "W", unit)
		assert.InDelta(t, 39.32, power, 3.932+0.001)
		assert.GreaterOrEqual(t, power, 0.0)

		temp, _ := numericPart(t, dataRows(t, "temperature.gpu", args...)[0])
		assert.InDelta(t, 40, temp, 4.0+0.5) // integer shape rounds the ±4 band

		fan, unit := numericPart(t, dataRows(t, "fan.speed", args...)[0])
		assert.Equal(t, "%", unit)
		assert.InDelta(t, 38, fan, 3.8+0.5)
	}
}

// TestFluctuatePercentClamp uses the L40S idle capture, which genuinely
// records 100% GPU utilization, to prove a percentage never exceeds 100.
func TestFluctuatePercentClamp(t *testing.T) {
	t.Parallel()

	for seed := range 30 {
		cell := dataRows(t, "utilization.gpu",
			"--capture", "linux-x86_64__nvidia-l40s__595.80", "--fluctuate", "--seed", strconv.Itoa(seed))[0]
		v, unit := numericPart(t, cell)
		assert.Equal(t, "%", unit)
		assert.LessOrEqual(t, v, 100.0, "cell %q", cell)
		assert.GreaterOrEqual(t, v, 90.0-0.5, "cell %q", cell)
	}
}

// TestFluctuateLeavesUnavailableAndFixedFields proves an unavailable cell
// never gains a number (H200-MIG reports fan speed and utilization as [N/A])
// and a non-whitelisted field stays byte-identical.
func TestFluctuateLeavesUnavailableAndFixedFields(t *testing.T) {
	t.Parallel()

	args := []string{"--capture", "linux-x86_64__nvidia-h200-mig__590.48.01", "--fluctuate", "--seed", "7"}

	assert.Equal(t, "[N/A]", dataRows(t, "fan.speed", args...)[0])
	assert.Equal(t, "[N/A]", dataRows(t, "utilization.gpu", args...)[0])

	// capacities, enum states and identity never move
	assert.Equal(t, "143771 MiB", dataRows(t, "memory.total", args...)[0])
	assert.Equal(t, "Enabled", dataRows(t, "mig.mode.current", args...)[0])
	assert.Equal(t, "GPU-00000000-0000-0000-0000-000000000000", dataRows(t, "uuid", args...)[0])
}

// TestFluctuateOverridePrecedence proves an explicit override beats the
// fluctuation for its field while others keep moving.
func TestFluctuateOverridePrecedence(t *testing.T) {
	t.Parallel()

	rows := dataRows(t, "temperature.gpu",
		"--capture", rtx2080Capture, "--fluctuate", "--set", "temperature.gpu=42", "--seed", "3")
	assert.Equal(t, "42", rows[0])
}

// TestFluctuateMemoryConsistency proves the memory columns stay consistent
// after jittering: used never exceeds total minus reserved, and free is
// recomputed so the four values add up.
func TestFluctuateMemoryConsistency(t *testing.T) {
	t.Parallel()

	for seed := range 20 {
		code, stdout, stderr := runFake(t, "--capture", h200Capture, "--state", "load",
			"--fluctuate", "--seed", strconv.Itoa(seed),
			"--query-gpu=memory.total,memory.reserved,memory.used,memory.free", "--format=csv")
		require.Equal(t, 0, code, "stderr: %s", stderr)

		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		require.Len(t, lines, 2)

		cells := strings.Split(lines[1], ", ")
		require.Len(t, cells, 4)

		total, _ := numericPart(t, cells[0])
		reserved, _ := numericPart(t, cells[1])
		used, _ := numericPart(t, cells[2])
		free, _ := numericPart(t, cells[3])

		assert.LessOrEqual(t, used, total-reserved, "row %q", lines[1])
		assert.InDelta(t, total-reserved, used+free, 0.001, "row %q", lines[1])
	}
}

// TestFluctuateFlagForms covers the boolean flag's spellings: bare form,
// =BOOL form, separated BOOL form, and the flag switching off a config's
// fluctuate.
func TestFluctuateFlagForms(t *testing.T) {
	t.Parallel()

	baseline := dataRows(t, "temperature.gpu", "--capture", rtx2080Capture)

	// bare form followed directly by the query: must not swallow it
	moved := dataRows(t, "temperature.gpu", "--capture", rtx2080Capture, "--fluctuate", "--seed", "12")
	require.Len(t, moved, 1)

	// separated bool value
	separated := dataRows(t, "temperature.gpu", "--capture", rtx2080Capture, "--fluctuate", "false")
	assert.Equal(t, baseline, separated)

	// =BOOL form disabling a config's fluctuate: output matches plain replay
	cfg := writeConfig(t, "capture: "+rtx2080Capture+"\nfluctuate: true\n")
	disabled := dataRows(t, "temperature.gpu", "--config", cfg, "--fluctuate=false")
	assert.Equal(t, baseline, disabled)

	// the config alone enables it: power draw carries two decimals, so the
	// jittered value differs from the captured one
	assert.NotEqual(t, dataRows(t, "power.draw", "--capture", rtx2080Capture),
		dataRows(t, "power.draw", "--config", cfg, "--seed", "8"))

	// a non-bool value is rejected
	code, _, stderr := runFake(t, "--capture", rtx2080Capture, "--fluctuate=nope", "-L")
	assert.Equal(t, 2, code)
	assert.NotEmpty(t, stderr)
}

// TestFluctuateMemoryOverridePrecedence proves the memory reconciliation
// never runs over an explicitly overridden memory cell, and that it computes
// against an overridden total.
func TestFluctuateMemoryOverridePrecedence(t *testing.T) {
	t.Parallel()

	memoryQuery := func(sets ...string) []string {
		t.Helper()

		args := make([]string, 0, 7+2*len(sets))
		args = append(args, "--capture", rtx2080Capture, "--fluctuate", "--seed", "6")

		for _, set := range sets {
			args = append(args, "--set", set)
		}

		code, stdout, stderr := runFake(t,
			append(args, "--query-gpu=memory.total,memory.reserved,memory.used,memory.free", "--format=csv")...)
		require.Equal(t, 0, code, "stderr: %s", stderr)

		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		require.Len(t, lines, 2)

		return strings.Split(lines[1], ", ")
	}

	// an overridden used is served verbatim and free stays as captured,
	// instead of being recomputed against the override
	cells := memoryQuery("memory.used=999999")
	assert.Equal(t, "999999", cells[2])
	assert.Equal(t, "7571 MiB", cells[3])

	// an overridden free is served verbatim while used still jitters
	cells = memoryQuery("memory.free=1234")
	assert.NotEqual(t, "214 MiB", cells[2])
	assert.Equal(t, "1234", cells[3])

	// an overridden total feeds the recomputation of free
	cells = memoryQuery("memory.total=100000")
	used, _ := numericPart(t, cells[2])
	free, _ := numericPart(t, cells[3])
	assert.InDelta(t, 100000-408, used+free, 0.001)
}

// TestFluctuateCellGrammar proves a cell fluctuates when its number is a
// plain decimal prefix — a unit glued on with no space included, as the
// exporter itself tolerates — while an exponent-form cell, which the
// exporter rejects, is never rewritten into something it would accept.
func TestFluctuateCellGrammar(t *testing.T) {
	t.Parallel()

	fence := strings.Repeat("#", 80)
	capture := fence + `
# idle :: query-gpu (csv, what the exporter parses)
# $ nvidia-smi --query-gpu=uuid,temperature.gpu,power.draw,fan.speed --format=csv
` + fence + `
uuid, temperature.gpu, power.draw, fan.speed
GPU-1, 40%, 39.32W, 1e3 %
`

	path := filepath.Join(t.TempDir(), "glued-units.txt")
	require.NoError(t, os.WriteFile(path, []byte(capture), 0o600))

	code, stdout, stderr := runFake(t, "--capture", path, "--fluctuate", "--seed", "3",
		"--query-gpu=temperature.gpu,power.draw,fan.speed", "--format=csv")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.Len(t, lines, 2)

	cells := strings.Split(lines[1], ", ")
	require.Len(t, cells, 3)

	temperature, ok := strings.CutSuffix(cells[0], "%")
	assert.True(t, ok, "glued unit must be preserved verbatim, got %q", cells[0])

	v, err := strconv.ParseFloat(temperature, 64)
	require.NoError(t, err, "cell %q", cells[0])
	assert.InDelta(t, 40, v, 4.5)
	assert.NotEqual(t, "40%", cells[0], "a glued-unit cell must still fluctuate")

	power, ok := strings.CutSuffix(cells[1], "W")
	assert.True(t, ok, "glued unit must be preserved verbatim, got %q", cells[1])

	_, err = strconv.ParseFloat(power, 64)
	require.NoError(t, err, "cell %q", cells[1])
	assert.NotEqual(t, "39.32W", cells[1])

	assert.Equal(t, "1e3 %", cells[2],
		"an exponent cell the exporter rejects must never become a value it accepts")
}

// TestFluctuateComputeApps proves per-process used memory moves while pids
// and names stay fixed.
func TestFluctuateComputeApps(t *testing.T) {
	t.Parallel()

	query := []string{"--query-compute-apps=pid,process_name,used_gpu_memory", "--format=csv"}
	base := []string{"--capture", "linux-x86_64__nvidia-l40s__595.80", "--state", "load"}

	code, plain, stderr := runFake(t, append(base, query...)...)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	code, moved, stderr := runFake(t, append(append(base, "--fluctuate", "--seed", "4"), query...)...)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	plainLines := strings.Split(strings.TrimSpace(plain), "\n")
	movedLines := strings.Split(strings.TrimSpace(moved), "\n")
	require.Len(t, movedLines, len(plainLines))
	require.Greater(t, len(plainLines), 1)

	for i := 1; i < len(plainLines); i++ {
		plainCells := strings.Split(plainLines[i], ", ")
		movedCells := strings.Split(movedLines[i], ", ")
		assert.Equal(t, plainCells[0], movedCells[0], "pid must not move")
		assert.Equal(t, plainCells[1], movedCells[1], "process name must not move")
		assert.NotEqual(t, plainCells[2], movedCells[2], "used memory should move")
	}
}
