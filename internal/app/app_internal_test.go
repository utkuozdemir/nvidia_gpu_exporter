package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestValidateBackendFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		backend        string
		command        string
		pcieThroughput bool
		wantErr        string
	}{
		{name: "exec defaults", backend: backendExec, command: "nvidia-smi"},
		{name: "exec with custom command", backend: backendExec, command: "sudo nvidia-smi"},
		{name: "nvml defaults", backend: backendNVML, command: "nvidia-smi"},
		{name: "nvml with pcie throughput", backend: backendNVML, command: "nvidia-smi", pcieThroughput: true},
		{
			name: "nvml rejects custom command", backend: backendNVML, command: "sudo nvidia-smi",
			wantErr: "--nvidia-smi-command cannot be combined",
		},
		{
			name: "exec rejects pcie throughput", backend: backendExec, command: "nvidia-smi",
			pcieThroughput: true,
			wantErr:        "--collect.pcie-throughput requires --collect.backend=nvml",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := validateBackendFlags(testCase.backend, testCase.command, testCase.pcieThroughput)
			if testCase.wantErr == "" {
				assert.NoError(t, err)

				return
			}

			assert.ErrorContains(t, err, testCase.wantErr)
		})
	}
}
