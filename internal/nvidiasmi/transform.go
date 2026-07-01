package nvidiasmi

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/util"
)

const floatBitSize = 64

var numericRegex = regexp.MustCompile(`[+-]?(\d*[.])?\d+`)

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
