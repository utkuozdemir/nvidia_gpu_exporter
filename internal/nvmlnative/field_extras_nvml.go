//go:build (linux && cgo) || windows

package nvmlnative

import (
	"math"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvmlnative/nvml"
)

const (
	// maxLinkScopeCount bounds per-link GetFieldValues batches against bogus
	// driver counts. Current Hopper/Blackwell GPUs have at most 18 NVLinks.
	maxLinkScopeCount = 64

	// nvmlNVLinkStateActive matches NVML_NVLINK_STATE_ACTIVE (0 = inactive,
	// 1 = active, 2 = sleep).
	nvmlNVLinkStateActive = 1

	nsPerSecond = 1e9
)

type extraFieldSlots struct {
	pcieReplay    int
	pcieRollover  int
	pcieCorr      int
	pcieNonFatal  int
	pcieFatal     int
	pcieL0Recov   int
	nvlinkCount   int
	nvswitchCount int
	nvlinkTX      int
	nvlinkRX      int
	c2cCount      int
	perfThermal   int
	perfBoard     int
	perfReliab    int
	perfBase      int
	tlimShut      int
	tlimSlow      int
	tlimMemMax    int
	tlimGPUMax    int
}

func newExtraFieldSlots() extraFieldSlots {
	return extraFieldSlots{
		pcieReplay: -1, pcieRollover: -1,
		pcieCorr: -1, pcieNonFatal: -1, pcieFatal: -1, pcieL0Recov: -1,
		nvlinkCount: -1, nvswitchCount: -1, nvlinkTX: -1, nvlinkRX: -1,
		c2cCount:    -1,
		perfThermal: -1, perfBoard: -1, perfReliab: -1, perfBase: -1,
		tlimShut: -1, tlimSlow: -1, tlimMemMax: -1, tlimGPUMax: -1,
	}
}

func buildExtraFieldBatch(opts CollectOptions) ([]nvml.FieldValue, extraFieldSlots) {
	slots := newExtraFieldSlots()
	values := make([]nvml.FieldValue, 0, 19)

	add := func(id, scope uint32) int {
		idx := len(values)
		values = append(values, nvml.FieldValue{FieldId: id, ScopeId: scope})

		return idx
	}

	if opts.PCIeReplays {
		slots.pcieReplay = add(nvml.FI_DEV_PCIE_REPLAY_COUNTER, 0)
		slots.pcieRollover = add(nvml.FI_DEV_PCIE_REPLAY_ROLLOVER_COUNTER, 0)
	}

	if opts.PCIeErrors {
		slots.pcieCorr = add(nvml.FI_DEV_PCIE_COUNT_CORRECTABLE_ERRORS, 0)
		slots.pcieNonFatal = add(nvml.FI_DEV_PCIE_COUNT_NON_FATAL_ERROR, 0)
		slots.pcieFatal = add(nvml.FI_DEV_PCIE_COUNT_FATAL_ERROR, 0)
		slots.pcieL0Recov = add(nvml.FI_DEV_PCIE_L0_TO_RECOVERY_COUNTER, 0)
	}

	if opts.NVLink {
		slots.nvlinkCount = add(nvml.FI_DEV_NVLINK_LINK_COUNT, 0)
		slots.nvswitchCount = add(nvml.FI_DEV_NVSWITCH_CONNECTED_LINK_COUNT, 0)
		slots.nvlinkTX = add(nvml.FI_DEV_NVLINK_THROUGHPUT_DATA_TX, math.MaxUint32)
		slots.nvlinkRX = add(nvml.FI_DEV_NVLINK_THROUGHPUT_DATA_RX, math.MaxUint32)
	}

	if opts.C2CLinks {
		slots.c2cCount = add(nvml.FI_DEV_C2C_LINK_COUNT, 0)
	}

	if opts.PerfPolicyViolations {
		slots.perfThermal = add(nvml.FI_DEV_PERF_POLICY_THERMAL, 0)
		slots.perfBoard = add(nvml.FI_DEV_PERF_POLICY_BOARD_LIMIT, 0)
		slots.perfReliab = add(nvml.FI_DEV_PERF_POLICY_RELIABILITY, 0)
		slots.perfBase = add(nvml.FI_DEV_PERF_POLICY_TOTAL_BASE_CLOCKS, 0)
	}

	if opts.TemperatureTLimitThresholds {
		slots.tlimShut = add(nvml.FI_DEV_TEMPERATURE_SHUTDOWN_TLIMIT, 0)
		slots.tlimSlow = add(nvml.FI_DEV_TEMPERATURE_SLOWDOWN_TLIMIT, 0)
		slots.tlimMemMax = add(nvml.FI_DEV_TEMPERATURE_MEM_MAX_TLIMIT, 0)
		slots.tlimGPUMax = add(nvml.FI_DEV_TEMPERATURE_GPU_MAX_TLIMIT, 0)
	}

	return values, slots
}

// collectFieldValueExtras consolidates all device-scoped FI_DEV_* extras into
// a single GetFieldValues call per GPU, followed by at most one per-link
// GetFieldValues call for NVLink active-link state when NVLink links are
// present. Returns (hasC2C, continueExtras).
func (b *Backend) collectFieldValueExtras(
	dev device,
	uuid string,
	opts CollectOptions,
	extras *collect.Extras,
) (bool, bool) {
	values, slots := buildExtraFieldBatch(opts)
	if len(values) == 0 {
		return false, true
	}

	ok, supported := b.queryExtraFieldValues(dev, "field-extras", "cannot read NVML field-value extras", values)
	if !ok || !supported {
		return false, ok
	}

	if !b.decodePCIeReplays(uuid, values, slots, extras) ||
		!b.decodePCIeErrors(uuid, values, slots, extras) ||
		!b.decodePerfPolicies(uuid, values, slots, extras) ||
		!b.decodeTLimitThresholds(uuid, values, slots, extras) {
		return false, false
	}

	return b.decodeInterconnectExtras(dev, uuid, opts, values, slots, extras)
}

func (b *Backend) decodePCIeReplays(
	uuid string,
	values []nvml.FieldValue,
	slots extraFieldSlots,
	extras *collect.Extras,
) bool {
	if slots.pcieReplay < 0 {
		return true
	}

	replays, ok := b.decodeExtraFieldCounter("pcie-replays", values[slots.pcieReplay])
	if !ok {
		return false
	}

	rollovers, ok := b.decodeExtraFieldCounter("pcie-replays", values[slots.pcieRollover])
	if !ok {
		return false
	}

	if replays != nil || rollovers != nil {
		extras.PCIeReplays = append(extras.PCIeReplays, collect.PCIeReplayCounter{
			UUID:      uuid,
			Replays:   replays,
			Rollovers: rollovers,
		})
	}

	return true
}

func (b *Backend) decodePCIeErrors(
	uuid string,
	values []nvml.FieldValue,
	slots extraFieldSlots,
	extras *collect.Extras,
) bool {
	if slots.pcieCorr < 0 {
		return true
	}

	corr, ok := b.decodeExtraFieldCounter("pcie-errors", values[slots.pcieCorr])
	if !ok {
		return false
	}

	nonFatal, ok := b.decodeExtraFieldCounter("pcie-errors", values[slots.pcieNonFatal])
	if !ok {
		return false
	}

	fatal, ok := b.decodeExtraFieldCounter("pcie-errors", values[slots.pcieFatal])
	if !ok {
		return false
	}

	l0Recov, ok := b.decodeExtraFieldCounter("pcie-errors", values[slots.pcieL0Recov])
	if !ok {
		return false
	}

	if corr != nil || nonFatal != nil || fatal != nil || l0Recov != nil {
		extras.PCIeErrors = append(extras.PCIeErrors, collect.PCIeErrorCounter{
			UUID:         uuid,
			Correctable:  corr,
			NonFatal:     nonFatal,
			Fatal:        fatal,
			L0ToRecovery: l0Recov,
		})
	}

	return true
}

func decodeFourFloatFields(
	values []nvml.FieldValue,
	idxs [4]int,
	decode func(nvml.FieldValue) (*float64, bool),
) ([4]*float64, bool, bool) {
	var (
		out      [4]*float64
		anyValue bool
	)

	for pos, idx := range idxs {
		val, ok := decode(values[idx])
		if !ok {
			return out, false, false
		}

		if val != nil {
			out[pos] = val
			anyValue = true
		}
	}

	return out, anyValue, true
}

func (b *Backend) decodePerfPolicies(
	uuid string,
	values []nvml.FieldValue,
	slots extraFieldSlots,
	extras *collect.Extras,
) bool {
	if slots.perfThermal < 0 {
		return true
	}

	vals, anyValue, ok := decodeFourFloatFields(
		values,
		[4]int{slots.perfThermal, slots.perfBoard, slots.perfReliab, slots.perfBase},
		b.decodePerfPolicySec,
	)
	if !ok {
		return false
	}

	if anyValue {
		extras.PerfPolicyViolations = append(extras.PerfPolicyViolations, collect.PerfPolicyViolation{
			UUID:            uuid,
			Thermal:         vals[0],
			BoardLimit:      vals[1],
			Reliability:     vals[2],
			TotalBaseClocks: vals[3],
		})
	}

	return true
}

func (b *Backend) decodeTLimitThresholds(
	uuid string,
	values []nvml.FieldValue,
	slots extraFieldSlots,
	extras *collect.Extras,
) bool {
	if slots.tlimShut < 0 {
		return true
	}

	vals, anyValue, ok := decodeFourFloatFields(
		values,
		[4]int{slots.tlimShut, slots.tlimSlow, slots.tlimMemMax, slots.tlimGPUMax},
		b.decodeTLimitField,
	)
	if !ok {
		return false
	}

	if anyValue {
		extras.TemperatureTLimitThresholds = append(
			extras.TemperatureTLimitThresholds,
			collect.TemperatureTLimitThresholds{
				UUID:      uuid,
				Shutdown:  vals[0],
				Slowdown:  vals[1],
				MemoryMax: vals[2],
				GPUMax:    vals[3],
			},
		)
	}

	return true
}

func (b *Backend) decodeInterconnectExtras(
	dev device,
	uuid string,
	opts CollectOptions,
	values []nvml.FieldValue,
	slots extraFieldSlots,
	extras *collect.Extras,
) (bool, bool) {
	nvlinkCount, c2cCount, ok := b.decodeLinkCounts(values, slots)
	if !ok {
		return false, false
	}

	nvLinks := clampLinkCount(nvlinkCount)
	c2cLinks := clampLinkCount(c2cCount)

	if opts.C2CLinks && c2cLinks > 0 {
		idx := len(extras.C2CLinks)
		extras.C2CLinks = append(extras.C2CLinks, collect.C2CLinkStatus{
			UUID:  uuid,
			Links: *c2cCount,
		})

		activeLinks, ok := b.collectActiveLinks(dev, "c2c", nvml.FI_DEV_C2C_LINK_GET_STATUS, c2cLinks, 1)
		if !ok {
			return false, false
		}

		extras.C2CLinks[idx].ActiveLinks = activeLinks
	}

	if nvLinks > 0 && !b.collectNVLinkSection(dev, uuid, values, slots, nvlinkCount, nvLinks, extras) {
		return false, false
	}

	return c2cLinks > 0, true
}

func (b *Backend) collectNVLinkSection(
	dev device,
	uuid string,
	values []nvml.FieldValue,
	slots extraFieldSlots,
	nvlinkCount *uint64,
	nvLinks uint32,
	extras *collect.Extras,
) bool {
	idx := len(extras.NVLink)
	if !b.appendNVLinkExtras(uuid, values, slots, nvlinkCount, extras) {
		return false
	}

	activeLinks, ok := b.collectActiveLinks(
		dev, "nvlink", nvml.FI_DEV_NVLINK_GET_STATE, nvLinks, nvmlNVLinkStateActive,
	)
	if !ok {
		return false
	}

	// HGX NVSwitch systems (verified on A100 SXM4 and H100 SXM5) return
	// NOT_SUPPORTED for FI_DEV_NVLINK_GET_STATE while serving per-link
	// state through nvmlDeviceGetNvLinkState.
	if activeLinks == nil {
		activeLinks, ok = b.collectNVLinkActiveStateDirect(dev, nvLinks)
		if !ok {
			return false
		}
	}

	extras.NVLink[idx].ActiveLinks = activeLinks

	recovery, recoveryFailed, ok := b.collectNVLinkRecoveryCounters(dev, nvLinks)
	if !ok {
		return false
	}

	extras.NVLink[idx].RecoveryEvents = recovery
	extras.NVLink[idx].RecoveryFailedEvents = recoveryFailed

	return true
}

func (b *Backend) decodeLinkCounts(values []nvml.FieldValue, slots extraFieldSlots) (*uint64, *uint64, bool) {
	nvlinkCount, ok := b.decodeNVLinkCount(values, slots)
	if !ok {
		return nil, nil, false
	}

	var c2cCount *uint64

	if slots.c2cCount >= 0 {
		c2cCount, ok = b.decodeExtraFieldCounter("c2c", values[slots.c2cCount])
		if !ok {
			return nil, nil, false
		}
	}

	return nvlinkCount, c2cCount, true
}

func (b *Backend) decodeNVLinkCount(values []nvml.FieldValue, slots extraFieldSlots) (*uint64, bool) {
	if slots.nvlinkCount < 0 {
		return nil, true
	}

	nvlinkCount, ok := b.decodeExtraFieldCounter("nvlink", values[slots.nvlinkCount])
	if !ok {
		return nil, false
	}

	if (nvlinkCount != nil && *nvlinkCount > 0) || slots.nvswitchCount < 0 {
		return nvlinkCount, true
	}

	nvswitchCount, ok := b.decodeExtraFieldCounter("nvlink", values[slots.nvswitchCount])
	if !ok {
		return nil, false
	}

	if nvswitchCount != nil && *nvswitchCount > 0 {
		return nvswitchCount, true
	}

	return nvlinkCount, true
}

func (b *Backend) appendNVLinkExtras(
	uuid string,
	values []nvml.FieldValue,
	slots extraFieldSlots,
	nvlinkCount *uint64,
	extras *collect.Extras,
) bool {
	txKiB, ok := b.decodeExtraFieldCounter("nvlink", values[slots.nvlinkTX])
	if !ok {
		return false
	}

	rxKiB, ok := b.decodeExtraFieldCounter("nvlink", values[slots.nvlinkRX])
	if !ok {
		return false
	}

	extras.NVLink = append(extras.NVLink, collect.NVLinkThroughput{
		UUID:    uuid,
		TXBytes: scaleCounterKiB(txKiB),
		RXBytes: scaleCounterKiB(rxKiB),
		Links:   nvlinkCount,
	})

	return true
}

func clampLinkCount(count *uint64) uint32 {
	if count == nil || *count == 0 {
		return 0
	}

	if *count > maxLinkScopeCount {
		return maxLinkScopeCount
	}

	return uint32(*count)
}

func (b *Backend) collectActiveLinks(
	dev device,
	family string,
	fieldID uint32,
	linkCount uint32,
	activeValue uint64,
) (*uint64, bool) {
	values := make([]nvml.FieldValue, linkCount)
	for link := range linkCount {
		values[link] = nvml.FieldValue{FieldId: fieldID, ScopeId: link}
	}

	ok, supported := b.queryExtraFieldValues(dev, family+"-state", "cannot read per-link state", values)
	if !ok || !supported {
		return nil, ok
	}

	var (
		activeSum   uint64
		activeCount int
	)

	for _, fv := range values {
		state, ok := b.decodeExtraFieldCounter(family, fv)
		if !ok {
			return nil, false
		}

		if state != nil {
			activeCount++

			if *state == activeValue {
				activeSum++
			}
		}
	}

	// Only publish the active-link count when every link reported a reading,
	// so a transient per-link error never understates active links.
	if activeCount != len(values) {
		return nil, true
	}

	return &activeSum, true
}

func (b *Backend) collectNVLinkActiveStateDirect(dev device, nvLinks uint32) (*uint64, bool) {
	var (
		activeSum   uint64
		activeCount uint32
	)

	for link := range nvLinks {
		state, ret := dev.GetNvLinkState(int(link))

		//nolint:exhaustive // all other codes are unexpected failures
		switch ret {
		case nvml.SUCCESS:
			activeCount++

			if state == nvml.FEATURE_ENABLED {
				activeSum++
			}
		case nvml.ERROR_NOT_SUPPORTED, nvml.ERROR_FUNCTION_NOT_FOUND, nvml.ERROR_NOT_FOUND,
			nvml.ERROR_NO_DATA, nvml.ERROR_INVALID_ARGUMENT:
			return nil, true
		default:
			return nil, b.extrasFailure("nvlink-state", "cannot read NVLink state", ret)
		}
	}

	if activeCount != nvLinks {
		return nil, true
	}

	return &activeSum, true
}

// collectNVLinkRecoveryCounters queries the NVLink 5 recovery-event counters
// per link (ScopeId 0..nvLinks-1, matching nvml.h for field IDs 201..225) and
// sums each family when every link reports a reading.
func (b *Backend) collectNVLinkRecoveryCounters(dev device, nvLinks uint32) (*uint64, *uint64, bool) {
	values := make([]nvml.FieldValue, 2*nvLinks)
	for link := range nvLinks {
		values[link] = nvml.FieldValue{
			FieldId: nvml.FI_DEV_NVLINK_COUNT_LINK_RECOVERY_EVENTS,
			ScopeId: link,
		}
		values[nvLinks+link] = nvml.FieldValue{
			FieldId: nvml.FI_DEV_NVLINK_COUNT_LINK_RECOVERY_FAILED_EVENTS,
			ScopeId: link,
		}
	}

	ok, supported := b.queryExtraFieldValues(dev, "nvlink-recovery", "cannot read NVLink recovery counters", values)
	if !ok || !supported {
		return nil, nil, ok
	}

	recovery, ok := b.sumPerLinkCounters("nvlink", values[:nvLinks])
	if !ok {
		return nil, nil, false
	}

	recoveryFailed, ok := b.sumPerLinkCounters("nvlink", values[nvLinks:])
	if !ok {
		return nil, nil, false
	}

	return recovery, recoveryFailed, true
}

func (b *Backend) sumPerLinkCounters(family string, values []nvml.FieldValue) (*uint64, bool) {
	var (
		sum   uint64
		count int
	)

	for _, fv := range values {
		val, ok := b.decodeExtraFieldCounter(family, fv)
		if !ok {
			return nil, false
		}

		if val != nil {
			count++
			sum += *val
		}
	}

	if count != len(values) {
		return nil, true
	}

	return &sum, true
}

func (b *Backend) decodePerfPolicySec(fieldValue nvml.FieldValue) (*float64, bool) {
	ns, ok := b.decodeExtraFieldCounter("perf-policy", fieldValue)
	if !ok || ns == nil {
		return nil, ok
	}

	return new(float64(*ns) / nsPerSecond), true
}

// decodeTLimitField decodes one signed float64 from a FieldValue entry for
// signed T.Limit temperature thresholds.
func (b *Backend) decodeTLimitField(fieldValue nvml.FieldValue) (*float64, bool) {
	fret := nvml.Return(fieldValue.NvmlReturn) //nolint:gosec // G115: the field carries an nvmlReturn_t

	//nolint:exhaustive // all other codes are unexpected failures
	switch fret {
	case nvml.SUCCESS:
		val, ok := decodeFieldValue(fieldValue)
		if !ok || math.IsNaN(val) || math.IsInf(val, 0) {
			return nil, true
		}

		return &val, true
	case nvml.ERROR_NOT_SUPPORTED, nvml.ERROR_FUNCTION_NOT_FOUND, nvml.ERROR_NOT_FOUND,
		nvml.ERROR_NO_DATA, nvml.ERROR_INVALID_ARGUMENT:
		return nil, true
	default:
		return nil, b.extrasFailure("tlimit-thresholds", "cannot read an NVML field value", fret)
	}
}
