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

const maxKnownPowerSource = 2

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
