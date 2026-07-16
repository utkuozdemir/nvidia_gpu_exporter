// Package fakesmi implements a fake nvidia-smi that replays a capture,
// either one embedded from internal/captures or a capture file from disk. It
// answers the invocations the exporter makes (and any other invocation the
// capture has a recorded section for) purely from the capture content, with
// no baked-in knowledge of GPUs, drivers or fields, so a newly contributed
// capture works without any code change.
package fakesmi

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/capture"
)

// DefaultState is the capture state replayed when none is given.
const DefaultState = "idle"

// CaptureSource supplies the embedded captures the fake resolves names
// against. It is injected by the caller so this package pins no capture
// corpus of its own: the fake-nvidia-smi binary carries the full test
// corpus, while the exporter's demo backend carries a small curated set
// (embedding the corpus into the exporter is forbidden by a guard test).
type CaptureSource struct {
	// FS holds the embedded capture files.
	FS fs.FS
	// Default is the capture name served when none is configured.
	Default string
}

// usageExitCode reports a problem with the fake itself (bad flags, unreadable
// capture, an invocation the capture has no section for), as opposed to a
// behavior the real nvidia-smi could exhibit.
const usageExitCode = 2

// errorExitCode approximates the real nvidia-smi's exit code for a rejected
// query, e.g. an unknown query field.
const errorExitCode = 1

// config carries the fake's own settings, given as flags ahead of the
// nvidia-smi invocation to replay.
type config struct {
	capturePath string
	state       string
	stderrMsg   string
	failArg     string
	delay       time.Duration
	exitCode    int
	exitSet     bool
	// overrides replaces a query field's value in the data rows, keyed by the
	// field name, with a generator (a fixed value from --set, or a fresh random
	// draw from --set-range). It lets a run drive a state a real capture does not
	// contain, or make values move, without a new capture file. One map per
	// simulated GPU, indexed by the GPU; a single map when gpus is off.
	overrides []map[string]valueGen
	// gpus holds the simulated GPUs' identities the captured GPU row is
	// replicated into; nil replays the capture's rows untouched.
	gpus []gpuIdentity
	// fluct jitters naturally-varying fields around their captured values;
	// nil when --fluctuate is off.
	fluct *fluctuator
}

// Config is a pre-parsed configuration document, for in-process callers that
// need one immutable configuration across several invocations of one
// collection cycle (the exporter's demo backend): re-reading the file per
// invocation could otherwise interleave an atomic config edit into a cycle.
type Config struct {
	fc fileConfig
}

// LoadConfig reads and strictly parses a config file once.
func LoadConfig(path string) (*Config, error) {
	fc, err := loadFileConfig(path)
	if err != nil {
		return nil, err
	}

	return &Config{fc: *fc}, nil
}

// ParseConfig strictly parses a config document from memory (the demo
// backend's built-in default configuration).
func ParseConfig(data []byte) (*Config, error) {
	fc, err := parseFileConfig(data, "<builtin>")
	if err != nil {
		return nil, err
	}

	return &Config{fc: *fc}, nil
}

// Extras exposes the raw extras block, which the fake itself ignores: the
// demo backend decodes it with its own schema.
func (c *Config) Extras() *yaml.Node {
	if c.fc.Extras.IsZero() {
		return nil
	}

	return &c.fc.Extras
}

// GPUCount reports how many GPUs the configuration simulates: the gpus
// setting's count when present, otherwise the capture's single GPU.
func (c *Config) GPUCount() int {
	if c.fc.GPUs == nil {
		return 1
	}

	if c.fc.GPUs.count > 0 {
		return c.fc.GPUs.count
	}

	return len(c.fc.GPUs.entries)
}

// EnsureFieldOverride sets a fixed override for one field on the simulated
// GPU at index, unless the configuration already overrides that field
// (globally or for that GPU): an explicit user choice wins. The demo backend
// uses this to keep the served table coherent with its synthesized extras
// (e.g. the MIG mode flag on GPUs carrying a MIG topology). Without
// multi-GPU simulation there is only GPU 0, and the override is global.
func (c *Config) EnsureFieldOverride(index int, field, value string) error {
	if _, exists := c.fc.Overrides[field]; exists {
		return nil
	}

	if index < 0 || index >= c.GPUCount() {
		return fmt.Errorf("gpu index %d is out of range: the config simulates %d GPU(s)", index, c.GPUCount())
	}

	if c.fc.GPUs == nil {
		if c.fc.Overrides == nil {
			c.fc.Overrides = map[string]overrideEntry{}
		}

		c.fc.Overrides[field] = overrideEntry{fixed: &value}

		return nil
	}

	// identities are index-derived, so padding with empty entries changes
	// nothing for the GPUs they describe
	for len(c.fc.GPUs.entries) <= index {
		c.fc.GPUs.entries = append(c.fc.GPUs.entries, gpuFileEntry{})
	}

	entry := &c.fc.GPUs.entries[index]
	if _, exists := entry.overrides[field]; exists {
		return nil
	}

	if entry.overrides == nil {
		entry.overrides = map[string]overrideEntry{}
	}

	entry.overrides[field] = overrideEntry{fixed: &value}

	return nil
}

// StripFailureInjection clears the failure-injection settings (exit code,
// delay, stderr noise, per-argument failure): they exist to test the
// exec pipeline's subprocess handling, which the demo backend's in-process
// path deliberately does not reproduce.
func (c *Config) StripFailureInjection() {
	c.fc.Exit = nil
	c.fc.Delay = ""
	c.fc.StderrMsg = ""
	c.fc.FailArg = ""
}

// RunWith executes one invocation against a pre-parsed configuration, with
// no leading fake flags: args is the plain nvidia-smi invocation to answer.
// The returned value is the process exit code equivalent.
func RunWith(source CaptureSource, cfg *Config, args []string, stdout, stderr io.Writer) int {
	merged, _, err := resolveMerged(source, rawFlags{}, &cfg.fc, nil)
	if err != nil {
		fmt.Fprintf(stderr, "fake-nvidia-smi: %v\n", err)

		return usageExitCode
	}

	return run(source, merged, args, stdout, stderr)
}

// Run executes the fake: it parses the fake's own leading flags, loads the
// capture, and answers the remaining arguments from it. The returned value is
// the process exit code.
func Run(source CaptureSource, args []string, stdout, stderr io.Writer) int {
	cfg, rest, err := parseFlags(source, args)
	if err != nil {
		fmt.Fprintf(stderr, "fake-nvidia-smi: %v\n", err)

		return usageExitCode
	}

	return run(source, cfg, rest, stdout, stderr)
}

// run answers one resolved invocation.
func run(source CaptureSource, cfg config, rest []string, stdout, stderr io.Writer) int {
	if cfg.delay > 0 {
		time.Sleep(cfg.delay)
	}

	if cfg.exitSet {
		if cfg.stderrMsg != "" {
			fmt.Fprintln(stderr, cfg.stderrMsg)
		}

		return cfg.exitCode
	}

	if failed := failMatching(cfg, rest, stderr); failed {
		return errorExitCode
	}

	capt, err := loadCapture(source, cfg.capturePath)
	if err != nil {
		fmt.Fprintf(stderr, "fake-nvidia-smi: %v\n", err)

		return usageExitCode
	}

	return answer(capt, &cfg, rest, stdout, stderr)
}

// loadCapture resolves the --capture value: a path (anything with a path
// separator, or naming an existing file) loads from disk, and anything else
// names an embedded capture from the injected source, where the .txt suffix
// may be omitted.
func loadCapture(source CaptureSource, value string) (*capture.Capture, error) {
	_, statErr := os.Stat(value)

	if statErr == nil || strings.ContainsRune(value, '/') || strings.ContainsRune(value, os.PathSeparator) {
		capt, err := capture.Load(value)
		if err != nil {
			return nil, fmt.Errorf("failed to load capture from disk: %w", err)
		}

		return capt, nil
	}

	name := value
	if !strings.HasSuffix(name, ".txt") {
		name += ".txt"
	}

	data, err := fs.ReadFile(source.FS, name)
	if err != nil {
		return nil, fmt.Errorf("capture %q is neither a file on disk nor an embedded capture", value)
	}

	parsed, err := capture.Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse embedded capture %q: %w", name, err)
	}

	return parsed, nil
}

// rawFlags holds the leading flags as given, before a --config base is loaded
// and merged. The *Set bools record which fields a flag set, so a flag wins over
// the config regardless of where --config sits in the arguments.
type rawFlags struct {
	capturePath  string
	captureSet   bool
	state        string
	stateSet     bool
	stderrMsg    string
	stderrSet    bool
	failArg      string
	failSet      bool
	exitCode     int
	exitSet      bool
	delay        time.Duration
	delaySet     bool
	configPath   string
	seed         int64
	seedSet      bool
	gpus         int
	gpusSet      bool
	fluctuate    bool
	fluctuateSet bool
	ops          []rawOverride // --set and --set-range, in argument order
}

// parseFlags consumes the fake's own flags from the front of args, then resolves
// them against an optional --config base, returning the remaining arguments,
// which form the nvidia-smi invocation to replay.
func parseFlags(source CaptureSource, args []string) (config, []string, error) {
	var raw rawFlags

	for len(args) > 0 {
		name, value, hasValue := strings.Cut(args[0], "=")

		if name == "--fluctuate" {
			rest, err := applyFluctuateFlag(&raw, value, hasValue, args[1:])
			if err != nil {
				return config{}, nil, err
			}

			args = rest

			continue
		}

		switch name {
		case "--capture", "--state", "--stderr-msg", "--fail-arg", "--exit", "--delay",
			"--set", "--set-range", "--config", "--seed", "--gpus":
		default:
			return resolve(source, raw, args)
		}

		args = args[1:]

		if !hasValue {
			if len(args) == 0 {
				return config{}, nil, fmt.Errorf("flag %s needs a value", name)
			}

			value, args = args[0], args[1:]
		}

		if err := applyFlag(&raw, name, value); err != nil {
			return config{}, nil, err
		}
	}

	return resolve(source, raw, nil)
}

// applyFlag records a single parsed flag into raw.
//
//nolint:cyclop
func applyFlag(raw *rawFlags, name, value string) error {
	switch name {
	case "--capture":
		raw.capturePath, raw.captureSet = value, true
	case "--state":
		raw.state, raw.stateSet = value, true
	case "--stderr-msg":
		raw.stderrMsg, raw.stderrSet = value, true
	case "--fail-arg":
		raw.failArg, raw.failSet = value, true
	case "--config":
		if value == "" {
			return errors.New("--config needs a path")
		}

		raw.configPath = value
	case "--set":
		op, err := parseSetFlag(value)
		if err != nil {
			return err
		}

		raw.ops = append(raw.ops, op)
	case "--set-range":
		op, err := parseSetRangeFlag(value)
		if err != nil {
			return err
		}

		raw.ops = append(raw.ops, op)
	default:
		return applyNumericFlag(raw, name, value)
	}

	return nil
}

// applyNumericFlag records the flags carrying a parsed number.
func applyNumericFlag(raw *rawFlags, name, value string) error {
	switch name {
	case "--exit":
		code, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid --exit value %q: %w", value, err)
		}

		raw.exitCode, raw.exitSet = code, true
	case "--delay":
		duration, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid --delay value %q: %w", value, err)
		}

		raw.delay, raw.delaySet = duration, true
	case "--seed":
		seed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid --seed value %q: %w", value, err)
		}

		raw.seed, raw.seedSet = seed, true
	case "--gpus":
		count, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid --gpus value %q: %w", value, err)
		}

		if err := validateGPUCount(count); err != nil {
			return err
		}

		raw.gpus, raw.gpusSet = count, true
	}

	return nil
}

// applyFluctuateFlag parses the one boolean flag. The generic parser always
// consumes a following value, which here would swallow the first nvidia-smi
// argument, so a bare --fluctuate means true, and a following bare true/false
// (any strconv.ParseBool spelling) is consumed only when it is one.
func applyFluctuateFlag(raw *rawFlags, value string, hasValue bool, rest []string) ([]string, error) {
	if !hasValue && len(rest) > 0 {
		if _, err := strconv.ParseBool(rest[0]); err == nil {
			value, hasValue = rest[0], true
			rest = rest[1:]
		}
	}

	enabled := true

	if hasValue {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid --fluctuate value %q: %w", value, err)
		}

		enabled = parsed
	}

	raw.fluctuate, raw.fluctuateSet = enabled, true

	return rest, nil
}

// resolve merges the parsed flags with an optional --config base into the final
// config: the config supplies defaults, and any flag-set value wins per field.
func resolve(source CaptureSource, raw rawFlags, rest []string) (config, []string, error) {
	var fc *fileConfig

	if raw.configPath != "" {
		loaded, err := loadFileConfig(raw.configPath)
		if err != nil {
			return config{}, nil, err
		}

		fc = loaded
	}

	return resolveMerged(source, raw, fc, rest)
}

// resolveMerged merges already-loaded settings into the final config, shared
// by the flag path and the pre-parsed-config path.
func resolveMerged(source CaptureSource, raw rawFlags, fc *fileConfig, rest []string) (config, []string, error) {
	cfg := config{capturePath: source.Default, state: DefaultState}

	mergeBaseSettings(&cfg, raw, fc)

	if err := mergeFailureSettings(&cfg, raw, fc); err != nil {
		return config{}, nil, err
	}

	if err := mergeValueSettings(&cfg, raw, fc); err != nil {
		return config{}, nil, err
	}

	return cfg, rest, nil
}

// mergeValueSettings resolves everything that shapes the served values: the
// seed, the simulated GPUs, the override generators and the fluctuation.
// Flags win over the config per setting, as everywhere else.
func mergeValueSettings(cfg *config, raw rawFlags, fc *fileConfig) error {
	seed := time.Now().UnixNano()

	switch {
	case raw.seedSet:
		seed = raw.seed
	case fc != nil && fc.Seed != nil:
		seed = *fc.Seed
	}

	gpus, gpuEntries, err := resolveGPUs(raw, fc)
	if err != nil {
		return err
	}

	overrides, err := buildOverrides(fc, raw.ops, gpuEntries, len(gpus), seed)
	if err != nil {
		return err
	}

	cfg.gpus = gpus
	cfg.overrides = overrides

	fluctuate := raw.fluctuate
	if !raw.fluctuateSet {
		fluctuate = fc != nil && fc.Fluctuate
	}

	if fluctuate {
		cfg.fluct = newFluctuator(seed)
	}

	return nil
}

// mergeBaseSettings applies capture and state, preferring a flag over the
// config over the default.
func mergeBaseSettings(cfg *config, raw rawFlags, fc *fileConfig) {
	switch {
	case raw.captureSet:
		cfg.capturePath = raw.capturePath
	case fc != nil && fc.Capture != "":
		cfg.capturePath = fc.Capture
	}

	switch {
	case raw.stateSet:
		cfg.state = raw.state
	case fc != nil && fc.State != "":
		cfg.state = fc.State
	}
}

// mergeFailureSettings applies the failure-injection settings, preferring a flag
// over the config.
//
//nolint:cyclop // a flat flag-over-config merge, one branch per setting
func mergeFailureSettings(cfg *config, raw rawFlags, fc *fileConfig) error {
	switch {
	case raw.stderrSet:
		cfg.stderrMsg = raw.stderrMsg
	case fc != nil && fc.StderrMsg != "":
		cfg.stderrMsg = fc.StderrMsg
	}

	switch {
	case raw.failSet:
		cfg.failArg = raw.failArg
	case fc != nil && fc.FailArg != "":
		cfg.failArg = fc.FailArg
	}

	switch {
	case raw.exitSet:
		cfg.exitCode, cfg.exitSet = raw.exitCode, true
	case fc != nil && fc.Exit != nil:
		cfg.exitCode, cfg.exitSet = *fc.Exit, true
	}

	switch {
	case raw.delaySet:
		cfg.delay = raw.delay
	case fc != nil && fc.Delay != "":
		d, err := time.ParseDuration(fc.Delay)
		if err != nil {
			return fmt.Errorf("invalid config delay %q: %w", fc.Delay, err)
		}

		cfg.delay = d
	}

	return nil
}

// parseSetFlag parses one --set field=value into a fixed override.
func parseSetFlag(raw string) (rawOverride, error) {
	field, value, ok := strings.Cut(raw, "=")
	if !ok || field == "" {
		return rawOverride{}, fmt.Errorf("invalid --set %q, want field=value", raw)
	}

	if err := validateSetValue(field, value); err != nil {
		return rawOverride{}, err
	}

	return rawOverride{field: field, fixed: &value}, nil
}

// parseSetRangeFlag parses one --set-range field=min:max into a range override.
func parseSetRangeFlag(raw string) (rawOverride, error) {
	field, rangeRaw, ok := strings.Cut(raw, "=")
	if !ok || field == "" {
		return rawOverride{}, fmt.Errorf("invalid --set-range %q, want field=min:max", raw)
	}

	spec, err := parseRange(field, rangeRaw)
	if err != nil {
		return rawOverride{}, err
	}

	return rawOverride{field: field, rng: &spec}, nil
}

// failMatching implements the selective failure injection: when an argument
// of the replayed invocation starts with the configured prefix, the
// invocation fails while all others keep working. This is how tests break one
// of the exporter's queries (e.g. only the per-process one) in isolation.
func failMatching(cfg config, args []string, stderr io.Writer) bool {
	if cfg.failArg == "" {
		return false
	}

	for _, arg := range args {
		if strings.HasPrefix(arg, cfg.failArg) {
			message := cfg.stderrMsg
			if message == "" {
				message = fmt.Sprintf("fake-nvidia-smi: injected failure for %q", arg)
			}

			fmt.Fprintln(stderr, message)

			return true
		}
	}

	return false
}

// answer serves the nvidia-smi invocation in args from the capture: the two
// CSV queries by column projection, anything else by verbatim replay of the
// section recorded for the same command line.
func answer(capt *capture.Capture, cfg *config, args []string, stdout, stderr io.Writer) int {
	if request, sectionLabel, isQuery := queryRequest(args); isQuery {
		return answerQuery(capt, cfg, request, sectionLabel, stdout, stderr)
	}

	if section := findVerbatim(capt, cfg.state, args); section != nil {
		fmt.Fprintln(stdout, section.Body)

		return 0
	}

	fmt.Fprintf(stderr, "fake-nvidia-smi: capture has no section recorded for %q in state %q\n",
		strings.Join(args, " "), cfg.state)

	return usageExitCode
}

// answerQuery serves one of the two CSV queries by projecting the recorded
// section through the transform pipeline.
func answerQuery(capt *capture.Capture, cfg *config, request, sectionLabel string, stdout, stderr io.Writer) int {
	if len(cfg.gpus) > 0 {
		if err := validateReplicableCapture(capt, cfg.state); err != nil {
			fmt.Fprintf(stderr, "fake-nvidia-smi: %v\n", err)

			return usageExitCode
		}
	}

	section := capt.Find(cfg.state, sectionLabel)
	if section == nil {
		fmt.Fprintf(stderr, "fake-nvidia-smi: capture has no %q section for state %q\n",
			sectionLabel, cfg.state)

		return usageExitCode
	}

	output, err := project(section, request, cfg)
	if err != nil {
		// the real nvidia-smi reports a rejected query on stdout
		fmt.Fprintln(stdout, err)

		return errorExitCode
	}

	fmt.Fprintln(stdout, output)

	return 0
}

// validateReplicableCapture ensures the selected state's query-gpu recording
// has exactly one data row: gpus replicates that row per simulated GPU, and a
// capture that already records several GPUs has no defined replication
// semantics. The check also guards a compute-apps query, whose rows join the
// GPUs by identity.
func validateReplicableCapture(capt *capture.Capture, state string) error {
	section := capt.Find(state, "query-gpu (csv")
	if section == nil {
		return fmt.Errorf("gpus needs a query-gpu section for state %q to replicate", state)
	}

	// the parsed body is trimmed of surrounding blank lines, so its newline
	// count is exactly the number of data rows after the header
	if rows := strings.Count(section.Body, "\n"); rows != 1 {
		return fmt.Errorf("gpus needs a single-GPU capture, but the query-gpu section for state %q has %d data rows",
			state, rows)
	}

	return nil
}

// queryRequest recognizes the two CSV query invocations. It returns the
// requested field list and the label prefix of the capture section that
// answers it.
func queryRequest(args []string) (string, string, bool) {
	if len(args) != 2 || args[1] != "--format=csv" {
		return "", "", false
	}

	if request, isGPUQuery := strings.CutPrefix(args[0], "--query-gpu="); isGPUQuery {
		return request, "query-gpu (csv", true
	}

	if request, isAppsQuery := strings.CutPrefix(args[0], "--query-compute-apps="); isAppsQuery {
		return request, "query-compute-apps", true
	}

	return "", "", false
}

// findVerbatim looks for a section whose recorded command line matches the
// requested invocation, canonicalized on both sides (the recorded lines vary
// in incidental whitespace). Sections of the selected state and the
// state-independent capabilities sections are eligible.
func findVerbatim(capt *capture.Capture, state string, args []string) *capture.Section {
	requested := strings.Join(args, " ")

	for i := range capt.Sections {
		section := &capt.Sections[i]

		if section.State != state && section.State != "capabilities" {
			continue
		}

		recorded := strings.TrimPrefix(section.Command, "nvidia-smi")
		if section.Command != "" && strings.Join(strings.Fields(recorded), " ") == requested {
			return section
		}
	}

	return nil
}
