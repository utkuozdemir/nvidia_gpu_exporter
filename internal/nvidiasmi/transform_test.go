package nvidiasmi_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

const delta = 1e-9

func assertFloat(t *testing.T, expected, actual float64) {
	t.Helper()

	assert.InDelta(t, expected, actual, delta)
}

func TestTransformRawValueValidValues(t *testing.T) {
	t.Parallel()

	expectedConversions := map[string]float64{
		"disabled":          0,
		"enabled":           1,
		"EnAbLeD":           1,
		"  enabled  ":       1,
		"default":           0,
		"exclusive_thread":  1,
		"prohibited":        2,
		"exclusive_process": 3,
		"0x1E240":           123456,
		"0x1e240":           123456,
		"P15":               15,
		"aaa1234.56bbb":     1234.56,
	}

	for raw, expected := range expectedConversions {
		val, err := nvidiasmi.TransformRawValue(raw, 1)
		require.NoError(t, err)
		assertFloat(t, expected, val)
	}
}

func TestTransformRawValueInvalidValues(t *testing.T) {
	t.Parallel()

	rawValues := []string{
		"aaaaa", "0X1234", "aa111aa111", "123.456.789",
	}

	for _, raw := range rawValues {
		_, err := nvidiasmi.TransformRawValue(raw, 1)
		require.Error(t, err)
	}
}

func TestTransformFieldValueEnumFields(t *testing.T) {
	t.Parallel()

	cases := map[nvidiasmi.QField]map[string]float64{
		"gpu_recovery_action": {
			"None": 0, "none": 0, "  None  ": 0,
			"GPU Reset": 1, "Reset": 1, "gpu reset": 1,
			"Node Reboot": 2, "Reboot": 2,
			"Drain P2P": 3, "Drain and Reset": 4,
		},
		"fabric.state": {
			"Not Supported": 0, "Not Started": 1, "In Progress": 2, "Completed": 3,
		},
	}

	for field, conversions := range cases {
		for raw, expected := range conversions {
			val, err := nvidiasmi.TransformFieldValue(field, raw, 1)
			require.NoErrorf(t, err, "field %q value %q", field, raw)
			assertFloat(t, expected, val)
		}
	}
}

// TestTransformFieldValueEnumDrops proves the enum mappers never emit a bogus
// number, and distinguish an expected-absent reading (skipped quietly) from an
// unexpected one (surfaced): "[Not Supported]" is absent, but a retrieval
// "[Unknown Error]" or an unknown string is not, so the exporter can warn.
func TestTransformFieldValueEnumDrops(t *testing.T) {
	t.Parallel()

	absent := []string{"", "N/A", "[N/A]", "[Not Supported]", "[Insufficient Permissions]"}
	unexpected := []string{"[Unknown Error]", "definitely-not-a-value", "Power Cycle"}

	for _, field := range []nvidiasmi.QField{"gpu_recovery_action", "fabric.state"} {
		for _, raw := range absent {
			_, err := nvidiasmi.TransformFieldValue(field, raw, 1)
			require.ErrorIsf(t, err, nvidiasmi.ErrAbsentValue, "field %q value %q", field, raw)
		}

		for _, raw := range unexpected {
			_, err := nvidiasmi.TransformFieldValue(field, raw, 1)
			require.Errorf(t, err, "field %q value %q must be an error", field, raw)
			require.NotErrorIsf(t, err, nvidiasmi.ErrAbsentValue,
				"field %q value %q must not be treated as absent", field, raw)
		}
	}
}

// TestTransformFieldValueFallsThrough proves fields without an enum mapping keep
// using the generic transform unchanged.
func TestTransformFieldValueFallsThrough(t *testing.T) {
	t.Parallel()

	val, err := nvidiasmi.TransformFieldValue("temperature.gpu", "45", 1)
	require.NoError(t, err)
	assertFloat(t, 45, val)

	val, err = nvidiasmi.TransformFieldValue("persistence_mode", "Enabled", 1)
	require.NoError(t, err)
	assertFloat(t, 1, val)
}

func TestTransformRawMultiplier(t *testing.T) {
	t.Parallel()

	val, err := nvidiasmi.TransformRawValue("11", 2)

	require.NoError(t, err)
	assertFloat(t, 22, val)

	val, err = nvidiasmi.TransformRawValue("10", 0.5)
	require.NoError(t, err)
	assertFloat(t, 5, val)

	val, err = nvidiasmi.TransformRawValue("enabled", 42)
	require.NoError(t, err)
	assertFloat(t, 1, val)
}
