package exporter

import (
	"context"
	"log/slog"
	"reflect"
	"regexp"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

var fqNameInDesc = regexp.MustCompile(`fqName: "([^"]*)"`)

// allFeatures returns a Features value with every boolean toggle turned on.
// Reflection sets every bool field, so a newly added feature flag cannot be
// missed by the reserved-name or descriptor-registration tests.
func allFeatures(t *testing.T) Features {
	t.Helper()

	var features Features

	val := reflect.ValueOf(&features).Elem()
	for idx := range val.NumField() {
		field := val.Field(idx)
		if field.Kind() != reflect.Bool {
			t.Fatalf("unexpected non-bool field %s on exporter.Features", val.Type().Field(idx).Name)
		}

		field.SetBool(true)
	}

	return features
}

// TestReservedMetricNamesCoverAll pins fixedMetricNames against what the
// exporter actually describes with every feature on. It is what keeps the list
// from rotting: a new metric family added without a matching entry would let a
// returned field claim its name and make every scrape fail to register.
func TestReservedMetricNamesCoverAll(t *testing.T) {
	t.Parallel()

	fields := nvidiasmi.ResolvedFields{
		Info: []nvidiasmi.InfoField{{QField: nvidiasmi.UUIDQField, Label: "uuid"}},
	}

	features := allFeatures(t)

	for _, exitCodeMetric := range []ExitCodeMetric{ExecExitCodeMetric, NVMLReturnCodeMetric} {
		exp := New(context.Background(), DefaultPrefix, fields, emptySource{},
			features, nil, exitCodeMetric, slog.New(slog.DiscardHandler))

		reserved := reservedMetricNames(DefaultPrefix, exitCodeMetric)

		descCh := make(chan *prometheus.Desc, 128)

		go func() {
			exp.Describe(descCh)
			close(descCh)
		}()

		for desc := range descCh {
			match := fqNameInDesc.FindStringSubmatch(desc.String())
			if match == nil {
				t.Fatalf("could not read the metric name out of %s", desc)
			}

			if _, ok := reserved[match[1]]; !ok {
				t.Errorf("metric %q is described but not reserved: add it to fixedMetricNames, "+
					"or a returned field of the same name would make every scrape fail to register",
					match[1])
			}
		}
	}
}
