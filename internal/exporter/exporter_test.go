package exporter_test

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/neilotoole/slogt/v2"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thejerf/slogassert"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/exporter"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

const delta = 1e-9

//go:embed testdata/query.txt
var queryTest string

func assertFloat(t *testing.T, expected, actual float64) {
	t.Helper()

	assert.InDelta(t, expected, actual, delta)
}

func TestBuildFQNameAndMultiplierRegular(t *testing.T) {
	t.Parallel()

	fqName, multiplier := exporter.BuildFQNameAndMultiplier(
		"prefix",
		"encoder.stats.sessionCount",
		slogt.New(t),
	)

	assertFloat(t, 1, multiplier)
	assert.Equal(t, "prefix_encoder_stats_session_count", fqName)
}

func TestBuildFQNameAndMultiplierWatts(t *testing.T) {
	t.Parallel()

	fqName, multiplier := exporter.BuildFQNameAndMultiplier(
		"prefix",
		"power.draw [W]",
		slogt.New(t),
	)

	assertFloat(t, 1, multiplier)
	assert.Equal(t, "prefix_power_draw_watts", fqName)
}

func TestBuildFQNameAndMultiplierMiB(t *testing.T) {
	t.Parallel()

	fqName, multiplier := exporter.BuildFQNameAndMultiplier(
		"prefix",
		"memory.total [MiB]",
		slogt.New(t),
	)

	assertFloat(t, 1048576, multiplier)
	assert.Equal(t, "prefix_memory_total_bytes", fqName)
}

func TestBuildFQNameAndMultiplierMHZ(t *testing.T) {
	t.Parallel()

	fqName, multiplier := exporter.BuildFQNameAndMultiplier(
		"prefix",
		"clocks.current.graphics [MHz]",
		slogt.New(t),
	)

	assertFloat(t, 1000000, multiplier)
	assert.Equal(t, "prefix_clocks_current_graphics_clock_hz", fqName)
}

func TestBuildFQNameAndMultiplierRatio(t *testing.T) {
	t.Parallel()

	fqName, multiplier := exporter.BuildFQNameAndMultiplier("prefix", "fan.speed [%]", slogt.New(t))

	assertFloat(t, 0.01, multiplier)
	assert.Equal(t, "prefix_fan_speed_ratio", fqName)
}

func TestBuildFQNameAndMultiplierMicroseconds(t *testing.T) {
	t.Parallel()

	fqName, multiplier := exporter.BuildFQNameAndMultiplier(
		"prefix",
		"clocks_event_reasons_counters.sw_thermal_slowdown [us]",
		slogt.New(t),
	)

	assertFloat(t, 0.000001, multiplier)
	assert.Equal(t, "prefix_clocks_event_reasons_counters_sw_thermal_slowdown_seconds", fqName)
}

func TestBuildFQNameAndMultiplierMilliseconds(t *testing.T) {
	t.Parallel()

	// seen on driver 590.48: power_smoothing.window_multiplier [ms]
	fqName, multiplier := exporter.BuildFQNameAndMultiplier(
		"prefix",
		"power_smoothing.window_multiplier [ms]",
		slogt.New(t),
	)

	assertFloat(t, 0.001, multiplier)
	assert.Equal(t, "prefix_power_smoothing_window_multiplier_seconds", fqName)
}

func TestBuildFQNameAndMultiplierNoPrefix(t *testing.T) {
	t.Parallel()

	fqName, multiplier := exporter.BuildFQNameAndMultiplier(
		"",
		"encoder.stats.sessionCount",
		slogt.New(t),
	)

	assertFloat(t, 1, multiplier)
	assert.Equal(t, "encoder_stats_session_count", fqName)
}

func TestBuildMetricInfo(t *testing.T) {
	t.Parallel()

	metricInfo := exporter.BuildMetricInfo("prefix", "encoder.stats.sessionCount", slogt.New(t))

	assertFloat(t, 1, metricInfo.ValueMultiplier)
	assert.Equal(t, prometheus.GaugeValue, metricInfo.MType)
}

func TestBuildMetricInfoInvalidName(t *testing.T) {
	t.Parallel()

	handler := slogassert.New(t, slog.LevelError, nil)
	logger := slog.New(handler)

	exporter.BuildMetricInfo("prefix", "foo.bar [asdf]", logger)

	handler.AssertMessage(
		"returned field contains unexpected characters, it is parsed it with best effort, " +
			"but it might get renamed in the future. please report it in the project's issue tracker",
	)
}

func TestBuildQFieldToMetricInfoMap(t *testing.T) {
	t.Parallel()

	logger := slogt.New(t)
	qFieldToMetricInfoMap := exporter.BuildQFieldToMetricInfoMap(
		"prefix",
		map[nvidiasmi.QField]nvidiasmi.RField{"aaa": "AAA", "bbb": "BBB"},
		logger,
	)

	assert.Len(t, qFieldToMetricInfoMap, 2)

	metricInfo1 := qFieldToMetricInfoMap["aaa"]
	assertFloat(t, 1, metricInfo1.ValueMultiplier)
	assert.Equal(t, prometheus.GaugeValue, metricInfo1.MType)

	metricInfo2 := qFieldToMetricInfoMap["bbb"]
	assertFloat(t, 1, metricInfo2.ValueMultiplier)
	assert.Equal(t, prometheus.GaugeValue, metricInfo2.MType)
}

// newTestExporter resolves fields against a nonexistent nvidia-smi command
// (falling back to the built-in mapping) and wires the exporter to a live
// source whose query is backed by the given run function.
func newTestExporter(
	t *testing.T,
	prefix string,
	qFieldsRaw string,
	run nvidiasmi.RunFunc,
) *exporter.GPUExporter {
	t.Helper()

	logger := slogt.New(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)

	resolved, err := nvidiasmi.ResolveFields(ctx, "bbb", qFieldsRaw, "", 0, nvidiasmi.DefaultRunFunc, logger)
	require.NoError(t, err)

	query := func(queryCtx context.Context) (collect.Reading, int, error) {
		table, exitCode, err := nvidiasmi.Query(queryCtx, "bbb", resolved.Query, run)
		if err != nil {
			return collect.Reading{}, exitCode, fmt.Errorf("query failed: %w", err)
		}

		return collect.Reading{Table: table}, exitCode, nil
	}

	source := collect.NewLive(query, 0, nil, logger)

	return exporter.New(ctx, prefix, resolved, source, exporter.Features{}, nil, exporter.ExecExitCodeMetric, logger)
}

// staticSource serves a fixed snapshot, for driving the render paths directly.
type staticSource struct {
	snapshot collect.Snapshot
}

func (s *staticSource) Latest(_ context.Context) collect.Snapshot {
	return s.snapshot
}

// newAppsExporter wires an exporter with per-process metrics enabled to a
// fixed snapshot.
func newAppsExporter(t *testing.T, snapshot collect.Snapshot) *exporter.GPUExporter {
	t.Helper()

	logger := slogt.New(t)

	resolved, err := nvidiasmi.ResolveFields(
		t.Context(), "bbb", "fan.speed", "", 0, nvidiasmi.DefaultRunFunc, logger)
	require.NoError(t, err)

	return exporter.New(
		t.Context(),
		"aaa",
		resolved,
		&staticSource{snapshot: snapshot},
		exporter.Features{ComputeApps: true},
		nil,
		exporter.ExecExitCodeMetric,
		logger,
	)
}

// newExtrasExporter wires an exporter with the given features to a fixed
// snapshot, for driving the extras render paths directly.
func newExtrasExporter(t *testing.T, features exporter.Features, snapshot collect.Snapshot) *exporter.GPUExporter {
	t.Helper()

	logger := slogt.New(t)

	resolved, err := nvidiasmi.ResolveFields(
		t.Context(), "bbb", "fan.speed", "", 0, nvidiasmi.DefaultRunFunc, logger)
	require.NoError(t, err)

	return exporter.New(
		t.Context(),
		"aaa",
		resolved,
		&staticSource{snapshot: snapshot},
		features,
		nil,
		exporter.ExecExitCodeMetric,
		logger,
	)
}

// gpuTable builds a minimal one-GPU table carrying just the uuid cell.
func gpuTable(uuid string) *nvidiasmi.Table {
	cell := nvidiasmi.Cell{QField: nvidiasmi.UUIDQField, RField: "uuid", RawValue: uuid}

	return &nvidiasmi.Table{
		Rows: []nvidiasmi.Row{{
			QFieldToCells: map[nvidiasmi.QField]nvidiasmi.Cell{nvidiasmi.UUIDQField: cell},
			Cells:         []nvidiasmi.Cell{cell},
		}},
	}
}

func TestDescribe(t *testing.T) {
	t.Parallel()

	const prefix = "aaa"

	exp := newTestExporter(t, prefix, "fan.speed,memory.used", nvidiasmi.DefaultRunFunc)

	doneCh := make(chan bool)
	descCh := make(chan *prometheus.Desc)

	go func() {
		exp.Describe(descCh)

		doneCh <- true
	}()

	var descStrs []string

end:
	for {
		select {
		case desc := <-descCh:
			descStrs = append(descStrs, desc.String())
		case <-doneCh:
			break end
		}
	}

	slices.Sort(descStrs)

	expectedMetrics := []string{
		"fan_speed_ratio", "memory_used_bytes", "failed_scrapes_total", "gpu_info",
		"uuid", "name", "driver_model_current", "driver_model_pending",
		"vbios_version", "driver_version", "pci_bus_id", "serial",
		"compute_cap", "pci_sub_device_id", "index", "command_exit_code",
		"last_collect_success", "last_collect_success_timestamp_seconds",
		"last_collect_duration_seconds",
	}

	slices.Sort(expectedMetrics)

	assert.Len(t, descStrs, len(expectedMetrics))

	for i, metric := range expectedMetrics {
		descStr := descStrs[i]

		assert.Contains(t, descStr, fmt.Sprintf(`"%s_%s"`, prefix, metric))
	}
}

// gatherFamilies scrapes the exporter through a pedantic registry and returns
// the metric families by name.
func gatherFamilies(t *testing.T, exp *exporter.GPUExporter) map[string]*dto.MetricFamily {
	t.Helper()

	registry := prometheus.NewPedanticRegistry()
	require.NoError(t, registry.Register(exp))

	families, err := registry.Gather()
	require.NoError(t, err)

	byName := make(map[string]*dto.MetricFamily, len(families))
	for _, family := range families {
		byName[family.GetName()] = family
	}

	return byName
}

func gaugeValue(t *testing.T, families map[string]*dto.MetricFamily, name string) float64 {
	t.Helper()

	family, ok := families[name]
	require.True(t, ok, "metric family %q not found", name)
	require.Len(t, family.GetMetric(), 1)

	return family.GetMetric()[0].GetGauge().GetValue()
}

func TestCollect(t *testing.T) {
	t.Parallel()

	exp := newTestExporter(
		t,
		"aaa",
		"uuid,name,driver_model.current,driver_model.pending,"+
			"vbios_version,driver_version,fan.speed,memory.used,pci.bus_id",
		func(cmd *exec.Cmd) error {
			_, _ = cmd.Stdout.Write([]byte(queryTest))

			return nil
		},
	)

	families := gatherFamilies(t, exp)

	// collection health: one successful collection, nothing failed
	failed, ok := families["aaa_failed_scrapes_total"]
	require.True(t, ok)
	assertFloat(t, 0, failed.GetMetric()[0].GetCounter().GetValue())
	assertFloat(t, 1, gaugeValue(t, families, "aaa_last_collect_success"))
	assertFloat(t, 0, gaugeValue(t, families, "aaa_command_exit_code"))
	assert.Positive(t, gaugeValue(t, families, "aaa_last_collect_success_timestamp_seconds"))
	assert.GreaterOrEqual(t, gaugeValue(t, families, "aaa_last_collect_duration_seconds"), 0.0)

	const rtxUUID = "df6e7a7c-7314-46f8-abc4-b88b36dcf3aa"

	// GPU data: both rows from the canned nvidia-smi output
	info, ok := families["aaa_gpu_info"]
	require.True(t, ok)
	require.Len(t, info.GetMetric(), 2)

	infoLabels := make(map[string]string)
	for _, labelPair := range metricByUUID(t, info, rtxUUID).GetLabel() {
		infoLabels[labelPair.GetName()] = labelPair.GetValue()
	}

	assert.Equal(t, "NVIDIA GeForce RTX 2080 SUPER", infoLabels["name"])
	assert.Equal(t, "7.5", infoLabels["compute_cap"])

	fanSpeed, ok := families["aaa_fan_speed_ratio"]
	require.True(t, ok)
	require.Len(t, fanSpeed.GetMetric(), 2)
	assertFloat(t, 0.38, metricByUUID(t, fanSpeed, rtxUUID).GetGauge().GetValue())

	memoryUsed, ok := families["aaa_memory_used_bytes"]
	require.True(t, ok)
	assertFloat(t, 575*1048576, metricByUUID(t, memoryUsed, rtxUUID).GetGauge().GetValue())
}

// metricByUUID returns the metric in the family carrying the given uuid label.
func metricByUUID(t *testing.T, family *dto.MetricFamily, uuid string) *dto.Metric {
	t.Helper()

	for _, metric := range family.GetMetric() {
		for _, labelPair := range metric.GetLabel() {
			if labelPair.GetName() == "uuid" && labelPair.GetValue() == uuid {
				return metric
			}
		}
	}

	t.Fatalf("no metric with uuid %q in family %q", uuid, family.GetName())

	return nil
}

func TestCollectError(t *testing.T) {
	t.Parallel()

	exp := newTestExporter(t, "aaa", "fan.speed,memory.used", nvidiasmi.DefaultRunFunc)

	families := gatherFamilies(t, exp)

	// one failed collection, never a successful one
	failed, ok := families["aaa_failed_scrapes_total"]
	require.True(t, ok)
	assertFloat(t, 1, failed.GetMetric()[0].GetCounter().GetValue())
	assertFloat(t, 0, gaugeValue(t, families, "aaa_last_collect_success"))
	assertFloat(t, -1, gaugeValue(t, families, "aaa_command_exit_code"))
	assert.GreaterOrEqual(t, gaugeValue(t, families, "aaa_last_collect_duration_seconds"), 0.0)

	// no success yet: the timestamp and all GPU series must be absent
	assert.NotContains(t, families, "aaa_last_collect_success_timestamp_seconds")
	assert.NotContains(t, families, "aaa_gpu_info")
	assert.NotContains(t, families, "aaa_fan_speed_ratio")

	// the failure counter advances per collection
	families = gatherFamilies(t, exp)
	failed, ok = families["aaa_failed_scrapes_total"]
	require.True(t, ok)
	assertFloat(t, 2, failed.GetMetric()[0].GetCounter().GetValue())
}

// TestCollectDeliversMetricsOnFatalError pins the shutdown-on-error contract:
// the collection that triggers the shutdown cancels the exporter context
// before rendering, and the final scrape must still carry the health metrics
// that explain what happened.
func TestCollectDeliversMetricsOnFatalError(t *testing.T) {
	t.Parallel()

	logger := slogt.New(t)

	ctx, cancel := context.WithCancelCause(t.Context())
	t.Cleanup(func() { cancel(nil) })

	resolved, err := nvidiasmi.ResolveFields(ctx, "bbb", "fan.speed", "", 0, nvidiasmi.DefaultRunFunc, logger)
	require.NoError(t, err)

	// a real non-zero exit, so the shutdown callback fires
	query := func(queryCtx context.Context) (collect.Reading, int, error) {
		runErr := exec.CommandContext(queryCtx, "sh", "-c", "exit 3").Run()

		return collect.Reading{}, 3, fmt.Errorf("query failed: %w", runErr)
	}

	source := collect.NewLive(query, 0, func(fatalErr error) { cancel(fatalErr) }, logger)
	exp := exporter.New(ctx, "aaa", resolved, source, exporter.Features{}, nil, exporter.ExecExitCodeMetric, logger)

	families := gatherFamilies(t, exp)

	// the context is cancelled by now, and the metrics still made it out
	require.Error(t, context.Cause(ctx))

	failed, ok := families["aaa_failed_scrapes_total"]
	require.True(t, ok)
	assertFloat(t, 1, failed.GetMetric()[0].GetCounter().GetValue())
	assertFloat(t, 0, gaugeValue(t, families, "aaa_last_collect_success"))
	assertFloat(t, 3, gaugeValue(t, families, "aaa_command_exit_code"))
}

func appsSnapshot(table *nvidiasmi.Table, apps []nvidiasmi.ComputeApp, appsSuccess bool) collect.Snapshot {
	return collect.Snapshot{
		Attempted:     true,
		Success:       true,
		Table:         table,
		Apps:          apps,
		AppsAttempted: true,
		AppsSuccess:   appsSuccess,
		LastSuccess:   time.Now(),
	}
}

func TestCollectComputeApps(t *testing.T) {
	t.Parallel()

	apps := []nvidiasmi.ComputeApp{
		{GPUUUID: "abc", PID: "42", ProcessName: "/usr/bin/burn", UsedMemory: "10 MiB"},
		{GPUUUID: "abc", PID: "43", ProcessName: `C:\Windows\System32\dwm.exe`, UsedMemory: "[N/A]"},
	}

	exp := newAppsExporter(t, appsSnapshot(gpuTable("GPU-ABC"), apps, true))
	families := gatherFamilies(t, exp)

	assertFloat(t, 1, gaugeValue(t, families, "aaa_compute_apps_last_collect_success"))

	info, ok := families["aaa_compute_app_info"]
	require.True(t, ok)
	require.Len(t, info.GetMetric(), 2)

	// the [N/A] memory value is an expected state: info present, memory absent
	memory, ok := families["aaa_compute_app_used_memory_bytes"]
	require.True(t, ok)
	require.Len(t, memory.GetMetric(), 1)
	assertFloat(t, 10*1024*1024, memory.GetMetric()[0].GetGauge().GetValue())

	labels := map[string]string{}
	for _, pair := range memory.GetMetric()[0].GetLabel() {
		labels[pair.GetName()] = pair.GetValue()
	}

	assert.Equal(t, map[string]string{
		"uuid": "abc", "pid": "42", "process_name": "/usr/bin/burn",
	}, labels)

	assertFloat(t, 2, gaugeValue(t, families, "aaa_compute_apps"))
}

func TestCollectComputeAppsZeroProcesses(t *testing.T) {
	t.Parallel()

	exp := newAppsExporter(t, appsSnapshot(gpuTable("GPU-DEF"), nil, true))
	families := gatherFamilies(t, exp)

	// an idle GPU reports an explicit 0, distinguishable from a failed query
	assertFloat(t, 1, gaugeValue(t, families, "aaa_compute_apps_last_collect_success"))
	assertFloat(t, 0, gaugeValue(t, families, "aaa_compute_apps"))
	assert.NotContains(t, families, "aaa_compute_app_info")
	assert.NotContains(t, families, "aaa_compute_app_used_memory_bytes")
}

func TestCollectComputeAppsFailureSuppressesSeries(t *testing.T) {
	t.Parallel()

	// a failed per-process query must not look like an idle GPU: only the
	// success gauge is emitted, and no count series reads 0
	exp := newAppsExporter(t, appsSnapshot(gpuTable("GPU-ABC"), nil, false))
	families := gatherFamilies(t, exp)

	assertFloat(t, 0, gaugeValue(t, families, "aaa_compute_apps_last_collect_success"))
	assert.NotContains(t, families, "aaa_compute_apps")
	assert.NotContains(t, families, "aaa_compute_app_info")
	assert.NotContains(t, families, "aaa_compute_app_used_memory_bytes")
}

func TestCollectComputeAppsDisabled(t *testing.T) {
	t.Parallel()

	logger := slogt.New(t)

	resolved, err := nvidiasmi.ResolveFields(
		t.Context(), "bbb", "fan.speed", "", 0, nvidiasmi.DefaultRunFunc, logger)
	require.NoError(t, err)

	// even a snapshot carrying apps data produces no per-process series when
	// the feature is off
	apps := []nvidiasmi.ComputeApp{{GPUUUID: "abc", PID: "42", ProcessName: "x", UsedMemory: "1 MiB"}}
	source := &staticSource{snapshot: appsSnapshot(gpuTable("GPU-ABC"), apps, true)}
	exp := exporter.New(
		t.Context(),
		"aaa",
		resolved,
		source,
		exporter.Features{},
		nil,
		exporter.ExecExitCodeMetric,
		logger,
	)

	families := gatherFamilies(t, exp)

	assert.NotContains(t, families, "aaa_compute_apps_last_collect_success")
	assert.NotContains(t, families, "aaa_compute_apps")
	assert.NotContains(t, families, "aaa_compute_app_info")
	assert.NotContains(t, families, "aaa_compute_app_used_memory_bytes")
}

// extrasSnapshot builds a successful snapshot carrying the given extras.
func extrasSnapshot(table *nvidiasmi.Table, extras collect.Extras) collect.Snapshot {
	return collect.Snapshot{
		Attempted:   true,
		Success:     true,
		Table:       table,
		Extras:      extras,
		LastSuccess: time.Now(),
	}
}

// labelValue reads one label's value off a rendered metric.
func labelValue(t *testing.T, metric *dto.Metric, name string) string {
	t.Helper()

	for _, label := range metric.GetLabel() {
		if label.GetName() == name {
			return label.GetValue()
		}
	}

	t.Fatalf("label %s not found", name)

	return ""
}

func TestExtrasRendered(t *testing.T) {
	t.Parallel()

	extras := collect.Extras{
		CUDAVersion: "13.1",
		PCIe: []collect.PCIeThroughput{
			{UUID: "abc", TXBytesPerSecond: 123000, RXBytesPerSecond: 456000},
		},
		Energy: []collect.EnergyCounter{{UUID: "abc", Joules: 12345.678}},
	}

	features := exporter.Features{PCIeThroughput: true, Energy: true}
	exp := newExtrasExporter(t, features, extrasSnapshot(gpuTable("GPU-ABC"), extras))

	families := gatherFamilies(t, exp)

	tx, ok := families["aaa_pcie_throughput_tx_bytes_per_second"]
	require.True(t, ok)
	assertFloat(t, 123000, tx.GetMetric()[0].GetGauge().GetValue())
	assert.Equal(t, "abc", labelValue(t, tx.GetMetric()[0], "uuid"))

	rx, ok := families["aaa_pcie_throughput_rx_bytes_per_second"]
	require.True(t, ok)
	assertFloat(t, 456000, rx.GetMetric()[0].GetGauge().GetValue())

	energy, ok := families["aaa_energy_joules_total"]
	require.True(t, ok)
	assert.Equal(t, dto.MetricType_COUNTER, energy.GetType())
	assertFloat(t, 12345.678, energy.GetMetric()[0].GetCounter().GetValue())
	assert.Equal(t, "abc", labelValue(t, energy.GetMetric()[0], "uuid"))

	info, ok := families["aaa_gpu_info"]
	require.True(t, ok)
	assert.Equal(t, "13.1", labelValue(t, info.GetMetric()[0], "cuda_version"))
}

func TestExtrasSuppressedWhenFeaturesOff(t *testing.T) {
	t.Parallel()

	// even a snapshot carrying extras data produces none of the gated
	// families when the features are off
	extras := collect.Extras{
		CUDAVersion: "13.1",
		PCIe:        []collect.PCIeThroughput{{UUID: "abc", TXBytesPerSecond: 1, RXBytesPerSecond: 2}},
		Energy:      []collect.EnergyCounter{{UUID: "abc", Joules: 3}},
	}

	exp := newExtrasExporter(t, exporter.Features{}, extrasSnapshot(gpuTable("GPU-ABC"), extras))

	families := gatherFamilies(t, exp)

	assert.NotContains(t, families, "aaa_pcie_throughput_tx_bytes_per_second")
	assert.NotContains(t, families, "aaa_pcie_throughput_rx_bytes_per_second")
	assert.NotContains(t, families, "aaa_energy_joules_total")

	// the cuda_version label is not feature-gated: it rides gpu_info in both
	// backends
	info, ok := families["aaa_gpu_info"]
	require.True(t, ok)
	assert.Equal(t, "13.1", labelValue(t, info.GetMetric()[0], "cuda_version"))
}

func TestCudaVersionLabelEmptyWhenUnknown(t *testing.T) {
	t.Parallel()

	exp := newExtrasExporter(t, exporter.Features{}, extrasSnapshot(gpuTable("GPU-ABC"), collect.Extras{}))

	families := gatherFamilies(t, exp)

	info, ok := families["aaa_gpu_info"]
	require.True(t, ok)
	assert.Empty(t, labelValue(t, info.GetMetric()[0], "cuda_version"))
}

func migExtras() collect.Extras {
	util := func(v float64) *float64 { return &v }

	shared := &collect.MIGUtilization{
		GraphicsActivityRatio: util(0.5),
		SMActivityRatio:       util(0.9),
		// SMOccupancy deliberately nil: a per-metric gap must render nothing
		TensorActivityRatio:  util(0.1),
		PCIeTXBytesPerSecond: util(1048576),
		PCIeRXBytesPerSecond: util(2097152),
	}

	return collect.Extras{
		MIG: []collect.MIGInstance{
			{
				ParentUUID: "abc", UUID: "mig-a", GPUInstanceID: "1", ComputeInstanceID: "0",
				Profile: "1g.10gb",
				Memory:  &collect.MIGMemory{Total: 1000, Used: 100, Free: 900, Reserved: 50},
				// two compute instances of the same GPU instance share the
				// utilization values
				Utilization: shared,
			},
			{
				ParentUUID: "abc", UUID: "mig-b", GPUInstanceID: "1", ComputeInstanceID: "1",
				Profile: "1g.10gb",
				// same GPU instance: the framebuffer is shared, the values
				// repeat (as on real hardware)
				Memory:      &collect.MIGMemory{Total: 1000, Used: 100, Free: 900, Reserved: 50},
				Utilization: shared,
			},
			{
				ParentUUID: "abc", UUID: "mig-c", GPUInstanceID: "2", ComputeInstanceID: "0",
				Profile: "2g.20gb",
				// memory and utilization unreadable: only the info series
			},
		},
	}
}

func TestMIGExtrasRendered(t *testing.T) {
	t.Parallel()

	exp := newExtrasExporter(t, exporter.Features{MIG: true}, extrasSnapshot(gpuTable("GPU-ABC"), migExtras()))

	families := gatherFamilies(t, exp)

	info, ok := families["aaa_mig_info"]
	require.True(t, ok)
	require.Len(t, info.GetMetric(), 3)

	first := info.GetMetric()[0]
	assert.Equal(t, "abc", labelValue(t, first, "uuid"))
	assert.Equal(t, "mig-a", labelValue(t, first, "mig_uuid"))
	assert.Equal(t, "1", labelValue(t, first, "gpu_instance_id"))
	assert.Equal(t, "0", labelValue(t, first, "compute_instance_id"))
	assert.Equal(t, "1g.10gb", labelValue(t, first, "profile"))

	memUsed, ok := families["aaa_mig_memory_used_bytes"]
	require.True(t, ok)
	require.Len(t, memUsed.GetMetric(), 1,
		"memory is per GPU instance: one series for the two-slice instance, none for the unreadable one")
	assert.Equal(t, "1", labelValue(t, memUsed.GetMetric()[0], "gpu_instance_id"))
	assertFloat(t, 100, memUsed.GetMetric()[0].GetGauge().GetValue())

	smActivity, ok := families["aaa_mig_sm_activity_ratio"]
	require.True(t, ok)
	require.Len(t, smActivity.GetMetric(), 1,
		"one GPU instance hosting two compute instances must emit its utilization once")
	assert.Equal(t, "1", labelValue(t, smActivity.GetMetric()[0], "gpu_instance_id"))
	assertFloat(t, 0.9, smActivity.GetMetric()[0].GetGauge().GetValue())

	_, ok = families["aaa_mig_sm_occupancy_ratio"]
	assert.False(t, ok, "a nil per-metric value must render nothing")

	pcieTx, ok := families["aaa_mig_pcie_throughput_tx_bytes_per_second"]
	require.True(t, ok)
	assertFloat(t, 1048576, pcieTx.GetMetric()[0].GetGauge().GetValue())
}

func TestMIGSuppressedWhenOff(t *testing.T) {
	t.Parallel()

	exp := newExtrasExporter(t, exporter.Features{}, extrasSnapshot(gpuTable("GPU-ABC"), migExtras()))

	families := gatherFamilies(t, exp)

	for family := range families {
		assert.NotContains(t, family, "mig_", "MIG families must not render when the feature is off")
	}
}

func TestComputeAppMIGLabels(t *testing.T) {
	t.Parallel()

	apps := []nvidiasmi.ComputeApp{{
		GPUUUID: "abc", PID: "42", ProcessName: "python",
		UsedMemory: "1 MiB", GPUInstanceID: "3", ComputeInstanceID: "0",
	}}

	snapshot := appsSnapshot(gpuTable("GPU-ABC"), apps, true)

	withLabels := newExtrasExporter(t,
		exporter.Features{ComputeApps: true, ComputeAppMIGLabels: true}, snapshot)
	families := gatherFamilies(t, withLabels)

	info, ok := families["aaa_compute_app_info"]
	require.True(t, ok)
	assert.Equal(t, "3", labelValue(t, info.GetMetric()[0], "gpu_instance_id"))
	assert.Equal(t, "0", labelValue(t, info.GetMetric()[0], "compute_instance_id"))

	// without the opt-in, the label set stays the shipped 3-label one
	withoutLabels := newExtrasExporter(t, exporter.Features{ComputeApps: true}, snapshot)
	families = gatherFamilies(t, withoutLabels)

	info, ok = families["aaa_compute_app_info"]
	require.True(t, ok)
	require.Len(t, info.GetMetric()[0].GetLabel(), 3)
}

// staticXIDs is a canned XIDSource.
type staticXIDs struct {
	counters []collect.XIDCounter
}

func (s *staticXIDs) XIDCounts() []collect.XIDCounter { return s.counters }

func TestXIDsRenderEvenWhenCollectionFails(t *testing.T) {
	t.Parallel()

	logger := slogt.New(t)

	resolved, err := nvidiasmi.ResolveFields(
		t.Context(), "bbb", "fan.speed", "", 0, nvidiasmi.DefaultRunFunc, logger)
	require.NoError(t, err)

	xids := &staticXIDs{counters: []collect.XIDCounter{
		{UUID: "abc", XID: 79, Count: 3, LastSeen: time.Unix(1700000000, 0)},
	}}

	// a failed collection: no table at all, which is exactly when the XID
	// counters must stay visible
	failed := collect.Snapshot{Attempted: true, Success: false, Failures: 1}

	exp := exporter.New(
		t.Context(),
		"aaa",
		resolved,
		&staticSource{snapshot: failed},
		exporter.Features{XIDEvents: true},
		xids,
		exporter.ExecExitCodeMetric,
		logger,
	)

	families := gatherFamilies(t, exp)

	counts, ok := families["aaa_xid_errors_total"]
	require.True(t, ok, "xid counters must render without a table")
	assert.Equal(t, dto.MetricType_COUNTER, counts.GetType())
	assertFloat(t, 3, counts.GetMetric()[0].GetCounter().GetValue())
	assert.Equal(t, "abc", labelValue(t, counts.GetMetric()[0], "uuid"))
	assert.Equal(t, "79", labelValue(t, counts.GetMetric()[0], "xid"))

	stamps, ok := families["aaa_xid_last_timestamp_seconds"]
	require.True(t, ok)
	assertFloat(t, 1700000000, stamps.GetMetric()[0].GetGauge().GetValue())
}

func TestXIDsSuppressedWhenOff(t *testing.T) {
	t.Parallel()

	logger := slogt.New(t)

	resolved, err := nvidiasmi.ResolveFields(
		t.Context(), "bbb", "fan.speed", "", 0, nvidiasmi.DefaultRunFunc, logger)
	require.NoError(t, err)

	// a live, populated source: the feature gate alone must suppress the
	// families
	xids := &staticXIDs{counters: []collect.XIDCounter{{UUID: "abc", XID: 79, Count: 3}}}

	exp := exporter.New(
		t.Context(),
		"aaa",
		resolved,
		&staticSource{snapshot: extrasSnapshot(gpuTable("GPU-ABC"), collect.Extras{})},
		exporter.Features{},
		xids,
		exporter.ExecExitCodeMetric,
		logger,
	)

	families := gatherFamilies(t, exp)

	assert.NotContains(t, families, "aaa_xid_errors_total")
	assert.NotContains(t, families, "aaa_xid_last_timestamp_seconds")
}
