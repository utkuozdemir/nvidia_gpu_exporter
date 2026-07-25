package fakesmi

import (
	"log/slog"
	"math"
	"strings"
	"testing"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/capture"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

// FuzzSplitRow asserts splitRow's contract: it either fails, or returns
// exactly want cells. project() indexes the result by recorded column, so a
// short result is an index panic one caller away.
func FuzzSplitRow(f *testing.F) {
	f.Add("GPU-abc, 123, python, 40960 MiB", 4, 2)
	f.Add("a, b", 2, -1)
	f.Fuzz(func(t *testing.T, row string, want, freeTextColumn int) {
		// real query-gpu captures carry 200+ columns, so the bound has to be
		// well above that to cover the shape the fake actually replays
		if want < 1 || want > 512 || freeTextColumn >= want || freeTextColumn < -1 {
			return
		}

		cells, err := splitRow(row, want, freeTextColumn)
		if err != nil {
			return
		}

		if len(cells) != want {
			t.Fatalf("returned %d cells, want %d", len(cells), want)
		}
	})
}

// FuzzFluctuateStaysParseable is the differential property the fluctuator's own
// doc comment claims: a cell it decides to rewrite must still be a cell the
// exporter's transform accepts. If a rewrite made a value unparseable, demo and
// compose runs would silently lose series.
func FuzzFluctuateStaysParseable(f *testing.F) {
	f.Add("38 %", int64(1))
	f.Add("39.32 W", int64(2))
	f.Add("[N/A]", int64(3))
	f.Add("214 MiB", int64(4))
	f.Fuzz(func(t *testing.T, cell string, seed int64) {
		parsed, ok := parseNumericCell(cell)
		if !ok {
			return
		}

		// the exporter must accept the captured cell in the first place,
		// otherwise there is no series to preserve
		if _, err := nvidiasmi.TransformRawValue(cell, 1); err != nil {
			return
		}

		rewritten := parsed.format(newFluctuator(seed).jitter("utilization.gpu", parsed))

		value, err := nvidiasmi.TransformRawValue(rewritten, 1)
		if err != nil {
			t.Fatalf("jittered %q into %q, which the exporter rejects: %v", cell, rewritten, err)
		}

		if math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatalf("jittered %q into %q, transformed to %v", cell, rewritten, value)
		}
	})
}

// FuzzProjectGPUQuery is the differential property the whole
// developing-without-a-GPU story rests on: whatever a contributed capture
// records, the GPU table the fake projects out of it is one the exporter's own
// CSV parser accepts, with one row per recorded row per simulated GPU. The
// command is assembled from the fuzzed field list rather than fuzzed whole,
// because answer() only ever routes the two real query shapes into project().
func FuzzProjectGPUQuery(f *testing.F) {
	f.Add("uuid,name", "uuid, name\nGPU-abc, Tesla T4", uint16(0x3), 1)
	f.Add("uuid,name,memory.used", "uuid, name, memory.used [MiB]\nGPU-abc, Tesla T4, 214 MiB", uint16(0x4), 3)
	f.Fuzz(func(t *testing.T, fields, body string, requestMask uint16, gpus int) {
		if gpus < 1 || gpus > 8 {
			return
		}

		// The request is derived from the recorded field list rather than
		// fuzzed independently: an unrelated request is rejected before the
		// projection runs, so fuzzing it separately would spend most execs
		// never reaching the code under test.
		request := requestFrom(fields, requestMask)
		if request == "" {
			return
		}

		section := &capture.Section{
			State:   "idle",
			Label:   "query-gpu (csv",
			Command: "nvidia-smi --query-gpu=" + fields + " --format=csv",
			Body:    body,
		}

		output, err := project(section, request, newFuzzConfig(t, gpus))
		if err != nil {
			return
		}

		table, parseErr := nvidiasmi.ParseCSVIntoTable(output, toQFields(trimmedNames(request)))
		if parseErr != nil {
			t.Fatalf("projected output the exporter cannot parse: %v\noutput:\n%q", parseErr, output)
		}

		assertRowsPreserved(t, output, body, gpus, len(table.Rows))
	})
}

// assertRowsPreserved checks that the projection carries one row per recorded
// row per simulated GPU. The parser trims blank lines off both ends, so the
// count is only meaningful when the projection has none. Rather than dropping
// that part of the domain, the condition is decided on the input: a body whose
// every cell is non-empty cannot legitimately project a blank line, so a blank
// line there is itself the bug.
func assertRowsPreserved(t *testing.T, output, body string, gpus, parsedRows int) {
	t.Helper()

	blank := hasBlankLine(output)

	if allCellsNonEmpty(body) && blank {
		t.Fatalf("projected a blank line from a body with no empty cell:\n%q", output)
	}

	if blank {
		return
	}

	recordedRows := len(strings.Split(body, "\n")) - 1
	if want := gpus * recordedRows; parsedRows != want {
		t.Fatalf("projected %d rows, want %d (%d GPUs x %d recorded rows)",
			parsedRows, want, gpus, recordedRows)
	}
}

// hasBlankLine reports whether any line of output is empty once trimmed.
func hasBlankLine(output string) bool {
	for line := range strings.SplitSeq(output, "\n") {
		if strings.TrimSpace(line) == "" {
			return true
		}
	}

	return false
}

// trimmedNames splits a comma-separated field list and trims each name.
func trimmedNames(raw string) []string {
	names := strings.Split(raw, ",")
	for i := range names {
		names[i] = strings.TrimSpace(names[i])
	}

	return names
}

// FuzzProjectComputeAppsQuery is the same property for the per-process query,
// whose recorded field set the exporter always requests in full. Its process
// name column may carry commas of its own, which is the one place the fake's
// CSV shape and the exporter's parser have to agree on something non-trivial.
func FuzzProjectComputeAppsQuery(f *testing.F) {
	f.Add("gpu_uuid, pid, process_name, used_gpu_memory\nGPU-abc, 1, python, 4 MiB", 1)
	f.Add("gpu_uuid, pid, process_name, used_gpu_memory\nGPU-abc, 1, a,b, 4 MiB", 2)
	f.Fuzz(func(t *testing.T, body string, gpus int) {
		if gpus < 1 || gpus > 8 {
			return
		}

		const fields = "gpu_uuid,pid,process_name,used_gpu_memory"

		section := &capture.Section{
			State:   "idle",
			Label:   "query-compute-apps",
			Command: "nvidia-smi --query-compute-apps=" + fields + " --format=csv",
			Body:    body,
		}

		output, err := project(section, fields, newFuzzConfig(t, gpus))
		if err != nil {
			return
		}

		if _, parseErr := nvidiasmi.ParseComputeApps(output, discardLogger); parseErr != nil {
			t.Fatalf("projected output the exporter cannot parse: %v\noutput:\n%q", parseErr, output)
		}
	})
}

// newFuzzConfig builds the fullest replay configuration: multi-GPU identity
// rewriting and fluctuation both on, so a projection has to survive every
// transform stage.
func newFuzzConfig(t *testing.T, gpus int) *config {
	t.Helper()

	identities, err := buildIdentities(gpus, nil)
	if err != nil {
		t.Fatalf("failed to build %d identities: %v", gpus, err)
	}

	overrides := make([]map[string]valueGen, gpus)
	for i := range overrides {
		overrides[i] = map[string]valueGen{}
	}

	return &config{gpus: identities, overrides: overrides, fluct: newFluctuator(1)}
}

// requestFrom picks a non-empty subset of a recorded field list, selected by
// the bits of mask, preserving the recorded order.
func requestFrom(fields string, mask uint16) string {
	names := strings.Split(fields, ",")

	picked := make([]string, 0, len(names))

	for i, name := range names {
		if i < 16 && mask&(1<<uint(i)) != 0 {
			picked = append(picked, name)
		}
	}

	return strings.Join(picked, ",")
}

// allCellsNonEmpty reports whether every comma-separated cell of every line of
// body carries something once trimmed.
func allCellsNonEmpty(body string) bool {
	for line := range strings.SplitSeq(body, "\n") {
		for cell := range strings.SplitSeq(line, ",") {
			if strings.TrimSpace(cell) == "" {
				return false
			}
		}
	}

	return true
}

var discardLogger = slog.New(slog.DiscardHandler)

func toQFields(names []string) []nvidiasmi.QField {
	out := make([]nvidiasmi.QField, len(names))
	for i, name := range names {
		out[i] = nvidiasmi.QField(name)
	}

	return out
}
