//go:build (linux && cgo) || windows

package nvmlnative

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvmlnative/nvml"
)

// The GPM metric ids this backend samples, from the NVML metric catalog.
// Activity metrics report percentages (0-100), the PCIe and C2C metrics MiB/s.
const (
	gpmMetricGraphicsUtil    = 1
	gpmMetricSMUtil          = 2
	gpmMetricSMOccupancy     = 3
	gpmMetricIntegerUtil     = 4
	gpmMetricAnyTensorUtil   = 5
	gpmMetricDramBwUtil      = 10
	gpmMetricFP64Util        = 11
	gpmMetricFP32Util        = 12
	gpmMetricFP16Util        = 13
	gpmMetricPcieTxPerSec    = 20
	gpmMetricPcieRxPerSec    = 21
	gpmMetricC2CTotalTxPerSc = 100
	gpmMetricC2CTotalRxPerSc = 101
)

// gpmMibMultiplier converts the GPM PCIe metrics (documented as MiB/sec, a
// different unit from the whole-GPU KB/s throughput call) to bytes/s.
const gpmMibMultiplier = 1024 * 1024

// gpmMinWindow keeps the sampling window above NVML's >100ms minimum for
// nvmlGpmMetricsGet when scrapers hit the exporter in quick succession: below
// it the retained sample is not rotated and the previous computed values are
// served again.
const gpmMinWindow = 150 * time.Millisecond

// gpmMaxWindow bounds how stale a retained sample may grow (a scraper that
// vanished for a long time): beyond it the sample is reseeded and the cycle
// emits no utilization, exactly like a first sight.
const gpmMaxWindow = 10 * time.Minute

// migInvalidID is how NVML reports "not a MIG process" in the per-process
// instance id fields.
const migInvalidID = 0xFFFFFFFF

type gpmSampleResult struct {
	mig   *collect.MIGUtilization
	c2cTX *float64
	c2cRX *float64
}

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
	last *gpmSampleResult
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
func (b *Backend) collectMIG(
	dev device,
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

	// GPM is an all-or-nothing group: sampling allocates a handle that must be
	// freed through a second export, so a driver providing only part of the set
	// would either leak or crash on teardown.
	if !b.gpmAvailable() {
		return true
	}

	return b.collectMIGUtilization(dev, parentUUID, extras, groups)
}

// collectMIGUtilization samples per-GPU-instance activity and stamps it onto
// the instances enumerated for this parent.
func (b *Backend) collectMIGUtilization(
	dev device,
	parentUUID string,
	extras *collect.Extras,
	groups []migGroup,
) bool {
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
	dev device,
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
				Reserved: new(memory.Reserved),
			}
		case ret == nvml.ERROR_FUNCTION_NOT_FOUND:
			legacy, lret := mig.GetMemoryInfo()
			if lret == nvml.SUCCESS {
				instance.Memory = &collect.MIGMemory{
					Total: legacy.Total,
					Used:  legacy.Used,
					Free:  legacy.Free,
				}
			} else if isLifecycleError(lret) {
				return nil, b.extrasFailure("mig", "cannot read a MIG device memory", lret)
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

// collectGPM samples whole-GPU GPM activity ratios on non-MIG GPUs that
// support GPM. MIG-enabled GPUs report per-instance GPM under collectMIG
// instead. Reports whether extras collection may continue.
func (b *Backend) collectGPM(
	dev device,
	uuid string,
	extras *collect.Extras,
	seenGIs map[string]bool,
	c2cActive bool,
) bool {
	supported, ok := b.canSampleWholeGPUGPM(dev, uuid, seenGIs)
	if !ok || !supported {
		return ok
	}

	var c2cIDs []uint32
	if c2cActive {
		c2cIDs = []uint32{gpmMetricC2CTotalTxPerSc, gpmMetricC2CTotalRxPerSc}
	}

	res, ok := b.gpmSample(
		"gpm", "GPU", uuid, uuid,
		func(sample nvml.GpmSample) nvml.Return {
			return b.api.gpmSampleGet(dev.raw(), sample)
		},
		baseGPMMetricIDs(), c2cIDs,
	)
	if !ok || res == nil || res.mig == nil {
		return ok
	}

	appendWholeGPUGPM(uuid, res, extras)

	return true
}

func baseGPMMetricIDs() []uint32 {
	return []uint32{
		gpmMetricGraphicsUtil, gpmMetricSMUtil, gpmMetricSMOccupancy,
		gpmMetricIntegerUtil, gpmMetricAnyTensorUtil, gpmMetricDramBwUtil,
		gpmMetricFP64Util, gpmMetricFP32Util, gpmMetricFP16Util,
		gpmMetricPcieTxPerSec, gpmMetricPcieRxPerSec,
	}
}

func appendWholeGPUGPM(uuid string, res *gpmSampleResult, extras *collect.Extras) {
	util := res.mig
	extras.GPM = append(extras.GPM, collect.GPUUtilization{
		UUID:                          uuid,
		GraphicsActivityRatio:         util.GraphicsActivityRatio,
		SMActivityRatio:               util.SMActivityRatio,
		SMOccupancyRatio:              util.SMOccupancyRatio,
		TensorActivityRatio:           util.TensorActivityRatio,
		DRAMBandwidthUtilizationRatio: util.DRAMBandwidthUtilizationRatio,
		IntegerActivityRatio:          util.IntegerActivityRatio,
		FP64ActivityRatio:             util.FP64ActivityRatio,
		FP32ActivityRatio:             util.FP32ActivityRatio,
		FP16ActivityRatio:             util.FP16ActivityRatio,
		C2CTXBytesPerSecond:           res.c2cTX,
		C2CRXBytesPerSecond:           res.c2cRX,
	})

	if util.PCIeTXBytesPerSecond != nil && util.PCIeRXBytesPerSecond != nil {
		extras.PCIe = append(extras.PCIe, collect.PCIeThroughput{
			UUID:             uuid,
			TXBytesPerSecond: *util.PCIeTXBytesPerSecond,
			RXBytesPerSecond: *util.PCIeRXBytesPerSecond,
		})
	}
}

func (b *Backend) canSampleWholeGPUGPM(
	dev device,
	uuid string,
	seenGIs map[string]bool,
) (bool, bool) {
	mode, _, ret := dev.GetMigMode()

	switch {
	case ret == nvml.SUCCESS && mode == nvml.DEVICE_MIG_ENABLE:
		return false, true
	case ret != nvml.SUCCESS && ret != nvml.ERROR_NOT_SUPPORTED && ret != nvml.ERROR_FUNCTION_NOT_FOUND:
		return false, b.extrasFailure("gpm", "cannot read the MIG mode", ret)
	}

	seenGIs[uuid] = true

	if !b.wholeGPUGpmAvailable() {
		return false, true
	}

	support, ret := dev.GpmQueryDeviceSupport()

	switch {
	case isLifecycleError(ret):
		return false, b.extrasFailure("gpm", "cannot probe the GPU activity support", ret)
	case ret != nvml.SUCCESS, support.IsSupportedDevice == 0:
		return false, true
	default:
		return true, true
	}
}

// gpmUtilization computes one GPU instance's utilization from the retained
// previous sample and a fresh one, rotating the retention. A first sight (or
// an invalidated retention) seeds the state and reports nil values. Reports
// whether extras collection may continue.
func (b *Backend) gpmUtilization(dev device, key string, group migGroup) (*collect.MIGUtilization, bool) {
	res, ok := b.gpmSample(
		"mig-gpm", "GPU instance", key, strings.Join(group.members, ","),
		func(sample nvml.GpmSample) nvml.Return {
			return b.api.gpmMigSampleGet(dev.raw(), group.gi, sample)
		},
		baseGPMMetricIDs(), nil,
	)
	if !ok || res == nil {
		return nil, ok
	}

	return res.mig, true
}

// gpmSample runs the two-sample GPM retention state machine for either a whole
// GPU or a MIG GPU instance.
//
//nolint:cyclop // the retention guards are a linear rule set
func (b *Backend) gpmSample(
	family string,
	target string,
	key string,
	fingerprint string,
	take func(nvml.GpmSample) nvml.Return,
	baseIDs []uint32,
	extraIDs []uint32,
) (*gpmSampleResult, bool) {
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
		return nil, b.extrasFailure(family, "cannot allocate a GPM sample", ret)
	}

	if ret := take(sample); ret != nvml.SUCCESS {
		_ = b.api.gpmSampleFree(sample)

		return nil, b.extrasFailure(family, "cannot sample the "+target+" activity", ret)
	}

	if state == nil {
		if b.gpm == nil {
			b.gpm = map[string]*gpmState{}
		}

		b.gpm[key] = &gpmState{sample: sample, taken: now, fingerprint: fingerprint}

		return nil, true
	}

	res, errMsg, fatalRet := b.gpmMetrics(family, target, state.sample, sample, baseIDs, extraIDs)
	if fatalRet != nvml.SUCCESS {
		// Free the fresh local sample before extrasFailure triggers markLost
		// and nvmlShutdown, so no sample is ever freed after NVML teardown.
		_ = b.api.gpmSampleFree(sample)

		return nil, b.extrasFailure(family, errMsg, fatalRet)
	}

	_ = b.api.gpmSampleFree(state.sample)
	state.sample = sample
	state.taken = now
	state.last = res

	return res, true
}

// gpmMetrics diffs two samples into utilization values. Base IDs (R535+ GPU
// and MIG activity + precision pipes + PCIe) run first; C2C IDs (100, 101)
// run in a separate optional gpmMetricsGet on the same sample pair so a driver
// lacking C2C GPM never breaks the base metrics. Returns a non-SUCCESS
// nvml.Return when a lifecycle-class error occurred so gpmSample can free the
// fresh local sample before markLost tears down NVML.
func (b *Backend) gpmMetrics(
	family string,
	target string,
	prev, cur nvml.GpmSample,
	baseIDs []uint32,
	extraIDs []uint32,
) (*gpmSampleResult, string, nvml.Return) {
	res := &gpmSampleResult{mig: &collect.MIGUtilization{}}

	populated, errMsg, fatalRet := b.gpmMetricsBatch(family, target, prev, cur, baseIDs, res, false)
	if fatalRet != nvml.SUCCESS {
		return nil, errMsg, fatalRet
	}

	if !populated {
		return nil, "", nvml.SUCCESS
	}

	if len(extraIDs) > 0 {
		_, errMsg, fatalRet := b.gpmMetricsBatch(family, target, prev, cur, extraIDs, res, true)
		if fatalRet != nvml.SUCCESS {
			return nil, errMsg, fatalRet
		}
	}

	return res, "", nvml.SUCCESS
}

func (b *Backend) gpmMetricsBatch(
	family string,
	target string,
	prev, cur nvml.GpmSample,
	ids []uint32,
	res *gpmSampleResult,
	optional bool,
) (bool, string, nvml.Return) {
	var metricsGet nvml.GpmMetricsGetType

	metricsGet.NumMetrics = uint32(len(ids)) //nolint:gosec // G115: len(ids) is a small constant
	metricsGet.Sample1 = prev
	metricsGet.Sample2 = cur

	for i, id := range ids {
		metricsGet.Metrics[i].MetricId = id
	}

	if ret := b.api.gpmMetricsGet(&metricsGet); ret != nvml.SUCCESS {
		if isLifecycleError(ret) {
			return false, "cannot compute the " + target + " activity metrics", ret
		}

		if optional {
			b.warnOnce(family+"-extended", "cannot compute extended "+target+" activity metrics", ret)
		} else {
			b.warnOnce(family, "cannot compute the "+target+" activity metrics", ret)
		}

		return false, "", nvml.SUCCESS
	}

	for i, id := range ids {
		metric := metricsGet.Metrics[i]

		metricRet := nvml.Return(metric.NvmlReturn) //nolint:gosec // G115: the field carries an nvmlReturn_t
		if metricRet != nvml.SUCCESS {
			if isLifecycleError(metricRet) {
				return false, target + " activity metric hit a lifecycle error", metricRet
			}

			continue
		}

		if math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) {
			continue
		}

		assignGPMMetric(id, metric.Value, res)
	}

	return true, "", nvml.SUCCESS
}

//nolint:cyclop // flat GPM metric-ID dispatch
func assignGPMMetric(id uint32, value float64, res *gpmSampleResult) {
	util := res.mig

	switch id {
	case gpmMetricGraphicsUtil:
		util.GraphicsActivityRatio = new(value / 100)
	case gpmMetricSMUtil:
		util.SMActivityRatio = new(value / 100)
	case gpmMetricSMOccupancy:
		util.SMOccupancyRatio = new(value / 100)
	case gpmMetricIntegerUtil:
		util.IntegerActivityRatio = new(value / 100)
	case gpmMetricAnyTensorUtil:
		util.TensorActivityRatio = new(value / 100)
	case gpmMetricDramBwUtil:
		util.DRAMBandwidthUtilizationRatio = new(value / 100)
	case gpmMetricFP64Util:
		util.FP64ActivityRatio = new(value / 100)
	case gpmMetricFP32Util:
		util.FP32ActivityRatio = new(value / 100)
	case gpmMetricFP16Util:
		util.FP16ActivityRatio = new(value / 100)
	case gpmMetricPcieTxPerSec:
		util.PCIeTXBytesPerSecond = new(value * gpmMibMultiplier)
	case gpmMetricPcieRxPerSec:
		util.PCIeRXBytesPerSecond = new(value * gpmMibMultiplier)
	case gpmMetricC2CTotalTxPerSc:
		res.c2cTX = new(value * gpmMibMultiplier)
	case gpmMetricC2CTotalRxPerSc:
		res.c2cRX = new(value * gpmMibMultiplier)
	}
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
func migProfile(mig device) (string, nvml.Return) {
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
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) >= 4 && strings.EqualFold(trimmed[:4], "mig-") {
		trimmed = trimmed[4:]
	}

	return strings.ToLower(trimmed)
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

// gpmAvailable reports whether the driver exports every GPM entry point the
// MIG sampling path uses. They are probed as a set because the family allocates
// a sample through one export and releases it through another.
func (b *Backend) gpmAvailable() bool {
	return b.avail.Load().hasAll(
		"nvmlGpmSampleAlloc",
		"nvmlGpmSampleFree",
		"nvmlGpmMigSampleGet",
		"nvmlGpmMetricsGet",
	)
}

// wholeGPUGpmAvailable reports whether the driver exports every GPM entry
// point the whole-GPU sampling path uses.
func (b *Backend) wholeGPUGpmAvailable() bool {
	return b.avail.Load().hasAll(
		"nvmlGpmSampleAlloc",
		"nvmlGpmSampleFree",
		"nvmlGpmSampleGet",
		"nvmlGpmMetricsGet",
	)
}
