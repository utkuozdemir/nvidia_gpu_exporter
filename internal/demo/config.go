// Package demo implements the demo backend: the exec pipeline running
// against the in-process fake nvidia-smi, plus synthesizers for the metric
// families only the nvml backend can serve on real hardware (energy, PCIe
// throughput, per-MIG-instance readings, XID error counters). It is pure Go:
// no driver, no cgo, any platform.
package demo

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// extrasConfig is the demo-specific `extras:` block of the shared fake-smi
// config file. Every part is optional; built-in defaults keep all families
// alive.
type extrasConfig struct {
	// Seed drives every synthesized draw; nil derives one from the clock.
	Seed *int64 `yaml:"seed"`
	// PCIe sets the throughput jitter ranges, in bytes per second.
	PCIe *pcieConfig `yaml:"pcie"`
	// XIDs configures the error-event emitter.
	XIDs *xidConfig `yaml:"xids"`
	// MIG lists per-GPU MIG topologies, keyed by the simulated GPU's list
	// index (the same keying as the fake's per-GPU overrides).
	MIG []migGPUConfig `yaml:"mig"`
	// EnergyFallbackPowerWatts integrates the energy counter when the GPU
	// query does not include the power field (an explicit field selection
	// may exclude it; the counter must not depend on the public schema).
	EnergyFallbackPowerWatts float64 `yaml:"energy-fallback-power-watts"` //nolint:tagliatelle // kebab-case config keys
}

// pcieConfig is the PCIe throughput jitter, bytes per second.
type pcieConfig struct {
	TXBytesPerSecond rangeCfg `yaml:"tx-bytes-per-second"` //nolint:tagliatelle // kebab-case config keys
	RXBytesPerSecond rangeCfg `yaml:"rx-bytes-per-second"` //nolint:tagliatelle // kebab-case config keys
}

// rangeCfg is an inclusive numeric range.
type rangeCfg struct {
	Min float64 `yaml:"min"`
	Max float64 `yaml:"max"`
}

// xidConfig drives the XID emitter: a static set applied at startup (so the
// families are populated immediately; real series only appear after a first
// event) and an optional cadence for motion.
type xidConfig struct {
	Initial []xidEventConfig `yaml:"initial"`
	// Interval is the mean spacing of ongoing random events; empty or zero
	// disables them (deterministic, golden-test friendly).
	Interval string `yaml:"interval"`
	// Codes is the pool ongoing events draw from.
	Codes []uint64 `yaml:"codes"`
}

// xidEventConfig is one pre-seeded event batch.
type xidEventConfig struct {
	GPU   int    `yaml:"gpu"`
	XID   uint64 `yaml:"xid"`
	Count uint64 `yaml:"count"`
}

// migGPUConfig is one simulated GPU's MIG topology.
type migGPUConfig struct {
	GPU       int                 `yaml:"gpu"`
	Instances []migInstanceConfig `yaml:"instances"`
}

// migInstanceConfig is one GPU instance.
type migInstanceConfig struct {
	GI      int    `yaml:"gi"`
	Profile string `yaml:"profile"`
	// CIs is the number of compute instances sharing the GPU instance;
	// defaults to one.
	CIs int `yaml:"cis"`
	// MemoryTotalBytes overrides the framebuffer size; the default is
	// parsed from the profile name's size token.
	MemoryTotalBytes uint64 `yaml:"memory-total-bytes"` //nolint:tagliatelle // kebab-case config keys
	// Busy marks the instance that reports high activity; the others idle.
	Busy bool `yaml:"busy"`
}

// defaultXIDCodes is the pool ongoing events draw from: the codes commonly
// seen in the wild (application faults, ECC, thermal, bus errors).
//
//nolint:gochecknoglobals // static default table
var defaultXIDCodes = []uint64{13, 31, 43, 48, 63, 79, 119}

// defaultEnergyFallbackPowerWatts integrates energy when no power reading is
// available anywhere.
const defaultEnergyFallbackPowerWatts = 120

// profileSizeRe extracts the size token of a MIG profile name ("3g.71gb").
var profileSizeRe = regexp.MustCompile(`\.(\d+)gb$`)

// parseExtras decodes and validates the extras block; a nil node yields the
// defaults.
func parseExtras(node *yaml.Node) (*extrasConfig, error) {
	cfg := &extrasConfig{}

	if node != nil {
		var buf bytes.Buffer
		if err := yaml.NewEncoder(&buf).Encode(node); err != nil {
			return nil, fmt.Errorf("failed to re-encode the extras block: %w", err)
		}

		dec := yaml.NewDecoder(&buf)
		dec.KnownFields(true)

		if err := dec.Decode(cfg); err != nil {
			return nil, fmt.Errorf("failed to parse the extras block: %w", err)
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	cfg.applyDefaults()

	return cfg, nil
}

// validate rejects inconsistent extras before they can produce misleading
// series.
//
//nolint:cyclop // one linear rule list
func (c *extrasConfig) validate() error {
	if c.PCIe != nil {
		for name, rng := range map[string]rangeCfg{
			"tx-bytes-per-second": c.PCIe.TXBytesPerSecond,
			"rx-bytes-per-second": c.PCIe.RXBytesPerSecond,
		} {
			if !isFinite(rng.Min) || !isFinite(rng.Max) || rng.Min < 0 || rng.Max < rng.Min {
				return fmt.Errorf("pcie %s must be finite and satisfy 0 <= min <= max", name)
			}
		}
	}

	if !isFinite(c.EnergyFallbackPowerWatts) || c.EnergyFallbackPowerWatts < 0 {
		return errors.New("energy-fallback-power-watts must be a finite non-negative number")
	}

	if err := c.validateXIDs(); err != nil {
		return err
	}

	seenGPU := map[int]bool{}

	for _, gpu := range c.MIG {
		if seenGPU[gpu.GPU] {
			return fmt.Errorf("duplicate mig entry for gpu %d", gpu.GPU)
		}

		seenGPU[gpu.GPU] = true

		if err := validateInstances(gpu); err != nil {
			return err
		}
	}

	return nil
}

// validateXIDs checks the XID emitter settings.
func (c *extrasConfig) validateXIDs() error {
	if c.XIDs == nil {
		return nil
	}

	if c.XIDs.Interval != "" {
		interval, err := time.ParseDuration(c.XIDs.Interval)
		if err != nil {
			return fmt.Errorf("invalid xids interval: %w", err)
		}

		if interval <= 0 {
			return errors.New("xids interval must be positive, omit it to disable ongoing events")
		}
	}

	for _, event := range c.XIDs.Initial {
		if event.GPU < 0 {
			return fmt.Errorf("initial xid event has a negative gpu index %d", event.GPU)
		}
	}

	return nil
}

// isFinite reports whether the value is a usable number.
func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// validateInstances checks one GPU's topology.
func validateInstances(gpu migGPUConfig) error {
	if gpu.GPU < 0 {
		return fmt.Errorf("mig entry has a negative gpu index %d", gpu.GPU)
	}

	if len(gpu.Instances) == 0 {
		return fmt.Errorf("mig entry for gpu %d has no instances", gpu.GPU)
	}

	seenGI := map[int]bool{}

	for _, instance := range gpu.Instances {
		if instance.GI < 0 {
			return fmt.Errorf("negative gi %d on gpu %d", instance.GI, gpu.GPU)
		}

		if seenGI[instance.GI] {
			return fmt.Errorf("duplicate gi %d on gpu %d", instance.GI, gpu.GPU)
		}

		seenGI[instance.GI] = true

		if instance.Profile == "" {
			return fmt.Errorf("gi %d on gpu %d needs a profile", instance.GI, gpu.GPU)
		}

		if instance.CIs < 0 {
			return fmt.Errorf("gi %d on gpu %d has a negative cis count", instance.GI, gpu.GPU)
		}

		if instance.MemoryTotalBytes == 0 && !profileSizeRe.MatchString(instance.Profile) {
			return fmt.Errorf("gi %d on gpu %d: profile %q carries no size token, set memory-total-bytes",
				instance.GI, gpu.GPU, instance.Profile)
		}
	}

	return nil
}

// applyDefaults fills the optional parts.
func (c *extrasConfig) applyDefaults() {
	if c.PCIe == nil {
		c.PCIe = &pcieConfig{
			TXBytesPerSecond: rangeCfg{Min: 0.2e9, Max: 3e9},
			RXBytesPerSecond: rangeCfg{Min: 0.5e9, Max: 8e9},
		}
	}

	if c.XIDs == nil {
		c.XIDs = &xidConfig{}
	}

	if len(c.XIDs.Codes) == 0 {
		c.XIDs.Codes = defaultXIDCodes
	}

	if c.EnergyFallbackPowerWatts <= 0 {
		c.EnergyFallbackPowerWatts = defaultEnergyFallbackPowerWatts
	}

	for gpuIdx := range c.MIG {
		for i := range c.MIG[gpuIdx].Instances {
			instance := &c.MIG[gpuIdx].Instances[i]
			if instance.CIs == 0 {
				instance.CIs = 1
			}

			if instance.MemoryTotalBytes == 0 {
				instance.MemoryTotalBytes = profileMemoryBytes(instance.Profile)
			}
		}
	}
}

// xidInterval reports the parsed cadence, zero when disabled.
func (c *extrasConfig) xidInterval() time.Duration {
	if c.XIDs == nil || c.XIDs.Interval == "" {
		return 0
	}

	interval, err := time.ParseDuration(c.XIDs.Interval)
	if err != nil {
		// validated at parse time; unreachable
		return 0
	}

	return interval
}

// profileMemoryBytes derives a framebuffer size from the profile name's size
// token ("3g.71gb" -> 71 GiB). The marketing number is only an
// approximation of what real hardware reports, which is why an explicit
// memory-total-bytes override exists.
func profileMemoryBytes(profile string) uint64 {
	match := profileSizeRe.FindStringSubmatch(profile)
	if match == nil {
		return 0
	}

	gigabytes, err := strconv.ParseUint(match[1], 10, 32)
	if err != nil {
		return 0
	}

	return gigabytes << 30
}
