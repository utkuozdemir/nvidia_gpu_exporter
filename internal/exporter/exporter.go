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
	failedScrapesTotal    prometheus.Counter
	exitCode              prometheus.Gauge
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
}

// Collect fetches the latest reading from the source and delivers it as
// Prometheus metrics. It implements prometheus.Collector.
func (e *GPUExporter) Collect(metricCh chan<- prometheus.Metric) {
	snapshot := e.source.Latest(e.ctx)

	e.exitCode.Set(float64(snapshot.ExitCode))
	e.sendMetric(metricCh, e.exitCode)

	if !snapshot.Success {
		metricCh <- e.failedScrapesTotal

		e.failedScrapesTotal.Inc()

		return
	}

	for _, currentRow := range snapshot.Table.Rows {
		e.renderRow(metricCh, currentRow)
	}
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
