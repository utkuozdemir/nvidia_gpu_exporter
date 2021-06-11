package exporter

import (
	"bytes"
	"fmt"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const (
	DefaultPrefix           = "nvidia_smi"
	DefaultNvidiaSmiCommand = "nvidia-smi"
)

var (
	DefaultMetrics = []string{
		"timestamp", "driver_version", "count", "name", "serial", "uuid", "pci.bus_id", "pci.domain", "pci.bus",
		"pci.device", "pci.device_id", "pci.sub_device_id", "pcie.link.gen.current", "pcie.link.gen.max",
		"pcie.link.width.current", "pcie.link.width.max", "index", "display_mode", "display_active",
		"persistence_mode", "accounting.mode", "accounting.buffer_size", "driver_model.current",
		"driver_model.pending", "vbios_version", "inforom.img", "inforom.oem", "inforom.ecc", "inforom.pwr",
		"gom.current", "gom.pending", "fan.speed", "pstate", "clocks_throttle_reasons.supported",
		"clocks_throttle_reasons.active", "clocks_throttle_reasons.gpu_idle",
		"clocks_throttle_reasons.applications_clocks_setting", "clocks_throttle_reasons.sw_power_cap",
		"clocks_throttle_reasons.hw_slowdown", "clocks_throttle_reasons.hw_thermal_slowdown",
		"clocks_throttle_reasons.hw_power_brake_slowdown", "clocks_throttle_reasons.sw_thermal_slowdown",
		"clocks_throttle_reasons.sync_boost", "memory.total", "memory.used", "memory.free", "compute_mode",
		"utilization.gpu", "utilization.memory", "encoder.stats.sessionCount", "encoder.stats.averageFps",
		"encoder.stats.averageLatency", "ecc.mode.current", "ecc.mode.pending",
		"ecc.errors.corrected.volatile.device_memory", "ecc.errors.corrected.volatile.dram",
		"ecc.errors.corrected.volatile.register_file", "ecc.errors.corrected.volatile.l1_cache",
		"ecc.errors.corrected.volatile.l2_cache", "ecc.errors.corrected.volatile.texture_memory",
		"ecc.errors.corrected.volatile.cbu", "ecc.errors.corrected.volatile.sram",
		"ecc.errors.corrected.volatile.total", "ecc.errors.corrected.aggregate.device_memory",
		"ecc.errors.corrected.aggregate.dram", "ecc.errors.corrected.aggregate.register_file",
		"ecc.errors.corrected.aggregate.l1_cache", "ecc.errors.corrected.aggregate.l2_cache",
		"ecc.errors.corrected.aggregate.texture_memory", "ecc.errors.corrected.aggregate.cbu",
		"ecc.errors.corrected.aggregate.sram", "ecc.errors.corrected.aggregate.total",
		"ecc.errors.uncorrected.volatile.device_memory", "ecc.errors.uncorrected.volatile.dram",
		"ecc.errors.uncorrected.volatile.register_file", "ecc.errors.uncorrected.volatile.l1_cache",
		"ecc.errors.uncorrected.volatile.l2_cache", "ecc.errors.uncorrected.volatile.texture_memory",
		"ecc.errors.uncorrected.volatile.cbu", "ecc.errors.uncorrected.volatile.sram",
		"ecc.errors.uncorrected.volatile.total", "ecc.errors.uncorrected.aggregate.device_memory",
		"ecc.errors.uncorrected.aggregate.dram", "ecc.errors.uncorrected.aggregate.register_file",
		"ecc.errors.uncorrected.aggregate.l1_cache", "ecc.errors.uncorrected.aggregate.l2_cache",
		"ecc.errors.uncorrected.aggregate.texture_memory", "ecc.errors.uncorrected.aggregate.cbu",
		"ecc.errors.uncorrected.aggregate.sram", "ecc.errors.uncorrected.aggregate.total",
		"retired_pages.single_bit_ecc.count", "retired_pages.double_bit.count", "retired_pages.pending",
		"temperature.gpu", "temperature.memory", "power.management", "power.draw", "power.limit",
		"enforced.power.limit", "power.default_limit", "power.min_limit", "power.max_limit", "clocks.current.graphics",
		"clocks.current.sm", "clocks.current.memory", "clocks.current.video", "clocks.applications.graphics",
		"clocks.applications.memory", "clocks.default_applications.graphics", "clocks.default_applications.memory",
		"clocks.max.graphics", "clocks.max.sm", "clocks.max.memory", "mig.mode.current", "mig.mode.pending",
	}
	numericRegex = regexp.MustCompile("[+-]?([0-9]*[.])?[0-9]+")

	matchFirstCap = regexp.MustCompile("(.)([A-Z][a-z]+)")
	matchAllCap   = regexp.MustCompile("([a-z0-9])([A-Z])")
)

// Exporter collects HAProxy stats from the given URI and exports them using
// the prometheus metrics package.
type gpuExporter struct {
	mutex              sync.RWMutex
	prefix             string
	metricKeys         []string
	metricMap          map[string]MetricInfo
	nvidiaSmiCommand   string
	scrapesTotal       prometheus.Counter
	failedScrapesTotal prometheus.Counter
	logger             log.Logger
}

func New(prefix string, nvidiaSmiCommand string, metrics []string, logger log.Logger) prometheus.Collector {
	metricMap := buildMetricMap(prefix, metrics)
	return &gpuExporter{
		prefix:           prefix,
		nvidiaSmiCommand: nvidiaSmiCommand,
		metricKeys:       metrics,
		metricMap:        metricMap,
		logger:           logger,
		scrapesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: prefix,
			Name:      "scrapes_total",
			Help:      "Number of total scrapes, including both failed and successful ones",
		}),
		failedScrapesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: prefix,
			Name:      "failed_scrapes_total",
			Help:      "Number of failed scrapes",
		}),
	}
}

// Describe describes all the metrics ever exported by the HAProxy exporter. It
// implements prometheus.Collector.
func (e *gpuExporter) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range e.metricMap {
		ch <- m.desc
	}
	ch <- e.scrapesTotal.Desc()
	ch <- e.failedScrapesTotal.Desc()
}

// Collect fetches the stats from configured HAProxy location and delivers them
// as Prometheus metrics. It implements prometheus.Collector.
func (e *gpuExporter) Collect(ch chan<- prometheus.Metric) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	ch <- e.scrapesTotal
	e.scrapesTotal.Inc()

	queryFields := strings.Join(e.metricKeys, ",")

	cmdAndArgs := strings.Fields(e.nvidiaSmiCommand)
	cmdAndArgs = append(cmdAndArgs, fmt.Sprintf("--query-gpu=%s", queryFields))
	cmdAndArgs = append(cmdAndArgs, "--format=csv")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.Command(cmdAndArgs[0], cmdAndArgs[1:]...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		_ = level.Error(e.logger).Log("error", err, "stderr", stderr.String())
		ch <- e.failedScrapesTotal
		e.failedScrapesTotal.Inc()
		return
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	titlesLine := lines[0]
	valuesLine := lines[len(lines)-1]
	titles := parseCsvLine(titlesLine)
	values := parseCsvLine(valuesLine)
	fmt.Println(titles)

	for i, m := range e.metricKeys {
		value := values[i]
		gpuMetric := e.metricMap[m]

		num, err := gpuMetric.valueTransformer(value)
		if err != nil {
			_ = level.Warn(e.logger).Log("transform_error", err, "key", m, "value", value)
			continue
		}

		ch <- prometheus.MustNewConstMetric(gpuMetric.desc, gpuMetric.mType, num)
	}
}

type MetricInfo struct {
	desc             *prometheus.Desc
	mType            prometheus.ValueType
	valueTransformer func(string) (float64, error)
}

func descForMetricKey(prefix string, key string, help string) *prometheus.Desc {
	name := toSnakeCase(strings.ReplaceAll(key, ".", "_"))
	fqName := prometheus.BuildFQName(prefix, "", name)
	return prometheus.NewDesc(fqName, help, nil, nil)
}

func bestEffortValueTransformer(value string) (float64, error) {
	val := strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(val, "0x") {
		return hexToDecimal(val)
	}

	switch val {
	case "enabled", "yes", "active":
		return 1, nil
	case "disabled", "no", "not active":
		return 0, nil
	case "default":
		return 0, nil
	case "exclusive_thread":
		return 1, nil
	case "prohibited":
		return 2, nil
	case "exclusive_process":
		return 3, nil
	default:
		allNums := numericRegex.FindAllString(val, 2)
		if len(allNums) != 1 {
			return -1, fmt.Errorf("couldn't parse number from: %s", val)
		}

		return strconv.ParseFloat(allNums[0], 64)
	}
}

func hexToDecimal(hex string) (float64, error) {
	s := hex
	s = strings.Replace(s, "0x", "", -1)
	s = strings.Replace(s, "0X", "", -1)
	parsed, err := strconv.ParseUint(s, 16, 64)
	return float64(parsed), err
}

func buildMetricMap(prefix string, metrics []string) map[string]MetricInfo {
	result := make(map[string]MetricInfo)
	for _, key := range metrics {
		result[key] = MetricInfo{
			// todo: provide help
			desc:             descForMetricKey(prefix, key, ""),
			mType:            prometheus.GaugeValue,
			valueTransformer: bestEffortValueTransformer,
		}
	}

	return result
}

func parseCsvLine(line string) []string {
	values := strings.Split(line, ",")
	result := make([]string, len(values))
	for i, field := range values {
		result[i] = strings.TrimSpace(field)
	}
	return result
}

func toSnakeCase(str string) string {
	snake := matchFirstCap.ReplaceAllString(str, "${1}_${2}")
	snake = matchAllCap.ReplaceAllString(snake, "${1}_${2}")
	return strings.ToLower(snake)
}
