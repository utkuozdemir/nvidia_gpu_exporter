// Package nvmlnative implements the experimental NVML collection backend:
// GPU metrics read directly from libnvidia-ml instead of exec-ing nvidia-smi.
// The catalog below is the backend's field vocabulary: every query field it
// can serve, with the exact returned-header string nvidia-smi prints for it.
// Metric names derive from these header strings, so they must match
// nvidia-smi byte-for-byte; they were verified against a live H100 (driver
// 590.48.01) via the diff harness in _work/nvml-experiment.
//
// Fields nvidia-smi knows but this catalog does not (vGPU capabilities,
// Blackwell power smoothing, GB200 module power, kmd_version...) are simply
// absent from the backend's vocabulary, the same way an older nvidia-smi
// does not know newer fields. On hardware where they exist they read [N/A]
// in exec mode anyway, so the exported metric surface stays the same.
package nvmlnative

import (
	"fmt"
	"log/slog"
	"maps"
	"strconv"
	"strings"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

// fieldOrder is the catalog's canonical field order (the order nvidia-smi
// --help-query-gpu lists them in).
//
//nolint:goconst // field names repeat across the catalog tables by design
var fieldOrder = []nvidiasmi.QField{
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
	"pci.baseClass",
	"pci.subClass",
	"pci.device_id",
	"pci.sub_device_id",
	"pcie.link.gen.current",
	"pcie.link.gen.gpucurrent",
	"pcie.link.gen.max",
	"pcie.link.gen.gpumax",
	"pcie.link.width.current",
	"pcie.link.width.max",
	"index",
	"display_mode",
	"display_attached",
	"display_active",
	"persistence_mode",
	"addressing_mode",
	"accounting.mode",
	"accounting.buffer_size",
	"driver_model.current",
	"driver_model.pending",
	"vbios_version",
	"inforom.img",
	"inforom.oem",
	"inforom.ecc",
	"inforom.pwr",
	"inforom.checksum_validation",
	"gpu_recovery_action",
	"gom.current",
	"gom.pending",
	"fan.speed",
	"pstate",
	"clocks_event_reasons.supported",
	"clocks_event_reasons.active",
	"clocks_event_reasons.gpu_idle",
	"clocks_event_reasons.applications_clocks_setting",
	"clocks_event_reasons.sw_power_cap",
	"clocks_event_reasons.hw_slowdown",
	"clocks_event_reasons.hw_thermal_slowdown",
	"clocks_event_reasons.hw_power_brake_slowdown",
	"clocks_event_reasons.sw_thermal_slowdown",
	"clocks_event_reasons.sync_boost",
	"clocks_event_reasons_counters.sw_power_cap",
	"clocks_event_reasons_counters.sync_boost",
	"clocks_event_reasons_counters.sw_thermal_slowdown",
	"clocks_event_reasons_counters.hw_thermal_slowdown",
	"clocks_event_reasons_counters.hw_power_brake_slowdown",
	"memory.total",
	"memory.reserved",
	"memory.used",
	"memory.free",
	"compute_mode",
	"compute_cap",
	"utilization.gpu",
	"utilization.memory",
	"utilization.encoder",
	"utilization.decoder",
	"utilization.jpeg",
	"utilization.ofa",
	"encoder.stats.sessionCount",
	"encoder.stats.averageFps",
	"encoder.stats.averageLatency",
	"dramEncryption.mode.current",
	"dramEncryption.mode.pending",
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
	"ecc.errors.uncorrected.volatile.sram.parity",
	"ecc.errors.uncorrected.volatile.sram.secded",
	"ecc.errors.uncorrected.aggregate.sram.parity",
	"ecc.errors.uncorrected.aggregate.sram.secded",
	"ecc.errors.uncorrected.aggregate.sram.thresholdExceeded",
	"ecc.errors.uncorrected.aggregate.sram.l2",
	"ecc.errors.uncorrected.aggregate.sram.sm",
	"ecc.errors.uncorrected.aggregate.sram.mcu",
	"ecc.errors.uncorrected.aggregate.sram.pcie",
	"ecc.errors.uncorrected.aggregate.sram.other",
	"retired_pages.single_bit_ecc.count",
	"retired_pages.double_bit.count",
	"retired_pages.pending",
	"remapped_rows.correctable",
	"remapped_rows.uncorrectable",
	"remapped_rows.pending",
	"remapped_rows.failure",
	"remapped_rows.histogram.max",
	"remapped_rows.histogram.high",
	"remapped_rows.histogram.partial",
	"remapped_rows.histogram.low",
	"remapped_rows.histogram.none",
	"temperature.gpu",
	"temperature.gpu.tlimit",
	"temperature.memory",
	"power.management",
	"power.draw",
	"power.draw.average",
	"power.draw.instant",
	"power.limit",
	"enforced.power.limit",
	"power.default_limit",
	"power.min_limit",
	"power.max_limit",
	"edpp_multiplier",
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
	"gsp.mode.current",
	"gsp.mode.default",
	"c2c.mode",
	"protected_memory.total",
	"protected_memory.used",
	"protected_memory.free",
	"fabric.state",
	"fabric.status",
	"fabric.cliqueId",
	"fabric.clusterUuid",
	"platform.chassis_serial_number",
	"platform.slot_number",
	"platform.tray_index",
	"platform.host_id",
	"platform.peer_type",
	"platform.module_id",
	"platform.gpu_fabric_guid",
	"hostname",
}

// catalogRFields maps every catalogued query field to the returned-header
// string nvidia-smi prints for it, which the exporter turns into the metric
// name and unit multiplier.
//
//nolint:gosec // G101: false positive, these are nvidia-smi field names, not credentials
var catalogRFields = map[nvidiasmi.QField]nvidiasmi.RField{
	"timestamp":                      "timestamp",
	"driver_version":                 "driver_version",
	"count":                          "count",
	"name":                           "name",
	"serial":                         "serial",
	"uuid":                           "uuid",
	"pci.bus_id":                     "pci.bus_id",
	"pci.domain":                     "pci.domain",
	"pci.bus":                        "pci.bus",
	"pci.device":                     "pci.device",
	"pci.device_id":                  "pci.device_id",
	"pci.sub_device_id":              "pci.sub_device_id",
	"pcie.link.gen.current":          "pcie.link.gen.current",
	"pcie.link.gen.gpucurrent":       "pcie.link.gen.gpucurrent",
	"pcie.link.gen.max":              "pcie.link.gen.max",
	"pcie.link.gen.gpumax":           "pcie.link.gen.gpumax",
	"pcie.link.width.current":        "pcie.link.width.current",
	"pcie.link.width.max":            "pcie.link.width.max",
	"index":                          "index",
	"display_mode":                   "display_mode",
	"display_attached":               "display_attached",
	"display_active":                 "display_active",
	"persistence_mode":               "persistence_mode",
	"addressing_mode":                "addressing_mode",
	"accounting.mode":                "accounting.mode",
	"accounting.buffer_size":         "accounting.buffer_size",
	"driver_model.current":           "driver_model.current",
	"driver_model.pending":           "driver_model.pending",
	"vbios_version":                  "vbios_version",
	"inforom.img":                    "inforom.img",
	"inforom.oem":                    "inforom.oem",
	"inforom.ecc":                    "inforom.ecc",
	"inforom.pwr":                    "inforom.pwr",
	"inforom.checksum_validation":    "inforom.checksum_validation",
	"gpu_recovery_action":            "gpu_recovery_action",
	"gom.current":                    "gom.current",
	"gom.pending":                    "gom.pending",
	"fan.speed":                      "fan.speed [%]",
	"pstate":                         "pstate",
	"clocks_event_reasons.supported": "clocks_event_reasons.supported",
	"clocks_event_reasons.active":    "clocks_event_reasons.active",
	"clocks_event_reasons.gpu_idle":  "clocks_event_reasons.gpu_idle",
	"clocks_event_reasons.applications_clocks_setting":      "clocks_event_reasons.applications_clocks_setting",
	"clocks_event_reasons.sw_power_cap":                     "clocks_event_reasons.sw_power_cap",
	"clocks_event_reasons.hw_slowdown":                      "clocks_event_reasons.hw_slowdown",
	"clocks_event_reasons.hw_thermal_slowdown":              "clocks_event_reasons.hw_thermal_slowdown",
	"clocks_event_reasons.hw_power_brake_slowdown":          "clocks_event_reasons.hw_power_brake_slowdown",
	"clocks_event_reasons.sw_thermal_slowdown":              "clocks_event_reasons.sw_thermal_slowdown",
	"clocks_event_reasons.sync_boost":                       "clocks_event_reasons.sync_boost",
	"clocks_event_reasons_counters.sw_power_cap":            "clocks_event_reasons_counters.sw_power_cap [us]",
	"clocks_event_reasons_counters.sync_boost":              "clocks_event_reasons_counters.sync_boost [us]",
	"clocks_event_reasons_counters.sw_thermal_slowdown":     "clocks_event_reasons_counters.sw_thermal_slowdown [us]",
	"clocks_event_reasons_counters.hw_thermal_slowdown":     "clocks_event_reasons_counters.hw_thermal_slowdown [us]",
	"clocks_event_reasons_counters.hw_power_brake_slowdown": "clocks_event_reasons_counters.hw_power_brake_slowdown [us]",
	"memory.total":                                            "memory.total [MiB]",
	"memory.reserved":                                         "memory.reserved [MiB]",
	"memory.used":                                             "memory.used [MiB]",
	"memory.free":                                             "memory.free [MiB]",
	"compute_mode":                                            "compute_mode",
	"compute_cap":                                             "compute_cap",
	"utilization.gpu":                                         "utilization.gpu [%]",
	"utilization.memory":                                      "utilization.memory [%]",
	"utilization.encoder":                                     "utilization.encoder [%]",
	"utilization.decoder":                                     "utilization.decoder [%]",
	"utilization.jpeg":                                        "utilization.jpeg [%]",
	"utilization.ofa":                                         "utilization.ofa [%]",
	"encoder.stats.sessionCount":                              "encoder.stats.sessionCount",
	"encoder.stats.averageFps":                                "encoder.stats.averageFps",
	"encoder.stats.averageLatency":                            "encoder.stats.averageLatency",
	"dramEncryption.mode.current":                             "dramEncryption.mode.current",
	"dramEncryption.mode.pending":                             "dramEncryption.mode.pending",
	"ecc.mode.current":                                        "ecc.mode.current",
	"ecc.mode.pending":                                        "ecc.mode.pending",
	"ecc.errors.corrected.volatile.device_memory":             "ecc.errors.corrected.volatile.device_memory",
	"ecc.errors.corrected.volatile.dram":                      "ecc.errors.corrected.volatile.dram",
	"ecc.errors.corrected.volatile.register_file":             "ecc.errors.corrected.volatile.register_file",
	"ecc.errors.corrected.volatile.l1_cache":                  "ecc.errors.corrected.volatile.l1_cache",
	"ecc.errors.corrected.volatile.l2_cache":                  "ecc.errors.corrected.volatile.l2_cache",
	"ecc.errors.corrected.volatile.texture_memory":            "ecc.errors.corrected.volatile.texture_memory",
	"ecc.errors.corrected.volatile.cbu":                       "ecc.errors.corrected.volatile.cbu",
	"ecc.errors.corrected.volatile.sram":                      "ecc.errors.corrected.volatile.sram",
	"ecc.errors.corrected.volatile.total":                     "ecc.errors.corrected.volatile.total",
	"ecc.errors.corrected.aggregate.device_memory":            "ecc.errors.corrected.aggregate.device_memory",
	"ecc.errors.corrected.aggregate.dram":                     "ecc.errors.corrected.aggregate.dram",
	"ecc.errors.corrected.aggregate.register_file":            "ecc.errors.corrected.aggregate.register_file",
	"ecc.errors.corrected.aggregate.l1_cache":                 "ecc.errors.corrected.aggregate.l1_cache",
	"ecc.errors.corrected.aggregate.l2_cache":                 "ecc.errors.corrected.aggregate.l2_cache",
	"ecc.errors.corrected.aggregate.texture_memory":           "ecc.errors.corrected.aggregate.texture_memory",
	"ecc.errors.corrected.aggregate.cbu":                      "ecc.errors.corrected.aggregate.cbu",
	"ecc.errors.corrected.aggregate.sram":                     "ecc.errors.corrected.aggregate.sram",
	"ecc.errors.corrected.aggregate.total":                    "ecc.errors.corrected.aggregate.total",
	"ecc.errors.uncorrected.volatile.device_memory":           "ecc.errors.uncorrected.volatile.device_memory",
	"ecc.errors.uncorrected.volatile.dram":                    "ecc.errors.uncorrected.volatile.dram",
	"ecc.errors.uncorrected.volatile.register_file":           "ecc.errors.uncorrected.volatile.register_file",
	"ecc.errors.uncorrected.volatile.l1_cache":                "ecc.errors.uncorrected.volatile.l1_cache",
	"ecc.errors.uncorrected.volatile.l2_cache":                "ecc.errors.uncorrected.volatile.l2_cache",
	"ecc.errors.uncorrected.volatile.texture_memory":          "ecc.errors.uncorrected.volatile.texture_memory",
	"ecc.errors.uncorrected.volatile.cbu":                     "ecc.errors.uncorrected.volatile.cbu",
	"ecc.errors.uncorrected.volatile.sram":                    "ecc.errors.uncorrected.volatile.sram",
	"ecc.errors.uncorrected.volatile.total":                   "ecc.errors.uncorrected.volatile.total",
	"ecc.errors.uncorrected.aggregate.device_memory":          "ecc.errors.uncorrected.aggregate.device_memory",
	"ecc.errors.uncorrected.aggregate.dram":                   "ecc.errors.uncorrected.aggregate.dram",
	"ecc.errors.uncorrected.aggregate.register_file":          "ecc.errors.uncorrected.aggregate.register_file",
	"ecc.errors.uncorrected.aggregate.l1_cache":               "ecc.errors.uncorrected.aggregate.l1_cache",
	"ecc.errors.uncorrected.aggregate.l2_cache":               "ecc.errors.uncorrected.aggregate.l2_cache",
	"ecc.errors.uncorrected.aggregate.texture_memory":         "ecc.errors.uncorrected.aggregate.texture_memory",
	"ecc.errors.uncorrected.aggregate.cbu":                    "ecc.errors.uncorrected.aggregate.cbu",
	"ecc.errors.uncorrected.aggregate.sram":                   "ecc.errors.uncorrected.aggregate.sram",
	"ecc.errors.uncorrected.aggregate.total":                  "ecc.errors.uncorrected.aggregate.total",
	"ecc.errors.uncorrected.volatile.sram.parity":             "ecc.errors.uncorrected.volatile.sram.parity",
	"ecc.errors.uncorrected.volatile.sram.secded":             "ecc.errors.uncorrected.volatile.sram.secded",
	"ecc.errors.uncorrected.aggregate.sram.parity":            "ecc.errors.uncorrected.aggregate.sram.parity",
	"ecc.errors.uncorrected.aggregate.sram.secded":            "ecc.errors.uncorrected.aggregate.sram.secded",
	"ecc.errors.uncorrected.aggregate.sram.thresholdExceeded": "ecc.errors.uncorrected.aggregate.sram.thresholdExceeded",
	"ecc.errors.uncorrected.aggregate.sram.l2":                "ecc.errors.uncorrected.aggregate.sram.l2",
	"ecc.errors.uncorrected.aggregate.sram.sm":                "ecc.errors.uncorrected.aggregate.sram.sm",
	"ecc.errors.uncorrected.aggregate.sram.mcu":               "ecc.errors.uncorrected.aggregate.sram.mcu",
	"ecc.errors.uncorrected.aggregate.sram.pcie":              "ecc.errors.uncorrected.aggregate.sram.pcie",
	"ecc.errors.uncorrected.aggregate.sram.other":             "ecc.errors.uncorrected.aggregate.sram.other",
	"retired_pages.single_bit_ecc.count":                      "retired_pages.single_bit_ecc.count",
	"retired_pages.double_bit.count":                          "retired_pages.double_bit.count",
	"retired_pages.pending":                                   "retired_pages.pending",
	"remapped_rows.correctable":                               "remapped_rows.correctable",
	"remapped_rows.uncorrectable":                             "remapped_rows.uncorrectable",
	"remapped_rows.pending":                                   "remapped_rows.pending",
	"remapped_rows.failure":                                   "remapped_rows.failure",
	"remapped_rows.histogram.max":                             "remapped_rows.histogram.max",
	"remapped_rows.histogram.high":                            "remapped_rows.histogram.high",
	"remapped_rows.histogram.partial":                         "remapped_rows.histogram.partial",
	"remapped_rows.histogram.low":                             "remapped_rows.histogram.low",
	"remapped_rows.histogram.none":                            "remapped_rows.histogram.none",
	"temperature.gpu":                                         "temperature.gpu",
	"temperature.gpu.tlimit":                                  "temperature.gpu.tlimit",
	"temperature.memory":                                      "temperature.memory",
	"power.management":                                        "power.management",
	"power.draw":                                              "power.draw [W]",
	"power.draw.average":                                      "power.draw.average [W]",
	"power.draw.instant":                                      "power.draw.instant [W]",
	"power.limit":                                             "power.limit [W]",
	"enforced.power.limit":                                    "enforced.power.limit [W]",
	"power.default_limit":                                     "power.default_limit [W]",
	"power.min_limit":                                         "power.min_limit [W]",
	"power.max_limit":                                         "power.max_limit [W]",
	"edpp_multiplier":                                         "edpp_multiplier [%]",
	"clocks.current.graphics":                                 "clocks.current.graphics [MHz]",
	"clocks.current.sm":                                       "clocks.current.sm [MHz]",
	"clocks.current.memory":                                   "clocks.current.memory [MHz]",
	"clocks.current.video":                                    "clocks.current.video [MHz]",
	"clocks.applications.graphics":                            "clocks.applications.graphics [MHz]",
	"clocks.applications.memory":                              "clocks.applications.memory [MHz]",
	"clocks.default_applications.graphics":                    "clocks.default_applications.graphics [MHz]",
	"clocks.default_applications.memory":                      "clocks.default_applications.memory [MHz]",
	"clocks.max.graphics":                                     "clocks.max.graphics [MHz]",
	"clocks.max.sm":                                           "clocks.max.sm [MHz]",
	"clocks.max.memory":                                       "clocks.max.memory [MHz]",
	"mig.mode.current":                                        "mig.mode.current",
	"mig.mode.pending":                                        "mig.mode.pending",
	"gsp.mode.current":                                        "gsp.mode.current",
	"gsp.mode.default":                                        "gsp.mode.default",
	"c2c.mode":                                                "c2c.mode",
	"protected_memory.total":                                  "protected_memory.total [MiB]",
	"protected_memory.used":                                   "protected_memory.used [MiB]",
	"protected_memory.free":                                   "protected_memory.free [MiB]",
	"fabric.state":                                            "fabric.state",
	"fabric.status":                                           "fabric.status",
	"fabric.cliqueId":                                         "fabric.cliqueId",
	"fabric.clusterUuid":                                      "fabric.clusterUuid",
	"platform.chassis_serial_number":                          "platform.chassis_serial_number",
	"platform.slot_number":                                    "platform.slot_number",
	"platform.tray_index":                                     "platform.tray_index",
	"platform.host_id":                                        "platform.host_id",
	"platform.peer_type":                                      "platform.peer_type",
	"platform.module_id":                                      "platform.module_id",
	"platform.gpu_fabric_guid":                                "platform.gpu_fabric_guid",
	"hostname":                                                "hostname",
	"pci.baseClass":                                           "pci.baseClass",
	"pci.subClass":                                            "pci.subClass",
}

// throttleAliases maps the legacy clocks_throttle_reasons.* spellings to the
// canonical clocks_event_reasons.* fields. Drivers older than the rename
// advertise and print only the throttle spelling, so exec AUTO emits those
// metric names there; native AUTO must pick the same spelling per driver or
// the two backends would name the same data differently.
var throttleAliases = map[nvidiasmi.QField]nvidiasmi.QField{
	"clocks_throttle_reasons.supported":                   "clocks_event_reasons.supported",
	"clocks_throttle_reasons.active":                      "clocks_event_reasons.active",
	"clocks_throttle_reasons.gpu_idle":                    "clocks_event_reasons.gpu_idle",
	"clocks_throttle_reasons.applications_clocks_setting": "clocks_event_reasons.applications_clocks_setting",
	"clocks_throttle_reasons.sw_power_cap":                "clocks_event_reasons.sw_power_cap",
	"clocks_throttle_reasons.hw_slowdown":                 "clocks_event_reasons.hw_slowdown",
	"clocks_throttle_reasons.hw_thermal_slowdown":         "clocks_event_reasons.hw_thermal_slowdown",
	"clocks_throttle_reasons.hw_power_brake_slowdown":     "clocks_event_reasons.hw_power_brake_slowdown",
	"clocks_throttle_reasons.sw_thermal_slowdown":         "clocks_event_reasons.sw_thermal_slowdown",
	"clocks_throttle_reasons.sync_boost":                  "clocks_event_reasons.sync_boost",
}

// eventReasonsMinDriverMajor is the first driver branch whose nvidia-smi
// advertises the clocks_event_reasons.* spelling first. The captures corpus
// only proves >= 590 uses the event spelling; the r555 boundary is an
// UNVERIFIED estimate, so on branches between the real rename and 590 the
// two backends may spell these ten metric names differently. Pin the
// boundary from a pre-590 capture when one becomes available (tracked in
// the design doc's known limits).
const eventReasonsMinDriverMajor = 555

// canonicalQField translates an alias to the field the collector produces.
func canonicalQField(q nvidiasmi.QField) nvidiasmi.QField {
	if canonical, ok := throttleAliases[q]; ok {
		return canonical
	}

	return q
}

// driverUsesEventReasons reports whether the driver's nvidia-smi uses the
// clocks_event_reasons.* spelling. Unparseable versions count as modern.
func driverUsesEventReasons(driverVersion string) bool {
	major, _, _ := strings.Cut(driverVersion, ".")

	value, err := strconv.Atoi(major)
	if err != nil {
		return true
	}

	return value >= eventReasonsMinDriverMajor
}

// Resolve resolves the query fields against the backend catalog. AUTO means
// the full catalog, with the clock-reasons family spelled the way the
// installed driver's nvidia-smi would spell it; explicit lists accept both
// spellings and fail on fields the catalog does not know.
func Resolve(
	qFieldsRaw, qFieldsExcludeRaw, driverVersion string,
	logger *slog.Logger,
) (nvidiasmi.ResolvedFields, error) {
	vocabulary := make(map[nvidiasmi.QField]nvidiasmi.RField, len(catalogRFields)+len(throttleAliases))
	maps.Copy(vocabulary, catalogRFields)

	for alias := range throttleAliases {
		// legacy headers are the plain field name, no unit suffix
		vocabulary[alias] = nvidiasmi.RField(alias)
	}

	order := fieldOrder
	if !driverUsesEventReasons(driverVersion) {
		order = make([]nvidiasmi.QField, len(fieldOrder))

		aliasFor := make(map[nvidiasmi.QField]nvidiasmi.QField, len(throttleAliases))
		for alias, canonical := range throttleAliases {
			aliasFor[canonical] = alias
		}

		for i, q := range fieldOrder {
			if alias, ok := aliasFor[q]; ok {
				order[i] = alias
			} else {
				order[i] = q
			}
		}
	}

	resolved, err := nvidiasmi.ResolveFromCatalog(
		order, vocabulary, qFieldsRaw, qFieldsExcludeRaw, logger)
	if err != nil {
		return nvidiasmi.ResolvedFields{}, fmt.Errorf("failed to resolve against the nvml catalog: %w", err)
	}

	return resolved, nil
}

// deprecatedFields are the fields nvidia-smi answers with its deprecation
// token regardless of what NVML returns (H100/590 verified; the collector
// prints the token for these without calling the getter). The corpus drift
// test checks this list against every capture's data row, so a driver
// un-deprecating or newly deprecating a field fails loudly.
var deprecatedFields = []nvidiasmi.QField{
	"display_mode",
	"power.management",
	"clocks.applications.graphics",
	"clocks.applications.memory",
	"clocks.default_applications.graphics",
	"clocks.default_applications.memory",
}

// deferredFields are query fields nvidia-smi advertises that this backend
// consciously does not serve yet: no verified NVML mapping exists, or the
// family needs hardware nobody has captured. On the hardware captured so far
// they all read as absent, so both backends export nothing for them. The
// corpus drift test fails when a NEW unknown field shows a real value in the
// newest capture and it is not listed here; adding a field here is the
// explicit "defer" decision, adding it to the catalog is the fix.
var deferredFields = map[nvidiasmi.QField]bool{
	"kmd_version":                          true,
	"pcie.link.gen.hostmax":                true,
	"remapped_rows.correctable_inactive":   true,
	"remapped_rows.uncorrectable_inactive": true,
}

// deferredFieldPrefixes covers whole deferred families (vGPU capabilities,
// Blackwell power smoothing, GB200 module power, fabric health...).
var deferredFieldPrefixes = []string{
	"vgpu_", "module.power.", "module.enforced.", "gpu.base.", "power_smoothing.",
	"bbx.", "fabric.health.",
}

// isDeferredField reports whether an unknown field is a recorded, conscious
// deferral rather than new drift.
func isDeferredField(qField nvidiasmi.QField) bool {
	if deferredFields[qField] {
		return true
	}

	for _, prefix := range deferredFieldPrefixes {
		if strings.HasPrefix(string(qField), prefix) {
			return true
		}
	}

	return false
}
