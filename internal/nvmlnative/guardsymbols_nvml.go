//go:build linux && cgo

package nvmlnative

// guardedSymbols are the exact driver-library exports the guarded device
// surface binds, one per call site. They are the symbols probed at
// initialization; anything absent makes its call answer
// ERROR_FUNCTION_NOT_FOUND instead of entering cgo and crashing the process.
//
// These are the literal symbols go-nvml binds, derived from its generated
// bindings, NOT the "any of these spellings" sets in nvmlSymbolRequirements.
// Those exist for the capture-inventory drift test and are deliberately
// broader: go-nvml compiles with NVML_NO_UNVERSIONED_FUNC_DEFS, so each cgo
// wrapper binds one literal name and an alternative spelling being present
// does not make the call safe.
//
// A test keeps this list in lockstep with the guard.
//
//nolint:gochecknoglobals // shared manifest, mirrors the guard
var guardedSymbols = []string{
	"nvmlDeviceGetAccountingBufferSize",
	"nvmlDeviceGetAccountingMode",
	"nvmlDeviceGetAddressingMode",
	"nvmlDeviceGetC2cModeInfoV",
	"nvmlDeviceGetClockInfo",
	"nvmlDeviceGetComputeInstanceId",
	"nvmlDeviceGetComputeMode",
	"nvmlDeviceGetConfComputeProtectedMemoryUsage",
	"nvmlDeviceGetCudaComputeCapability",
	"nvmlDeviceGetCurrPcieLinkGeneration",
	"nvmlDeviceGetCurrPcieLinkWidth",
	"nvmlDeviceGetCurrentClocksEventReasons",
	"nvmlDeviceGetDecoderUtilization",
	"nvmlDeviceGetDisplayActive",
	"nvmlDeviceGetDisplayMode",
	"nvmlDeviceGetDramEncryptionMode",
	"nvmlDeviceGetEccMode",
	"nvmlDeviceGetEncoderStats",
	"nvmlDeviceGetEncoderUtilization",
	"nvmlDeviceGetEnforcedPowerLimit",
	"nvmlDeviceGetFanSpeed",
	"nvmlDeviceGetFieldValues",
	"nvmlDeviceGetGpuFabricInfoV",
	"nvmlDeviceGetGpuInstanceId",
	"nvmlDeviceGetGpuMaxPcieLinkGeneration",
	"nvmlDeviceGetGpuOperationMode",
	"nvmlDeviceGetGspFirmwareMode",
	"nvmlDeviceGetHostname_v1",
	"nvmlDeviceGetIndex",
	"nvmlDeviceGetInforomImageVersion",
	"nvmlDeviceGetInforomVersion",
	"nvmlDeviceGetJpgUtilization",
	"nvmlDeviceGetMarginTemperature",
	"nvmlDeviceGetMaxClockInfo",
	"nvmlDeviceGetMaxMigDeviceCount",
	"nvmlDeviceGetMaxPcieLinkGeneration",
	"nvmlDeviceGetMaxPcieLinkWidth",
	"nvmlDeviceGetMemoryErrorCounter",
	"nvmlDeviceGetMemoryInfo_v2",
	"nvmlDeviceGetMigDeviceHandleByIndex",
	"nvmlDeviceGetMigMode",
	"nvmlDeviceGetName",
	"nvmlDeviceGetOfaUtilization",
	"nvmlDeviceGetPciInfoExt",
	"nvmlDeviceGetPcieThroughput",
	"nvmlDeviceGetPerformanceState",
	"nvmlDeviceGetPersistenceMode",
	"nvmlDeviceGetPlatformInfo",
	"nvmlDeviceGetPowerManagementDefaultLimit",
	"nvmlDeviceGetPowerManagementLimit",
	"nvmlDeviceGetPowerManagementLimitConstraints",
	"nvmlDeviceGetPowerUsage",
	"nvmlDeviceGetRemappedRows",
	"nvmlDeviceGetRemappedRows_v2",
	"nvmlDeviceGetRetiredPages",
	"nvmlDeviceGetRetiredPagesPendingStatus",
	"nvmlDeviceGetRowRemapperHistogram",
	"nvmlDeviceGetSerial",
	"nvmlDeviceGetSramEccErrorStatus",
	"nvmlDeviceGetSupportedClocksEventReasons",
	"nvmlDeviceGetTemperature",
	"nvmlDeviceGetTotalEccErrors",
	"nvmlDeviceGetTotalEnergyConsumption",
	"nvmlDeviceGetUUID",
	"nvmlDeviceGetUtilizationRates",
	"nvmlDeviceGetVbiosVersion",
	"nvmlDeviceValidateInforom",
	"nvmlGpmSampleAlloc",
	"nvmlGpmSampleFree",
	"nvmlGpmMigSampleGet",
	"nvmlGpmMetricsGet",
	"nvmlEventSetCreate",
	"nvmlEventSetFree",
	"nvmlSystemGetProcessName",
	"nvmlSystemGetDriverVersion",
	"nvmlSystemGetCudaDriverVersion",
	"nvmlDeviceGetDriverModel",
	"nvmlDeviceGetDriverModel_v2",
	"nvmlDeviceGetComputeRunningProcesses",
	"nvmlDeviceGetComputeRunningProcesses_v2",
	"nvmlDeviceGetComputeRunningProcesses_v3",
	"nvmlDeviceRegisterEvents",
	"nvmlGpmQueryDeviceSupport",
}
