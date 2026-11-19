package nvidiasmi

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/util"
)

const floatBitSize = 64

var (
	// ErrAbsentValue marks a value that an enum field reports as expectedly
	// unavailable (for example on a GPU that does not support the field). Callers
	// skip these quietly. It is deliberately distinct from an unrecognized value,
	// which is unexpected and should be surfaced rather than dropped silently.
	ErrAbsentValue = errors.New("value is an expected-absent token")

	errNotNumeric = errors.New("could not parse number from value")
	errNonFinite  = errors.New("value does not scale to a finite number")
)

// enumFieldAbsentTokens are the values that mean "this field exists but is not
// available here". This is IsKnownAbsentValue minus "[unknown error]": for a
// health field an unknown error is a retrieval failure worth surfacing, not a
// clean unsupported state. Drivers 525 through 570 print "state  N/A" and
// "status N/A" for fabric.state and fabric.status on GPUs without NVLink
// fabric.
var enumFieldAbsentTokens = map[string]struct{}{
	"": {}, "n/a": {}, "[n/a]": {}, "[not supported]": {}, "[insufficient permissions]": {},
	"[function not found]": {}, "state  n/a": {}, "status n/a": {},
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
	GPURecoveryActionQField: enumValueMapper(GPURecoveryActionQField, map[string]float64{
		"none": 0, "gpu reset": 1, "reset": 1, "node reboot": 2, "reboot": 2,
		"drain p2p": 3, "drain and reset": 4,
	}),
	// nvmlGpuFabricState_t. Higher is further along registration; 3 (Completed)
	// is the healthy steady state.
	fabricStateQField: enumValueMapper(fabricStateQField, map[string]float64{
		"not supported": 0, "not started": 1, "in progress": 2, "completed": 3,
	}),
	// nvmlReturn_t for the NVLink fabric registration status (0 = Success).
	fabricStatusQField: enumValueMapper(fabricStatusQField, map[string]float64{
		"success": 0,
	}),
	// nvmlDeviceAddressingModeType_t (0 = None, 1 = HMM, 2 = ATS).
	addressingModeQField: enumValueMapper(addressingModeQField, map[string]float64{
		"none": 0, "hmm": 1, "ats": 2,
	}),
	// nvmlGpuOperationMode_t (0 = All On, 1 = Compute, 2 = Low Double Precision).
	gomCurrentQField: enumValueMapper(gomCurrentQField, map[string]float64{
		"all on": 0, "compute": 1, "low double precision": 2,
	}),
	gomPendingQField: enumValueMapper(gomPendingQField, map[string]float64{
		"all on": 0, "compute": 1, "low double precision": 2,
	}),
	// nvmlPlatformInfo_t.peerType (0 = Direct Connected, 1 = Switch Connected).
	platformPeerTypeQField: enumValueMapper(platformPeerTypeQField, map[string]float64{
		"direct connected": 0, "switch connected": 1,
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
// resolve to ErrAbsentValue (skipped quietly), recognized strings or native
// bare integer values map to their integer, and any other value returns a plain
// error so it is never turned into a bogus number and the exporter can report it.
func enumValueMapper(field QField, table map[string]float64) func(string) (float64, error) {
	return func(raw string) (float64, error) {
		key := strings.ToLower(strings.TrimSpace(raw))

		if _, absent := enumFieldAbsentTokens[key]; absent {
			return 0, ErrAbsentValue
		}

		if v, ok := table[key]; ok {
			return v, nil
		}

		if v, err := strconv.ParseInt(key, 10, 64); err == nil {
			return float64(v), nil
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

	if val, ok := matchKeywordValue(trimmed); ok {
		return val, nil
	}

	return parseSanitizedValueWithBestEffort(trimmed, valueMultiplier)
}

// matchKeywordValue matches the non-numeric state tokens nvidia-smi prints
// without allocating a lowercased copy on ASCII inputs. Non-ASCII inputs fall
// back to strings.ToLower so Unicode case mappings (such as Turkish İ -> i)
// match the original behavior without letting strings.EqualFold match
// non-ASCII runes like long s (ſ).
func matchKeywordValue(val string) (float64, bool) {
	if !isASCII(val) {
		val = strings.ToLower(val)
		if !isASCII(val) {
			return 0, false
		}
	}

	switch {
	case strings.EqualFold(val, "enabled"), strings.EqualFold(val, "yes"),
		strings.EqualFold(val, "active"), strings.EqualFold(val, "valid"):
		return 1, true
	case strings.EqualFold(val, "disabled"), strings.EqualFold(val, "no"),
		strings.EqualFold(val, "not active"), strings.EqualFold(val, "invalid"),
		strings.EqualFold(val, "default"):
		return 0, true
	case strings.EqualFold(val, "exclusive_thread"):
		return 1, true
	case strings.EqualFold(val, "prohibited"):
		return 2, true
	case strings.EqualFold(val, "exclusive_process"):
		return 3, true
	default:
		return 0, false
	}
}

func isASCII(val string) bool {
	for idx := range len(val) {
		if val[idx] > unicode.MaxASCII {
			return false
		}
	}

	return true
}

func parseSanitizedValueWithBestEffort(
	sanitizedValue string,
	valueMultiplier float64,
) (float64, error) {
	numStr, ok := findSingleNumber(sanitizedValue)
	if !ok {
		return -1, errNotNumeric
	}

	parsed, err := strconv.ParseFloat(numStr, floatBitSize)
	if err != nil {
		return -1, fmt.Errorf("failed to parse float %q: %w", numStr, err)
	}

	scaled := parsed * valueMultiplier

	// A reading large enough to overflow once scaled into its base unit is not
	// a measurement. Reporting it as infinity would poison every rate and
	// average computed over the series, so it is dropped like any other
	// unusable value.
	if math.IsInf(scaled, 0) || math.IsNaN(scaled) {
		return -1, errNonFinite
	}

	return scaled, nil
}

// findSingleNumber scans input for non-overlapping matches of [+-]?(\d*[.])?\d+
// with leftmost-first semantics, returning the match only when there is
// exactly one. Equivalent to regexp.MustCompile(`[+-]?(\d*[.])?\d+`).FindAllString(input, 2)
// without heap allocations.
func findSingleNumber(input string) (string, bool) {
	var match string

	for pos := 0; pos < len(input); {
		end, ok := scanNumberAt(input, pos)
		if !ok {
			pos++

			continue
		}

		if match != "" {
			return "", false
		}

		match = input[pos:end]
		pos = end
	}

	return match, match != ""
}

func scanNumberAt(input string, start int) (int, bool) {
	end := start
	if input[end] == '+' || input[end] == '-' {
		end++
	}

	digitsStart := end
	for end < len(input) && isASCIIDigit(input[end]) {
		end++
	}

	if end+1 < len(input) && input[end] == '.' && isASCIIDigit(input[end+1]) {
		end += 2
		for end < len(input) && isASCIIDigit(input[end]) {
			end++
		}

		return end, true
	}

	return end, end > digitsStart
}

func isASCIIDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}
