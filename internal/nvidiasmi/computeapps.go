package nvidiasmi

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// computeAppsQFields is the field set queried from --query-compute-apps. It is
// fixed: these four fields have existed for many driver generations, and they
// are the complete per-process identity plus the one per-process measurement
// nvidia-smi offers. No auto-detection, no configuration.
const computeAppsQFields = "gpu_uuid,pid,process_name,used_gpu_memory"

// computeAppsNumCols is the column count computeAppsQFields produces.
const computeAppsNumCols = 4

// UsedMemoryMultiplier converts the used_gpu_memory value (reported in MiB,
// per --help-query-compute-apps) to bytes.
const UsedMemoryMultiplier = 1024 * 1024

// ComputeApp is one process holding a compute context on a GPU, as reported by
// nvidia-smi --query-compute-apps.
type ComputeApp struct {
	// GPUUUID is normalized the same way as the uuid label on all GPU metrics
	// (lowercase, "gpu-" prefix stripped), so per-process series join with
	// them. Under MIG this is the parent GPU's uuid: nvidia-smi does not
	// attribute processes to MIG instances in this output.
	GPUUUID string
	// PID is kept as a string: it is only ever a label, never a value.
	PID         string
	ProcessName string
	// UsedMemory is the raw reported value (e.g. "40960 MiB"). It stays raw
	// because it is not always a number: "[N/A]" on Windows WDDM,
	// "[Insufficient Permissions]" in restricted containers.
	UsedMemory string
}

// QueryComputeApps runs nvidia-smi --query-compute-apps and parses the CSV
// output, returning one entry per process with a compute context.
func QueryComputeApps(
	ctx context.Context,
	command string,
	run RunFunc,
	logger *slog.Logger,
) ([]ComputeApp, error) {
	stdout, _, err := execQuery(ctx, command, run,
		"--query-compute-apps="+computeAppsQFields, "--format=csv")
	if err != nil {
		return nil, err
	}

	return ParseComputeApps(stdout, logger)
}

// ParseComputeApps parses --query-compute-apps CSV output. A header-only
// output (no processes) parses to an empty result.
//
// Rows are parsed positionally from the outside in: gpu_uuid and pid from the
// left, used_gpu_memory from the right, and everything in between rejoined as
// the process name. Process names are workload-controlled and may contain
// commas on drivers that do not sanitize them (modern drivers replace a comma
// with "?" in CSV output, but that is not assumed here). A malformed or
// unreadable row is skipped so that one weird process cannot take down all
// per-process metrics.
func ParseComputeApps(output string, logger *slog.Logger) ([]ComputeApp, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, nil
	}

	lines := strings.Split(trimmed, "\n")

	if numCols := len(parseCSVLine(lines[0])); numCols != computeAppsNumCols {
		return nil, fmt.Errorf(
			"unexpected compute apps header: expected %d columns, got %d: %q",
			computeAppsNumCols, numCols, strings.TrimSpace(lines[0]),
		)
	}

	apps := make([]ComputeApp, 0, len(lines)-1)

	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}

		app, ok := parseComputeAppRow(line, logger)
		if !ok {
			continue
		}

		apps = append(apps, app)
	}

	return apps, nil
}

// parseComputeAppRow parses one data row, reporting whether the row is usable.
func parseComputeAppRow(line string, logger *slog.Logger) (ComputeApp, bool) {
	fields := strings.Split(line, ",")
	if len(fields) < computeAppsNumCols {
		logger.Warn("skipping malformed compute apps row", "row", strings.TrimSpace(line))

		return ComputeApp{}, false
	}

	pid := strings.TrimSpace(fields[1])
	if _, err := strconv.ParseUint(pid, 10, 64); err != nil {
		// A non-numeric pid means the whole row is unreadable, most commonly
		// "[Insufficient Permissions]" under MIG in a restricted container.
		// That is an expected state reported for every process on every
		// collection, so a known token is skipped without logging.
		if !IsKnownAbsentValue(pid) {
			logger.Warn("skipping compute apps row with unparseable pid",
				"pid", pid, "row", strings.TrimSpace(line))
		}

		return ComputeApp{}, false
	}

	name := strings.TrimSpace(strings.Join(fields[2:len(fields)-1], ","))

	return ComputeApp{
		GPUUUID:     NormalizeUUID(fields[0]),
		PID:         pid,
		ProcessName: name,
		UsedMemory:  strings.TrimSpace(fields[len(fields)-1]),
	}, true
}

// NormalizeUUID normalizes a GPU uuid the way all metric labels carry it:
// lowercase, without the "GPU-" prefix.
func NormalizeUUID(raw string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "gpu-")
}

// IsKnownAbsentValue reports whether a raw value is one of the tokens
// nvidia-smi uses for "this field exists but cannot be read right now":
// "[N/A]" (e.g. per-process memory on Windows WDDM), "[Insufficient
// Permissions]" (restricted containers, especially under MIG),
// "[Not Supported]", or an empty value. These are expected states reported on
// every collection, so callers skip them without logging.
func IsKnownAbsentValue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "n/a", "[n/a]", "[not supported]", "[insufficient permissions]", "[unknown error]":
		return true
	default:
		return false
	}
}
