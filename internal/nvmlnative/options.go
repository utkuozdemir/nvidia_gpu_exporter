package nvmlnative

// CollectOptions selects the optional per-cycle work beyond the resolved
// query fields. The extras it enables are outside the nvidia-smi query-field
// schema, so they are gated here at startup rather than through the
// per-cycle field plan.
type CollectOptions struct {
	// ComputeApps enables the compute per-process query.
	ComputeApps bool
	// GraphicsApps enables the graphics per-process query.
	GraphicsApps bool
	// PCIeThroughput enables per-GPU PCIe throughput sampling. Each reading
	// blocks inside the driver for a 20ms sampling window per direction, so
	// this is a deliberate opt-in (--collect.pcie-throughput).
	PCIeThroughput bool
	// PCIeReplays enables the per-GPU PCIe replay and replay-rollover
	// counters.
	PCIeReplays bool
	// NVLink enables the per-GPU aggregate NVLink throughput counters.
	NVLink bool
	// GPM enables the whole-GPU GPM activity ratios on non-MIG GPUs that
	// support GPM.
	GPM bool
	// Energy enables the per-GPU cumulative energy counter.
	Energy bool
	// MIG enables the per-MIG-instance readings. GPUs without MIG mode
	// enabled contribute nothing beyond one mode probe.
	MIG bool
	// Fans enables per-fan current ratio, target ratio and RPM gauges.
	Fans bool
	// BAR1 enables per-GPU BAR1 memory usage gauges.
	BAR1 bool
	// TemperatureThresholds enables per-GPU temperature threshold gauges.
	TemperatureThresholds bool
	// PowerSource enables the per-GPU power source gauge.
	PowerSource bool
	// PCIeErrors enables per-GPU PCIe AER and L0-to-recovery counters.
	PCIeErrors bool
	// C2CLinks enables per-GPU C2C total and active link count gauges.
	C2CLinks bool
	// PerfPolicyViolations enables per-GPU performance-policy throttle
	// violation duration counters.
	PerfPolicyViolations bool
	// TemperatureTLimitThresholds enables per-GPU signed T.Limit thermal
	// thresholds (Ada and newer).
	TemperatureTLimitThresholds bool
	// RepairStatus enables per-GPU hardware repair-pending and unrepairable
	// memory status gauges.
	RepairStatus bool
	// VirtualizationMode enables the per-GPU virtualization mode gauge.
	VirtualizationMode bool
	// PowerMizerMode enables the per-GPU PowerMizer mode gauge.
	PowerMizerMode bool
	// PCIeRuntimePM enables per-GPU Linux sysfs PCIe runtime PM residency
	// counters.
	PCIeRuntimePM bool
}

//nolint:cyclop // flat disjunction over optional device extras
func (o CollectOptions) hasDeviceExtras() bool {
	return o.Energy || o.PCIeThroughput || o.PCIeReplays || o.PCIeErrors ||
		o.NVLink || o.C2CLinks || o.PerfPolicyViolations ||
		o.GPM || o.MIG || o.Fans || o.BAR1 ||
		o.TemperatureThresholds || o.TemperatureTLimitThresholds ||
		o.PowerSource || o.RepairStatus || o.VirtualizationMode ||
		o.PowerMizerMode || o.PCIeRuntimePM
}
