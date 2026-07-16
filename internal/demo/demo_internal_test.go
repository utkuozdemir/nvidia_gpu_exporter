package demo

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/demodata"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/fakesmi"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

func testSource() fakesmi.CaptureSource {
	return fakesmi.CaptureSource{FS: demodata.FS, Default: demodata.Default}
}

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// extrasFrom parses a full demo config document and decodes its extras block.
func extrasFrom(t *testing.T, doc string) (*extrasConfig, error) {
	t.Helper()

	cfg, err := fakesmi.ParseConfig([]byte(doc))
	require.NoError(t, err)

	return parseExtras(cfg.Extras())
}

//nolint:funlen // a table of config validation cases, long but flat
func TestParseExtrasValidation(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		doc     string
		wantErr string
	}{
		{
			name: "valid full config",
			doc: `extras:
  seed: 42
  pcie:
    tx-bytes-per-second: {min: 1e9, max: 2e9}
    rx-bytes-per-second: {min: 1e9, max: 2e9}
  xids:
    initial: [{gpu: 0, xid: 79, count: 2}]
    interval: 10m
    codes: [13, 31]
  mig:
    - gpu: 0
      instances:
        - {gi: 2, profile: 3g.71gb, cis: 2, busy: true}
        - {gi: 7, profile: 1g.18gb}
`,
		},
		{name: "no extras block", doc: "state: idle\n"},
		{
			name:    "unknown key rejected by the strict decoder",
			doc:     "extras:\n  bogus: 1\n",
			wantErr: "bogus",
		},
		{
			name: "duplicate gpu",
			doc: "extras:\n  mig:\n    - {gpu: 0, instances: [{gi: 1, profile: 1g.18gb}]}\n" +
				"    - {gpu: 0, instances: [{gi: 2, profile: 1g.18gb}]}\n",
			wantErr: "duplicate mig entry for gpu 0",
		},
		{
			name:    "duplicate gi",
			doc:     "extras:\n  mig:\n    - {gpu: 0, instances: [{gi: 1, profile: 1g.18gb}, {gi: 1, profile: 2g.35gb}]}\n",
			wantErr: "duplicate gi 1 on gpu 0",
		},
		{
			name:    "gpu without instances",
			doc:     "extras:\n  mig:\n    - {gpu: 0}\n",
			wantErr: "has no instances",
		},
		{
			name:    "instance without profile",
			doc:     "extras:\n  mig:\n    - {gpu: 0, instances: [{gi: 1}]}\n",
			wantErr: "needs a profile",
		},
		{
			name:    "profile without size token and no explicit bytes",
			doc:     "extras:\n  mig:\n    - {gpu: 0, instances: [{gi: 1, profile: weird}]}\n",
			wantErr: "carries no size token",
		},
		{
			name: "profile without size token but explicit bytes",
			doc:  "extras:\n  mig:\n    - {gpu: 0, instances: [{gi: 1, profile: weird, memory-total-bytes: 1024}]}\n",
		},
		{
			name:    "negative cis",
			doc:     "extras:\n  mig:\n    - {gpu: 0, instances: [{gi: 1, profile: 1g.18gb, cis: -1}]}\n",
			wantErr: "negative cis",
		},
		{
			name:    "pcie min above max",
			doc:     "extras:\n  pcie:\n    tx-bytes-per-second: {min: 5, max: 1}\n",
			wantErr: "must be finite and satisfy",
		},
		{
			name:    "pcie negative min",
			doc:     "extras:\n  pcie:\n    rx-bytes-per-second: {min: -1, max: 1}\n",
			wantErr: "must be finite and satisfy",
		},
		{
			name:    "bad xid interval",
			doc:     "extras:\n  xids:\n    interval: often\n",
			wantErr: "invalid xids interval",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := extrasFrom(t, testCase.doc)

			if testCase.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, testCase.wantErr)
			}
		})
	}
}

func TestExtrasDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := extrasFrom(t, "extras:\n  mig:\n    - {gpu: 0, instances: [{gi: 1, profile: 3g.71gb}]}\n")
	require.NoError(t, err)

	assert.Positive(t, cfg.PCIe.TXBytesPerSecond.Max)
	assert.Positive(t, cfg.PCIe.RXBytesPerSecond.Max)
	assert.NotEmpty(t, cfg.XIDs.Codes)
	assert.InDelta(t, float64(defaultEnergyFallbackPowerWatts), cfg.EnergyFallbackPowerWatts, 0)
	assert.Equal(t, 1, cfg.MIG[0].Instances[0].CIs, "cis must default to one")
	assert.Equal(t, uint64(71)<<30, cfg.MIG[0].Instances[0].MemoryTotalBytes,
		"memory must default to the profile's size token")
	assert.Equal(t, time.Duration(0), cfg.xidInterval(), "no interval means no ongoing events")
}

func TestProfileMemoryBytes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, uint64(71)<<30, profileMemoryBytes("3g.71gb"))
	assert.Equal(t, uint64(18)<<30, profileMemoryBytes("1g.18gb"))
	assert.Zero(t, profileMemoryBytes("weird"))
}

func TestMigUUIDStableAndDistinct(t *testing.T) {
	t.Parallel()

	first := migUUID("parent", 2, 0, "3g.71gb")

	assert.Equal(t, first, migUUID("parent", 2, 0, "3g.71gb"), "same tuple must reuse the uuid")
	assert.NotEqual(t, first, migUUID("parent", 2, 1, "3g.71gb"))
	assert.NotEqual(t, first, migUUID("parent", 7, 0, "3g.71gb"))
	assert.NotEqual(t, first, migUUID("other", 2, 0, "3g.71gb"))
	assert.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, first)
}

func TestSynthEnergyTrapezoid(t *testing.T) {
	t.Parallel()

	backend := &Backend{energy: map[string]*energyState{}}
	extras := &extrasConfig{EnergyFallbackPowerWatts: 120}
	start := time.Unix(1000, 0)

	var first collect.Reading

	backend.synthEnergy([]string{"u0"}, map[string]float64{"u0": 100}, extras, start, &first)
	require.Len(t, first.Extras.Energy, 1)
	assert.Zero(t, first.Extras.Energy[0].Joules, "the first sample must emit zero")

	var second collect.Reading

	backend.synthEnergy([]string{"u0"}, map[string]float64{"u0": 200}, extras, start.Add(10*time.Second), &second)
	assert.InDelta(t, 1500.0, second.Extras.Energy[0].Joules, 1e-9,
		"trapezoid of 100W to 200W over 10s")

	// no table power: the configured fallback keeps integrating
	var third collect.Reading

	backend.synthEnergy([]string{"u0"}, nil, extras, start.Add(20*time.Second), &third)
	assert.InDelta(t, 1500.0+(200+120)/2.0*10, third.Extras.Energy[0].Joules, 1e-9)

	// a GPU that left the config is forgotten; a new one starts at zero
	var fourth collect.Reading

	backend.synthEnergy([]string{"u1"}, nil, extras, start.Add(30*time.Second), &fourth)
	require.Len(t, fourth.Extras.Energy, 1)
	assert.Zero(t, fourth.Extras.Energy[0].Joules)
	assert.NotContains(t, backend.energy, "u0")
}

func TestSynthMIGShape(t *testing.T) {
	t.Parallel()

	seed := int64(1)
	backend := &Backend{rng: newDemoRand(&seed), seenGIs: map[string]bool{}}
	extras, err := extrasFrom(t, `extras:
  mig:
    - gpu: 0
      instances:
        - {gi: 2, profile: 3g.71gb, cis: 2, busy: true}
        - {gi: 7, profile: 1g.18gb}
    - gpu: 5
      instances:
        - {gi: 1, profile: 1g.18gb}
`)
	require.NoError(t, err)

	var reading collect.Reading

	backend.synthMIG([]string{"u0", "u1"}, extras, &reading)

	// gpu 5 is out of range and skipped; gpu 0 yields 2 CIs + 1 CI
	require.Len(t, reading.Extras.MIG, 3)

	first, second, third := reading.Extras.MIG[0], reading.Extras.MIG[1], reading.Extras.MIG[2]

	assert.Equal(t, "u0", first.ParentUUID)
	assert.Equal(t, "2", first.GPUInstanceID)
	assert.Equal(t, "0", first.ComputeInstanceID)
	assert.Equal(t, "1", second.ComputeInstanceID)
	assert.Equal(t, "1c.3g.71gb", first.Profile, "sliced instances carry the ci-qualified profile")
	assert.Equal(t, "1g.18gb", third.Profile)
	assert.NotEqual(t, first.UUID, second.UUID)

	// sibling CIs share the GPU-instance-scoped readings
	assert.Same(t, first.Memory, second.Memory)
	assert.NotSame(t, first.Memory, third.Memory)

	// like the real backend's GPM sampling, the first cycle that sees a GPU
	// instance serves no utilization
	assert.Nil(t, first.Utilization)
	assert.Nil(t, third.Utilization)

	// the memory invariant holds
	assert.Equal(t, first.Memory.Total, first.Memory.Used+first.Memory.Free+first.Memory.Reserved)

	// the second cycle serves utilization: the busy instance runs hot,
	// sibling CIs share it
	var next collect.Reading

	backend.synthMIG([]string{"u0", "u1"}, extras, &next)
	require.Len(t, next.Extras.MIG, 3)
	first, second, third = next.Extras.MIG[0], next.Extras.MIG[1], next.Extras.MIG[2]

	require.NotNil(t, first.Utilization)
	require.NotNil(t, third.Utilization)
	assert.Same(t, first.Utilization, second.Utilization)
	assert.Greater(t, *first.Utilization.SMActivityRatio, 0.5)
	assert.Less(t, *third.Utilization.SMActivityRatio, 0.1)
}

func TestSynthMIGDepartedInstanceStartsOver(t *testing.T) {
	t.Parallel()

	seed := int64(1)
	backend := &Backend{rng: newDemoRand(&seed), seenGIs: map[string]bool{}}
	extras, err := extrasFrom(t, "extras:\n  mig:\n    - {gpu: 0, instances: [{gi: 1, profile: 1g.18gb}]}\n")
	require.NoError(t, err)

	var first collect.Reading

	backend.synthMIG([]string{"u0"}, extras, &first)
	require.Nil(t, first.Extras.MIG[0].Utilization)

	// the instance leaves the topology for one cycle
	empty, err := extrasFrom(t, "state: idle\n")
	require.NoError(t, err)
	backend.synthMIG([]string{"u0"}, empty, &collect.Reading{})

	// back again: first-cycle semantics apply anew
	var back collect.Reading

	backend.synthMIG([]string{"u0"}, extras, &back)
	assert.Nil(t, back.Extras.MIG[0].Utilization)
}

func TestTickXIDsSeedsInitialOnce(t *testing.T) {
	t.Parallel()

	seed := int64(1)
	backend := &Backend{rng: newDemoRand(&seed), xids: map[string]map[uint64]*xidStat{}}
	extras, err := extrasFrom(t, `extras:
  xids:
    initial:
      - {gpu: 0, xid: 79, count: 2}
      - {gpu: 5, xid: 13, count: 1}
      - {gpu: 0, xid: 31, count: 0}
`)
	require.NoError(t, err)

	uuids := []string{"u0", "u1"}
	now := time.Unix(1000, 0)

	backend.tickXIDs(uuids, extras, now)
	backend.tickXIDs(uuids, extras, now.Add(time.Second))

	counters := backend.XIDCounts()
	require.Len(t, counters, 1, "out-of-range and zero-count events are skipped, seeding happens once")
	assert.Equal(t, "u0", counters[0].UUID)
	assert.Equal(t, uint64(79), counters[0].XID)
	assert.Equal(t, uint64(2), counters[0].Count)
}

func TestTickXIDsCadenceAndCatchUpBound(t *testing.T) {
	t.Parallel()

	seed := int64(1)
	backend := &Backend{rng: newDemoRand(&seed), xids: map[string]map[uint64]*xidStat{}}
	extras, err := extrasFrom(t, "extras:\n  xids:\n    interval: 1m\n")
	require.NoError(t, err)

	uuids := []string{"u0"}
	start := time.Unix(1000, 0)

	backend.tickXIDs(uuids, extras, start)
	assert.Empty(t, backend.XIDCounts(), "the first slot lies at least half an interval away")

	// a long pause covers dozens of slots but the catch-up is bounded
	backend.tickXIDs(uuids, extras, start.Add(time.Hour))

	var total uint64
	for _, counter := range backend.XIDCounts() {
		total += counter.Count
	}

	assert.Equal(t, uint64(10), total)
}

func TestAttributeApps(t *testing.T) {
	t.Parallel()

	extras, err := extrasFrom(t, `extras:
  mig:
    - gpu: 0
      instances:
        - {gi: 2, profile: 3g.71gb, cis: 2}
        - {gi: 7, profile: 1g.18gb}
`)
	require.NoError(t, err)

	reading := func() collect.Reading {
		return collect.Reading{Apps: []nvidiasmi.ComputeApp{
			{GPUUUID: "u0", PID: "101"},
			{GPUUUID: "u0", PID: "202"},
			{GPUUUID: "u1", PID: "303"},
		}}
	}

	first := reading()
	attributeApps([]string{"u0", "u1"}, extras, &first)

	for _, app := range first.Apps[:2] {
		assert.Contains(t, []string{"2", "7"}, app.GPUInstanceID, "pid %s", app.PID)
		assert.NotEmpty(t, app.ComputeInstanceID)
	}

	assert.Empty(t, first.Apps[2].GPUInstanceID, "a GPU without a topology keeps empty attribution")
	assert.Empty(t, first.Apps[2].ComputeInstanceID)

	second := reading()
	attributeApps([]string{"u0", "u1"}, extras, &second)
	assert.Equal(t, first.Apps, second.Apps, "the process-to-instance mapping must be stable")
}

func TestNewBuiltinConfigAndRunFunc(t *testing.T) {
	t.Parallel()

	backend, err := New(testSource(), "", testLogger())
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer

	cmd := &exec.Cmd{Args: []string{"demo-nvidia-smi", "--version"}, Stdout: &stdout, Stderr: &stderr}
	require.NoError(t, backend.RunFunc()(cmd))
	assert.Contains(t, stdout.String(), "CUDA")

	var bad bytes.Buffer

	cmd = &exec.Cmd{Args: []string{"demo-nvidia-smi", "--no-such-flag"}, Stdout: &bad, Stderr: &bad}
	require.ErrorContains(t, backend.RunFunc()(cmd), "demo invocation failed")
}

func TestNewInvalidConfigFailsStartup(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "demo.yaml")
	require.NoError(t, os.WriteFile(path, []byte("extras:\n  xids:\n    interval: often\n"), 0o600))

	_, err := New(testSource(), path, testLogger())
	require.ErrorContains(t, err, "invalid demo config")
}

func TestWrapQueryFuncReloadFailureFailsTheCollection(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "demo.yaml")
	require.NoError(t, os.WriteFile(path, []byte("extras:\n  seed: 1\n"), 0o600))

	backend, err := New(testSource(), path, testLogger())
	require.NoError(t, err)

	inner := func(_ context.Context) (collect.Reading, int, error) {
		return collect.Reading{}, 0, nil
	}
	wrapped := backend.WrapQueryFunc(inner)

	_, code, err := wrapped(t.Context())
	require.NoError(t, err)
	assert.Zero(t, code)

	// a broken edit fails that collection instead of crashing or serving
	// mismatched data
	require.NoError(t, os.WriteFile(path, []byte("extras:\n  bogus: 1\n"), 0o600))

	_, code, err = wrapped(t.Context())
	require.Error(t, err)
	assert.Equal(t, -1, code)

	// fixing the file recovers without a restart
	require.NoError(t, os.WriteFile(path, []byte("extras:\n  seed: 1\n"), 0o600))

	_, _, err = wrapped(t.Context())
	require.NoError(t, err)
}

func TestNewPreflightRejectsBadCaptureAndState(t *testing.T) {
	t.Parallel()

	for name, doc := range map[string]string{
		"missing capture": "capture: no-such-capture\n",
		"unknown state":   "state: exploded\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "demo.yaml")
			require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))

			_, err := New(testSource(), path, testLogger())
			require.ErrorContains(t, err, "cannot serve a GPU table")
		})
	}
}

func TestReconcileServesMIGModeEnabled(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "demo.yaml")
	doc := "gpus: 2\nextras:\n  mig:\n    - {gpu: 0, instances: [{gi: 1, profile: 1g.18gb}]}\n"
	require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))

	backend, err := New(testSource(), path, testLogger())
	require.NoError(t, err)

	var out bytes.Buffer

	cmd := &exec.Cmd{
		Args:   []string{"demo-nvidia-smi", "--query-gpu=mig.mode.current", "--format=csv"},
		Stdout: &out, Stderr: io.Discard,
	}
	require.NoError(t, backend.RunFunc()(cmd))

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 3, "a header and one row per simulated GPU")
	assert.Equal(t, "Enabled", strings.TrimSpace(lines[1]), "the MIG GPU must report the mode on")
	assert.NotEqual(t, "Enabled", strings.TrimSpace(lines[2]), "the plain GPU keeps the captured mode")
}

func TestReconcileRejectsOutOfRangeIndexes(t *testing.T) {
	t.Parallel()

	for name, doc := range map[string]string{
		"mig gpu": "gpus: 2\nextras:\n  mig:\n    - {gpu: 5, instances: [{gi: 1, profile: 1g.18gb}]}\n",
		"xid gpu": "extras:\n  xids:\n    initial: [{gpu: 3, xid: 79, count: 1}]\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "demo.yaml")
			require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))

			_, err := New(testSource(), path, testLogger())
			require.ErrorContains(t, err, "out of range")
		})
	}
}

func TestFailureInjectionIsIgnored(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "demo.yaml")
	require.NoError(t, os.WriteFile(path, []byte("exit: 7\ndelay: 5s\n"), 0o600))

	start := time.Now()

	backend, err := New(testSource(), path, testLogger())
	require.NoError(t, err, "failure injection must not reach the in-process path")

	var out bytes.Buffer

	cmd := &exec.Cmd{
		Args:   []string{"demo-nvidia-smi", "--query-gpu=uuid", "--format=csv"},
		Stdout: &out, Stderr: io.Discard,
	}
	require.NoError(t, backend.RunFunc()(cmd))
	assert.Less(t, time.Since(start), 2*time.Second, "the configured delay must not apply")
	assert.NotEmpty(t, out.String())
}

func TestBeginCycleResetsStateOnConfigChange(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "demo.yaml")
	require.NoError(t, os.WriteFile(path, []byte("extras:\n  seed: 1\n"), 0o600))

	backend, err := New(testSource(), path, testLogger())
	require.NoError(t, err)

	backend.energy["u0"] = &energyState{joules: 5}
	backend.xidsSeeded = true

	// an unchanged document keeps the accumulated state
	require.NoError(t, backend.beginCycle())
	assert.Contains(t, backend.energy, "u0")
	assert.True(t, backend.xidsSeeded)

	// a changed document resets it, so seed and initial-event edits apply
	require.NoError(t, os.WriteFile(path, []byte("extras:\n  seed: 2\n"), 0o600))
	require.NoError(t, backend.beginCycle())
	assert.Empty(t, backend.energy)
	assert.False(t, backend.xidsSeeded)
}

func TestWrapQueryFuncSerializesCycles(t *testing.T) {
	t.Parallel()

	backend, err := New(testSource(), "", testLogger())
	require.NoError(t, err)

	var active, peak atomic.Int64

	inner := func(_ context.Context) (collect.Reading, int, error) {
		current := active.Add(1)
		defer active.Add(-1)

		for {
			seen := peak.Load()
			if current <= seen || peak.CompareAndSwap(seen, current) {
				break
			}
		}

		time.Sleep(time.Millisecond)

		return collect.Reading{}, 0, nil
	}

	wrapped := backend.WrapQueryFunc(inner)

	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			_, _, err := wrapped(t.Context())
			assert.NoError(t, err)
		})
	}

	wg.Wait()

	assert.Equal(t, int64(1), peak.Load(), "cycles must never overlap: one snapshot rules one whole cycle")
}
