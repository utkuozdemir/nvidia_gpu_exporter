//go:build linux && cgo

package nvmlnative

import (
	"errors"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectFansMultiFan(t *testing.T) {
	t.Parallel()

	speeds := []uint32{38, 37}
	rpms := []uint32{1050, 1049}

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }
	dev.GetNumFansFunc = func() (int, nvml.Return) { return 2, nvml.SUCCESS }
	dev.GetFanSpeed_v2Func = func(fan int) (uint32, nvml.Return) {
		return speeds[fan], nvml.SUCCESS
	}
	dev.GetTargetFanSpeedFunc = func(int) (int, nvml.Return) {
		return 40, nvml.SUCCESS
	}

	var rpmFans []int

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	api := fake.api()
	api.deviceGetFanSpeedRPM = func(_ nvml.Device, fan int) (uint32, nvml.Return) {
		rpmFans = append(rpmFans, fan)

		return rpms[fan], nvml.SUCCESS
	}

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{Fans: true})(t.Context())
	require.NoError(t, err)
	require.Len(t, reading.Extras.Fans, 2)
	assert.Equal(t, []int{0, 1}, rpmFans)

	assert.Equal(t, "0", reading.Extras.Fans[0].Fan)
	require.NotNil(t, reading.Extras.Fans[0].CurrentRatio)
	assert.InDelta(t, 0.38, *reading.Extras.Fans[0].CurrentRatio, 1e-9)
	require.NotNil(t, reading.Extras.Fans[0].TargetRatio)
	assert.InDelta(t, 0.40, *reading.Extras.Fans[0].TargetRatio, 1e-9)
	require.NotNil(t, reading.Extras.Fans[0].RPM)
	assert.Equal(t, uint32(1050), *reading.Extras.Fans[0].RPM)

	assert.Equal(t, "1", reading.Extras.Fans[1].Fan)
	require.NotNil(t, reading.Extras.Fans[1].CurrentRatio)
	assert.InDelta(t, 0.37, *reading.Extras.Fans[1].CurrentRatio, 1e-9)
	require.NotNil(t, reading.Extras.Fans[1].RPM)
	assert.Equal(t, uint32(1049), *reading.Extras.Fans[1].RPM)
}

func TestCollectFansMissingRPMSymbol(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }
	dev.GetNumFansFunc = func() (int, nvml.Return) { return 1, nvml.SUCCESS }
	dev.GetFanSpeed_v2Func = func(int) (uint32, nvml.Return) { return 38, nvml.SUCCESS }
	dev.GetTargetFanSpeedFunc = func(int) (int, nvml.Return) { return 40, nvml.SUCCESS }

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	apiNoRPM := fake.api()
	apiNoRPM.lookupSymbol = func(name string) error {
		if name == "nvmlDeviceGetFanSpeedRPM" {
			return errors.New("symbol not found")
		}

		return nil
	}
	apiNoRPM.deviceGetFanSpeedRPM = func(nvml.Device, int) (uint32, nvml.Return) {
		t.Fatal("deviceGetFanSpeedRPM must not be called when its symbol is missing")

		return 0, nvml.ERROR_UNKNOWN
	}

	backendNoRPM, err := newWithAPI(apiNoRPM, slogt.New(t))
	require.NoError(t, err)

	readingNoRPM, _, err := backendNoRPM.QueryFunc(
		resolveFields(t, "power.draw"), CollectOptions{Fans: true},
	)(t.Context())
	require.NoError(t, err)
	require.Len(t, readingNoRPM.Extras.Fans, 1)
	assert.Nil(t, readingNoRPM.Extras.Fans[0].RPM)
	assert.NotNil(t, readingNoRPM.Extras.Fans[0].CurrentRatio)
}

func TestCollectFansPassiveGPU(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }
	dev.GetNumFansFunc = func() (int, nvml.Return) { return 0, nvml.ERROR_NOT_SUPPORTED }

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{Fans: true})(t.Context())
	require.NoError(t, err)
	assert.Empty(t, reading.Extras.Fans)
}

func TestCollectBAR1AndPowerSource(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }
	dev.GetBAR1MemoryInfoFunc = func() (nvml.BAR1Memory, nvml.Return) {
		return nvml.BAR1Memory{Bar1Total: 256 << 20, Bar1Used: 38 << 20, Bar1Free: 218 << 20}, nvml.SUCCESS
	}
	dev.GetPowerSourceFunc = func() (nvml.PowerSource, nvml.Return) {
		return nvml.PowerSource(0), nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	opts := CollectOptions{BAR1: true, PowerSource: true}

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), opts)(t.Context())
	require.NoError(t, err)

	require.Len(t, reading.Extras.BAR1, 1)
	assert.Equal(t, uint64(256<<20), reading.Extras.BAR1[0].Total)
	assert.Equal(t, uint64(38<<20), reading.Extras.BAR1[0].Used)
	assert.Equal(t, uint64(218<<20), reading.Extras.BAR1[0].Free)

	require.Len(t, reading.Extras.PowerSources, 1)
	assert.Equal(t, uint32(0), reading.Extras.PowerSources[0].Source)

	dev.GetPowerSourceFunc = func() (nvml.PowerSource, nvml.Return) {
		return nvml.PowerSource(99), nvml.SUCCESS
	}

	unrecognized, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), opts)(t.Context())
	require.NoError(t, err)
	assert.Empty(t, unrecognized.Extras.PowerSources)
}

func TestCollectTemperatureThresholds(t *testing.T) {
	t.Parallel()

	values := map[nvml.TemperatureThresholds]uint32{
		nvml.TEMPERATURE_THRESHOLD_SHUTDOWN:      100,
		nvml.TEMPERATURE_THRESHOLD_SLOWDOWN:      97,
		nvml.TEMPERATURE_THRESHOLD_GPU_MAX:       89,
		nvml.TEMPERATURE_THRESHOLD_ACOUSTIC_CURR: 84,
	}

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }
	dev.GetTemperatureThresholdFunc = func(kind nvml.TemperatureThresholds) (uint32, nvml.Return) {
		if val, ok := values[kind]; ok {
			return val, nvml.SUCCESS
		}

		return 0, nvml.ERROR_NOT_SUPPORTED
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	reading, _, err := backend.QueryFunc(
		resolveFields(t, "power.draw"), CollectOptions{TemperatureThresholds: true},
	)(t.Context())
	require.NoError(t, err)

	require.Len(t, reading.Extras.TemperatureThresholds, 1)
	thresh := reading.Extras.TemperatureThresholds[0]
	require.NotNil(t, thresh.Shutdown)
	assert.Equal(t, uint32(100), *thresh.Shutdown)
	require.NotNil(t, thresh.Slowdown)
	assert.Equal(t, uint32(97), *thresh.Slowdown)
	assert.Nil(t, thresh.MemoryMax)
	require.NotNil(t, thresh.GPUMax)
	assert.Equal(t, uint32(89), *thresh.GPUMax)
	require.NotNil(t, thresh.Acoustic)
	assert.Equal(t, uint32(84), *thresh.Acoustic)
}
