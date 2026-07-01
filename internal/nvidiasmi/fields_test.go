package nvidiasmi_test

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

var (
	//go:embed testdata/fields.txt
	fieldsTest string

	expectedQFields = []nvidiasmi.QField{
		"timestamp",
		"driver_version",
		"count",
		"name",
		"serial",
		"uuid",
		"pci.bus_id",
		"pci.domain",
		"pci.bus",
		"pci.device",
		"pci.device_id",
		"pci.sub_device_id",
		"pcie.link.gen.current",
		"pcie.link.gen.max",
		"pcie.link.width.current",
		"pcie.link.width.max",
		"index",
		"display_mode",
		"display_active",
		"persistence_mode",
		"accounting.mode",
		"accounting.buffer_size",
		"driver_model.current",
		"driver_model.pending",
		"vbios_version",
		"inforom.img",
		"inforom.oem",
		"inforom.ecc",
		"inforom.pwr",
		"gom.current",
		"gom.pending",
		"fan.speed",
		"pstate",
		"clocks_throttle_reasons.supported",
		"clocks_throttle_reasons.active",
		"clocks_throttle_reasons.gpu_idle",
		"clocks_throttle_reasons.applications_clocks_setting",
		"clocks_throttle_reasons.sw_power_cap",
		"clocks_throttle_reasons.hw_slowdown",
		"clocks_throttle_reasons.hw_thermal_slowdown",
		"clocks_throttle_reasons.hw_power_brake_slowdown",
		"clocks_throttle_reasons.sw_thermal_slowdown",
		"clocks_throttle_reasons.sync_boost",
		"memory.total",
		"memory.used",
		"memory.free",
		"compute_mode",
		"utilization.gpu",
		"utilization.memory",
		"encoder.stats.sessionCount",
		"encoder.stats.averageFps",
		"encoder.stats.averageLatency",
		"ecc.mode.current",
		"ecc.mode.pending",
		"ecc.errors.corrected.volatile.device_memory",
		"ecc.errors.corrected.volatile.dram",
		"ecc.errors.corrected.volatile.register_file",
		"ecc.errors.corrected.volatile.l1_cache",
		"ecc.errors.corrected.volatile.l2_cache",
		"ecc.errors.corrected.volatile.texture_memory",
		"ecc.errors.corrected.volatile.cbu",
		"ecc.errors.corrected.volatile.sram",
		"ecc.errors.corrected.volatile.total",
		"ecc.errors.corrected.aggregate.device_memory",
		"ecc.errors.corrected.aggregate.dram",
		"ecc.errors.corrected.aggregate.register_file",
		"ecc.errors.corrected.aggregate.l1_cache",
		"ecc.errors.corrected.aggregate.l2_cache",
		"ecc.errors.corrected.aggregate.texture_memory",
		"ecc.errors.corrected.aggregate.cbu",
		"ecc.errors.corrected.aggregate.sram",
		"ecc.errors.corrected.aggregate.total",
		"ecc.errors.uncorrected.volatile.device_memory",
		"ecc.errors.uncorrected.volatile.dram",
		"ecc.errors.uncorrected.volatile.register_file",
		"ecc.errors.uncorrected.volatile.l1_cache",
		"ecc.errors.uncorrected.volatile.l2_cache",
		"ecc.errors.uncorrected.volatile.texture_memory",
		"ecc.errors.uncorrected.volatile.cbu",
		"ecc.errors.uncorrected.volatile.sram",
		"ecc.errors.uncorrected.volatile.total",
		"ecc.errors.uncorrected.aggregate.device_memory",
		"ecc.errors.uncorrected.aggregate.dram",
		"ecc.errors.uncorrected.aggregate.register_file",
		"ecc.errors.uncorrected.aggregate.l1_cache",
		"ecc.errors.uncorrected.aggregate.l2_cache",
		"ecc.errors.uncorrected.aggregate.texture_memory",
		"ecc.errors.uncorrected.aggregate.cbu",
		"ecc.errors.uncorrected.aggregate.sram",
		"ecc.errors.uncorrected.aggregate.total",
		"retired_pages.single_bit_ecc.count",
		"retired_pages.double_bit.count",
		"retired_pages.pending",
		"temperature.gpu",
		"temperature.memory",
		"power.management",
		"power.draw",
		"power.limit",
		"enforced.power.limit",
		"power.default_limit",
		"power.min_limit",
		"power.max_limit",
		"clocks.current.graphics",
		"clocks.current.sm",
		"clocks.current.memory",
		"clocks.current.video",
		"clocks.applications.graphics",
		"clocks.applications.memory",
		"clocks.default_applications.graphics",
		"clocks.default_applications.memory",
		"clocks.max.graphics",
		"clocks.max.sm",
		"clocks.max.memory",
		"mig.mode.current",
		"mig.mode.pending",
	}
)

func TestExtractQFields(t *testing.T) {
	t.Parallel()

	fields := nvidiasmi.ExtractQFields(fieldsTest)

	assert.Equal(t, expectedQFields, fields)
}

func TestParseAutoQFields(t *testing.T) {
	t.Parallel()

	var capturedCmd *exec.Cmd

	command := func(cmd *exec.Cmd) error {
		capturedCmd = cmd
		_, _ = cmd.Stdout.Write([]byte(fieldsTest))

		return nil
	}

	fields, err := nvidiasmi.ParseAutoQFields(t.Context(), "nvidia-smi", command)

	if assert.Len(t, capturedCmd.Args, 2) {
		assert.Equal(t, "nvidia-smi", capturedCmd.Args[0])
		assert.Equal(t, "--help-query-gpu", capturedCmd.Args[1])
	}

	require.NoError(t, err)
	assert.Equal(t, expectedQFields, fields)
}

// TestParseQueryFields must be run manually.
//
//nolint:forbidigo
func TestParseQueryFields(t *testing.T) {
	t.SkipNow()
	t.Parallel()

	nvidiaSmiCommand := "nvidia-smi"

	qFields, err := nvidiasmi.ParseAutoQFields(t.Context(), nvidiaSmiCommand, nvidiasmi.DefaultRunFunc)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}

	fields := nvidiasmi.QFieldSliceToStringSlice(qFields)

	fmt.Printf("Fields:\n\n%s\n", strings.Join(fields, "\n"))
}
