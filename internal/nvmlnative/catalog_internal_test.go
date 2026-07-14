package nvmlnative

import (
	"testing"

	"github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

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
