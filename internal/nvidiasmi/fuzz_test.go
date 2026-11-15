package nvidiasmi_test

import (
	"log/slog"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

var discard = slog.New(slog.DiscardHandler)

// cudaVersionShape is the shape ParseCudaVersion promises to return, restated
// here so the assertion does not lean on the implementation's own pattern.
var cudaVersionShape = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)

// containerIDShape is the 64-character lowercase hex shape ParseCgroupIdentity
// promises for container_id.
var containerIDShape = regexp.MustCompile(`^[0-9a-f]{64}$`)

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

// FuzzParseCgroupIdentity covers `/proc/<pid>/cgroup` parsing. Any non-empty
// return value becomes a Prometheus label on the per-process series, so
// container_id and systemd_unit are never both set, container_id is always 64
// lowercase hex characters, and systemd_unit is a valid UTF-8 `.service` name
// without path or line separators.
func FuzzParseCgroupIdentity(f *testing.F) {
	f.Add("0::/system.slice/docker-ad46282edd65b1c223e5175af1d71cca1dee615a517cbfcfaab9a1c90db0495b.scope\n")
	f.Add("0::/system.slice/ollama.service\n")
	f.Add("0::/user.slice/user-1000.slice/user@1000.service/init.scope\n")
	f.Add(
		"1:name=systemd:/kubepods.slice/" +
			"cri-containerd-ad46282edd65b1c223e5175af1d71cca1dee615a517cbfcfaab9a1c90db0495b.scope\n0::/\n",
	)
	f.Fuzz(func(t *testing.T, content string) {
		containerID, systemdUnit := nvidiasmi.ParseCgroupIdentity(content)
		if containerID != "" && systemdUnit != "" {
			t.Fatalf("returned both container_id %q and systemd_unit %q", containerID, systemdUnit)
		}

		if containerID != "" && !containerIDShape.MatchString(containerID) {
			t.Fatalf("returned invalid container_id %q", containerID)
		}

		if systemdUnit != "" && !validFuzzSystemdUnit(systemdUnit) {
			t.Fatalf("returned invalid systemd_unit %q", systemdUnit)
		}
	})
}

func validFuzzSystemdUnit(unit string) bool {
	return utf8.ValidString(unit) &&
		strings.HasSuffix(unit, ".service") &&
		!strings.ContainsAny(unit, "/\r\n")
}

// FuzzParseProcesses covers nvidia-smi -q -d PIDS parsing. Every kept entry
// must carry a numeric PID, a non-empty GPU UUID, a non-empty process name, and
// must never duplicate (uuid, pid, gi, ci) within either slice.
func FuzzParseProcesses(f *testing.F) {
	f.Add("GPU 00000000:01:00.0\n    Processes\n        Process ID : 123\n" +
		"            Type : C+G\n            Name : python\n            Used GPU Memory : 128 MiB\n")
	f.Add("==============NVSMI LOG==============\nGPU 0000:02:00.0\n    Processes : None\n")
	f.Add("GPU 00000000:01:00.0\n    Processes\n        GPU instance ID : 2\n" +
		"        Compute instance ID : 0\n        Process ID : 42\n            Type : M+C\n" +
		"            Name :\n            Used GPU Memory : Not available in WDDM driver model\n")

	table := &nvidiasmi.Table{
		Rows: []nvidiasmi.Row{
			{QFieldToCells: map[nvidiasmi.QField]nvidiasmi.Cell{
				nvidiasmi.UUIDQField: {RawValue: "GPU-111"},
				"pci.bus_id":         {RawValue: "00000000:01:00.0"},
			}},
			{QFieldToCells: map[nvidiasmi.QField]nvidiasmi.Cell{
				nvidiasmi.UUIDQField: {RawValue: "GPU-222"},
				"pci.bus_id":         {RawValue: "00000000:02:00.0"},
			}},
		},
	}

	f.Fuzz(func(t *testing.T, output string) {
		procs, err := nvidiasmi.ParseProcesses(output, table, discard)
		if err != nil {
			return
		}

		for _, slice := range [][]nvidiasmi.ComputeApp{procs.Compute, procs.Graphics} {
			assertValidFuzzProcessSlice(t, slice)
		}
	})
}

func assertValidFuzzProcessSlice(t *testing.T, slice []nvidiasmi.ComputeApp) {
	t.Helper()

	seen := map[string]bool{}

	for _, app := range slice {
		if app.GPUUUID == "" || strings.Contains(app.GPUUUID, "\n") {
			t.Fatalf("kept process with unusable gpu uuid %q", app.GPUUUID)
		}

		if _, err := strconv.ParseUint(app.PID, 10, 64); err != nil {
			t.Fatalf("kept process with non-numeric pid %q: %v", app.PID, err)
		}

		if app.ProcessName == "" || strings.Contains(app.ProcessName, "\n") {
			t.Fatalf("kept process with unusable name %q", app.ProcessName)
		}

		key := app.GPUUUID + "\x00" + app.PID + "\x00" + app.GPUInstanceID + "\x00" + app.ComputeInstanceID
		if seen[key] {
			t.Fatalf("duplicate process tuple in slice: %q", key)
		}

		seen[key] = true
	}
}

// FuzzParsePmon covers nvidia-smi pmon output parsing. Every kept entry must
// carry a numeric PID, a known GPU UUID, and at least one finite [0, 1] ratio.
func FuzzParsePmon(f *testing.F) {
	f.Add("# gpu pid type sm mem enc dec jpg ofa command\n0 123 C 50 10 - 0 5 1 python\n")
	f.Add("0 123 G 10 20 0 - C:\\Program Files\\NVIDIA Share.exe\n")
	f.Add("0 - - - - - - - - -\n")

	indexToUUID := map[string]string{"0": "gpu-0", "1": "gpu-1"}

	f.Fuzz(func(t *testing.T, output string) {
		byKey := nvidiasmi.ParsePmon(output, indexToUUID)
		for key, util := range byKey {
			assertValidPmonEntry(t, key, util)
		}
	})
}

func assertValidPmonEntry(t *testing.T, key nvidiasmi.PmonKey, util *nvidiasmi.ProcessUtilization) {
	t.Helper()

	if key.GPUUUID == "" {
		t.Fatalf("kept pmon entry with empty gpu uuid")
	}

	if _, err := strconv.ParseUint(key.PID, 10, 64); err != nil {
		t.Fatalf("kept pmon entry with non-numeric pid %q: %v", key.PID, err)
	}

	if util == nil {
		t.Fatalf("kept nil ProcessUtilization for %v", key)
	}

	assertValidPmonRatios(t, key, util)
}

func assertValidPmonRatios(t *testing.T, key nvidiasmi.PmonKey, util *nvidiasmi.ProcessUtilization) {
	t.Helper()

	anyPresent := false

	for _, ratio := range []*float64{
		util.SMRatio, util.MemoryRatio, util.EncoderRatio,
		util.DecoderRatio, util.JPEGRatio, util.OFARatio,
	} {
		if ratio == nil {
			continue
		}

		anyPresent = true

		if math.IsNaN(*ratio) || math.IsInf(*ratio, 0) || *ratio < 0 || *ratio > 1 {
			t.Fatalf("pmon ratio out of [0, 1]: %v", *ratio)
		}
	}

	if !anyPresent {
		t.Fatalf("kept all-nil ProcessUtilization for %v", key)
	}
}
