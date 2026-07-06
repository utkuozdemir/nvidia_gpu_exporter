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

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/capture"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/captures"
)

// DefaultState is the capture state replayed when none is given.
const DefaultState = "idle"

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
	// overrides replaces a query field's value in every data row, keyed by the
	// field name, with a generator (a fixed value from --set, or a fresh random
	// draw from --set-range). It lets a run drive a state a real capture does not
	// contain, or make values move, without a new capture file.
	overrides map[string]valueGen
}

// Run executes the fake: it parses the fake's own leading flags, loads the
// capture, and answers the remaining arguments from it. The returned value is
// the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	cfg, rest, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "fake-nvidia-smi: %v\n", err)

		return usageExitCode
	}

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

	capt, err := loadCapture(cfg.capturePath)
	if err != nil {
		fmt.Fprintf(stderr, "fake-nvidia-smi: %v\n", err)

		return usageExitCode
	}

	return answer(capt, cfg.state, cfg.overrides, rest, stdout, stderr)
}

// loadCapture resolves the --capture value: a path (anything with a path
// separator, or naming an existing file) loads from disk, and anything else
// names an embedded capture from internal/captures, where the .txt suffix
// may be omitted.
func loadCapture(value string) (*capture.Capture, error) {
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

	data, err := fs.ReadFile(captures.FS, name)
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
	capturePath string
	captureSet  bool
	state       string
	stateSet    bool
	stderrMsg   string
	stderrSet   bool
	failArg     string
	failSet     bool
	exitCode    int
	exitSet     bool
	delay       time.Duration
	delaySet    bool
	configPath  string
	seed        int64
	seedSet     bool
	ops         []rawOverride // --set and --set-range, in argument order
}

// parseFlags consumes the fake's own flags from the front of args, then resolves
// them against an optional --config base, returning the remaining arguments,
// which form the nvidia-smi invocation to replay.
func parseFlags(args []string) (config, []string, error) {
	var raw rawFlags

	for len(args) > 0 {
		name, value, hasValue := strings.Cut(args[0], "=")

		switch name {
		case "--capture", "--state", "--stderr-msg", "--fail-arg", "--exit", "--delay",
			"--set", "--set-range", "--config", "--seed":
		default:
			return resolve(raw, args)
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

	return resolve(raw, nil)
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
	case "--exit":
		code, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid --exit value %q: %w", value, err)
		}

		raw.exitCode, raw.exitSet = code, true
	case "--delay":
		d, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid --delay value %q: %w", value, err)
		}

		raw.delay, raw.delaySet = d, true
	case "--config":
		if value == "" {
			return errors.New("--config needs a path")
		}

		raw.configPath = value
	case "--seed":
		s, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid --seed value %q: %w", value, err)
		}

		raw.seed, raw.seedSet = s, true
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
	}

	return nil
}

// resolve merges the parsed flags with an optional --config base into the final
// config: the config supplies defaults, and any flag-set value wins per field.
func resolve(raw rawFlags, rest []string) (config, []string, error) {
	cfg := config{capturePath: captures.Default, state: DefaultState}

	var fc *fileConfig

	if raw.configPath != "" {
		loaded, err := loadFileConfig(raw.configPath)
		if err != nil {
			return config{}, nil, err
		}

		fc = loaded
	}

	mergeBaseSettings(&cfg, raw, fc)

	if err := mergeFailureSettings(&cfg, raw, fc); err != nil {
		return config{}, nil, err
	}

	seed := time.Now().UnixNano()

	switch {
	case raw.seedSet:
		seed = raw.seed
	case fc != nil && fc.Seed != nil:
		seed = *fc.Seed
	}

	overrides, err := buildOverrides(fc, raw.ops, seed)
	if err != nil {
		return config{}, nil, err
	}

	cfg.overrides = overrides

	return cfg, rest, nil
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
func answer(
	capt *capture.Capture,
	state string,
	overrides map[string]valueGen,
	args []string,
	stdout, stderr io.Writer,
) int {
	if request, sectionLabel, isQuery := queryRequest(args); isQuery {
		section := capt.Find(state, sectionLabel)
		if section == nil {
			fmt.Fprintf(stderr, "fake-nvidia-smi: capture has no %q section for state %q\n",
				sectionLabel, state)

			return usageExitCode
		}

		output, err := project(section, request, overrides)
		if err != nil {
			// the real nvidia-smi reports a rejected query on stdout
			fmt.Fprintln(stdout, err)

			return errorExitCode
		}

		fmt.Fprintln(stdout, output)

		return 0
	}

	if section := findVerbatim(capt, state, args); section != nil {
		fmt.Fprintln(stdout, section.Body)

		return 0
	}

	fmt.Fprintf(stderr, "fake-nvidia-smi: capture has no section recorded for %q in state %q\n",
		strings.Join(args, " "), state)

	return usageExitCode
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
