package exporter

import "strings"

type table struct {
	rows                  []row
	returnedFieldNames    []string
	queryFieldNameToCells map[string][]cell
}

type row struct {
	queryFieldNameToCells map[string]cell
	cells                 []cell
}

type cell struct {
	queryFieldName    string
	returnedFieldName string
	rawValue          string
}

func parseCSVIntoTable(queryResult string, queryFieldNames []string) table {
	lines := strings.Split(strings.TrimSpace(queryResult), "\n")
	titlesLine := lines[0]
	valuesLines := lines[1:]
	returnedFieldNames := parseCSVLine(titlesLine)

	numCols := len(queryFieldNames)
	numRows := len(valuesLines)

	rows := make([]row, numRows)

	queryFieldNameToCells := make(map[string][]cell)
	for _, queryFieldName := range queryFieldNames {
		queryFieldNameToCells[queryFieldName] = make([]cell, numRows)
	}

	for rowIndex, valuesLine := range valuesLines {
		queryFieldNameToCell := make(map[string]cell, numCols)
		cells := make([]cell, numCols)
		rawValues := parseCSVLine(valuesLine)
		for colIndex, rawValue := range rawValues {
			queryFieldName := queryFieldNames[colIndex]
			gm := cell{
				queryFieldName:    queryFieldName,
				returnedFieldName: returnedFieldNames[colIndex],
				rawValue:          rawValue,
			}
			queryFieldNameToCell[queryFieldName] = gm
			cells[colIndex] = gm
			queryFieldNameToCells[queryFieldName][rowIndex] = gm
		}

		gmc := row{
			queryFieldNameToCells: queryFieldNameToCell,
			cells:                 cells,
		}

		rows[rowIndex] = gmc

	}

	return table{
		rows:                  rows,
		returnedFieldNames:    returnedFieldNames,
		queryFieldNameToCells: queryFieldNameToCells,
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
