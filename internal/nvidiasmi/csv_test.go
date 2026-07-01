package nvidiasmi_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

const testCsv = `
name, power.draw [W]
NVIDIA GeForce RTX 2080 SUPER, 30.14 W
Some Dummy GPU, 12.34 W
`

func TestParseCsvIntoTable(t *testing.T) {
	t.Parallel()

	parsed, err := nvidiasmi.ParseCSVIntoTable(testCsv, []nvidiasmi.QField{"name", "power.draw"})

	require.NoError(t, err)
	assert.Len(t, parsed.Rows, 2)
	assert.Equal(t, []nvidiasmi.RField{"name", "power.draw [W]"}, parsed.RFields)

	cell00 := nvidiasmi.Cell{
		QField:   "name",
		RField:   "name",
		RawValue: "NVIDIA GeForce RTX 2080 SUPER",
	}
	cell01 := nvidiasmi.Cell{QField: "power.draw", RField: "power.draw [W]", RawValue: "30.14 W"}
	cell10 := nvidiasmi.Cell{QField: "name", RField: "name", RawValue: "Some Dummy GPU"}
	cell11 := nvidiasmi.Cell{QField: "power.draw", RField: "power.draw [W]", RawValue: "12.34 W"}

	row0 := nvidiasmi.Row{
		QFieldToCells: map[nvidiasmi.QField]nvidiasmi.Cell{"name": cell00, "power.draw": cell01},
		Cells:         []nvidiasmi.Cell{cell00, cell01},
	}

	row1 := nvidiasmi.Row{
		QFieldToCells: map[nvidiasmi.QField]nvidiasmi.Cell{"name": cell10, "power.draw": cell11},
		Cells:         []nvidiasmi.Cell{cell10, cell11},
	}

	expected := nvidiasmi.Table{
		Rows:    []nvidiasmi.Row{row0, row1},
		RFields: []nvidiasmi.RField{"name", "power.draw [W]"},
		QFieldToCells: map[nvidiasmi.QField][]nvidiasmi.Cell{
			"name":       {cell00, cell10},
			"power.draw": {cell01, cell11},
		},
	}

	assert.Equal(t, expected, parsed)
}
