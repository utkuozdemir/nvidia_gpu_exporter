//go:build !(linux && cgo)

package nvmlnative

import (
	"context"
	"errors"
	"log/slog"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

// Available reports whether this build carries the NVML backend.
const Available = false

// ErrUnavailable is returned by New in builds without the NVML backend: the
// backend needs Linux and cgo, and only the dedicated release artifacts
// carry it.
var ErrUnavailable = errors.New(
	"the nvml backend is not available in this build: it requires Linux and a cgo-enabled binary " +
		"(use the nvml release artifact or build with CGO_ENABLED=1)")

// Backend is the unavailable-build placeholder.
type Backend struct{}

// New always fails in builds without the NVML backend.
func New(_ *slog.Logger) (*Backend, error) {
	return nil, ErrUnavailable
}

// Close is a no-op on the placeholder.
func (b *Backend) Close() {}

// DriverVersion is never reachable: New always fails first.
func (b *Backend) DriverVersion() string { return "" }

// QueryFunc is never reachable: New always fails first.
func (b *Backend) QueryFunc(_ nvidiasmi.ResolvedFields, _ CollectOptions) collect.QueryFunc {
	panic("nvml backend is not available in this build")
}

// RunXIDWatcher is never reachable: New always fails first.
func (b *Backend) RunXIDWatcher(_ context.Context) error {
	panic("nvml backend is not available in this build")
}

// XIDCounts is never reachable: New always fails first.
func (b *Backend) XIDCounts() []collect.XIDCounter {
	panic("nvml backend is not available in this build")
}
