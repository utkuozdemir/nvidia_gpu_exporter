//go:build gpu && linux && cgo

package integration_test

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvmlnative"
)

// TestBackendParityOnRealGPU is the live consistency oracle: on a machine
// with an NVIDIA driver it collects the same fields through the exec backend
// and the nvml backend and requires the results to match. It never runs in
// regular CI (build tag gpu); every GPU box session should run it:
//
//	go test -tags gpu ./internal/integration/ -run TestBackendParity -v
//
// Sampling is bracketed (exec, native, exec): stable fields must equal one
// of the exec brackets byte for byte, volatile fields must fall inside the
// bracket range, and absent fields must be absent on both sides. This is
// what catches value-format drift (rounding, unit, token spelling changes)
// that the offline corpus tests cannot see.
func TestBackendParityOnRealGPU(t *testing.T) {
	logger := slogt.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	resolved, err := nvidiasmi.ResolveFields(
		ctx, nvidiasmi.DefaultCommand, "AUTO", "", 30*time.Second, nvidiasmi.DefaultRunFunc, logger)
	require.NoError(t, err, "is nvidia-smi available on this machine?")

	backend, err := nvmlnative.New(logger)
	require.NoError(t, err, "is the NVIDIA driver library available on this machine?")

	t.Cleanup(backend.Close)

	nativeResolved, err := nvmlnative.Resolve("AUTO", "", backend.DriverVersion(), logger)
	require.NoError(t, err)

	// bracketed sampling: exec, native, exec
	execBefore := queryExec(t, ctx, resolved)
	nativeReading, _, err := backend.QueryFunc(nativeResolved, false)(ctx)
	require.NoError(t, err)
	execAfter := queryExec(t, ctx, resolved)

	native := nativeReading.Table
	require.NotNil(t, native)
	require.Len(t, native.Rows, len(execBefore.Rows),
		"the two backends see a different number of GPUs")

	nativeByUUID := rowsByUUID(t, native)
	execAfterByUUID := rowsByUUID(t, execAfter)

	failures := 0

	for uuid, beforeRow := range rowsByUUID(t, execBefore) {
		nativeRow, ok := nativeByUUID[uuid]
		require.True(t, ok, "GPU %s is missing from the native backend's table", uuid)

		afterRow, ok := execAfterByUUID[uuid]
		require.True(t, ok, "GPU %s is missing from the second exec sample", uuid)

		for _, qField := range resolved.Query {
			failures += compareField(t, uuid, qField, beforeRow, nativeRow, afterRow)
			if failures > maxReportedMismatches {
				t.Fatalf("too many mismatches (%d); aborting the field walk", failures)
			}
		}
	}
}

const maxReportedMismatches = 25

// volatileFieldPrefixes are readings that legitimately change between the
// bracket samples; their numeric values must fall inside the bracket, and
// their non-numeric states must match one of the brackets. The classes are
// the ones established by the H100 diff sessions.
var volatileFieldPrefixes = []string{
	"timestamp", "temperature.", "power.draw", "utilization.", "clocks.current.",
	"memory.used", "memory.free", "fan.speed", "pstate", "encoder.stats.",
	"clocks_event_reasons", "clocks_throttle_reasons", "ecc.errors.",
}

func isVolatileField(q nvidiasmi.QField) bool {
	for _, prefix := range volatileFieldPrefixes {
		if strings.HasPrefix(string(q), prefix) {
			return true
		}
	}

	return false
}

// compareField checks one field of one GPU across the bracket, returning the
// number of mismatches reported.
func compareField(
	t *testing.T,
	uuid string,
	qField nvidiasmi.QField,
	before, native, after nvidiasmi.Row,
) int {
	t.Helper()

	beforeCell, inExec := before.QFieldToCells[qField]
	nativeCell, inNative := native.QFieldToCells[qField]

	switch {
	case inExec && !inNative:
		// exec advertises a field the catalog lacks: drift when it carries a
		// real value (mirrors the corpus test's rule for new fields)
		if !isAbsentValue(beforeCell.RawValue) {
			t.Errorf("exec exports a field the nvml backend does not serve\n"+
				"  gpu: %s\n  field: %s\n  exec value: %q\n"+
				"  fix: add it to the catalog in internal/nvmlnative, or record the deferral",
				uuid, qField, beforeCell.RawValue)

			return 1
		}

		return 0
	case !inExec:
		return 0
	}

	afterCell := after.QFieldToCells[qField]

	execAbsent := isAbsentValue(beforeCell.RawValue) && isAbsentValue(afterCell.RawValue)
	nativeAbsent := isAbsentValue(nativeCell.RawValue)

	if execAbsent != nativeAbsent {
		t.Errorf("absence mismatch between the backends\n"+
			"  gpu: %s\n  field: %s\n  exec: %q / %q\n  nvml: %q\n"+
			"  fix: the error-to-token mapping or the collector for this field in internal/nvmlnative",
			uuid, qField, beforeCell.RawValue, afterCell.RawValue, nativeCell.RawValue)

		return 1
	}

	if nativeAbsent {
		return 0
	}

	if !isVolatileField(qField) {
		if nativeCell.RawValue != beforeCell.RawValue && nativeCell.RawValue != afterCell.RawValue {
			t.Errorf("stable field value mismatch\n"+
				"  gpu: %s\n  field: %s\n  exec: %q / %q\n  nvml: %q\n"+
				"  fix: the formatter for this field in internal/nvmlnative (format.go, backend_nvml.go)",
				uuid, qField, beforeCell.RawValue, afterCell.RawValue, nativeCell.RawValue)

			return 1
		}

		return 0
	}

	return compareVolatile(t, uuid, qField, beforeCell.RawValue, nativeCell.RawValue, afterCell.RawValue)
}

// compareVolatile checks a volatile reading: numeric values must fall inside
// the bracket range (with a small tolerance for quantization), states must
// equal one of the brackets.
func compareVolatile(t *testing.T, uuid string, qField nvidiasmi.QField, before, native, after string) int {
	t.Helper()

	beforeNum, beforeOK := parseNumeric(before)
	nativeNum, nativeOK := parseNumeric(native)
	afterNum, afterOK := parseNumeric(after)

	if !beforeOK || !afterOK || !nativeOK {
		// non-numeric volatile state (pstate, reason flags): any bracket match is fine
		if native != before && native != after {
			t.Errorf("volatile state mismatch\n"+
				"  gpu: %s\n  field: %s\n  exec: %q / %q\n  nvml: %q\n"+
				"  fix: the enum spelling for this field in internal/nvmlnative/format.go",
				uuid, qField, before, after, native)

			return 1
		}

		return 0
	}

	low, high := beforeNum, afterNum
	if low > high {
		low, high = high, low
	}

	// one quantization step of slack: readings like power jump by fractions
	// while brackets are ~tens of milliseconds apart
	tolerance := 0.011 + (high-low)*0.05

	if nativeNum < low-tolerance-absoluteSlack(qField) || nativeNum > high+tolerance+absoluteSlack(qField) {
		t.Errorf("volatile value outside the sampling bracket\n"+
			"  gpu: %s\n  field: %s\n  exec bracket: %q .. %q\n  nvml: %q\n"+
			"  fix: the unit or scale conversion for this field in internal/nvmlnative",
			uuid, qField, before, after, native)

		return 1
	}

	return 0
}

// absoluteSlack widens the bracket for readings that legitimately move fast.
func absoluteSlack(qField nvidiasmi.QField) float64 {
	switch {
	case strings.HasPrefix(string(qField), "clocks.current."):
		return 500 // MHz; clocks can step several bins between samples
	case strings.HasPrefix(string(qField), "utilization."):
		return 100 // percent; utilization can swing fully between samples
	case strings.HasPrefix(string(qField), "power.draw"):
		return 50 // watts
	case strings.HasPrefix(string(qField), "memory."):
		return 512 // MiB; allocations move between samples
	case strings.HasPrefix(string(qField), "temperature."):
		return 3 // degrees
	default:
		return 1
	}
}

var numericValueRegex = regexp.MustCompile(`^[+-]?(\d*[.])?\d+`)

func parseNumeric(raw string) (float64, bool) {
	match := numericValueRegex.FindString(strings.TrimSpace(raw))
	if match == "" {
		return 0, false
	}

	value, err := strconv.ParseFloat(match, 64)

	return value, err == nil
}

func isAbsentValue(raw string) bool {
	if nvidiasmi.IsKnownAbsentValue(raw) {
		return true
	}

	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "[requested functionality has been deprecated]", "[function not found]":
		return true
	default:
		return false
	}
}

func queryExec(t *testing.T, ctx context.Context, resolved nvidiasmi.ResolvedFields) *nvidiasmi.Table {
	t.Helper()

	table, _, err := nvidiasmi.Query(ctx, nvidiasmi.DefaultCommand, resolved.Query, nvidiasmi.DefaultRunFunc)
	require.NoError(t, err)

	return table
}

func rowsByUUID(t *testing.T, table *nvidiasmi.Table) map[string]nvidiasmi.Row {
	t.Helper()

	rows := make(map[string]nvidiasmi.Row, len(table.Rows))
	for _, row := range table.Rows {
		uuid := nvidiasmi.NormalizeUUID(row.QFieldToCells[nvidiasmi.UUIDQField].RawValue)
		rows[uuid] = row
	}

	return rows
}

// TestBackendMetricFamilyParityOnRealGPU compares the rendered metric surface
// of the two backends: family names, label sets and series shape must agree
// for everything both serve, so a naming or labeling regression cannot hide
// behind matching raw values.
func TestBackendMetricFamilyParityOnRealGPU(t *testing.T) {
	execFamilies := scrapeBackend(t, "exec")
	nativeFamilies := scrapeBackend(t, "nvml")

	// the per-backend status families are the one intended difference
	delete(execFamilies, "nvidia_smi_command_exit_code")
	delete(nativeFamilies, "nvidia_smi_nvml_return_code")

	for family, execSeries := range execFamilies {
		nativeSeries, ok := nativeFamilies[family]
		if !ok {
			t.Errorf("metric family missing from the nvml backend\n"+
				"  family: %s (%d series on exec)\n"+
				"  fix: the catalog or collector in internal/nvmlnative; if the family comes from a "+
				"field the catalog defers, this is recorded drift",
				family, execSeries)

			continue
		}

		if execSeries != nativeSeries {
			t.Errorf("series count mismatch\n  family: %s\n  exec: %d series, nvml: %d series",
				family, execSeries, nativeSeries)
		}
	}

	for family := range nativeFamilies {
		if _, ok := execFamilies[family]; !ok {
			t.Errorf("metric family only the nvml backend exports\n  family: %s", family)
		}
	}
}

// scrapeBackend runs the exporter in-process with the given backend and
// returns nvidia_smi_* family -> series count.
func scrapeBackend(t *testing.T, backend string) map[string]int {
	t.Helper()

	baseURL := startExporter(t,
		"--web.listen-address=127.0.0.1:0",
		"--log.level=error",
		"--collect.backend="+backend,
	)
	body := scrape(t, baseURL)

	families := map[string]int{}

	for line := range strings.SplitSeq(body, "\n") {
		if !strings.HasPrefix(line, "nvidia_smi_") {
			continue
		}

		name := line
		if cut := strings.IndexAny(line, "{ "); cut > 0 {
			name = line[:cut]
		}

		families[name]++
	}

	require.NotEmpty(t, families, "backend %s served no nvidia_smi_ metrics", backend)

	return families
}
