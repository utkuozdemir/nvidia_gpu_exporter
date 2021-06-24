package exporter

import "strings"

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

func parseCSVIntoTable(queryResult string, qFields []qField) table {
	lines := strings.Split(strings.TrimSpace(queryResult), "\n")
	titlesLine := lines[0]
	valuesLines := lines[1:]
	rFields := toRFieldSlice(parseCSVLine(titlesLine))

	numCols := len(qFields)
	numRows := len(valuesLines)

	rows := make([]row, numRows)

	qFieldToCells := make(map[qField][]cell)
	for _, qField := range qFields {
		qFieldToCells[qField] = make([]cell, numRows)
	}

	for rowIndex, valuesLine := range valuesLines {
		qFieldToCell := make(map[qField]cell, numCols)
		cells := make([]cell, numCols)
		rawValues := parseCSVLine(valuesLine)
		for colIndex, rawValue := range rawValues {
			qField := qFields[colIndex]
			gm := cell{
				qField:   qField,
				rField:   rFields[colIndex],
				rawValue: rawValue,
			}
			qFieldToCell[qField] = gm
			cells[colIndex] = gm
			qFieldToCells[qField][rowIndex] = gm
		}

		gmc := row{
			qFieldToCells: qFieldToCell,
			cells:         cells,
		}

		rows[rowIndex] = gmc

	}

	return table{
		rows:          rows,
		rFields:       rFields,
		qFieldToCells: qFieldToCells,
	}
}

func parseCSVLine(line string) []string {
	values := strings.Split(line, ",")
	result := make([]string, len(values))
	for i, field := range values {
		result[i] = strings.TrimSpace(field)
	}
	return result
}
