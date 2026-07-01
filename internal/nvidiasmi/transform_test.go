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
