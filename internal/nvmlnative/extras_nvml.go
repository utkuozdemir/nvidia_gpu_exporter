//go:build (linux && cgo) || windows

package nvmlnative

import (
	"strconv"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvmlnative/nvml"
)

// collectFans appends per-fan speed ratio, target speed ratio and RPM readings.
// Passively cooled GPUs return NOT_SUPPORTED or zero fans and are skipped silently.
func (b *Backend) collectFans(dev device, uuid string, extras *collect.Extras) bool {
	numFans, ret := dev.GetNumFans()

	switch {
	case ret == nvml.ERROR_NOT_SUPPORTED || ret == nvml.ERROR_FUNCTION_NOT_FOUND:
		return true
	case ret != nvml.SUCCESS:
		return b.extrasFailure("fans", "cannot count the GPU fans", ret)
	}

	avail := b.avail.Load()

	for fan := range numFans {
		reading, ok := b.collectOneFan(dev, avail, uuid, fan)
		if !ok {
			return false
		}

		if reading != nil {
			extras.Fans = append(extras.Fans, *reading)
		}
	}

	return true
}

//nolint:cyclop // four optional getters per fan with lifecycle guards
func (b *Backend) collectOneFan(dev device, avail *availability, uuid string, fan int) (*collect.FanReading, bool) {
	var (
		currentRatio  *float64
		targetRatio   *float64
		rpm           *uint32
		controlPolicy *uint32
	)

	if speed, sret := dev.GetFanSpeed_v2(fan); sret == nvml.SUCCESS {
		currentRatio = new(float64(speed) / 100.0)
	} else if isLifecycleError(sret) {
		return nil, b.extrasFailure("fans", "cannot read a GPU fan speed", sret)
	}

	if target, tret := dev.GetTargetFanSpeed(fan); tret == nvml.SUCCESS {
		targetRatio = new(float64(target) / 100.0)
	} else if isLifecycleError(tret) {
		return nil, b.extrasFailure("fans", "cannot read a GPU target fan speed", tret)
	}

	if avail.has("nvmlDeviceGetFanSpeedRPM") {
		if fanRPM, rret := b.api.deviceGetFanSpeedRPM(dev.raw(), fan); rret == nvml.SUCCESS {
			rpm = &fanRPM
		} else if isLifecycleError(rret) {
			return nil, b.extrasFailure("fans", "cannot read a GPU fan RPM", rret)
		}
	}

	if policy, pret := dev.GetFanControlPolicy_v2(fan); pret == nvml.SUCCESS {
		if code := uint32(policy); code <= maxKnownFanControlPolicy {
			controlPolicy = &code
		}
	} else if isLifecycleError(pret) {
		return nil, b.extrasFailure("fans", "cannot read a GPU fan control policy", pret)
	}

	if currentRatio == nil && targetRatio == nil && rpm == nil && controlPolicy == nil {
		return nil, true
	}

	return &collect.FanReading{
		UUID:          uuid,
		Fan:           strconv.Itoa(fan),
		CurrentRatio:  currentRatio,
		TargetRatio:   targetRatio,
		RPM:           rpm,
		ControlPolicy: controlPolicy,
	}, true
}

// collectBAR1 appends one device's BAR1 memory usage in bytes.
func (b *Backend) collectBAR1(dev device, uuid string, extras *collect.Extras) bool {
	mem, ret := dev.GetBAR1MemoryInfo()
	if ret == nvml.SUCCESS {
		extras.BAR1 = append(extras.BAR1, collect.BAR1Memory{
			UUID:  uuid,
			Total: mem.Bar1Total,
			Used:  mem.Bar1Used,
			Free:  mem.Bar1Free,
		})

		return true
	}

	if ret == nvml.ERROR_NOT_SUPPORTED || ret == nvml.ERROR_FUNCTION_NOT_FOUND {
		return true
	}

	return b.extrasFailure("bar1", "cannot read the GPU BAR1 memory", ret)
}

// collectTemperatureThresholds appends one device's thermal thresholds in Celsius.
func (b *Backend) collectTemperatureThresholds(dev device, uuid string, extras *collect.Extras) bool {
	var (
		out      = collect.TemperatureThresholds{UUID: uuid}
		anyValue bool
	)

	targets := []struct {
		kind nvml.TemperatureThresholds
		dest **uint32
	}{
		{nvml.TEMPERATURE_THRESHOLD_SHUTDOWN, &out.Shutdown},
		{nvml.TEMPERATURE_THRESHOLD_SLOWDOWN, &out.Slowdown},
		{nvml.TEMPERATURE_THRESHOLD_MEM_MAX, &out.MemoryMax},
		{nvml.TEMPERATURE_THRESHOLD_GPU_MAX, &out.GPUMax},
		{nvml.TEMPERATURE_THRESHOLD_ACOUSTIC_CURR, &out.Acoustic},
	}

	for _, target := range targets {
		val, ret := dev.GetTemperatureThreshold(target.kind)
		if ret == nvml.SUCCESS {
			*target.dest = &val
			anyValue = true
		} else if isLifecycleError(ret) {
			return b.extrasFailure("temperature-thresholds", "cannot read a GPU temperature threshold", ret)
		}
	}

	if anyValue {
		extras.TemperatureThresholds = append(extras.TemperatureThresholds, out)
	}

	return true
}

const (
	maxKnownFanControlPolicy   = 1
	maxKnownPowerSource        = 2
	maxKnownPowerMizerMode     = 3
	maxKnownVirtualizationMode = 4
)

// collectPowerSource appends one device's power source reading.
func (b *Backend) collectPowerSource(dev device, uuid string, extras *collect.Extras) bool {
	src, ret := dev.GetPowerSource()
	if ret == nvml.SUCCESS {
		code := uint32(src)
		if code > maxKnownPowerSource {
			b.logger.Warn("unrecognized GPU power source value, skipping", "uuid", uuid, "power_source", code)

			return true
		}

		extras.PowerSources = append(extras.PowerSources, collect.PowerSource{
			UUID:   uuid,
			Source: code,
		})

		return true
	}

	if ret == nvml.ERROR_NOT_SUPPORTED || ret == nvml.ERROR_FUNCTION_NOT_FOUND {
		return true
	}

	return b.extrasFailure("power-source", "cannot read the GPU power source", ret)
}

// collectRepairStatus appends one device's repair-pending and unrepairable
// memory flags.
func (b *Backend) collectRepairStatus(dev device, uuid string, extras *collect.Extras) bool {
	var (
		out      = collect.RepairStatus{UUID: uuid}
		anyValue bool
	)

	rep, rret := dev.GetRepairStatus()
	if rret == nvml.SUCCESS {
		out.ChannelPending = new(rep.BChannelRepairPending != 0)
		out.TPCPending = new(rep.BTpcRepairPending != 0)
		anyValue = true
	} else if isLifecycleError(rret) {
		return b.extrasFailure("repair-status", "cannot read the GPU repair status", rret)
	}

	unrep, uret := dev.GetUnrepairableMemoryFlag_v1()
	if uret == nvml.SUCCESS {
		out.Unrepairable = new(unrep.BUnrepairableMemory != 0)
		anyValue = true
	} else if isLifecycleError(uret) {
		return b.extrasFailure("repair-status", "cannot read the GPU unrepairable memory flag", uret)
	}

	if anyValue {
		extras.RepairStatuses = append(extras.RepairStatuses, out)
	}

	return true
}

// collectVirtualizationMode appends one device's virtualization mode enum.
func (b *Backend) collectVirtualizationMode(dev device, uuid string, extras *collect.Extras) bool {
	mode, ret := dev.GetVirtualizationMode()
	if ret == nvml.SUCCESS {
		if mode < 0 || uint32(mode) > maxKnownVirtualizationMode {
			b.logger.Warn("unrecognized GPU virtualization mode, skipping", "uuid", uuid, "virtualization_mode", mode)

			return true
		}

		extras.VirtualizationModes = append(extras.VirtualizationModes, collect.VirtualizationMode{
			UUID: uuid,
			Mode: uint32(mode),
		})

		return true
	}

	if ret == nvml.ERROR_NOT_SUPPORTED || ret == nvml.ERROR_FUNCTION_NOT_FOUND {
		return true
	}

	return b.extrasFailure("virtualization-mode", "cannot read the GPU virtualization mode", ret)
}

// collectPowerMizerMode appends one device's active PowerMizer mode enum.
func (b *Backend) collectPowerMizerMode(dev device, uuid string, extras *collect.Extras) bool {
	modes, ret := dev.GetPowerMizerMode_v1()
	if ret == nvml.SUCCESS {
		if modes.CurrentMode > maxKnownPowerMizerMode {
			b.logger.Warn(
				"unrecognized GPU PowerMizer mode, skipping",
				"uuid",
				uuid,
				"power_mizer_mode",
				modes.CurrentMode,
			)

			return true
		}

		extras.PowerMizerModes = append(extras.PowerMizerModes, collect.PowerMizerMode{
			UUID: uuid,
			Mode: modes.CurrentMode,
		})

		return true
	}

	if ret == nvml.ERROR_NOT_SUPPORTED || ret == nvml.ERROR_FUNCTION_NOT_FOUND {
		return true
	}

	return b.extrasFailure("power-mizer", "cannot read the GPU PowerMizer mode", ret)
}
