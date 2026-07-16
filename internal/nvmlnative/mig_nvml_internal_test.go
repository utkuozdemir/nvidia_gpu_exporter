//go:build linux && cgo

package nvmlnative

import (
	"math"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"
	"github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gpmFake tracks the seam-level GPM calls: sample lifecycle accounting
// (allocs must equal frees plus live retained samples) and canned metric
// values keyed by metric id.
type gpmFake struct {
	allocs, frees int
	migSampleRet  nvml.Return
	metricsRet    nvml.Return
	values        map[uint32]float64
}

func (g *gpmFake) install(api *nvmlAPI) {
	api.gpmSampleAlloc = func() (nvml.GpmSample, nvml.Return) {
		g.allocs++

		return &mock.GpmSample{}, nvml.SUCCESS
	}
	api.gpmSampleFree = func(nvml.GpmSample) nvml.Return {
		g.frees++

		return nvml.SUCCESS
	}
	api.gpmMigSampleGet = func(nvml.Device, int, nvml.GpmSample) nvml.Return {
		return g.migSampleRet
	}
	api.gpmMetricsGet = func(metricsGet *nvml.GpmMetricsGetType) nvml.Return {
		if g.metricsRet != nvml.SUCCESS {
			return g.metricsRet
		}

		for i := range metricsGet.NumMetrics {
			metric := &metricsGet.Metrics[i]
			metric.NvmlReturn = uint32(nvml.SUCCESS)
			metric.Value = g.values[metric.MetricId]
		}

		return nvml.SUCCESS
	}
}

func (g *gpmFake) live() int { return g.allocs - g.frees }

// migDevice builds a mock MIG device handle (compute instance 0; the
// multi-CI rendering semantics are covered by the exporter tests).
func migDevice(uuid string, gi int) *mock.Device {
	return &mock.Device{
		GetUUIDFunc:              func() (string, nvml.Return) { return uuid, nvml.SUCCESS },
		GetNameFunc:              func() (string, nvml.Return) { return "NVIDIA H100 80GB HBM3 MIG 1g.10gb", nvml.SUCCESS },
		GetGpuInstanceIdFunc:     func() (int, nvml.Return) { return gi, nvml.SUCCESS },
		GetComputeInstanceIdFunc: func() (int, nvml.Return) { return 0, nvml.SUCCESS },
		GetMemoryInfo_v2Func: func() (nvml.Memory_v2, nvml.Return) {
			return nvml.Memory_v2{Total: 10 * 1024 * 1024 * 1024, Used: 1024, Free: 999, Reserved: 512}, nvml.SUCCESS
		},
	}
}

// migParent builds a MIG-enabled parent device serving the given MIG
// handles, with GPM support flagged as requested.
func migParent(gpmSupported bool, migs ...nvml.Device) *mock.Device {
	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }
	dev.GetMigModeFunc = func() (int, int, nvml.Return) {
		return nvml.DEVICE_MIG_ENABLE, nvml.DEVICE_MIG_ENABLE, nvml.SUCCESS
	}
	dev.GetMaxMigDeviceCountFunc = func() (int, nvml.Return) { return len(migs) + 2, nvml.SUCCESS }
	dev.GetMigDeviceHandleByIndexFunc = func(idx int) (nvml.Device, nvml.Return) {
		if idx >= len(migs) {
			// sparse tail, as on real hardware
			return nil, nvml.ERROR_NOT_FOUND
		}

		return migs[idx], nvml.SUCCESS
	}
	dev.GpmQueryDeviceSupportFunc = func() (nvml.GpmSupport, nvml.Return) {
		supported := uint32(0)
		if gpmSupported {
			supported = 1
		}

		return nvml.GpmSupport{IsSupportedDevice: supported}, nvml.SUCCESS
	}

	return dev
}

func migOpts() CollectOptions { return CollectOptions{MIG: true} }

func TestMIGInventoryAndMemory(t *testing.T) {
	t.Parallel()

	parent := migParent(false,
		migDevice("MIG-AAAAAAAA-1111-1111-1111-111111111111", 1),
		migDevice("MIG-BBBBBBBB-1111-1111-1111-111111111111", 2),
	)

	fake := &fakeAPI{devices: []nvml.Device{parent}}
	backend := newTestBackend(t, fake)

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), migOpts())(t.Context())
	require.NoError(t, err)

	require.Len(t, reading.Extras.MIG, 2)

	first := reading.Extras.MIG[0]
	assert.Equal(t, "11111111-2222-3333-4444-555555555555", first.ParentUUID)
	assert.Equal(t, "aaaaaaaa-1111-1111-1111-111111111111", first.UUID, "MIG- prefix must be normalized away")
	assert.Equal(t, "1", first.GPUInstanceID)
	assert.Equal(t, "0", first.ComputeInstanceID)
	assert.Equal(t, "1g.10gb", first.Profile, "the profile must be parsed out of the device name")
	require.NotNil(t, first.Memory)
	assert.Equal(t, uint64(10*1024*1024*1024), first.Memory.Total)
	assert.Equal(t, uint64(512), first.Memory.Reserved)
	assert.Nil(t, first.Utilization, "no GPM support must mean inventory and memory only")
}

func TestMIGDisabledTouchesNoMIGGetter(t *testing.T) {
	t.Parallel()

	// MIG mode disabled: beyond the mode probe, no MIG getter has a stub,
	// so touching any of them would panic
	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }
	dev.GetMigModeFunc = func() (int, int, nvml.Return) {
		return nvml.DEVICE_MIG_DISABLE, nvml.DEVICE_MIG_DISABLE, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), migOpts())(t.Context())
	require.NoError(t, err)
	assert.Empty(t, reading.Extras.MIG)
}

func TestMIGNotSupportedIsSilent(t *testing.T) {
	t.Parallel()

	// a GPU that cannot do MIG at all reports NOT_SUPPORTED on the mode
	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }
	dev.GetMigModeFunc = func() (int, int, nvml.Return) { return 0, 0, nvml.ERROR_NOT_SUPPORTED }

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), migOpts())(t.Context())
	require.NoError(t, err)
	assert.Empty(t, reading.Extras.MIG)
	assert.Equal(t, int64(0), fake.shutdowns.Load())
}

func TestMIGGPMCrossCycle(t *testing.T) {
	t.Parallel()

	parent := migParent(true, migDevice("MIG-AAAAAAAA-1111-1111-1111-111111111111", 1))
	fake := &fakeAPI{devices: []nvml.Device{parent}}

	gpm := &gpmFake{values: map[uint32]float64{
		gpmMetricGraphicsUtil:  99.9992,
		gpmMetricSMUtil:        90.668,
		gpmMetricSMOccupancy:   50,
		gpmMetricAnyTensorUtil: 22.7059,
		gpmMetricPcieTxPerSec:  100,
		gpmMetricPcieRxPerSec:  50,
	}}

	api := fake.api()
	gpm.install(&api)

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	current := time.Now()
	backend.now = func() time.Time { return current }

	query := backend.QueryFunc(resolveFields(t, "power.draw"), migOpts())

	// first sight seeds the sample and emits nothing
	reading, _, err := query(t.Context())
	require.NoError(t, err)
	require.Len(t, reading.Extras.MIG, 1)
	assert.Nil(t, reading.Extras.MIG[0].Utilization)
	assert.Equal(t, 1, gpm.live(), "the seed sample must be retained")

	// the second cycle diffs against the retained sample
	current = current.Add(5 * time.Second)

	reading, _, err = query(t.Context())
	require.NoError(t, err)

	util := reading.Extras.MIG[0].Utilization
	require.NotNil(t, util)
	require.NotNil(t, util.GraphicsActivityRatio)
	assert.InDelta(t, 0.999992, *util.GraphicsActivityRatio, 1e-9, "GPM percentages must become ratios")
	require.NotNil(t, util.SMActivityRatio)
	assert.InDelta(t, 0.90668, *util.SMActivityRatio, 1e-9)
	require.NotNil(t, util.TensorActivityRatio)
	assert.InDelta(t, 0.227059, *util.TensorActivityRatio, 1e-9)
	require.NotNil(t, util.PCIeTXBytesPerSecond)
	assert.InDelta(t, 100*1048576, *util.PCIeTXBytesPerSecond, 1e-9, "GPM PCIe is MiB/s, not KB/s")
	assert.Equal(t, 1, gpm.live(), "exactly one retained sample per GPU instance")

	// a rapid follow-up scrape must not rotate the anchored sample and must
	// serve the same values again
	current = current.Add(100 * time.Millisecond)
	allocsBefore := gpm.allocs

	reading, _, err = query(t.Context())
	require.NoError(t, err)
	require.NotNil(t, reading.Extras.MIG[0].Utilization)
	assert.InDelta(t, 0.999992, *reading.Extras.MIG[0].Utilization.GraphicsActivityRatio, 1e-9)
	assert.Equal(t, allocsBefore, gpm.allocs, "the min-window guard must not take a new sample")
}

func TestMIGGPMFingerprintInvalidation(t *testing.T) {
	t.Parallel()

	// the same numeric GI id backed by a different MIG device between two
	// cycles (destroy + recreate) must reseed instead of diffing across
	// generations
	uuid := "MIG-AAAAAAAA-1111-1111-1111-111111111111"
	mig := migDevice("", 1)
	mig.GetUUIDFunc = func() (string, nvml.Return) { return uuid, nvml.SUCCESS }

	parent := migParent(true, mig)
	fake := &fakeAPI{devices: []nvml.Device{parent}}

	gpm := &gpmFake{values: map[uint32]float64{gpmMetricSMUtil: 50}}

	api := fake.api()
	gpm.install(&api)

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	current := time.Now()
	backend.now = func() time.Time { return current }

	query := backend.QueryFunc(resolveFields(t, "power.draw"), migOpts())

	_, _, err = query(t.Context())
	require.NoError(t, err)

	// reconfigure: same GI id, new MIG device uuid
	uuid = "MIG-CCCCCCCC-1111-1111-1111-111111111111"
	current = current.Add(5 * time.Second)

	reading, _, err := query(t.Context())
	require.NoError(t, err)
	assert.Nil(t, reading.Extras.MIG[0].Utilization,
		"a changed GPU instance generation must reseed, not diff across generations")
	assert.Equal(t, 1, gpm.live(), "the stale sample must be freed, the new seed retained")
	assert.Equal(t, 2, gpm.allocs)
}

func TestMIGGPMOrphanCleanupAndMarkLost(t *testing.T) {
	t.Parallel()

	migEnabled := true
	parent := migParent(true, migDevice("MIG-AAAAAAAA-1111-1111-1111-111111111111", 1))
	parent.GetMigModeFunc = func() (int, int, nvml.Return) {
		if migEnabled {
			return nvml.DEVICE_MIG_ENABLE, nvml.DEVICE_MIG_ENABLE, nvml.SUCCESS
		}

		return nvml.DEVICE_MIG_DISABLE, nvml.DEVICE_MIG_DISABLE, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{parent}}

	gpm := &gpmFake{values: map[uint32]float64{}}

	api := fake.api()
	gpm.install(&api)

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	current := time.Now()
	backend.now = func() time.Time { return current }

	query := backend.QueryFunc(resolveFields(t, "power.draw"), migOpts())

	_, _, err = query(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, gpm.live())

	// the GPU instance disappears (MIG disabled): the orphaned sample must
	// be freed
	migEnabled = false
	current = current.Add(5 * time.Second)

	_, _, err = query(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, gpm.live(), "orphaned GPU instance samples must be freed")

	// re-enable and seed again, then a lifecycle teardown must free the rest
	migEnabled = true
	current = current.Add(5 * time.Second)

	_, _, err = query(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, gpm.live())

	backend.mu.Lock()
	backend.markLost()
	backend.mu.Unlock()

	assert.Equal(t, 0, gpm.live(), "markLost must free every retained sample before shutdown")
}

func TestComputeAppsMIGAttribution(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	dev.GetPowerUsageFunc = func() (uint32, nvml.Return) { return 12340, nvml.SUCCESS }
	dev.GetComputeRunningProcessesFunc = func() ([]nvml.ProcessInfo, nvml.Return) {
		return []nvml.ProcessInfo{
			{Pid: 42, UsedGpuMemory: 1024 * 1024, GpuInstanceId: 3, ComputeInstanceId: 0},
			{Pid: 43, UsedGpuMemory: 1024 * 1024, GpuInstanceId: migInvalidID, ComputeInstanceId: migInvalidID},
		}, nvml.SUCCESS
	}

	fake := &fakeAPI{devices: []nvml.Device{dev}}
	backend := newTestBackend(t, fake)

	opts := CollectOptions{ComputeApps: true}

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), opts)(t.Context())
	require.NoError(t, err)
	require.Len(t, reading.Apps, 2)

	assert.Equal(t, "3", reading.Apps[0].GPUInstanceID)
	assert.Equal(t, "0", reading.Apps[0].ComputeInstanceID)
	assert.Empty(t, reading.Apps[1].GPUInstanceID, "the invalid-id sentinel must render as an empty label")
	assert.Empty(t, reading.Apps[1].ComputeInstanceID)
}

func TestMIGGPMMaxWindowReseeds(t *testing.T) {
	t.Parallel()

	parent := migParent(true, migDevice("MIG-AAAAAAAA-1111-1111-1111-111111111111", 1))
	fake := &fakeAPI{devices: []nvml.Device{parent}}

	gpm := &gpmFake{values: map[uint32]float64{gpmMetricSMUtil: 50}}

	api := fake.api()
	gpm.install(&api)

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	current := time.Now()
	backend.now = func() time.Time { return current }

	query := backend.QueryFunc(resolveFields(t, "power.draw"), migOpts())

	_, _, err = query(t.Context())
	require.NoError(t, err)

	// a gap beyond the maximum window must reseed instead of reporting an
	// average over a boundless window
	current = current.Add(11 * time.Minute)

	reading, _, err := query(t.Context())
	require.NoError(t, err)
	assert.Nil(t, reading.Extras.MIG[0].Utilization, "an overgrown window must reseed, not diff")
	assert.Equal(t, 1, gpm.live(), "the stale sample must be freed and the fresh seed retained")
	assert.Equal(t, 2, gpm.allocs)

	// and the reseeded state works again afterwards
	current = current.Add(5 * time.Second)

	reading, _, err = query(t.Context())
	require.NoError(t, err)
	require.NotNil(t, reading.Extras.MIG[0].Utilization)
}

func TestMIGGPMPerMetricFailureAndNonFiniteSkip(t *testing.T) {
	t.Parallel()

	parent := migParent(true, migDevice("MIG-AAAAAAAA-1111-1111-1111-111111111111", 1))
	fake := &fakeAPI{devices: []nvml.Device{parent}}

	gpm := &gpmFake{}

	api := fake.api()
	gpm.install(&api)
	// override the metrics decoder: one metric fails per-metric, one is
	// non-finite, the rest carry values
	api.gpmMetricsGet = func(metricsGet *nvml.GpmMetricsGetType) nvml.Return {
		for i := range metricsGet.NumMetrics {
			metric := &metricsGet.Metrics[i]

			switch metric.MetricId {
			case gpmMetricSMUtil:
				metric.NvmlReturn = uint32(nvml.ERROR_NOT_SUPPORTED)
			case gpmMetricSMOccupancy:
				metric.NvmlReturn = uint32(nvml.SUCCESS)
				metric.Value = math.NaN()
			default:
				metric.NvmlReturn = uint32(nvml.SUCCESS)
				metric.Value = 42
			}
		}

		return nvml.SUCCESS
	}

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	current := time.Now()
	backend.now = func() time.Time { return current }

	query := backend.QueryFunc(resolveFields(t, "power.draw"), migOpts())

	_, _, err = query(t.Context())
	require.NoError(t, err)

	current = current.Add(5 * time.Second)

	reading, _, err := query(t.Context())
	require.NoError(t, err)

	util := reading.Extras.MIG[0].Utilization
	require.NotNil(t, util)
	assert.Nil(t, util.SMActivityRatio, "a failed per-metric status must render nothing")
	assert.Nil(t, util.SMOccupancyRatio, "a non-finite value must render nothing")
	require.NotNil(t, util.GraphicsActivityRatio, "healthy sibling metrics must survive")
	assert.InDelta(t, 0.42, *util.GraphicsActivityRatio, 1e-9)
}

func TestMIGGPMMetricsFailureRotatesAndRecovers(t *testing.T) {
	t.Parallel()

	parent := migParent(true, migDevice("MIG-AAAAAAAA-1111-1111-1111-111111111111", 1))
	fake := &fakeAPI{devices: []nvml.Device{parent}}

	gpm := &gpmFake{values: map[uint32]float64{gpmMetricSMUtil: 50}}

	api := fake.api()
	gpm.install(&api)

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	current := time.Now()
	backend.now = func() time.Time { return current }

	query := backend.QueryFunc(resolveFields(t, "power.draw"), migOpts())

	_, _, err = query(t.Context())
	require.NoError(t, err)

	// a transient non-lifecycle metrics failure: no values this cycle, the
	// rotation continues, nothing leaks and the cycle stays green
	gpm.metricsRet = nvml.ERROR_UNKNOWN
	current = current.Add(5 * time.Second)

	reading, _, err := query(t.Context())
	require.NoError(t, err)
	assert.Nil(t, reading.Extras.MIG[0].Utilization)
	assert.Equal(t, 1, gpm.live())
	assert.Equal(t, int64(0), fake.shutdowns.Load())

	// and it heals on the next cycle
	gpm.metricsRet = nvml.SUCCESS
	current = current.Add(5 * time.Second)

	reading, _, err = query(t.Context())
	require.NoError(t, err)
	require.NotNil(t, reading.Extras.MIG[0].Utilization)
}

func TestMIGGPMSampleGetLifecycleAborts(t *testing.T) {
	t.Parallel()

	parent := migParent(true, migDevice("MIG-AAAAAAAA-1111-1111-1111-111111111111", 1))
	fake := &fakeAPI{devices: []nvml.Device{parent}}

	gpm := &gpmFake{migSampleRet: nvml.ERROR_GPU_IS_LOST}

	api := fake.api()
	gpm.install(&api)

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), migOpts())(t.Context())
	require.NoError(t, err, "extras must never fail the collection")
	assert.Nil(t, reading.Extras.MIG[0].Utilization)
	assert.Equal(t, int64(1), fake.shutdowns.Load(), "the lifecycle error must mark the backend for re-init")
	assert.Equal(t, 0, gpm.live(), "no sample may leak through the abort")
}

func TestMIGLifecycleOnHandleLookupAborts(t *testing.T) {
	t.Parallel()

	// the handle lookup hitting a lost GPU must mark the backend for
	// re-initialization, not read as a sparse slot
	parent := migParent(true)
	parent.GetMigDeviceHandleByIndexFunc = func(int) (nvml.Device, nvml.Return) {
		return nil, nvml.ERROR_GPU_IS_LOST
	}
	parent.GetMaxMigDeviceCountFunc = func() (int, nvml.Return) { return 2, nvml.SUCCESS }

	fake := &fakeAPI{devices: []nvml.Device{parent}}
	backend := newTestBackend(t, fake)

	reading, _, err := backend.QueryFunc(resolveFields(t, "power.draw"), migOpts())(t.Context())
	require.NoError(t, err)
	assert.Empty(t, reading.Extras.MIG)
	assert.Equal(t, int64(1), fake.shutdowns.Load(), "a lost GPU during MIG enumeration must mark for re-init")
}

func TestCloseFreesGPMSamples(t *testing.T) {
	t.Parallel()

	parent := migParent(true, migDevice("MIG-AAAAAAAA-1111-1111-1111-111111111111", 1))
	fake := &fakeAPI{devices: []nvml.Device{parent}}

	gpm := &gpmFake{values: map[uint32]float64{}}

	api := fake.api()
	gpm.install(&api)

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	_, _, err = backend.QueryFunc(resolveFields(t, "power.draw"), migOpts())(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, gpm.live())

	backend.Close()

	assert.Equal(t, 0, gpm.live(), "Close must free every retained sample before shutdown")
	assert.Equal(t, int64(1), fake.shutdowns.Load())
}

func TestMIGGPMFingerprintProfileChange(t *testing.T) {
	t.Parallel()

	// the same GI id and MIG uuid, but a different profile (destroy and
	// recreate with another shape) must also invalidate the retention
	name := "NVIDIA H100 80GB HBM3 MIG 1g.10gb"
	mig := migDevice("MIG-AAAAAAAA-1111-1111-1111-111111111111", 1)
	mig.GetNameFunc = func() (string, nvml.Return) { return name, nvml.SUCCESS }

	parent := migParent(true, mig)
	fake := &fakeAPI{devices: []nvml.Device{parent}}

	gpm := &gpmFake{values: map[uint32]float64{gpmMetricSMUtil: 50}}

	api := fake.api()
	gpm.install(&api)

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	current := time.Now()
	backend.now = func() time.Time { return current }

	query := backend.QueryFunc(resolveFields(t, "power.draw"), migOpts())

	_, _, err = query(t.Context())
	require.NoError(t, err)

	name = "NVIDIA H100 80GB HBM3 MIG 2g.20gb"
	current = current.Add(5 * time.Second)

	reading, _, err := query(t.Context())
	require.NoError(t, err)
	assert.Nil(t, reading.Extras.MIG[0].Utilization, "a changed profile must reseed the retention")
	assert.Equal(t, 1, gpm.live())
}
