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
	queryFieldNamesAuto     = "AUTO"
	DefaultQueryFieldNames  = queryFieldNamesAuto
	uuidQueryFieldName      = "uuid"
	nameQueryFieldName      = "name"
)

var (
	//DefaultQueryFieldNames = []string{
	//	"timestamp", "driver_version", "count", nameQueryFieldName, "serial", uuidQueryFieldName, "pci.bus_id",
	//	"pci.domain", "pci.bus",
	//	"pci.device", "pci.device_id", "pci.sub_device_id", "pcie.link.gen.current", "pcie.link.gen.max",
	//	"pcie.link.width.current", "pcie.link.width.max", "index", "display_mode", "display_active",
	//	"persistence_mode", "accounting.mode", "accounting.buffer_size", "driver_model.current",
	//	"driver_model.pending", "vbios_version", "inforom.img", "inforom.oem", "inforom.ecc", "inforom.pwr",
	//	"gom.current", "gom.pending", "fan.speed", "pstate", "clocks_throttle_reasons.supported",
	//	"clocks_throttle_reasons.active", "clocks_throttle_reasons.gpu_idle",
	//	"clocks_throttle_reasons.applications_clocks_setting", "clocks_throttle_reasons.sw_power_cap",
	//	"clocks_throttle_reasons.hw_slowdown", "clocks_throttle_reasons.hw_thermal_slowdown",
	//	"clocks_throttle_reasons.hw_power_brake_slowdown", "clocks_throttle_reasons.sw_thermal_slowdown",
	//	"clocks_throttle_reasons.sync_boost", "memory.total", "memory.used", "memory.free", "compute_mode",
	//	"utilization.gpu", "utilization.memory", "encoder.stats.sessionCount", "encoder.stats.averageFps",
	//	"encoder.stats.averageLatency", "ecc.mode.current", "ecc.mode.pending",
	//	"ecc.errors.corrected.volatile.device_memory", "ecc.errors.corrected.volatile.dram",
	//	"ecc.errors.corrected.volatile.register_file", "ecc.errors.corrected.volatile.l1_cache",
	//	"ecc.errors.corrected.volatile.l2_cache", "ecc.errors.corrected.volatile.texture_memory",
	//	"ecc.errors.corrected.volatile.cbu", "ecc.errors.corrected.volatile.sram",
	//	"ecc.errors.corrected.volatile.total", "ecc.errors.corrected.aggregate.device_memory",
	//	"ecc.errors.corrected.aggregate.dram", "ecc.errors.corrected.aggregate.register_file",
	//	"ecc.errors.corrected.aggregate.l1_cache", "ecc.errors.corrected.aggregate.l2_cache",
	//	"ecc.errors.corrected.aggregate.texture_memory", "ecc.errors.corrected.aggregate.cbu",
	//	"ecc.errors.corrected.aggregate.sram", "ecc.errors.corrected.aggregate.total",
	//	"ecc.errors.uncorrected.volatile.device_memory", "ecc.errors.uncorrected.volatile.dram",
	//	"ecc.errors.uncorrected.volatile.register_file", "ecc.errors.uncorrected.volatile.l1_cache",
	//	"ecc.errors.uncorrected.volatile.l2_cache", "ecc.errors.uncorrected.volatile.texture_memory",
	//	"ecc.errors.uncorrected.volatile.cbu", "ecc.errors.uncorrected.volatile.sram",
	//	"ecc.errors.uncorrected.volatile.total", "ecc.errors.uncorrected.aggregate.device_memory",
	//	"ecc.errors.uncorrected.aggregate.dram", "ecc.errors.uncorrected.aggregate.register_file",
	//	"ecc.errors.uncorrected.aggregate.l1_cache", "ecc.errors.uncorrected.aggregate.l2_cache",
	//	"ecc.errors.uncorrected.aggregate.texture_memory", "ecc.errors.uncorrected.aggregate.cbu",
	//	"ecc.errors.uncorrected.aggregate.sram", "ecc.errors.uncorrected.aggregate.total",
	//	"retired_pages.single_bit_ecc.count", "retired_pages.double_bit.count", "retired_pages.pending",
	//	"temperature.gpu", "temperature.memory", "power.management", "power.draw", "power.limit",
	//	"enforced.power.limit", "power.default_limit", "power.min_limit", "power.max_limit", "clocks.current.graphics",
	//	"clocks.current.sm", "clocks.current.memory", "clocks.current.video", "clocks.applications.graphics",
	//	"clocks.applications.memory", "clocks.default_applications.graphics", "clocks.default_applications.memory",
	//	"clocks.max.graphics", "clocks.max.sm", "clocks.max.memory", "mig.mode.current", "mig.mode.pending",
	//}

	variableLabels = []string{uuidQueryFieldName, nameQueryFieldName}
	numericRegex   = regexp.MustCompile("[+-]?([0-9]*[.])?[0-9]+")
)

// Exporter collects stats and exports them using
// the prometheus metrics package.
type gpuExporter struct {
	mutex                      sync.RWMutex
	prefix                     string
	queryFieldNames            []string
	queryFieldNameToMetricInfo map[string]MetricInfo
	nvidiaSmiCommand           string
	failedScrapesTotal         prometheus.Counter
	logger                     log.Logger
}

func New(prefix string, nvidiaSmiCommand string, queryFieldNames string, logger log.Logger) (prometheus.Collector, error) {
	queryFieldNamesParsed, err := parseQueryFieldNames(queryFieldNames, nvidiaSmiCommand)
	if err != nil {
		return nil, err
	}

	t, err := scrape(queryFieldNamesParsed, nvidiaSmiCommand)
	if err != nil {
		return nil, err
	}

	queryFieldNameToMetricInfoMap := buildQueryFieldNameToMetricInfoMap(prefix, queryFieldNamesParsed, t.returnedFieldNames)

	e := gpuExporter{
		prefix:                     prefix,
		nvidiaSmiCommand:           nvidiaSmiCommand,
		queryFieldNames:            queryFieldNamesParsed,
		queryFieldNameToMetricInfo: queryFieldNameToMetricInfoMap,
		logger:                     logger,
		failedScrapesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: prefix,
			Name:      "failed_scrapes_total",
			Help:      "Number of failed scrapes",
		}),
	}

	return &e, nil
}

func parseQueryFieldNames(queryFieldNames string, nvidiaSmiCommand string) ([]string, error) {
	queryFieldNamesSeparated := strings.Split(queryFieldNames, ",")
	if len(queryFieldNamesSeparated) == 1 && queryFieldNamesSeparated[0] == queryFieldNamesAuto {
		return ParseQueryFields(nvidiaSmiCommand)
	}
	return queryFieldNamesSeparated, nil
}

// Describe describes all the metrics ever exported by the exporter. It
// implements prometheus.Collector.
func (e *gpuExporter) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range e.queryFieldNameToMetricInfo {
		ch <- m.desc
	}
	ch <- e.failedScrapesTotal.Desc()
}

// Collect fetches the stats and delivers them as Prometheus metrics. It implements prometheus.Collector.
func (e *gpuExporter) Collect(ch chan<- prometheus.Metric) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	t, err := scrape(e.queryFieldNames, e.nvidiaSmiCommand)
	if err != nil {
		_ = level.Error(e.logger).Log("error", err)
		ch <- e.failedScrapesTotal
		e.failedScrapesTotal.Inc()
		return
	}

	for _, r := range t.rows {
		uuid := strings.TrimPrefix(strings.ToLower(r.queryFieldNameToCells[uuidQueryFieldName].rawValue), "gpu-")
		name := r.queryFieldNameToCells[nameQueryFieldName].rawValue
		for _, c := range r.cells {
			mi := e.queryFieldNameToMetricInfo[c.queryFieldName]
			num, err := transformRawValue(c.rawValue, mi.valueMultiplier)
			if err != nil {
				_ = level.Debug(e.logger).Log("transform_error",
					err, "query_field_name",
					c.queryFieldName, "raw_value", c.rawValue)
				continue
			}

			ch <- prometheus.MustNewConstMetric(mi.desc, mi.mType, num, uuid, name)
		}
	}
}

func scrape(queryFieldNames []string, nvidiaSmiCommand string) (*table, error) {
	queryFields := strings.Join(queryFieldNames, ",")

	cmdAndArgs := strings.Fields(nvidiaSmiCommand)
	cmdAndArgs = append(cmdAndArgs, fmt.Sprintf("--query-gpu=%s", queryFields))
	cmdAndArgs = append(cmdAndArgs, "--format=csv")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.Command(cmdAndArgs[0], cmdAndArgs[1:]...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("command failed. stderr: %s err: %w", stderr.String(), err)
	}

	t := parseCSVIntoTable(strings.TrimSpace(stdout.String()), queryFieldNames)
	return &t, nil
}

type MetricInfo struct {
	desc            *prometheus.Desc
	mType           prometheus.ValueType
	valueMultiplier float64
}

func transformRawValue(rawValue string, valueMultiplier float64) (float64, error) {
	val := strings.ToLower(strings.TrimSpace(rawValue))
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

		parsed, err := strconv.ParseFloat(allNums[0], 64)
		if err != nil {
			return -1, err
		}

		return parsed * valueMultiplier, err
	}
}

func buildQueryFieldNameToMetricInfoMap(prefix string, queryFieldNames []string, returnedFieldNames []string) map[string]MetricInfo {
	result := make(map[string]MetricInfo)
	for i, returnedFieldName := range returnedFieldNames {
		result[queryFieldNames[i]] = buildMetricInfo(prefix, returnedFieldName)
	}

	return result
}

func buildMetricInfo(prefix string, returnedFieldName string) MetricInfo {
	suffixTransformed := returnedFieldName
	multiplier := 1.0
	split := strings.Split(returnedFieldName, " ")[0]
	if strings.HasSuffix(returnedFieldName, " [W]") {
		suffixTransformed = split + "_watts"
	} else if strings.HasSuffix(returnedFieldName, " [MHz]") {
		suffixTransformed = split + "_clock_hz"
		multiplier = 1000000
	} else if strings.HasSuffix(returnedFieldName, " [MiB]") {
		suffixTransformed = split + "_bytes"
		multiplier = 1048576
	} else if strings.HasSuffix(returnedFieldName, " [%]") {
		suffixTransformed = split + "_ratio"
		multiplier = 0.01
	}

	metricName := toSnakeCase(strings.ReplaceAll(suffixTransformed, ".", "_"))
	fqName := prometheus.BuildFQName(prefix, "", metricName)
	desc := prometheus.NewDesc(fqName, "", variableLabels, nil) // todo: add help text
	return MetricInfo{
		desc:            desc,
		mType:           prometheus.GaugeValue,
		valueMultiplier: multiplier,
	}
}
