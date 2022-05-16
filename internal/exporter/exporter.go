package exporter

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/exp/maps"
)

// qField stands for query field - the field name before the query.
type qField string

// rField stands for returned field - the field name as returned by the nvidia-smi.
type rField string

type runCmd func(cmd *exec.Cmd) error

const (
	DefaultPrefix           = "nvidia_smi"
	DefaultNvidiaSmiCommand = "nvidia-smi"

	floatBitSize = 64
)

var (
	ErrUnexpectedQueryField = errors.New("unexpected query field")
	ErrParseNumber          = errors.New("could not parse number from value")

	numericRegex = regexp.MustCompile(`[+-]?(\d*[.])?\d+`)

	requiredFields = []requiredField{
		{qField: uuidQField, label: "uuid"},
		{qField: nameQField, label: "name"},
		{qField: driverModelCurrentQField, label: "driver_model_current"},
		{qField: driverModelPendingQField, label: "driver_model_pending"},
		{qField: vBiosVersionQField, label: "vbios_version"},
		{qField: driverVersionQField, label: "driver_version"},
	}

	defaultRunCmd = func(cmd *exec.Cmd) error {
		err := cmd.Run()
		if err != nil {
			return fmt.Errorf("error running command: %w", err)
		}

		return nil
	}
)

// GPUExporter collects stats and exports them using
// the prometheus metrics package.
type GPUExporter struct {
	mutex                 sync.RWMutex
	prefix                string
	qFields               []qField
	qFieldToMetricInfoMap map[qField]MetricInfo
	nvidiaSmiCommand      string
	failedScrapesTotal    prometheus.Counter
	exitCode              prometheus.Gauge
	gpuInfoDesc           *prometheus.Desc
	logger                log.Logger
	command               runCmd
}

func New(prefix string, nvidiaSmiCommand string, qFieldsRaw string, logger log.Logger) (*GPUExporter, error) {
	qFieldsOrdered, qFieldToRFieldMap, err := buildQFieldToRFieldMap(logger, qFieldsRaw, nvidiaSmiCommand, defaultRunCmd)
	if err != nil {
		return nil, err
	}

	qFieldToMetricInfoMap := buildQFieldToMetricInfoMap(prefix, qFieldToRFieldMap)

	infoLabels := getLabels(requiredFields)
	exporter := GPUExporter{
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
		command: defaultRunCmd,
	}

	return &exporter, nil
}

func buildQFieldToRFieldMap(logger log.Logger, qFieldsRaw string,
	nvidiaSmiCommand string, command runCmd,
) ([]qField, map[qField]rField, error) {
	qFieldsSeparated := strings.Split(qFieldsRaw, ",")

	qFields := toQFieldSlice(qFieldsSeparated)
	for _, reqField := range requiredFields {
		qFields = append(qFields, reqField.qField)
	}

	qFields = removeDuplicates(qFields)

	if len(qFieldsSeparated) == 1 && qFieldsSeparated[0] == qFieldsAuto {
		parsed, err := parseAutoQFields(nvidiaSmiCommand, command)
		if err != nil {
			_ = level.Warn(logger).Log("msg",
				"Failed to auto-determine query field names, "+
					"falling back to the built-in list", "error", err)

			return maps.Keys(fallbackQFieldToRFieldMap), fallbackQFieldToRFieldMap, nil
		}

		qFields = parsed
	}

	_, resultTable, err := scrape(qFields, nvidiaSmiCommand, command)

	var rFields []rField

	if err != nil {
		_ = level.Warn(logger).Log("msg",
			"Failed to run an initial scrape, using the built-in list for field mapping")

		rFields, err = getFallbackValues(qFields)
		if err != nil {
			return nil, nil, err
		}
	} else {
		rFields = resultTable.rFields
	}

	r := make(map[qField]rField, len(qFields))
	for i, q := range qFields {
		r[q] = rFields[i]
	}

	return qFields, r, nil
}

// Describe describes all the metrics ever exported by the exporter. It
// implements prometheus.Collector.
func (e *GPUExporter) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range e.qFieldToMetricInfoMap {
		ch <- m.desc
	}
	ch <- e.failedScrapesTotal.Desc()
	ch <- e.gpuInfoDesc
}

// Collect fetches the stats and delivers them as Prometheus metrics. It implements prometheus.Collector.
func (e *GPUExporter) Collect(metricCh chan<- prometheus.Metric) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	exitCode, currentTable, err := scrape(e.qFields, e.nvidiaSmiCommand, e.command)
	e.exitCode.Set(float64(exitCode))
	metricCh <- e.exitCode

	if err != nil {
		_ = level.Error(e.logger).Log("error", err)
		metricCh <- e.failedScrapesTotal
		e.failedScrapesTotal.Inc()

		return
	}

	for _, currentRow := range currentTable.rows {
		uuid := strings.TrimPrefix(strings.ToLower(currentRow.qFieldToCells[uuidQField].rawValue), "gpu-")
		name := currentRow.qFieldToCells[nameQField].rawValue
		driverModelCurrent := currentRow.qFieldToCells[driverModelCurrentQField].rawValue
		driverModelPending := currentRow.qFieldToCells[driverModelPendingQField].rawValue
		vBiosVersion := currentRow.qFieldToCells[vBiosVersionQField].rawValue
		driverVersion := currentRow.qFieldToCells[driverVersionQField].rawValue

		infoMetric := prometheus.MustNewConstMetric(e.gpuInfoDesc, prometheus.GaugeValue,
			1, uuid, name, driverModelCurrent,
			driverModelPending, vBiosVersion, driverVersion)
		metricCh <- infoMetric

		for _, currentCell := range currentRow.cells {
			metricInfo := e.qFieldToMetricInfoMap[currentCell.qField]

			num, err := transformRawValue(currentCell.rawValue, metricInfo.valueMultiplier)
			if err != nil {
				_ = level.Debug(e.logger).Log("error", err, "query_field_name",
					currentCell.qField, "raw_value", currentCell.rawValue)

				continue
			}

			metricCh <- prometheus.MustNewConstMetric(metricInfo.desc, metricInfo.mType, num, uuid)
		}
	}
}

func scrape(qFields []qField, nvidiaSmiCommand string, command runCmd) (int, *table[string], error) {
	qFieldsJoined := strings.Join(QFieldSliceToStringSlice(qFields), ",")

	cmdAndArgs := strings.Fields(nvidiaSmiCommand)
	cmdAndArgs = append(cmdAndArgs, fmt.Sprintf("--query-gpu=%s", qFieldsJoined))
	cmdAndArgs = append(cmdAndArgs, "--format=csv")

	var stdout bytes.Buffer

	var stderr bytes.Buffer

	cmd := exec.Command(cmdAndArgs[0], cmdAndArgs[1:]...) //nolint:gosec
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := command(cmd)
	if err != nil {
		exitCode := -1

		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		}

		return exitCode, nil, fmt.Errorf("%w: command failed. code: %d | command: %s | stdout: %s | stderr: %s",
			err, exitCode, strings.Join(cmdAndArgs, " "), stdout.String(), stderr.String())
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

//nolint:gomnd
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
		return parseSanitizedValueWithBestEffort(val, valueMultiplier)
	}
}

func parseSanitizedValueWithBestEffort(sanitizedValue string, valueMultiplier float64) (float64, error) {
	allNums := numericRegex.FindAllString(sanitizedValue, 2) //nolint:gomnd
	if len(allNums) != 1 {
		return -1, fmt.Errorf("%w: %s", ErrParseNumber, sanitizedValue)
	}

	parsed, err := strconv.ParseFloat(allNums[0], floatBitSize)
	if err != nil {
		return -1, fmt.Errorf("failed to parse float: %w", err)
	}

	return parsed * valueMultiplier, nil
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

	//nolint:gocritic
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

func getFallbackValues(qFields []qField) ([]rField, error) {
	rFields := make([]rField, len(qFields))

	counter := 0

	for _, q := range qFields {
		val, contains := fallbackQFieldToRFieldMap[q]
		if !contains {
			return nil, fmt.Errorf("%w: %s", ErrUnexpectedQueryField, q)
		}

		rFields[counter] = val
		counter++
	}

	return rFields, nil
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

func removeDuplicates[T comparable](qFields []T) []T {
	valMap := make(map[T]struct{})

	var uniques []T

	for _, field := range qFields {
		_, exists := valMap[field]
		if !exists {
			uniques = append(uniques, field)
			valMap[field] = struct{}{}
		}
	}

	return uniques
}
