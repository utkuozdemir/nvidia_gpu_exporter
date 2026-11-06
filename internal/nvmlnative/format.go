package nvmlnative

import (
	"fmt"
	"strconv"
)

// The formatters below render NVML readings for the shared transform/render
// pipeline, keeping unit suffixes and enum spellings compatible with
// nvidia-smi while preserving NVML's native numeric precision.

// Absence and error tokens, mapped from NVML return codes the way nvidia-smi
// prints them.
//
//nolint:gosec // G101: false positive, these are nvidia-smi output tokens, not credentials
const (
	tokenNotAvailable            = "[N/A]"
	tokenNoPermission            = "[Insufficient Permissions]"
	tokenNotFound                = "[Not Found]"
	tokenUnknownError            = "[Unknown Error]"
	tokenFunctionNotFound        = "[Function Not Found]"
	tokenDeprecated              = "[Requested functionality has been deprecated]"
	tokenBareNotAvailable        = "N/A" // fabric.* and temperature.memory print it without brackets
	timestampLayout              = "2006/01/02 15:04:05.000"
	mibDivisor            uint64 = 1024 * 1024
)

func onOff(enabled bool) string {
	if enabled {
		return "Enabled"
	}

	return "Disabled"
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}

	return "No"
}

func milliwatts(mw uint32) string {
	return strconv.FormatFloat(float64(mw)/1000.0, 'f', -1, 64) + " W"
}

// cudaVersionStr renders the encoded integer from
// nvmlSystemGetCudaDriverVersion (major*1000 + minor*10) the way nvidia-smi
// prints the CUDA version: 13010 -> "13.1".
func cudaVersionStr(version int) string {
	return fmt.Sprintf("%d.%d", version/1000, (version%1000)/10)
}

func mhz(v uint32) string { return fmt.Sprintf("%d MHz", v) }

// mib formats a byte count in MiB without rounding, so multiplying by
// 1048576 in the exporter reproduces the exact byte count.
func mib(bytes uint64) string {
	return strconv.FormatFloat(float64(bytes)/float64(mibDivisor), 'f', -1, 64) + " MiB"
}

func pct(v uint32) string { return fmt.Sprintf("%d %%", v) }

func computeModeStr(mode int32) string {
	switch mode {
	case 0:
		return "Default"
	case 1:
		return "Exclusive_Thread"
	case 2:
		return "Prohibited"
	case 3:
		return "Exclusive_Process"
	default:
		return strconv.Itoa(int(mode))
	}
}

func driverModelStr(model int32) string {
	switch model {
	case 0:
		return "WDDM"
	case 1:
		return "TCC"
	case 2:
		return "MCDM"
	default:
		return fmt.Sprintf("Unknown(%d)", model)
	}
}

func gomStr(mode int32) string {
	switch mode {
	case 0:
		return "All On"
	case 1:
		return "Compute"
	case 2:
		return "Low Double Precision"
	default:
		return strconv.Itoa(int(mode))
	}
}

// addressingModeStr spells nvmlDeviceAddressingMode values. "None" and "HMM"
// are capture-verified; "ATS" is the documented third state.
func addressingModeStr(mode uint32) string {
	switch mode {
	case 0:
		return "None"
	case 1:
		return "HMM"
	case 2:
		return "ATS"
	default:
		return strconv.FormatUint(uint64(mode), 10)
	}
}

// fabricStateStr spells nvmlGpuFabricState values. State 0 (not supported)
// prints the bare N/A token, brackets deliberately absent (capture-verified).
func fabricStateStr(state uint8) string {
	switch state {
	case 0:
		return tokenBareNotAvailable
	case 1:
		return "Not Started"
	case 2:
		return "In Progress"
	case 3:
		return "Completed"
	default:
		return strconv.FormatUint(uint64(state), 10)
	}
}

// recoveryActionStr spells nvmlDeviceGpuRecoveryAction values. Only "None"
// is capture-verified; the failure spellings follow the NVML enum names and
// the exporter's enum mapper accepts both spellings of each.
func recoveryActionStr(action uint64) string {
	switch action {
	case 0:
		return "None"
	case 1:
		return "GPU Reset"
	case 2:
		return "Node Reboot"
	case 3:
		return "Drain P2P"
	case 4:
		return "Drain and Reset"
	default:
		return strconv.FormatUint(action, 10)
	}
}

func peerTypeStr(peerType uint8) string {
	switch peerType {
	case 0:
		return "Direct Connected"
	case 1:
		return "Switch Connected"
	default:
		return strconv.FormatUint(uint64(peerType), 10)
	}
}

func activeNotActive(mask, bit uint64) string {
	if mask&bit != 0 {
		return "Active"
	}

	return "Not Active"
}

func uuidBytes(b [16]uint8) string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func cstr(buf []byte) string {
	for i, c := range buf {
		if c == 0 {
			return string(buf[:i])
		}
	}

	return string(buf)
}

func i8str(chars []int8) string {
	u := make([]byte, len(chars))
	for i, c := range chars {
		u[i] = byte(c) //nolint:gosec // G115: reinterpreting C char bytes, overflow is the point
	}

	return cstr(u)
}
