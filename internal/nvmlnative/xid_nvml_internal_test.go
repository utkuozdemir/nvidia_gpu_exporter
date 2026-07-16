//go:build linux && cgo

package nvmlnative

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"
	"github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// xidFake drives the watcher: a queue of events served by Wait, plus
// lifecycle accounting for the event sets.
type xidFake struct {
	mu      sync.Mutex
	pending []nvml.EventData
	creates atomic.Int64
	frees   atomic.Int64
	regs    atomic.Int64
	waiting atomic.Int64
	// holdWait simulates how long the driver's bounded wait blocks (as
	// nanoseconds); wedge, when non-nil, blocks the wait until closed.
	holdWait atomic.Int64
	wedge    chan struct{}
	regRet   nvml.Return
	waitRet  nvml.Return
}

func (x *xidFake) push(data nvml.EventData) {
	x.mu.Lock()
	defer x.mu.Unlock()

	x.pending = append(x.pending, data)
}

func (x *xidFake) wait(uint32) (nvml.EventData, nvml.Return) {
	x.waiting.Add(1)

	if x.wedge != nil {
		<-x.wedge
	}

	if x.waitRet != nvml.SUCCESS {
		return nvml.EventData{}, x.waitRet
	}

	x.mu.Lock()

	if len(x.pending) > 0 {
		data := x.pending[0]
		x.pending = x.pending[1:]
		x.mu.Unlock()

		return data, nvml.SUCCESS
	}

	x.mu.Unlock()

	// simulate the driver's bounded wait without burning a test's real
	// second per iteration (tests raise holdWait for realistic contention)
	hold := time.Duration(x.holdWait.Load())
	if hold == 0 {
		hold = time.Millisecond
	}

	time.Sleep(hold)

	return nvml.EventData{}, nvml.ERROR_TIMEOUT
}

// install wires the fake into the seam and the device.
func (x *xidFake) install(api *nvmlAPI, dev *mock.Device) {
	api.eventSetCreate = func() (nvml.EventSet, nvml.Return) {
		x.creates.Add(1)

		return &mock.EventSet{
			WaitFunc: x.wait,
			FreeFunc: func() nvml.Return {
				x.frees.Add(1)

				return nvml.SUCCESS
			},
		}, nvml.SUCCESS
	}

	dev.RegisterEventsFunc = func(uint64, nvml.EventSet) nvml.Return {
		if x.regRet != nvml.SUCCESS {
			return x.regRet
		}

		x.regs.Add(1)

		return nvml.SUCCESS
	}
}

// startWatcher runs the watcher until the test ends, reporting a wait group
// so teardown can assert the goroutine exited.
func startWatcher(t *testing.T, backend *Backend) (context.CancelFunc, *sync.WaitGroup) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	var wg sync.WaitGroup

	wg.Go(func() {
		assert.NoError(t, backend.RunXIDWatcher(ctx))
	})

	return cancel, &wg
}

func TestXIDWatcherCountsEvents(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	fake := &fakeAPI{devices: []nvml.Device{dev}}
	xid := &xidFake{}

	api := fake.api()
	xid.install(&api, dev)

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	xid.push(nvml.EventData{Device: dev, EventType: nvml.EventTypeXidCriticalError, EventData: 79})
	xid.push(nvml.EventData{Device: dev, EventType: nvml.EventTypeXidCriticalError, EventData: 79})
	xid.push(nvml.EventData{Device: dev, EventType: nvml.EventTypeXidCriticalError, EventData: 31})

	cancel, wg := startWatcher(t, backend)

	require.Eventually(t, func() bool {
		return len(backend.XIDCounts()) == 2
	}, 5*time.Second, 5*time.Millisecond, "both xid series must appear")

	counts := backend.XIDCounts()
	assert.Equal(t, "11111111-2222-3333-4444-555555555555", counts[0].UUID,
		"the uuid must come from the registration-time cache, normalized")
	assert.Equal(t, uint64(31), counts[0].XID, "output must be sorted")
	assert.Equal(t, uint64(1), counts[0].Count)
	assert.Equal(t, uint64(79), counts[1].XID)
	assert.Equal(t, uint64(2), counts[1].Count)
	assert.False(t, counts[0].LastSeen.IsZero())

	cancel()
	wg.Wait()

	assert.Equal(t, xid.creates.Load(), xid.frees.Load(), "every event set must be freed")
}

func TestXIDWatcherUnsupportedIdles(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	fake := &fakeAPI{devices: []nvml.Device{dev}}
	xid := &xidFake{regRet: nvml.ERROR_NOT_SUPPORTED}

	api := fake.api()
	xid.install(&api, dev)

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	cancel, wg := startWatcher(t, backend)

	require.Eventually(t, func() bool {
		return xid.creates.Load() >= 1
	}, 5*time.Second, 5*time.Millisecond)

	assert.Empty(t, backend.XIDCounts())

	cancel()
	wg.Wait()

	assert.Equal(t, xid.creates.Load(), xid.frees.Load(),
		"a set with no registrations must still be freed")
}

func TestXIDWatcherRebuildsAcrossGenerationsAndKeepsCounts(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	fake := &fakeAPI{devices: []nvml.Device{dev}}
	xid := &xidFake{}

	api := fake.api()
	xid.install(&api, dev)

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	xid.push(nvml.EventData{Device: dev, EventType: nvml.EventTypeXidCriticalError, EventData: 79})

	cancel, wg := startWatcher(t, backend)

	require.Eventually(t, func() bool {
		return len(backend.XIDCounts()) == 1
	}, 5*time.Second, 5*time.Millisecond)

	// a driver loss: the watcher must notice the dead generation, drive the
	// re-initialization itself (no scrape runs here), re-register, and keep
	// the counts
	regsBefore := xid.regs.Load()

	backend.mu.Lock()
	backend.markLost()
	backend.mu.Unlock()

	require.Eventually(t, func() bool {
		return xid.regs.Load() > regsBefore
	}, 10*time.Second, 5*time.Millisecond, "the watcher must recover and re-register without a scrape")

	assert.Equal(t, int64(2), fake.inits.Load(), "the watcher must have re-initialized NVML itself")

	xid.push(nvml.EventData{Device: dev, EventType: nvml.EventTypeXidCriticalError, EventData: 79})

	require.Eventually(t, func() bool {
		counts := backend.XIDCounts()

		return len(counts) == 1 && counts[0].Count == 2
	}, 5*time.Second, 5*time.Millisecond, "counts must survive the generation change")

	cancel()
	wg.Wait()

	// the first generation's set died with the shutdown that killed it (the
	// watcher must NOT free it afterwards); only the second one is freed
	assert.Equal(t, xid.creates.Load()-1, xid.frees.Load(),
		"exactly the dead generation's set must stay unfreed")
}

func TestXIDWatcherWaitFailureRebuildsWithBackoff(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	fake := &fakeAPI{devices: []nvml.Device{dev}}
	xid := &xidFake{waitRet: nvml.ERROR_UNKNOWN}

	api := fake.api()
	xid.install(&api, dev)

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	cancel, wg := startWatcher(t, backend)

	// the failing set is freed and rebuilt (with backoff, so give it time
	// for at least one full rebuild round)
	require.Eventually(t, func() bool {
		return xid.creates.Load() >= 2 && xid.frees.Load() >= 1
	}, 10*time.Second, 5*time.Millisecond)

	// and the rebuilds are paced: a persistent wait failure must never spin
	// the register/wait/free loop at full speed
	time.Sleep(2 * time.Second)
	assert.Less(t, xid.creates.Load(), int64(8),
		"wait failures must back off between rebuilds, not hot-loop")

	cancel()
	wg.Wait()

	assert.Equal(t, xid.creates.Load(), xid.frees.Load(),
		"the generation stayed alive, so every set must be freed")
}

func TestMarkLostShutsDownWhileWatcherIsHealthy(t *testing.T) {
	t.Parallel()

	// the empirically pinned barrier property: a healthy watcher holds the
	// shared lock for the full duration of each bounded wait, and shutdown
	// must still acquire the exclusive lock within roughly one wait bound
	// (a polling acquisition provably cannot; see tryLifecycleLock)
	dev := identityDevice()
	fake := &fakeAPI{devices: []nvml.Device{dev}}
	xid := &xidFake{}

	api := fake.api()
	xid.install(&api, dev)

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	// make each wait hold the shared lock for a realistic while
	xid.holdWait.Store(int64(200 * time.Millisecond))

	cancel, wg := startWatcher(t, backend)

	require.Eventually(t, func() bool {
		return xid.regs.Load() >= 1
	}, 5*time.Second, 5*time.Millisecond)

	start := time.Now()

	backend.mu.Lock()
	backend.markLost()
	backend.mu.Unlock()

	elapsed := time.Since(start)

	assert.Equal(t, int64(1), fake.shutdowns.Load(),
		"a healthy watcher must not make shutdown miss its window")
	assert.Less(t, elapsed, lifecycleLockDeadline,
		"the exclusive acquisition must land within the wait bound, not hit the deadline")

	cancel()
	wg.Wait()
}

func TestMarkLostSkipsShutdownBehindWedgedWait(t *testing.T) {
	t.Parallel()

	dev := identityDevice()
	fake := &fakeAPI{devices: []nvml.Device{dev}}
	xid := &xidFake{}

	api := fake.api()
	xid.install(&api, dev)

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	// wedge the driver call: Wait blocks until released, far beyond the
	// shutdown deadline
	xid.wedge = make(chan struct{})

	cancel, wg := startWatcher(t, backend)

	require.Eventually(t, func() bool {
		return xid.waiting.Load() >= 1
	}, 5*time.Second, 5*time.Millisecond, "the watcher must be inside the wedged wait")

	start := time.Now()

	backend.mu.Lock()
	backend.markLost()
	backend.mu.Unlock()

	assert.Equal(t, int64(0), fake.shutdowns.Load(),
		"shutdown must be skipped, never executed under a wedged driver call")
	assert.GreaterOrEqual(t, time.Since(start), lifecycleLockDeadline,
		"the skip must come from the bounded deadline")

	// releasing the wedge lets the watcher notice the dead generation and
	// park; the late-acquire cleanup must unlock the barrier so recovery
	// (which flips markLost again) does not deadlock
	close(xid.wedge)

	require.Eventually(t, func() bool {
		return fake.inits.Load() >= 2
	}, 10*time.Second, 5*time.Millisecond, "the watcher must recover once the wedge clears")

	cancel()
	wg.Wait()
}
