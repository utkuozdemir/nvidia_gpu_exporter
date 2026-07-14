package collect_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

// awaitFirstCollection waits until the source has a first outcome to serve:
// reads never block on collection, so tests interested in a collection's
// outcome have to poll for it.
func awaitFirstCollection(t *testing.T, cached *collect.Cached) collect.Snapshot {
	t.Helper()

	var snapshot collect.Snapshot

	require.Eventually(t, func() bool {
		snapshot = cached.Latest(t.Context())

		return snapshot.Attempted
	}, 5*time.Second, time.Millisecond)

	return snapshot
}

// startCached runs the source in the background and stops it on test cleanup.
func startCached(t *testing.T, cached *collect.Cached) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)

	go func() {
		done <- cached.Run(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})
}

func TestCachedServesCacheWithoutRecollecting(t *testing.T) {
	t.Parallel()

	table := &nvidiasmi.Table{}

	var calls atomic.Int64

	query := func(_ context.Context) (collect.Reading, int, error) {
		calls.Add(1)

		return collect.Reading{Table: table}, 0, nil
	}

	cached := collect.NewCached(query, time.Hour, 0, nil, slogt.New(t))
	startCached(t, cached)
	awaitFirstCollection(t, cached)

	for range 5 {
		snapshot := cached.Latest(t.Context())

		assert.True(t, snapshot.Attempted)
		assert.True(t, snapshot.Success)
		assert.Same(t, table, snapshot.Table)
	}

	// five reads, but only the initial background collection ran
	assert.Equal(t, int64(1), calls.Load())
}

func TestCachedFailureDropsTableAndCountsOnce(t *testing.T) {
	t.Parallel()

	query := staticQuery(nil, -1, errQueryFailed)

	cached := collect.NewCached(query, time.Hour, 0, nil, slogt.New(t))
	startCached(t, cached)

	first := awaitFirstCollection(t, cached)
	second := cached.Latest(t.Context())

	assert.True(t, first.Attempted)
	assert.False(t, first.Success)
	assert.Nil(t, first.Table)

	// the failure was counted per collection, not per read
	assert.Equal(t, uint64(1), first.Failures)
	assert.Equal(t, uint64(1), second.Failures)
}

func TestCachedCollectsOnTicks(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64

	query := func(_ context.Context) (collect.Reading, int, error) {
		calls.Add(1)

		return collect.Reading{Table: &nvidiasmi.Table{}}, 0, nil
	}

	cached := collect.NewCached(query, 10*time.Millisecond, 0, nil, slogt.New(t))
	startCached(t, cached)

	assert.Eventually(t, func() bool {
		return calls.Load() >= 3
	}, 5*time.Second, time.Millisecond)
}

func TestCachedFirstScrapeServesNotReadyImmediately(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	query := func(ctx context.Context) (collect.Reading, int, error) {
		select {
		case <-release:
			return collect.Reading{Table: &nvidiasmi.Table{}}, 0, nil
		case <-ctx.Done():
			return collect.Reading{}, -1, ctx.Err()
		}
	}

	cached := collect.NewCached(query, time.Millisecond, 0, nil, slogt.New(t))
	startCached(t, cached)

	// the first collection is in flight; a read must not wait for it and must
	// report the not-yet-collected state instead
	snapshot := cached.Latest(t.Context())

	assert.False(t, snapshot.Attempted)
	assert.False(t, snapshot.Success)
	assert.Nil(t, snapshot.Table)
	assert.Equal(t, uint64(0), snapshot.Failures)

	close(release)

	assert.Eventually(t, func() bool {
		return cached.Latest(t.Context()).Success
	}, 5*time.Second, time.Millisecond)
}

func TestCachedNeverBlocksOnWedgedCollection(t *testing.T) {
	t.Parallel()

	query := func(ctx context.Context) (collect.Reading, int, error) {
		// never completes on its own; unblocked only by shutdown cancellation.
		// with a zero collection timeout this simulates an nvidia-smi stuck in
		// an uninterruptible wait
		<-ctx.Done()

		return collect.Reading{}, -1, ctx.Err()
	}

	cached := collect.NewCached(query, time.Hour, 0, nil, slogt.New(t))
	startCached(t, cached)

	start := time.Now()
	snapshot := cached.Latest(t.Context())

	assert.Less(t, time.Since(start), 5*time.Second)
	assert.False(t, snapshot.Attempted)
	assert.False(t, snapshot.Success)
	assert.Nil(t, snapshot.Table)
	assert.Equal(t, uint64(0), snapshot.Failures)
}

func TestCachedRunIsSingleUse(t *testing.T) {
	t.Parallel()

	cached := collect.NewCached(staticQuery(&nvidiasmi.Table{}, 0, nil), time.Hour, 0, nil, slogt.New(t))
	startCached(t, cached)

	// wait until the first Run is up and serving
	snapshot := awaitFirstCollection(t, cached)
	require.True(t, snapshot.Success)

	require.Error(t, cached.Run(t.Context()))
}

func TestCachedRunStopsOnCancel(t *testing.T) {
	t.Parallel()

	cached := collect.NewCached(staticQuery(&nvidiasmi.Table{}, 0, nil), time.Millisecond, 0, nil, slogt.New(t))

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)

	go func() {
		done <- cached.Run(ctx)
	}()

	require.Eventually(t, func() bool {
		return cached.Latest(ctx).Success
	}, 5*time.Second, time.Millisecond)

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop on context cancellation")
	}
}

func TestCachedTimeoutBoundsCollection(t *testing.T) {
	t.Parallel()

	query := func(ctx context.Context) (collect.Reading, int, error) {
		<-ctx.Done()

		return collect.Reading{}, -1, ctx.Err()
	}

	cached := collect.NewCached(query, time.Hour, 50*time.Millisecond, nil, slogt.New(t))
	startCached(t, cached)

	snapshot := awaitFirstCollection(t, cached)

	assert.True(t, snapshot.Attempted)
	assert.False(t, snapshot.Success)
	require.ErrorIs(t, snapshot.Err, context.DeadlineExceeded)
}

func TestCachedConcurrentReadsDuringCollections(t *testing.T) {
	t.Parallel()

	cached := collect.NewCached(
		staticQuery(&nvidiasmi.Table{}, 0, nil),
		time.Millisecond,
		0,
		nil,
		slogt.New(t),
	)
	startCached(t, cached)
	awaitFirstCollection(t, cached)

	done := make(chan struct{})

	for range 4 {
		go func() {
			defer func() { done <- struct{}{} }()

			for range 100 {
				snapshot := cached.Latest(t.Context())
				assert.True(t, snapshot.Attempted)
			}
		}()
	}

	for range 4 {
		<-done
	}
}
