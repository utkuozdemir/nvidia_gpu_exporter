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

const h200Capture = "linux-x86_64__nvidia-h200__590.48.01"

// dataRows runs a query and returns the data cells of the requested single
// field, one entry per row (the header is dropped).
func dataRows(t *testing.T, field string, args ...string) []string {
	t.Helper()

	full := append(append([]string{}, args...), "--query-gpu="+field, "--format=csv")
	code, stdout, stderr := runFake(t, full...)
	require.Equal(t, 0, code, "stderr: %s", stderr)

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	require.GreaterOrEqual(t, len(lines), 2)

	out := make([]string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		out = append(out, strings.TrimSpace(line))
	}

	return out
}

func TestSetRangeWithinBounds(t *testing.T) {
	t.Parallel()

	for _, cell := range dataRows(t, "temperature.gpu",
		"--capture", h200Capture, "--seed", "3", "--set-range", "temperature.gpu=60:85") {
		v, err := strconv.ParseFloat(cell, 64)
		require.NoError(t, err, "cell %q", cell)
		assert.GreaterOrEqual(t, v, 60.0)
		assert.LessOrEqual(t, v, 85.0)
	}
}

// TestSetRangeSeedReproducible pins that a seed reproduces the values across
// runs even with several ranged fields (the field order must not matter).
func TestSetRangeSeedReproducible(t *testing.T) {
	t.Parallel()

	args := []string{
		"--capture", h200Capture, "--seed", "99",
		"--set-range", "temperature.gpu=10:90",
		"--set-range", "utilization.gpu=0:100",
		"--set-range", "power.draw=50:400",
	}
	// check two of the three ranged fields, so order-independence is exercised
	for _, field := range []string{"temperature.gpu", "power.draw"} {
		first := dataRows(t, field, args...)
		second := dataRows(t, field, args...)
		assert.Equalf(t, first, second, "same seed must reproduce %s", field)
	}
}

// TestSetRangeFormatting proves whole-number bounds yield an integer, fractional
// bounds yield fixed-point, and a wide range never emits exponent form (which
// the exporter's number extractor would reject).
func TestSetRangeFormatting(t *testing.T) {
	t.Parallel()

	intCell := dataRows(t, "temperature.gpu",
		"--capture", h200Capture, "--seed", "1", "--set-range", "temperature.gpu=60:85")[0]
	assert.NotContains(t, intCell, ".")

	floatCell := dataRows(t, "power.draw",
		"--capture", h200Capture, "--seed", "1", "--set-range", "power.draw=0.5:1.5")[0]
	assert.Contains(t, floatCell, ".")

	// a fractional wide range exercises the float branch on a large magnitude,
	// where a lazy formatter would switch to exponent notation
	wideCell := dataRows(t, "power.draw",
		"--capture", h200Capture, "--seed", "1", "--set-range", "power.draw=0.5:100000000.5")[0]
	assert.Contains(t, wideCell, ".")
	assert.NotContainsf(t, strings.ToLower(wideCell), "e", "must be fixed-point, got %q", wideCell)
}

// TestSetRangePerRowIndependent proves each data row draws its own value, so a
// multi-process (or multi-GPU) capture does not move in lockstep.
func TestSetRangePerRowIndependent(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runFake(t, "--capture", "linux-x86_64__nvidia-l40s__595.80",
		"--state", "load", "--seed", "5", "--set-range", "used_gpu_memory=1:1000000",
		"--query-compute-apps=pid,used_gpu_memory", "--format=csv")
	require.Equal(t, 0, code, "stderr: %s", stderr)

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.Greater(t, len(lines), 2, "need at least two process rows")

	mem := make(map[string]bool)
	for _, line := range lines[1:] {
		mem[strings.Split(line, ", ")[1]] = true
	}

	assert.Greater(t, len(mem), 1, "rows should draw independent values, got %v", mem)
}

func TestSetRangeInvalid(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{"temperature.gpu=notarange", "temperature.gpu=90:10", "=60:85", "x=a:b"} {
		code, _, stderr := runFake(t, "--capture", h200Capture, "--set-range", bad, "-L")
		assert.Equalf(t, 2, code, "--set-range %q should be rejected", bad)
		assert.NotEmpty(t, stderr)
	}
}

// writeConfig writes a temp yaml config and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "fake.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	return path
}

func TestConfigFile(t *testing.T) {
	t.Parallel()

	cfg := writeConfig(t, `
capture: `+h200Capture+`
state: load
overrides:
  gpu_recovery_action: Reset
  compute_mode: {value: Prohibited}
  temperature.gpu: {min: 70, max: 71}
`)

	// a scalar fixed value, an object fixed value, and a range in one section
	assert.Equal(t, "Reset", dataRows(t, "gpu_recovery_action", "--config", cfg, "--seed", "1")[0])
	assert.Equal(t, "Prohibited", dataRows(t, "compute_mode", "--config", cfg, "--seed", "1")[0])

	temp := dataRows(t, "temperature.gpu", "--config", cfg, "--seed", "1")[0]
	v, err := strconv.ParseFloat(temp, 64)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, v, 70.0)
	assert.LessOrEqual(t, v, 71.0)
}

// TestConfigFlagPrecedence proves a flag overrides the config for the same field.
func TestConfigFlagPrecedence(t *testing.T) {
	t.Parallel()

	cfg := writeConfig(t, "capture: "+h200Capture+"\noverrides:\n  temperature.gpu: {min: 10, max: 20}\n")

	// --set wins over the config's range for temperature.gpu, wherever --config sits
	cell := dataRows(t, "temperature.gpu", "--set", "temperature.gpu=42", "--config", cfg)[0]
	assert.Equal(t, "42", cell)
}

func TestConfigRejectsUnknownKey(t *testing.T) {
	t.Parallel()

	cfg := writeConfig(t, "captur: x\n") // typo
	code, _, stderr := runFake(t, "--config", cfg, "--query-gpu=uuid", "--format=csv")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "config")
}

// TestSetRangeFractionalBounds proves a narrow, sub-unit, or negative
// fractional range never rounds a value outside its bounds and never emits
// exponent form.
func TestSetRangeFractionalBounds(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		field    string
		min, max float64
	}{
		{"temperature.gpu=1.234:1.234", 1.234, 1.234},
		{"temperature.gpu=0.001:0.002", 0.001, 0.002},
		{"power.draw=-5.5:-5.0", -5.5, -5.0},
	} {
		field := strings.SplitN(tc.field, "=", 2)[0]
		for _, cell := range dataRows(t, field, "--capture", h200Capture, "--seed", "2", "--set-range", tc.field) {
			assert.NotContainsf(t, strings.ToLower(cell), "e", "no exponent, got %q", cell)

			v, err := strconv.ParseFloat(cell, 64)
			require.NoError(t, err, cell)
			assert.GreaterOrEqual(t, v, tc.min)
			assert.LessOrEqual(t, v, tc.max)
		}
	}
}

// TestConfigSeedReproducible proves a seed in the config reproduces the values.
func TestConfigSeedReproducible(t *testing.T) {
	t.Parallel()

	cfg := writeConfig(t, "capture: "+h200Capture+"\nseed: 55\noverrides:\n"+
		"  temperature.gpu: {min: 0, max: 1000}\n  power.draw: {min: 0, max: 500}\n")
	first := dataRows(t, "temperature.gpu", "--config", cfg)
	second := dataRows(t, "temperature.gpu", "--config", cfg)
	assert.Equal(t, first, second)
}

// TestConfigRejectsCommaValue proves a config value with a comma is rejected,
// matching the --set guard.
func TestConfigRejectsCommaValue(t *testing.T) {
	t.Parallel()

	cfg := writeConfig(t, "overrides:\n  process_name: {value: \"a,b\"}\n")
	code, _, stderr := runFake(t, "--capture", h200Capture, "--config", cfg, "--query-gpu=uuid", "--format=csv")
	assert.Equal(t, 2, code)
	assert.NotEmpty(t, stderr)
}

// TestConfigRejectsBadOverride covers the override-entry validation: a fixed
// value and a range are mutually exclusive, a range needs both bounds, and an
// unknown key inside an entry is rejected.
func TestConfigRejectsBadOverride(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"overrides:\n  power.draw: {value: 100, min: 1, max: 2}\n",
		"overrides:\n  power.draw: {min: 1}\n",
		"overrides:\n  power.draw: {mim: 1, max: 2}\n",
	} {
		cfg := writeConfig(t, body)
		code, _, stderr := runFake(t, "--capture", h200Capture, "--config", cfg, "--query-gpu=uuid", "--format=csv")
		assert.Equalf(t, 2, code, "config %q should be rejected", body)
		assert.NotEmpty(t, stderr)
	}
}
