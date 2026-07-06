package nvidiasmi

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/util"
)

const floatBitSize = 64

var numericRegex = regexp.MustCompile(`[+-]?(\d*[.])?\d+`)

// ErrAbsentValue marks a value that an enum field reports as expectedly
// unavailable (for example on a GPU that does not support the field). Callers
// skip these quietly. It is deliberately distinct from an unrecognized value,
// which is unexpected and should be surfaced rather than dropped silently.
var ErrAbsentValue = errors.New("value is an expected-absent token")

// enumFieldAbsentTokens are the values that mean "this field exists but is not
// available here". This is IsKnownAbsentValue minus "[unknown error]": for a
// health field an unknown error is a retrieval failure worth surfacing, not a
// clean unsupported state.
var enumFieldAbsentTokens = map[string]struct{}{
	"": {}, "n/a": {}, "[n/a]": {}, "[not supported]": {}, "[insufficient permissions]": {},
}

// fieldValueMappers maps a query field whose value is an NVML enum string to a
// function turning that string into the field's native NVML enum integer.
// Fields not listed fall through to the generic TransformRawValue.
var fieldValueMappers = map[QField]func(string) (float64, error){
	// nvmlGpuRecoveryAction_t. 0 (None) is healthy; any non-zero value is a
	// recovery action the driver recommends, so `> 0` means "needs attention".
	// Both the short and prefixed spellings are accepted because nvidia-smi's
	// exact wording is not fully pinned down (no bad-GPU capture to verify);
	// anything unrecognized is reported by the exporter rather than dropped.
	gpuRecoveryActionQField: enumValueMapper(gpuRecoveryActionQField, map[string]float64{
		"none": 0, "gpu reset": 1, "reset": 1, "node reboot": 2, "reboot": 2,
		"drain p2p": 3, "drain and reset": 4,
	}),
	// nvmlGpuFabricState_t. Higher is further along registration; 3 (Completed)
	// is the healthy steady state.
	fabricStateQField: enumValueMapper(fabricStateQField, map[string]float64{
		"not supported": 0, "not started": 1, "in progress": 2, "completed": 3,
	}),
}

// IsEnumMappedField reports whether the field's value is mapped through an NVML
// enum table. The exporter uses this to surface an unrecognized value loudly
// instead of dropping it like an ordinary non-numeric field.
func IsEnumMappedField(qField QField) bool {
	_, ok := fieldValueMappers[qField]

	return ok
}

// enumValueMapper builds a mapper for an NVML enum field: expected-absent tokens
// resolve to ErrAbsentValue (skipped quietly), recognized strings map to their
// native integer, and any other value returns a plain error so it is never
// turned into a bogus number and the exporter can report it.
func enumValueMapper(field QField, table map[string]float64) func(string) (float64, error) {
	return func(raw string) (float64, error) {
		key := strings.ToLower(strings.TrimSpace(raw))

		if _, absent := enumFieldAbsentTokens[key]; absent {
			return 0, ErrAbsentValue
		}

		if v, ok := table[key]; ok {
			return v, nil
		}

		return 0, fmt.Errorf("field %q value %q is not a recognized enum value", field, raw)
	}
}

// TransformFieldValue transforms a raw value into a float64, applying the
// field's NVML enum mapping when it has one and otherwise falling back to the
// generic transform. Keeping the field-aware mapping here leaves the exporter's
// render loop unaware of any per-field value semantics.
func TransformFieldValue(qField QField, rawValue string, valueMultiplier float64) (float64, error) {
	if mapper, ok := fieldValueMappers[qField]; ok {
		return mapper(rawValue)
	}

	return TransformRawValue(rawValue, valueMultiplier)
}

// TransformRawValue transforms a raw value into a float64.
func TransformRawValue(rawValue string, valueMultiplier float64) (float64, error) {
	trimmed := strings.TrimSpace(rawValue)
	if strings.HasPrefix(trimmed, "0x") {
		decimal, err := util.HexToDecimal(trimmed)
		if err != nil {
			return 0, fmt.Errorf("failed to transform raw value %q: %w", trimmed, err)
		}

		return decimal, nil
	}

	val := strings.ToLower(trimmed)

	switch val {
	case "enabled", "yes", "active":
		return 1, nil
	case "disabled", "no", "not active":
		return 0, nil
	case "default":
		return 0, nil
	case "exclusive_thread":
		return 1, nil
	case "prohibited":
		return 2, nil
	case "exclusive_process":
		return 3, nil
	default:
		return parseSanitizedValueWithBestEffort(val, valueMultiplier)
	}
}

func parseSanitizedValueWithBestEffort(
	sanitizedValue string,
	valueMultiplier float64,
) (float64, error) {
	allNums := numericRegex.FindAllString(sanitizedValue, 2)
	if len(allNums) != 1 {
		return -1, fmt.Errorf("could not parse number from value: %q", sanitizedValue)
	}

	parsed, err := strconv.ParseFloat(allNums[0], floatBitSize)
	if err != nil {
		return -1, fmt.Errorf("failed to parse float %q: %w", allNums[0], err)
	}

	return parsed * valueMultiplier, nil
}
