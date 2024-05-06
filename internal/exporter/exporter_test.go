package exporter_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/exporter"

	_ "embed"
)

const (
	delta = 1e-9
)

//go:embed _query-test.txt
var queryTest string

func assertFloat(t *testing.T, expected, actual float64) {
	t.Helper()

	assert.InDelta(t, expected, actual, delta)
}

func TestTransformRawValueValidValues(t *testing.T) {
	t.Parallel()

	expectedConversions := map[string]float64{
		"disabled":          0,
		"enabled":           1,
		"EnAbLeD":           1,
		"  enabled  ":       1,
		"default":           0,
		"exclusive_thread":  1,
		"prohibited":        2,
		"exclusive_process": 3,
		"0x1E240":           123456,
		"0x1e240":           123456,
		"P15":               15,
		"aaa1234.56bbb":     1234.56,
	}

	for raw, expected := range expectedConversions {
		val, err := exporter.TransformRawValue(raw, 1)
		require.NoError(t, err)
		assertFloat(t, expected, val)
	}
}

func TestTransformRawValueInvalidValues(t *testing.T) {
	t.Parallel()

	rawValues := []string{
		"aaaaa", "0X1234", "aa111aa111", "123.456.789",
	}

	for _, raw := range rawValues {
		_, err := exporter.TransformRawValue(raw, 1)
		require.Error(t, err)
	}
}

func TestTransformRawMultiplier(t *testing.T) {
	t.Parallel()

	val, err := exporter.TransformRawValue("11", 2)

	require.NoError(t, err)
	assertFloat(t, 22, val)

	val, err = exporter.TransformRawValue("10", 0.5)
	require.NoError(t, err)
	assertFloat(t, 5, val)

	val, err = exporter.TransformRawValue("enabled", 42)
	require.NoError(t, err)
	assertFloat(t, 1, val)
}

func TestBuildFQNameAndMultiplierRegular(t *testing.T) {
	t.Parallel()

	fqName, multiplier := exporter.BuildFQNameAndMultiplier("prefix", "encoder.stats.sessionCount")

	assertFloat(t, 1, multiplier)
	assert.Equal(t, "prefix_encoder_stats_session_count", fqName)
}

func TestBuildFQNameAndMultiplierWatts(t *testing.T) {
	t.Parallel()

	fqName, multiplier := exporter.BuildFQNameAndMultiplier("prefix", "power.draw [W]")

	assertFloat(t, 1, multiplier)
	assert.Equal(t, "prefix_power_draw_watts", fqName)
}

func TestBuildFQNameAndMultiplierMiB(t *testing.T) {
	t.Parallel()

	fqName, multiplier := exporter.BuildFQNameAndMultiplier("prefix", "memory.total [MiB]")

	assertFloat(t, 1048576, multiplier)
	assert.Equal(t, "prefix_memory_total_bytes", fqName)
}

func TestBuildFQNameAndMultiplierMHZ(t *testing.T) {
	t.Parallel()

	fqName, multiplier := exporter.BuildFQNameAndMultiplier("prefix", "clocks.current.graphics [MHz]")

	assertFloat(t, 1000000, multiplier)
	assert.Equal(t, "prefix_clocks_current_graphics_clock_hz", fqName)
}

func TestBuildFQNameAndMultiplierRatio(t *testing.T) {
	t.Parallel()

	fqName, multiplier := exporter.BuildFQNameAndMultiplier("prefix", "fan.speed [%]")

	assertFloat(t, 0.01, multiplier)
	assert.Equal(t, "prefix_fan_speed_ratio", fqName)
}

func TestBuildFQNameAndMultiplierNoPrefix(t *testing.T) {
	t.Parallel()

	fqName, multiplier := exporter.BuildFQNameAndMultiplier("", "encoder.stats.sessionCount")

	assertFloat(t, 1, multiplier)
	assert.Equal(t, "encoder_stats_session_count", fqName)
}

func TestBuildMetricInfo(t *testing.T) {
	t.Parallel()

	metricInfo := exporter.BuildMetricInfo("prefix", "encoder.stats.sessionCount")

	assertFloat(t, 1, metricInfo.ValueMultiplier)
	assert.Equal(t, prometheus.GaugeValue, metricInfo.MType)
}

func TestBuildQFieldToMetricInfoMap(t *testing.T) {
	t.Parallel()

	qFieldToMetricInfoMap := exporter.BuildQFieldToMetricInfoMap(
		"prefix",
		map[exporter.QField]exporter.RField{"aaa": "AAA", "bbb": "BBB"},
	)

	assert.Len(t, qFieldToMetricInfoMap, 2)

	metricInfo1 := qFieldToMetricInfoMap["aaa"]
	assertFloat(t, 1, metricInfo1.ValueMultiplier)
	assert.Equal(t, prometheus.GaugeValue, metricInfo1.MType)

	metricInfo2 := qFieldToMetricInfoMap["bbb"]
	assertFloat(t, 1, metricInfo2.ValueMultiplier)
	assert.Equal(t, prometheus.GaugeValue, metricInfo2.MType)
}

func TestNewUnknownField(t *testing.T) {
	t.Parallel()

	logger := log.NewNopLogger()
	_, err := exporter.New("aaa", "bbb", "a", logger)

	require.Error(t, err)
}

func TestDescribe(t *testing.T) {
	t.Parallel()

	logger := log.NewNopLogger()
	exp, err := exporter.New("aaa", "bbb", "fan.speed,memory.used", logger)

	require.NoError(t, err)

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

	assert.Len(t, descStrs, 10)
	descs := strings.Join(descStrs, "\n")
	assert.Contains(t, descs, "aaa_fan_speed")
	assert.Contains(t, descs, "aaa_memory_used")
	assert.Contains(t, descs, "aaa_failed_scrapes_total")
	assert.Contains(t, descs, "aaa_gpu_info")
	assert.Contains(t, descs, "aaa_uuid")
	assert.Contains(t, descs, "aaa_name")
	assert.Contains(t, descs, "aaa_driver_model_current")
	assert.Contains(t, descs, "aaa_driver_model_pending")
	assert.Contains(t, descs, "aaa_vbios_version")
	assert.Contains(t, descs, "aaa_driver_version")
}

func TestCollect(t *testing.T) {
	t.Parallel()

	logger := log.NewNopLogger()
	exp, err := exporter.New("aaa", "bbb",
		"uuid,name,driver_model.current,driver_model.pending,"+
			"vbios_version,driver_version,fan.speed,memory.used", logger)

	exp.Command = func(cmd *exec.Cmd) error {
		_, _ = cmd.Stdout.Write([]byte(queryTest))

		return nil
	}

	require.NoError(t, err)

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

	assert.Len(t, metrics, 10)
	assert.Contains(t, metricsJoined, "aaa_gpu_info")
	assert.Contains(t, metricsJoined, "command_exit_code")
	assert.Contains(t, metricsJoined, "aaa_name")
	assert.Contains(t, metricsJoined, "aaa_fan_speed_ratio")
	assert.Contains(t, metricsJoined, "aaa_memory_used_bytes")
}

func TestCollectError(t *testing.T) {
	t.Parallel()

	logger := log.NewNopLogger()
	exp, err := exporter.New("aaa", "bbb", "fan.speed,memory.used", logger)

	require.NoError(t, err)

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

// TestParseQueryFields must be run manually.
//
//nolint:forbidigo
func TestParseQueryFields(t *testing.T) {
	t.SkipNow()
	t.Parallel()

	nvidiaSmiCommand := "nvidia-smi"

	qFields, err := exporter.ParseAutoQFields(nvidiaSmiCommand, nil)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}

	fields := exporter.QFieldSliceToStringSlice(qFields)

	fmt.Printf("Fields:\n\n%s\n", strings.Join(fields, "\n"))
}
