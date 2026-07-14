//go:build linux && cgo

package nvmlnative

import (
	"context"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"
	"github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		driverVersion:   func() (string, nvml.Return) { return "590.48.01", nvml.SUCCESS },
		processName:     func(int) (string, nvml.Return) { return "/usr/bin/burn", nvml.SUCCESS },
		validateInforom: func(nvml.Device) nvml.Return { return nvml.SUCCESS },
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

func newTestBackend(t *testing.T, f *fakeAPI) *Backend {
	t.Helper()

	backend, err := newWithAPI(f.api(), slogt.New(t))
	require.NoError(t, err)

	return backend
}

func cellValue(t *testing.T, table *nvidiasmi.Table, q nvidiasmi.QField) string {
	t.Helper()

	require.NotEmpty(t, table.Rows)

	cell, ok := table.Rows[0].QFieldToCells[q]
	require.True(t, ok, "no cell for %q", q)

	return cell.RawValue
}

func TestCollectHappyPath(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }

	f := &fakeAPI{devices: []nvml.Device{dev}}
	b := newTestBackend(t, f)

	reading, code, err := b.QueryFunc(resolveFields(t, "power.draw"), false)(t.Context())
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

	f := &fakeAPI{devices: []nvml.Device{dev}}
	b := newTestBackend(t, f)

	reading, _, err := b.QueryFunc(resolveFields(t, "power.draw"), false)(t.Context())
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

	f := &fakeAPI{devices: []nvml.Device{dev}}
	b := newTestBackend(t, f)
	query := b.QueryFunc(resolveFields(t, "power.draw"), false)

	_, code, err := query(t.Context())
	require.Error(t, err)
	assert.Equal(t, int(nvml.ERROR_GPU_IS_LOST), code)

	var fatal *collect.FatalError

	require.ErrorAs(t, err, &fatal, "lifecycle errors must drive shutdown-on-error")
	assert.Equal(t, 1, f.shutdowns, "backend must tear NVML down after a lifecycle error")

	// recovery: the next cycle re-initializes and succeeds
	lost = false

	_, code, err = query(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, 2, f.inits, "the recovery cycle must re-initialize NVML")
}

func TestZeroDevicesIsAFailedCollection(t *testing.T) {
	t.Parallel()

	f := &fakeAPI{}
	b := newTestBackend(t, f)

	_, _, err := b.QueryFunc(resolveFields(t, "power.draw"), false)(t.Context())
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

	f := &fakeAPI{devices: []nvml.Device{dev}}
	b := newTestBackend(t, f)

	reading, _, err := b.QueryFunc(resolveFields(t, "power.draw.average"), false)(t.Context())
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

	f := &fakeAPI{devices: []nvml.Device{dev}}
	b := newTestBackend(t, f)

	reading, code, err := b.QueryFunc(resolveFields(t, "power.draw"), true)(t.Context())
	require.NoError(t, err, "per-process failures must not fail the collection")
	assert.Equal(t, 0, code)
	assert.True(t, reading.AppsAttempted)
	assert.False(t, reading.AppsSuccess)
	require.Error(t, reading.AppsErr)
	assert.Equal(t, 1, f.shutdowns, "a lost GPU during per-process collection must still mark for re-init")
}

func TestComputeAppsHappyPath(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 100000, nvml.SUCCESS }
	dev.GetComputeRunningProcessesFunc = func() ([]nvml.ProcessInfo, nvml.Return) {
		return []nvml.ProcessInfo{{Pid: 4242, UsedGpuMemory: 2 * 1024 * 1024 * 1024}}, nvml.SUCCESS
	}

	f := &fakeAPI{devices: []nvml.Device{dev}}
	b := newTestBackend(t, f)

	reading, _, err := b.QueryFunc(resolveFields(t, "power.draw"), true)(t.Context())
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

	f := &fakeAPI{devices: []nvml.Device{dev}}
	b := newTestBackend(t, f)
	query := b.QueryFunc(resolveFields(t, "power.draw"), false)

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

	f := &fakeAPI{devices: []nvml.Device{identityDevice()}}
	b := newTestBackend(t, f)

	b.Close()
	b.Close() // idempotent

	assert.Equal(t, 1, f.shutdowns)
}

func TestCloseSkipsWhenCollectionHoldsTheDriver(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) {
		<-release

		return 100000, nvml.SUCCESS
	}

	f := &fakeAPI{devices: []nvml.Device{dev}}
	b := newTestBackend(t, f)
	query := b.QueryFunc(resolveFields(t, "power.draw"), false)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, _, err := query(ctx)
	require.ErrorContains(t, err, "abandoned")

	done := make(chan struct{})

	go func() {
		b.Close() // must return immediately, not hang behind the wedged call
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung behind a wedged driver call")
	}

	assert.Equal(t, 0, f.shutdowns, "Close must skip shutdown while the driver is held")
	close(release)
}

func TestFunctionNotFoundIsLoggedOnce(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 0, nvml.ERROR_FUNCTION_NOT_FOUND }

	f := &fakeAPI{devices: []nvml.Device{dev}}
	b := newTestBackend(t, f)
	query := b.QueryFunc(resolveFields(t, "power.draw"), false)

	reading, _, err := query(t.Context())
	require.NoError(t, err, "a missing optional function must not fail the collection")
	assert.Equal(t, "[Function Not Found]", cellValue(t, reading.Table, "power.draw"))
	assert.True(t, b.fnfLogged, "the missing function must be logged for drift visibility")

	// the second cycle must not log again (the flag stays set)
	_, _, err = query(t.Context())
	require.NoError(t, err)
	assert.True(t, b.fnfLogged)
}
