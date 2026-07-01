//nolint:testpackage // exercises unexported field-exclusion helpers directly
package nvidiasmi

import (
	"log/slog"
	"slices"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestMatchesAnyPattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		exclude string
		field   string
		want    bool
	}{
		{name: "exact match", exclude: "alpha.beta", field: "alpha.beta", want: true},
		{name: "exact non-match", exclude: "alpha.beta", field: "alpha.gamma", want: false},
		{
			name:    "trailing wildcard matches family",
			exclude: "alpha.histogram.*",
			field:   "alpha.histogram.max",
			want:    true,
		},
		{name: "trailing wildcard non-match", exclude: "alpha.histogram.*", field: "alpha.correctable", want: false},
		{name: "mid wildcard", exclude: "alpha.*.total", field: "alpha.volatile.total", want: true},
		{name: "dot is literal not any-char", exclude: "alpha.beta", field: "alphaXbeta", want: false},
		{name: "one of several patterns", exclude: "one,two,alpha.beta", field: "alpha.beta", want: true},
		{name: "whitespace is trimmed", exclude: " alpha.beta , gamma.delta ", field: "gamma.delta", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			patterns := parseFieldExcludePatterns(tt.exclude)
			if got := matchesAnyPattern(tt.field, patterns); got != tt.want {
				t.Errorf("matchesAnyPattern(%q, %q) = %v, want %v", tt.field, tt.exclude, got, tt.want)
			}
		})
	}
}

func TestFilterExcludedQFields(t *testing.T) {
	t.Parallel()

	// UUIDQField/nameQField/driverVersionQField are required fields (they back the
	// gpu_info metric) and must never be dropped. The rest are arbitrary.
	in := []QField{
		UUIDQField, nameQField, driverVersionQField,
		"alpha.one", "alpha.two",
		"beta.histogram.max", "beta.histogram.low",
	}

	tests := []struct {
		name    string
		exclude string
		want    []QField
	}{
		{
			name:    "empty exclude keeps everything",
			exclude: "",
			want:    in,
		},
		{
			name:    "exact field removed",
			exclude: "alpha.one",
			want: []QField{
				UUIDQField, nameQField, driverVersionQField,
				"alpha.two",
				"beta.histogram.max", "beta.histogram.low",
			},
		},
		{
			name:    "wildcard removes whole family",
			exclude: "beta.histogram.*",
			want: []QField{
				UUIDQField, nameQField, driverVersionQField,
				"alpha.one", "alpha.two",
			},
		},
		{
			name:    "required fields are never excluded",
			exclude: string(UUIDQField) + "," + string(nameQField) + "," + string(driverVersionQField),
			want:    in,
		},
		{
			name:    "wildcard does not drop protected fields it happens to match",
			exclude: "*",
			want:    []QField{UUIDQField, nameQField, driverVersionQField},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := filterExcludedQFields(slices.Clone(in), tt.exclude, discardLogger())
			if !slices.Equal(got, tt.want) {
				t.Errorf("filterExcludedQFields(exclude=%q) =\n  %v\nwant\n  %v", tt.exclude, got, tt.want)
			}
		})
	}
}
