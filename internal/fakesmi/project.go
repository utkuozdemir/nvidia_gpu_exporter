package fakesmi

import (
	"errors"
	"fmt"
	"strings"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/capture"
)

// freeTextField is the one CSV column whose values may themselves contain
// commas (nvidia-smi sanitizes them on current drivers, but the exporter does
// not rely on that, and neither does the fake).
const freeTextField = "process_name"

var errEmptySection = errors.New("section body has no header row")

// project answers a CSV query for the requested comma-separated fields from a
// recorded section: the field list recorded in the section's own command line
// maps field names to CSV columns, and the output carries the requested
// columns, in the requested order, for the header and every row.
func project(section *capture.Section, requestRaw string) (string, error) {
	recorded, err := recordedFields(section.Command)
	if err != nil {
		return "", err
	}

	columnOf := make(map[string]int, len(recorded))
	for i, name := range recorded {
		columnOf[name] = i
	}

	columns, err := requestedColumns(requestRaw, columnOf)
	if err != nil {
		return "", err
	}

	lines := strings.Split(section.Body, "\n")
	if lines[0] == "" {
		return "", errEmptySection
	}

	output := make([]string, 0, len(lines))

	for lineNum, line := range lines {
		// the header row never contains free-text cells, row lines may
		freeTextColumn := -1
		if column, hasFreeText := columnOf[freeTextField]; hasFreeText && lineNum > 0 {
			freeTextColumn = column
		}

		cells, err := splitRow(line, len(recorded), freeTextColumn)
		if err != nil {
			return "", fmt.Errorf("row %d: %w", lineNum+1, err)
		}

		projected := make([]string, 0, len(columns))
		for _, column := range columns {
			projected = append(projected, cells[column])
		}

		output = append(output, strings.Join(projected, ", "))
	}

	return strings.Join(output, "\n"), nil
}

// requestedColumns resolves a requested comma-separated field list to column
// indexes of the recorded CSV.
func requestedColumns(requestRaw string, columnOf map[string]int) ([]int, error) {
	var columns []int

	for name := range strings.SplitSeq(requestRaw, ",") {
		column, known := columnOf[strings.TrimSpace(name)]
		if !known {
			// mimics how the real nvidia-smi rejects an unknown field
			//nolint:revive,staticcheck
			return nil, fmt.Errorf("Field %q is not a valid field to query.", name)
		}

		columns = append(columns, column)
	}

	return columns, nil
}

// recordedFields extracts the field list from a recorded query command line,
// e.g. "nvidia-smi --query-gpu=a,b,c --format=csv" yields [a b c].
func recordedFields(command string) ([]string, error) {
	for token := range strings.FieldsSeq(command) {
		_, value, isQuery := strings.Cut(token, "=")
		if isQuery && strings.HasPrefix(token, "--query-") && !strings.HasPrefix(token, "--format") {
			return strings.Split(value, ","), nil
		}
	}

	return nil, fmt.Errorf("no field list found in recorded command %q", command)
}

// splitRow splits a CSV row into exactly want cells. When the row has more
// cells than recorded fields and a free-text column exists, the extra commas
// belong to that column: the cells left and right of it are fixed, and the
// middle is kept together with its commas intact. Every cell, the free-text
// one included, is trimmed of surrounding whitespace, matching how the
// exporter's own parser treats cells.
func splitRow(row string, want, freeTextColumn int) ([]string, error) {
	raw := strings.Split(row, ",")

	if len(raw) != want && (len(raw) < want || freeTextColumn < 0) {
		return nil, fmt.Errorf("expected %d cells, got %d in row %q", want, len(raw), row)
	}

	cells := make([]string, want)
	extra := len(raw) - want

	for column := range want {
		switch {
		case freeTextColumn >= 0 && column == freeTextColumn:
			cells[column] = strings.TrimSpace(strings.Join(raw[column:column+extra+1], ","))
		case freeTextColumn >= 0 && column > freeTextColumn:
			cells[column] = strings.TrimSpace(raw[column+extra])
		default:
			cells[column] = strings.TrimSpace(raw[column])
		}
	}

	return cells, nil
}
