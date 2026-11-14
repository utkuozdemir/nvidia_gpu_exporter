//go:build (linux && cgo) || windows

package nvmlnative

import (
	"strconv"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvmlnative/nvml"
)

// collectFans appends per-fan speed ratio, target speed ratio and RPM readings.
// Passively cooled GPUs return NOT_SUPPORTED or zero fans and are skipped silently.
//
//nolint:cyclop // three optional getters per fan with lifecycle guards
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
		var (
			currentRatio *float64
			targetRatio  *float64
			rpm          *uint32
		)

		speed, sret := dev.GetFanSpeed_v2(fan)
		if sret == nvml.SUCCESS {
			ratio := float64(speed) / 100.0
			currentRatio = &ratio
		} else if isLifecycleError(sret) {
			return b.extrasFailure("fans", "cannot read a GPU fan speed", sret)
		}

		target, tret := dev.GetTargetFanSpeed(fan)
		if tret == nvml.SUCCESS {
			ratio := float64(target) / 100.0
			targetRatio = &ratio
		} else if isLifecycleError(tret) {
			return b.extrasFailure("fans", "cannot read a GPU target fan speed", tret)
		}

		if avail.has("nvmlDeviceGetFanSpeedRPM") {
			fanRPM, rret := b.api.deviceGetFanSpeedRPM(dev.raw(), fan)
			if rret == nvml.SUCCESS {
				rpm = &fanRPM
			} else if isLifecycleError(rret) {
				return b.extrasFailure("fans", "cannot read a GPU fan RPM", rret)
			}
		}

		if currentRatio != nil || targetRatio != nil || rpm != nil {
			extras.Fans = append(extras.Fans, collect.FanReading{
				UUID:         uuid,
				Fan:          strconv.Itoa(fan),
				CurrentRatio: currentRatio,
				TargetRatio:  targetRatio,
				RPM:          rpm,
			})
		}
	}

	return true
}

// collectBAR1 appends one device's BAR1 memory usage in bytes.
func (b *Backend) collectBAR1(dev device, uuid string, extras *collect.Extras) bool {
	mem, ret := dev.GetBAR1MemoryInfo()

	switch {
	case ret == nvml.SUCCESS:
		extras.BAR1 = append(extras.BAR1, collect.BAR1Memory{
			UUID:  uuid,
			Total: mem.Bar1Total,
			Used:  mem.Bar1Used,
			Free:  mem.Bar1Free,
		})
	case ret == nvml.ERROR_NOT_SUPPORTED || ret == nvml.ERROR_FUNCTION_NOT_FOUND:
	default:
		return b.extrasFailure("bar1", "cannot read the GPU BAR1 memory", ret)
	}

	return true
}

// collectTemperatureThresholds appends one device's thermal thresholds in Celsius.
func (b *Backend) collectTemperatureThresholds(dev device, uuid string, extras *collect.Extras) bool {
	read := func(kind nvml.TemperatureThresholds) (*uint32, bool) {
		val, ret := dev.GetTemperatureThreshold(kind)

		switch {
		case ret == nvml.SUCCESS:
			return &val, true
		case isLifecycleError(ret):
			return nil, b.extrasFailure("temperature-thresholds", "cannot read a GPU temperature threshold", ret)
		default:
			return nil, true
		}
	}

	shutdown, ok := read(nvml.TEMPERATURE_THRESHOLD_SHUTDOWN)
	if !ok {
		return false
	}

	slowdown, ok := read(nvml.TEMPERATURE_THRESHOLD_SLOWDOWN)
	if !ok {
		return false
	}

	memMax, ok := read(nvml.TEMPERATURE_THRESHOLD_MEM_MAX)
	if !ok {
		return false
	}

	gpuMax, ok := read(nvml.TEMPERATURE_THRESHOLD_GPU_MAX)
	if !ok {
		return false
	}

	acoustic, ok := read(nvml.TEMPERATURE_THRESHOLD_ACOUSTIC_CURR)
	if !ok {
		return false
	}

	if shutdown != nil || slowdown != nil || memMax != nil || gpuMax != nil || acoustic != nil {
		extras.TemperatureThresholds = append(extras.TemperatureThresholds, collect.TemperatureThresholds{
			UUID:      uuid,
			Shutdown:  shutdown,
			Slowdown:  slowdown,
			MemoryMax: memMax,
			GPUMax:    gpuMax,
			Acoustic:  acoustic,
		})
	}

	return true
}

// collectPowerSource appends one device's power source reading.
func (b *Backend) collectPowerSource(dev device, uuid string, extras *collect.Extras) bool {
	src, ret := dev.GetPowerSource()

	switch {
	case ret == nvml.SUCCESS:
		extras.PowerSources = append(extras.PowerSources, collect.PowerSource{
			UUID:   uuid,
			Source: uint32(src),
		})
	case ret == nvml.ERROR_NOT_SUPPORTED || ret == nvml.ERROR_FUNCTION_NOT_FOUND:
	default:
		return b.extrasFailure("power-source", "cannot read the GPU power source", ret)
	}

	return true
}
