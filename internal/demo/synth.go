package demo

import (
	"crypto/sha256"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"time"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
)

// demoRand is the seeded source behind every synthesized draw. One source,
// advanced per draw: with a fixed seed and a fixed cycle sequence the values
// reproduce (the golden test does exactly one cycle).
type demoRand struct {
	src *rand.Rand
}

func newDemoRand(seed *int64) *demoRand {
	value := time.Now().UnixNano()
	if seed != nil {
		value = *seed
	}

	//nolint:gosec // not cryptographic: deterministic fake data
	return &demoRand{src: rand.New(rand.NewSource(value))}
}

// draw returns a uniform value from the range.
func (r *demoRand) draw(rng rangeCfg) float64 {
	return rng.Min + r.src.Float64()*(rng.Max-rng.Min)
}

// synthEnergy integrates each GPU's power draw over the injected clock into
// the cumulative energy counter, trapezoidally between the previous and
// current samples. The power source is the cycle's own table value when the
// query included the field, so the counter's rate matches the displayed
// power; the configured fallback keeps the counter alive when an explicit
// field selection excluded it (the extra never depends on the public
// schema). The first sample of a GPU emits zero, a documented convention.
func (b *Backend) synthEnergy(
	uuids []string,
	power map[string]float64,
	extras *extrasConfig,
	now time.Time,
	reading *collect.Reading,
) {
	seen := map[string]bool{}

	for _, uuid := range uuids {
		seen[uuid] = true

		watts, ok := power[uuid]
		if !ok {
			watts = extras.EnergyFallbackPowerWatts
		}

		state := b.energy[uuid]
		if state == nil {
			state = &energyState{}
			b.energy[uuid] = state
		} else {
			elapsed := now.Sub(state.lastAt).Seconds()
			if elapsed > 0 {
				state.joules += (state.lastPower + watts) / 2 * elapsed
			}
		}

		state.lastPower = watts
		state.lastAt = now

		reading.Extras.Energy = append(reading.Extras.Energy, collect.EnergyCounter{
			UUID:   uuid,
			Joules: state.joules,
		})
	}

	// a GPU that left the config stops integrating and disappears
	for uuid := range b.energy {
		if !seen[uuid] {
			delete(b.energy, uuid)
		}
	}
}

// synthPCIe draws each GPU's throughput from the configured ranges.
func (b *Backend) synthPCIe(uuids []string, extras *extrasConfig, reading *collect.Reading) {
	for _, uuid := range uuids {
		reading.Extras.PCIe = append(reading.Extras.PCIe, collect.PCIeThroughput{
			UUID:             uuid,
			TXBytesPerSecond: b.rng.draw(extras.PCIe.TXBytesPerSecond),
			RXBytesPerSecond: b.rng.draw(extras.PCIe.RXBytesPerSecond),
		})
	}
}

// synthMIG builds the configured MIG topology, mirroring the real backend's
// semantics: deterministic MIG uuids, sibling compute instances carrying
// identical GPU-instance-scoped memory and utilization (the exporter dedups
// per GPU instance), deterministic list order, and no utilization on the
// first cycle that sees a GPU instance (the real backend's GPM sampling
// needs a sample pair, and the exporter's metric help documents exactly
// that).
func (b *Backend) synthMIG(uuids []string, extras *extrasConfig, reading *collect.Reading) {
	live := map[string]bool{}

	for _, gpu := range extras.MIG {
		if gpu.GPU < 0 || gpu.GPU >= len(uuids) {
			continue
		}

		parent := uuids[gpu.GPU]

		for _, instance := range gpu.Instances {
			memory := b.migMemory(instance)

			key := parent + "/" + strconv.Itoa(instance.GI)
			live[key] = true

			var util *collect.MIGUtilization
			if b.seenGIs[key] {
				util = b.migUtilization(instance, extras)
			} else {
				b.seenGIs[key] = true
			}

			for ci := range instance.CIs {
				reading.Extras.MIG = append(reading.Extras.MIG, collect.MIGInstance{
					ParentUUID:        parent,
					UUID:              migUUID(parent, instance.GI, ci, instance.Profile),
					GPUInstanceID:     strconv.Itoa(instance.GI),
					ComputeInstanceID: strconv.Itoa(ci),
					Profile:           ciProfile(instance),
					Memory:            memory,
					Utilization:       util,
				})
			}
		}
	}

	// a GPU instance that left the topology starts over if it comes back
	for key := range b.seenGIs {
		if !live[key] {
			delete(b.seenGIs, key)
		}
	}
}

// migMemory synthesizes one GPU instance's memory, upholding the
// used + free + reserved = total invariant.
func (b *Backend) migMemory(instance migInstanceConfig) *collect.MIGMemory {
	total := instance.MemoryTotalBytes

	usedShare := rangeCfg{Min: 0.02, Max: 0.10}
	if instance.Busy {
		usedShare = rangeCfg{Min: 0.55, Max: 0.90}
	}

	reserved := total / 200
	used := uint64(b.rng.draw(usedShare) * float64(total-reserved))

	return &collect.MIGMemory{
		Total:    total,
		Used:     used,
		Free:     total - used - reserved,
		Reserved: reserved,
	}
}

// migUtilization synthesizes one GPU instance's activity: the busy instance
// runs hot, the rest idle.
func (b *Backend) migUtilization(instance migInstanceConfig, extras *extrasConfig) *collect.MIGUtilization {
	activity := rangeCfg{Min: 0, Max: 0.05}
	pcieShare := rangeCfg{Min: 0, Max: 0.02}

	if instance.Busy {
		activity = rangeCfg{Min: 0.70, Max: 1.0}
		pcieShare = rangeCfg{Min: 0.2, Max: 0.6}
	}

	value := func(rng rangeCfg) *float64 {
		v := b.rng.draw(rng)

		return &v
	}

	return &collect.MIGUtilization{
		GraphicsActivityRatio: value(activity),
		SMActivityRatio:       value(activity),
		SMOccupancyRatio:      value(activity),
		TensorActivityRatio:   value(rangeCfg{Min: activity.Min * 0.5, Max: activity.Max * 0.8}),
		PCIeTXBytesPerSecond: value(rangeCfg{
			Min: extras.PCIe.TXBytesPerSecond.Min * pcieShare.Min,
			Max: extras.PCIe.TXBytesPerSecond.Max * pcieShare.Max,
		}),
		PCIeRXBytesPerSecond: value(rangeCfg{
			Min: extras.PCIe.RXBytesPerSecond.Min * pcieShare.Min,
			Max: extras.PCIe.RXBytesPerSecond.Max * pcieShare.Max,
		}),
	}
}

// tickXIDs applies the pre-seeded events on the first cycle and emits ongoing
// events per the configured cadence, deterministically given the seed.
func (b *Backend) tickXIDs(uuids []string, extras *extrasConfig, now time.Time) {
	b.seedInitialXIDs(uuids, extras, now)

	interval := extras.xidInterval()
	if interval <= 0 || len(uuids) == 0 {
		return
	}

	if b.nextXIDAt.IsZero() {
		b.nextXIDAt = now.Add(b.jitteredInterval(interval))
	}

	// catch up missed slots, bounded so a long pause cannot burst
	for fired := 0; fired < 10 && now.After(b.nextXIDAt); fired++ {
		uuid := uuids[b.rng.src.Intn(len(uuids))]
		code := extras.XIDs.Codes[b.rng.src.Intn(len(extras.XIDs.Codes))]

		b.bumpXID(uuid, code, 1, b.nextXIDAt)
		b.nextXIDAt = b.nextXIDAt.Add(b.jitteredInterval(interval))
	}

	// a pause longer than the bounded catch-up covers is forgiven, so the
	// schedule does not spend days replaying stale-timestamped events
	if now.After(b.nextXIDAt) {
		b.nextXIDAt = now.Add(b.jitteredInterval(interval))
	}
}

// seedInitialXIDs applies the configured error history once, on the first
// cycle, resolving GPU indexes against the served table.
func (b *Backend) seedInitialXIDs(uuids []string, extras *extrasConfig, now time.Time) {
	if b.xidsSeeded {
		return
	}

	b.xidsSeeded = true

	for _, event := range extras.XIDs.Initial {
		if event.GPU < 0 || event.GPU >= len(uuids) || event.Count == 0 {
			continue
		}

		b.bumpXID(uuids[event.GPU], event.XID, event.Count, now)
	}
}

// jitteredInterval spreads the cadence to 50-150% of the mean.
func (b *Backend) jitteredInterval(mean time.Duration) time.Duration {
	return time.Duration(float64(mean) * (0.5 + b.rng.src.Float64()))
}

// bumpXID folds events into the accumulator.
func (b *Backend) bumpXID(uuid string, xid, count uint64, at time.Time) {
	perGPU := b.xids[uuid]
	if perGPU == nil {
		perGPU = map[uint64]*xidStat{}
		b.xids[uuid] = perGPU
	}

	stat := perGPU[xid]
	if stat == nil {
		stat = &xidStat{}
		perGPU[xid] = stat
	}

	stat.count += count
	stat.last = at
}

// migUUID derives a stable MIG device uuid from the identity tuple, like the
// real driver's deterministic placement-derived uuids.
func migUUID(parent string, gi, ci int, profile string) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s/%d/%d/%s", parent, gi, ci, profile))

	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// ciProfile spells the compute-instance-qualified profile the way nvidia-smi
// names sliced instances ("1c.3g.71gb") when a GPU instance hosts several
// compute instances.
func ciProfile(instance migInstanceConfig) string {
	if instance.CIs <= 1 {
		return instance.Profile
	}

	return "1c." + instance.Profile
}

// sortXIDCounters orders the scrape output deterministically.
func sortXIDCounters(counters []collect.XIDCounter) {
	sort.Slice(counters, func(i, j int) bool {
		if counters[i].UUID != counters[j].UUID {
			return counters[i].UUID < counters[j].UUID
		}

		return counters[i].XID < counters[j].XID
	})
}

// attributeApps assigns per-process readings to the configured MIG topology,
// the way the real nvml backend attributes processes to their compute
// instance. The mapping is stable: a process keeps its instance across
// cycles (hash of the pid over the flattened instance list). Processes on
// GPUs without a topology keep empty attribution, like non-MIG GPUs.
func attributeApps(uuids []string, extras *extrasConfig, reading *collect.Reading) {
	if len(reading.Apps) == 0 || len(extras.MIG) == 0 {
		return
	}

	type instanceRef struct{ gi, ci int }

	topology := map[string][]instanceRef{}

	for _, gpu := range extras.MIG {
		if gpu.GPU < 0 || gpu.GPU >= len(uuids) {
			continue
		}

		for _, instance := range gpu.Instances {
			for ci := range instance.CIs {
				topology[uuids[gpu.GPU]] = append(topology[uuids[gpu.GPU]], instanceRef{gi: instance.GI, ci: ci})
			}
		}
	}

	for i := range reading.Apps {
		app := &reading.Apps[i]

		refs := topology[app.GPUUUID]
		if len(refs) == 0 {
			continue
		}

		ref := refs[pidSlot(app.PID, len(refs))]
		app.GPUInstanceID = strconv.Itoa(ref.gi)
		app.ComputeInstanceID = strconv.Itoa(ref.ci)
	}
}

// pidSlot picks a stable slot for a pid.
func pidSlot(pid string, slots int) int {
	sum := sha256.Sum256([]byte(pid))

	return int(sum[0]) % slots
}
