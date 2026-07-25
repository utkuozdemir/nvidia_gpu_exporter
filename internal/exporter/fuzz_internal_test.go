package exporter

import (
	"context"
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

// emptySource serves a snapshot with no reading, which is enough to register.
type emptySource struct{}

func (emptySource) Latest(context.Context) collect.Snapshot { return collect.Snapshot{} }

// FuzzExporterRegisters asserts the property the whole metric surface depends
// on: whatever returned field names a driver hands back, the exporter registers
// as one collector. Registration happens per scrape against a fresh registry,
// so a descriptor the registry rejects answers every scrape with an error
// rather than costing a single series. The exporter is built and registered
// whole, with every feature on, because that is what production does and
// because a query field can collide with a family the exporter owns, not just
// with another query field.
func FuzzExporterRegisters(f *testing.F) {
	f.Add("utilization.gpu [%]", "memory.total [MiB]")
	f.Add("power.draw [W]", "clocks.current.sm [MHz]")
	f.Add("", "x")
	f.Add("foo.bar", "foo_bar")
	f.Add("x [W]", "x_watts")
	f.Add("gpu.info", "failed_scrapes_total")
	f.Add("energy_joules_total", "xid_errors_total")
	f.Fuzz(func(t *testing.T, first, second string) {
		returned := map[nvidiasmi.QField]nvidiasmi.RField{
			"first":  nvidiasmi.RField(first),
			"second": nvidiasmi.RField(second),
		}

		fields := nvidiasmi.ResolvedFields{
			Query:    []nvidiasmi.QField{"first", "second"},
			Returned: returned,
			Info:     []nvidiasmi.InfoField{{QField: nvidiasmi.UUIDQField, Label: "uuid"}},
		}

		features := Features{
			ComputeApps: true, ComputeAppMIGLabels: true, PCIeThroughput: true,
			Energy: true, MIG: true, XIDEvents: true,
		}

		exp := New(context.Background(), DefaultPrefix, fields, emptySource{},
			features, nil, NVMLReturnCodeMetric, slog.New(slog.DiscardHandler))

		if err := prometheus.NewRegistry().Register(exp); err != nil {
			t.Fatalf("returned fields %q and %q make the exporter unregistrable: %v", first, second, err)
		}
	})
}
