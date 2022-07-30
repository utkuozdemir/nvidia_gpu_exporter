package util_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/util"
)

func TestToSnakeCase(t *testing.T) {
	t.Parallel()

	snakeCase := util.ToSnakeCase("aaaAAA_aaaAaa")

	assert.Equal(t, "aaa_aaa_aaa_aaa", snakeCase)
}

func TestHexToDecimal(t *testing.T) {
	t.Parallel()

	decimal, err := util.HexToDecimal("0x40051458")

	assert.NoError(t, err)
	assert.True(t, almostEqual(decimal, 1074074712.0))
}

func TestHexToDecimalError(t *testing.T) {
	t.Parallel()

	_, err := util.HexToDecimal("SOMETHING")

	assert.Error(t, err)
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9
}
