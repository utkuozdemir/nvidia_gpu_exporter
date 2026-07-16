package collect

import "time"

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
	// PCIe holds per-GPU PCIe throughput samples. The nvml backend fills it
	// under --collect.pcie-throughput; the demo backend always fills it.
	PCIe []PCIeThroughput
	// Energy holds per-GPU cumulative energy counters. The nvml and demo
	// backends fill it; devices that cannot report it are absent.
	Energy []EnergyCounter
	// MIG holds per-MIG-instance readings. The nvml backend fills it for
	// GPUs with MIG mode enabled; the demo backend synthesizes its
	// configured topology.
	MIG []MIGInstance
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

// XIDCounter is one (GPU, XID code) pair's cumulative error-event count.
// XID state deliberately does NOT ride Extras: it is owned by a long-lived
// watcher and read at scrape time, so the counters stay visible during the
// exact driver incidents that make collections fail.
type XIDCounter struct {
	// UUID is the GPU uuid, normalized like every uuid label, cached at
	// event registration time (the GPU may be unreadable once it errors).
	UUID string
	// XID is the numeric XID error code.
	XID uint64
	// Count is the number of events observed since the exporter started.
	Count uint64
	// LastSeen is when the most recent event was received by the exporter
	// (NVML events carry no timestamp of their own).
	LastSeen time.Time
}

// MIGInstance is one MIG device: a compute instance inside a GPU instance of
// a MIG-partitioned GPU.
type MIGInstance struct {
	// ParentUUID is the parent GPU's uuid, normalized like every uuid
	// label, so MIG series join with the per-GPU ones.
	ParentUUID string
	// UUID is the MIG device's own uuid, normalized the same way.
	UUID string
	// GPUInstanceID and ComputeInstanceID are the numeric MIG topology ids,
	// carried as strings because they are only ever labels.
	GPUInstanceID     string
	ComputeInstanceID string
	// Profile is the MIG profile name (for example "1g.10gb"), parsed from
	// the device name, or the full device name when the shape is unknown.
	Profile string
	// Memory holds the memory readings in bytes, nil when they could not be
	// read. The framebuffer belongs to the GPU instance: sibling compute
	// instances report identical values (verified live), so the exporter
	// renders memory once per GPU instance.
	Memory *MIGMemory
	// Utilization holds the GPU instance's activity readings, nil when the
	// GPU does not support them (pre-Hopper), on the first cycle a GPU
	// instance is seen, or when the reading failed. All compute instances
	// of one GPU instance share the same values.
	Utilization *MIGUtilization
}

// MIGMemory is one MIG device's memory, in bytes.
type MIGMemory struct {
	Total    uint64
	Used     uint64
	Free     uint64
	Reserved uint64
}

// MIGUtilization is one GPU instance's activity over the window between the
// two most recent collections. Each value is nil when that particular metric
// could not be read.
type MIGUtilization struct {
	// GraphicsActivityRatio, SMActivityRatio, SMOccupancyRatio and
	// TensorActivityRatio are fractions of the window, 0 to 1.
	GraphicsActivityRatio *float64
	SMActivityRatio       *float64
	SMOccupancyRatio      *float64
	TensorActivityRatio   *float64
	// PCIeTXBytesPerSecond and PCIeRXBytesPerSecond are the instance's PCIe
	// traffic over the window.
	PCIeTXBytesPerSecond *float64
	PCIeRXBytesPerSecond *float64
}
