package fakesmi

import (
	"bytes"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// valueGen produces the value served for one field. It is called once per data
// row, so a range yields an independent draw for every GPU or process row.
type valueGen func() string

// rangeSpec is an inclusive numeric range a field's value is drawn from.
type rangeSpec struct {
	Min float64 `yaml:"min"`
	Max float64 `yaml:"max"`
}

// fileConfig is the YAML schema for --config: the same settings the flags carry.
type fileConfig struct {
	Capture   string                   `yaml:"capture"`
	State     string                   `yaml:"state"`
	Seed      *int64                   `yaml:"seed"`
	Overrides map[string]overrideEntry `yaml:"overrides"`
	Exit      *int                     `yaml:"exit"`
	Delay     string                   `yaml:"delay"`
	StderrMsg string                   `yaml:"stderr-msg"` //nolint:tagliatelle // matches the --stderr-msg flag
	FailArg   string                   `yaml:"fail-arg"`   //nolint:tagliatelle // matches the --fail-arg flag
}

// overrideEntry is one field's override in the config. It is either a fixed
// value (written as a scalar, or as `value: ...`) or a range (`min` and `max`).
type overrideEntry struct {
	fixed *string
	rng   *rangeSpec
}

// UnmarshalYAML accepts a scalar (a fixed value) or a mapping with either
// `value` (fixed) or `min` and `max` (a range), and rejects anything else.
func (e *overrideEntry) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		e.fixed = &node.Value

		return nil
	}

	if node.Kind != yaml.MappingNode {
		return errors.New("override must be a value or a mapping with value or min/max")
	}

	for i := 0; i < len(node.Content); i += 2 {
		if key := node.Content[i].Value; key != "value" && key != "min" && key != "max" {
			return fmt.Errorf("unknown override key %q, want value or min/max", key)
		}
	}

	var spec struct {
		Value *string  `yaml:"value"`
		Min   *float64 `yaml:"min"`
		Max   *float64 `yaml:"max"`
	}

	if err := node.Decode(&spec); err != nil {
		return fmt.Errorf("failed to decode override: %w", err)
	}

	return e.assign(spec.Value, spec.Min, spec.Max)
}

// assign records a decoded override as a fixed value or a range, rejecting a
// mix of the two or an incomplete range.
func (e *overrideEntry) assign(value *string, minVal, maxVal *float64) error {
	switch {
	case value != nil && (minVal != nil || maxVal != nil):
		return errors.New("override cannot set both value and min/max")
	case value != nil:
		e.fixed = value
	case minVal != nil && maxVal != nil:
		e.rng = &rangeSpec{Min: *minVal, Max: *maxVal}
	default:
		return errors.New("override needs a value, or both min and max")
	}

	return nil
}

// loadFileConfig reads and strictly decodes a config file: an unknown key is an
// error rather than a silent no-op, so a typo surfaces.
func loadFileConfig(path string) (*fileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config %q: %w", path, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var cfg fileConfig
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config %q: %w", path, err)
	}

	return &cfg, nil
}

// rawOverride is a not-yet-resolved override for one field: either a fixed value
// or a range. Ranges are resolved into a generator once the seed is known.
type rawOverride struct {
	field string
	fixed *string
	rng   *rangeSpec
}

// parseRange parses a "min:max" flag value into a validated range.
func parseRange(field, raw string) (rangeSpec, error) {
	minRaw, maxRaw, ok := strings.Cut(raw, ":")
	if !ok {
		return rangeSpec{}, fmt.Errorf("invalid --set-range for %q, want min:max", field)
	}

	minVal, err := strconv.ParseFloat(strings.TrimSpace(minRaw), 64)
	if err != nil {
		return rangeSpec{}, fmt.Errorf("invalid --set-range min for %q: %w", field, err)
	}

	maxVal, err := strconv.ParseFloat(strings.TrimSpace(maxRaw), 64)
	if err != nil {
		return rangeSpec{}, fmt.Errorf("invalid --set-range max for %q: %w", field, err)
	}

	return validateRange(field, rangeSpec{Min: minVal, Max: maxVal})
}

// validateRange rejects non-finite bounds and min greater than max.
func validateRange(field string, spec rangeSpec) (rangeSpec, error) {
	if math.IsNaN(spec.Min) || math.IsInf(spec.Min, 0) || math.IsNaN(spec.Max) || math.IsInf(spec.Max, 0) {
		return rangeSpec{}, fmt.Errorf("range for %q must have finite bounds", field)
	}

	if spec.Min > spec.Max {
		return rangeSpec{}, fmt.Errorf("range for %q has min %v greater than max %v", field, spec.Min, spec.Max)
	}

	return spec, nil
}

// validateSetValue rejects a comma or line break, which would corrupt the CSV
// the exporter splits on commas. It mirrors addOverride's guard.
func validateSetValue(field, value string) error {
	if strings.ContainsAny(value, ",\r\n") {
		return fmt.Errorf("value for %q must not contain a comma or newline", field)
	}

	return nil
}

// constGen serves a fixed value on every row.
func constGen(value string) valueGen {
	return func() string { return value }
}

// rangeGen serves a fresh uniform draw from the range on every row. Each field
// gets its own random source seeded from (seed, field name), so a seed
// reproduces the values regardless of how many other fields are ranged or in
// what order they are stored.
func rangeGen(field string, spec rangeSpec, seed int64) valueGen {
	//nolint:gosec // not cryptographic: deterministic fake test data
	src := rand.New(rand.NewSource(fieldSeed(seed, field)))
	whole := spec.Min == math.Trunc(spec.Min) && spec.Max == math.Trunc(spec.Max)

	return func() string {
		value := spec.Min + src.Float64()*(spec.Max-spec.Min)
		if whole {
			return strconv.FormatInt(int64(math.Round(value)), 10)
		}
		// fixed-point ('f' never uses exponent form, which the exporter's number
		// extractor rejects), full precision so a narrow range is never rounded
		// outside its bounds
		return strconv.FormatFloat(value, 'f', -1, 64)
	}
}

// fieldSeed derives a per-field random seed, so field order never affects
// reproducibility and one field's values do not shift when another is ranged.
func fieldSeed(seed int64, field string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(field))

	//nolint:gosec // deterministic fake seed, the bit pattern is what we want
	return seed ^ int64(h.Sum64())
}

// buildOverrides resolves the config base and the flag overlay into the final
// per-field generator map. Config entries come first, flag overrides last, so a
// flag wins per field.
func buildOverrides(fc *fileConfig, flagOps []rawOverride, seed int64) (map[string]valueGen, error) {
	var ordered []rawOverride

	if fc != nil {
		for field, entry := range fc.Overrides {
			if entry.fixed != nil {
				if err := validateSetValue(field, *entry.fixed); err != nil {
					return nil, err
				}

				ordered = append(ordered, rawOverride{field: field, fixed: entry.fixed})

				continue
			}

			if _, err := validateRange(field, *entry.rng); err != nil {
				return nil, err
			}

			ordered = append(ordered, rawOverride{field: field, rng: entry.rng})
		}
	}

	ordered = append(ordered, flagOps...)

	out := make(map[string]valueGen, len(ordered))
	for _, op := range ordered {
		if op.fixed != nil {
			out[op.field] = constGen(*op.fixed)
		} else {
			out[op.field] = rangeGen(op.field, *op.rng, seed)
		}
	}

	return out, nil
}
