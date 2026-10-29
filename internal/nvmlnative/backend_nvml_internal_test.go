//go:build linux && cgo

package nvmlnative

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"
	"github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thejerf/slogassert"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

// fakeAPI counts init/shutdown calls and serves the given devices. The mock
// devices panic on any method without a stub, so these tests double as proof
// that unrequested getters are never called (the collection plan).
type fakeAPI struct {
	// atomics: the XID watcher drives init/shutdown from its own goroutine
	inits, shutdowns atomic.Int64
	devices          []nvml.Device
	initRet          nvml.Return
}

func (f *fakeAPI) api() nvmlAPI {
	return nvmlAPI{
		init: func() nvml.Return {
			f.inits.Add(1)

			return f.initRet
		},
		shutdown: func() nvml.Return {
			f.shutdowns.Add(1)

			return nvml.SUCCESS
		},
		deviceCount: func() (int, nvml.Return) { return len(f.devices), nvml.SUCCESS },
		deviceByIndex: func(i int) (nvml.Device, nvml.Return) {
			if i >= len(f.devices) {
				return nil, nvml.ERROR_INVALID_ARGUMENT
			}

			return f.devices[i], nvml.SUCCESS
		},
		driverVersion:     func() (string, nvml.Return) { return "590.48.01", nvml.SUCCESS },
		cudaDriverVersion: func() (int, nvml.Return) { return 13010, nvml.SUCCESS },
		processName:       func(int) (string, nvml.Return) { return "/usr/bin/burn", nvml.SUCCESS },
		lookupSymbol:      func(string) error { return nil },
		validateInforom:   func(nvml.Device) nvml.Return { return nvml.SUCCESS },
	}
}

// identityDevice stubs exactly the getters behind the fixed gpu_info identity
// fields, which every resolution includes.
func identityDevice() *mock.Device {
	return &mock.Device{
		GetNameFunc:   func() (string, nvml.Return) { return "NVIDIA H100 80GB HBM3", nvml.SUCCESS },
		GetSerialFunc: func() (string, nvml.Return) { return "123", nvml.SUCCESS },
		GetUUIDFunc: func() (string, nvml.Return) {
			return "GPU-11111111-2222-3333-4444-555555555555", nvml.SUCCESS
		},
		GetIndexFunc: func() (int, nvml.Return) { return 0, nvml.SUCCESS },
		GetPciInfoFunc: func() (nvml.PciInfo, nvml.Return) {
			return nvml.PciInfo{}, nvml.SUCCESS
		},
		GetDriverModelFunc: func() (nvml.DriverModel, nvml.DriverModel, nvml.Return) {
			return 0, 0, nvml.ERROR_NOT_SUPPORTED
		},
		GetVbiosVersionFunc: func() (string, nvml.Return) { return "96.00.A5.00.03", nvml.SUCCESS },
		GetCudaComputeCapabilityFunc: func() (int, int, nvml.Return) {
			return 9, 0, nvml.SUCCESS
		},
	}
}

func resolveFields(t *testing.T, list string) nvidiasmi.ResolvedFields {
	t.Helper()

	resolved, err := Resolve(list, "", "590.48.01", slogt.New(t))
	require.NoError(t, err)

	return resolved
}

func newTestBackend(t *testing.T, fake *fakeAPI) *Backend {
	t.Helper()

	backend, err := newWithAPI(fake.api(), slogt.New(t))
	require.NoError(t, err)

	return backend
}

func cellValue(t *testing.T, table *nvidiasmi.Table, qField nvidiasmi.QField) string {
	t.Helper()

	require.NotEmpty(t, table.Rows)

	cell, ok := table.Rows[0].QFieldToCells[qField]
	require.True(t, ok, "no cell for %q", qField)

	return cell.RawValue
}

func TestCollectHappyPath(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPciInfoFunc = func() (nvml.PciInfo, nvml.Return) {
		return nvml.PciInfo{PciSubSystemId: 0x10DE}, nvml.SUCCESS
	}
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	reading, code, err := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{})(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "12.34 W", cellValue(t, reading.Table, "power.draw"))
	assert.Equal(t, "NVIDIA H100 80GB HBM3", cellValue(t, reading.Table, "name"))
	assert.Equal(t, "0x10DE", cellValue(t, reading.Table, "pci.sub_device_id"))
	// the fan getter has no stub: reaching it would have panicked, so this
	// test also proves unrequested getters are not called
}

func TestNotSupportedBecomesAbsentToken(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 0, nvml.ERROR_NOT_SUPPORTED }

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{})(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "[N/A]", cellValue(t, reading.Table, "power.draw"))
}

func TestLifecycleErrorFailsCollectionAndReinitializes(t *testing.T) {
	t.Parallel()

	lost := true
	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) {
		if lost {
			return 0, nvml.ERROR_GPU_IS_LOST
		}

		return 100000, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)
	query := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{})

	_, code, err := query(t.Context())
	require.Error(t, err)
	assert.Equal(t, int(nvml.ERROR_GPU_IS_LOST), code)

	var fatal *collect.FatalError

	require.ErrorAs(t, err, &fatal, "lifecycle errors must drive shutdown-on-error")
	assert.Equal(t, int64(1), fake.shutdowns.Load(), "backend must tear NVML down after a lifecycle error")

	// recovery: the next cycle re-initializes and succeeds
	lost = false

	_, code, err = query(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, int64(2), fake.inits.Load(), "the recovery cycle must re-initialize NVML")
}

func TestZeroDevicesIsAFailedCollection(t *testing.T) {
	t.Parallel()

	fake := &fakeAPI{}
	backend := newTestBackend(t, fake)

	_, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{})(t.Context())
	require.ErrorContains(t, err, "no NVML devices")

	var fatal *collect.FatalError

	require.NotErrorAs(t, err, &fatal, "zero devices is a failed collection, not a fatal one")
}

func TestUnknownFieldValueTypeDoesNotPanic(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetFieldValuesFunc = func(values []nvml.FieldValue) nvml.Return {
		for i := range values {
			values[i].NvmlReturn = uint32(nvml.SUCCESS)
			values[i].ValueType = 99 // a value type this build does not know
		}

		return nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw.average"), CollectOptions{})(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "[N/A]", cellValue(t, reading.Table, "power.draw.average"))
}

func TestClocksEventReasonCountersConvertNanosecondsToMicroseconds(t *testing.T) {
	t.Parallel()

	// the raw value NVML serves is in nanoseconds; nvidia-smi prints the same
	// counter in microseconds, and the two backends must emit identical values.
	// 109042687680 ns is a real reading from an RTX 2080 SUPER on driver 610.57.04.
	dev := identityDevice()
	dev.GetFieldValuesFunc = func(values []nvml.FieldValue) nvml.Return {
		for i := range values {
			values[i].NvmlReturn = uint32(nvml.SUCCESS)
			values[i].ValueType = uint32(nvml.VALUE_TYPE_UNSIGNED_LONG_LONG)
			binary.LittleEndian.PutUint64(values[i].Value[:], 109042687680)
		}

		return nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	reading, _, err := backend.QueryFunc(
		resolveFields(t, "clocks_event_reasons_counters.sw_power_cap"),
		CollectOptions{},
	)(t.Context())
	require.NoError(t, err)
	assert.Equal(
		t,
		"109042687 us",
		cellValue(t, reading.Table, "clocks_event_reasons_counters.sw_power_cap"),
	)
}

func TestComputeAppsFailSoftlyButMarkLifecycle(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 100000, nvml.SUCCESS }
	dev.GetComputeRunningProcessesFunc = func() ([]nvml.ProcessInfo, nvml.Return) {
		return nil, nvml.ERROR_GPU_IS_LOST
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	reading, code, err := backend.QueryFunc(
		resolveFields(t, "power.draw"),
		CollectOptions{ComputeApps: true},
	)(
		t.Context(),
	)
	require.NoError(t, err, "per-process failures must not fail the collection")
	assert.Equal(t, 0, code)
	assert.True(t, reading.AppsAttempted)
	assert.False(t, reading.AppsSuccess)
	require.Error(t, reading.AppsErr)
	assert.Equal(
		t,
		int64(1),
		fake.shutdowns.Load(),
		"a lost GPU during per-process collection must still mark for re-init",
	)
}

func TestComputeAppsHappyPath(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 100000, nvml.SUCCESS }
	dev.GetComputeRunningProcessesFunc = func() ([]nvml.ProcessInfo, nvml.Return) {
		return []nvml.ProcessInfo{
			{Pid: 4242, UsedGpuMemory: 2 * 1024 * 1024 * 1024},
			{Pid: 4243, UsedGpuMemory: 512 * 1024 * 1024},
		}, nvml.SUCCESS
	}
	dev.GetProcessUtilizationFunc = func(uint64) ([]nvml.ProcessUtilizationSample, nvml.Return) {
		return []nvml.ProcessUtilizationSample{
			{Pid: 4242, TimeStamp: 100, SmUtil: 20, MemUtil: 10, EncUtil: 0, DecUtil: 0},
			{Pid: 4242, TimeStamp: 200, SmUtil: 85, MemUtil: 40, EncUtil: 0, DecUtil: 0},
		}, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{ComputeApps: true})(t.Context())
	require.NoError(t, err)
	require.Len(t, reading.Apps, 2)
	assert.Equal(t, "4242", reading.Apps[0].PID)
	assert.Equal(t, "/usr/bin/burn", reading.Apps[0].ProcessName)
	assert.Equal(t, "2048 MiB", reading.Apps[0].UsedMemory)
	assert.Equal(t, "11111111-2222-3333-4444-555555555555", reading.Apps[0].GPUUUID)
	require.NotNil(t, reading.Apps[0].Utilization, "newest sample per PID must win")
	assert.InDelta(t, 0.85, reading.Apps[0].Utilization.SMRatio, 1e-9)
	assert.InDelta(t, 0.40, reading.Apps[0].Utilization.MemoryRatio, 1e-9)
	assert.Zero(t, reading.Apps[0].Utilization.EncoderRatio)
	assert.Zero(t, reading.Apps[0].Utilization.DecoderRatio)

	require.NotNil(t, reading.Apps[1].Utilization, "an active process absent from the sample list must report 0")
	assert.Zero(t, reading.Apps[1].Utilization.SMRatio)
	assert.Zero(t, reading.Apps[1].Utilization.MemoryRatio)
}

func TestComputeAppsProcessNameNotFound(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 100000, nvml.SUCCESS }
	dev.GetComputeRunningProcessesFunc = func() ([]nvml.ProcessInfo, nvml.Return) {
		return []nvml.ProcessInfo{{Pid: 12256, UsedGpuMemory: 24859 * 1024 * 1024}}, nvml.SUCCESS
	}
	dev.GetProcessUtilizationFunc = func(uint64) ([]nvml.ProcessUtilizationSample, nvml.Return) {
		return nil, nvml.ERROR_NOT_FOUND
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	api := fake.api()
	api.processName = func(int) (string, nvml.Return) { return "", nvml.ERROR_NOT_FOUND }

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{ComputeApps: true})(t.Context())
	require.NoError(t, err)
	require.Len(t, reading.Apps, 1)
	assert.Equal(t, "12256", reading.Apps[0].PID)
	assert.Equal(t, "[Not Found]", reading.Apps[0].ProcessName)
	assert.Equal(t, "24859 MiB", reading.Apps[0].UsedMemory)
	require.NotNil(t, reading.Apps[0].Utilization, "ERROR_NOT_FOUND means all active processes were idle (0)")
	assert.Zero(t, reading.Apps[0].Utilization.SMRatio)
}

func TestGraphicsAppsHappyPath(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 100000, nvml.SUCCESS }
	dev.GetGraphicsRunningProcessesFunc = func() ([]nvml.ProcessInfo, nvml.Return) {
		return []nvml.ProcessInfo{{Pid: 2620, UsedGpuMemory: 128 * 1024 * 1024}}, nvml.SUCCESS
	}
	dev.GetProcessUtilizationFunc = func(uint64) ([]nvml.ProcessUtilizationSample, nvml.Return) {
		return []nvml.ProcessUtilizationSample{
			{Pid: 2620, TimeStamp: 500, SmUtil: 12, MemUtil: 5, EncUtil: 8, DecUtil: 4},
		}, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	reading, _, err := backend.QueryFunc(
		resolveFields(t, "power.draw"),
		CollectOptions{GraphicsApps: true},
	)(
		t.Context(),
	)
	require.NoError(t, err)
	assert.True(t, reading.GraphicsAppsAttempted)
	assert.True(t, reading.GraphicsAppsSuccess)
	require.Len(t, reading.GraphicsApps, 1)
	assert.Equal(t, "2620", reading.GraphicsApps[0].PID)
	assert.Equal(t, "/usr/bin/burn", reading.GraphicsApps[0].ProcessName)
	assert.Equal(t, "128 MiB", reading.GraphicsApps[0].UsedMemory)
	assert.Equal(t, "11111111-2222-3333-4444-555555555555", reading.GraphicsApps[0].GPUUUID)
	require.NotNil(t, reading.GraphicsApps[0].Utilization)
	assert.InDelta(t, 0.12, reading.GraphicsApps[0].Utilization.SMRatio, 1e-9)
	assert.InDelta(t, 0.08, reading.GraphicsApps[0].Utilization.EncoderRatio, 1e-9)
	assert.InDelta(t, 0.04, reading.GraphicsApps[0].Utilization.DecoderRatio, 1e-9)
}

func TestComputeAppsUtilizationSkipsIdleDevice(t *testing.T) {
	t.Parallel()

	// zero running processes must not call GetProcessUtilization (no stub)
	idleDev := identityDevice()
	idleDev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 100000, nvml.SUCCESS }
	idleDev.GetComputeRunningProcessesFunc = func() ([]nvml.ProcessInfo, nvml.Return) {
		return nil, nvml.SUCCESS
	}

	idleFake := &fakeAPI{devices: []nvml.Device{idleDev}}
	idleBackend := newTestBackend(t, idleFake)

	reading, _, err := idleBackend.QueryFunc(
		resolveFields(t, "power.draw"),
		CollectOptions{ComputeApps: true},
	)(t.Context())
	require.NoError(t, err)
	assert.True(t, reading.AppsSuccess)
	assert.Empty(t, reading.Apps)
}

func TestComputeAppsUtilizationEdgeCases(t *testing.T) {
	t.Parallel()

	// a non-lifecycle error on GetProcessUtilization keeps the process list
	// with nil utilization, whereas a lifecycle error marks lost and fails the
	// per-process pass so remaining unqueried devices are not reported as idle
	retCode := nvml.SUCCESS
	dev0 := identityDevice()
	dev0.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 100000, nvml.SUCCESS }
	dev0.GetComputeRunningProcessesFunc = func() ([]nvml.ProcessInfo, nvml.Return) {
		return []nvml.ProcessInfo{{Pid: 77, UsedGpuMemory: 1024 * 1024}}, nvml.SUCCESS
	}
	dev0.GetProcessUtilizationFunc = func(lastSeen uint64) ([]nvml.ProcessUtilizationSample, nvml.Return) {
		assert.Positive(t, lastSeen, "lookback timestamp must be positive")

		return nil, retCode
	}

	dev1 := identityDevice()
	dev1.GetUUIDFunc = func() (string, nvml.Return) { return "GPU-11111111-1111-1111-1111-111111111111", nvml.SUCCESS }
	dev1.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 100000, nvml.SUCCESS }
	dev1.GetComputeRunningProcessesFunc = func() ([]nvml.ProcessInfo, nvml.Return) {
		require.NotEqual(t, nvml.ERROR_GPU_IS_LOST, retCode, "must not query device 1 after device 0 marks NVML lost")

		return []nvml.ProcessInfo{{Pid: 88, UsedGpuMemory: 1024 * 1024}}, nvml.SUCCESS
	}
	dev1.GetProcessUtilizationFunc = func(_ uint64) ([]nvml.ProcessUtilizationSample, nvml.Return) {
		return nil, nvml.ERROR_NOT_FOUND
	}

	fake := &fakeAPI{devices: []nvml.Device{dev0, dev1}}
	query := newTestBackend(t, fake).QueryFunc(resolveFields(t, "power.draw"), CollectOptions{ComputeApps: true})

	retCode = nvml.ERROR_UNKNOWN

	reading, _, err := query(t.Context())
	require.NoError(t, err)
	assert.True(t, reading.AppsSuccess, "non-lifecycle utilization failure must keep the process list intact")
	require.Len(t, reading.Apps, 2)
	assert.Nil(t, reading.Apps[0].Utilization)
	assert.Zero(t, fake.shutdowns.Load())

	retCode = nvml.ERROR_GPU_IS_LOST

	reading, _, err = query(t.Context())
	require.NoError(t, err)
	assert.False(t, reading.AppsSuccess, "lifecycle utilization failure must fail the per-process pass")
	assert.Empty(t, reading.Apps)
	assert.Equal(t, int64(1), fake.shutdowns.Load(), "lifecycle error must mark the backend for re-init")
}

func TestDualContextAppsShareUtilizationCall(t *testing.T) {
	t.Parallel()

	var utilCalls atomic.Uint32

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 100000, nvml.SUCCESS }
	dev.GetComputeRunningProcessesFunc = func() ([]nvml.ProcessInfo, nvml.Return) {
		return []nvml.ProcessInfo{{Pid: 2165, UsedGpuMemory: 64 * 1024 * 1024}}, nvml.SUCCESS
	}
	dev.GetGraphicsRunningProcessesFunc = func() ([]nvml.ProcessInfo, nvml.Return) {
		return []nvml.ProcessInfo{{Pid: 2165, UsedGpuMemory: 64 * 1024 * 1024}}, nvml.SUCCESS
	}
	dev.GetProcessUtilizationFunc = func(_ uint64) ([]nvml.ProcessUtilizationSample, nvml.Return) {
		call := utilCalls.Add(1)

		return []nvml.ProcessUtilizationSample{
			{Pid: 2165, TimeStamp: uint64(call), SmUtil: 10 * call, MemUtil: 5},
		}, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)
	query := backend.QueryFunc(
		resolveFields(t, "power.draw"),
		CollectOptions{ComputeApps: true, GraphicsApps: true},
	)

	reading, _, err := query(t.Context())
	require.NoError(t, err)
	assert.Equal(t, uint32(1), utilCalls.Load(), "utilization must be queried once per device per cycle")
	require.Len(t, reading.Apps, 1)
	require.Len(t, reading.GraphicsApps, 1)
	require.NotNil(t, reading.Apps[0].Utilization)
	require.NotNil(t, reading.GraphicsApps[0].Utilization)
	assert.Equal(t, reading.Apps[0].Utilization, reading.GraphicsApps[0].Utilization)
}

func TestAbandonedCollectionFailsFastAndRecovers(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) {
		<-release // a wedged driver call

		return 100000, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)
	query := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{})

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, code, err := query(ctx)
	require.ErrorContains(t, err, "abandoned")
	assert.Equal(t, -1, code)

	// while the wedged call lingers, new cycles fail fast instead of queuing
	_, _, err = query(t.Context())
	require.ErrorContains(t, err, "still in progress")

	// once the driver returns, collections work again
	close(release)

	require.Eventually(t, func() bool {
		_, _, err := query(t.Context())

		return err == nil
	}, 2*time.Second, 20*time.Millisecond)
}

func TestCloseShutsDownOnce(t *testing.T) {
	t.Parallel()

	fake := &fakeAPI{devices: []nvml.Device{identityDevice()}}
	backend := newTestBackend(t, fake)

	backend.Close()
	backend.Close() // idempotent

	assert.Equal(t, int64(1), fake.shutdowns.Load())
}

func TestCloseSkipsWhenCollectionHoldsTheDriver(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) {
		<-release

		return 100000, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)
	query := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{})

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, _, err := query(ctx)
	require.ErrorContains(t, err, "abandoned")

	done := make(chan struct{})

	go func() {
		backend.Close() // must return immediately, not hang behind the wedged call
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung behind a wedged driver call")
	}

	assert.Equal(t, int64(0), fake.shutdowns.Load(), "Close must skip shutdown while the driver is held")
	close(release)
}

func TestFunctionNotFoundIsLoggedOnce(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 0, nvml.ERROR_FUNCTION_NOT_FOUND }

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	handler := slogassert.New(t, slog.LevelWarn, nil)
	backend, err := newWithAPI(fake.api(), slog.New(handler))
	require.NoError(t, err)

	query := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{})

	reading, _, err := query(t.Context())
	require.NoError(t, err, "a missing optional function must not fail the collection")
	assert.Equal(t, "[Function Not Found]", cellValue(t, reading.Table, "power.draw"))

	// the second cycle must not log again
	_, _, err = query(t.Context())
	require.NoError(t, err)

	handler.AssertMessage("required NVML functions are unavailable in this driver; " +
		"the affected fields will be absent - please report this on the project's issue tracker")
	handler.AssertEmpty()
}

func TestExtrasCudaVersionRetainedAcrossTransientFailure(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }

	fake := &fakeAPI{devices: []nvml.Device{dev}}

	failing := false
	api := fake.api()
	api.cudaDriverVersion = func() (int, nvml.Return) {
		if failing {
			return 0, nvml.ERROR_UNKNOWN
		}

		return 13010, nvml.SUCCESS
	}

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	query := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{})

	reading, _, err := query(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "13.1", reading.Extras.CUDAVersion)

	// a transient failure must not flap the label to empty
	failing = true

	reading, _, err = query(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "13.1", reading.Extras.CUDAVersion)
}

func TestExtrasCudaVersionUnavailable(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }

	fake := &fakeAPI{devices: []nvml.Device{dev}}

	api := fake.api()
	api.cudaDriverVersion = func() (int, nvml.Return) { return 0, nvml.ERROR_FUNCTION_NOT_FOUND }

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{})(t.Context())
	require.NoError(t, err, "an unreadable CUDA version must not fail the collection")
	assert.Empty(t, reading.Extras.CUDAVersion)
}

func TestExtrasEnergyCounter(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }
	dev.GetTotalEnergyConsumptionFunc = func() (uint64, nvml.Return) { return 12345678, nvml.SUCCESS }

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	opts := CollectOptions{Energy: true}

	reading, code, err := backend.QueryFunc(resolveFields(t, "power.draw"), opts)(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, code)

	require.Len(t, reading.Extras.Energy, 1)
	assert.Equal(t, "11111111-2222-3333-4444-555555555555", reading.Extras.Energy[0].UUID)
	assert.InDelta(t, 12345.678, reading.Extras.Energy[0].Joules, 1e-9)
	// the pcie getter has no stub: reaching it would have panicked, so this
	// test also proves the disabled extras are never collected
}

func TestExtrasEnergyNotSupportedSkipsDevice(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }
	dev.GetTotalEnergyConsumptionFunc = func() (uint64, nvml.Return) {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{Energy: true})(t.Context())
	require.NoError(t, err, "a pre-Volta GPU must not fail the collection")
	assert.Empty(t, reading.Extras.Energy)
}

func TestExtrasPcieThroughput(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }
	dev.GetPcieThroughputFunc = func(counter nvml.PcieUtilCounter) (uint32, nvml.Return) {
		if counter == nvml.PCIE_UTIL_TX_BYTES {
			return 100, nvml.SUCCESS
		}

		return 200, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	opts := CollectOptions{PCIeThroughput: true}

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), opts)(t.Context())
	require.NoError(t, err)

	require.Len(t, reading.Extras.PCIe, 1)
	assert.Equal(t, "11111111-2222-3333-4444-555555555555", reading.Extras.PCIe[0].UUID)
	assert.InDelta(t, 100000, reading.Extras.PCIe[0].TXBytesPerSecond, 1e-9)
	assert.InDelta(t, 200000, reading.Extras.PCIe[0].RXBytesPerSecond, 1e-9)
}

func TestExtrasLifecycleErrorStaysSoftAndMarksLost(t *testing.T) {
	t.Parallel()

	lost := true
	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }
	dev.GetTotalEnergyConsumptionFunc = func() (uint64, nvml.Return) {
		if lost {
			return 0, nvml.ERROR_GPU_IS_LOST
		}

		return 1000, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)
	query := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{Energy: true})

	// the cycle stays green: extras never fail the collection, but the
	// lifecycle-class return still marks the backend for re-initialization
	reading, code, err := query(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, reading.Extras.Energy)
	assert.Equal(t, "12.34 W", cellValue(t, reading.Table, "power.draw"))

	lost = false

	reading, _, err = query(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(2), fake.inits.Load(), "the next cycle must re-initialize NVML")
	require.Len(t, reading.Extras.Energy, 1)
}

func TestExtrasNonLifecycleFailureSkipsOnlyThatDevice(t *testing.T) {
	t.Parallel()

	// device 0's uuid reads fine during the table pass but fails with a
	// non-lifecycle error during the extras pass: only that device's extras
	// may be dropped, device 1 must still report
	dev0 := identityDevice()
	dev0.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }

	uuidCalls := 0
	dev0.GetUUIDFunc = func() (string, nvml.Return) {
		uuidCalls++
		if uuidCalls > 1 {
			return "", nvml.ERROR_UNKNOWN
		}

		return "GPU-11111111-2222-3333-4444-555555555555", nvml.SUCCESS
	}

	dev1 := identityDevice()
	dev1.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }
	dev1.GetUUIDFunc = func() (string, nvml.Return) {
		return "GPU-99999999-2222-3333-4444-555555555555", nvml.SUCCESS
	}
	dev1.GetTotalEnergyConsumptionFunc = func() (uint64, nvml.Return) { return 2000, nvml.SUCCESS }

	fake := &fakeAPI{devices: []nvml.Device{dev0, dev1}}
	backend := newTestBackend(t, fake)

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{Energy: true})(t.Context())
	require.NoError(t, err)

	require.Len(t, reading.Extras.Energy, 1, "device 1 must survive device 0's non-lifecycle failure")
	assert.Equal(t, "99999999-2222-3333-4444-555555555555", reading.Extras.Energy[0].UUID)
	assert.Equal(t, int64(0), fake.shutdowns.Load(), "a non-lifecycle failure must not tear NVML down")
}

func TestExtrasLifecycleAbortsRemainingDevices(t *testing.T) {
	t.Parallel()

	// device 0's energy read hits a lifecycle error with PCIe also enabled:
	// device 0's pcie getter and device 1's energy getter have no stubs, so
	// reaching either would panic, proving the abort covers the rest of the
	// device and all later devices
	dev0 := identityDevice()
	dev0.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }
	dev0.GetTotalEnergyConsumptionFunc = func() (uint64, nvml.Return) {
		return 0, nvml.ERROR_GPU_IS_LOST
	}

	dev1 := identityDevice()
	dev1.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }
	dev1.GetUUIDFunc = func() (string, nvml.Return) {
		return "GPU-99999999-2222-3333-4444-555555555555", nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev0, dev1}}
	backend := newTestBackend(t, fake)

	opts := CollectOptions{Energy: true, PCIeThroughput: true}

	reading, code, err := backend.QueryFunc(resolveFields(t, "power.draw"), opts)(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, reading.Extras.Energy)
	assert.Empty(t, reading.Extras.PCIe)
	assert.Equal(t, int64(1), fake.shutdowns.Load(), "the lifecycle error must mark the backend for re-init")
}

func TestExtrasPcieNotSupportedSkipsDevice(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }
	dev.GetPcieThroughputFunc = func(nvml.PcieUtilCounter) (uint32, nvml.Return) {
		return 0, nvml.ERROR_NOT_SUPPORTED
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	opts := CollectOptions{PCIeThroughput: true}

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), opts)(t.Context())
	require.NoError(t, err, "an unsupported PCIe counter must not fail the collection")
	assert.Empty(t, reading.Extras.PCIe)
	assert.Equal(t, int64(0), fake.shutdowns.Load(), "NOT_SUPPORTED is not a lifecycle error")
}

func TestRemappedRowsInactiveFields(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetRemappedRows_v2Func = func() (nvml.RemappedRowsInfo_v2, nvml.Return) {
		return nvml.RemappedRowsInfo_v2{CorrInactiveRemaps: 3, UncInactiveRemaps: 7}, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	fields := resolveFields(t, "remapped_rows.correctable_inactive,remapped_rows.uncorrectable_inactive")

	reading, _, err := backend.QueryFunc(fields, CollectOptions{})(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "3", cellValue(t, reading.Table, "remapped_rows.correctable_inactive"))
	assert.Equal(t, "7", cellValue(t, reading.Table, "remapped_rows.uncorrectable_inactive"))
}

func TestRemappedRowsInactiveAbsentOnOldDrivers(t *testing.T) {
	t.Parallel()

	// a driver without the newer entry point: the classic four fields keep
	// coming from the unversioned call, only the inactive pair reads absent
	dev := identityDevice()
	dev.GetRemappedRows_v2Func = func() (nvml.RemappedRowsInfo_v2, nvml.Return) {
		return nvml.RemappedRowsInfo_v2{}, nvml.ERROR_FUNCTION_NOT_FOUND
	}
	dev.GetRemappedRowsFunc = func() (int, int, bool, bool, nvml.Return) {
		return 5, 1, false, false, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	fields := resolveFields(t,
		"remapped_rows.correctable,remapped_rows.correctable_inactive,"+
			"remapped_rows.uncorrectable,remapped_rows.uncorrectable_inactive")

	reading, _, err := backend.QueryFunc(fields, CollectOptions{})(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "5", cellValue(t, reading.Table, "remapped_rows.correctable"))
	assert.Equal(t, "1", cellValue(t, reading.Table, "remapped_rows.uncorrectable"))
	assert.Equal(t, "[Function Not Found]", cellValue(t, reading.Table, "remapped_rows.correctable_inactive"))
	assert.Equal(t, "[Function Not Found]", cellValue(t, reading.Table, "remapped_rows.uncorrectable_inactive"))
}

func TestRemappedRowsInactiveAbsentWhenSymbolMissing(t *testing.T) {
	t.Parallel()

	// a driver library that ships no v2 export at all: the getter must not
	// even be called (a missing export crashes at call time, it does not
	// return an error), so the device has no stub for it
	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }

	fake := &fakeAPI{devices: []nvml.Device{dev}}

	api := fake.api()
	api.lookupSymbol = func(name string) error {
		if name == "nvmlDeviceGetRemappedRows_v2" {
			return errors.New("symbol not found")
		}

		return nil
	}

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	fields := resolveFields(t, "power.draw,remapped_rows.correctable_inactive")

	reading, _, err := backend.QueryFunc(fields, CollectOptions{})(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "[Function Not Found]", cellValue(t, reading.Table, "remapped_rows.correctable_inactive"))
	assert.Equal(t, "12.34 W", cellValue(t, reading.Table, "power.draw"))
}

func TestClocksEventReasonsCheckedAgainstSupportedBitmask(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetSupportedClocksEventReasonsFunc = func() (uint64, nvml.Return) {
		return 0x1ff | reasonBoardLimit, nvml.SUCCESS
	}
	dev.GetCurrentClocksEventReasonsFunc = func() (uint64, nvml.Return) {
		return reasonBoardLimit | reasonReliability, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	fields := resolveFields(t,
		"clocks_event_reasons.gpu_idle,clocks_event_reasons.board_limit,clocks_event_reasons.reliability")

	reading, _, err := backend.QueryFunc(fields, CollectOptions{})(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "Not Active", cellValue(t, reading.Table, "clocks_event_reasons.gpu_idle"))
	assert.Equal(t, "Active", cellValue(t, reading.Table, "clocks_event_reasons.board_limit"))
	assert.Equal(t, "[N/A]", cellValue(t, reading.Table, "clocks_event_reasons.reliability"))
}

func TestClocksEventReasonsSupportedGetterErrorPropagates(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetSupportedClocksEventReasonsFunc = func() (uint64, nvml.Return) {
		return 0, nvml.ERROR_NO_PERMISSION
	}
	dev.GetCurrentClocksEventReasonsFunc = func() (uint64, nvml.Return) {
		return reasonBoardLimit, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	fields := resolveFields(t, "clocks_event_reasons.board_limit")

	reading, _, err := backend.QueryFunc(fields, CollectOptions{})(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "[Insufficient Permissions]", cellValue(t, reading.Table, "clocks_event_reasons.board_limit"))
}

func TestClocksEventReasonsActiveLifecycleErrorNotMasked(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetSupportedClocksEventReasonsFunc = func() (uint64, nvml.Return) {
		return 0x1ff, nvml.SUCCESS
	}
	dev.GetCurrentClocksEventReasonsFunc = func() (uint64, nvml.Return) {
		return 0, nvml.ERROR_GPU_IS_LOST
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	fields := resolveFields(t, "clocks_event_reasons.board_limit")

	_, code, err := backend.QueryFunc(fields, CollectOptions{})(t.Context())
	require.Error(t, err)
	assert.Equal(t, int(nvml.ERROR_GPU_IS_LOST), code)
}

func TestPciInfoSplitFromPciInfoExt(t *testing.T) {
	t.Parallel()

	// GetPciInfoExt has no stub on dev: reaching it would panic, proving
	// the classic PCI fields are served from GetPciInfo alone on pre-550
	// drivers that lack nvmlDeviceGetPciInfoExt.
	dev := identityDevice()
	dev.GetPciInfoFunc = func() (nvml.PciInfo, nvml.Return) {
		info := nvml.PciInfo{
			Domain:         0,
			Bus:            0x0c,
			Device:         0,
			PciDeviceId:    0x1E8110DE,
			PciSubSystemId: 0x37151462,
		}
		copy(info.BusId[:], []int8{'0', '0', '0', '0', '0', '0', '0', '0', ':', '0', 'C', ':', '0', '0', '.', '0'})

		return info, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	fields := resolveFields(t, "pci.bus_id,pci.domain,pci.bus,pci.device,pci.device_id,pci.sub_device_id")

	reading, _, err := backend.QueryFunc(fields, CollectOptions{})(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "00000000:0C:00.0", cellValue(t, reading.Table, "pci.bus_id"))
	assert.Equal(t, "0x0000", cellValue(t, reading.Table, "pci.domain"))
	assert.Equal(t, "0x0C", cellValue(t, reading.Table, "pci.bus"))
	assert.Equal(t, "0x00", cellValue(t, reading.Table, "pci.device"))
	assert.Equal(t, "0x1E8110DE", cellValue(t, reading.Table, "pci.device_id"))
	assert.Equal(t, "0x37151462", cellValue(t, reading.Table, "pci.sub_device_id"))

	// requesting pci.baseClass and pci.subClass uses GetPciInfoExt
	dev.GetPciInfoExtFunc = func() (nvml.PciInfoExt, nvml.Return) {
		return nvml.PciInfoExt{BaseClass: 0x3, SubClass: 0x0}, nvml.SUCCESS
	}

	extFields := resolveFields(t, "pci.baseClass,pci.subClass")

	reading, _, err = backend.QueryFunc(extFields, CollectOptions{})(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "0x3", cellValue(t, reading.Table, "pci.baseClass"))
	assert.Equal(t, "0x0", cellValue(t, reading.Table, "pci.subClass"))
}

func TestClocksThrottleReasonsFallbackWhenEventReasonsMissing(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetSupportedClocksEventReasonsFunc = func() (uint64, nvml.Return) {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}
	dev.GetCurrentClocksEventReasonsFunc = func() (uint64, nvml.Return) {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}
	dev.GetSupportedClocksThrottleReasonsFunc = func() (uint64, nvml.Return) {
		return 0x1ff, nvml.SUCCESS
	}
	dev.GetCurrentClocksThrottleReasonsFunc = func() (uint64, nvml.Return) {
		return reasonGpuIdle, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	fields, err := Resolve(
		"clocks_throttle_reasons.supported,clocks_throttle_reasons.active,clocks_throttle_reasons.gpu_idle",
		"",
		"525.147.05",
		slogt.New(t),
	)
	require.NoError(t, err)

	reading, _, err := backend.QueryFunc(fields, CollectOptions{})(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "0x00000000000001FF", cellValue(t, reading.Table, "clocks_throttle_reasons.supported"))
	assert.Equal(t, "0x0000000000000001", cellValue(t, reading.Table, "clocks_throttle_reasons.active"))
	assert.Equal(t, "Active", cellValue(t, reading.Table, "clocks_throttle_reasons.gpu_idle"))
}

func TestMemoryInfoFallbackWhenV2Missing(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetMemoryInfo_v2Func = func() (nvml.Memory_v2, nvml.Return) {
		return nvml.Memory_v2{}, nvml.ERROR_FUNCTION_NOT_FOUND
	}
	dev.GetMemoryInfoFunc = func() (nvml.Memory, nvml.Return) {
		return nvml.Memory{
			Total: 8192 * 1024 * 1024,
			Used:  1024 * 1024 * 1024,
			Free:  7168 * 1024 * 1024,
		}, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	fields := resolveFields(t, "memory.total,memory.reserved,memory.used,memory.free")

	reading, _, err := backend.QueryFunc(fields, CollectOptions{})(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "8192 MiB", cellValue(t, reading.Table, "memory.total"))
	assert.Equal(t, "[Function Not Found]", cellValue(t, reading.Table, "memory.reserved"))
	assert.Equal(t, "1024 MiB", cellValue(t, reading.Table, "memory.used"))
	assert.Equal(t, "7168 MiB", cellValue(t, reading.Table, "memory.free"))
}
