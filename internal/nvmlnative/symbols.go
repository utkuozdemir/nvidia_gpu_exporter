package nvmlnative

// symbolRequirement declares the exported driver-library symbols one Go call
// site can be served by: any one of the alternatives suffices, which covers
// NVML's versioned entry points. The drift test checks these against the
// nvml-symbols inventory recorded in the captures; a companion test parses
// the collector source so a new call site cannot be added without an entry
// here.
type symbolRequirement struct {
	goCall string
	anyOf  []string
	serves string
}

//nolint:gochecknoglobals,goconst // shared requirement manifest; field names repeat by design
var nvmlSymbolRequirements = []symbolRequirement{
	{goCall: "init", anyOf: []string{"nvmlInit_v2", "nvmlInit"}, serves: "NVML initialization"},
	{goCall: "shutdown", anyOf: []string{"nvmlShutdown"}, serves: "NVML shutdown"},
	{
		goCall: "deviceCount",
		anyOf:  []string{"nvmlDeviceGetCount_v2", "nvmlDeviceGetCount"},
		serves: "device enumeration",
	},
	{
		goCall: "deviceByIndex",
		anyOf:  []string{"nvmlDeviceGetHandleByIndex_v2", "nvmlDeviceGetHandleByIndex"},
		serves: "device enumeration",
	},
	{goCall: "driverVersion", anyOf: []string{"nvmlSystemGetDriverVersion"}, serves: "driver_version"},
	{goCall: "processName", anyOf: []string{"nvmlSystemGetProcessName"}, serves: "per-process metrics"},
	{goCall: "validateInforom", anyOf: []string{"nvmlDeviceValidateInforom"}, serves: "inforom.checksum_validation"},
	{
		goCall: "GetName",
		anyOf:  []string{"nvmlDeviceGetName", "nvmlDeviceGetName_v2", "nvmlDeviceGetName_v3"},
		serves: "name",
	},
	{
		goCall: "GetSerial",
		anyOf:  []string{"nvmlDeviceGetSerial", "nvmlDeviceGetSerial_v2", "nvmlDeviceGetSerial_v3"},
		serves: "serial",
	},
	{
		goCall: "GetUUID",
		anyOf:  []string{"nvmlDeviceGetUUID", "nvmlDeviceGetUUID_v2", "nvmlDeviceGetUUID_v3"},
		serves: "uuid",
	},
	{
		goCall: "GetIndex",
		anyOf:  []string{"nvmlDeviceGetIndex", "nvmlDeviceGetIndex_v2", "nvmlDeviceGetIndex_v3"},
		serves: "index",
	},
	{
		goCall: "GetPciInfoExt",
		anyOf:  []string{"nvmlDeviceGetPciInfoExt", "nvmlDeviceGetPciInfoExt_v2", "nvmlDeviceGetPciInfoExt_v3"},
		serves: "pci.*",
	},
	{
		goCall: "GetCurrPcieLinkGeneration",
		anyOf: []string{
			"nvmlDeviceGetCurrPcieLinkGeneration",
			"nvmlDeviceGetCurrPcieLinkGeneration_v2",
			"nvmlDeviceGetCurrPcieLinkGeneration_v3",
		},
		serves: "pcie.link.gen.current/gpucurrent",
	},
	{
		goCall: "GetMaxPcieLinkGeneration",
		anyOf: []string{
			"nvmlDeviceGetMaxPcieLinkGeneration",
			"nvmlDeviceGetMaxPcieLinkGeneration_v2",
			"nvmlDeviceGetMaxPcieLinkGeneration_v3",
		},
		serves: "pcie.link.gen.max",
	},
	{
		goCall: "GetGpuMaxPcieLinkGeneration",
		anyOf: []string{
			"nvmlDeviceGetGpuMaxPcieLinkGeneration",
			"nvmlDeviceGetGpuMaxPcieLinkGeneration_v2",
			"nvmlDeviceGetGpuMaxPcieLinkGeneration_v3",
		},
		serves: "pcie.link.gen.gpumax",
	},
	{
		goCall: "GetCurrPcieLinkWidth",
		anyOf: []string{
			"nvmlDeviceGetCurrPcieLinkWidth",
			"nvmlDeviceGetCurrPcieLinkWidth_v2",
			"nvmlDeviceGetCurrPcieLinkWidth_v3",
		},
		serves: "pcie.link.width.current",
	},
	{
		goCall: "GetMaxPcieLinkWidth",
		anyOf: []string{
			"nvmlDeviceGetMaxPcieLinkWidth",
			"nvmlDeviceGetMaxPcieLinkWidth_v2",
			"nvmlDeviceGetMaxPcieLinkWidth_v3",
		},
		serves: "pcie.link.width.max",
	},
	{
		goCall: "GetDisplayMode",
		anyOf:  []string{"nvmlDeviceGetDisplayMode", "nvmlDeviceGetDisplayMode_v2", "nvmlDeviceGetDisplayMode_v3"},
		serves: "display_attached",
	},
	{
		goCall: "GetDisplayActive",
		anyOf: []string{
			"nvmlDeviceGetDisplayActive",
			"nvmlDeviceGetDisplayActive_v2",
			"nvmlDeviceGetDisplayActive_v3",
		},
		serves: "display_active",
	},
	{
		goCall: "GetPersistenceMode",
		anyOf: []string{
			"nvmlDeviceGetPersistenceMode",
			"nvmlDeviceGetPersistenceMode_v2",
			"nvmlDeviceGetPersistenceMode_v3",
		},
		serves: "persistence_mode",
	},
	{
		goCall: "GetAddressingMode",
		anyOf: []string{
			"nvmlDeviceGetAddressingMode",
			"nvmlDeviceGetAddressingMode_v2",
			"nvmlDeviceGetAddressingMode_v3",
		},
		serves: "addressing_mode",
	},
	{
		goCall: "GetAccountingMode",
		anyOf: []string{
			"nvmlDeviceGetAccountingMode",
			"nvmlDeviceGetAccountingMode_v2",
			"nvmlDeviceGetAccountingMode_v3",
		},
		serves: "accounting.mode",
	},
	{
		goCall: "GetAccountingBufferSize",
		anyOf: []string{
			"nvmlDeviceGetAccountingBufferSize",
			"nvmlDeviceGetAccountingBufferSize_v2",
			"nvmlDeviceGetAccountingBufferSize_v3",
		},
		serves: "accounting.buffer_size",
	},
	{
		goCall: "GetDriverModel",
		anyOf:  []string{"nvmlDeviceGetDriverModel", "nvmlDeviceGetDriverModel_v2", "nvmlDeviceGetDriverModel_v3"},
		serves: "driver_model.*",
	},
	{
		goCall: "GetVbiosVersion",
		anyOf:  []string{"nvmlDeviceGetVbiosVersion", "nvmlDeviceGetVbiosVersion_v2", "nvmlDeviceGetVbiosVersion_v3"},
		serves: "vbios_version",
	},
	{
		goCall: "GetInforomImageVersion",
		anyOf: []string{
			"nvmlDeviceGetInforomImageVersion",
			"nvmlDeviceGetInforomImageVersion_v2",
			"nvmlDeviceGetInforomImageVersion_v3",
		},
		serves: "inforom.img",
	},
	{
		goCall: "GetInforomVersion",
		anyOf: []string{
			"nvmlDeviceGetInforomVersion",
			"nvmlDeviceGetInforomVersion_v2",
			"nvmlDeviceGetInforomVersion_v3",
		},
		serves: "inforom.oem/ecc/pwr",
	},
	{
		goCall: "GetGpuOperationMode",
		anyOf: []string{
			"nvmlDeviceGetGpuOperationMode",
			"nvmlDeviceGetGpuOperationMode_v2",
			"nvmlDeviceGetGpuOperationMode_v3",
		},
		serves: "gom.*",
	},
	{
		goCall: "GetFanSpeed",
		anyOf:  []string{"nvmlDeviceGetFanSpeed", "nvmlDeviceGetFanSpeed_v2", "nvmlDeviceGetFanSpeed_v3"},
		serves: "fan.speed",
	},
	{
		goCall: "GetPerformanceState",
		anyOf: []string{
			"nvmlDeviceGetPerformanceState",
			"nvmlDeviceGetPerformanceState_v2",
			"nvmlDeviceGetPerformanceState_v3",
		},
		serves: "pstate",
	},
	{
		goCall: "GetSupportedClocksEventReasons",
		anyOf: []string{
			"nvmlDeviceGetSupportedClocksEventReasons",
			"nvmlDeviceGetSupportedClocksEventReasons_v2",
			"nvmlDeviceGetSupportedClocksEventReasons_v3",
		},
		serves: "clocks_event_reasons.supported",
	},
	{
		goCall: "GetCurrentClocksEventReasons",
		anyOf: []string{
			"nvmlDeviceGetCurrentClocksEventReasons",
			"nvmlDeviceGetCurrentClocksEventReasons_v2",
			"nvmlDeviceGetCurrentClocksEventReasons_v3",
		},
		serves: "clocks_event_reasons.active and the per-reason flags",
	},
	{
		goCall: "GetFieldValues",
		anyOf:  []string{"nvmlDeviceGetFieldValues", "nvmlDeviceGetFieldValues_v2", "nvmlDeviceGetFieldValues_v3"},
		serves: "power.draw.average/instant, temperature.memory, reason counters, gpu_recovery_action, edpp_multiplier",
	},
	{goCall: "GetMemoryInfo_v2", anyOf: []string{"nvmlDeviceGetMemoryInfo_v2"}, serves: "memory.*"},
	{
		goCall: "GetComputeMode",
		anyOf:  []string{"nvmlDeviceGetComputeMode", "nvmlDeviceGetComputeMode_v2", "nvmlDeviceGetComputeMode_v3"},
		serves: "compute_mode",
	},
	{
		goCall: "GetCudaComputeCapability",
		anyOf: []string{
			"nvmlDeviceGetCudaComputeCapability",
			"nvmlDeviceGetCudaComputeCapability_v2",
			"nvmlDeviceGetCudaComputeCapability_v3",
		},
		serves: "compute_cap",
	},
	{
		goCall: "GetUtilizationRates",
		anyOf: []string{
			"nvmlDeviceGetUtilizationRates",
			"nvmlDeviceGetUtilizationRates_v2",
			"nvmlDeviceGetUtilizationRates_v3",
		},
		serves: "utilization.gpu/memory",
	},
	{
		goCall: "GetEncoderUtilization",
		anyOf: []string{
			"nvmlDeviceGetEncoderUtilization",
			"nvmlDeviceGetEncoderUtilization_v2",
			"nvmlDeviceGetEncoderUtilization_v3",
		},
		serves: "utilization.encoder",
	},
	{
		goCall: "GetDecoderUtilization",
		anyOf: []string{
			"nvmlDeviceGetDecoderUtilization",
			"nvmlDeviceGetDecoderUtilization_v2",
			"nvmlDeviceGetDecoderUtilization_v3",
		},
		serves: "utilization.decoder",
	},
	{
		goCall: "GetJpgUtilization",
		anyOf: []string{
			"nvmlDeviceGetJpgUtilization",
			"nvmlDeviceGetJpgUtilization_v2",
			"nvmlDeviceGetJpgUtilization_v3",
		},
		serves: "utilization.jpeg",
	},
	{
		goCall: "GetOfaUtilization",
		anyOf: []string{
			"nvmlDeviceGetOfaUtilization",
			"nvmlDeviceGetOfaUtilization_v2",
			"nvmlDeviceGetOfaUtilization_v3",
		},
		serves: "utilization.ofa",
	},
	{
		goCall: "GetEncoderStats",
		anyOf:  []string{"nvmlDeviceGetEncoderStats", "nvmlDeviceGetEncoderStats_v2", "nvmlDeviceGetEncoderStats_v3"},
		serves: "encoder.stats.*",
	},
	{
		goCall: "GetDramEncryptionMode",
		anyOf: []string{
			"nvmlDeviceGetDramEncryptionMode",
			"nvmlDeviceGetDramEncryptionMode_v2",
			"nvmlDeviceGetDramEncryptionMode_v3",
		},
		serves: "dramEncryption.mode.*",
	},
	{
		goCall: "GetEccMode",
		anyOf:  []string{"nvmlDeviceGetEccMode", "nvmlDeviceGetEccMode_v2", "nvmlDeviceGetEccMode_v3"},
		serves: "ecc.mode.*",
	},
	{
		goCall: "GetMemoryErrorCounter",
		anyOf: []string{
			"nvmlDeviceGetMemoryErrorCounter",
			"nvmlDeviceGetMemoryErrorCounter_v2",
			"nvmlDeviceGetMemoryErrorCounter_v3",
		},
		serves: "ecc.errors.* per location",
	},
	{
		goCall: "GetTotalEccErrors",
		anyOf: []string{
			"nvmlDeviceGetTotalEccErrors",
			"nvmlDeviceGetTotalEccErrors_v2",
			"nvmlDeviceGetTotalEccErrors_v3",
		},
		serves: "ecc.errors.*.total",
	},
	{
		goCall: "GetSramEccErrorStatus",
		anyOf: []string{
			"nvmlDeviceGetSramEccErrorStatus",
			"nvmlDeviceGetSramEccErrorStatus_v2",
			"nvmlDeviceGetSramEccErrorStatus_v3",
		},
		serves: "ecc.errors.*.sram.* detail",
	},
	{
		goCall: "GetRetiredPages",
		anyOf:  []string{"nvmlDeviceGetRetiredPages", "nvmlDeviceGetRetiredPages_v2", "nvmlDeviceGetRetiredPages_v3"},
		serves: "retired_pages counts",
	},
	{
		goCall: "GetRetiredPagesPendingStatus",
		anyOf: []string{
			"nvmlDeviceGetRetiredPagesPendingStatus",
			"nvmlDeviceGetRetiredPagesPendingStatus_v2",
			"nvmlDeviceGetRetiredPagesPendingStatus_v3",
		},
		serves: "retired_pages.pending",
	},
	{
		goCall: "GetRemappedRows",
		anyOf:  []string{"nvmlDeviceGetRemappedRows", "nvmlDeviceGetRemappedRows_v2", "nvmlDeviceGetRemappedRows_v3"},
		serves: "remapped_rows.*",
	},
	{
		goCall: "GetRowRemapperHistogram",
		anyOf: []string{
			"nvmlDeviceGetRowRemapperHistogram",
			"nvmlDeviceGetRowRemapperHistogram_v2",
			"nvmlDeviceGetRowRemapperHistogram_v3",
		},
		serves: "remapped_rows.histogram.*",
	},
	{
		goCall: "GetTemperature",
		anyOf:  []string{"nvmlDeviceGetTemperature", "nvmlDeviceGetTemperature_v2", "nvmlDeviceGetTemperature_v3"},
		serves: "temperature.gpu",
	},
	{
		goCall: "GetMarginTemperature",
		anyOf: []string{
			"nvmlDeviceGetMarginTemperature",
			"nvmlDeviceGetMarginTemperature_v2",
			"nvmlDeviceGetMarginTemperature_v3",
		},
		serves: "temperature.gpu.tlimit",
	},
	{
		goCall: "GetPowerUsage",
		anyOf:  []string{"nvmlDeviceGetPowerUsage", "nvmlDeviceGetPowerUsage_v2", "nvmlDeviceGetPowerUsage_v3"},
		serves: "power.draw",
	},
	{
		goCall: "GetPowerManagementLimit",
		anyOf: []string{
			"nvmlDeviceGetPowerManagementLimit",
			"nvmlDeviceGetPowerManagementLimit_v2",
			"nvmlDeviceGetPowerManagementLimit_v3",
		},
		serves: "power.limit",
	},
	{
		goCall: "GetEnforcedPowerLimit",
		anyOf: []string{
			"nvmlDeviceGetEnforcedPowerLimit",
			"nvmlDeviceGetEnforcedPowerLimit_v2",
			"nvmlDeviceGetEnforcedPowerLimit_v3",
		},
		serves: "enforced.power.limit",
	},
	{
		goCall: "GetPowerManagementDefaultLimit",
		anyOf: []string{
			"nvmlDeviceGetPowerManagementDefaultLimit",
			"nvmlDeviceGetPowerManagementDefaultLimit_v2",
			"nvmlDeviceGetPowerManagementDefaultLimit_v3",
		},
		serves: "power.default_limit",
	},
	{
		goCall: "GetPowerManagementLimitConstraints",
		anyOf: []string{
			"nvmlDeviceGetPowerManagementLimitConstraints",
			"nvmlDeviceGetPowerManagementLimitConstraints_v2",
			"nvmlDeviceGetPowerManagementLimitConstraints_v3",
		},
		serves: "power.min_limit/max_limit",
	},
	{
		goCall: "GetClockInfo",
		anyOf:  []string{"nvmlDeviceGetClockInfo", "nvmlDeviceGetClockInfo_v2", "nvmlDeviceGetClockInfo_v3"},
		serves: "clocks.current.*",
	},
	{
		goCall: "GetMaxClockInfo",
		anyOf:  []string{"nvmlDeviceGetMaxClockInfo", "nvmlDeviceGetMaxClockInfo_v2", "nvmlDeviceGetMaxClockInfo_v3"},
		serves: "clocks.max.*",
	},
	{
		goCall: "GetMigMode",
		anyOf:  []string{"nvmlDeviceGetMigMode", "nvmlDeviceGetMigMode_v2", "nvmlDeviceGetMigMode_v3"},
		serves: "mig.mode.*",
	},
	{
		goCall: "GetGspFirmwareMode",
		anyOf: []string{
			"nvmlDeviceGetGspFirmwareMode",
			"nvmlDeviceGetGspFirmwareMode_v2",
			"nvmlDeviceGetGspFirmwareMode_v3",
		},
		serves: "gsp.mode.*",
	},
	{
		goCall: "GetC2cModeInfoV",
		anyOf:  []string{"nvmlDeviceGetC2cModeInfoV", "nvmlDeviceGetC2cModeInfoV_v2", "nvmlDeviceGetC2cModeInfoV_v3"},
		serves: "c2c.mode",
	},
	{
		goCall: "GetConfComputeProtectedMemoryUsage",
		anyOf: []string{
			"nvmlDeviceGetConfComputeProtectedMemoryUsage",
			"nvmlDeviceGetConfComputeProtectedMemoryUsage_v2",
			"nvmlDeviceGetConfComputeProtectedMemoryUsage_v3",
		},
		serves: "protected_memory.*",
	},
	{
		goCall: "GetGpuFabricInfoV",
		anyOf: []string{
			"nvmlDeviceGetGpuFabricInfoV",
			"nvmlDeviceGetGpuFabricInfoV_v2",
			"nvmlDeviceGetGpuFabricInfoV_v3",
		},
		serves: "fabric.*",
	},
	{
		goCall: "GetPlatformInfo",
		anyOf:  []string{"nvmlDeviceGetPlatformInfo", "nvmlDeviceGetPlatformInfo_v2", "nvmlDeviceGetPlatformInfo_v3"},
		serves: "platform.*",
	},
	{goCall: "GetHostname_v1", anyOf: []string{"nvmlDeviceGetHostname_v1"}, serves: "hostname"},
	{
		goCall: "GetComputeRunningProcesses",
		anyOf: []string{
			"nvmlDeviceGetComputeRunningProcesses",
			"nvmlDeviceGetComputeRunningProcesses_v2",
			"nvmlDeviceGetComputeRunningProcesses_v3",
		},
		serves: "per-process metrics",
	},
}
