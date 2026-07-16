package nvmlnative

// CollectOptions selects the optional per-cycle work beyond the resolved
// query fields. The extras it enables are outside the nvidia-smi query-field
// schema, so they are gated here at startup rather than through the
// per-cycle field plan.
type CollectOptions struct {
	// ComputeApps enables the per-process query.
	ComputeApps bool
	// PCIeThroughput enables per-GPU PCIe throughput sampling. Each reading
	// blocks inside the driver for a 20ms sampling window per direction, so
	// this is a deliberate opt-in (--collect.pcie-throughput).
	PCIeThroughput bool
	// Energy enables the per-GPU cumulative energy counter.
	Energy bool
	// MIG enables the per-MIG-instance readings. GPUs without MIG mode
	// enabled contribute nothing beyond one mode probe.
	MIG bool
}
