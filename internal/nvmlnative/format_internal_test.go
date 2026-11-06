package nvmlnative

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMib verifies that mib formats byte counts without rounding so the
// exporter reproduces the exact native byte count.
func TestMib(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "478.25 MiB", mib(501481472))
	assert.Equal(t, "81559 MiB", mib(85520809984))
	assert.Equal(t, "0 MiB", mib(0))
	assert.Equal(t, "15.9921875 MiB", mib(16769024))
	assert.Equal(t, "81080.75 MiB", mib(85019328512))
}

func TestMilliwatts(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "654.543 W", milliwatts(654543))
	assert.Equal(t, "69.77 W", milliwatts(69770))
	assert.Equal(t, "0 W", milliwatts(0))
}

func TestEnumSpellings(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "Enabled", onOff(true))
	assert.Equal(t, "Disabled", onOff(false))
	assert.Equal(t, "Yes", yesNo(true))
	assert.Equal(t, "No", yesNo(false))
	assert.Equal(t, "345 MHz", mhz(345))
	assert.Equal(t, "0 %", pct(0))
	assert.Equal(t, "100 %", pct(100))
	assert.Equal(t, "All On", gomStr(0))
	assert.Equal(t, "Default", computeModeStr(0))
	assert.Equal(t, "Exclusive_Process", computeModeStr(3))
	assert.Equal(t, "WDDM", driverModelStr(0))
	assert.Equal(t, "TCC", driverModelStr(1))
	assert.Equal(t, "None", addressingModeStr(0))
	assert.Equal(t, "HMM", addressingModeStr(1))
	assert.Equal(t, "Completed", fabricStateStr(3))
	assert.Equal(t, "N/A", fabricStateStr(0))
	assert.Equal(t, "None", recoveryActionStr(0))
	assert.Equal(t, "Direct Connected", peerTypeStr(0))
	assert.Equal(t, "Switch Connected", peerTypeStr(1))
	assert.Equal(t, "Active", activeNotActive(0x5, 0x1))
	assert.Equal(t, "Not Active", activeNotActive(0x4, 0x1))
}

func TestCStrings(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "abc", cstr([]byte{'a', 'b', 'c', 0, 'x'}))
	assert.Equal(t, "abc", cstr([]byte("abc")))
	assert.Equal(
		t,
		"00000000:83:00.0",
		i8str([]int8{48, 48, 48, 48, 48, 48, 48, 48, 58, 56, 51, 58, 48, 48, 46, 48, 0}),
	)
}

func TestUUIDBytes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "00000000-0000-0000-0000-000000000000", uuidBytes([16]uint8{}))
	assert.Equal(t, "01020304-0506-0708-090a-0b0c0d0e0f10",
		uuidBytes([16]uint8{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}))
}

func TestCudaVersionStr(t *testing.T) {
	t.Parallel()

	// 13010 is the encoded form of the capture-verified "13.1" (driver 590)
	assert.Equal(t, "13.1", cudaVersionStr(13010))
	assert.Equal(t, "13.3", cudaVersionStr(13030))
	assert.Equal(t, "12.8", cudaVersionStr(12080))
	assert.Equal(t, "0.0", cudaVersionStr(0))
}
