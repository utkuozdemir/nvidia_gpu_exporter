package exporter

import (
	"fmt"
	"strings"
)

type Table[T any] struct {
	Rows          []Row[T]
	RFields       []RField
	QFieldToCells map[QField][]Cell[T]
}

type Row[T any] struct {
	QFieldToCells map[QField]Cell[T]
	Cells         []Cell[T]
}

type Cell[T any] struct {
	QField   QField
	RField   RField
	RawValue T
}

var ErrFieldCountMismatch = fmt.Errorf("field count mismatch")

func ParseCSVIntoTable(queryResult string, qFields []QField) (Table[string], error) {
	lines := strings.Split(strings.TrimSpace(queryResult), "\n")
	titlesLine := lines[0]
	valuesLines := lines[1:]
	rFields := toRFieldSlice(parseCSVLine(titlesLine))

	numCols := len(qFields)
	numRows := len(valuesLines)

	rows := make([]Row[string], numRows)

	qFieldToCells := make(map[QField][]Cell[string])
	for _, q := range qFields {
		qFieldToCells[q] = make([]Cell[string], numRows)
	}

	for rowIndex, valuesLine := range valuesLines {
		qFieldToCell := make(map[QField]Cell[string], numCols)
		cells := make([]Cell[string], numCols)
		rawValues := parseCSVLine(valuesLine)

		if len(qFields) != len(rFields) {
			return Table[string]{}, fmt.Errorf("%w: query fields: %d, returned fields: %d",
				ErrFieldCountMismatch, len(qFields), len(rFields))
		}

		for colIndex, rawValue := range rawValues {
			currentQField := qFields[colIndex]
			currentRField := rFields[colIndex]
			tableCell := Cell[string]{
				QField:   currentQField,
				RField:   currentRField,
				RawValue: rawValue,
			}
			qFieldToCell[currentQField] = tableCell
			cells[colIndex] = tableCell
			qFieldToCells[currentQField][rowIndex] = tableCell
		}

		tableRow := Row[string]{
			QFieldToCells: qFieldToCell,
			Cells:         cells,
		}

		rows[rowIndex] = tableRow
	}

	return Table[string]{
		Rows:          rows,
		RFields:       rFields,
		QFieldToCells: qFieldToCells,
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
