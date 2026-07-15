package collect

// Extras carries backend-specific readings that are outside the nvidia-smi
// query-field schema. Every family follows the Apps contract: it fails
// softly, so a family the backend cannot serve is nil/empty and never fails
// the collection. The zero value means "no extras". Backends must build
// fresh slices on every cycle: snapshots are shallow-copied to concurrent
// scrapers, so a retained slice must never be mutated in place.
type Extras struct {
	// CUDAVersion is the CUDA version the installed driver supports (for
	// example "13.1"), empty when unknown. Both backends fill it; the
	// exporter renders it as the cuda_version label on gpu_info.
	CUDAVersion string
	// PCIe holds per-GPU PCIe throughput samples. Only the nvml backend
	// fills it, and only under --collect.pcie-throughput.
	PCIe []PCIeThroughput
	// Energy holds per-GPU cumulative energy counters. Only the nvml
	// backend fills it; devices that cannot report it are absent.
	Energy []EnergyCounter
}

// PCIeThroughput is one GPU's sampled PCIe throughput.
type PCIeThroughput struct {
	// UUID is the GPU uuid, normalized like every uuid label.
	UUID string
	// TXBytesPerSecond and RXBytesPerSecond are sampled by the driver over
	// two separate, consecutive 20ms windows, not one simultaneous pair.
	TXBytesPerSecond float64
	RXBytesPerSecond float64
}

// EnergyCounter is one GPU's cumulative energy consumption.
type EnergyCounter struct {
	// UUID is the GPU uuid, normalized like every uuid label.
	UUID string
	// Joules counts since the driver was last loaded. It resets when the
	// driver reloads or the GPU is reset, both outside this process.
	Joules float64
}
