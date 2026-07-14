package collect_test

import (
	"context"
	"errors"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

var errQueryFailed = errors.New("query failed")

func staticQuery(table *nvidiasmi.Table, exitCode int, err error) collect.QueryFunc {
	return func(_ context.Context) (collect.Reading, int, error) {
		return collect.Reading{Table: table}, exitCode, err
	}
}

// realExitError produces a genuine *exec.ExitError.
func realExitError(t *testing.T) error {
	t.Helper()

	err := exec.CommandContext(t.Context(), "sh", "-c", "exit 3").Run()
	require.Error(t, err)

	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)

	return err //nolint:wrapcheck // tests need the bare *exec.ExitError
}

func TestLiveSuccess(t *testing.T) {
	t.Parallel()

	table := &nvidiasmi.Table{}
	live := collect.NewLive(staticQuery(table, 0, nil), 0, nil, slogt.New(t))

	snapshot := live.Latest(t.Context())

	assert.True(t, snapshot.Attempted)
	assert.True(t, snapshot.Success)
	assert.Same(t, table, snapshot.Table)
	assert.Equal(t, 0, snapshot.ExitCode)
	assert.False(t, snapshot.LastSuccess.IsZero())
	assert.Equal(t, uint64(0), snapshot.Failures)
	require.NoError(t, snapshot.Err)
}

func TestLiveFailureDropsTable(t *testing.T) {
	t.Parallel()

	live := collect.NewLive(staticQuery(nil, -1, errQueryFailed), 0, nil, slogt.New(t))

	snapshot := live.Latest(t.Context())

	assert.True(t, snapshot.Attempted)
	assert.False(t, snapshot.Success)
	assert.Nil(t, snapshot.Table)
	assert.Equal(t, -1, snapshot.ExitCode)
	assert.True(t, snapshot.LastSuccess.IsZero())
	assert.Equal(t, uint64(1), snapshot.Failures)
	require.ErrorIs(t, snapshot.Err, errQueryFailed)
}

func TestLiveFailuresAccumulateAndLastSuccessSticks(t *testing.T) {
	t.Parallel()

	table := &nvidiasmi.Table{}
	calls := 0
	query := func(_ context.Context) (collect.Reading, int, error) {
		calls++
		if calls == 1 {
			return collect.Reading{Table: table}, 0, nil
		}

		return collect.Reading{}, -1, errQueryFailed
	}

	live := collect.NewLive(query, 0, nil, slogt.New(t))

	first := live.Latest(t.Context())
	require.True(t, first.Success)

	second := live.Latest(t.Context())
	assert.False(t, second.Success)
	assert.Equal(t, uint64(1), second.Failures)
	// the last-success time survives a later failure, it just stops advancing
	assert.Equal(t, first.LastSuccess, second.LastSuccess)

	third := live.Latest(t.Context())
	assert.Equal(t, uint64(2), third.Failures)
	assert.Equal(t, first.LastSuccess, third.LastSuccess)
}

func TestLiveTimeoutBecomesFailure(t *testing.T) {
	t.Parallel()

	query := func(ctx context.Context) (collect.Reading, int, error) {
		<-ctx.Done()

		return collect.Reading{}, -1, ctx.Err()
	}

	live := collect.NewLive(query, 50*time.Millisecond, nil, slogt.New(t))

	start := time.Now()
	snapshot := live.Latest(t.Context())

	assert.Less(t, time.Since(start), 5*time.Second)
	assert.False(t, snapshot.Success)
	require.ErrorIs(t, snapshot.Err, context.DeadlineExceeded)
}

func TestLiveZeroTimeoutDisablesDeadline(t *testing.T) {
	t.Parallel()

	query := func(ctx context.Context) (collect.Reading, int, error) {
		// with a zero timeout the context must not carry a deadline; a plain
		// context.WithTimeout(ctx, 0) would be expired already
		_, hasDeadline := ctx.Deadline()
		assert.False(t, hasDeadline)

		require.NoError(t, ctx.Err())

		return collect.Reading{Table: &nvidiasmi.Table{}}, 0, nil
	}

	live := collect.NewLive(query, 0, nil, slogt.New(t))

	snapshot := live.Latest(t.Context())

	assert.True(t, snapshot.Success)
}

func TestLiveOnFatal(t *testing.T) {
	t.Parallel()

	exitErr := realExitError(t)

	tests := []struct {
		name      string
		err       error
		wantFatal bool
	}{
		{name: "non-zero exit fires it", err: exitErr, wantFatal: true},
		{name: "other errors do not", err: errQueryFailed, wantFatal: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotFatal error

			onFatal := func(err error) { gotFatal = err }

			live := collect.NewLive(staticQuery(nil, 3, tt.err), 0, onFatal, slogt.New(t))
			live.Latest(t.Context())

			if tt.wantFatal {
				require.ErrorIs(t, gotFatal, tt.err)
			} else {
				require.NoError(t, gotFatal)
			}
		})
	}
}

func TestLiveOnFatalNotFiredOnTimeout(t *testing.T) {
	t.Parallel()

	exitErr := realExitError(t)

	var gotFatal error

	onFatal := func(err error) { gotFatal = err }

	// the query returns an exit error, but only after the collection timeout
	// fired: the kill is ours, so shutdown-on-error must not trigger
	query := func(ctx context.Context) (collect.Reading, int, error) {
		<-ctx.Done()

		return collect.Reading{}, 3, exitErr
	}

	live := collect.NewLive(query, 50*time.Millisecond, onFatal, slogt.New(t))
	snapshot := live.Latest(t.Context())

	assert.False(t, snapshot.Success)
	require.NoError(t, gotFatal)
}

func TestLiveAppsSoftFailure(t *testing.T) {
	t.Parallel()

	// the per-process query failing must not fail the collection: no failure
	// counted, shutdown-on-error not fired, GPU data still served
	table := &nvidiasmi.Table{}
	exitErr := realExitError(t)
	query := func(_ context.Context) (collect.Reading, int, error) {
		return collect.Reading{
			Table:         table,
			AppsAttempted: true,
			AppsSuccess:   false,
			AppsErr:       exitErr,
		}, 0, nil
	}

	var gotFatal error

	live := collect.NewLive(query, 0, func(err error) { gotFatal = err }, slogt.New(t))

	snapshot := live.Latest(t.Context())

	assert.True(t, snapshot.Success)
	assert.Same(t, table, snapshot.Table)
	assert.Equal(t, uint64(0), snapshot.Failures)
	assert.True(t, snapshot.AppsAttempted)
	assert.False(t, snapshot.AppsSuccess)
	assert.Nil(t, snapshot.Apps)
	require.NoError(t, snapshot.Err)
	require.NoError(t, gotFatal)
}

func TestLiveAppsSuccessCarriesApps(t *testing.T) {
	t.Parallel()

	apps := []nvidiasmi.ComputeApp{{GPUUUID: "abc", PID: "42", ProcessName: "./gpu_burn", UsedMemory: "10 MiB"}}
	query := func(_ context.Context) (collect.Reading, int, error) {
		return collect.Reading{
			Table:         &nvidiasmi.Table{},
			Apps:          apps,
			AppsAttempted: true,
			AppsSuccess:   true,
		}, 0, nil
	}

	live := collect.NewLive(query, 0, nil, slogt.New(t))

	snapshot := live.Latest(t.Context())

	assert.True(t, snapshot.Success)
	assert.True(t, snapshot.AppsAttempted)
	assert.True(t, snapshot.AppsSuccess)
	assert.Equal(t, apps, snapshot.Apps)
}

func TestLiveConcurrentCallsShareOneRun(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	table := &nvidiasmi.Table{}
	firstIn := make(chan struct{}, 1)
	release := make(chan struct{})
	query := func(_ context.Context) (collect.Reading, int, error) {
		calls.Add(1)

		select {
		case firstIn <- struct{}{}:
		default:
		}

		<-release

		return collect.Reading{Table: table}, 0, nil
	}

	live := collect.NewLive(query, 0, nil, slogt.New(t))

	got := make(chan collect.Snapshot, 3)

	for range 3 {
		go func() {
			got <- live.Latest(t.Context())
		}()
	}

	// one run is now in flight and holding; give the other callers time to
	// join it as waiters instead of queueing their own runs
	<-firstIn
	time.Sleep(100 * time.Millisecond)

	close(release)

	for range 3 {
		snapshot := <-got

		assert.True(t, snapshot.Success)
		assert.Same(t, table, snapshot.Table)
	}

	assert.Equal(t, int32(1), calls.Load(), "concurrent scrapes must share a single run")
}

func TestLiveFailedSharedRunCountsOnce(t *testing.T) {
	t.Parallel()

	firstIn := make(chan struct{}, 1)
	release := make(chan struct{})
	query := func(_ context.Context) (collect.Reading, int, error) {
		select {
		case firstIn <- struct{}{}:
		default:
		}

		<-release

		return collect.Reading{}, -1, errQueryFailed
	}

	live := collect.NewLive(query, 0, nil, slogt.New(t))

	got := make(chan collect.Snapshot, 3)

	for range 3 {
		go func() {
			got <- live.Latest(t.Context())
		}()
	}

	<-firstIn
	time.Sleep(100 * time.Millisecond)

	close(release)

	for range 3 {
		snapshot := <-got

		assert.False(t, snapshot.Success)
		assert.Equal(t, uint64(1), snapshot.Failures, "a failed shared run must count as one failure")
	}
}

func TestLiveDepartingWaiterDoesNotCancelSharedRun(t *testing.T) {
	t.Parallel()

	table := &nvidiasmi.Table{}
	firstIn := make(chan struct{}, 1)
	release := make(chan struct{})

	var runCtxAlive atomic.Bool

	query := func(ctx context.Context) (collect.Reading, int, error) {
		select {
		case firstIn <- struct{}{}:
		default:
		}

		<-release

		runCtxAlive.Store(ctx.Err() == nil)

		return collect.Reading{Table: table}, 0, nil
	}

	live := collect.NewLive(query, 0, nil, slogt.New(t))

	staying := make(chan collect.Snapshot, 1)

	go func() {
		staying <- live.Latest(t.Context())
	}()

	<-firstIn

	// a second waiter joins the same run and then gives up
	leaveCtx, leaveCancel := context.WithCancel(t.Context())

	leaving := make(chan collect.Snapshot, 1)

	go func() {
		leaving <- live.Latest(leaveCtx)
	}()

	time.Sleep(100 * time.Millisecond)
	leaveCancel()

	// the departing waiter serves the no-result state right away
	left := <-leaving
	assert.False(t, left.Attempted)
	assert.Nil(t, left.Table)
	assert.Equal(t, uint64(0), left.Failures)

	close(release)

	// the remaining waiter still gets the run's result, from a run that was
	// never cancelled
	stayed := <-staying
	assert.True(t, stayed.Success)
	assert.Same(t, table, stayed.Table)
	assert.True(t, runCtxAlive.Load(), "the run must not be cancelled while a waiter remains")
}

func TestLiveRunWithoutWaitersIsCancelled(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{}, 1)
	query := func(ctx context.Context) (collect.Reading, int, error) {
		select {
		case entered <- struct{}{}:
		default:
		}

		// completes only through cancellation
		<-ctx.Done()

		return collect.Reading{}, -1, ctx.Err()
	}

	live := collect.NewLive(query, 0, nil, slogt.New(t))

	ctx, cancel := context.WithCancel(t.Context())

	got := make(chan collect.Snapshot, 1)

	go func() {
		got <- live.Latest(ctx)
	}()

	<-entered
	cancel()

	// the sole waiter leaves with the no-result state
	snapshot := <-got
	assert.False(t, snapshot.Attempted)
	assert.Nil(t, snapshot.Table)

	// its departure cancels the run, which lands as exactly one failure. The
	// probe context is already cancelled so the probes read the health state
	// without starting runs of their own.
	probeCtx, probeCancel := context.WithCancel(t.Context())
	probeCancel()

	assert.Eventually(t, func() bool {
		return live.Latest(probeCtx).Failures == 1
	}, 5*time.Second, time.Millisecond)
}

func TestLiveScrapeAfterAbandonedRunStartsFresh(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	firstEntered := make(chan struct{})
	unwindRelease := make(chan struct{})

	t.Cleanup(func() { close(unwindRelease) })

	// the first run hangs until cancelled and then unwinds slowly, like a
	// killed process that takes a while to reap; later runs succeed
	query := func(ctx context.Context) (collect.Reading, int, error) {
		if calls.Add(1) == 1 {
			close(firstEntered)
			<-ctx.Done()
			<-unwindRelease

			return collect.Reading{}, -1, ctx.Err()
		}

		return collect.Reading{Table: &nvidiasmi.Table{}}, 0, nil
	}

	live := collect.NewLive(query, 0, nil, slogt.New(t))

	ctx, cancel := context.WithCancel(t.Context())

	got := make(chan collect.Snapshot, 1)

	go func() {
		got <- live.Latest(ctx)
	}()

	<-firstEntered
	cancel()
	<-got

	// the abandoned run is still unwinding; a new scrape must get a fresh
	// run instead of inheriting the cancellation
	snapshot := live.Latest(t.Context())

	assert.True(t, snapshot.Success)
	assert.Equal(t, int32(2), calls.Load())
}

func TestLiveAlreadyCancelledContextRunsNothing(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	query := func(_ context.Context) (collect.Reading, int, error) {
		calls.Add(1)

		return collect.Reading{Table: &nvidiasmi.Table{}}, 0, nil
	}

	live := collect.NewLive(query, 0, nil, slogt.New(t))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	snapshot := live.Latest(ctx)

	assert.False(t, snapshot.Attempted)
	assert.Nil(t, snapshot.Table)
	assert.Equal(t, int32(0), calls.Load(), "a dead scrape must not start a run")
}

func TestLiveSerializesConcurrentCalls(t *testing.T) {
	t.Parallel()

	var inFlight, overlaps atomic.Int32

	firstIn := make(chan struct{}, 1)
	release := make(chan struct{})
	query := func(_ context.Context) (collect.Reading, int, error) {
		if inFlight.Add(1) > 1 {
			overlaps.Add(1)
		}

		select {
		case firstIn <- struct{}{}:
		default:
		}

		<-release

		inFlight.Add(-1)

		return collect.Reading{Table: &nvidiasmi.Table{}}, 0, nil
	}

	live := collect.NewLive(query, 0, nil, slogt.New(t))

	done := make(chan struct{})

	for range 3 {
		go func() {
			live.Latest(t.Context())

			done <- struct{}{}
		}()
	}

	// one query is now definitely in flight and holding; give the other two
	// callers time to reach Latest, where a broken implementation would enter
	// the query concurrently and be counted as an overlap
	<-firstIn
	time.Sleep(100 * time.Millisecond)

	close(release)

	for range 3 {
		<-done
	}

	assert.Equal(t, int32(0), overlaps.Load())
}
