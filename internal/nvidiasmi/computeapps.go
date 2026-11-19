package nvidiasmi

import "strings"

// UsedMemoryMultiplier converts the used_gpu_memory value (reported in MiB,
// per --help-query-compute-apps) to bytes.
const UsedMemoryMultiplier = 1024 * 1024

// ComputeApp represents one process holding a GPU context.
type ComputeApp struct {
	// GPUUUID is normalized the same way as the uuid label on all GPU metrics
	// (lowercase, "gpu-" prefix stripped), so per-process series join with
	// them. Under MIG this is the parent GPU's uuid: nvidia-smi does not
	// attribute processes to MIG instances in this output.
	GPUUUID string
	// PID is kept as a string: it is only ever a label, never a value.
	PID         string
	ProcessName string
	// UsedMemory is the raw reported value (e.g. "40960 MiB"). It stays raw
	// because it is not always a number: "[N/A]" on Windows WDDM,
	// "[Insufficient Permissions]" in restricted containers.
	UsedMemory string
	// GPUInstanceID and ComputeInstanceID attribute the process to a MIG
	// instance on MIG-partitioned GPUs, or to the configured MIG topology
	// in the demo backend, and stay empty otherwise.
	GPUInstanceID     string
	ComputeInstanceID string
	// ContainerID and SystemdUnit attribute the process to a container or a
	// systemd service when --collect.compute-apps-identity is enabled. They
	// are read from /proc/<pid>/cgroup on Linux, or synthesized by the demo
	// backend, and stay empty otherwise.
	ContainerID string
	SystemdUnit string
	// Utilization holds per-process engine utilization ratios (0.0 to 1.0)
	// from NVML, nvidia-smi pmon, or demo synthesis. It is nil when process
	// utilization is unavailable or unsupported (such as MIG).
	Utilization *ProcessUtilization
}

// ProcessUtilization holds the recent engine utilization ratios (0.0 to 1.0)
// for a single GPU process. Individual pointers stay nil when the backend
// cannot observe or does not report a given engine.
type ProcessUtilization struct {
	SMRatio      *float64
	MemoryRatio  *float64
	EncoderRatio *float64
	DecoderRatio *float64
	JPEGRatio    *float64
	OFARatio     *float64
}

// NormalizeUUID normalizes a GPU uuid the way all metric labels carry it:
// lowercase, without the "GPU-" prefix.
func NormalizeUUID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) >= 4 && strings.EqualFold(trimmed[:4], "gpu-") {
		trimmed = trimmed[4:]
	}

	return strings.ToLower(trimmed)
}

// IsKnownAbsentValue reports whether a raw value is one of the tokens
// nvidia-smi uses for "this field exists but cannot be read right now":
// "[N/A]" (e.g. per-process memory on Windows WDDM), "[Insufficient
// Permissions]" (restricted containers, especially under MIG),
// "[Not Supported]", or an empty value. These are expected states reported on
// every collection, so callers skip them without logging.
func IsKnownAbsentValue(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if !isASCII(trimmed) {
		trimmed = strings.ToLower(trimmed)
		if !isASCII(trimmed) {
			return false
		}
	}

	switch {
	case trimmed == "",
		strings.EqualFold(trimmed, "n/a"),
		strings.EqualFold(trimmed, "[n/a]"),
		strings.EqualFold(trimmed, "[not supported]"),
		strings.EqualFold(trimmed, "[insufficient permissions]"),
		strings.EqualFold(trimmed, "[unknown error]"):
		return true
	default:
		return false
	}
}
