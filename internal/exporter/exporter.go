package exporter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/util"
)

const DefaultPrefix = "nvidia_smi"

// ExitCodeMetric names the per-backend collection status metric. The exec
// backend reports the process exit code; the nvml backend has no process, so
// it reports the NVML return code under a different name rather than
// silently changing an established metric's meaning.
type ExitCodeMetric struct {
	Name string
	Help string
}

// ExecExitCodeMetric is the exec backend's collection status metric.
var ExecExitCodeMetric = ExitCodeMetric{
	Name: "command_exit_code",
	Help: "Exit code of the most recent nvidia-smi run",
}

// NVMLReturnCodeMetric is the nvml backend's collection status metric.
var NVMLReturnCodeMetric = ExitCodeMetric{
	Name: "nvml_return_code",
	Help: "NVML return code of the most recent collection (0 = success)",
}

// uuidLabel is the GPU identity label every per-GPU metric carries.
const uuidLabel = "uuid"

// computeAppLabels is the label set on the per-process metrics.
var computeAppLabels = []string{uuidLabel, "pid", "process_name"}

// computeAppMIGLabels is the per-process label set with MIG attribution
// (opt-in: adding labels changes the series identity of a shipped family).
var computeAppMIGLabels = []string{uuidLabel, "pid", "process_name", "gpu_instance_id", "compute_instance_id"}

// Features selects the conditionally-described metric families. Each one
// follows the compute-apps precedent: its descriptors exist only when the
// feature is enabled, so Describe and Collect stay consistent and a disabled
// feature leaves no trace in the output.
type Features struct {
	// ComputeApps enables the per-process metric families.
	ComputeApps bool
	// ComputeAppMIGLabels adds the MIG attribution labels to the
	// per-process metrics (nvml backend, --collect.compute-apps-mig).
	ComputeAppMIGLabels bool
	// PCIeThroughput enables the per-GPU PCIe throughput gauges (nvml
	// backend, --collect.pcie-throughput).
	PCIeThroughput bool
	// Energy enables the per-GPU cumulative energy counter (nvml backend).
	Energy bool
	// MIG enables the per-MIG-instance metric families (nvml backend).
	MIG bool
	// XIDEvents enables the XID error counter families (nvml backend). The
	// values come from the XIDSource passed to New, not from the snapshot.
	XIDEvents bool
}

// XIDSource serves the cumulative XID error counts. It is read at scrape
// time, independently of the collection pipeline, so the counters stay
// visible while collections fail (which is exactly when XIDs happen). The
// implementation must be safe for concurrent use.
type XIDSource interface {
	XIDCounts() []collect.XIDCounter
}

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
	appInfoDesc           *prometheus.Desc
	appMemoryDesc         *prometheus.Desc
	appCountDesc          *prometheus.Desc
	appsSuccessDesc       *prometheus.Desc
	pcieTxDesc            *prometheus.Desc
	pcieRxDesc            *prometheus.Desc
	energyDesc            *prometheus.Desc
	migDescs              *migDescs
	appMIGLabels          bool
	xids                  XIDSource
	xidCountDesc          *prometheus.Desc
	xidTimestampDesc      *prometheus.Desc
	logger                *slog.Logger
	ctx                   context.Context //nolint:containedctx
}

// migDescs bundles the per-MIG-instance descriptors, nil as a whole when the
// feature is off.
type migDescs struct {
	info             *prometheus.Desc
	memTotal         *prometheus.Desc
	memUsed          *prometheus.Desc
	memFree          *prometheus.Desc
	memReserved      *prometheus.Desc
	graphicsActivity *prometheus.Desc
	smActivity       *prometheus.Desc
	smOccupancy      *prometheus.Desc
	tensorActivity   *prometheus.Desc
	pcieTx           *prometheus.Desc
	pcieRx           *prometheus.Desc
}

// all lists the bundled descriptors, for Describe.
func (m *migDescs) all() []*prometheus.Desc {
	return []*prometheus.Desc{
		m.info, m.memTotal, m.memUsed, m.memFree, m.memReserved,
		m.graphicsActivity, m.smActivity, m.smOccupancy, m.tensorActivity,
		m.pcieTx, m.pcieRx,
	}
}

// New builds the exporter. A metric family whose feature is off in features
// gets nil descriptors: the exporter neither describes nor emits its series.
func New(
	ctx context.Context,
	prefix string,
	fields nvidiasmi.ResolvedFields,
	source collect.Source,
	features Features,
	xids XIDSource,
	exitCodeMetric ExitCodeMetric,
	logger *slog.Logger,
) *GPUExporter {
	qFieldToMetricInfoMap := BuildQFieldToMetricInfoMap(prefix, fields.Returned, logger)

	// cuda_version rides gpu_info but is not a query field: it comes from the
	// collection's extras, so it is appended after the resolved info fields
	// rather than joining them (the qfield schema must not see it)
	infoLabels := make([]string, 0, len(fields.Info)+1)
	for _, infoField := range fields.Info {
		infoLabels = append(infoLabels, infoField.Label)
	}

	infoLabels = append(infoLabels, "cuda_version")

	appInfoDesc, appMemoryDesc, appCountDesc, appsSuccessDesc := newComputeAppDescs(
		prefix, features.ComputeApps, features.ComputeAppMIGLabels)
	pcieTxDesc, pcieRxDesc := newPCIeDescs(prefix, features.PCIeThroughput)

	exp := &GPUExporter{
		ctx:                   ctx,
		prefix:                prefix,
		fields:                fields,
		qFieldToMetricInfoMap: qFieldToMetricInfoMap,
		source:                source,
		appInfoDesc:           appInfoDesc,
		appMemoryDesc:         appMemoryDesc,
		appCountDesc:          appCountDesc,
		appsSuccessDesc:       appsSuccessDesc,
		pcieTxDesc:            pcieTxDesc,
		pcieRxDesc:            pcieRxDesc,
		energyDesc:            newEnergyDesc(prefix, features.Energy),
		migDescs:              newMIGDescs(prefix, features.MIG),
		appMIGLabels:          features.ComputeAppMIGLabels,
		xids:                  xids,
		logger:                logger,
		gpuInfoDesc: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "gpu_info"),
			fmt.Sprintf("A metric with a constant '1' value labeled by gpu %s.",
				strings.Join(infoLabels, ", ")),
			infoLabels,
			nil),
	}

	addHealthDescs(exp, prefix, exitCodeMetric)
	addXIDDescs(exp, prefix, features.XIDEvents)

	return exp
}

// addXIDDescs builds the XID error counter descriptors, left nil when the
// feature is disabled.
func addXIDDescs(exp *GPUExporter, prefix string, enabled bool) {
	if !enabled {
		return
	}

	exp.xidCountDesc = prometheus.NewDesc(
		prometheus.BuildFQName(prefix, "", "xid_errors_total"),
		"Number of XID errors observed on the GPU since the exporter started. "+
			"A series appears when its first event arrives; earlier history cannot be replayed.",
		[]string{uuidLabel, "xid"},
		nil)
	exp.xidTimestampDesc = prometheus.NewDesc(
		prometheus.BuildFQName(prefix, "", "xid_last_timestamp_seconds"),
		"Unix timestamp of the most recently observed XID error, as received by the exporter "+
			"(the driver events carry no timestamp of their own).",
		[]string{uuidLabel, "xid"},
		nil)
}

// addHealthDescs builds the collection health descriptors.
func addHealthDescs(exp *GPUExporter, prefix string, exitCodeMetric ExitCodeMetric) {
	exp.failedScrapesDesc = prometheus.NewDesc(
		prometheus.BuildFQName(prefix, "", "failed_scrapes_total"),
		"Number of failed collections",
		nil,
		nil)
	exp.exitCodeDesc = prometheus.NewDesc(
		prometheus.BuildFQName(prefix, "", exitCodeMetric.Name),
		exitCodeMetric.Help,
		nil,
		nil)
	exp.collectSuccessDesc = prometheus.NewDesc(
		prometheus.BuildFQName(prefix, "", "last_collect_success"),
		"Whether the most recent collection succeeded (1) or not (0)",
		nil,
		nil)
	exp.collectTimestampDesc = prometheus.NewDesc(
		prometheus.BuildFQName(prefix, "", "last_collect_success_timestamp_seconds"),
		"Unix timestamp of the most recent successful collection",
		nil,
		nil)
	exp.collectDurationDesc = prometheus.NewDesc(
		prometheus.BuildFQName(prefix, "", "last_collect_duration_seconds"),
		"Duration of the most recent collection",
		nil,
		nil)
}

// newComputeAppDescs builds the per-process metric descriptors (info, memory,
// count, success), all nil when the feature is disabled.
func newComputeAppDescs(
	prefix string,
	enabled bool,
	migLabels bool,
) (*prometheus.Desc, *prometheus.Desc, *prometheus.Desc, *prometheus.Desc) {
	if !enabled {
		return nil, nil, nil, nil
	}

	labels := computeAppLabels
	if migLabels {
		labels = computeAppMIGLabels
	}

	info := prometheus.NewDesc(
		prometheus.BuildFQName(prefix, "", "compute_app_info"),
		"A metric with a constant '1' value labeled by the identity of a process with a compute context on a GPU.",
		labels,
		nil)
	memory := prometheus.NewDesc(
		prometheus.BuildFQName(prefix, "", "compute_app_used_memory_bytes"),
		"GPU memory used by the process. Absent when the driver cannot report it (e.g. Windows WDDM).",
		labels,
		nil)
	count := prometheus.NewDesc(
		prometheus.BuildFQName(prefix, "", "compute_apps"),
		"Number of processes with a compute context on the GPU.",
		[]string{uuidLabel},
		nil)
	success := prometheus.NewDesc(
		prometheus.BuildFQName(prefix, "", "compute_apps_last_collect_success"),
		"Whether the most recent per-process collection succeeded (1) or not (0)",
		nil,
		nil)

	return info, memory, count, success
}

// newPCIeDescs builds the PCIe throughput descriptors, nil when the feature
// is disabled.
func newPCIeDescs(prefix string, enabled bool) (*prometheus.Desc, *prometheus.Desc) {
	if !enabled {
		return nil, nil
	}

	tx := prometheus.NewDesc(
		prometheus.BuildFQName(prefix, "", "pcie_throughput_tx_bytes_per_second"),
		"PCIe traffic transmitted by the GPU, sampled by the driver over a dedicated 20ms window.",
		[]string{uuidLabel},
		nil)
	rx := prometheus.NewDesc(
		prometheus.BuildFQName(prefix, "", "pcie_throughput_rx_bytes_per_second"),
		"PCIe traffic received by the GPU, sampled by the driver over a dedicated 20ms window.",
		[]string{uuidLabel},
		nil)

	return tx, rx
}

// newEnergyDesc builds the energy counter descriptor, nil when the feature is
// disabled.
func newEnergyDesc(prefix string, enabled bool) *prometheus.Desc {
	if !enabled {
		return nil
	}

	return prometheus.NewDesc(
		prometheus.BuildFQName(prefix, "", "energy_joules_total"),
		"Total energy consumed by the GPU in joules since the driver was last loaded. "+
			"Resets on a driver reload; absent on GPUs that cannot report it.",
		[]string{uuidLabel},
		nil)
}

// newMIGDescs builds the per-MIG-instance descriptors, nil when the feature
// is disabled. Memory belongs to the MIG device (mig_uuid); utilization is
// attributed per GPU instance, which may host several MIG devices.
func newMIGDescs(prefix string, enabled bool) *migDescs {
	if !enabled {
		return nil
	}

	instanceLabels := []string{uuidLabel, "gpu_instance_id"}

	memDesc := func(kind string) *prometheus.Desc {
		return prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "mig_memory_"+kind+"_bytes"),
			"Memory of the GPU instance ("+kind+"). The framebuffer belongs to the GPU "+
				"instance and is shared by its compute instances (verified live: sibling "+
				"compute instances report identical values).",
			instanceLabels,
			nil)
	}

	activityDesc := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", name),
			help+" Computed over the window between the two most recent collections; "+
				"absent on the first collection that sees the GPU instance.",
			instanceLabels,
			nil)
	}

	return &migDescs{
		info: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "mig_info"),
			"A metric with a constant '1' value labeled by the identity of a MIG device: "+
				"the parent GPU's uuid, the MIG device's own uuid, its GPU instance and "+
				"compute instance ids, and its profile.",
			[]string{uuidLabel, "mig_uuid", "gpu_instance_id", "compute_instance_id", "profile"},
			nil),
		memTotal:    memDesc("total"),
		memUsed:     memDesc("used"),
		memFree:     memDesc("free"),
		memReserved: memDesc("reserved"),
		graphicsActivity: activityDesc("mig_graphics_activity_ratio",
			"Fraction of time the GPU instance's graphics/compute engines were active."),
		smActivity: activityDesc("mig_sm_activity_ratio",
			"Fraction of time the GPU instance's SMs were active."),
		smOccupancy: activityDesc("mig_sm_occupancy_ratio",
			"Fraction of the GPU instance's resident warp slots that were occupied."),
		tensorActivity: activityDesc("mig_tensor_activity_ratio",
			"Fraction of time the GPU instance's tensor pipes were active."),
		pcieTx: activityDesc("mig_pcie_throughput_tx_bytes_per_second",
			"PCIe traffic transmitted by the GPU instance."),
		pcieRx: activityDesc("mig_pcie_throughput_rx_bytes_per_second",
			"PCIe traffic received by the GPU instance."),
	}
}

// WithContext returns a collector that renders the same metrics but bounds
// its collection by the given context. The HTTP handler uses it to bound each
// scrape's collection by the scrape's own lifetime. The copy shares all
// state with the original; only the context differs, and everything shared is
// immutable after construction or owns its own synchronization.
func (e *GPUExporter) WithContext(ctx context.Context) *GPUExporter {
	scoped := *e
	scoped.ctx = ctx

	return &scoped
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

	if e.appInfoDesc != nil {
		e.sendDesc(descCh, e.appInfoDesc)
		e.sendDesc(descCh, e.appMemoryDesc)
		e.sendDesc(descCh, e.appCountDesc)
		e.sendDesc(descCh, e.appsSuccessDesc)
	}

	if e.pcieTxDesc != nil {
		e.sendDesc(descCh, e.pcieTxDesc)
		e.sendDesc(descCh, e.pcieRxDesc)
	}

	if e.energyDesc != nil {
		e.sendDesc(descCh, e.energyDesc)
	}

	if e.migDescs != nil {
		for _, desc := range e.migDescs.all() {
			e.sendDesc(descCh, desc)
		}
	}

	if e.xidCountDesc != nil {
		e.sendDesc(descCh, e.xidCountDesc)
		e.sendDesc(descCh, e.xidTimestampDesc)
	}
}

// Collect fetches the latest reading from the source and delivers it as
// Prometheus metrics. It implements prometheus.Collector.
func (e *GPUExporter) Collect(metricCh chan<- prometheus.Metric) {
	snapshot := e.source.Latest(e.ctx)

	e.renderHealth(metricCh, snapshot)

	// the XID counters render before the no-data return below: a GPU
	// throwing XIDs typically also fails collections, and that is exactly
	// when these series must stay visible
	e.renderXIDs(metricCh)

	if snapshot.Table == nil {
		return
	}

	for _, currentRow := range snapshot.Table.Rows {
		e.renderRow(metricCh, currentRow, snapshot.Extras.CUDAVersion)
	}

	e.renderApps(metricCh, snapshot)
	e.renderExtras(metricCh, snapshot)
}

// renderXIDs emits the XID error counters from the dedicated source. Unlike
// every other family they do not come from the snapshot: the counts are
// event-driven, cumulative in-process state served fresh on every scrape.
func (e *GPUExporter) renderXIDs(metricCh chan<- prometheus.Metric) {
	if e.xidCountDesc == nil || e.xids == nil {
		return
	}

	for _, counter := range e.xids.XIDCounts() {
		xid := strconv.FormatUint(counter.XID, 10)

		e.sendLabeledCounter(metricCh, e.xidCountDesc, float64(counter.Count), counter.UUID, xid)
		e.sendLabeledGauge(metricCh, e.xidTimestampDesc,
			float64(counter.LastSeen.UnixNano())/1e9, counter.UUID, xid)
	}
}

// sendLabeledCounter emits one constant counter with the given label values,
// logging instead of failing if it cannot be built.
func (e *GPUExporter) sendLabeledCounter(
	metricCh chan<- prometheus.Metric,
	desc *prometheus.Desc,
	value float64,
	labelValues ...string,
) {
	metric, err := prometheus.NewConstMetric(desc, prometheus.CounterValue, value, labelValues...)
	if err != nil {
		e.logger.Error("failed to create metric", "err", err, "desc", desc.String())

		return
	}

	e.sendMetric(metricCh, metric)
}

// renderExtras emits the backend-specific families carried outside the
// query-field schema. A nil descriptor (feature off) or an empty family emits
// nothing: per family, absence is the signal for "could not be read".
func (e *GPUExporter) renderExtras(metricCh chan<- prometheus.Metric, snapshot collect.Snapshot) {
	if e.pcieTxDesc != nil {
		for _, sample := range snapshot.Extras.PCIe {
			e.sendConstWithUUID(metricCh, e.pcieTxDesc, prometheus.GaugeValue,
				sample.TXBytesPerSecond, sample.UUID)
			e.sendConstWithUUID(metricCh, e.pcieRxDesc, prometheus.GaugeValue,
				sample.RXBytesPerSecond, sample.UUID)
		}
	}

	if e.energyDesc != nil {
		for _, counter := range snapshot.Extras.Energy {
			e.sendConstWithUUID(metricCh, e.energyDesc, prometheus.CounterValue,
				counter.Joules, counter.UUID)
		}
	}

	if e.migDescs != nil {
		// utilization is per GPU instance while the entries are per MIG
		// device (compute instance): emit each GPU instance's series once
		emittedGIs := map[string]bool{}

		for _, instance := range snapshot.Extras.MIG {
			e.renderMIGInstance(metricCh, instance, emittedGIs)
		}
	}
}

// renderMIGInstance emits one MIG device's info series, plus its GPU
// instance's memory and utilization when the GPU instance was not rendered
// yet this scrape: memory and activity belong to the GPU instance (its
// compute instances share both), so one series per GPU instance.
func (e *GPUExporter) renderMIGInstance(
	metricCh chan<- prometheus.Metric,
	instance collect.MIGInstance,
	emittedGIs map[string]bool,
) {
	e.sendLabeledGauge(metricCh, e.migDescs.info, 1,
		instance.ParentUUID, instance.UUID, instance.GPUInstanceID, instance.ComputeInstanceID, instance.Profile)

	giKey := instance.ParentUUID + "/" + instance.GPUInstanceID
	if emittedGIs[giKey] {
		return
	}

	emittedGIs[giKey] = true

	instanceLabels := []string{instance.ParentUUID, instance.GPUInstanceID}

	if memory := instance.Memory; memory != nil {
		e.sendLabeledGauge(metricCh, e.migDescs.memTotal, float64(memory.Total), instanceLabels...)
		e.sendLabeledGauge(metricCh, e.migDescs.memUsed, float64(memory.Used), instanceLabels...)
		e.sendLabeledGauge(metricCh, e.migDescs.memFree, float64(memory.Free), instanceLabels...)
		e.sendLabeledGauge(metricCh, e.migDescs.memReserved, float64(memory.Reserved), instanceLabels...)
	}

	util := instance.Utilization
	if util == nil {
		return
	}

	for _, entry := range []struct {
		desc  *prometheus.Desc
		value *float64
	}{
		{e.migDescs.graphicsActivity, util.GraphicsActivityRatio},
		{e.migDescs.smActivity, util.SMActivityRatio},
		{e.migDescs.smOccupancy, util.SMOccupancyRatio},
		{e.migDescs.tensorActivity, util.TensorActivityRatio},
		{e.migDescs.pcieTx, util.PCIeTXBytesPerSecond},
		{e.migDescs.pcieRx, util.PCIeRXBytesPerSecond},
	} {
		if entry.value == nil {
			continue
		}

		e.sendLabeledGauge(metricCh, entry.desc, *entry.value, instanceLabels...)
	}
}

// sendLabeledGauge emits one constant gauge with the given label values,
// logging instead of failing if it cannot be built.
func (e *GPUExporter) sendLabeledGauge(
	metricCh chan<- prometheus.Metric,
	desc *prometheus.Desc,
	value float64,
	labelValues ...string,
) {
	metric, err := prometheus.NewConstMetric(desc, prometheus.GaugeValue, value, labelValues...)
	if err != nil {
		e.logger.Error("failed to create metric", "err", err, "desc", desc.String())

		return
	}

	e.sendMetric(metricCh, metric)
}

// sendConstWithUUID emits one constant metric carrying the uuid label,
// logging instead of failing if it cannot be built.
func (e *GPUExporter) sendConstWithUUID(
	metricCh chan<- prometheus.Metric,
	desc *prometheus.Desc,
	valueType prometheus.ValueType,
	value float64,
	uuid string,
) {
	metric, err := prometheus.NewConstMetric(desc, valueType, value, uuid)
	if err != nil {
		e.logger.Error("failed to create metric", "err", err, "desc", desc.String(), "uuid", uuid)

		return
	}

	e.sendMetric(metricCh, metric)
}

// renderApps emits the per-process metrics. Failure must not look like idle:
// when the per-process query failed, only the success gauge (0) is emitted and
// every per-process series is suppressed, including the zero-filled per-GPU
// count, so "no processes" (count 0, success 1) stays distinguishable from
// "could not observe the process list" (no count series, success 0).
func (e *GPUExporter) renderApps(metricCh chan<- prometheus.Metric, snapshot collect.Snapshot) {
	if e.appInfoDesc == nil || !snapshot.AppsAttempted {
		return
	}

	success := 0.0
	if snapshot.AppsSuccess {
		success = 1
	}

	e.sendConst(metricCh, e.appsSuccessDesc, prometheus.GaugeValue, success)

	if !snapshot.AppsSuccess {
		return
	}

	// zero-fill the per-GPU count from the GPU table, so an idle GPU reports
	// an explicit 0 instead of a missing series
	counts := make(map[string]float64, len(snapshot.Table.Rows))
	for _, row := range snapshot.Table.Rows {
		counts[nvidiasmi.NormalizeUUID(row.QFieldToCells[nvidiasmi.UUIDQField].RawValue)] = 0
	}

	for _, app := range snapshot.Apps {
		counts[app.GPUUUID]++

		e.renderApp(metricCh, app)
	}

	for uuid, count := range counts {
		metric, err := prometheus.NewConstMetric(e.appCountDesc, prometheus.GaugeValue, count, uuid)
		if err != nil {
			e.logger.Error("failed to create compute apps count metric", "err", err, "uuid", uuid)

			continue
		}

		e.sendMetric(metricCh, metric)
	}
}

// renderApp emits the info and memory metrics for a single process.
func (e *GPUExporter) renderApp(metricCh chan<- prometheus.Metric, app nvidiasmi.ComputeApp) {
	e.sendAppMetric(metricCh, e.appInfoDesc, 1, app)

	if nvidiasmi.IsKnownAbsentValue(app.UsedMemory) {
		// an expected state ("[N/A]" on Windows WDDM, "[Insufficient
		// Permissions]" in restricted containers), reported for every
		// process on every collection: skip without logging
		return
	}

	num, err := nvidiasmi.TransformRawValue(app.UsedMemory, nvidiasmi.UsedMemoryMultiplier)
	if err != nil {
		e.logger.Debug("failed to transform per-process memory value",
			"err", err, "raw_value", app.UsedMemory, "pid", app.PID)

		return
	}

	e.sendAppMetric(metricCh, e.appMemoryDesc, num, app)
}

// sendAppMetric emits one per-process gauge carrying the compute app labels.
func (e *GPUExporter) sendAppMetric(
	metricCh chan<- prometheus.Metric,
	desc *prometheus.Desc,
	value float64,
	app nvidiasmi.ComputeApp,
) {
	labelValues := []string{app.GPUUUID, app.PID, app.ProcessName}
	if e.appMIGLabels {
		labelValues = append(labelValues, app.GPUInstanceID, app.ComputeInstanceID)
	}

	metric, err := prometheus.NewConstMetric(desc, prometheus.GaugeValue, value, labelValues...)
	if err != nil {
		e.logger.Error("failed to create per-process metric", "err", err, "pid", app.PID)

		return
	}

	e.sendMetric(metricCh, metric)
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
// single GPU row. cudaVersion fills the appended cuda_version label, which
// comes from the collection's extras rather than the row's cells.
func (e *GPUExporter) renderRow(metricCh chan<- prometheus.Metric, row nvidiasmi.Row, cudaVersion string) {
	uuid := nvidiasmi.NormalizeUUID(row.QFieldToCells[nvidiasmi.UUIDQField].RawValue)

	labelValues := make([]string, len(e.fields.Info)+1)

	for idx, infoField := range e.fields.Info {
		if infoField.QField == nvidiasmi.UUIDQField {
			labelValues[idx] = uuid

			continue
		}

		labelValues[idx] = row.QFieldToCells[infoField.QField].RawValue
	}

	labelValues[len(e.fields.Info)] = cudaVersion

	infoMetric, infoMetricErr := prometheus.NewConstMetric(e.gpuInfoDesc, prometheus.GaugeValue,
		1, labelValues...)
	if infoMetricErr != nil {
		e.logger.Error("failed to create info metric", "err", infoMetricErr)

		return
	}

	e.sendMetric(metricCh, infoMetric)

	for _, currentCell := range row.Cells {
		metricInfo := e.qFieldToMetricInfoMap[currentCell.QField]

		num, numErr := nvidiasmi.TransformFieldValue(
			currentCell.QField, currentCell.RawValue, metricInfo.ValueMultiplier)
		if numErr != nil {
			switch {
			case errors.Is(numErr, nvidiasmi.ErrAbsentValue):
				// expected unavailable reading (e.g. an unsupported field), skip quietly
			case nvidiasmi.IsEnumMappedField(currentCell.QField):
				// an enum field returned a value we do not map: never guess a number,
				// but surface it so a new/unexpected state is not silently invisible
				e.logger.Warn("skipping metric: unrecognized enum value", "query_field_name",
					currentCell.QField, "raw_value", currentCell.RawValue)
			default:
				e.logger.Debug("failed to transform raw value", "err", numErr, "query_field_name",
					currentCell.QField, "raw_value", currentCell.RawValue)
			}

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
	desc := prometheus.NewDesc(fqName, string(rField), []string{uuidLabel}, nil)

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
	case strings.HasSuffix(rFieldStr, " [ms]"):
		suffixTransformed = split + "_seconds"
		multiplier = 0.001
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
