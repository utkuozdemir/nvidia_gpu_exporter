//go:build gpu && linux && cgo

package integration_test

import (
	"context"
	"regexp"
	"slices"
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
//
//nolint:paralleltest // deliberately serial: both tests own the real GPU and NVML lifecycle
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
	execBefore := queryExec(ctx, t, resolved)
	nativeReading, _, err := backend.QueryFunc(nativeResolved, nvmlnative.CollectOptions{})(ctx)
	require.NoError(t, err)
	execAfter := queryExec(ctx, t, resolved)

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
//
//nolint:cyclop,funlen // one linear pass over the comparison rules keeps them readable
func compareField(
	t *testing.T,
	uuid string,
	qField nvidiasmi.QField,
	before, native, after nvidiasmi.Row,
) int {
	t.Helper()

	beforeCell, inExec := before.QFieldToCells[qField]
	nativeCell, inNative := native.QFieldToCells[qField]

	afterCell := after.QFieldToCells[qField]

	switch {
	case inExec && !inNative:
		// exec advertises a field the catalog lacks: drift when either
		// bracket carries a real value and the gap is not a recorded
		// deferral (mirrors the corpus test's rule for new fields)
		realValue := !isAbsentValue(beforeCell.RawValue) || !isAbsentValue(afterCell.RawValue)

		switch {
		case realValue && !nvmlnative.IsDeferredField(qField):
			t.Errorf("exec exports a field the nvml backend does not serve\n"+
				"  gpu: %s\n  field: %s\n  exec values: %q / %q\n"+
				"  fix: add it to the catalog in internal/nvmlnative, or record the deferral",
				uuid, qField, beforeCell.RawValue, afterCell.RawValue)

			return 1
		case realValue:
			t.Logf("recorded deferral with real values on this hardware: %s (%q)", qField, beforeCell.RawValue)
		}

		return 0
	case !inExec:
		return 0
	}

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
		// same absence is not enough: the exact token spelling is part of
		// the contract ([N/A] vs bare N/A vs the deprecation token), and
		// collapsing them would hide error-classification drift
		if nativeCell.RawValue != beforeCell.RawValue && nativeCell.RawValue != afterCell.RawValue {
			t.Errorf("absence token mismatch between the backends\n"+
				"  gpu: %s\n  field: %s\n  exec: %q / %q\n  nvml: %q\n"+
				"  fix: the error-to-token mapping for this field in internal/nvmlnative",
				uuid, qField, beforeCell.RawValue, afterCell.RawValue, nativeCell.RawValue)

			return 1
		}

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

var timestampShapeRegex = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}\.\d{3}$`)

// compareVolatile checks a volatile reading: the non-numeric remainder (the
// unit suffix) must match a bracket byte for byte, since that is where
// format drift lives; the numeric part must fall inside the bracket range,
// which may legitimately move. States must equal one of the brackets.
//
//nolint:cyclop,funlen // one linear pass over the comparison rules keeps them readable
func compareVolatile(t *testing.T, uuid string, qField nvidiasmi.QField, before, native, after string) int {
	t.Helper()

	if qField == "timestamp" {
		// wall-clock values always differ; only the shape is the contract
		if !timestampShapeRegex.MatchString(strings.TrimSpace(native)) {
			t.Errorf("timestamp shape mismatch\n  gpu: %s\n  nvml: %q\n"+
				"  fix: the timestamp layout in internal/nvmlnative/format.go", uuid, native)

			return 1
		}

		return 0
	}

	// hex bitmasks (clock reason masks) are states, not magnitudes: a
	// numeric-prefix parse would truncate 0x... to 0 and compare nothing
	if strings.HasPrefix(strings.TrimSpace(native), "0x") ||
		strings.HasPrefix(strings.TrimSpace(before), "0x") {
		if native != before && native != after {
			t.Errorf("volatile bitmask mismatch\n"+
				"  gpu: %s\n  field: %s\n  exec: %q / %q\n  nvml: %q\n"+
				"  fix: the mask formatting for this field in internal/nvmlnative",
				uuid, qField, before, after, native)

			return 1
		}

		return 0
	}

	beforeNum, beforeOK := parseNumeric(before)
	nativeNum, nativeOK := parseNumeric(native)
	afterNum, afterOK := parseNumeric(after)

	if nativeOK {
		nativeSuffix := numericValueRegex.ReplaceAllString(strings.TrimSpace(native), "")
		beforeSuffix := numericValueRegex.ReplaceAllString(strings.TrimSpace(before), "")
		afterSuffix := numericValueRegex.ReplaceAllString(strings.TrimSpace(after), "")

		if nativeSuffix != beforeSuffix && nativeSuffix != afterSuffix {
			t.Errorf("unit or format suffix mismatch on a volatile field\n"+
				"  gpu: %s\n  field: %s\n  exec: %q / %q\n  nvml: %q\n"+
				"  fix: the formatter for this field in internal/nvmlnative/format.go",
				uuid, qField, before, after, native)

			return 1
		}
	}

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

func queryExec(ctx context.Context, t *testing.T, resolved nvidiasmi.ResolvedFields) *nvidiasmi.Table {
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
// of the two backends: for every family both serve, the full series identity
// set (family plus complete label name/value pairs) must be equal, so a
// naming or labeling regression cannot hide behind matching raw values. A
// family only exec serves is a failure unless it derives from a field the
// catalog knowingly lacks, in which case it is reported, mirroring the
// corpus drift rule.
//
//nolint:paralleltest // deliberately serial: both tests own the real GPU and NVML lifecycle
func TestBackendMetricFamilyParityOnRealGPU(t *testing.T) {
	execFamilies := scrapeBackend(t, "exec")
	nativeFamilies := scrapeBackend(t, "nvml")

	// the per-backend status families are the one intended difference
	delete(execFamilies, "nvidia_smi_command_exit_code")
	delete(nativeFamilies, "nvidia_smi_nvml_return_code")

	// the always-on nvml-only families must be present, not merely allowed:
	// a silently broken extras path must not pass as an accepted absence
	// (the energy counter requires Volta or newer, which every GPU box
	// qualifies as)
	if !hasFamilyWithPrefix(nativeFamilies, "nvidia_smi_energy_joules_total") {
		t.Error("nvml-only metric family missing from the nvml backend: nvidia_smi_energy_joules_total")
	}

	// cuda_version is derived independently per backend (the nvidia-smi
	// --version text vs the NVML call), so a mismatch would fail the whole
	// gpu_info family with a message that never mentions it; compare the
	// label explicitly instead
	execCuda := cudaVersionValues(execFamilies)
	require.NotContains(t, execCuda, "",
		"the exec backend parsed no CUDA version from nvidia-smi --version")
	require.Equal(t, execCuda, cudaVersionValues(nativeFamilies),
		"the two backends disagree on the cuda_version label")

	// the opt-in PCIe throughput family is exercised under its flag
	pcieFamilies := scrapeBackend(t, "nvml", "--collect.pcie-throughput")
	for _, family := range []string{
		"nvidia_smi_pcie_throughput_tx_bytes_per_second",
		"nvidia_smi_pcie_throughput_rx_bytes_per_second",
	} {
		if _, ok := pcieFamilies[family]; !ok {
			t.Errorf("metric family missing with --collect.pcie-throughput: %s", family)
		}
	}

	for family, execSeries := range execFamilies {
		nativeSeries, ok := nativeFamilies[family]
		if !ok {
			t.Errorf("metric family missing from the nvml backend\n"+
				"  family: %s (%d exec series, e.g. %s)\n"+
				"  fix: the catalog or collector in internal/nvmlnative; if the family comes from a "+
				"field the catalog defers, this is recorded drift",
				family, len(execSeries), anySeries(execSeries))

			continue
		}

		for series := range execSeries {
			if _, ok := nativeSeries[series]; !ok {
				t.Errorf("series missing from the nvml backend\n  family: %s\n  series: %s\n"+
					"  fix: label construction for this family in internal/exporter or the nvml collector",
					family, series)
			}
		}

		for series := range nativeSeries {
			if _, ok := execSeries[series]; !ok {
				t.Errorf("series only the nvml backend exports\n  family: %s\n  series: %s",
					family, series)
			}
		}
	}

	for family := range nativeFamilies {
		if _, ok := execFamilies[family]; !ok && !isNVMLOnlyFamily(family) {
			t.Errorf("metric family only the nvml backend exports\n  family: %s", family)
		}
	}
}

// nvmlOnlyFamilyPrefixes lists the metric family prefixes only the nvml
// backend serves (the extras families). They are exempt from the family
// parity requirement in the nvml direction, nothing more: presence is
// asserted per scenario (always-on families in the default scrape, opt-in
// families under their flag), since a single list doing both would force
// every entry to appear in every scrape. Exec-only families remain a hard
// failure.
//
//nolint:gochecknoglobals // shared test fixture
var nvmlOnlyFamilyPrefixes = []string{
	"nvidia_smi_energy_joules_total",
	"nvidia_smi_pcie_throughput_",
}

func isNVMLOnlyFamily(family string) bool {
	for _, prefix := range nvmlOnlyFamilyPrefixes {
		if strings.HasPrefix(family, prefix) {
			return true
		}
	}

	return false
}

func hasFamilyWithPrefix(families map[string]map[string]bool, prefix string) bool {
	for family := range families {
		if strings.HasPrefix(family, prefix) {
			return true
		}
	}

	return false
}

// cudaVersionValues extracts the sorted set of cuda_version label values off
// the gpu_info series.
func cudaVersionValues(families map[string]map[string]bool) []string {
	pattern := regexp.MustCompile(`cuda_version="([^"]*)"`)
	seen := map[string]bool{}

	for series := range families["nvidia_smi_gpu_info"] {
		if match := pattern.FindStringSubmatch(series); match != nil {
			seen[match[1]] = true
		}
	}

	values := make([]string, 0, len(seen))
	for value := range seen {
		values = append(values, value)
	}

	slices.Sort(values)

	return values
}

func anySeries(series map[string]bool) string {
	for s := range series {
		return s
	}

	return ""
}

// scrapeBackend runs the exporter in-process with the given backend and
// returns nvidia_smi_* family -> set of series signatures (name{labels}).
func scrapeBackend(t *testing.T, backend string, extraArgs ...string) map[string]map[string]bool {
	t.Helper()

	// startExporter injects the listen address and log level itself
	args := append([]string{"--collect.backend=" + backend}, extraArgs...)

	baseURL := startExporter(t, args...)
	body := scrape(t, baseURL)

	families := map[string]map[string]bool{}

	for line := range strings.SplitSeq(body, "\n") {
		if !strings.HasPrefix(line, "nvidia_smi_") {
			continue
		}

		// series signature: metric name plus the full label block, value cut
		signature := line
		if cut := strings.LastIndex(line, " "); cut > 0 {
			signature = line[:cut]
		}

		name := signature
		if cut := strings.IndexAny(signature, "{"); cut > 0 {
			name = signature[:cut]
		}

		if families[name] == nil {
			families[name] = map[string]bool{}
		}

		families[name][signature] = true
	}

	require.NotEmpty(t, families, "backend %s served no nvidia_smi_ metrics", backend)

	return families
}
