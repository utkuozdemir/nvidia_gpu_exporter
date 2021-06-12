package exporter

import (
	"github.com/stretchr/testify/assert"
	"math"
	"testing"
)

func TestToSnakeCase(t *testing.T) {
	snakeCase := toSnakeCase("aaaAAA_aaaAaa")
	assert.Equal(t, "aaa_aaa_aaa_aaa", snakeCase)
}

func TestHexToDecimal(t *testing.T) {
	decimal, err := hexToDecimal("0x40051458")
	assert.NoError(t, err)
	assert.True(t, almostEqual(decimal, 1074074712.0))
}

func TestHexToDecimalError(t *testing.T) {
	_, err := hexToDecimal("SOMETHING")
	assert.Error(t, err)
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9
}
