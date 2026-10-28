package app

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

func TestValidateMetricsPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path    string
		wantErr bool
	}{
		{path: "/metrics", wantErr: false},
		{path: "/some/nested/path", wantErr: false},
		{path: "/-/metrics", wantErr: false},
		// prefixes of reserved routes that are not the routes themselves are fine
		{path: "/-/healthy-metrics", wantErr: false},
		{path: "/debug/pprofile", wantErr: false},
		{path: "metrics", wantErr: true},
		{path: "", wantErr: true},
		{path: "/", wantErr: true},
		{path: "/metrics/", wantErr: true},
		{path: "/metrics{a}", wantErr: true},
		{path: "/met rics", wantErr: true},
		{path: "/met\trics", wantErr: true},
		// unclean paths would panic the mux registration as unmatchable
		{path: "//metrics", wantErr: true},
		{path: "/foo/../metrics", wantErr: true},
		{path: "/./metrics", wantErr: true},
		// escapes could disguise a collision (this one unescapes to /-/healthy)
		{path: "/-/%68ealthy", wantErr: true},
		// query and fragment characters would register a route that can never
		// be reached the way it reads
		{path: "/metrics?x=1", wantErr: true},
		{path: "/metrics#frag", wantErr: true},
		{path: "/-/healthy", wantErr: true},
		{path: "/-/ready", wantErr: true},
		{path: "/debug/pprof", wantErr: true},
		{path: "/debug/pprof/heap", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()

			err := validateMetricsPath(tt.path)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

//nolint:funlen // a table of header cases, long but flat
func TestScrapeContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		header       string
		offset       time.Duration
		wantDeadline bool
		// wantTimeout is the expected remaining time when a deadline is set,
		// asserted loosely to stay clock-safe
		wantTimeout time.Duration
	}{
		{name: "no header means no deadline", header: "", wantDeadline: false},
		{name: "malformed header means no deadline", header: "not-a-number", wantDeadline: false},
		{name: "zero means no deadline", header: "0", wantDeadline: false},
		{name: "negative means no deadline", header: "-5", wantDeadline: false},
		{name: "nan means no deadline", header: "NaN", wantDeadline: false},
		{name: "absurdly large means no deadline", header: "1e30", wantDeadline: false},
		{
			name:         "smaller than the offset means no deadline",
			header:       "0.2",
			offset:       500 * time.Millisecond,
			wantDeadline: false,
		},
		{
			name:         "equal to the offset means no deadline",
			header:       "0.5",
			offset:       500 * time.Millisecond,
			wantDeadline: false,
		},
		{
			name:         "normal value sets a deadline",
			header:       "10",
			offset:       500 * time.Millisecond,
			wantDeadline: true,
			wantTimeout:  9500 * time.Millisecond,
		},
		{
			name:         "fractional value sets a deadline",
			header:       "2.5",
			offset:       500 * time.Millisecond,
			wantDeadline: true,
			wantTimeout:  2 * time.Second,
		},
		{
			name:         "zero offset uses the full advertised timeout",
			header:       "3",
			offset:       0,
			wantDeadline: true,
			wantTimeout:  3 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
			if tt.header != "" {
				req.Header.Set(scrapeTimeoutHeader, tt.header)
			}

			start := time.Now()

			ctx, cancel := scrapeContext(req, tt.offset)
			defer cancel()

			deadline, hasDeadline := ctx.Deadline()
			require.Equal(t, tt.wantDeadline, hasDeadline)

			if tt.wantDeadline {
				remaining := time.Until(deadline) + time.Since(start)
				assert.InDelta(t, tt.wantTimeout.Seconds(), remaining.Seconds(), 0.2)
			}
		})
	}
}

//nolint:funlen // table-driven flag matrix
func TestValidateBackendFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		flags   backendFlagSet
		wantErr string
	}{
		{name: "exec defaults", flags: backendFlagSet{backend: backendExec, nvidiaSmiCommand: "nvidia-smi"}},
		{
			name:  "exec with custom command",
			flags: backendFlagSet{backend: backendExec, nvidiaSmiCommand: "sudo nvidia-smi"},
		},
		{name: "nvml defaults", flags: backendFlagSet{backend: backendNVML, nvidiaSmiCommand: "nvidia-smi"}},
		{
			name:  "nvml with pcie throughput",
			flags: backendFlagSet{backend: backendNVML, nvidiaSmiCommand: "nvidia-smi", pcieThroughput: true},
		},
		{
			name: "nvml with compute apps mig",
			flags: backendFlagSet{
				backend: backendNVML, nvidiaSmiCommand: "nvidia-smi",
				computeApps: true, computeAppsMIG: true,
			},
		},
		{
			name: "nvml with graphics apps and mig attribution",
			flags: backendFlagSet{
				backend: backendNVML, nvidiaSmiCommand: "nvidia-smi",
				graphicsApps: true, computeAppsMIG: true,
			},
		},
		{
			name:    "exec rejects graphics apps",
			flags:   backendFlagSet{backend: backendExec, nvidiaSmiCommand: "nvidia-smi", graphicsApps: true},
			wantErr: "--collect.graphics-apps requires --collect.backend=nvml or demo",
		},
		{
			name:    "nvml rejects custom command",
			flags:   backendFlagSet{backend: backendNVML, nvidiaSmiCommand: "sudo nvidia-smi"},
			wantErr: "--nvidia-smi-command cannot be combined",
		},
		{
			name:    "exec rejects pcie throughput",
			flags:   backendFlagSet{backend: backendExec, nvidiaSmiCommand: "nvidia-smi", pcieThroughput: true},
			wantErr: "--collect.pcie-throughput requires --collect.backend=nvml",
		},
		{
			name: "exec rejects compute apps mig",
			flags: backendFlagSet{
				backend: backendExec, nvidiaSmiCommand: "nvidia-smi",
				computeApps: true, computeAppsMIG: true,
			},
			wantErr: "--collect.compute-apps-mig requires --collect.backend=nvml",
		},
		{
			name: "compute apps mig requires compute apps",
			flags: backendFlagSet{
				backend: backendNVML, nvidiaSmiCommand: "nvidia-smi", computeAppsMIG: true,
			},
			wantErr: "--collect.compute-apps-mig requires --collect.compute-apps",
		},
		{
			name: "compute apps identity on linux exec",
			flags: backendFlagSet{
				backend: backendExec, nvidiaSmiCommand: "nvidia-smi",
				computeApps: true, computeAppsIdentity: true, goos: "linux",
			},
		},
		{
			name: "compute apps identity with graphics apps on linux nvml",
			flags: backendFlagSet{
				backend: backendNVML, nvidiaSmiCommand: "nvidia-smi",
				graphicsApps: true, computeAppsIdentity: true, goos: "linux",
			},
		},
		{
			name: "compute apps identity on non-linux demo",
			flags: backendFlagSet{
				backend: backendDemo, nvidiaSmiCommand: "nvidia-smi",
				computeApps: true, computeAppsIdentity: true, goos: "darwin",
			},
		},
		{
			name: "compute apps identity requires compute or graphics apps",
			flags: backendFlagSet{
				backend: backendExec, nvidiaSmiCommand: "nvidia-smi",
				computeAppsIdentity: true, goos: "linux",
			},
			wantErr: "--collect.compute-apps-identity requires --collect.compute-apps or --collect.graphics-apps",
		},
		{
			name: "compute apps identity rejects custom command",
			flags: backendFlagSet{
				backend: backendExec, nvidiaSmiCommand: "ssh gpu-host nvidia-smi",
				computeApps: true, computeAppsIdentity: true, goos: "linux",
			},
			wantErr: "--collect.compute-apps-identity cannot be combined with --nvidia-smi-command",
		},
		{
			name: "compute apps identity rejects non-linux non-demo",
			flags: backendFlagSet{
				backend: backendExec, nvidiaSmiCommand: "nvidia-smi",
				computeApps: true, computeAppsIdentity: true, goos: "windows",
			},
			wantErr: "--collect.compute-apps-identity requires Linux or --collect.backend=demo",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := validateBackendFlags(testCase.flags)
			if testCase.wantErr == "" {
				assert.NoError(t, err)

				return
			}

			assert.ErrorContains(t, err, testCase.wantErr)
		})
	}
}

type countingFS struct {
	fs.FS

	opens map[string]int
}

func (c *countingFS) Open(name string) (fs.File, error) {
	c.opens[name]++

	//nolint:wrapcheck // test FS wrapper forwarding directly to fstest.MapFS
	return c.FS.Open(name)
}

//nolint:funlen // multi-cycle cgroup enrichment and deduplication assertions
func TestWrapWithProcessIdentity(t *testing.T) {
	t.Parallel()

	cid := "a3b8c9d0e1f2a3b8c9d0e1f2a3b8c9d0e1f2a3b8c9d0e1f2a3b8c9d0e1f2a3b8"

	mapFS := fstest.MapFS{
		"100/cgroup": &fstest.MapFile{
			Data: []byte("0::/system.slice/docker-" + cid + ".scope\n"),
		},
		"200/cgroup": &fstest.MapFile{
			Data: []byte("0::/system.slice/ollama.service\n"),
		},
		"400/cgroup": &fstest.MapFile{
			Data: []byte("0::/system.slice/unrelated-local.service\n"),
		},
	}

	procFS := &countingFS{
		FS:    mapFS,
		opens: make(map[string]int),
	}

	inner := func(context.Context) (collect.Reading, int, error) {
		return collect.Reading{
			Apps: []nvidiasmi.ComputeApp{
				{GPUUUID: "gpu-0", PID: "100", ProcessName: "python"},
				// same PID on a second GPU: must reuse the cached lookup
				{GPUUUID: "gpu-1", PID: "100", ProcessName: "python"},
				// vanished process: missing /proc/<pid>/cgroup leaves both empty
				{GPUUUID: "gpu-0", PID: "300", ProcessName: "python"},
				// container without hostPID: [Not Found] must skip /proc
				{GPUUUID: "gpu-0", PID: "400", ProcessName: "[Not Found]"},
			},
			GraphicsApps: []nvidiasmi.ComputeApp{
				// same PID in graphics list: must reuse the cached lookup
				{GPUUUID: "gpu-0", PID: "100", ProcessName: "python"},
				{GPUUUID: "gpu-0", PID: "200", ProcessName: "ollama"},
			},
		}, 0, nil
	}

	wrapped := wrapWithProcessIdentity(procFS, inner)
	reading, code, err := wrapped(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, code)

	assert.Equal(t, cid, reading.Apps[0].ContainerID)
	assert.Empty(t, reading.Apps[0].SystemdUnit)
	assert.Equal(t, cid, reading.Apps[1].ContainerID)
	assert.Empty(t, reading.Apps[2].ContainerID)
	assert.Empty(t, reading.Apps[2].SystemdUnit)
	assert.Empty(t, reading.Apps[3].ContainerID)
	assert.Empty(t, reading.Apps[3].SystemdUnit)

	assert.Equal(t, cid, reading.GraphicsApps[0].ContainerID)
	assert.Empty(t, reading.GraphicsApps[1].ContainerID)
	assert.Equal(t, "ollama.service", reading.GraphicsApps[1].SystemdUnit)

	assert.Equal(t, 1, procFS.opens["100/cgroup"], "PID 100 must be read once across GPUs and app lists")
	assert.Equal(t, 1, procFS.opens["200/cgroup"])
	assert.Equal(t, 1, procFS.opens["300/cgroup"])
	assert.Zero(t, procFS.opens["400/cgroup"], "[Not Found] process must not touch /proc")

	// Next collection cycle must re-read /proc so reused PIDs get fresh identity.
	mapFS["100/cgroup"] = &fstest.MapFile{
		Data: []byte("0::/system.slice/vllm.service\n"),
	}

	reading2, _, err := wrapped(t.Context())
	require.NoError(t, err)
	assert.Empty(t, reading2.Apps[0].ContainerID)
	assert.Equal(t, "vllm.service", reading2.Apps[0].SystemdUnit)
	assert.Equal(t, 2, procFS.opens["100/cgroup"])
}
