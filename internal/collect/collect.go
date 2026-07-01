package collect

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"time"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

// Snapshot is one collection's outcome, which the exporter renders as metrics.
// Data and health are separate: Table is the data to show (nil when the latest
// collection failed), the rest describes attempts. Health is read from the
// explicit booleans, never inferred from Err.
type Snapshot struct {
	// Attempted reports whether any collection has completed yet.
	Attempted bool
	// Success reports whether the most recent collection succeeded.
	Success bool
	// Table holds the GPU data of the most recent collection, nil on failure.
	Table *nvidiasmi.Table
	// ExitCode is the exit code of the most recent attempt, valid only when Attempted.
	ExitCode int
	// Duration is how long the most recent attempt took, valid only when Attempted.
	Duration time.Duration
	// LastSuccess is the completion time of the most recent success, zero until the first one.
	LastSuccess time.Time
	// Failures is the cumulative count of failed collections.
	Failures uint64
	// Err is the most recent attempt's error, kept for source-owned logging and tests.
	Err error
}

// Source produces the latest reading. The exporter depends only on this.
type Source interface {
	Latest(ctx context.Context) Snapshot
}

// QueryFunc runs nvidia-smi once. It is injected so the timing and staleness
// logic can be tested without spawning a process or parsing CSV.
type QueryFunc func(ctx context.Context) (*nvidiasmi.Table, int, error)

// collectOnce runs one collection bounded by timeout and folds the outcome
// into a Snapshot, updating the cumulative failure count and last-success
// time owned by the caller. A failed attempt is logged here, exactly once,
// so failures are neither logged per scrape nor lost. The onFatal callback
// implements shutdown-on-error: it fires only on a genuine non-zero exit of
// the command, not on a timeout the collector caused itself.
func collectOnce(
	ctx context.Context,
	query QueryFunc,
	timeout time.Duration,
	onFatal func(error),
	logger *slog.Logger,
	failures *uint64,
	lastOK *time.Time,
) Snapshot {
	callCtx, cancel := withOptionalTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	table, exitCode, err := query(callCtx)
	now := time.Now()

	snapshot := Snapshot{
		Attempted: true,
		ExitCode:  exitCode,
		Duration:  now.Sub(start),
		Err:       err,
	}

	if err != nil {
		*failures++

		logger.Error("failed to collect metrics", "err", err)

		snapshot.Success = false
		snapshot.Table = nil
		snapshot.LastSuccess = *lastOK
		snapshot.Failures = *failures

		var exitErr *exec.ExitError
		if callCtx.Err() == nil && errors.As(err, &exitErr) && onFatal != nil {
			onFatal(err)
		}

		return snapshot
	}

	*lastOK = now

	snapshot.Success = true
	snapshot.Table = table
	snapshot.LastSuccess = now
	snapshot.Failures = *failures

	return snapshot
}

// withOptionalTimeout bounds ctx by d, where a zero d means no bound. A plain
// context.WithTimeout with a zero duration would return an already-expired
// context, which is not what "disabled" means, hence the explicit check.
func withOptionalTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d == 0 {
		return ctx, func() {}
	}

	return context.WithTimeout(ctx, d)
}
