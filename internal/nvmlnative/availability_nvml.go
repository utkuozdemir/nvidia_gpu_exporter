//go:build linux && cgo

package nvmlnative

import (
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// availability is an immutable snapshot of which driver-library exports were
// present the last time NVML was initialized. It is replaced wholesale on
// re-initialization rather than mutated, so readers outside the collection
// lock (the XID watcher) always observe a coherent set.
//
// The zero value reports everything as absent, which is the safe direction: a
// backend that somehow reached collection without probing serves no series
// instead of crashing.
type availability struct {
	present map[string]bool
	missing []string
}

// probeSymbols resolves every guarded export once. lookup reports nil when the
// driver library exports the name. It must be called after NVML has loaded,
// since the lookup is refused while the library is closed.
func probeSymbols(lookup func(string) error, symbols []string) *availability {
	avail := &availability{present: make(map[string]bool, len(symbols))}

	for _, symbol := range symbols {
		ok := lookup(symbol) == nil
		avail.present[symbol] = ok

		if !ok {
			avail.missing = append(avail.missing, symbol)
		}
	}

	sort.Strings(avail.missing)

	return avail
}

// has reports whether the driver library exports the given entry point.
func (a *availability) has(symbol string) bool {
	if a == nil {
		return false
	}

	return a.present[symbol]
}

// hasAny reports whether the driver library exports at least one of the given
// entry points. It exists for the handful of calls whose version go-nvml
// selects at load time: it probes the newer spellings and falls back to the
// classic one, so the call is safe as long as any of them resolves.
func (a *availability) hasAny(symbols ...string) bool {
	return slices.ContainsFunc(symbols, a.has)
}

// hasAll reports whether every one of the given entry points is exported. It
// exists for families that acquire and release a resource through separate
// calls, where a partially present set is worse than an absent one.
func (a *availability) hasAll(symbols ...string) bool {
	for _, symbol := range symbols {
		if !a.has(symbol) {
			return false
		}
	}

	return true
}

// log reports the exports this driver does not provide, once per NVML
// generation. This is the diagnostic that turns "some series are missing" into
// an answerable bug report, so it names every symbol rather than a count.
func (a *availability) log(logger *slog.Logger) {
	if a == nil || len(a.missing) == 0 {
		return
	}

	logger.Info("driver library does not export some entry points, "+
		"the metrics they serve stay absent",
		"count", len(a.missing), "symbols", strings.Join(a.missing, ","))
}

// retString describes an NVML status without asking the driver. go-nvml's
// Return.String routes through the library's own nvmlErrorString once it is
// loaded, which puts a driver call on exactly the paths that run when the
// driver is already misbehaving, and makes error reporting itself capable of
// killing the process. The numeric status is enough for a log line.
func retString(ret nvml.Return) string {
	if name, ok := returnNames[ret]; ok {
		return name
	}

	return "NVML status " + strconv.Itoa(int(ret))
}

//nolint:gochecknoglobals // lookup table for the statuses this backend acts on
var returnNames = map[nvml.Return]string{
	nvml.SUCCESS:                       "SUCCESS",
	nvml.ERROR_UNINITIALIZED:           "ERROR_UNINITIALIZED",
	nvml.ERROR_INVALID_ARGUMENT:        "ERROR_INVALID_ARGUMENT",
	nvml.ERROR_NOT_SUPPORTED:           "ERROR_NOT_SUPPORTED",
	nvml.ERROR_NO_PERMISSION:           "ERROR_NO_PERMISSION",
	nvml.ERROR_NOT_FOUND:               "ERROR_NOT_FOUND",
	nvml.ERROR_INSUFFICIENT_SIZE:       "ERROR_INSUFFICIENT_SIZE",
	nvml.ERROR_DRIVER_NOT_LOADED:       "ERROR_DRIVER_NOT_LOADED",
	nvml.ERROR_TIMEOUT:                 "ERROR_TIMEOUT",
	nvml.ERROR_IRQ_ISSUE:               "ERROR_IRQ_ISSUE",
	nvml.ERROR_LIBRARY_NOT_FOUND:       "ERROR_LIBRARY_NOT_FOUND",
	nvml.ERROR_FUNCTION_NOT_FOUND:      "ERROR_FUNCTION_NOT_FOUND",
	nvml.ERROR_CORRUPTED_INFOROM:       "ERROR_CORRUPTED_INFOROM",
	nvml.ERROR_GPU_IS_LOST:             "ERROR_GPU_IS_LOST",
	nvml.ERROR_RESET_REQUIRED:          "ERROR_RESET_REQUIRED",
	nvml.ERROR_OPERATING_SYSTEM:        "ERROR_OPERATING_SYSTEM",
	nvml.ERROR_LIB_RM_VERSION_MISMATCH: "ERROR_LIB_RM_VERSION_MISMATCH",
	nvml.ERROR_IN_USE:                  "ERROR_IN_USE",
	nvml.ERROR_MEMORY:                  "ERROR_MEMORY",
	nvml.ERROR_NO_DATA:                 "ERROR_NO_DATA",
	nvml.ERROR_UNKNOWN:                 "ERROR_UNKNOWN",
}
