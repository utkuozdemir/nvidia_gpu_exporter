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
}

func (o CollectOptions) hasDeviceExtras() bool {
	return o.Energy || o.PCIeThroughput || o.PCIeReplays || o.NVLink ||
		o.GPM || o.MIG || o.Fans || o.BAR1 || o.TemperatureThresholds || o.PowerSource
}
