//go:build linux && cgo

package nvmlnative

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvmlnative/nvml"
)

//nolint:gochecknoglobals // overridable in tests
var sysfsPCIDevicesDir = "/sys/bus/pci/devices"

const msPerSecond = 1000.0

// collectPCIeRuntimePM appends one device's Linux sysfs PCIe runtime PM
// residency counters. The PCI bus ID is cached per UUID for the NVML
// generation so sysfs reads add zero NVML ioctls on steady-state scrapes.
func (b *Backend) collectPCIeRuntimePM(dev device, uuid string, extras *collect.Extras) bool {
	busID, cached := b.pciBusIDCache[uuid]
	if !cached {
		info, ret := dev.GetPciInfo()
		if ret != nvml.SUCCESS {
			if isLifecycleError(ret) {
				return b.extrasFailure("pcie-runtime-pm", "cannot read PCI info for runtime PM", ret)
			}

			return true
		}

		busID = i8str(info.BusId[:])

		if b.pciBusIDCache == nil {
			b.pciBusIDCache = map[string]string{}
		}

		b.pciBusIDCache[uuid] = busID
	}

	if reading, ok := readPCIeRuntimePM(uuid, busID); ok {
		extras.PCIeRuntimePM = append(extras.PCIeRuntimePM, reading)
	}

	return true
}

// readPCIeRuntimePM reads cumulative runtime-suspended and runtime-active
// durations from Linux sysfs for the given NVML PCI bus ID. Missing or
// unreadable files are omitted silently.
func readPCIeRuntimePM(uuid, busID string) (collect.PCIeRuntimePM, bool) {
	norm := normalizeSysfsPCIBusID(busID)
	if norm == "" {
		return collect.PCIeRuntimePM{}, false
	}

	powerDir := filepath.Join(sysfsPCIDevicesDir, norm, "power")

	suspended := readSysfsMillisToSeconds(filepath.Join(powerDir, "runtime_suspended_time"))
	active := readSysfsMillisToSeconds(filepath.Join(powerDir, "runtime_active_time"))

	if suspended == nil && active == nil {
		return collect.PCIeRuntimePM{}, false
	}

	return collect.PCIeRuntimePM{
		UUID:             uuid,
		SuspendedSeconds: suspended,
		ActiveSeconds:    active,
	}, true
}

func normalizeSysfsPCIBusID(busID string) string {
	trimmed := strings.ToLower(strings.TrimSpace(busID))
	if trimmed == "" || strings.ContainsAny(trimmed, "/\\") || strings.Contains(trimmed, "..") {
		return ""
	}

	domain, rest, ok := strings.Cut(trimmed, ":")
	if !ok {
		return ""
	}

	if len(domain) == 8 && strings.HasPrefix(domain, "0000") {
		domain = domain[4:]
	}

	return domain + ":" + rest
}

func readSysfsMillisToSeconds(path string) *float64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	ms, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return nil
	}

	return new(float64(ms) / msPerSecond)
}
