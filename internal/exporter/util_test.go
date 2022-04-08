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
	t.Parallel()

	snakeCase := toSnakeCase("aaaAAA_aaaAaa")

	assert.Equal(t, "aaa_aaa_aaa_aaa", snakeCase)
}

func TestHexToDecimal(t *testing.T) {
	t.Parallel()

	decimal, err := hexToDecimal("0x40051458")

	assert.NoError(t, err)
	assert.True(t, almostEqual(decimal, 1074074712.0))
}

func TestHexToDecimalError(t *testing.T) {
	t.Parallel()

	_, err := hexToDecimal("SOMETHING")

	assert.Error(t, err)
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9
}

// TestParseQueryFields is ran manually.
//nolint:forbidigo
func TestParseQueryFields(t *testing.T) {
	t.SkipNow()
	t.Parallel()

	nvidiaSmiCommand := "nvidia-smi"

	qFields, err := parseAutoQFields(nvidiaSmiCommand, nil)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}

	fields := QFieldSliceToStringSlice(qFields)

	fmt.Printf("Fields:\n\n%s\n", strings.Join(fields, "\n"))
}
