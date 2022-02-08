package exporter

import (
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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

// TestParseQueryFields is ran manually
func TestParseQueryFields(t *testing.T) {
	t.SkipNow()
	nvidiaSmiCommand := "nvidia-smi"

	qFields, err := ParseAutoQFields(nvidiaSmiCommand)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}
	fields := QFieldSliceToStringSlice(qFields)
	fmt.Printf("Fields:\n\n%s\n", strings.Join(fields, "\n"))
}
