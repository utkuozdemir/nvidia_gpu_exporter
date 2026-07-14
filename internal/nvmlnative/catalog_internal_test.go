package nvmlnative

import (
	"regexp"
	"strings"
	"testing"

	"github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/captures"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

// TestCatalogHeadersMatchCapture proves the property the whole backend hangs
// on: for every catalogued field, the returned-header string (which the
// exporter turns into the metric name and unit multiplier) is byte-identical
// to what a real nvidia-smi prints. The oracle is the H100 capture, taken on
// the same driver generation the catalog was verified against.
func TestCatalogHeadersMatchCapture(t *testing.T) {
	t.Parallel()

	qFields, rFields := captureQueryGPUHeader(t, "linux-x86_64__nvidia-h100-80gb-hbm3__590.48.01.txt")
	require.Len(t, rFields, len(qFields))

	verified := 0

	for fieldIdx, qField := range qFields {
		rField, ok := catalogRFields[nvidiasmi.QField(qField)]
		if !ok {
			// fields outside the catalog vocabulary (vGPU, power smoothing...)
			// are legitimately absent; they read [N/A] in exec mode anyway
			continue
		}

		assert.Equal(t, rFields[fieldIdx], string(rField), "returned header for %q", qField)

		verified++
	}

	// the capture must actually cover the catalog, otherwise this test
	// silently proves nothing
	require.Greater(t, verified, 170)
}

// TestCatalogOrderAndMapAgree pins the catalog's internal consistency: every
// ordered field has a header and vice versa.
func TestCatalogOrderAndMapAgree(t *testing.T) {
	t.Parallel()

	require.Len(t, catalogRFields, len(fieldOrder))

	for _, q := range fieldOrder {
		require.Contains(t, catalogRFields, q)
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()

	logger := slogt.New(t)

	t.Run("auto selects the full catalog", func(t *testing.T) {
		t.Parallel()

		resolved, err := Resolve("AUTO", "", "590.48.01", logger)
		require.NoError(t, err)
		assert.Len(t, resolved.Query, len(fieldOrder))
		assert.Equal(t, nvidiasmi.RField("power.draw [W]"), resolved.Returned["power.draw"])
	})

	t.Run("explicit list keeps identity fields", func(t *testing.T) {
		t.Parallel()

		resolved, err := Resolve("power.draw,temperature.gpu", "", "590.48.01", logger)
		require.NoError(t, err)
		assert.Contains(t, resolved.Query, nvidiasmi.UUIDQField)
		assert.Contains(t, resolved.Query, nvidiasmi.QField("power.draw"))
	})

	t.Run("unknown field fails loudly", func(t *testing.T) {
		t.Parallel()

		_, err := Resolve("power.draw,no.such.field", "", "590.48.01", logger)
		require.ErrorContains(t, err, "no.such.field")
	})

	t.Run("legacy driver spells throttle reasons", func(t *testing.T) {
		t.Parallel()

		resolved, err := Resolve("AUTO", "", "550.54.14", logger)
		require.NoError(t, err)
		assert.Contains(t, resolved.Query, nvidiasmi.QField("clocks_throttle_reasons.active"))
		assert.NotContains(t, resolved.Query, nvidiasmi.QField("clocks_event_reasons.active"))
		assert.Equal(t, nvidiasmi.RField("clocks_throttle_reasons.active"),
			resolved.Returned["clocks_throttle_reasons.active"])
	})

	t.Run("explicit lists accept both spellings", func(t *testing.T) {
		t.Parallel()

		resolved, err := Resolve(
			"clocks_throttle_reasons.active,clocks_event_reasons.active", "", "590.48.01", logger)
		require.NoError(t, err)
		assert.Contains(t, resolved.Query, nvidiasmi.QField("clocks_throttle_reasons.active"))
		// the alias resolves to the same collector output as the canonical field
		assert.Equal(t, nvidiasmi.QField("clocks_event_reasons.active"),
			canonicalQField("clocks_throttle_reasons.active"))
		assert.Equal(t, nvidiasmi.QField("power.draw"), canonicalQField("power.draw"))
	})

	t.Run("exclusions drop fields", func(t *testing.T) {
		t.Parallel()

		resolved, err := Resolve("AUTO", "ecc.errors.*", "590.48.01", logger)
		require.NoError(t, err)
		assert.NotContains(t, resolved.Query, nvidiasmi.QField("ecc.errors.corrected.volatile.total"))
		assert.Contains(t, resolved.Query, nvidiasmi.QField("power.draw"))
	})
}

var queryGPUCommandRegex = regexp.MustCompile(`(?m)^# \$ nvidia-smi --query-gpu=(\S+) --format=csv\s*$`)

// captureQueryGPUHeader extracts the query fields and the returned CSV header
// row of the first query-gpu section from an embedded capture.
func captureQueryGPUHeader(t *testing.T, name string) ([]string, []string) {
	t.Helper()

	var qFields, rFields []string

	data, err := captures.FS.ReadFile(name)
	require.NoError(t, err)

	lines := strings.Split(string(data), "\n")

	for lineIdx, line := range lines {
		match := queryGPUCommandRegex.FindStringSubmatch(line)
		if match == nil {
			continue
		}

		qFields = strings.Split(match[1], ",")

		for j := lineIdx + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "#") || lines[j] == "" {
				continue
			}

			for cell := range strings.SplitSeq(lines[j], ", ") {
				rFields = append(rFields, strings.TrimSpace(cell))
			}

			return qFields, rFields
		}
	}

	t.Fatalf("no query-gpu section found in capture %s", name)

	return nil, nil
}
