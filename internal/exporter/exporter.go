package exporter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/util"
)

// QField stands for query field - the field name before the query.
type QField string

// RField stands for returned field - the field name as returned by the nvidia-smi.
type RField string

type runCmd func(cmd *exec.Cmd) error

const (
	DefaultPrefix           = "nvidia_smi"
	DefaultNvidiaSmiCommand = "nvidia-smi"

	floatBitSize = 64
)

var (
	numericRegex = regexp.MustCompile(`[+-]?(\d*[.])?\d+`)

	//nolint:gochecknoglobals
	requiredFields = []requiredField{
		{qField: uuidQField, label: "uuid"},
		{qField: nameQField, label: "name"},
		{qField: driverModelCurrentQField, label: "driver_model_current"},
		{qField: driverModelPendingQField, label: "driver_model_pending"},
		{qField: vBiosVersionQField, label: "vbios_version"},
		{qField: driverVersionQField, label: "driver_version"},
	}

	//nolint:gochecknoglobals
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
	qFields               []QField
	qFieldToMetricInfoMap map[QField]MetricInfo
	nvidiaSmiCommand      string
	failedScrapesTotal    prometheus.Counter
	exitCode              prometheus.Gauge
	gpuInfoDesc           *prometheus.Desc
	migInfoDesc           *prometheus.Desc
	logger                *slog.Logger
	Command               runCmd
	ctx                   context.Context //nolint:containedctx
}

func New(ctx context.Context, prefix string, nvidiaSmiCommand string, qFieldsRaw string,
	logger *slog.Logger,
) (*GPUExporter, error) {
	qFieldsOrdered, qFieldToRFieldMap, err := buildQFieldToRFieldMap(
		logger,
		qFieldsRaw,
		nvidiaSmiCommand,
		defaultRunCmd,
	)
	if err != nil {
		return nil, err
	}

	qFieldToMetricInfoMap := BuildQFieldToMetricInfoMap(prefix, qFieldToRFieldMap)

	infoLabels := getLabels(requiredFields)
	migInfoLabels := []string{"name", "uuid", "gpu_uuid"}
	exporter := GPUExporter{
		ctx:                   ctx,
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
		migInfoDesc: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "mig_info"),
			fmt.Sprintf("A metric with a constant '1' value labeled by mig %s.",
				strings.Join(migInfoLabels, ", ")),
			migInfoLabels,
			nil),
		Command: defaultRunCmd,
	}

	return &exporter, nil
}

func buildQFieldToRFieldMap(logger *slog.Logger, qFieldsRaw string, nvidiaSmiCommand string,
	command runCmd,
) ([]QField, map[QField]RField, error) {
	qFieldsSeparated := strings.Split(qFieldsRaw, ",")

	qFields := toQFieldSlice(qFieldsSeparated)
	for _, reqField := range requiredFields {
		qFields = append(qFields, reqField.qField)
	}

	qFields = removeDuplicates(qFields)

	if len(qFieldsSeparated) == 1 && qFieldsSeparated[0] == qFieldsAuto {
		parsed, err := ParseAutoQFields(nvidiaSmiCommand, command)
		if err != nil {
			logger.Warn(
				"failed to auto-determine query field names, falling back to the built-in list",
				"err",
				err,
			)

			keys := slices.Collect(maps.Keys(fallbackQFieldToRFieldMap))

			return keys, fallbackQFieldToRFieldMap, nil
		}

		qFields = parsed
	}

	_, resultTable, err := scrape(qFields, nvidiaSmiCommand, command)

	var rFields []RField

	if err != nil {
		logger.Warn(
			"failed to run the initial scrape, using the built-in list for field mapping",
			"err",
			err,
		)

		rFields, err = getFallbackValues(qFields)
		if err != nil {
			return nil, nil, err
		}
	} else {
		rFields = resultTable.RFields
	}

	r := make(map[QField]RField, len(qFields))
	for i, q := range qFields {
		r[q] = rFields[i]
	}

	return qFields, r, nil
}

// Describe describes all the metrics ever exported by the exporter. It
// implements prometheus.Collector.
func (e *GPUExporter) Describe(descCh chan<- *prometheus.Desc) {
	for _, m := range e.qFieldToMetricInfoMap {
		e.sendDesc(descCh, m.desc)
	}

	e.sendDesc(descCh, e.failedScrapesTotal.Desc())
	e.sendDesc(descCh, e.exitCode.Desc())
	e.sendDesc(descCh, e.gpuInfoDesc)
	e.sendDesc(descCh, e.migInfoDesc)
}

// Collect fetches the stats and delivers them as Prometheus metrics. It implements prometheus.Collector.
//
//nolint:funlen
func (e *GPUExporter) Collect(metricCh chan<- prometheus.Metric) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	exitCode, currentTable, err := scrape(e.qFields, e.nvidiaSmiCommand, e.Command)
	e.exitCode.Set(float64(exitCode))

	e.sendMetric(metricCh, e.exitCode)

	if err != nil {
		e.logger.Error("failed to collect metrics", "err", err)

		metricCh <- e.failedScrapesTotal
		e.failedScrapesTotal.Inc()

		return
	}

	for _, currentRow := range currentTable.Rows {
		uuid := strings.TrimPrefix(
			strings.ToLower(currentRow.QFieldToCells[uuidQField].RawValue),
			"gpu-",
		)
		name := currentRow.QFieldToCells[nameQField].RawValue
		driverModelCurrent := currentRow.QFieldToCells[driverModelCurrentQField].RawValue
		driverModelPending := currentRow.QFieldToCells[driverModelPendingQField].RawValue
		vBiosVersion := currentRow.QFieldToCells[vBiosVersionQField].RawValue
		driverVersion := currentRow.QFieldToCells[driverVersionQField].RawValue

		infoMetric, infoMetricErr := prometheus.NewConstMetric(e.gpuInfoDesc, prometheus.GaugeValue,
			1, uuid, name, driverModelCurrent,
			driverModelPending, vBiosVersion, driverVersion)
		if infoMetricErr != nil {
			e.logger.Error("failed to create info metric", "err", infoMetricErr)

			continue
		}

		e.sendMetric(metricCh, infoMetric)

		for _, currentCell := range currentRow.Cells {
			metricInfo := e.qFieldToMetricInfoMap[currentCell.QField]

			num, numErr := TransformRawValue(currentCell.RawValue, metricInfo.ValueMultiplier)
			if numErr != nil {
				e.logger.Debug("failed to transform raw value", "err", numErr, "query_field_name",
					currentCell.QField, "raw_value", currentCell.RawValue)

				continue
			}

			metric, metricErr := prometheus.NewConstMetric(
				metricInfo.desc,
				metricInfo.MType,
				num,
				uuid,
			)
			if metricErr != nil {
				e.logger.Error("failed to create metric", "err", metricErr, "query_field_name",
					currentCell.QField, "raw_value", currentCell.RawValue)

				continue
			}

			e.sendMetric(metricCh, metric)
		}
	}

	migDevices, err := scrapeMIG(e.nvidiaSmiCommand, e.Command)
	if err != nil {
		e.logger.Error("failed to scrape MIG devices", "err", err)

		return
	}

	for _, device := range migDevices {
		metric, err := prometheus.NewConstMetric(
			e.migInfoDesc,
			prometheus.GaugeValue,
			1,
			device.UUID,
			device.Name,
			device.GPUUUID,
		)
		if err != nil {
			e.logger.Error("failed to create MIG info metric", "err", err, "device", device)

			continue
		}

		e.sendMetric(metricCh, metric)
	}
}

func (e *GPUExporter) sendMetric(metricCh chan<- prometheus.Metric, metric prometheus.Metric) {
	select {
	case <-e.ctx.Done():
		e.logger.Info("context done, return")

		return
	case metricCh <- metric:
	}
}

func (e *GPUExporter) sendDesc(descCh chan<- *prometheus.Desc, desc *prometheus.Desc) {
	select {
	case <-e.ctx.Done():
		e.logger.Info("context done, return")

		return
	case descCh <- desc:
	}
}

func scrape(qFields []QField, nvidiaSmiCommand string, command runCmd) (int, *Table, error) {
	qFieldsJoined := strings.Join(QFieldSliceToStringSlice(qFields), ",")

	cmdAndArgs := strings.Fields(nvidiaSmiCommand)
	cmdAndArgs = append(cmdAndArgs, "--query-gpu="+qFieldsJoined)
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

		return exitCode, nil, fmt.Errorf(
			"command failed: code: %d | command: %s | stdout: %s | stderr: %s: %w",
			exitCode,
			strings.Join(cmdAndArgs, " "),
			stdout.String(),
			stderr.String(),
			err,
		)
	}

	t, err := ParseCSVIntoTable(strings.TrimSpace(stdout.String()), qFields)
	if err != nil {
		return -1, nil, err
	}

	return 0, &t, nil
}

type MIGInfo struct {
	Name    string
	UUID    string
	GPUUUID string
}

func scrapeMIG(nvidiaSmiCommand string, command runCmd) ([]MIGInfo, error) {
	cmdAndArgs := strings.Fields(nvidiaSmiCommand)
	cmdAndArgs = append(cmdAndArgs, "--list-gpus")

	var stdout bytes.Buffer

	var stderr bytes.Buffer

	cmd := exec.Command(cmdAndArgs[0], cmdAndArgs[1:]...) //nolint:gosec
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := command(cmd)
	if err != nil {
		return make([]MIGInfo, 0), fmt.Errorf("error running command: %w", err)
	}

	devices := make([]MIGInfo, 0)
	currentGPUUUID := ""

	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "GPU ") {
			uuidStart := strings.Index(line, "UUID: GPU-")
			if uuidStart != -1 {
				currentGPUUUID = strings.TrimSuffix(line[uuidStart+10:], ")")
			}

			continue
		}

		if strings.HasPrefix(line, "MIG ") {
			parts := strings.Fields(line)
			name := strings.Join(parts[0:2], " ")
			uuid := strings.TrimSuffix(parts[5], ")")

			device := MIGInfo{
				Name:    name,
				UUID:    strings.TrimPrefix(uuid, "MIG-"),
				GPUUUID: strings.TrimPrefix(currentGPUUUID, "GPU-"),
			}

			devices = append(devices, device)
		}
	}

	return devices, nil
}

type MetricInfo struct {
	desc            *prometheus.Desc
	MType           prometheus.ValueType
	ValueMultiplier float64
}

// TransformRawValue transforms a raw value into a float64.
//
//nolint:mnd
func TransformRawValue(rawValue string, valueMultiplier float64) (float64, error) {
	trimmed := strings.TrimSpace(rawValue)
	if strings.HasPrefix(trimmed, "0x") {
		decimal, err := util.HexToDecimal(trimmed)
		if err != nil {
			return 0, fmt.Errorf("failed to transform raw value %q: %w", trimmed, err)
		}

		return decimal, nil
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

func parseSanitizedValueWithBestEffort(
	sanitizedValue string,
	valueMultiplier float64,
) (float64, error) {
	allNums := numericRegex.FindAllString(sanitizedValue, 2) //nolint:mnd
	if len(allNums) != 1 {
		return -1, fmt.Errorf("could not parse number from value: %q", sanitizedValue)
	}

	parsed, err := strconv.ParseFloat(allNums[0], floatBitSize)
	if err != nil {
		return -1, fmt.Errorf("failed to parse float %q: %w", allNums[0], err)
	}

	return parsed * valueMultiplier, nil
}

func BuildQFieldToMetricInfoMap(
	prefix string,
	qFieldtoRFieldMap map[QField]RField,
) map[QField]MetricInfo {
	result := make(map[QField]MetricInfo)
	for qField, rField := range qFieldtoRFieldMap {
		result[qField] = BuildMetricInfo(prefix, rField)
	}

	return result
}

func BuildMetricInfo(prefix string, rField RField) MetricInfo {
	fqName, multiplier := BuildFQNameAndMultiplier(prefix, rField)
	desc := prometheus.NewDesc(fqName, string(rField), []string{"uuid"}, nil)

	return MetricInfo{
		desc:            desc,
		MType:           prometheus.GaugeValue,
		ValueMultiplier: multiplier,
	}
}

func BuildFQNameAndMultiplier(prefix string, rField RField) (string, float64) {
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

	metricName := util.ToSnakeCase(strings.ReplaceAll(suffixTransformed, ".", "_"))
	fqName := prometheus.BuildFQName(prefix, "", metricName)

	return fqName, multiplier
}

func getFallbackValues(qFields []QField) ([]RField, error) {
	rFields := make([]RField, len(qFields))

	counter := 0

	for _, q := range qFields {
		val, contains := fallbackQFieldToRFieldMap[q]
		if !contains {
			return nil, fmt.Errorf("unexpected query field: %q", q)
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
	qField QField
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
