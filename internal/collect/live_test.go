package collect_test

import (
	"context"
	"errors"
	"os/exec"
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
	return func(_ context.Context) (*nvidiasmi.Table, int, error) {
		return table, exitCode, err
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
	query := func(_ context.Context) (*nvidiasmi.Table, int, error) {
		calls++
		if calls == 1 {
			return table, 0, nil
		}

		return nil, -1, errQueryFailed
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

	query := func(ctx context.Context) (*nvidiasmi.Table, int, error) {
		<-ctx.Done()

		return nil, -1, ctx.Err()
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

	query := func(ctx context.Context) (*nvidiasmi.Table, int, error) {
		// with a zero timeout the context must not carry a deadline; a plain
		// context.WithTimeout(ctx, 0) would be expired already
		_, hasDeadline := ctx.Deadline()
		assert.False(t, hasDeadline)

		require.NoError(t, ctx.Err())

		return &nvidiasmi.Table{}, 0, nil
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
	query := func(ctx context.Context) (*nvidiasmi.Table, int, error) {
		<-ctx.Done()

		return nil, 3, exitErr
	}

	live := collect.NewLive(query, 50*time.Millisecond, onFatal, slogt.New(t))
	snapshot := live.Latest(t.Context())

	assert.False(t, snapshot.Success)
	require.NoError(t, gotFatal)
}

func TestLiveSerializesConcurrentCalls(t *testing.T) {
	t.Parallel()

	var inFlight, maxInFlight int

	block := make(chan struct{})
	query := func(_ context.Context) (*nvidiasmi.Table, int, error) {
		// no synchronization needed: Live's lock serializes these
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}

		<-block

		inFlight--

		return &nvidiasmi.Table{}, 0, nil
	}

	live := collect.NewLive(query, 0, nil, slogt.New(t))

	done := make(chan struct{})

	for range 3 {
		go func() {
			live.Latest(t.Context())

			done <- struct{}{}
		}()
	}

	close(block)

	for range 3 {
		<-done
	}

	assert.Equal(t, 1, maxInFlight)
}
