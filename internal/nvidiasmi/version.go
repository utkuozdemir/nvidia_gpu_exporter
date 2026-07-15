package nvidiasmi

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

// cudaVersionValue is the shape of a plausible CUDA version value. Newer
// drivers replace the value of a renamed line with prose (for example
// `CUDA version : Deprecated, see "CUDA UMD version" instead`), so anything
// that does not look like a version number must be ignored, not exported.
var cudaVersionValue = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)

// QueryCudaVersion runs `nvidia-smi --version` once and extracts the CUDA
// version the driver supports. It is best-effort: any failure returns the
// empty string (logged once), which renders as an empty cuda_version label.
// It runs at startup only, never per scrape.
func QueryCudaVersion(
	ctx context.Context,
	command string,
	timeout time.Duration,
	run RunFunc,
	logger *slog.Logger,
) string {
	if timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	stdout, _, err := execQuery(ctx, command, run, "--version")
	if err != nil {
		logger.Warn("failed to read the CUDA version from nvidia-smi, "+
			"the cuda_version label stays empty", "err", err)

		return ""
	}

	version := ParseCudaVersion(stdout)
	if version == "" {
		logger.Warn("no CUDA version found in the nvidia-smi --version output, " +
			"the cuda_version label stays empty")
	}

	return version
}

// ParseCudaVersion extracts the CUDA version from `nvidia-smi --version`
// output. Drivers of the 610 branch and later rename the line to
// "CUDA UMD version" and turn the old "CUDA version" line into a deprecation
// pointer, so the UMD spelling wins and the legacy spelling is the fallback.
func ParseCudaVersion(output string) string {
	legacy := ""

	for line := range strings.SplitSeq(output, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}

		value = strings.TrimSpace(value)
		if !cudaVersionValue.MatchString(value) {
			continue
		}

		switch strings.ToLower(strings.Join(strings.Fields(key), " ")) {
		case "cuda umd version":
			return value
		case "cuda version":
			legacy = value
		}
	}

	return legacy
}
