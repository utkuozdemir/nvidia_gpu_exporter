//go:build linux && cgo

package nvmlnative

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

// xidWaitTimeoutMs bounds each event wait, so the watcher regularly
// re-checks its generation and the context without busy-looping.
const xidWaitTimeoutMs = 1000

// xidBackoffStart and xidBackoffMax pace the registration retries when NVML
// is down or events are unavailable. The backoff resets after a successful
// registration and is context-aware, so shutdown never waits it out.
const (
	xidBackoffStart = time.Second
	xidBackoffMax   = 30 * time.Second
)

// xidAccumulator is the cross-cycle XID state: written by the watcher
// goroutine, read at scrape time. It has its own lock because it is the only
// state shared between the watcher and the scrape path.
type xidAccumulator struct {
	mu    sync.Mutex
	stats map[string]map[uint64]*xidStat
	// waitWarned makes a persistent event-wait failure visible exactly
	// once, alongside the accumulator because it shares the same lock.
	waitWarned bool
}

// xidStat is one (GPU, XID code) pair's running state.
type xidStat struct {
	count uint64
	last  time.Time
}

// bump records one observed event.
func (a *xidAccumulator) bump(uuid string, xid uint64, at time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.stats == nil {
		a.stats = map[string]map[uint64]*xidStat{}
	}

	perGPU := a.stats[uuid]
	if perGPU == nil {
		perGPU = map[uint64]*xidStat{}
		a.stats[uuid] = perGPU
	}

	stat := perGPU[xid]
	if stat == nil {
		stat = &xidStat{}
		perGPU[xid] = stat
	}

	stat.count++
	stat.last = at
}

// counts snapshots the accumulated state, sorted for deterministic output.
func (a *xidAccumulator) counts() []collect.XIDCounter {
	a.mu.Lock()
	defer a.mu.Unlock()

	var counters []collect.XIDCounter

	for uuid, perGPU := range a.stats {
		for xid, stat := range perGPU {
			counters = append(counters, collect.XIDCounter{
				UUID:     uuid,
				XID:      xid,
				Count:    stat.count,
				LastSeen: stat.last,
			})
		}
	}

	sort.Slice(counters, func(i, j int) bool {
		if counters[i].UUID != counters[j].UUID {
			return counters[i].UUID < counters[j].UUID
		}

		return counters[i].XID < counters[j].XID
	})

	return counters
}

// XIDCounts serves the accumulated XID error counts to the exporter. Safe
// for concurrent use; independent of the collection pipeline by design, so
// the counters stay visible while collections fail.
func (b *Backend) XIDCounts() []collect.XIDCounter {
	return b.xids.counts()
}

// xidWatcherLog makes each watcher failure mode visible exactly once, so a
// setup without event support does not flood the log every backoff round.
type xidWatcherLog struct {
	createWarned   bool
	registerWarned bool
	noneWarned     bool
}

// RunXIDWatcher registers for XID error events on every device and
// accumulates them until ctx ends. It runs beside the collection cycles, not
// inside them: XIDs must be caught as they happen, not when a scrape
// arrives. It never returns an error: on a machine without event support the
// watcher idles (retrying periodically, in case a driver reload changes the
// answer) and the XID families simply stay empty. When the driver generation
// dies underneath it, the watcher drives the re-initialization itself, so
// event collection recovers even when nothing is scraping.
func (b *Backend) RunXIDWatcher(ctx context.Context) error {
	warnings := &xidWatcherLog{}
	backoff := xidBackoffStart

	for ctx.Err() == nil {
		set, uuids, generation, ok := b.xidRegister(warnings)
		if !ok {
			// registration failing usually means the driver generation is
			// dead: recover it here rather than waiting for a scrape
			b.tryRecover()

			if !sleepContext(ctx, backoff) {
				return nil
			}

			backoff = min(backoff*2, xidBackoffMax)

			continue
		}

		// a fresh registration round may fail for fresh reasons: make them
		// visible again instead of staying silenced forever
		*warnings = xidWatcherLog{}

		healthy := b.xidWait(ctx, set, uuids, generation)
		if healthy {
			backoff = xidBackoffStart

			continue
		}

		// the wait path itself failed: pace the rebuild, or a persistently
		// failing driver call would spin register/wait/free at full speed
		if !sleepContext(ctx, backoff) {
			return nil
		}

		backoff = min(backoff*2, xidBackoffMax)
	}

	return nil
}

// xidRegister builds an event set and registers every device for XID
// events, under the shared lifecycle barrier. It reports the set, the
// registration-time uuid cache (a GPU may be unreadable by the time it
// errors), the NVML generation the set belongs to, and whether anything was
// registered at all.
//
//nolint:cyclop,funlen,ireturn // one linear pass; the event set is go-nvml.s own interface type
func (b *Backend) xidRegister(warnings *xidWatcherLog) (nvml.EventSet, map[nvml.Device]string, uint64, bool) {
	b.lifecycleMu.RLock()
	defer b.lifecycleMu.RUnlock()

	if !b.genLive.Load() {
		return nil, nil, 0, false
	}

	generation := b.generation.Load()

	set, ret := b.api.eventSetCreate()
	if ret != nvml.SUCCESS {
		if !warnings.createWarned {
			warnings.createWarned = true

			b.logger.Warn("cannot create an NVML event set, XID errors will not be counted",
				"nvml_return", ret.String())
		}

		return nil, nil, 0, false
	}

	count, ret := b.api.deviceCount()
	if ret != nvml.SUCCESS {
		_ = set.Free()

		return nil, nil, 0, false
	}

	uuids := map[nvml.Device]string{}

	for deviceIdx := range count {
		dev, ret := b.api.deviceByIndex(deviceIdx)
		if ret != nvml.SUCCESS {
			continue
		}

		uuid, ret := dev.GetUUID()
		if ret != nvml.SUCCESS {
			continue
		}

		if ret := dev.RegisterEvents(nvml.EventTypeXidCriticalError, set); ret != nvml.SUCCESS {
			if !warnings.registerWarned {
				warnings.registerWarned = true

				b.logger.Warn("cannot register for XID error events on a GPU",
					"uuid", nvidiasmi.NormalizeUUID(uuid), "nvml_return", ret.String())
			}

			continue
		}

		uuids[dev] = nvidiasmi.NormalizeUUID(uuid)
	}

	if len(uuids) == 0 {
		_ = set.Free()

		if !warnings.noneWarned {
			warnings.noneWarned = true

			b.logger.Warn("registered no GPU for XID error events, the XID counters stay empty")
		}

		return nil, nil, 0, false
	}

	// the readiness signal: anything waiting to inject a test XID (the
	// GPU-box runbook) must wait for this line, since earlier events cannot
	// be replayed
	b.logger.Info("watching for GPU XID error events", "gpus", len(uuids))

	return set, uuids, generation, true
}

// xidWait drains the event set until the context ends, the NVML generation
// dies, or the set fails. Every driver call happens under the shared
// lifecycle barrier. The set is freed under that same barrier, and ONLY
// while its generation is still alive: once the generation died, the
// shutdown that killed it also reclaimed the set, and freeing it would
// touch a torn-down library (when a wedged shutdown was skipped instead,
// the unfreeable set is the lesser evil, and the next real shutdown
// reclaims it). Reports whether the wait path stayed healthy: false means
// the driver failed the wait itself and the rebuild must be paced.
func (b *Backend) xidWait(
	ctx context.Context,
	set nvml.EventSet,
	uuids map[nvml.Device]string,
	generation uint64,
) bool {
	generationAlive := func() bool {
		return b.genLive.Load() && b.generation.Load() == generation
	}

	for {
		b.lifecycleMu.RLock()

		if ctx.Err() != nil || !generationAlive() {
			if generationAlive() {
				_ = set.Free()
			}

			b.lifecycleMu.RUnlock()

			return true
		}

		data, ret := set.Wait(xidWaitTimeoutMs)

		//nolint:exhaustive // every other return is a failed wait
		switch ret {
		case nvml.SUCCESS:
			b.lifecycleMu.RUnlock()
			b.recordXID(uuids, data)
		case nvml.ERROR_TIMEOUT:
			b.lifecycleMu.RUnlock()
		default:
			// the wait itself failed: this event set is done. Free it while
			// still holding the barrier (unless its generation died) and let
			// the outer loop rebuild.
			if generationAlive() {
				_ = set.Free()
			}

			b.lifecycleMu.RUnlock()

			b.warnOnceXID("GPU XID event wait failed, rebuilding the event registration", ret)

			return false
		}
	}
}

// warnOnceXID logs a wait failure without flooding: the accumulator mutex
// guards the flag because the watcher is the only writer but tests may run
// concurrent watchers.
func (b *Backend) warnOnceXID(msg string, ret nvml.Return) {
	b.xids.mu.Lock()
	defer b.xids.mu.Unlock()

	if b.xids.waitWarned {
		return
	}

	b.xids.waitWarned = true

	b.logger.Warn(msg, "nvml_return", ret.String())
}

// recordXID folds one received event into the accumulator.
func (b *Backend) recordXID(uuids map[nvml.Device]string, data nvml.EventData) {
	uuid := uuids[data.Device]
	if uuid == "" {
		// an event from a device that was not in the registration cache:
		// label it explicitly rather than dropping the count
		uuid = "unknown"
	}

	b.xids.bump(uuid, data.EventData, b.now())

	b.logger.Warn("observed a GPU XID error", "uuid", uuid, "xid", data.EventData)
}

// sleepContext sleeps for the given duration, reporting false when the
// context ended first.
func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
