package exporter

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
)

// qField stands for query field - the field name before the query
type qField string

// rField stands for returned field - the field name as returned by the nvidia-smi
type rField string

const (
	DefaultPrefix           = "nvidia_smi"
	DefaultNvidiaSmiCommand = "nvidia-smi"
)

var (
	numericRegex = regexp.MustCompile("[+-]?([0-9]*[.])?[0-9]+")

	requiredFields = []requiredField{
		{qField: uuidQField, label: "uuid"},
		{qField: nameQField, label: "name"},
		{qField: driverModelCurrentQField, label: "driver_model_current"},
		{qField: driverModelPendingQField, label: "driver_model_pending"},
		{qField: vBiosVersionQField, label: "vbios_version"},
		{qField: driverVersionQField, label: "driver_version"},
	}

	runCmd = func(cmd *exec.Cmd) error { return cmd.Run() }
)

// Exporter collects stats and exports them using
// the prometheus metrics package.
type gpuExporter struct {
	mutex                 sync.RWMutex
	prefix                string
	qFields               []qField
	qFieldToMetricInfoMap map[qField]MetricInfo
	nvidiaSmiCommand      string
	failedScrapesTotal    prometheus.Counter
	exitCode              prometheus.Gauge
	gpuInfoDesc           *prometheus.Desc
	logger                log.Logger
}

func New(prefix string, nvidiaSmiCommand string, qFieldsRaw string, logger log.Logger) (prometheus.Collector, error) {
	qFieldsOrdered, qFieldToRFieldMap, err := buildQFieldToRFieldMap(logger, qFieldsRaw, nvidiaSmiCommand)
	if err != nil {
		return nil, err
	}

	qFieldToMetricInfoMap := buildQFieldToMetricInfoMap(prefix, qFieldToRFieldMap)
	// qFields := getKeys(qFieldToRFieldMap)

	infoLabels := getLabels(requiredFields)
	e := gpuExporter{
		prefix:                prefix,
		nvidiaSmiCommand:      nvidiaSmiCommand,
		qFields:               qFieldsOrdered,
		qFieldToMetricInfoMap: qFieldToMetricInfoMap,
		logger:                logger,
		failedScrapesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: prefix,
			Name:      "failed_scrapes_total",
			Help:      "Number of failed scrapes",
		}),
		exitCode: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: prefix,
			Name:      "command_exit_code",
			Help:      "Exit code of the last scrape command",
		}),
		gpuInfoDesc: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "gpu_info"),
			fmt.Sprintf("A metric with a constant '1' value labeled by gpu %s.",
				strings.Join(infoLabels, ", ")),
			infoLabels,
			nil),
	}

	return &e, nil
}

func buildQFieldToRFieldMap(logger log.Logger, qFieldsRaw string,
	nvidiaSmiCommand string) ([]qField, map[qField]rField, error) {
	qFieldsSeparated := strings.Split(qFieldsRaw, ",")

	qFields := toQFieldSlice(qFieldsSeparated)
	for _, reqField := range requiredFields {
		qFields = append(qFields, reqField.qField)
	}

	qFields = removeDuplicateQFields(qFields)

	if len(qFieldsSeparated) == 1 && qFieldsSeparated[0] == qFieldsAuto {
		parsed, err := ParseAutoQFields(nvidiaSmiCommand)
		if err != nil {
			_ = level.Warn(logger).Log("msg",
				"Failed to auto-determine query field names, "+
					"falling back to the built-in list")
			return getKeys(fallbackQFieldToRFieldMap), fallbackQFieldToRFieldMap, nil
		}

		qFields = parsed
	}

	_, t, err := scrape(qFields, nvidiaSmiCommand)
	var rFields []rField
	if err != nil {
		_ = level.Warn(logger).Log("msg",
			"Failed to run an initial scrape, using the built-in list for field mapping")
		rFields, err = getFallbackValues(qFields)
		if err != nil {
			return nil, nil, err
		}
	} else {
		rFields = t.rFields
	}

	r := make(map[qField]rField, len(qFields))
	for i, q := range qFields {
		r[q] = rFields[i]
	}

	return qFields, r, nil
}

// Describe describes all the metrics ever exported by the exporter. It
// implements prometheus.Collector.
func (e *gpuExporter) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range e.qFieldToMetricInfoMap {
		ch <- m.desc
	}
	ch <- e.failedScrapesTotal.Desc()
	ch <- e.gpuInfoDesc
}

// Collect fetches the stats and delivers them as Prometheus metrics. It implements prometheus.Collector.
func (e *gpuExporter) Collect(ch chan<- prometheus.Metric) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	exitCode, t, err := scrape(e.qFields, e.nvidiaSmiCommand)
	e.exitCode.Set(float64(exitCode))
	ch <- e.exitCode
	if err != nil {
		_ = level.Error(e.logger).Log("error", err)
		ch <- e.failedScrapesTotal
		e.failedScrapesTotal.Inc()
		return
	}

	for _, r := range t.rows {
		uuid := strings.TrimPrefix(strings.ToLower(r.qFieldToCells[uuidQField].rawValue), "gpu-")
		name := r.qFieldToCells[nameQField].rawValue
		driverModelCurrent := r.qFieldToCells[driverModelCurrentQField].rawValue
		driverModelPending := r.qFieldToCells[driverModelPendingQField].rawValue
		vBiosVersion := r.qFieldToCells[vBiosVersionQField].rawValue
		driverVersion := r.qFieldToCells[driverVersionQField].rawValue

		infoMetric := prometheus.MustNewConstMetric(e.gpuInfoDesc, prometheus.GaugeValue,
			1, uuid, name, driverModelCurrent,
			driverModelPending, vBiosVersion, driverVersion)
		ch <- infoMetric

		for _, c := range r.cells {
			mi := e.qFieldToMetricInfoMap[c.qField]
			num, err := transformRawValue(c.rawValue, mi.valueMultiplier)
			if err != nil {
				_ = level.Debug(e.logger).Log("error", err, "query_field_name",
					c.qField, "raw_value", c.rawValue)
				continue
			}

			ch <- prometheus.MustNewConstMetric(mi.desc, mi.mType, num, uuid)
		}
	}
}

func scrape(qFields []qField, nvidiaSmiCommand string) (int, *table, error) {
	qFieldsJoined := strings.Join(QFieldSliceToStringSlice(qFields), ",")

	cmdAndArgs := strings.Fields(nvidiaSmiCommand)
	cmdAndArgs = append(cmdAndArgs, fmt.Sprintf("--query-gpu=%s", qFieldsJoined))
	cmdAndArgs = append(cmdAndArgs, "--format=csv")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.Command(cmdAndArgs[0], cmdAndArgs[1:]...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := runCmd(cmd)
	if err != nil {
		exitCode := -1
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		}

		return exitCode, nil, fmt.Errorf("command failed. stderr: %s err: %w", stderr.String(), err)
	}

	t, err := parseCSVIntoTable(strings.TrimSpace(stdout.String()), qFields)
	if err != nil {
		return -1, nil, err
	}

	return 0, &t, nil
}

type MetricInfo struct {
	desc            *prometheus.Desc
	mType           prometheus.ValueType
	valueMultiplier float64
}

func transformRawValue(rawValue string, valueMultiplier float64) (float64, error) {
	trimmed := strings.TrimSpace(rawValue)
	if strings.HasPrefix(trimmed, "0x") {
		return hexToDecimal(trimmed)
	}

	val := strings.ToLower(trimmed)

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

func buildQFieldToMetricInfoMap(prefix string, qFieldtoRFieldMap map[qField]rField) map[qField]MetricInfo {
	result := make(map[qField]MetricInfo)
	for qField, rField := range qFieldtoRFieldMap {
		result[qField] = buildMetricInfo(prefix, rField)
	}

	return result
}

func buildMetricInfo(prefix string, rField rField) MetricInfo {
	fqName, multiplier := buildFQNameAndMultiplier(prefix, rField)
	desc := prometheus.NewDesc(fqName, string(rField), []string{"uuid"}, nil)
	return MetricInfo{
		desc:            desc,
		mType:           prometheus.GaugeValue,
		valueMultiplier: multiplier,
	}
}

func buildFQNameAndMultiplier(prefix string, rField rField) (string, float64) {
	rFieldStr := string(rField)
	suffixTransformed := rFieldStr
	multiplier := 1.0
	split := strings.Split(rFieldStr, " ")[0]
	if strings.HasSuffix(rFieldStr, " [W]") {
		suffixTransformed = split + "_watts"
	} else if strings.HasSuffix(rFieldStr, " [MHz]") {
		suffixTransformed = split + "_clock_hz"
		multiplier = 1000000
	} else if strings.HasSuffix(rFieldStr, " [MiB]") {
		suffixTransformed = split + "_bytes"
		multiplier = 1048576
	} else if strings.HasSuffix(rFieldStr, " [%]") {
		suffixTransformed = split + "_ratio"
		multiplier = 0.01
	}

	metricName := toSnakeCase(strings.ReplaceAll(suffixTransformed, ".", "_"))
	fqName := prometheus.BuildFQName(prefix, "", metricName)

	return fqName, multiplier
}

func getKeys(m map[qField]rField) []qField {
	r := make([]qField, len(m))
	i := 0
	for key := range m {
		r[i] = key
		i++
	}
	return r
}

func getFallbackValues(qFields []qField) ([]rField, error) {
	r := make([]rField, len(qFields))
	i := 0
	for _, q := range qFields {
		val, contains := fallbackQFieldToRFieldMap[q]
		if !contains {
			return nil, fmt.Errorf("unexpected query field: %s", q)
		}

		r[i] = val
		i++
	}
	return r, nil
}

func getLabels(reqFields []requiredField) []string {
	r := make([]string, len(reqFields))
	for i, reqField := range reqFields {
		r[i] = reqField.label
	}
	return r
}

type requiredField struct {
	qField qField
	label  string
}

func removeDuplicateQFields(qFields []qField) []qField {
	m := make(map[qField]struct{})
	var r []qField
	for _, f := range qFields {
		_, exists := m[f]
		if !exists {
			r = append(r, f)
			m[f] = struct{}{}
		}
	}

	return r
}
