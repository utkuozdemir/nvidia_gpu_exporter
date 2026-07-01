package exporter_test

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/neilotoole/slogt/v2"
	"github.com/prometheus/client_golang/prometheus"
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

	query := func(queryCtx context.Context) (*nvidiasmi.Table, int, error) {
		return nvidiasmi.Query(queryCtx, "bbb", resolved.Query, run)
	}

	source := collect.NewLive(query, 0, nil, logger)

	return exporter.New(ctx, prefix, resolved, source, logger)
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
	}

	slices.Sort(expectedMetrics)

	assert.Len(t, descStrs, len(expectedMetrics))

	for i, metric := range expectedMetrics {
		descStr := descStrs[i]

		assert.Contains(t, descStr, fmt.Sprintf(`"%s_%s"`, prefix, metric))
	}
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

	doneCh := make(chan bool)
	metricCh := make(chan prometheus.Metric)

	go func() {
		exp.Collect(metricCh)

		doneCh <- true
	}()

	var metrics []string

end:
	for {
		select {
		case metric := <-metricCh:
			metrics = append(metrics, metric.Desc().String())
		case <-doneCh:
			break end
		}
	}

	metricsJoined := strings.Join(metrics, "\n")

	assert.Len(t, metrics, 16)
	assert.Contains(t, metricsJoined, "aaa_gpu_info")
	assert.Contains(t, metricsJoined, "command_exit_code")
	assert.Contains(t, metricsJoined, "aaa_name")
	assert.Contains(t, metricsJoined, "aaa_fan_speed_ratio")
	assert.Contains(t, metricsJoined, "aaa_memory_used_bytes")
	assert.Contains(t, metricsJoined, "aaa_compute_cap")
	assert.Contains(t, metricsJoined, "serial")
	assert.Contains(t, metricsJoined, "pci_sub_device_id")
	assert.Contains(t, metricsJoined, "index")
}

func TestCollectError(t *testing.T) {
	t.Parallel()

	exp := newTestExporter(t, "aaa", "fan.speed,memory.used", nvidiasmi.DefaultRunFunc)

	doneCh := make(chan bool)
	metricCh := make(chan prometheus.Metric)

	go func() {
		exp.Collect(metricCh)

		doneCh <- true
	}()

	var metrics []string

end:
	for {
		select {
		case metric := <-metricCh:
			metrics = append(metrics, metric.Desc().String())
		case <-doneCh:
			break end
		}
	}

	assert.Len(t, metrics, 2)
	metricsJoined := strings.Join(metrics, "\n")

	assert.Contains(t, metricsJoined, "aaa_failed_scrapes_total")
	assert.Contains(t, metricsJoined, "aaa_command_exit_code")
}
