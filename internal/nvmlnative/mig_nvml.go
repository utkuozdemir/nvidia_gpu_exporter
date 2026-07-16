//go:build linux && cgo

package nvmlnative

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
)

// The GPM metric ids this backend samples, from the NVML metric catalog.
// Activity metrics report percentages (0-100), the PCIe metrics MiB/s.
const (
	gpmMetricGraphicsUtil  = 1
	gpmMetricSMUtil        = 2
	gpmMetricSMOccupancy   = 3
	gpmMetricAnyTensorUtil = 5
	gpmMetricPcieTxPerSec  = 20
	gpmMetricPcieRxPerSec  = 21
)

// gpmMibMultiplier converts the GPM PCIe metrics (documented as MiB/sec, a
// different unit from the whole-GPU KB/s throughput call) to bytes/s.
const gpmMibMultiplier = 1024 * 1024

// gpmMinWindow keeps the sampling window from collapsing when several
// scrapers hit the exporter in quick succession: below it the retained
// sample is not rotated and the previous computed values are served again,
// so the window stays anchored instead of shrinking to a meaningless size.
const gpmMinWindow = time.Second

// gpmMaxWindow bounds how stale a retained sample may grow (a scraper that
// vanished for a long time): beyond it the sample is reseeded and the cycle
// emits no utilization, exactly like a first sight.
const gpmMaxWindow = 10 * time.Minute

// migInvalidID is how NVML reports "not a MIG process" in the per-process
// instance id fields.
const migInvalidID = 0xFFFFFFFF

// gpmState is the retained previous GPM sample of one GPU instance.
type gpmState struct {
	sample nvml.GpmSample
	taken  time.Time
	// fingerprint identifies the GPU instance generation: numeric GI ids
	// are reused immediately across MIG reconfigurations, and diffing
	// samples across two different instances that happen to share an id
	// would produce a plausible-looking wrong value.
	fingerprint string
	// last holds the most recent computed utilization, served again while
	// the window guard keeps the sample anchored.
	last *collect.MIGUtilization
}

// migGroup accumulates one GPU instance's members during enumeration, to
// build the generation fingerprint.
type migGroup struct {
	gi      int
	members []string
}

// collectMIG gathers one parent device's MIG inventory, memory and GPM
// utilization into extras. seenGIs records the GPM state keys touched this
// cycle for orphan cleanup. Reports whether extras collection may continue.
//
//nolint:cyclop // one linear pass: mode gate, enumeration, GPM per instance
func (b *Backend) collectMIG(
	dev nvml.Device,
	parentUUID string,
	extras *collect.Extras,
	seenGIs map[string]bool,
) bool {
	mode, _, ret := dev.GetMigMode()

	switch {
	case ret == nvml.ERROR_NOT_SUPPORTED:
		// not a MIG-capable GPU: nothing to report
		return true
	case ret != nvml.SUCCESS:
		return b.extrasFailure("mig", "cannot read the MIG mode", ret)
	case mode != nvml.DEVICE_MIG_ENABLE:
		return true
	}

	groups, ok := b.enumerateMIG(dev, parentUUID, extras)
	if !ok {
		return false
	}

	if len(groups) == 0 {
		return true
	}

	// the enumerated GPU instances are alive no matter what the GPM probe
	// says below: mark them seen first, so a transient probe failure cannot
	// get their retained samples mistaken for orphans
	for _, group := range groups {
		seenGIs[giKey(parentUUID, group.gi)] = true
	}

	support, ret := dev.GpmQueryDeviceSupport()

	switch {
	case isLifecycleError(ret):
		return b.extrasFailure("mig", "cannot probe the GPU instance activity support", ret)
	case ret != nvml.SUCCESS, support.IsSupportedDevice == 0:
		// pre-Hopper MIG (e.g. A100), or a driver without the GPM
		// interface: inventory and memory only
		return true
	}

	utilByGI := map[string]*collect.MIGUtilization{}

	for _, group := range groups {
		util, ok := b.gpmUtilization(dev, giKey(parentUUID, group.gi), group)
		if !ok {
			return false
		}

		utilByGI[strconv.Itoa(group.gi)] = util
	}

	// stamp the GPU instance's utilization onto each of its instances,
	// starting from this cycle's tail of extras.MIG (this parent's entries)
	for i := range extras.MIG {
		instance := &extras.MIG[i]
		if instance.ParentUUID != parentUUID {
			continue
		}

		instance.Utilization = utilByGI[instance.GPUInstanceID]
	}

	return true
}

// giKey is the GPM retention key of one GPU instance.
func giKey(parentUUID string, gi int) string {
	return parentUUID + "/" + strconv.Itoa(gi)
}

// enumerateMIG lists the parent's MIG devices into extras and groups them by
// GPU instance. Reports the groups and whether extras collection may
// continue.
//
//nolint:cyclop,funlen // one linear enumeration pass with per-getter fallbacks
func (b *Backend) enumerateMIG(
	dev nvml.Device,
	parentUUID string,
	extras *collect.Extras,
) ([]migGroup, bool) {
	maxMig, ret := dev.GetMaxMigDeviceCount()
	if ret != nvml.SUCCESS {
		return nil, b.extrasFailure("mig", "cannot count the MIG devices", ret)
	}

	byGI := map[int]*migGroup{}

	for migIdx := range maxMig {
		mig, ret := dev.GetMigDeviceHandleByIndex(migIdx)

		switch {
		case ret == nvml.ERROR_NOT_FOUND, ret == nvml.ERROR_INVALID_ARGUMENT:
			// an empty slot: profiles occupy varying slot counts, so sparse
			// indices are normal
			continue
		case ret != nvml.SUCCESS:
			if !b.extrasFailure("mig", "cannot get a MIG device handle", ret) {
				return nil, false
			}

			continue
		}

		uuid, ret := mig.GetUUID()
		if ret != nvml.SUCCESS {
			if !b.extrasFailure("mig", "cannot read a MIG device uuid", ret) {
				return nil, false
			}

			continue
		}

		gi, giRet := mig.GetGpuInstanceId()
		ci, ciRet := mig.GetComputeInstanceId()

		if giRet != nvml.SUCCESS || ciRet != nvml.SUCCESS {
			if !b.extrasFailure("mig", "cannot read a MIG device topology", firstError(giRet, ciRet)) {
				return nil, false
			}

			continue
		}

		profile, ret := migProfile(mig)
		if isLifecycleError(ret) {
			return nil, b.extrasFailure("mig", "cannot read a MIG device name", ret)
		}

		instance := collect.MIGInstance{
			ParentUUID:        parentUUID,
			UUID:              normalizeMIGUUID(uuid),
			GPUInstanceID:     strconv.Itoa(gi),
			ComputeInstanceID: strconv.Itoa(ci),
			Profile:           profile,
		}

		memory, ret := mig.GetMemoryInfo_v2()

		switch {
		case ret == nvml.SUCCESS:
			instance.Memory = &collect.MIGMemory{
				Total:    memory.Total,
				Used:     memory.Used,
				Free:     memory.Free,
				Reserved: memory.Reserved,
			}
		case isLifecycleError(ret):
			return nil, b.extrasFailure("mig", "cannot read a MIG device memory", ret)
		}

		extras.MIG = append(extras.MIG, instance)

		group := byGI[gi]
		if group == nil {
			group = &migGroup{gi: gi}
			byGI[gi] = group
		}

		group.members = append(group.members, instance.UUID+":"+instance.Profile+":"+instance.ComputeInstanceID)
	}

	groups := make([]migGroup, 0, len(byGI))

	for _, group := range byGI {
		sort.Strings(group.members)
		groups = append(groups, *group)
	}

	sort.Slice(groups, func(i, j int) bool { return groups[i].gi < groups[j].gi })

	return groups, true
}

// gpmUtilization computes one GPU instance's utilization from the retained
// previous sample and a fresh one, rotating the retention. A first sight (or
// an invalidated retention) seeds the state and reports nil values. Reports
// whether extras collection may continue.
//
//nolint:cyclop // the retention guards are a linear rule set
func (b *Backend) gpmUtilization(dev nvml.Device, key string, group migGroup) (*collect.MIGUtilization, bool) {
	fingerprint := strings.Join(group.members, ",")
	now := b.now()

	state := b.gpm[key]

	// a reused numeric GI id or an overgrown window invalidates the retained
	// sample: diffing across generations or a boundless window would produce
	// plausible-looking wrong values
	if state != nil && (state.fingerprint != fingerprint || now.Sub(state.taken) > gpmMaxWindow) {
		_ = b.api.gpmSampleFree(state.sample)
		delete(b.gpm, key)

		state = nil
	}

	// keep the window anchored under rapid scraping: serve the previous
	// computed values instead of rotating the sample
	if state != nil && now.Sub(state.taken) < gpmMinWindow {
		return state.last, true
	}

	sample, ret := b.api.gpmSampleAlloc()
	if ret != nvml.SUCCESS {
		return nil, b.extrasFailure("mig-gpm", "cannot allocate a GPM sample", ret)
	}

	if ret := b.api.gpmMigSampleGet(dev, group.gi, sample); ret != nvml.SUCCESS {
		_ = b.api.gpmSampleFree(sample)

		return nil, b.extrasFailure("mig-gpm", "cannot sample the GPU instance activity", ret)
	}

	if state == nil {
		if b.gpm == nil {
			b.gpm = map[string]*gpmState{}
		}

		b.gpm[key] = &gpmState{sample: sample, taken: now, fingerprint: fingerprint}

		return nil, true
	}

	util, ok := b.gpmMetrics(state.sample, sample)
	if !ok {
		// a lifecycle-class failure: markLost already released the retained
		// samples and emptied the map, so only the fresh local sample is
		// still ours to free
		_ = b.api.gpmSampleFree(sample)

		return nil, false
	}

	_ = b.api.gpmSampleFree(state.sample)
	state.sample = sample
	state.taken = now
	state.last = util

	return util, true
}

// gpmMetrics diffs two samples into utilization values. A metric whose
// per-metric status is not success, or whose value is not finite, stays nil.
// Reports whether extras collection may continue: a lifecycle-class failure,
// outer or per-metric, marks the backend lost and aborts.
//
//nolint:cyclop // one linear decode pass with per-metric classification
func (b *Backend) gpmMetrics(prev, cur nvml.GpmSample) (*collect.MIGUtilization, bool) {
	ids := []uint32{
		gpmMetricGraphicsUtil, gpmMetricSMUtil, gpmMetricSMOccupancy,
		gpmMetricAnyTensorUtil, gpmMetricPcieTxPerSec, gpmMetricPcieRxPerSec,
	}

	var metricsGet nvml.GpmMetricsGetType

	metricsGet.NumMetrics = uint32(len(ids)) //nolint:gosec // G115: len(ids) is a small constant
	metricsGet.Sample1 = prev
	metricsGet.Sample2 = cur

	for i, id := range ids {
		metricsGet.Metrics[i].MetricId = id
	}

	if ret := b.api.gpmMetricsGet(&metricsGet); ret != nvml.SUCCESS {
		return nil, b.extrasFailure("mig-gpm", "cannot compute the GPU instance activity metrics", ret)
	}

	util := &collect.MIGUtilization{}

	for i, id := range ids {
		metric := metricsGet.Metrics[i]

		metricRet := nvml.Return(metric.NvmlReturn) //nolint:gosec // G115: the field carries an nvmlReturn_t
		if metricRet != nvml.SUCCESS {
			if isLifecycleError(metricRet) {
				return nil, b.extrasFailure("mig-gpm", "GPU instance activity metric hit a lifecycle error", metricRet)
			}

			continue
		}

		if math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) {
			continue
		}

		switch id {
		case gpmMetricGraphicsUtil:
			util.GraphicsActivityRatio = new(metric.Value / 100)
		case gpmMetricSMUtil:
			util.SMActivityRatio = new(metric.Value / 100)
		case gpmMetricSMOccupancy:
			util.SMOccupancyRatio = new(metric.Value / 100)
		case gpmMetricAnyTensorUtil:
			util.TensorActivityRatio = new(metric.Value / 100)
		case gpmMetricPcieTxPerSec:
			util.PCIeTXBytesPerSecond = new(metric.Value * gpmMibMultiplier)
		case gpmMetricPcieRxPerSec:
			util.PCIeRXBytesPerSecond = new(metric.Value * gpmMibMultiplier)
		}
	}

	return util, true
}

// freeGPMSamples releases every retained GPM sample. It must run before an
// NVML shutdown: samples must never be freed after the library is torn down
// nor reused across a re-initialization.
func (b *Backend) freeGPMSamples() {
	for key, state := range b.gpm {
		_ = b.api.gpmSampleFree(state.sample)
		delete(b.gpm, key)
	}
}

// dropOrphanGPMStates frees the retained samples of GPU instances that
// disappeared (a MIG reconfiguration between two cycles).
func (b *Backend) dropOrphanGPMStates(seenGIs map[string]bool) {
	for key, state := range b.gpm {
		if seenGIs[key] {
			continue
		}

		_ = b.api.gpmSampleFree(state.sample)
		delete(b.gpm, key)
	}
}

// migProfile extracts the MIG profile ("1g.10gb") from the MIG device name
// ("NVIDIA H100 80GB HBM3 MIG 1g.10gb"), falling back to the full name when
// the shape is unknown, or to the absent token when unreadable. The return
// code is reported so the caller can classify lifecycle failures.
func migProfile(mig nvml.Device) (string, nvml.Return) {
	name, ret := mig.GetName()
	if ret != nvml.SUCCESS {
		return tokenNotAvailable, ret
	}

	if _, profile, found := strings.Cut(name, " MIG "); found && profile != "" {
		return profile, nvml.SUCCESS
	}

	return name, nvml.SUCCESS
}

// normalizeMIGUUID normalizes a MIG device uuid the way GPU uuids are
// normalized on every label: lowercase, without the type prefix.
func normalizeMIGUUID(raw string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "mig-")
}

// firstError picks the first non-success return of a pair, for error paths
// that fold two getters.
func firstError(a, other nvml.Return) nvml.Return {
	if a != nvml.SUCCESS {
		return a
	}

	return other
}

// migAppID renders a per-process MIG instance id label value: NVML reports
// non-MIG processes with the invalid-id sentinel, which renders as an empty
// label.
func migAppID(id uint32) string {
	if id == migInvalidID {
		return ""
	}

	return strconv.FormatUint(uint64(id), 10)
}
