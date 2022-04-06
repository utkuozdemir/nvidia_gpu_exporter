package exporter

import (
	_ "embed"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

const (
	delta = 1e-9
)

//go:embed _query-test.txt
var queryTest string

func assertFloat(t *testing.T, expected, actual float64) bool {
	t.Helper()

	return assert.InDelta(t, expected, actual, delta)
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
		val, err := transformRawValue(raw, 1)
		assert.NoError(t, err)
		assertFloat(t, expected, val)
	}
}

func TestTransformRawValueInvalidValues(t *testing.T) {
	t.Parallel()

	rawValues := []string{
		"aaaaa", "0X1234", "aa111aa111", "123.456.789",
	}

	for _, raw := range rawValues {
		_, err := transformRawValue(raw, 1)
		assert.Error(t, err)
	}
}

func TestTransformRawMultiplier(t *testing.T) {
	t.Parallel()

	val, err := transformRawValue("11", 2)

	assert.NoError(t, err)
	assertFloat(t, 22, val)

	val, err = transformRawValue("10", 0.5)
	assert.NoError(t, err)
	assertFloat(t, 5, val)

	val, err = transformRawValue("enabled", 42)
	assert.NoError(t, err)
	assertFloat(t, 1, val)
}

func TestBuildFQNameAndMultiplierRegular(t *testing.T) {
	t.Parallel()

	fqName, multiplier := buildFQNameAndMultiplier("prefix", "encoder.stats.sessionCount")

	assertFloat(t, 1, multiplier)
	assert.Equal(t, "prefix_encoder_stats_session_count", fqName)
}

func TestBuildFQNameAndMultiplierWatts(t *testing.T) {
	t.Parallel()

	fqName, multiplier := buildFQNameAndMultiplier("prefix", "power.draw [W]")

	assertFloat(t, 1, multiplier)
	assert.Equal(t, "prefix_power_draw_watts", fqName)
}

func TestBuildFQNameAndMultiplierMiB(t *testing.T) {
	t.Parallel()

	fqName, multiplier := buildFQNameAndMultiplier("prefix", "memory.total [MiB]")

	assertFloat(t, 1048576, multiplier)
	assert.Equal(t, "prefix_memory_total_bytes", fqName)
}

func TestBuildFQNameAndMultiplierMHZ(t *testing.T) {
	t.Parallel()

	fqName, multiplier := buildFQNameAndMultiplier("prefix", "clocks.current.graphics [MHz]")

	assertFloat(t, 1000000, multiplier)
	assert.Equal(t, "prefix_clocks_current_graphics_clock_hz", fqName)
}

func TestBuildFQNameAndMultiplierRatio(t *testing.T) {
	t.Parallel()

	fqName, multiplier := buildFQNameAndMultiplier("prefix", "fan.speed [%]")

	assertFloat(t, 0.01, multiplier)
	assert.Equal(t, "prefix_fan_speed_ratio", fqName)
}

func TestBuildFQNameAndMultiplierNoPrefix(t *testing.T) {
	t.Parallel()

	fqName, multiplier := buildFQNameAndMultiplier("", "encoder.stats.sessionCount")

	assertFloat(t, 1, multiplier)
	assert.Equal(t, "encoder_stats_session_count", fqName)
}

func TestBuildMetricInfo(t *testing.T) {
	t.Parallel()

	metricInfo := buildMetricInfo("prefix", "encoder.stats.sessionCount")

	assertFloat(t, 1, metricInfo.valueMultiplier)
	assert.Equal(t, prometheus.GaugeValue, metricInfo.mType)
}

func TestBuildQFieldToMetricInfoMap(t *testing.T) {
	t.Parallel()

	qFieldToMetricInfoMap := buildQFieldToMetricInfoMap("prefix", map[qField]rField{"aaa": "AAA", "bbb": "BBB"})

	assert.Len(t, qFieldToMetricInfoMap, 2)

	metricInfo1 := qFieldToMetricInfoMap["aaa"]
	assertFloat(t, 1, metricInfo1.valueMultiplier)
	assert.Equal(t, prometheus.GaugeValue, metricInfo1.mType)

	metricInfo2 := qFieldToMetricInfoMap["bbb"]
	assertFloat(t, 1, metricInfo2.valueMultiplier)
	assert.Equal(t, prometheus.GaugeValue, metricInfo2.mType)
}

func TestNewUnknownField(t *testing.T) {
	t.Parallel()

	logger := log.NewNopLogger()
	_, err := New("aaa", "bbb", "a", logger)

	assert.Error(t, err)
}

func TestDescribe(t *testing.T) {
	t.Parallel()

	logger := log.NewNopLogger()
	exp, err := New("aaa", "bbb", "fan.speed,memory.used", logger)

	assert.NoError(t, err)

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

	runCmdOriginal := runCmd
	defer func() { runCmd = runCmdOriginal }()

	runCmd = func(cmd *exec.Cmd) error {
		_, _ = cmd.Stdout.Write([]byte(queryTest))

		return nil
	}

	logger := log.NewNopLogger()
	exp, err := New("aaa", "bbb",
		"uuid,name,driver_model.current,driver_model.pending,"+
			"vbios_version,driver_version,fan.speed,memory.used", logger)
	assert.NoError(t, err)

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
	exp, err := New("aaa", "bbb", "fan.speed,memory.used", logger)

	assert.NoError(t, err)

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
