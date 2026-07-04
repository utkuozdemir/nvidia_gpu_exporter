// Package fakesmi implements a fake nvidia-smi that replays a capture file
// from testdata/captures. It answers the invocations the exporter makes (and
// any other invocation the capture has a recorded section for) purely from
// the capture content, with no baked-in knowledge of GPUs, drivers or fields,
// so a newly contributed capture works without any code change.
package fakesmi

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/capture"
)

// DefaultCapture is the capture replayed when none is given: a common setup,
// relative to the repository root for `go run ./cmd/fake-nvidia-smi` use.
const DefaultCapture = "testdata/captures/linux-x86_64__nvidia-geforce-rtx-2080-super__595.71.05.txt"

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
	delay       time.Duration
	exitCode    int
	exitSet     bool
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

	capt, err := capture.Load(cfg.capturePath)
	if err != nil {
		fmt.Fprintf(stderr, "fake-nvidia-smi: %v\n", err)

		return usageExitCode
	}

	return answer(capt, cfg.state, rest, stdout, stderr)
}

// parseFlags consumes the fake's own flags from the front of args, returning
// the remaining arguments, which form the nvidia-smi invocation to replay.
//
//nolint:cyclop
func parseFlags(args []string) (config, []string, error) {
	cfg := config{capturePath: DefaultCapture, state: DefaultState}

	for len(args) > 0 {
		name, value, hasValue := strings.Cut(args[0], "=")

		switch name {
		case "--capture", "--state", "--stderr-msg", "--exit", "--delay":
		default:
			return cfg, args, nil
		}

		args = args[1:]

		if !hasValue {
			if len(args) == 0 {
				return cfg, nil, fmt.Errorf("flag %s needs a value", name)
			}

			value, args = args[0], args[1:]
		}

		var err error

		switch name {
		case "--capture":
			cfg.capturePath = value
		case "--state":
			cfg.state = value
		case "--stderr-msg":
			cfg.stderrMsg = value
		case "--exit":
			cfg.exitSet = true

			if cfg.exitCode, err = strconv.Atoi(value); err != nil {
				return cfg, nil, fmt.Errorf("invalid --exit value %q: %w", value, err)
			}
		case "--delay":
			if cfg.delay, err = time.ParseDuration(value); err != nil {
				return cfg, nil, fmt.Errorf("invalid --delay value %q: %w", value, err)
			}
		}
	}

	return cfg, nil, nil
}

// answer serves the nvidia-smi invocation in args from the capture: the two
// CSV queries by column projection, anything else by verbatim replay of the
// section recorded for the same command line.
func answer(capt *capture.Capture, state string, args []string, stdout, stderr io.Writer) int {
	if request, sectionLabel, isQuery := queryRequest(args); isQuery {
		section := capt.Find(state, sectionLabel)
		if section == nil {
			fmt.Fprintf(stderr, "fake-nvidia-smi: capture has no %q section for state %q\n",
				sectionLabel, state)

			return usageExitCode
		}

		output, err := project(section, request)
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
