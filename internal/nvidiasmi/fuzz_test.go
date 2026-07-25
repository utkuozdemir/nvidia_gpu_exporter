package nvidiasmi_test

import (
	"log/slog"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

var discard = slog.New(slog.DiscardHandler)

// cudaVersionShape is the shape ParseCudaVersion promises to return, restated
// here so the assertion does not lean on the implementation's own pattern.
var cudaVersionShape = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)

func toQFields(raw string) []nvidiasmi.QField {
	names := strings.Split(raw, ",")

	qFields := make([]nvidiasmi.QField, len(names))
	for i, name := range names {
		qFields[i] = nvidiasmi.QField(name)
	}

	return qFields
}

// FuzzParseCSVIntoTable covers the exporter's hottest untrusted input: the CSV
// nvidia-smi prints, whose shape varies by driver, platform and GPU, and which
// a custom command can replace entirely. A table that parses must be safe to
// index by column, since that is exactly what every caller does with it.
func FuzzParseCSVIntoTable(f *testing.F) {
	f.Add("name, uuid\nTesla T4, GPU-abc\n", "name,uuid")
	f.Add("utilization.gpu [%]\n38 %\n", "utilization.gpu")
	f.Add("", "")
	// a row wider than the header, and one narrower: both used to be accepted
	f.Add("name, uuid\nTesla T4, GPU-abc, extra\n", "name,uuid")
	f.Add("name, uuid\nTesla T4\n", "name,uuid")
	// a header that disagrees with the request and no data rows to catch it
	f.Add("name, uuid", "name,uuid,index")
	f.Fuzz(func(t *testing.T, output, fieldsRaw string) {
		qFields := toQFields(fieldsRaw)

		table, err := nvidiasmi.ParseCSVIntoTable(output, qFields)
		if err != nil {
			return
		}

		// callers map the returned names back by query-field position
		if len(table.RFields) != len(qFields) {
			t.Fatalf("accepted a table with %d returned fields for %d query fields",
				len(table.RFields), len(qFields))
		}

		for rowIndex := range table.Rows {
			assertRowConsistent(t, table, qFields, output, rowIndex)
		}
	})
}

// assertRowConsistent checks that one parsed row is addressable the way every
// caller addresses it: one cell per query field, each cell carrying its own
// column's query and returned field, the by-field lookups agreeing with the
// positional slice, and the values being that line's own. A violation here
// silently exports one field's reading under another field's metric.
func assertRowConsistent(
	t *testing.T,
	table nvidiasmi.Table,
	qFields []nvidiasmi.QField,
	output string,
	rowIndex int,
) {
	t.Helper()

	row := table.Rows[rowIndex]

	if len(row.Cells) != len(qFields) {
		t.Fatalf("row %d has %d cells, want %d", rowIndex, len(row.Cells), len(qFields))
	}

	for col, cell := range row.Cells {
		if cell.QField != qFields[col] {
			t.Fatalf("row %d column %d carries query field %q, want %q",
				rowIndex, col, cell.QField, qFields[col])
		}

		if cell.RField != table.RFields[col] {
			t.Fatalf("row %d column %d carries returned field %q, want %q",
				rowIndex, col, cell.RField, table.RFields[col])
		}

		// a duplicated field makes the by-field lookups ambiguous by
		// construction, so they are only checked for unique ones
		if !seenOnce(qFields, cell.QField) {
			continue
		}

		if got := row.QFieldToCells[cell.QField]; got != cell {
			t.Fatalf("row %d: by-field lookup of %q gives %+v, want %+v",
				rowIndex, cell.QField, got, cell)
		}

		if got := table.QFieldToCells[cell.QField][rowIndex]; got != cell {
			t.Fatalf("row %d: table lookup of %q gives %+v, want %+v",
				rowIndex, cell.QField, got, cell)
		}
	}

	rejoined := make([]string, 0, len(row.Cells))
	for _, cell := range row.Cells {
		rejoined = append(rejoined, cell.RawValue)
	}

	if got, want := strings.Join(rejoined, ","), trimmedCells(dataLine(output, rowIndex)); got != want {
		t.Fatalf("row %d parsed to %q, want %q", rowIndex, got, want)
	}
}

// seenOnce reports whether value appears exactly once in values.
func seenOnce[T comparable](values []T, value T) bool {
	count := 0

	for _, candidate := range values {
		if candidate == value {
			count++
		}
	}

	return count == 1
}

// dataLine returns the index-th data line of a CSV payload, the way the parser
// splits it.
func dataLine(output string, index int) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")

	return lines[index+1]
}

// trimmedCells splits a line on commas and trims each cell, matching how the
// parser stores raw values.
func trimmedCells(line string) string {
	cells := strings.Split(line, ",")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}

	return strings.Join(cells, ",")
}

// FuzzTransformRawValue covers the value side of the same input. Every cell a
// driver prints reaches this, and whatever comes out becomes a metric value, so
// a value that is not a number must be reported as an error rather than
// exported as one.
func FuzzTransformRawValue(f *testing.F) {
	f.Add("38 %", 0.01)
	f.Add("0x1F", 1.0)
	f.Add("[N/A]", 1.0)
	f.Add("Enabled", 1.0)
	// finite on its own, infinite once scaled into bytes
	f.Add("179769313486231570000000000000000000000000000000000000000000000000000"+
		"0000000000000000000000000000000000000000000000000000000000000000000000"+
		"0000000000000000000000000000000000000000000000000000000000000000000000"+
		"00000000000000000000000000000000000000000000000000000000000000000 MiB", 1048576.0)
	f.Add("2", math.MaxFloat64)
	f.Fuzz(func(t *testing.T, raw string, multiplier float64) {
		if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
			return
		}

		value, err := nvidiasmi.TransformRawValue(raw, multiplier)
		if err != nil {
			return
		}

		if math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatalf("transformed %q to a non-finite value %v", raw, value)
		}
	})
}

// FuzzParseComputeApps covers the per-process query, whose process name column
// carries workload-controlled text that drivers do not always sanitize.
func FuzzParseComputeApps(f *testing.F) {
	f.Add("gpu_uuid, pid, process_name, used_gpu_memory [MiB]\nGPU-abc, 123, python, 40960 MiB\n")
	f.Add("gpu_uuid, pid, process_name, used_gpu_memory [MiB]\nGPU-abc, 123, a,b,c, [N/A]\n")
	// a row with no uuid to attribute it to
	f.Add("gpu_uuid, pid, process_name, used_gpu_memory [MiB]\n, 123, python, 40960 MiB\n")
	// two rows differing only in a uuid neither of them has
	f.Add("gpu_uuid, pid, process_name, used_gpu_memory [MiB]\n" +
		", 1, python, 1 MiB\n, 1, python, 2 MiB\n")
	f.Fuzz(func(t *testing.T, output string) {
		apps, err := nvidiasmi.ParseComputeApps(output, discard)
		if err != nil {
			return
		}

		for _, app := range apps {
			// the pid becomes a metric label; a row whose pid is unusable is
			// meant to be skipped, not exported
			if app.PID == "" || strings.ContainsAny(app.PID, "\n\r") {
				t.Fatalf("kept a row with an unusable pid %q", app.PID)
			}

			// the uuid is what joins a process to its GPU; without one the
			// series cannot be attributed, and several such rows collapse
			// onto the same label set
			if app.GPUUUID == "" {
				t.Fatalf("kept a row with no gpu uuid: pid %q", app.PID)
			}

			// The row is parsed from the outside in, with the process name
			// rejoined from whatever is left in the middle. Reassembling the
			// four fields must reproduce some input line's cells, or a column
			// shifted and the memory reading is being labelled as a process
			// name.
			rejoined := strings.Join([]string{
				app.GPUUUID, app.PID, app.ProcessName, app.UsedMemory,
			}, ",")

			if !containsRejoinedRow(output, rejoined) {
				t.Fatalf("parsed a row to %q, which is not in the input", rejoined)
			}
		}
	})
}

// containsRejoinedRow reports whether any data line of output reassembles to
// row, with the cells trimmed and the uuid normalized the way the parser does.
func containsRejoinedRow(output, row string) bool {
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		cells := strings.Split(line, ",")
		if len(cells) < 4 {
			continue
		}

		// the free-text process name keeps the commas between the fixed cells
		rejoined := strings.Join([]string{
			nvidiasmi.NormalizeUUID(cells[0]),
			strings.TrimSpace(cells[1]),
			strings.TrimSpace(strings.Join(cells[2:len(cells)-1], ",")),
			strings.TrimSpace(cells[len(cells)-1]),
		}, ",")

		if rejoined == row {
			return true
		}
	}

	return false
}

// FuzzSplitCommand covers the --nvidia-smi-command value, the one input a user
// writes by hand. Its result is handed straight to exec, so an accepted command
// must produce a usable argument vector.
func FuzzSplitCommand(f *testing.F) {
	f.Add("nvidia-smi")
	f.Add(`"C:\Program Files\NVIDIA Corporation\NVSMI\nvidia-smi.exe"`)
	f.Add("sudo nvidia-smi")
	f.Fuzz(func(t *testing.T, command string) {
		parts, err := nvidiasmi.SplitCommand(command)
		if err != nil {
			return
		}

		if len(parts) == 0 || parts[0] == "" {
			t.Fatalf("returned an unusable argument vector %q for %q", parts, command)
		}
	})
}

// FuzzParseCudaVersion covers the --version output, whose lines drivers have
// already renamed once. Anything that is not a version number has to be
// dropped rather than exported as the cuda_version label.
func FuzzParseCudaVersion(f *testing.F) {
	f.Add("NVIDIA-SMI version : 590.48\nCUDA Version    : 13.1\n")
	f.Add(`CUDA version : Deprecated, see "CUDA UMD version" instead` + "\nCUDA UMD version : 13.1\n")
	f.Fuzz(func(t *testing.T, output string) {
		version := nvidiasmi.ParseCudaVersion(output)
		if version == "" {
			return
		}

		if !cudaVersionShape.MatchString(version) {
			t.Fatalf("returned %q, which is not a version number", version)
		}
	})
}

// FuzzExtractQFields covers auto-detection against --help-query-gpu output. A
// discovered name is joined into the query with commas, so a name carrying a
// separator would silently turn one column into two.
func FuzzExtractQFields(f *testing.F) {
	f.Add("\n\n\"timestamp\"\nThe timestamp.\n\n\"uuid\"\nThe uuid.\n")
	// a discovered name carrying the separator the query joins names with
	f.Add("\n\n\"uuid,name\"\nTwo fields in one name.\n")
	f.Add("\n\n\"uuid\nname\"\nA name with a line break.\n")
	f.Fuzz(func(t *testing.T, help string) {
		for _, field := range nvidiasmi.ExtractQFields(help) {
			if strings.ContainsAny(string(field), ",\n\r") {
				t.Fatalf("discovered an unusable field name %q", field)
			}
		}
	})
}
