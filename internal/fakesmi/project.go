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
// columns, in the requested order, for the header and every row. When overrides
// are given, a data row's cell for a named field is replaced before projection,
// so tests can drive values a real capture does not contain.
func project(section *capture.Section, requestRaw string, overrides map[string]valueGen) (string, error) {
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
		row, err := projectRow(line, lineNum, recorded, columnOf, columns, overrides)
		if err != nil {
			return "", fmt.Errorf("row %d: %w", lineNum+1, err)
		}

		output = append(output, row)
	}

	return strings.Join(output, "\n"), nil
}

// projectRow splits one CSV line, applies data-row overrides, and returns the
// requested columns joined back together. The header (lineNum 0) is never
// overridden and has no free-text cells.
func projectRow(
	line string,
	lineNum int,
	recorded []string,
	columnOf map[string]int,
	columns []int,
	overrides map[string]valueGen,
) (string, error) {
	freeTextColumn := -1
	if column, hasFreeText := columnOf[freeTextField]; hasFreeText && lineNum > 0 {
		freeTextColumn = column
	}

	cells, err := splitRow(line, len(recorded), freeTextColumn)
	if err != nil {
		return "", err
	}

	if lineNum > 0 {
		applyOverrides(cells, recorded, overrides)
	}

	projected := make([]string, 0, len(columns))
	for _, column := range columns {
		projected = append(projected, cells[column])
	}

	return strings.Join(projected, ", "), nil
}

// applyOverrides replaces the cell of every field named in overrides with the
// given value, keyed by the recorded field order. Fields not named are left as
// recorded, and an override for a field absent from this section is ignored.
func applyOverrides(cells, recorded []string, overrides map[string]valueGen) {
	for column, name := range recorded {
		if gen, ok := overrides[name]; ok {
			cells[column] = gen()
		}
	}
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
