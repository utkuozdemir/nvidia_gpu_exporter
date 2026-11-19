//go:build linux && cgo

package nvmlnative

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func putUint64Field(fv *nvml.FieldValue, val uint64) {
	fv.NvmlReturn = uint32(nvml.SUCCESS)
	fv.ValueType = uint32(nvml.VALUE_TYPE_UNSIGNED_LONG_LONG)
	binary.LittleEndian.PutUint64(fv.Value[:8], val)
}

func putInt32Field(fv *nvml.FieldValue, val int32) {
	fv.NvmlReturn = uint32(nvml.SUCCESS)
	fv.ValueType = uint32(nvml.VALUE_TYPE_SIGNED_INT)
	binary.LittleEndian.PutUint32(fv.Value[:4], uint32(val)) //nolint:gosec // test helper
}

//nolint:cyclop,funlen // test fixture table for all consolidated field-value extras
func fillConsolidatedExtraFields(values []nvml.FieldValue) nvml.Return {
	for i := range values {
		fv := &values[i]

		switch {
		case fv.FieldId == nvml.FI_DEV_PCIE_COUNT_CORRECTABLE_ERRORS:
			putUint64Field(fv, 3)
		case fv.FieldId == nvml.FI_DEV_PCIE_COUNT_NON_FATAL_ERROR:
			putUint64Field(fv, 1)
		case fv.FieldId == nvml.FI_DEV_PCIE_COUNT_FATAL_ERROR:
			putUint64Field(fv, 0)
		case fv.FieldId == nvml.FI_DEV_PCIE_L0_TO_RECOVERY_COUNTER:
			putUint64Field(fv, 12)
		case fv.FieldId == nvml.FI_DEV_PERF_POLICY_THERMAL:
			putUint64Field(fv, 500_000_000)
		case fv.FieldId == nvml.FI_DEV_PERF_POLICY_BOARD_LIMIT:
			putUint64Field(fv, 2_000_000_000)
		case fv.FieldId == nvml.FI_DEV_PERF_POLICY_RELIABILITY:
			putUint64Field(fv, 0)
		case fv.FieldId == nvml.FI_DEV_PERF_POLICY_TOTAL_BASE_CLOCKS:
			putUint64Field(fv, 7_500_000_000)
		case fv.FieldId == nvml.FI_DEV_TEMPERATURE_SHUTDOWN_TLIMIT:
			putInt32Field(fv, -8)
		case fv.FieldId == nvml.FI_DEV_TEMPERATURE_SLOWDOWN_TLIMIT:
			putInt32Field(fv, -2)
		case fv.FieldId == nvml.FI_DEV_TEMPERATURE_MEM_MAX_TLIMIT:
			putInt32Field(fv, 0)
		case fv.FieldId == nvml.FI_DEV_TEMPERATURE_GPU_MAX_TLIMIT:
			putInt32Field(fv, 0)
		case fv.FieldId == nvml.FI_DEV_NVLINK_LINK_COUNT:
			putUint64Field(fv, 3)
		case fv.FieldId == nvml.FI_DEV_NVLINK_THROUGHPUT_DATA_TX && fv.ScopeId == math.MaxUint32:
			putUint64Field(fv, 10)
		case fv.FieldId == nvml.FI_DEV_NVLINK_THROUGHPUT_DATA_RX && fv.ScopeId == math.MaxUint32:
			putUint64Field(fv, 20)
		case fv.FieldId == nvml.FI_DEV_NVLINK_GET_STATE:
			// Link 0 = active (1), Link 1 = sleep (2), Link 2 = inactive (0).
			// Only Link 0 counts as active.
			switch fv.ScopeId {
			case 0:
				putUint64Field(fv, 1)
			case 1:
				putUint64Field(fv, 2)
			default:
				putUint64Field(fv, 0)
			}
		case fv.FieldId == nvml.FI_DEV_NVLINK_COUNT_LINK_RECOVERY_EVENTS && fv.ScopeId < 3:
			putUint64Field(fv, uint64(fv.ScopeId+1)) // 1 + 2 + 3 = 6
		case fv.FieldId == nvml.FI_DEV_NVLINK_COUNT_LINK_RECOVERY_FAILED_EVENTS && fv.ScopeId < 3:
			putUint64Field(fv, 1) // 1 + 1 + 1 = 3
		case fv.FieldId == nvml.FI_DEV_C2C_LINK_COUNT:
			putUint64Field(fv, 2)
		case fv.FieldId == nvml.FI_DEV_C2C_LINK_GET_STATUS:
			putUint64Field(fv, 1)
		default:
			fv.NvmlReturn = uint32(nvml.ERROR_NOT_SUPPORTED)
		}
	}

	return nvml.SUCCESS
}

func TestCollectFieldValueExtrasConsolidated(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 100000, nvml.SUCCESS }
	dev.GetFieldValuesFunc = fillConsolidatedExtraFields

	backend := newTestBackend(t, &fakeAPI{devices: []nvml.Device{dev}})
	opts := CollectOptions{
		PCIeErrors:                  true,
		NVLink:                      true,
		C2CLinks:                    true,
		PerfPolicyViolations:        true,
		TemperatureTLimitThresholds: true,
	}

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), opts)(t.Context())
	require.NoError(t, err)

	require.Len(t, reading.Extras.PCIeErrors, 1)
	assert.Equal(t, uint64(3), *reading.Extras.PCIeErrors[0].Correctable)
	assert.Equal(t, uint64(1), *reading.Extras.PCIeErrors[0].NonFatal)
	assert.Equal(t, uint64(0), *reading.Extras.PCIeErrors[0].Fatal)
	assert.Equal(t, uint64(12), *reading.Extras.PCIeErrors[0].L0ToRecovery)

	require.Len(t, reading.Extras.PerfPolicyViolations, 1)
	assert.InDelta(t, 0.5, *reading.Extras.PerfPolicyViolations[0].Thermal, 1e-9)
	assert.InDelta(t, 2.0, *reading.Extras.PerfPolicyViolations[0].BoardLimit, 1e-9)
	assert.InDelta(t, 7.5, *reading.Extras.PerfPolicyViolations[0].TotalBaseClocks, 1e-9)

	require.Len(t, reading.Extras.TemperatureTLimitThresholds, 1)
	assert.InDelta(t, -8.0, *reading.Extras.TemperatureTLimitThresholds[0].Shutdown, 1e-9)
	assert.InDelta(t, -2.0, *reading.Extras.TemperatureTLimitThresholds[0].Slowdown, 1e-9)

	require.Len(t, reading.Extras.NVLink, 1)
	assert.Equal(t, uint64(3), *reading.Extras.NVLink[0].Links)
	assert.Equal(t, uint64(1), *reading.Extras.NVLink[0].ActiveLinks)
	assert.Equal(t, uint64(6), *reading.Extras.NVLink[0].RecoveryEvents)
	assert.Equal(t, uint64(3), *reading.Extras.NVLink[0].RecoveryFailedEvents)

	require.Len(t, reading.Extras.C2CLinks, 1)
	assert.Equal(t, uint64(2), reading.Extras.C2CLinks[0].Links)
	require.NotNil(t, reading.Extras.C2CLinks[0].ActiveLinks)
	assert.Equal(t, uint64(2), *reading.Extras.C2CLinks[0].ActiveLinks)
}

func TestCollectNVLinkRecoveryPartialLinkOmitted(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 100000, nvml.SUCCESS }
	dev.GetFieldValuesFunc = func(values []nvml.FieldValue) nvml.Return {
		for i := range values {
			fv := &values[i]
			switch {
			case fv.FieldId == nvml.FI_DEV_NVLINK_LINK_COUNT:
				putUint64Field(fv, 2)
			case fv.FieldId == nvml.FI_DEV_NVLINK_COUNT_LINK_RECOVERY_EVENTS && fv.ScopeId < 2:
				putUint64Field(fv, 4)
			case fv.FieldId == nvml.FI_DEV_NVLINK_COUNT_LINK_RECOVERY_FAILED_EVENTS && fv.ScopeId == 0:
				// Only link 0 reports, while link 1 stays NOT_SUPPORTED so the failed total must be omitted.
				putUint64Field(fv, 1)
			default:
				fv.NvmlReturn = uint32(nvml.ERROR_NOT_SUPPORTED)
			}
		}

		return nvml.SUCCESS
	}

	backend := newTestBackend(t, &fakeAPI{devices: []nvml.Device{dev}})
	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{NVLink: true})(t.Context())
	require.NoError(t, err)
	require.Len(t, reading.Extras.NVLink, 1)
	require.NotNil(t, reading.Extras.NVLink[0].RecoveryEvents)
	assert.Equal(t, uint64(8), *reading.Extras.NVLink[0].RecoveryEvents)
	assert.Nil(t, reading.Extras.NVLink[0].RecoveryFailedEvents)
}

//nolint:funlen // exercises four related extras getters in one backend lifecycle
func TestCollectRepairVirtualizationPowerMizerAndFanPolicy(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 50000, nvml.SUCCESS }
	dev.GetNumFansFunc = func() (int, nvml.Return) { return 1, nvml.SUCCESS }
	dev.GetFanSpeed_v2Func = func(int) (uint32, nvml.Return) { return 0, nvml.ERROR_NOT_SUPPORTED }
	dev.GetTargetFanSpeedFunc = func(int) (int, nvml.Return) { return 0, nvml.ERROR_NOT_SUPPORTED }
	dev.GetFanControlPolicy_v2Func = func(int) (nvml.FanControlPolicy, nvml.Return) {
		return 1, nvml.SUCCESS
	}
	dev.GetRepairStatusFunc = func() (nvml.RepairStatus, nvml.Return) {
		return nvml.RepairStatus{BChannelRepairPending: 1, BTpcRepairPending: 0}, nvml.SUCCESS
	}
	dev.GetUnrepairableMemoryFlag_v1Func = func() (nvml.UnrepairableMemoryStatus_v1, nvml.Return) {
		return nvml.UnrepairableMemoryStatus_v1{BUnrepairableMemory: 1}, nvml.SUCCESS
	}
	dev.GetVirtualizationModeFunc = func() (nvml.GpuVirtualizationMode, nvml.Return) {
		return 1, nvml.SUCCESS
	}
	dev.GetPowerMizerMode_v1Func = func() (nvml.DevicePowerMizerModes_v1, nvml.Return) {
		return nvml.DevicePowerMizerModes_v1{CurrentMode: 1}, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	api := fake.api()
	api.deviceGetFanSpeedRPM = func(nvml.Device, int) (uint32, nvml.Return) {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	opts := CollectOptions{
		Fans:               true,
		RepairStatus:       true,
		VirtualizationMode: true,
		PowerMizerMode:     true,
	}
	query := backend.QueryFunc(resolveFields(t, "power.draw"), opts)

	reading, _, err := query(t.Context())
	require.NoError(t, err)

	require.Len(t, reading.Extras.Fans, 1)
	require.NotNil(t, reading.Extras.Fans[0].ControlPolicy)
	assert.Equal(t, uint32(1), *reading.Extras.Fans[0].ControlPolicy)

	require.Len(t, reading.Extras.RepairStatuses, 1)
	assert.True(t, *reading.Extras.RepairStatuses[0].ChannelPending)
	assert.False(t, *reading.Extras.RepairStatuses[0].TPCPending)
	assert.True(t, *reading.Extras.RepairStatuses[0].Unrepairable)

	require.Len(t, reading.Extras.VirtualizationModes, 1)
	assert.Equal(t, uint32(1), reading.Extras.VirtualizationModes[0].Mode)

	require.Len(t, reading.Extras.PowerMizerModes, 1)
	assert.Equal(t, uint32(1), reading.Extras.PowerMizerModes[0].Mode)
}

func TestPerLinkPartialFailureOmitsAggregates(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 50000, nvml.SUCCESS }
	dev.GetFieldValuesFunc = func(values []nvml.FieldValue) nvml.Return {
		for i := range values {
			fv := &values[i]

			switch {
			case fv.FieldId == nvml.FI_DEV_NVLINK_LINK_COUNT:
				putUint64Field(fv, 2)
			case fv.FieldId == nvml.FI_DEV_NVLINK_GET_STATE && fv.ScopeId == 0:
				putUint64Field(fv, 1)
			default:
				fv.NvmlReturn = uint32(nvml.ERROR_NO_DATA)
			}
		}

		return nvml.SUCCESS
	}

	backend := newTestBackend(t, &fakeAPI{devices: []nvml.Device{dev}})
	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{NVLink: true})(t.Context())
	require.NoError(t, err)
	require.Len(t, reading.Extras.NVLink, 1)
	assert.Equal(t, uint64(2), *reading.Extras.NVLink[0].Links)
	assert.Nil(t, reading.Extras.NVLink[0].ActiveLinks)
	assert.Nil(t, reading.Extras.NVLink[0].RecoveryEvents)
	assert.Nil(t, reading.Extras.NVLink[0].RecoveryFailedEvents)
}

//nolint:funlen // exercises C2C GPM rejection plus sample-free-before-shutdown on lifecycle error
func TestCollectGPMExtendedBatchDegradesGracefullyOnOldDriver(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 50000, nvml.SUCCESS }
	dev.GetMigModeFunc = func() (int, int, nvml.Return) { return nvml.DEVICE_MIG_DISABLE, 0, nvml.SUCCESS }
	dev.GpmQueryDeviceSupportFunc = func() (nvml.GpmSupport, nvml.Return) {
		return nvml.GpmSupport{IsSupportedDevice: 1}, nvml.SUCCESS
	}
	dev.GetFieldValuesFunc = func(values []nvml.FieldValue) nvml.Return {
		for i := range values {
			if values[i].FieldId == nvml.FI_DEV_C2C_LINK_COUNT {
				putUint64Field(&values[i], 2)
			} else {
				values[i].NvmlReturn = uint32(nvml.ERROR_NOT_SUPPORTED)
			}
		}

		return nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	api := fake.api()
	now := time.Unix(1_700_000_000, 0)

	var (
		c2cRet  = nvml.ERROR_INVALID_ARGUMENT
		events  []string
		origShd = api.shutdown
	)

	api.shutdown = func() nvml.Return {
		events = append(events, "shutdown")

		return origShd()
	}
	api.gpmSampleAlloc = func() (nvml.GpmSample, nvml.Return) { return nil, nvml.SUCCESS }
	api.gpmSampleFree = func(nvml.GpmSample) nvml.Return {
		events = append(events, "free")

		return nvml.SUCCESS
	}
	api.gpmSampleGet = func(nvml.Device, nvml.GpmSample) nvml.Return { return nvml.SUCCESS }
	api.gpmMetricsGet = func(req *nvml.GpmMetricsGetType) nvml.Return {
		if req.Metrics[0].MetricId == gpmMetricC2CTotalTxPerSc {
			return c2cRet
		}

		for i := range req.NumMetrics {
			req.Metrics[i].NvmlReturn = uint32(nvml.SUCCESS)
			req.Metrics[i].Value = 50.0
		}

		return nvml.SUCCESS
	}

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	backend.now = func() time.Time { return now }
	query := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{GPM: true, C2CLinks: true})

	// First cycle seeds the GPM sample.
	_, _, err = query(t.Context())
	require.NoError(t, err)

	// Second cycle: C2C batch rejected with INVALID_ARGUMENT, base + precision pipes still succeed.
	now = now.Add(time.Second)

	reading, _, err := query(t.Context())
	require.NoError(t, err)
	require.Len(t, reading.Extras.GPM, 1)
	require.NotNil(t, reading.Extras.GPM[0].SMActivityRatio)
	assert.InDelta(t, 0.5, *reading.Extras.GPM[0].SMActivityRatio, 1e-9)
	require.NotNil(t, reading.Extras.GPM[0].FP32ActivityRatio)
	assert.InDelta(t, 0.5, *reading.Extras.GPM[0].FP32ActivityRatio, 1e-9)
	assert.Nil(t, reading.Extras.GPM[0].C2CTXBytesPerSecond)

	// Third cycle: lifecycle error on C2C batch must free both samples before NVML shutdown.
	events = nil
	c2cRet = nvml.ERROR_GPU_IS_LOST
	now = now.Add(time.Second)

	_, _, err = query(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"free", "free", "shutdown"}, events)
}

//nolint:paralleltest // mutates package-level sysfsPCIDevicesDir
func TestReadPCIeRuntimePM(t *testing.T) {
	orig := sysfsPCIDevicesDir
	tmp := t.TempDir()
	sysfsPCIDevicesDir = tmp

	t.Cleanup(func() { sysfsPCIDevicesDir = orig })

	powerDir := filepath.Join(tmp, "0000:0c:00.0", "power")
	require.NoError(t, os.MkdirAll(powerDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(powerDir, "runtime_suspended_time"), []byte("12500\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(powerDir, "runtime_active_time"), []byte("87500\n"), 0o600))

	reading, ok := readPCIeRuntimePM("uuid-0", "00000000:0C:00.0")
	require.True(t, ok)
	assert.InDelta(t, 12.5, *reading.SuspendedSeconds, 1e-9)
	assert.InDelta(t, 87.5, *reading.ActiveSeconds, 1e-9)

	_, ok = readPCIeRuntimePM("uuid-0", "../escape")
	assert.False(t, ok)
}

func TestModulePowerUsesModuleScope(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetFieldValuesFunc = func(values []nvml.FieldValue) nvml.Return {
		for idx := range values {
			switch {
			case values[idx].FieldId == nvml.FI_DEV_POWER_AVERAGE && values[idx].ScopeId == 0:
				putUint64Field(&values[idx], 120000)
			case values[idx].FieldId == nvml.FI_DEV_POWER_AVERAGE && values[idx].ScopeId == powerScopeModule:
				putUint64Field(&values[idx], 250000)
			default:
				values[idx].NvmlReturn = uint32(nvml.ERROR_NOT_SUPPORTED)
			}
		}

		return nvml.SUCCESS
	}

	backend := newTestBackend(t, &fakeAPI{devices: []nvml.Device{dev}})
	reading, _, err := backend.QueryFunc(
		resolveFields(t, "power.draw.average,module.power.draw.average"),
		CollectOptions{},
	)(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "120 W", cellValue(t, reading.Table, "power.draw.average"))
	assert.Equal(t, "250 W", cellValue(t, reading.Table, "module.power.draw.average"))
}

func TestUntouchedFieldValueSlotIsTreatedAsUnsupported(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 50000, nvml.SUCCESS }
	// Simulate a driver (like GB10 580.173.02) that returns SUCCESS from
	// GetFieldValues without touching unimplemented slots at all.
	dev.GetFieldValuesFunc = func([]nvml.FieldValue) nvml.Return {
		return nvml.SUCCESS
	}

	backend := newTestBackend(t, &fakeAPI{devices: []nvml.Device{dev}})
	reading, _, err := backend.QueryFunc(
		resolveFields(t, "power.draw"),
		CollectOptions{TemperatureTLimitThresholds: true, PCIeErrors: true},
	)(t.Context())
	require.NoError(t, err)
	assert.Empty(t, reading.Extras.TemperatureTLimitThresholds)
	assert.Empty(t, reading.Extras.PCIeErrors)
}

func TestNVSwitchFallbackUsesConnectedLinkCountAndGetNvLinkState(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 70000, nvml.SUCCESS }
	dev.GetFieldValuesFunc = func(values []nvml.FieldValue) nvml.Return {
		for i := range values {
			if values[i].FieldId == nvml.FI_DEV_NVSWITCH_CONNECTED_LINK_COUNT {
				putUint64Field(&values[i], 4)
			}
		}

		return nvml.SUCCESS
	}
	dev.GetNvLinkStateFunc = func(link int) (nvml.EnableState, nvml.Return) {
		if link < 3 {
			return nvml.FEATURE_ENABLED, nvml.SUCCESS
		}

		return nvml.FEATURE_DISABLED, nvml.SUCCESS
	}

	backend := newTestBackend(t, &fakeAPI{devices: []nvml.Device{dev}})
	reading, _, err := backend.QueryFunc(
		resolveFields(t, "power.draw"),
		CollectOptions{NVLink: true},
	)(t.Context())
	require.NoError(t, err)
	require.Len(t, reading.Extras.NVLink, 1)
	require.NotNil(t, reading.Extras.NVLink[0].Links)
	assert.Equal(t, uint64(4), *reading.Extras.NVLink[0].Links)
	require.NotNil(t, reading.Extras.NVLink[0].ActiveLinks)
	assert.Equal(t, uint64(3), *reading.Extras.NVLink[0].ActiveLinks)
}
