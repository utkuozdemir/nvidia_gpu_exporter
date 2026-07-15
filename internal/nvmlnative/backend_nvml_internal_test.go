//go:build linux && cgo

package nvmlnative

import (
	"context"
	"log/slog"
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
	inits, shutdowns int
	devices          []nvml.Device
	initRet          nvml.Return
}

func (f *fakeAPI) api() nvmlAPI {
	return nvmlAPI{
		init: func() nvml.Return {
			f.inits++

			return f.initRet
		},
		shutdown: func() nvml.Return {
			f.shutdowns++

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
		GetPciInfoExtFunc: func() (nvml.PciInfoExt, nvml.Return) {
			return nvml.PciInfoExt{}, nvml.SUCCESS
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
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	reading, code, err := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{})(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "12.34 W", cellValue(t, reading.Table, "power.draw"))
	assert.Equal(t, "NVIDIA H100 80GB HBM3", cellValue(t, reading.Table, "name"))
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
	assert.Equal(t, 1, fake.shutdowns, "backend must tear NVML down after a lifecycle error")

	// recovery: the next cycle re-initializes and succeeds
	lost = false

	_, code, err = query(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, 2, fake.inits, "the recovery cycle must re-initialize NVML")
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
	assert.Equal(t, 1, fake.shutdowns, "a lost GPU during per-process collection must still mark for re-init")
}

func TestComputeAppsHappyPath(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 100000, nvml.SUCCESS }
	dev.GetComputeRunningProcessesFunc = func() ([]nvml.ProcessInfo, nvml.Return) {
		return []nvml.ProcessInfo{{Pid: 4242, UsedGpuMemory: 2 * 1024 * 1024 * 1024}}, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), CollectOptions{ComputeApps: true})(t.Context())
	require.NoError(t, err)
	require.Len(t, reading.Apps, 1)
	assert.Equal(t, "4242", reading.Apps[0].PID)
	assert.Equal(t, "/usr/bin/burn", reading.Apps[0].ProcessName)
	assert.Equal(t, "2048 MiB", reading.Apps[0].UsedMemory)
	assert.Equal(t, "11111111-2222-3333-4444-555555555555", reading.Apps[0].GPUUUID)
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

	assert.Equal(t, 1, fake.shutdowns)
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

	assert.Equal(t, 0, fake.shutdowns, "Close must skip shutdown while the driver is held")
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
	assert.Equal(t, 2, fake.inits, "the next cycle must re-initialize NVML")
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
	assert.Equal(t, 0, fake.shutdowns, "a non-lifecycle failure must not tear NVML down")
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
	assert.Equal(t, 1, fake.shutdowns, "the lifecycle error must mark the backend for re-init")
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
	assert.Equal(t, 0, fake.shutdowns, "NOT_SUPPORTED is not a lifecycle error")
}
