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
	// example "13.1"), empty when unknown. Both backends fill it, and the
	// exporter renders it as the cuda_version label on gpu_info.
	CUDAVersion string
	// PCIe holds per-GPU PCIe throughput samples. The nvml backend fills it
	// under --collect.pcie-throughput, and the demo backend always fills it.
	PCIe []PCIeThroughput
	// PCIeReplays holds per-GPU PCIe replay and replay-rollover counters.
	// The nvml and demo backends fill it when supported.
	PCIeReplays []PCIeReplayCounter
	// NVLink holds per-GPU aggregate NVLink throughput counters in bytes.
	// The nvml and demo backends fill it when active NVLink links exist.
	NVLink []NVLinkThroughput
	// GPM holds whole-GPU GPM activity readings over the inter-collection
	// window. The nvml and demo backends fill it on non-MIG GPUs that
	// support GPM.
	GPM []GPUUtilization
	// Energy holds per-GPU cumulative energy counters. The nvml and demo
	// backends fill it, and devices that cannot report it are absent.
	Energy []EnergyCounter
	// MIG holds per-MIG-instance readings. The nvml backend fills it for
	// GPUs with MIG mode enabled, and the demo backend synthesizes its
	// configured topology.
	MIG []MIGInstance
	// Fans holds per-fan current speed ratio, target speed ratio and RPM
	// readings on actively cooled GPUs.
	Fans []FanReading
	// BAR1 holds per-GPU BAR1 aperture memory usage in bytes.
	BAR1 []BAR1Memory
	// TemperatureThresholds holds per-GPU thermal thresholds in Celsius.
	TemperatureThresholds []TemperatureThresholds
	// TemperatureTLimitThresholds holds per-GPU signed T.Limit thermal
	// thresholds in Celsius (Ada and newer).
	TemperatureTLimitThresholds []TemperatureTLimitThresholds
	// PowerSources holds per-GPU power source states.
	PowerSources []PowerSource
	// PCIeErrors holds per-GPU PCIe AER and L0-to-recovery counters.
	PCIeErrors []PCIeErrorCounter
	// C2CLinks holds per-GPU C2C total and active link counts.
	C2CLinks []C2CLinkStatus
	// PerfPolicyViolations holds per-GPU cumulative performance-policy
	// violation durations in seconds.
	PerfPolicyViolations []PerfPolicyViolation
	// RepairStatuses holds per-GPU hardware repair-pending and unrepairable
	// memory status flags.
	RepairStatuses []RepairStatus
	// VirtualizationModes holds per-GPU virtualization mode enums.
	VirtualizationModes []VirtualizationMode
	// PowerMizerModes holds per-GPU PowerMizer mode enums.
	PowerMizerModes []PowerMizerMode
	// PCIeRuntimePM holds per-GPU Linux sysfs PCIe runtime PM residency
	// counters in seconds.
	PCIeRuntimePM []PCIeRuntimePM
}

// FanReading is one fan's speed and control policy readings on a GPU. Each
// value is nil when that getter is unsupported on the card or driver.
type FanReading struct {
	UUID          string
	Fan           string
	CurrentRatio  *float64
	TargetRatio   *float64
	RPM           *uint32
	ControlPolicy *uint32
}

// BAR1Memory is one GPU's BAR1 aperture memory usage in bytes.
type BAR1Memory struct {
	UUID  string
	Total uint64
	Used  uint64
	Free  uint64
}

// TemperatureThresholds holds one GPU's thermal thresholds in degrees Celsius.
// Unsupported thresholds on a given card or driver are nil.
type TemperatureThresholds struct {
	UUID      string
	Shutdown  *uint32
	Slowdown  *uint32
	MemoryMax *uint32
	GPUMax    *uint32
	Acoustic  *uint32
}

// PowerSource is one GPU's active power source (0 = AC, 1 = battery, 2 = undersized).
type PowerSource struct {
	UUID   string
	Source uint32
}

// TemperatureTLimitThresholds holds one GPU's signed T.Limit thermal
// thresholds in degrees Celsius (Ada and newer).
type TemperatureTLimitThresholds struct {
	UUID      string
	Shutdown  *float64
	Slowdown  *float64
	MemoryMax *float64
	GPUMax    *float64
}

// PCIeErrorCounter holds one GPU's cumulative PCIe AER and L0-to-recovery counts.
type PCIeErrorCounter struct {
	UUID         string
	Correctable  *uint64
	NonFatal     *uint64
	Fatal        *uint64
	L0ToRecovery *uint64
}

// C2CLinkStatus holds one GPU's total and active C2C link counts.
type C2CLinkStatus struct {
	UUID        string
	Links       uint64
	ActiveLinks *uint64
}

// PerfPolicyViolation holds one GPU's cumulative performance-policy violation
// durations in seconds.
type PerfPolicyViolation struct {
	UUID            string
	Thermal         *float64
	BoardLimit      *float64
	Reliability     *float64
	TotalBaseClocks *float64
}

// RepairStatus holds one GPU's hardware repair-pending and unrepairable memory
// status flags.
type RepairStatus struct {
	UUID           string
	ChannelPending *bool
	TPCPending     *bool
	Unrepairable   *bool
}

// VirtualizationMode holds one GPU's virtualization mode enum
// (0 = none, 1 = passthrough, 2 = vgpu, 3 = host_vgpu, 4 = host_vsga).
type VirtualizationMode struct {
	UUID string
	Mode uint32
}

// PowerMizerMode holds one GPU's active PowerMizer mode enum.
type PowerMizerMode struct {
	UUID string
	Mode uint32
}

// PCIeRuntimePM holds one GPU's Linux sysfs PCIe runtime PM residency counters
// in seconds.
type PCIeRuntimePM struct {
	UUID             string
	SuspendedSeconds *float64
	ActiveSeconds    *float64
}

// PCIeReplayCounter is one GPU's cumulative PCIe replay and replay-rollover
// counts since the last GPU reset or driver reload.
type PCIeReplayCounter struct {
	UUID      string
	Replays   *uint64
	Rollovers *uint64
}

// NVLinkThroughput is one GPU's cumulative NVLink data traffic in bytes,
// link counts, and NVLink 5 recovery event counters across all links.
type NVLinkThroughput struct {
	UUID                 string
	TXBytes              *uint64
	RXBytes              *uint64
	Links                *uint64
	ActiveLinks          *uint64
	RecoveryEvents       *uint64
	RecoveryFailedEvents *uint64
}

// GPUUtilization is one whole GPU's GPM activity over the window between the
// two most recent collections. Each value is nil when that particular metric
// could not be read.
type GPUUtilization struct {
	UUID                          string
	GraphicsActivityRatio         *float64
	SMActivityRatio               *float64
	SMOccupancyRatio              *float64
	TensorActivityRatio           *float64
	DRAMBandwidthUtilizationRatio *float64
	IntegerActivityRatio          *float64
	FP64ActivityRatio             *float64
	FP32ActivityRatio             *float64
	FP16ActivityRatio             *float64
	C2CTXBytesPerSecond           *float64
	C2CRXBytesPerSecond           *float64
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

// MIGXIDCounter is one (GPU, GPU instance, compute instance, XID code) tuple's
// cumulative error-event count for MIG-attributed XID events.
type MIGXIDCounter struct {
	UUID              string
	GPUInstanceID     string
	ComputeInstanceID string
	XID               uint64
	Count             uint64
	LastSeen          time.Time
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

// MIGMemory is one MIG device's memory, in bytes. Reserved is nil when the
// driver only supports the unversioned memory call (pre-510).
type MIGMemory struct {
	Total    uint64
	Used     uint64
	Free     uint64
	Reserved *uint64
}

// MIGUtilization is one GPU instance's activity over the window between the
// two most recent collections. Each value is nil when that particular metric
// could not be read.
type MIGUtilization struct {
	// GraphicsActivityRatio, SMActivityRatio, SMOccupancyRatio,
	// TensorActivityRatio and DRAMBandwidthUtilizationRatio are fractions
	// of the window, 0 to 1.
	GraphicsActivityRatio         *float64
	SMActivityRatio               *float64
	SMOccupancyRatio              *float64
	TensorActivityRatio           *float64
	DRAMBandwidthUtilizationRatio *float64
	IntegerActivityRatio          *float64
	FP64ActivityRatio             *float64
	FP32ActivityRatio             *float64
	FP16ActivityRatio             *float64
	// PCIeTXBytesPerSecond and PCIeRXBytesPerSecond are the instance's PCIe
	// traffic over the window.
	PCIeTXBytesPerSecond *float64
	PCIeRXBytesPerSecond *float64
}
