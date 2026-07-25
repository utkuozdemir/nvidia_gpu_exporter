package app

import (
	"context"
	"net/http"
	"testing"
)

// FuzzValidateMetricsPath asserts what validateMetricsPath exists for: an
// accepted telemetry path must be registrable on the mux beside the exporter's
// own routes without panicking, and must not shadow them. Route registration
// panics are startup crashes, and a shadowed health route is a silent outage.
func FuzzValidateMetricsPath(f *testing.F) {
	f.Add("/metrics")
	f.Add("/-/healthy")
	f.Add("/debug/pprof/x")
	f.Add("/a b")
	f.Fuzz(func(t *testing.T, metricsPath string) {
		if err := validateMetricsPath(metricsPath); err != nil {
			return
		}

		mux := http.NewServeMux()

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("accepted path %q panics route registration: %v", metricsPath, r)
				}
			}()

			mux.Handle("GET /{$}", http.NotFoundHandler())
			mux.Handle("GET "+metricsPath, http.NotFoundHandler())
			mux.HandleFunc("GET /-/healthy", healthHandler("Healthy"))
			mux.HandleFunc("GET /-/ready", healthHandler("Ready"))
			registerPprof(mux)
		}()

		// the accepted path must not swallow a reserved route
		for _, reserved := range []string{"/-/healthy", "/-/ready", "/debug/pprof/", "/debug/pprof/profile"} {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://x"+reserved, nil)
			if err != nil {
				continue
			}

			if _, pattern := mux.Handler(req); pattern == "GET "+metricsPath {
				t.Fatalf("accepted path %q captures the reserved route %q", metricsPath, reserved)
			}
		}
	})
}
