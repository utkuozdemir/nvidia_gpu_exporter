package exporter

import (
	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"strings"
	"testing"
)

const (
	delta = 1e-9
)

func assertFloat(t *testing.T, expected, actual float64) bool {
	return assert.InDelta(t, expected, actual, delta)
}

func TestTransformRawValueValidValues(t *testing.T) {
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
	rawValues := []string{
		"aaaaa", "0X1234", "aa111aa111", "123.456.789",
	}

	for _, raw := range rawValues {
		_, err := transformRawValue(raw, 1)
		assert.Error(t, err)
	}
}

func TestTransformRawMultiplier(t *testing.T) {
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
	fqName, multiplier := buildFQNameAndMultiplier("prefix", "encoder.stats.sessionCount")
	assertFloat(t, 1, multiplier)
	assert.Equal(t, "prefix_encoder_stats_session_count", fqName)
}

func TestBuildFQNameAndMultiplierWatts(t *testing.T) {
	fqName, multiplier := buildFQNameAndMultiplier("prefix", "power.draw [W]")
	assertFloat(t, 1, multiplier)
	assert.Equal(t, "prefix_power_draw_watts", fqName)
}

func TestBuildFQNameAndMultiplierMiB(t *testing.T) {
	fqName, multiplier := buildFQNameAndMultiplier("prefix", "memory.total [MiB]")
	assertFloat(t, 1048576, multiplier)
	assert.Equal(t, "prefix_memory_total_bytes", fqName)
}

func TestBuildFQNameAndMultiplierMHZ(t *testing.T) {
	fqName, multiplier := buildFQNameAndMultiplier("prefix", "clocks.current.graphics [MHz]")
	assertFloat(t, 1000000, multiplier)
	assert.Equal(t, "prefix_clocks_current_graphics_clock_hz", fqName)
}

func TestBuildFQNameAndMultiplierRatio(t *testing.T) {
	fqName, multiplier := buildFQNameAndMultiplier("prefix", "fan.speed [%]")
	assertFloat(t, 0.01, multiplier)
	assert.Equal(t, "prefix_fan_speed_ratio", fqName)
}

func TestBuildFQNameAndMultiplierNoPrefix(t *testing.T) {
	fqName, multiplier := buildFQNameAndMultiplier("", "encoder.stats.sessionCount")
	assertFloat(t, 1, multiplier)
	assert.Equal(t, "encoder_stats_session_count", fqName)
}

func TestBuildMetricInfo(t *testing.T) {
	metricInfo := buildMetricInfo("prefix", "encoder.stats.sessionCount")
	assertFloat(t, 1, metricInfo.valueMultiplier)
	assert.Equal(t, prometheus.GaugeValue, metricInfo.mType)
}

func TestBuildQFieldToMetricInfoMap(t *testing.T) {
	m := buildQFieldToMetricInfoMap("prefix", map[qField]rField{"aaa": "AAA", "bbb": "BBB"})
	assert.Len(t, m, 2)

	metricInfo1 := m["aaa"]
	assertFloat(t, 1, metricInfo1.valueMultiplier)
	assert.Equal(t, prometheus.GaugeValue, metricInfo1.mType)

	metricInfo2 := m["bbb"]
	assertFloat(t, 1, metricInfo2.valueMultiplier)
	assert.Equal(t, prometheus.GaugeValue, metricInfo2.mType)
}

func TestNewUnknownField(t *testing.T) {
	logger := log.NewNopLogger()
	_, err := New("aaa", "bbb", "a", logger)
	assert.Error(t, err)
}

func TestDescribe(t *testing.T) {
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

	assert.Len(t, descStrs, 3)
	descs := strings.Join(descStrs, "\n")
	assert.Contains(t, descs, "aaa_fan_speed")
	assert.Contains(t, descs, "aaa_memory_used")
	assert.Contains(t, descs, "aaa_failed_scrapes_total")
}

func TestCollect(t *testing.T) {
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

	assert.Len(t, metrics, 1)
	assert.Contains(t, metrics[0], "aaa_failed_scrapes_total")
}
