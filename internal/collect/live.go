package collect

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Live collects synchronously: a Latest call runs nvidia-smi inline. Calls
// arriving while a collection is already running do not queue their own runs;
// they wait for the in-flight one and serve its result, so the number of
// nvidia-smi runs is independent of how many scrapers hit the exporter at
// once. The run is bounded by the collection timeout and by its waiters: a
// single caller giving up does not disturb the others, but a run nobody waits
// for anymore is cancelled.
type Live struct {
	query   QueryFunc
	timeout time.Duration
	onFatal func(error)
	logger  *slog.Logger

	mu       sync.Mutex // guards the fields below
	inflight *flight
	failures uint64
	lastOK   time.Time
}

// flight is one shared collection run. Waiters block on done and read the
// snapshot only after it is closed; the snapshot is written exactly once,
// before the close.
type flight struct {
	done     chan struct{}
	cancel   context.CancelFunc
	waiters  int
	snapshot Snapshot
}

// NewLive returns a synchronous source. A zero timeout leaves collections
// unbounded. The onFatal callback implements shutdown-on-error and may be nil.
func NewLive(query QueryFunc, timeout time.Duration, onFatal func(error), logger *slog.Logger) *Live {
	return &Live{
		query:   query,
		timeout: timeout,
		onFatal: onFatal,
		logger:  logger,
	}
}

// Latest returns the outcome of a collection running during the call: the
// in-flight one when there is one, a fresh run otherwise. When ctx ends
// before the collection does, it returns a no-data snapshot carrying the
// cumulative health state instead, so a scrape on a deadline still reports
// something truthful.
func (s *Live) Latest(ctx context.Context) Snapshot {
	if ctx.Err() != nil {
		return s.noResult()
	}

	shared := s.join(ctx)

	select {
	case <-shared.done:
		return shared.snapshot
	case <-ctx.Done():
		s.leave(shared)

		// the run may have completed in the meantime; prefer its result over
		// reporting nothing
		select {
		case <-shared.done:
			return shared.snapshot
		default:
			return s.noResult()
		}
	}
}

// join registers the caller as a waiter on the in-flight run, starting one
// when idle.
func (s *Live) join(ctx context.Context) *flight {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.inflight == nil {
		// the run is shared, so its lifetime must not be tied to any single
		// waiter's context: it is cancelled when its last waiter leaves
		runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		s.inflight = &flight{done: make(chan struct{}), cancel: cancel}

		go s.run(runCtx, s.inflight)
	}

	s.inflight.waiters++

	return s.inflight
}

// leave unregisters a waiter that gave up on the run; the last one out
// cancels it and takes it out of the joinable slot, so the next scrape
// starts a fresh run instead of inheriting the cancellation. That fresh run
// can briefly overlap the abandoned one while it unwinds; the abandoned
// process is already being killed at that point, and the cumulative counters
// are only ever updated under the lock, so the overlap is safe. An unkillable
// abandoned run (a wedged driver call, an nvidia-smi stuck in the kernel)
// thus cannot poison later scrapes.
func (s *Live) leave(shared *flight) {
	s.mu.Lock()
	defer s.mu.Unlock()

	shared.waiters--

	if shared.waiters == 0 && s.inflight == shared {
		shared.cancel()

		s.inflight = nil
	}
}

// run performs the collection and publishes the outcome. The fold into the
// cumulative counters, the release of the joinable slot and the publication
// happen as one locked transition, so a waiter deciding between the result
// and noResult (in leave) never observes them half-done.
func (s *Live) run(ctx context.Context, shared *flight) {
	defer shared.cancel()

	snapshot := collectOnce(ctx, s.query, s.timeout, s.onFatal, s.logger)

	s.mu.Lock()
	defer s.mu.Unlock()

	foldCumulative(&snapshot, &s.failures, &s.lastOK)

	if s.inflight == shared {
		s.inflight = nil
	}

	shared.snapshot = snapshot
	close(shared.done)
}

// noResult is what a caller that could not get a collection outcome serves:
// no data, no attempt, with the cumulative health state carried over.
func (s *Live) noResult() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	return Snapshot{Failures: s.failures, LastSuccess: s.lastOK}
}
