package exporter

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/util"
)

const DefaultPrefix = "nvidia_smi"

// GPUExporter renders the latest collection as Prometheus metrics. It is
// agnostic to how the reading is produced: the source may collect inline on
// each scrape or serve a cached result from background collection.
type GPUExporter struct {
	prefix                string
	fields                nvidiasmi.ResolvedFields
	qFieldToMetricInfoMap map[nvidiasmi.QField]MetricInfo
	source                collect.Source
	failedScrapesDesc     *prometheus.Desc
	exitCodeDesc          *prometheus.Desc
	collectSuccessDesc    *prometheus.Desc
	collectTimestampDesc  *prometheus.Desc
	collectDurationDesc   *prometheus.Desc
	gpuInfoDesc           *prometheus.Desc
	logger                *slog.Logger
	ctx                   context.Context //nolint:containedctx
}

func New(
	ctx context.Context,
	prefix string,
	fields nvidiasmi.ResolvedFields,
	source collect.Source,
	logger *slog.Logger,
) *GPUExporter {
	qFieldToMetricInfoMap := BuildQFieldToMetricInfoMap(prefix, fields.Returned, logger)

	infoLabels := make([]string, len(fields.Info))
	for i, infoField := range fields.Info {
		infoLabels[i] = infoField.Label
	}

	return &GPUExporter{
		ctx:                   ctx,
		prefix:                prefix,
		fields:                fields,
		qFieldToMetricInfoMap: qFieldToMetricInfoMap,
		source:                source,
		logger:                logger,
		failedScrapesDesc: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "failed_scrapes_total"),
			"Number of failed collections",
			nil,
			nil),
		exitCodeDesc: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "command_exit_code"),
			"Exit code of the most recent nvidia-smi run",
			nil,
			nil),
		collectSuccessDesc: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "last_collect_success"),
			"Whether the most recent collection succeeded (1) or not (0)",
			nil,
			nil),
		collectTimestampDesc: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "last_collect_success_timestamp_seconds"),
			"Unix timestamp of the most recent successful collection",
			nil,
			nil),
		collectDurationDesc: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "last_collect_duration_seconds"),
			"Duration of the most recent collection",
			nil,
			nil),
		gpuInfoDesc: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "gpu_info"),
			fmt.Sprintf("A metric with a constant '1' value labeled by gpu %s.",
				strings.Join(infoLabels, ", ")),
			infoLabels,
			nil),
	}
}

// Describe describes all the metrics ever exported by the exporter. It
// implements prometheus.Collector.
func (e *GPUExporter) Describe(descCh chan<- *prometheus.Desc) {
	for _, m := range e.qFieldToMetricInfoMap {
		e.sendDesc(descCh, m.desc)
	}

	e.sendDesc(descCh, e.failedScrapesDesc)
	e.sendDesc(descCh, e.exitCodeDesc)
	e.sendDesc(descCh, e.collectSuccessDesc)
	e.sendDesc(descCh, e.collectTimestampDesc)
	e.sendDesc(descCh, e.collectDurationDesc)
	e.sendDesc(descCh, e.gpuInfoDesc)
}

// Collect fetches the latest reading from the source and delivers it as
// Prometheus metrics. It implements prometheus.Collector.
func (e *GPUExporter) Collect(metricCh chan<- prometheus.Metric) {
	snapshot := e.source.Latest(e.ctx)

	e.renderHealth(metricCh, snapshot)

	if snapshot.Table == nil {
		return
	}

	for _, currentRow := range snapshot.Table.Rows {
		e.renderRow(metricCh, currentRow)
	}
}

// renderHealth emits the collection health metrics from the snapshot's
// explicit state. The failure counter and the success gauge are always
// emitted (the gauge reads 0 before the first attempt); the exit code and
// duration are omitted until a first collection completes, and the success
// timestamp until a first success, rather than reporting meaningless zeros.
func (e *GPUExporter) renderHealth(metricCh chan<- prometheus.Metric, snapshot collect.Snapshot) {
	e.sendConst(metricCh, e.failedScrapesDesc, prometheus.CounterValue, float64(snapshot.Failures))

	success := 0.0
	if snapshot.Success {
		success = 1
	}

	e.sendConst(metricCh, e.collectSuccessDesc, prometheus.GaugeValue, success)

	if snapshot.Attempted {
		e.sendConst(metricCh, e.exitCodeDesc, prometheus.GaugeValue, float64(snapshot.ExitCode))
		e.sendConst(metricCh, e.collectDurationDesc, prometheus.GaugeValue, snapshot.Duration.Seconds())
	}

	if !snapshot.LastSuccess.IsZero() {
		e.sendConst(metricCh, e.collectTimestampDesc, prometheus.GaugeValue,
			float64(snapshot.LastSuccess.Unix()))
	}
}

// sendConst emits one constant metric, logging instead of failing if it cannot
// be built.
func (e *GPUExporter) sendConst(
	metricCh chan<- prometheus.Metric,
	desc *prometheus.Desc,
	valueType prometheus.ValueType,
	value float64,
) {
	metric, err := prometheus.NewConstMetric(desc, valueType, value)
	if err != nil {
		e.logger.Error("failed to create metric", "err", err, "desc", desc.String())

		return
	}

	e.sendMetric(metricCh, metric)
}

// renderRow emits the gpu_info metric and one metric per queried field for a
// single GPU row.
func (e *GPUExporter) renderRow(metricCh chan<- prometheus.Metric, row nvidiasmi.Row) {
	uuid := strings.TrimPrefix(
		strings.ToLower(row.QFieldToCells[nvidiasmi.UUIDQField].RawValue),
		"gpu-",
	)

	labelValues := make([]string, len(e.fields.Info))

	for idx, infoField := range e.fields.Info {
		if infoField.QField == nvidiasmi.UUIDQField {
			labelValues[idx] = uuid

			continue
		}

		labelValues[idx] = row.QFieldToCells[infoField.QField].RawValue
	}

	infoMetric, infoMetricErr := prometheus.NewConstMetric(e.gpuInfoDesc, prometheus.GaugeValue,
		1, labelValues...)
	if infoMetricErr != nil {
		e.logger.Error("failed to create info metric", "err", infoMetricErr)

		return
	}

	e.sendMetric(metricCh, infoMetric)

	for _, currentCell := range row.Cells {
		metricInfo := e.qFieldToMetricInfoMap[currentCell.QField]

		num, numErr := nvidiasmi.TransformRawValue(currentCell.RawValue, metricInfo.ValueMultiplier)
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

// sendMetric delivers unconditionally: the Prometheus registry drains the
// channel until Collect returns, and delivery must not depend on the process
// context. With shutdown-on-error, the fatal collection cancels that context
// before rendering, and the final scrape still has to carry the health
// metrics that explain the shutdown.
func (e *GPUExporter) sendMetric(metricCh chan<- prometheus.Metric, metric prometheus.Metric) {
	metricCh <- metric
}

func (e *GPUExporter) sendDesc(descCh chan<- *prometheus.Desc, desc *prometheus.Desc) {
	descCh <- desc
}

type MetricInfo struct {
	desc            *prometheus.Desc
	MType           prometheus.ValueType
	ValueMultiplier float64
}

func BuildQFieldToMetricInfoMap(
	prefix string,
	qFieldtoRFieldMap map[nvidiasmi.QField]nvidiasmi.RField,
	logger *slog.Logger,
) map[nvidiasmi.QField]MetricInfo {
	result := make(map[nvidiasmi.QField]MetricInfo)
	for qField, rField := range qFieldtoRFieldMap {
		result[qField] = BuildMetricInfo(prefix, rField, logger)
	}

	return result
}

func BuildMetricInfo(prefix string, rField nvidiasmi.RField, logger *slog.Logger) MetricInfo {
	fqName, multiplier := BuildFQNameAndMultiplier(prefix, rField, logger)
	desc := prometheus.NewDesc(fqName, string(rField), []string{"uuid"}, nil)

	return MetricInfo{
		desc:            desc,
		MType:           prometheus.GaugeValue,
		ValueMultiplier: multiplier,
	}
}

func BuildFQNameAndMultiplier(
	prefix string,
	rField nvidiasmi.RField,
	logger *slog.Logger,
) (string, float64) {
	rFieldStr := string(rField)
	suffixTransformed := rFieldStr
	multiplier := 1.0
	split := strings.Split(rFieldStr, " ")[0]

	switch {
	case strings.HasSuffix(rFieldStr, " [W]"):
		suffixTransformed = split + "_watts"
	case strings.HasSuffix(rFieldStr, " [MHz]"):
		suffixTransformed = split + "_clock_hz"
		multiplier = 1000000
	case strings.HasSuffix(rFieldStr, " [MiB]"):
		suffixTransformed = split + "_bytes"
		multiplier = 1048576
	case strings.HasSuffix(rFieldStr, " [%]"):
		suffixTransformed = split + "_ratio"
		multiplier = 0.01
	case strings.HasSuffix(rFieldStr, " [us]"):
		suffixTransformed = split + "_seconds"
		multiplier = 0.000001
	}

	suffixTransformed = strings.ReplaceAll(suffixTransformed, ".", "_")
	suffixTransformed = util.ToSnakeCase(suffixTransformed)

	if strings.ContainsAny(suffixTransformed, " []") {
		suffixTransformed = strings.ReplaceAll(suffixTransformed, " [", "_")
		suffixTransformed = strings.ReplaceAll(suffixTransformed, "]", "")

		logger.Error("returned field contains unexpected characters, "+
			"it is parsed it with best effort, but it might get renamed in the future. "+
			"please report it in the project's issue tracker",
			"rfield_name", rFieldStr,
			"parsed_name", suffixTransformed,
		)
	}

	fqName := prometheus.BuildFQName(prefix, "", suffixTransformed)

	return fqName, multiplier
}
