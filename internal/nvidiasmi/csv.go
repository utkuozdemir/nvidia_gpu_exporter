package nvidiasmi

import (
	"fmt"
	"strings"
)

type Table struct {
	Rows          []Row
	RFields       []RField
	QFieldToCells map[QField][]Cell
}

type Row struct {
	QFieldToCells map[QField]Cell
	Cells         []Cell
}

type Cell struct {
	QField   QField
	RField   RField
	RawValue string
}

func ParseCSVIntoTable(queryResult string, qFields []QField) (Table, error) {
	lines := strings.Split(strings.TrimSpace(queryResult), "\n")
	titlesLine := lines[0]
	valuesLines := lines[1:]
	rFields := toRFieldSlice(parseCSVLine(titlesLine))

	numCols := len(qFields)
	numRows := len(valuesLines)

	// The header must line up with what was asked for before anything is
	// indexed by column. Checking it up front rather than per row also covers
	// the header-only case: callers map the returned names back by query-field
	// position, so a mismatch that survives here is an out-of-range access
	// one caller away.
	if numCols != len(rFields) {
		return Table{}, fmt.Errorf(
			"field count mismatch: query fields: %d, returned fields: %d",
			numCols,
			len(rFields),
		)
	}

	rows := make([]Row, numRows)

	qFieldToCells := make(map[QField][]Cell)
	for _, q := range qFields {
		qFieldToCells[q] = make([]Cell, numRows)
	}

	for rowIndex, valuesLine := range valuesLines {
		row, err := parseCSVRow(valuesLine, qFields, rFields)
		if err != nil {
			return Table{}, fmt.Errorf("row %d: %w", rowIndex+1, err)
		}

		for colIndex, cell := range row.Cells {
			qFieldToCells[qFields[colIndex]][rowIndex] = cell
		}

		rows[rowIndex] = row
	}

	return Table{
		Rows:          rows,
		RFields:       rFields,
		QFieldToCells: qFieldToCells,
	}, nil
}

// parseCSVRow splits one data line into cells, one per query field.
func parseCSVRow(valuesLine string, qFields []QField, rFields []RField) (Row, error) {
	numCols := len(qFields)
	rawValues := parseCSVLine(valuesLine)

	// A row that does not match the header has no defined column mapping: this
	// output has no quoting, so a value containing a comma is indistinguishable
	// from an extra column. Failing the collection reports no GPU data, which
	// is the contract; guessing would export values under the wrong metrics.
	if len(rawValues) != numCols {
		return Row{}, fmt.Errorf(
			"has %d columns, want %d: %q",
			len(rawValues),
			numCols,
			strings.TrimSpace(valuesLine),
		)
	}

	qFieldToCell := make(map[QField]Cell, numCols)
	cells := make([]Cell, numCols)

	for colIndex, rawValue := range rawValues {
		cell := Cell{
			QField:   qFields[colIndex],
			RField:   rFields[colIndex],
			RawValue: rawValue,
		}
		qFieldToCell[cell.QField] = cell
		cells[colIndex] = cell
	}

	return Row{QFieldToCells: qFieldToCell, Cells: cells}, nil
}

func parseCSVLine(line string) []string {
	values := strings.Split(line, ",")
	result := make([]string, len(values))

	for i, field := range values {
		result[i] = strings.TrimSpace(field)
	}

	return result
}
