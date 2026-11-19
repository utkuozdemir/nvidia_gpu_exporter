package exporter

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
)

type nvlinkLinkDescs struct {
	links                *prometheus.Desc
	activeLinks          *prometheus.Desc
	recoveryEvents       *prometheus.Desc
	recoveryFailedEvents *prometheus.Desc
}

func (n *nvlinkLinkDescs) all() []*prometheus.Desc {
	if n == nil {
		return nil
	}

	return []*prometheus.Desc{n.links, n.activeLinks, n.recoveryEvents, n.recoveryFailedEvents}
}

type pcieErrorDescs struct {
	errors       *prometheus.Desc
	l0ToRecovery *prometheus.Desc
}

func (p *pcieErrorDescs) all() []*prometheus.Desc {
	if p == nil {
		return nil
	}

	return []*prometheus.Desc{p.errors, p.l0ToRecovery}
}

type c2cLinkDescs struct {
	links       *prometheus.Desc
	activeLinks *prometheus.Desc
}

func (c *c2cLinkDescs) all() []*prometheus.Desc {
	if c == nil {
		return nil
	}

	return []*prometheus.Desc{c.links, c.activeLinks}
}

type perfPolicyDescs struct {
	violation       *prometheus.Desc
	baseClocksTotal *prometheus.Desc
}

func (p *perfPolicyDescs) all() []*prometheus.Desc {
	if p == nil {
		return nil
	}

	return []*prometheus.Desc{p.violation, p.baseClocksTotal}
}

type tlimitThresholdDescs struct {
	shutdown  *prometheus.Desc
	slowdown  *prometheus.Desc
	memoryMax *prometheus.Desc
	gpuMax    *prometheus.Desc
}

func (t *tlimitThresholdDescs) all() []*prometheus.Desc {
	if t == nil {
		return nil
	}

	return []*prometheus.Desc{t.shutdown, t.slowdown, t.memoryMax, t.gpuMax}
}

type repairDescs struct {
	pending      *prometheus.Desc
	unrepairable *prometheus.Desc
}

func (r *repairDescs) all() []*prometheus.Desc {
	if r == nil {
		return nil
	}

	return []*prometheus.Desc{r.pending, r.unrepairable}
}

type pcieRuntimePMDescs struct {
	suspended *prometheus.Desc
	active    *prometheus.Desc
}

func (p *pcieRuntimePMDescs) all() []*prometheus.Desc {
	if p == nil {
		return nil
	}

	return []*prometheus.Desc{p.suspended, p.active}
}

func newNVLinkLinkDescs(prefix string, enabled bool) *nvlinkLinkDescs {
	if !enabled {
		return nil
	}

	return &nvlinkLinkDescs{
		links: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "nvlink_links"),
			"Number of NVLink links present on the GPU.",
			[]string{uuidLabel},
			nil),
		activeLinks: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "nvlink_active_links"),
			"Number of active NVLink links on the GPU.",
			[]string{uuidLabel},
			nil),
		recoveryEvents: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "nvlink_recovery_events_total"),
			"Total NVLink recovery events across all links on the GPU.",
			[]string{uuidLabel},
			nil),
		recoveryFailedEvents: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "nvlink_recovery_failed_events_total"),
			"Total failed NVLink recovery events across all links on the GPU.",
			[]string{uuidLabel},
			nil),
	}
}

func newPCIeErrorDescs(prefix string, enabled bool) *pcieErrorDescs {
	if !enabled {
		return nil
	}

	return &pcieErrorDescs{
		errors: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "pcie_errors_total"),
			"Total PCIe AER errors on the GPU by severity type (correctable, non_fatal, fatal).",
			[]string{uuidLabel, "type"},
			nil),
		l0ToRecovery: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "pcie_l0_to_recovery_total"),
			"Total PCIe L0-to-recovery link retraining transitions on the GPU.",
			[]string{uuidLabel},
			nil),
	}
}

func newC2CLinkDescs(prefix string, enabled bool) *c2cLinkDescs {
	if !enabled {
		return nil
	}

	return &c2cLinkDescs{
		links: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "c2c_links"),
			"Number of Chip-to-Chip (C2C) links present on the GPU.",
			[]string{uuidLabel},
			nil),
		activeLinks: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "c2c_active_links"),
			"Number of active Chip-to-Chip (C2C) links on the GPU.",
			[]string{uuidLabel},
			nil),
	}
}

func newPerfPolicyDescs(prefix string, enabled bool) *perfPolicyDescs {
	if !enabled {
		return nil
	}

	return &perfPolicyDescs{
		violation: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "perf_policy_violation_seconds_total"),
			"Cumulative time in seconds the GPU was throttled by a performance policy "+
				"(thermal, board_limit, reliability).",
			[]string{uuidLabel, "policy"},
			nil),
		baseClocksTotal: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "perf_policy_base_clocks_violation_seconds_total"),
			"Cumulative time in seconds the GPU ran below its base clock due to performance limiters.",
			[]string{uuidLabel},
			nil),
	}
}

func newTLimitThresholdDescs(prefix string, enabled bool) *tlimitThresholdDescs {
	if !enabled {
		return nil
	}

	desc := func(kind, help string) *prometheus.Desc {
		return prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "temperature_tlimit_threshold_"+kind+"_celsius"),
			help,
			[]string{uuidLabel},
			nil)
	}

	return &tlimitThresholdDescs{
		shutdown: desc(
			"shutdown",
			"Signed T.Limit shutdown offset in degrees Celsius (compare against temperature_gpu_tlimit).",
		),
		slowdown: desc(
			"slowdown",
			"Signed T.Limit hardware slowdown offset in degrees Celsius (compare against temperature_gpu_tlimit).",
		),
		memoryMax: desc(
			"memory_max",
			"Signed T.Limit maximum operating memory offset in degrees Celsius (compare against temperature_gpu_tlimit).",
		),
		gpuMax: desc(
			"gpu_max",
			"Signed T.Limit maximum operating GPU offset in degrees Celsius (compare against temperature_gpu_tlimit).",
		),
	}
}

func newRepairDescs(prefix string, enabled bool) *repairDescs {
	if !enabled {
		return nil
	}

	return &repairDescs{
		pending: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "repair_pending"),
			"Whether a hardware repair is pending on the GPU (1 = pending, 0 = none), labeled by type (channel, tpc).",
			[]string{uuidLabel, "type"},
			nil),
		unrepairable: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "unrepairable_memory"),
			"Whether the GPU has unrepairable memory errors (1 = unrepairable, 0 = healthy).",
			[]string{uuidLabel},
			nil),
	}
}

func newVirtualizationModeDesc(prefix string, enabled bool) *prometheus.Desc {
	if !enabled {
		return nil
	}

	return prometheus.NewDesc(
		prometheus.BuildFQName(prefix, "", "virtualization_mode"),
		"Virtualization mode of the GPU (0 = none, 1 = passthrough, 2 = vGPU, 3 = host vGPU, 4 = host vSGA).",
		[]string{uuidLabel},
		nil)
}

func newPowerMizerModeDesc(prefix string, enabled bool) *prometheus.Desc {
	if !enabled {
		return nil
	}

	return prometheus.NewDesc(
		prometheus.BuildFQName(prefix, "", "power_mizer_mode"),
		"Active PowerMizer mode of the GPU (0 = adaptive, 1 = prefer max performance, 2 = auto, 3 = consistent performance).",
		[]string{uuidLabel},
		nil,
	)
}

func newPCIeRuntimePMDescs(prefix string, enabled bool) *pcieRuntimePMDescs {
	if !enabled {
		return nil
	}

	return &pcieRuntimePMDescs{
		suspended: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "pcie_runtime_suspended_seconds_total"),
			"Cumulative time in seconds the GPU PCIe device has spent runtime-suspended (Linux sysfs).",
			[]string{uuidLabel},
			nil),
		active: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "pcie_runtime_active_seconds_total"),
			"Cumulative time in seconds the GPU PCIe device has spent runtime-active (Linux sysfs).",
			[]string{uuidLabel},
			nil),
	}
}

func (e *GPUExporter) renderNVLinkExtras(metricCh chan<- prometheus.Metric, samples []collect.NVLinkThroughput) {
	if e.nvlinkTxDesc == nil {
		return
	}

	for _, sample := range samples {
		if sample.TXBytes != nil {
			e.sendConstWithUUID(
				metricCh,
				e.nvlinkTxDesc,
				prometheus.CounterValue,
				float64(*sample.TXBytes),
				sample.UUID,
			)
		}

		if sample.RXBytes != nil {
			e.sendConstWithUUID(
				metricCh,
				e.nvlinkRxDesc,
				prometheus.CounterValue,
				float64(*sample.RXBytes),
				sample.UUID,
			)
		}

		e.renderNVLinkLinkSample(metricCh, sample)
	}
}

func (e *GPUExporter) renderNVLinkLinkSample(metricCh chan<- prometheus.Metric, sample collect.NVLinkThroughput) {
	if e.nvlinkLinkDescs == nil {
		return
	}

	if sample.Links != nil {
		e.sendConstWithUUID(
			metricCh,
			e.nvlinkLinkDescs.links,
			prometheus.GaugeValue,
			float64(*sample.Links),
			sample.UUID,
		)
	}

	if sample.ActiveLinks != nil {
		e.sendConstWithUUID(metricCh, e.nvlinkLinkDescs.activeLinks, prometheus.GaugeValue,
			float64(*sample.ActiveLinks), sample.UUID)
	}

	if sample.RecoveryEvents != nil {
		e.sendConstWithUUID(metricCh, e.nvlinkLinkDescs.recoveryEvents, prometheus.CounterValue,
			float64(*sample.RecoveryEvents), sample.UUID)
	}

	if sample.RecoveryFailedEvents != nil {
		e.sendConstWithUUID(metricCh, e.nvlinkLinkDescs.recoveryFailedEvents, prometheus.CounterValue,
			float64(*sample.RecoveryFailedEvents), sample.UUID)
	}
}

func (e *GPUExporter) renderFanExtras(metricCh chan<- prometheus.Metric, fans []collect.FanReading) {
	if e.fanDescs == nil {
		return
	}

	for _, fan := range fans {
		if fan.CurrentRatio != nil {
			e.sendLabeledGauge(metricCh, e.fanDescs.currentRatio, *fan.CurrentRatio, fan.UUID, fan.Fan)
		}

		if fan.TargetRatio != nil {
			e.sendLabeledGauge(metricCh, e.fanDescs.targetRatio, *fan.TargetRatio, fan.UUID, fan.Fan)
		}

		if fan.RPM != nil {
			e.sendLabeledGauge(metricCh, e.fanDescs.rpm, float64(*fan.RPM), fan.UUID, fan.Fan)
		}

		if fan.ControlPolicy != nil {
			e.sendLabeledGauge(metricCh, e.fanDescs.controlPolicy, float64(*fan.ControlPolicy), fan.UUID, fan.Fan)
		}
	}
}

func (e *GPUExporter) renderExtendedExtras(metricCh chan<- prometheus.Metric, extras collect.Extras) {
	e.renderPCIeErrorExtras(metricCh, extras.PCIeErrors)
	e.renderC2CLinkExtras(metricCh, extras.C2CLinks)
	e.renderPerfPolicyExtras(metricCh, extras.PerfPolicyViolations)
	e.renderTLimitExtras(metricCh, extras.TemperatureTLimitThresholds)
	e.renderRepairExtras(metricCh, extras.RepairStatuses)
	e.renderModeAndPMExtras(metricCh, extras)
}

func (e *GPUExporter) renderPCIeErrorExtras(metricCh chan<- prometheus.Metric, samples []collect.PCIeErrorCounter) {
	if e.pcieErrorDescs == nil {
		return
	}

	for _, sample := range samples {
		if sample.Correctable != nil {
			e.sendLabeledCounter(
				metricCh,
				e.pcieErrorDescs.errors,
				float64(*sample.Correctable),
				sample.UUID,
				"correctable",
			)
		}

		if sample.NonFatal != nil {
			e.sendLabeledCounter(metricCh, e.pcieErrorDescs.errors, float64(*sample.NonFatal), sample.UUID, "non_fatal")
		}

		if sample.Fatal != nil {
			e.sendLabeledCounter(metricCh, e.pcieErrorDescs.errors, float64(*sample.Fatal), sample.UUID, "fatal")
		}

		if sample.L0ToRecovery != nil {
			e.sendConstWithUUID(metricCh, e.pcieErrorDescs.l0ToRecovery, prometheus.CounterValue,
				float64(*sample.L0ToRecovery), sample.UUID)
		}
	}
}

func (e *GPUExporter) renderC2CLinkExtras(metricCh chan<- prometheus.Metric, links []collect.C2CLinkStatus) {
	if e.c2cLinkDescs == nil {
		return
	}

	for _, link := range links {
		e.sendConstWithUUID(metricCh, e.c2cLinkDescs.links, prometheus.GaugeValue, float64(link.Links), link.UUID)

		if link.ActiveLinks != nil {
			e.sendConstWithUUID(metricCh, e.c2cLinkDescs.activeLinks, prometheus.GaugeValue,
				float64(*link.ActiveLinks), link.UUID)
		}
	}
}

func (e *GPUExporter) renderPerfPolicyExtras(
	metricCh chan<- prometheus.Metric,
	policies []collect.PerfPolicyViolation,
) {
	if e.perfPolicyDescs == nil {
		return
	}

	for _, pol := range policies {
		if pol.Thermal != nil {
			e.sendLabeledCounter(metricCh, e.perfPolicyDescs.violation, *pol.Thermal, pol.UUID, "thermal")
		}

		if pol.BoardLimit != nil {
			e.sendLabeledCounter(metricCh, e.perfPolicyDescs.violation, *pol.BoardLimit, pol.UUID, "board_limit")
		}

		if pol.Reliability != nil {
			e.sendLabeledCounter(metricCh, e.perfPolicyDescs.violation, *pol.Reliability, pol.UUID, "reliability")
		}

		if pol.TotalBaseClocks != nil {
			e.sendConstWithUUID(metricCh, e.perfPolicyDescs.baseClocksTotal, prometheus.CounterValue,
				*pol.TotalBaseClocks, pol.UUID)
		}
	}
}

func (e *GPUExporter) renderTLimitExtras(
	metricCh chan<- prometheus.Metric,
	thresholds []collect.TemperatureTLimitThresholds,
) {
	if e.tlimitThresholdDescs == nil {
		return
	}

	for _, thresh := range thresholds {
		for _, entry := range []struct {
			desc  *prometheus.Desc
			value *float64
		}{
			{e.tlimitThresholdDescs.shutdown, thresh.Shutdown},
			{e.tlimitThresholdDescs.slowdown, thresh.Slowdown},
			{e.tlimitThresholdDescs.memoryMax, thresh.MemoryMax},
			{e.tlimitThresholdDescs.gpuMax, thresh.GPUMax},
		} {
			if entry.value != nil {
				e.sendConstWithUUID(metricCh, entry.desc, prometheus.GaugeValue, *entry.value, thresh.UUID)
			}
		}
	}
}

func (e *GPUExporter) renderRepairExtras(metricCh chan<- prometheus.Metric, statuses []collect.RepairStatus) {
	if e.repairDescs == nil {
		return
	}

	for _, rep := range statuses {
		if rep.ChannelPending != nil {
			e.sendLabeledGauge(metricCh, e.repairDescs.pending, boolToFloat64(*rep.ChannelPending), rep.UUID, "channel")
		}

		if rep.TPCPending != nil {
			e.sendLabeledGauge(metricCh, e.repairDescs.pending, boolToFloat64(*rep.TPCPending), rep.UUID, "tpc")
		}

		if rep.Unrepairable != nil {
			e.sendConstWithUUID(metricCh, e.repairDescs.unrepairable, prometheus.GaugeValue,
				boolToFloat64(*rep.Unrepairable), rep.UUID)
		}
	}
}

func (e *GPUExporter) renderModeAndPMExtras(metricCh chan<- prometheus.Metric, extras collect.Extras) {
	if e.virtModeDesc != nil {
		for _, vm := range extras.VirtualizationModes {
			e.sendConstWithUUID(metricCh, e.virtModeDesc, prometheus.GaugeValue, float64(vm.Mode), vm.UUID)
		}
	}

	if e.powerMizerDesc != nil {
		for _, pm := range extras.PowerMizerModes {
			e.sendConstWithUUID(metricCh, e.powerMizerDesc, prometheus.GaugeValue, float64(pm.Mode), pm.UUID)
		}
	}

	if e.pcieRuntimePMDescs != nil {
		for _, rpm := range extras.PCIeRuntimePM {
			if rpm.SuspendedSeconds != nil {
				e.sendConstWithUUID(metricCh, e.pcieRuntimePMDescs.suspended, prometheus.CounterValue,
					*rpm.SuspendedSeconds, rpm.UUID)
			}

			if rpm.ActiveSeconds != nil {
				e.sendConstWithUUID(metricCh, e.pcieRuntimePMDescs.active, prometheus.CounterValue,
					*rpm.ActiveSeconds, rpm.UUID)
			}
		}
	}
}

func boolToFloat64(v bool) float64 {
	if v {
		return 1
	}

	return 0
}
