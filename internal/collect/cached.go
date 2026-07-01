package collect

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"
)

// Cached collects in the background on a fixed interval and serves the most
// recent outcome. Reads are lock-free, there is exactly one collection in
// flight at a time, and the number of nvidia-smi runs is independent of how
// often the exporter is scraped.
type Cached struct {
	query    QueryFunc
	interval time.Duration
	timeout  time.Duration
	onFatal  func(error)
	logger   *slog.Logger

	cur     atomic.Pointer[Snapshot]
	ready   chan struct{}
	started atomic.Bool // Run is single-use; protects the one-collection-in-flight invariant

	// owned solely by the Run goroutine
	failures uint64
	lastOK   time.Time
}

// NewCached returns a background-collecting source that runs a collection
// every interval, each bounded by timeout (0 leaves them unbounded). The
// onFatal callback implements shutdown-on-error and may be nil. Start it
// with Run.
func NewCached(
	query QueryFunc,
	interval time.Duration,
	timeout time.Duration,
	onFatal func(error),
	logger *slog.Logger,
) *Cached {
	return &Cached{
		query:    query,
		interval: interval,
		timeout:  timeout,
		onFatal:  onFatal,
		logger:   logger,
		ready:    make(chan struct{}),
	}
}

// Run collects immediately to warm the cache, then on every interval tick
// until ctx is cancelled. It is single-use: a second call would start a
// second ticker loop and race the collection state, so it is rejected.
func (s *Cached) Run(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errors.New("cached source already started")
	}

	s.tick(ctx)
	close(s.ready) // single-use Run means exactly one close

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// Latest returns the most recent outcome. A call racing the very first
// collection blocks until that collection completes, bounded by the
// collection timeout that bounds the first run (and by ctx). If no outcome
// is available yet, the returned snapshot reports not-ready: not attempted,
// not successful, no data.
func (s *Cached) Latest(ctx context.Context) Snapshot {
	select {
	case <-s.ready:
	case <-ctx.Done():
	}

	if snapshot := s.cur.Load(); snapshot != nil {
		return *snapshot
	}

	return Snapshot{}
}

// tick runs one collection and publishes its outcome. The published snapshot
// is immutable: it is never modified after Store, which is what makes the
// lock-free reads in Latest safe.
func (s *Cached) tick(ctx context.Context) {
	snapshot := collectOnce(ctx, s.query, s.timeout, s.onFatal, s.logger, &s.failures, &s.lastOK)
	s.cur.Store(&snapshot)
}
