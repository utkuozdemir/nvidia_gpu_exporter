package exporter

import (
	"fmt"
	"strings"
)

type table[T any] struct {
	rows          []row[T]
	rFields       []rField
	qFieldToCells map[qField][]cell[T]
}

type row[T any] struct {
	qFieldToCells map[qField]cell[T]
	cells         []cell[T]
}

type cell[T any] struct {
	qField   qField
	rField   rField
	rawValue T
}

var ErrFieldCountMismatch = fmt.Errorf("field count mismatch")

func parseCSVIntoTable(queryResult string, qFields []qField) (table[string], error) {
	lines := strings.Split(strings.TrimSpace(queryResult), "\n")
	titlesLine := lines[0]
	valuesLines := lines[1:]
	rFields := toRFieldSlice(parseCSVLine(titlesLine))

	numCols := len(qFields)
	numRows := len(valuesLines)

	rows := make([]row[string], numRows)

	qFieldToCells := make(map[qField][]cell[string])
	for _, q := range qFields {
		qFieldToCells[q] = make([]cell[string], numRows)
	}

	for rowIndex, valuesLine := range valuesLines {
		qFieldToCell := make(map[qField]cell[string], numCols)
		cells := make([]cell[string], numCols)
		rawValues := parseCSVLine(valuesLine)

		if len(qFields) != len(rFields) {
			return table[string]{}, fmt.Errorf("%w: query fields: %d, returned fields: %d",
				ErrFieldCountMismatch, len(qFields), len(rFields))
		}

		for colIndex, rawValue := range rawValues {
			currentQField := qFields[colIndex]
			currentRField := rFields[colIndex]
			tableCell := cell[string]{
				qField:   currentQField,
				rField:   currentRField,
				rawValue: rawValue,
			}
			qFieldToCell[currentQField] = tableCell
			cells[colIndex] = tableCell
			qFieldToCells[currentQField][rowIndex] = tableCell
		}

		tableRow := row[string]{
			qFieldToCells: qFieldToCell,
			cells:         cells,
		}

		rows[rowIndex] = tableRow
	}

	return table[string]{
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
