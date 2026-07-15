package nvidiasmi_test

import (
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/assert"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

// The parse cases replicate recorded `nvidia-smi --version` outputs from the
// capture corpus verbatim: the classic spelling (Linux, driver 590) and the
// renamed spelling of driver 610, where the legacy line carries a
// deprecation pointer instead of a value.
func TestParseCudaVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name: "classic spelling (driver 590, h100 capture)",
			output: `NVIDIA-SMI version  : 590.48.01
NVML version        : 590.48
DRIVER version      : 590.48.01
CUDA Version        : 13.1
`,
			want: "13.1",
		},
		{
			name: "renamed UMD spelling (driver 610 capture)",
			output: `NVIDIA-SMI version  : 610.62
KMD version         : 610.62
DRIVER version      : Deprecated, see "KMD version" instead
CUDA version        : Deprecated, see "CUDA UMD version" instead
CUDA UMD version    : 13.3
`,
			want: "13.3",
		},
		{
			name: "UMD wins over a legacy line that still carries a value",
			output: `CUDA version        : 13.2
CUDA UMD version    : 13.3
`,
			want: "13.3",
		},
		{name: "empty output", output: "", want: ""},
		{name: "no cuda line", output: "NVIDIA-SMI version  : 590.48.01\n", want: ""},
		{
			name:   "deprecation prose without a UMD line stays empty",
			output: `CUDA version        : Deprecated, see "CUDA UMD version" instead` + "\n",
			want:   "",
		},
		{
			name:   "non-version value is not exported",
			output: "CUDA Version        : N/A\n",
			want:   "",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.want, nvidiasmi.ParseCudaVersion(testCase.output))
		})
	}
}

func TestQueryCudaVersion(t *testing.T) {
	t.Parallel()

	run := func(cmd *exec.Cmd) error {
		assert.Equal(t, []string{"--version"}, cmd.Args[1:])

		_, err := cmd.Stdout.Write([]byte("CUDA Version        : 13.1\n"))

		return err //nolint:wrapcheck // test stub
	}

	got := nvidiasmi.QueryCudaVersion(t.Context(), "nvidia-smi", time.Second, run, slogt.New(t))
	assert.Equal(t, "13.1", got)
}

func TestQueryCudaVersionCommandFailure(t *testing.T) {
	t.Parallel()

	run := func(*exec.Cmd) error { return errors.New("boom") }

	got := nvidiasmi.QueryCudaVersion(t.Context(), "nvidia-smi", time.Second, run, slogt.New(t))
	assert.Empty(t, got)
}
