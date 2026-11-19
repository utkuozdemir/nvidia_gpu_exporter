//go:build windows

package nvmlnative

import "github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"

// collectPCIeRuntimePM is a no-op on Windows (Linux sysfs only).
func (b *Backend) collectPCIeRuntimePM(_ device, _ string, _ *collect.Extras) bool {
	return true
}
