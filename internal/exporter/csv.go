package exporter

import (
	"fmt"
	"strings"
)

type table struct {
	rows          []row
	rFields       []rField
	qFieldToCells map[qField][]cell
}

type row struct {
	qFieldToCells map[qField]cell
	cells         []cell
}

type cell struct {
	qField   qField
	rField   rField
	rawValue string
}

var ErrFieldCountMismatch = fmt.Errorf("field count mismatch")

func parseCSVIntoTable(queryResult string, qFields []qField) (table, error) {
	lines := strings.Split(strings.TrimSpace(queryResult), "\n")
	titlesLine := lines[0]
	valuesLines := lines[1:]
	rFields := toRFieldSlice(parseCSVLine(titlesLine))

	numCols := len(qFields)
	numRows := len(valuesLines)

	rows := make([]row, numRows)

	qFieldToCells := make(map[qField][]cell)
	for _, q := range qFields {
		qFieldToCells[q] = make([]cell, numRows)
	}

	for rowIndex, valuesLine := range valuesLines {
		qFieldToCell := make(map[qField]cell, numCols)
		cells := make([]cell, numCols)
		rawValues := parseCSVLine(valuesLine)

		if len(qFields) != len(rFields) {
			return table{}, fmt.Errorf("%w: query fields: %d, returned fields: %d",
				ErrFieldCountMismatch, len(qFields), len(rFields))
		}

		for colIndex, rawValue := range rawValues {
			currentQField := qFields[colIndex]
			currentRField := rFields[colIndex]
			tableCell := cell{
				qField:   currentQField,
				rField:   currentRField,
				rawValue: rawValue,
			}
			qFieldToCell[currentQField] = tableCell
			cells[colIndex] = tableCell
			qFieldToCells[currentQField][rowIndex] = tableCell
		}

		tableRow := row{
			qFieldToCells: qFieldToCell,
			cells:         cells,
		}

		rows[rowIndex] = tableRow
	}

	return table{
		rows:          rows,
		rFields:       rFields,
		qFieldToCells: qFieldToCells,
	}, nil
}

func parseCSVLine(line string) []string {
	values := strings.Split(line, ",")
	result := make([]string, len(values))

	for i, field := range values {
		result[i] = strings.TrimSpace(field)
	}

	return result
}
