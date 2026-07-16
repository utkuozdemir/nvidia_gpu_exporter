package demo

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/fakesmi"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

// defaultConfigYAML is the built-in configuration served when no
// --demo-config is given: a fluctuating two-GPU H200 box with a MIG topology
// on the first card (mirroring a live-verified layout), pre-seeded XID
// history and slow ongoing events, so every metric family shows something
// out of the box.
//
//go:embed default-config.yaml
var defaultConfigYAML []byte

// snapshot is one collection cycle's immutable configuration: the fake's
// document and the decoded extras, always swapped together so an atomic
// config edit can never produce mismatched table and extras within a cycle.
type snapshot struct {
	cfg    *fakesmi.Config
	extras *extrasConfig
	// hash fingerprints the raw config document. A changed document resets
	// the synthesized state (rng, counters, seeded events), like a driver
	// reload would: the retained state's meaning is tied to the scenario
	// that produced it.
	hash [sha256.Size]byte
}

// Backend synthesizes the nvml-superset metric surface without hardware. It
// wraps the exec pipeline: the table and per-process data come from the
// in-process fake nvidia-smi, and the extras families are synthesized on
// top, coherent with the served table.
type Backend struct {
	source     fakesmi.CaptureSource
	configPath string
	logger     *slog.Logger

	// cycleMu serializes whole collection cycles: one configuration
	// snapshot rules a cycle from its reload through the queries to the
	// extras overlay, so an overlapping abandoned collection can never mix
	// one config's table with another config's extras. Cycles are pure
	// in-memory work (failure injection is stripped), so holding the lock
	// across a cycle costs microseconds.
	cycleMu sync.Mutex

	mu      sync.Mutex
	current snapshot
	// now is the clock, injectable so the energy integration is testable.
	now func() time.Time
	rng *demoRand

	energy map[string]*energyState
	xids   map[string]map[uint64]*xidStat
	// xidsSeeded records that the initial events were applied (they resolve
	// GPU indexes against the first served table).
	xidsSeeded bool
	nextXIDAt  time.Time
	// seenGIs tracks the GPU instances previous cycles served: like the
	// real backend's GPM sampling, utilization needs a sample pair, so the
	// first cycle that sees a GPU instance serves no utilization for it.
	seenGIs map[string]bool
}

// energyState is one GPU's energy integration state.
type energyState struct {
	joules    float64
	lastPower float64
	lastAt    time.Time
}

// xidStat is one (GPU, XID code) pair's running state.
type xidStat struct {
	count uint64
	last  time.Time
}

// New loads and validates the initial configuration (the built-in default
// when path is empty) and builds the backend. Configuration problems are
// startup errors; a later edit breaking the file fails that collection
// instead.
func New(source fakesmi.CaptureSource, path string, logger *slog.Logger) (*Backend, error) {
	backend := &Backend{
		source:     source,
		configPath: path,
		logger:     logger,
		now:        time.Now,
	}

	snap, err := backend.loadSnapshot()
	if err != nil {
		return nil, err
	}

	if err := backend.preflight(snap); err != nil {
		return nil, err
	}

	backend.current = snap
	backend.resetState(snap)

	return backend, nil
}

// RunFunc adapts the in-process fake to the exec pipeline's runner seam: the
// prepared command's arguments are answered from the current cycle's
// snapshot, with no subprocess. Failure-injection exit codes and delay
// cancellation are exec-flavor features the in-process path deliberately
// does not reproduce.
func (b *Backend) RunFunc() nvidiasmi.RunFunc {
	return func(cmd *exec.Cmd) error {
		b.mu.Lock()
		snap := b.current
		b.mu.Unlock()

		code := fakesmi.RunWith(b.source, snap.cfg, cmd.Args[1:], cmd.Stdout, cmd.Stderr)
		if code != 0 {
			return fmt.Errorf("demo invocation failed with code %d", code)
		}

		return nil
	}
}

// WrapQueryFunc turns the exec-built collection into a demo cycle: one
// configuration snapshot is loaded up front and every part of the cycle (the
// GPU query, the per-process query, the extras synthesis) works from it.
// Whole cycles are serialized: an abandoned collection overlapping its
// replacement (the documented live-collection behavior) must not interleave
// two configurations within one cycle. Cycles are pure in-memory work
// (failure injection is stripped), so the serialization costs microseconds.
func (b *Backend) WrapQueryFunc(inner collect.QueryFunc) collect.QueryFunc {
	return func(ctx context.Context) (collect.Reading, int, error) {
		b.cycleMu.Lock()
		defer b.cycleMu.Unlock()

		if err := b.beginCycle(); err != nil {
			return collect.Reading{}, -1, err
		}

		reading, code, err := inner(ctx)
		if err != nil {
			return reading, code, err
		}

		b.overlay(&reading)

		return reading, code, nil
	}
}

// XIDCounts serves the accumulated synthetic XID events to the exporter,
// mirroring the real source's semantics: cumulative, sorted, visible at
// scrape time independent of collection success.
func (b *Backend) XIDCounts() []collect.XIDCounter {
	b.mu.Lock()
	defer b.mu.Unlock()

	var counters []collect.XIDCounter

	for uuid, perGPU := range b.xids {
		for xid, stat := range perGPU {
			counters = append(counters, collect.XIDCounter{
				UUID:     uuid,
				XID:      xid,
				Count:    stat.count,
				LastSeen: stat.last,
			})
		}
	}

	sortXIDCounters(counters)

	return counters
}

// preflight proves the configuration actually serves a GPU table: the capture
// must exist, hold the requested state, and the identity/override resolution
// must go through. Without it, a bad capture name would pass the YAML
// parsing, start an apparently healthy exporter, and then fail every
// collection.
func (b *Backend) preflight(snap snapshot) error {
	var stderr bytes.Buffer

	args := []string{"--query-gpu=uuid", "--format=csv"}
	if code := fakesmi.RunWith(b.source, snap.cfg, args, io.Discard, &stderr); code != 0 {
		return fmt.Errorf("the demo config cannot serve a GPU table: %s", strings.TrimSpace(stderr.String()))
	}

	return nil
}

// resetState reinitializes everything derived from the configuration
// scenario. Called at startup and when a live edit changes the document.
func (b *Backend) resetState(snap snapshot) {
	b.rng = newDemoRand(snap.extras.Seed)
	b.energy = map[string]*energyState{}
	b.xids = map[string]map[uint64]*xidStat{}
	b.xidsSeeded = false
	b.nextXIDAt = time.Time{}
	b.seenGIs = map[string]bool{}
}

// loadSnapshot reads and validates one immutable configuration, reconciling
// the fake's document with the extras so the served table cannot contradict
// the synthesized families.
func (b *Backend) loadSnapshot() (snapshot, error) {
	data := defaultConfigYAML

	if b.configPath != "" {
		var err error

		data, err = os.ReadFile(b.configPath)
		if err != nil {
			return snapshot{}, fmt.Errorf("failed to load the demo config: %w", err)
		}
	}

	cfg, err := fakesmi.ParseConfig(data)
	if err != nil {
		return snapshot{}, fmt.Errorf("failed to load the demo config: %w", err)
	}

	extras, err := parseExtras(cfg.Extras())
	if err != nil {
		return snapshot{}, fmt.Errorf("invalid demo config: %w", err)
	}

	if err := reconcile(cfg, extras); err != nil {
		return snapshot{}, fmt.Errorf("invalid demo config: %w", err)
	}

	return snapshot{cfg: cfg, extras: extras, hash: sha256.Sum256(data)}, nil
}

// reconcile aligns the fake's table with the extras. GPU indexes the table
// cannot serve are configuration errors, not silent no-ops. GPUs carrying a
// MIG topology report MIG mode enabled (unless the config explicitly
// overrides the field), because a real GPU can never serve MIG instances
// while reporting the mode off. The failure-injection settings are stripped:
// they exist to test the exec pipeline's subprocess handling, which the
// in-process path does not reproduce.
func reconcile(cfg *fakesmi.Config, extras *extrasConfig) error {
	cfg.StripFailureInjection()

	for _, gpu := range extras.MIG {
		for _, field := range []string{"mig.mode.current", "mig.mode.pending"} {
			if err := cfg.EnsureFieldOverride(gpu.GPU, field, "Enabled"); err != nil {
				return fmt.Errorf("mig entry: %w", err)
			}
		}
	}

	for _, event := range extras.XIDs.Initial {
		if event.GPU >= cfg.GPUCount() {
			return fmt.Errorf("initial xid event: gpu index %d is out of range: the config simulates %d GPU(s)",
				event.GPU, cfg.GPUCount())
		}
	}

	return nil
}

// beginCycle refreshes the configuration snapshot from disk, preserving the
// edit-without-restart loop. A changed document also resets the synthesized
// state, so seed and pre-seeded event edits take effect like everything
// else. The built-in default never changes.
func (b *Backend) beginCycle() error {
	if b.configPath == "" {
		return nil
	}

	snap, err := b.loadSnapshot()
	if err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if snap.hash != b.current.hash {
		b.logger.Info("demo config changed, resetting the synthesized state")
		b.resetState(snap)
	}

	b.current = snap

	return nil
}

// overlay synthesizes the extras families onto one cycle's reading, coherent
// with the served table (the energy counter integrates the table's own power
// draw). All retained state is mutex-guarded: abandoned live collections can
// overlap their replacements.
func (b *Backend) overlay(reading *collect.Reading) {
	b.mu.Lock()
	defer b.mu.Unlock()

	snap := b.current
	uuids, power := tableIdentity(reading.Table)
	b.privatePower(snap, uuids, power)

	now := b.now()

	b.synthEnergy(uuids, power, snap.extras, now, reading)
	b.synthPCIe(uuids, snap.extras, reading)
	b.synthMIG(uuids, snap.extras, reading)
	attributeApps(uuids, snap.extras, reading)
	b.tickXIDs(uuids, snap.extras, now)
}

// privatePower fills the power draws the public query left out, from the
// same configuration snapshot, the way the real backend reads the driver
// independently of the query plan: excluding the power field from the
// metrics must not change what the energy counter integrates. Rows whose
// power is not a number keep no entry and fall back to the configured watts.
func (b *Backend) privatePower(snap snapshot, uuids []string, power map[string]float64) {
	missing := false

	for _, uuid := range uuids {
		if _, exists := power[uuid]; !exists {
			missing = true

			break
		}
	}

	if !missing {
		return
	}

	var out bytes.Buffer

	args := []string{"--query-gpu=uuid,power.draw", "--format=csv"}
	if code := fakesmi.RunWith(b.source, snap.cfg, args, &out, io.Discard); code != 0 {
		return
	}

	lines := strings.SplitN(out.String(), "\n", 2)
	if len(lines) < 2 {
		return
	}

	// the first line is the CSV header
	for line := range strings.Lines(lines[1]) {
		uuid, raw, found := strings.Cut(strings.TrimSpace(line), ", ")
		if !found {
			continue
		}

		normalized := nvidiasmi.NormalizeUUID(uuid)
		if _, exists := power[normalized]; exists {
			continue
		}

		if watts, err := nvidiasmi.TransformFieldValue("power.draw", raw, 1); err == nil {
			power[normalized] = watts
		}
	}
}

// tableIdentity extracts the served GPUs' identity in row order: the uuid
// list (index-aligned with the config's GPU keying) and each GPU's power
// draw when the field was part of the query.
func tableIdentity(table *nvidiasmi.Table) ([]string, map[string]float64) {
	if table == nil {
		return nil, nil
	}

	uuids := make([]string, 0, len(table.Rows))
	power := make(map[string]float64, len(table.Rows))

	for _, row := range table.Rows {
		uuid := nvidiasmi.NormalizeUUID(row.QFieldToCells[nvidiasmi.UUIDQField].RawValue)
		uuids = append(uuids, uuid)

		if cell, ok := row.QFieldToCells["power.draw"]; ok {
			if watts, err := nvidiasmi.TransformFieldValue("power.draw", cell.RawValue, 1); err == nil {
				power[uuid] = watts
			}
		}
	}

	return uuids, power
}
