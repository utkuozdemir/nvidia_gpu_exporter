package collect

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Live collects synchronously: every Latest call runs nvidia-smi inline.
// Concurrent calls are serialized so simultaneous scrapes never launch
// multiple nvidia-smi processes at once, matching the exporter's historical
// behavior.
type Live struct {
	query   QueryFunc
	timeout time.Duration
	onFatal func(error)
	logger  *slog.Logger

	mu       sync.Mutex // serializes collections and guards the fields below
	failures uint64
	lastOK   time.Time
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

// Latest runs one collection and returns its outcome.
func (s *Live) Latest(ctx context.Context) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	return collectOnce(ctx, s.query, s.timeout, s.onFatal, s.logger, &s.failures, &s.lastOK)
}
