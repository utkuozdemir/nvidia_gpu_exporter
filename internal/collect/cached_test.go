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

	query := func(_ context.Context) (*nvidiasmi.Table, int, error) {
		calls.Add(1)

		return table, 0, nil
	}

	cached := collect.NewCached(query, time.Hour, 0, nil, slogt.New(t))
	startCached(t, cached)

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

	first := cached.Latest(t.Context())
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

	query := func(_ context.Context) (*nvidiasmi.Table, int, error) {
		calls.Add(1)

		return &nvidiasmi.Table{}, 0, nil
	}

	cached := collect.NewCached(query, 10*time.Millisecond, 0, nil, slogt.New(t))
	startCached(t, cached)

	assert.Eventually(t, func() bool {
		return calls.Load() >= 3
	}, 5*time.Second, time.Millisecond)
}

func TestCachedFirstScrapeBlocksUntilFirstCollection(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	query := func(ctx context.Context) (*nvidiasmi.Table, int, error) {
		select {
		case <-release:
			return &nvidiasmi.Table{}, 0, nil
		case <-ctx.Done():
			return nil, -1, ctx.Err()
		}
	}

	cached := collect.NewCached(query, time.Hour, 0, nil, slogt.New(t))
	startCached(t, cached)

	got := make(chan collect.Snapshot, 1)

	go func() {
		got <- cached.Latest(t.Context())
	}()

	// the reader must be blocked while the first collection is in flight
	select {
	case <-got:
		t.Fatal("Latest returned before the first collection completed")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)

	snapshot := <-got
	assert.True(t, snapshot.Success)
}

func TestCachedNotReadyWhenScrapeWinsTheRace(t *testing.T) {
	t.Parallel()

	query := func(ctx context.Context) (*nvidiasmi.Table, int, error) {
		// never completes on its own; unblocked only by shutdown cancellation
		<-ctx.Done()

		return nil, -1, ctx.Err()
	}

	cached := collect.NewCached(query, time.Hour, 0, nil, slogt.New(t))
	startCached(t, cached)

	// the reader gives up before the first collection completes
	readCtx, readCancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer readCancel()

	snapshot := cached.Latest(readCtx)

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
	snapshot := cached.Latest(t.Context())
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

	require.True(t, cached.Latest(ctx).Success)

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

	query := func(ctx context.Context) (*nvidiasmi.Table, int, error) {
		<-ctx.Done()

		return nil, -1, ctx.Err()
	}

	cached := collect.NewCached(query, time.Hour, 50*time.Millisecond, nil, slogt.New(t))
	startCached(t, cached)

	snapshot := cached.Latest(t.Context())

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
