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
	// Apps holds the per-process data, nil when disabled or failed.
	Apps []nvidiasmi.ComputeApp
	// AppsAttempted reports whether the collection included a per-process query.
	AppsAttempted bool
	// AppsSuccess reports whether that per-process query succeeded, valid only
	// when AppsAttempted.
	AppsSuccess bool
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

// Reading is what one collection cycle produced. The GPU table is the primary
// result: the error and exit code returned alongside a Reading belong to the
// GPU query alone and drive all collection health (failure counting, exit
// code, shutdown-on-error). The per-process query is secondary and fails
// softly: its outcome lives in the Apps fields and must never fail the
// collection.
type Reading struct {
	// Table holds the GPU data.
	Table *nvidiasmi.Table
	// Apps holds the per-process data, nil when disabled or failed.
	Apps []nvidiasmi.ComputeApp
	// AppsAttempted reports whether a per-process query ran (the feature is on).
	AppsAttempted bool
	// AppsSuccess reports whether that query succeeded, valid only when AppsAttempted.
	AppsSuccess bool
	// AppsErr is the per-process query's error, for source-owned logging only.
	AppsErr error
}

// QueryFunc runs one collection cycle: the nvidia-smi GPU query, plus the
// per-process query when enabled. It is injected so the timing and staleness
// logic can be tested without spawning a process or parsing CSV. The returned
// error and exit code describe the GPU query only, per the Reading contract.
type QueryFunc func(ctx context.Context) (Reading, int, error)

// FatalError marks a collection failure that should trigger
// shutdown-on-error. The exec backend signals fatality through
// *exec.ExitError (a genuine non-zero exit of the command); backends without
// a subprocess wrap their fatal-class failures (driver/GPU lifecycle errors,
// never per-field unavailability) in this type instead.
type FatalError struct {
	Err error
}

func (e *FatalError) Error() string {
	if e == nil || e.Err == nil {
		return "fatal collection error"
	}

	return e.Err.Error()
}

func (e *FatalError) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.Err
}

// collectOnce runs one collection bounded by timeout and returns the outcome
// as a Snapshot with the cumulative fields (Failures, LastSuccess) not yet
// folded in: that is the caller's job, via foldCumulative, under whatever
// synchronization owns those counters. A failed attempt is logged here,
// exactly once, so failures are neither logged per scrape nor lost. The
// onFatal callback implements shutdown-on-error: it fires only on a genuine
// non-zero exit of the command, not on a timeout the collector caused itself.
func collectOnce(
	ctx context.Context,
	query QueryFunc,
	timeout time.Duration,
	onFatal func(error),
	logger *slog.Logger,
) Snapshot {
	callCtx, cancel := withOptionalTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	reading, exitCode, err := query(callCtx)
	now := time.Now()

	snapshot := Snapshot{
		Attempted: true,
		ExitCode:  exitCode,
		Duration:  now.Sub(start),
		Err:       err,
	}

	if err != nil {
		logger.Error("failed to collect metrics", "err", err)

		var exitErr *exec.ExitError

		var fatalErr *FatalError

		if callCtx.Err() == nil && onFatal != nil &&
			(errors.As(err, &exitErr) || errors.As(err, &fatalErr)) {
			onFatal(err)
		}

		return snapshot
	}

	// The per-process query fails softly: it is logged here (once per attempt,
	// like a primary failure), but it does not count as a failed collection
	// and never triggers shutdown-on-error.
	if reading.AppsAttempted && !reading.AppsSuccess {
		logger.Warn("failed to collect per-process data", "err", reading.AppsErr)
	}

	snapshot.Success = true
	snapshot.Table = reading.Table
	snapshot.Apps = reading.Apps
	snapshot.AppsAttempted = reading.AppsAttempted
	snapshot.AppsSuccess = reading.AppsSuccess
	snapshot.LastSuccess = now

	return snapshot
}

// foldCumulative merges one collection outcome into the cumulative failure
// count and last-success time, and stamps the updated values onto the
// snapshot. The caller owns the synchronization of the two counters.
func foldCumulative(snapshot *Snapshot, failures *uint64, lastOK *time.Time) {
	if snapshot.Success {
		*lastOK = snapshot.LastSuccess
	} else {
		*failures++
	}

	snapshot.Failures = *failures
	snapshot.LastSuccess = *lastOK
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
