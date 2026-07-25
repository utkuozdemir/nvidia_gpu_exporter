//go:build linux && cgo

package nvmlnative

import "github.com/NVIDIA/go-nvml/pkg/nvml"

// device is the narrow NVML surface this package uses. Collector code depends
// on this interface and never on nvml.Device directly, so that every entry
// point passes through the availability guard below. A getter whose export the
// driver library does not provide crashes the process when called (the cgo
// binding is resolved lazily by the loader and has no way to report failure),
// so the guard answers with ERROR_FUNCTION_NOT_FOUND without entering cgo. The
// existing collector paths already render that return as an absent series.
//
// Adding a getter here is what forces its symbol to be declared: the compiler
// rejects a collector call that this interface does not carry.
type device interface {
	GetAccountingBufferSize() (int, nvml.Return)
	GetAccountingMode() (nvml.EnableState, nvml.Return)
	GetAddressingMode() (nvml.DeviceAddressingMode, nvml.Return)
	GetC2cModeInfoV1() (nvml.C2cModeInfo_v1, nvml.Return)
	GetClockInfo(nvml.ClockType) (uint32, nvml.Return)
	GetComputeInstanceId() (int, nvml.Return)
	GetComputeMode() (nvml.ComputeMode, nvml.Return)
	GetComputeRunningProcesses() ([]nvml.ProcessInfo, nvml.Return)
	GetConfComputeProtectedMemoryUsage() (nvml.Memory, nvml.Return)
	GetCudaComputeCapability() (int, int, nvml.Return)
	GetCurrentClocksEventReasons() (uint64, nvml.Return)
	GetCurrPcieLinkGeneration() (int, nvml.Return)
	GetCurrPcieLinkWidth() (int, nvml.Return)
	GetDecoderUtilization() (uint32, uint32, nvml.Return)
	GetDisplayActive() (nvml.EnableState, nvml.Return)
	GetDisplayMode() (nvml.EnableState, nvml.Return)
	GetDramEncryptionMode() (nvml.DramEncryptionInfo, nvml.DramEncryptionInfo, nvml.Return)
	GetDriverModel() (nvml.DriverModel, nvml.DriverModel, nvml.Return)
	GetEccMode() (nvml.EnableState, nvml.EnableState, nvml.Return)
	GetEncoderStats() (int, uint32, uint32, nvml.Return)
	GetEncoderUtilization() (uint32, uint32, nvml.Return)
	GetEnforcedPowerLimit() (uint32, nvml.Return)
	GetFanSpeed() (uint32, nvml.Return)
	GetFieldValues([]nvml.FieldValue) nvml.Return
	GetGpuFabricInfoV2() (nvml.GpuFabricInfo_v2, nvml.Return)
	GetGpuInstanceId() (int, nvml.Return)
	GetGpuMaxPcieLinkGeneration() (int, nvml.Return)
	GetGpuOperationMode() (nvml.GpuOperationMode, nvml.GpuOperationMode, nvml.Return)
	GetGspFirmwareMode() (bool, bool, nvml.Return)
	GetHostname_v1() (string, nvml.Return)
	GetIndex() (int, nvml.Return)
	GetInforomImageVersion() (string, nvml.Return)
	GetInforomVersion(nvml.InforomObject) (string, nvml.Return)
	GetJpgUtilization() (uint32, uint32, nvml.Return)
	GetMarginTemperature() (nvml.MarginTemperature, nvml.Return)
	GetMaxClockInfo(nvml.ClockType) (uint32, nvml.Return)
	GetMaxMigDeviceCount() (int, nvml.Return)
	GetMaxPcieLinkGeneration() (int, nvml.Return)
	GetMaxPcieLinkWidth() (int, nvml.Return)
	GetMemoryErrorCounter(nvml.MemoryErrorType, nvml.EccCounterType, nvml.MemoryLocation) (uint64, nvml.Return)
	GetMemoryInfo_v2() (nvml.Memory_v2, nvml.Return)
	GetMigDeviceHandleByIndex(int) (device, nvml.Return)
	GetMigMode() (int, int, nvml.Return)
	GetName() (string, nvml.Return)
	GetOfaUtilization() (uint32, uint32, nvml.Return)
	GetPcieThroughput(nvml.PcieUtilCounter) (uint32, nvml.Return)
	GetPciInfoExt() (nvml.PciInfoExt, nvml.Return)
	GetPerformanceState() (nvml.Pstates, nvml.Return)
	GetPersistenceMode() (nvml.EnableState, nvml.Return)
	GetPlatformInfo() (nvml.PlatformInfo, nvml.Return)
	GetPowerManagementDefaultLimit() (uint32, nvml.Return)
	GetPowerManagementLimit() (uint32, nvml.Return)
	GetPowerManagementLimitConstraints() (uint32, uint32, nvml.Return)
	GetPowerUsage() (uint32, nvml.Return)
	GetRemappedRows() (int, int, bool, bool, nvml.Return)
	GetRemappedRows_v2() (nvml.RemappedRowsInfo_v2, nvml.Return)
	GetRetiredPages(nvml.PageRetirementCause) ([]uint64, nvml.Return)
	GetRetiredPagesPendingStatus() (nvml.EnableState, nvml.Return)
	GetRowRemapperHistogram() (nvml.RowRemapperHistogramValues, nvml.Return)
	GetSerial() (string, nvml.Return)
	GetSramEccErrorStatus() (nvml.EccSramErrorStatus, nvml.Return)
	GetSupportedClocksEventReasons() (uint64, nvml.Return)
	GetTemperature(nvml.TemperatureSensors) (uint32, nvml.Return)
	GetTotalEccErrors(nvml.MemoryErrorType, nvml.EccCounterType) (uint64, nvml.Return)
	GetTotalEnergyConsumption() (uint64, nvml.Return)
	GetUtilizationRates() (nvml.Utilization, nvml.Return)
	GetUUID() (string, nvml.Return)
	GetVbiosVersion() (string, nvml.Return)
	GpmQueryDeviceSupport() (nvml.GpmSupport, nvml.Return)
	RegisterEvents(uint64, nvml.EventSet) nvml.Return

	// raw exposes the underlying handle for the few places that must match
	// identities go-nvml hands back to us (the XID watcher keys events by the
	// device it registered). It must never be used to make a call.
	raw() nvml.Device
}

// guardedDevice wraps a driver device handle with the symbol availability
// snapshot taken when NVML was initialized.
type guardedDevice struct {
	dev   nvml.Device
	avail *availability
}

func (g guardedDevice) raw() nvml.Device { return g.dev }

func (g guardedDevice) GetAccountingBufferSize() (int, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetAccountingBufferSize") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetAccountingBufferSize()
}

func (g guardedDevice) GetAccountingMode() (nvml.EnableState, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetAccountingMode") {
		var z0 nvml.EnableState

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetAccountingMode()
}

func (g guardedDevice) GetAddressingMode() (nvml.DeviceAddressingMode, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetAddressingMode") {
		var z0 nvml.DeviceAddressingMode

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetAddressingMode()
}

func (g guardedDevice) GetC2cModeInfoV1() (nvml.C2cModeInfo_v1, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetC2cModeInfoV") {
		var z0 nvml.C2cModeInfo_v1

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetC2cModeInfoV().V1()
}

func (g guardedDevice) GetClockInfo(p0 nvml.ClockType) (uint32, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetClockInfo") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetClockInfo(p0)
}

func (g guardedDevice) GetComputeInstanceId() (int, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetComputeInstanceId") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetComputeInstanceId()
}

func (g guardedDevice) GetComputeMode() (nvml.ComputeMode, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetComputeMode") {
		var z0 nvml.ComputeMode

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetComputeMode()
}

func (g guardedDevice) GetComputeRunningProcesses() ([]nvml.ProcessInfo, nvml.Return) {
	// go-nvml selects the version of this entry point itself: it dlsym-probes
	// for the newer spellings at load time and falls back to v1, so the call is
	// already safe against a driver that lacks the newer one. Hard-probing a
	// single spelling here would be wrong.
	if !g.avail.hasAny("nvmlDeviceGetComputeRunningProcesses",
		"nvmlDeviceGetComputeRunningProcesses_v2", "nvmlDeviceGetComputeRunningProcesses_v3") {
		var z0 []nvml.ProcessInfo

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetComputeRunningProcesses()
}

func (g guardedDevice) GetConfComputeProtectedMemoryUsage() (nvml.Memory, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetConfComputeProtectedMemoryUsage") {
		var z0 nvml.Memory

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetConfComputeProtectedMemoryUsage()
}

func (g guardedDevice) GetCudaComputeCapability() (int, int, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetCudaComputeCapability") {
		return 0, 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetCudaComputeCapability()
}

func (g guardedDevice) GetCurrentClocksEventReasons() (uint64, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetCurrentClocksEventReasons") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetCurrentClocksEventReasons()
}

func (g guardedDevice) GetCurrPcieLinkGeneration() (int, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetCurrPcieLinkGeneration") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetCurrPcieLinkGeneration()
}

func (g guardedDevice) GetCurrPcieLinkWidth() (int, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetCurrPcieLinkWidth") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetCurrPcieLinkWidth()
}

func (g guardedDevice) GetDecoderUtilization() (uint32, uint32, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetDecoderUtilization") {
		return 0, 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetDecoderUtilization()
}

func (g guardedDevice) GetDisplayActive() (nvml.EnableState, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetDisplayActive") {
		var z0 nvml.EnableState

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetDisplayActive()
}

func (g guardedDevice) GetDisplayMode() (nvml.EnableState, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetDisplayMode") {
		var z0 nvml.EnableState

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetDisplayMode()
}

func (g guardedDevice) GetDramEncryptionMode() (nvml.DramEncryptionInfo, nvml.DramEncryptionInfo, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetDramEncryptionMode") {
		var z0 nvml.DramEncryptionInfo
		var z1 nvml.DramEncryptionInfo

		return z0, z1, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetDramEncryptionMode()
}

func (g guardedDevice) GetDriverModel() (nvml.DriverModel, nvml.DriverModel, nvml.Return) {
	// go-nvml selects the version of this entry point itself: it dlsym-probes
	// for the newer spellings at load time and falls back to v1, so the call is
	// already safe against a driver that lacks the newer one. Hard-probing a
	// single spelling here would be wrong.
	if !g.avail.hasAny("nvmlDeviceGetDriverModel", "nvmlDeviceGetDriverModel_v2") {
		var z0 nvml.DriverModel
		var z1 nvml.DriverModel

		return z0, z1, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetDriverModel()
}

func (g guardedDevice) GetEccMode() (nvml.EnableState, nvml.EnableState, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetEccMode") {
		var z0 nvml.EnableState
		var z1 nvml.EnableState

		return z0, z1, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetEccMode()
}

func (g guardedDevice) GetEncoderStats() (int, uint32, uint32, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetEncoderStats") {
		return 0, 0, 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetEncoderStats()
}

func (g guardedDevice) GetEncoderUtilization() (uint32, uint32, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetEncoderUtilization") {
		return 0, 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetEncoderUtilization()
}

func (g guardedDevice) GetEnforcedPowerLimit() (uint32, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetEnforcedPowerLimit") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetEnforcedPowerLimit()
}

func (g guardedDevice) GetFanSpeed() (uint32, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetFanSpeed") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetFanSpeed()
}

func (g guardedDevice) GetFieldValues(p0 []nvml.FieldValue) nvml.Return {
	if !g.avail.has("nvmlDeviceGetFieldValues") {
		return nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetFieldValues(p0)
}

func (g guardedDevice) GetGpuFabricInfoV2() (nvml.GpuFabricInfo_v2, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetGpuFabricInfoV") {
		var z0 nvml.GpuFabricInfo_v2

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetGpuFabricInfoV().V2()
}

func (g guardedDevice) GetGpuInstanceId() (int, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetGpuInstanceId") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetGpuInstanceId()
}

func (g guardedDevice) GetGpuMaxPcieLinkGeneration() (int, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetGpuMaxPcieLinkGeneration") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetGpuMaxPcieLinkGeneration()
}

func (g guardedDevice) GetGpuOperationMode() (nvml.GpuOperationMode, nvml.GpuOperationMode, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetGpuOperationMode") {
		var z0 nvml.GpuOperationMode
		var z1 nvml.GpuOperationMode

		return z0, z1, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetGpuOperationMode()
}

func (g guardedDevice) GetGspFirmwareMode() (bool, bool, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetGspFirmwareMode") {
		return false, false, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetGspFirmwareMode()
}

func (g guardedDevice) GetHostname_v1() (string, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetHostname_v1") {
		return "", nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetHostname_v1()
}

func (g guardedDevice) GetIndex() (int, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetIndex") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetIndex()
}

func (g guardedDevice) GetInforomImageVersion() (string, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetInforomImageVersion") {
		return "", nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetInforomImageVersion()
}

func (g guardedDevice) GetInforomVersion(p0 nvml.InforomObject) (string, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetInforomVersion") {
		return "", nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetInforomVersion(p0)
}

func (g guardedDevice) GetJpgUtilization() (uint32, uint32, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetJpgUtilization") {
		return 0, 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetJpgUtilization()
}

func (g guardedDevice) GetMarginTemperature() (nvml.MarginTemperature, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetMarginTemperature") {
		var z0 nvml.MarginTemperature

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetMarginTemperature()
}

func (g guardedDevice) GetMaxClockInfo(p0 nvml.ClockType) (uint32, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetMaxClockInfo") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetMaxClockInfo(p0)
}

func (g guardedDevice) GetMaxMigDeviceCount() (int, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetMaxMigDeviceCount") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetMaxMigDeviceCount()
}

func (g guardedDevice) GetMaxPcieLinkGeneration() (int, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetMaxPcieLinkGeneration") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetMaxPcieLinkGeneration()
}

func (g guardedDevice) GetMaxPcieLinkWidth() (int, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetMaxPcieLinkWidth") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetMaxPcieLinkWidth()
}

func (g guardedDevice) GetMemoryErrorCounter(
	p0 nvml.MemoryErrorType,
	p1 nvml.EccCounterType,
	p2 nvml.MemoryLocation,
) (uint64, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetMemoryErrorCounter") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetMemoryErrorCounter(p0, p1, p2)
}

func (g guardedDevice) GetMemoryInfo_v2() (nvml.Memory_v2, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetMemoryInfo_v2") {
		var z0 nvml.Memory_v2

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetMemoryInfo_v2()
}

func (g guardedDevice) GetMigDeviceHandleByIndex(p0 int) (device, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetMigDeviceHandleByIndex") {
		return nil, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	mig, ret := g.dev.GetMigDeviceHandleByIndex(p0)
	if ret != nvml.SUCCESS {
		return nil, ret
	}

	return guardedDevice{dev: mig, avail: g.avail}, ret
}

func (g guardedDevice) GetMigMode() (int, int, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetMigMode") {
		return 0, 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetMigMode()
}

func (g guardedDevice) GetName() (string, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetName") {
		return "", nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetName()
}

func (g guardedDevice) GetOfaUtilization() (uint32, uint32, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetOfaUtilization") {
		return 0, 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetOfaUtilization()
}

func (g guardedDevice) GetPcieThroughput(p0 nvml.PcieUtilCounter) (uint32, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetPcieThroughput") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetPcieThroughput(p0)
}

func (g guardedDevice) GetPciInfoExt() (nvml.PciInfoExt, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetPciInfoExt") {
		var z0 nvml.PciInfoExt

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetPciInfoExt()
}

func (g guardedDevice) GetPerformanceState() (nvml.Pstates, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetPerformanceState") {
		var z0 nvml.Pstates

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetPerformanceState()
}

func (g guardedDevice) GetPersistenceMode() (nvml.EnableState, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetPersistenceMode") {
		var z0 nvml.EnableState

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetPersistenceMode()
}

func (g guardedDevice) GetPlatformInfo() (nvml.PlatformInfo, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetPlatformInfo") {
		var z0 nvml.PlatformInfo

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetPlatformInfo()
}

func (g guardedDevice) GetPowerManagementDefaultLimit() (uint32, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetPowerManagementDefaultLimit") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetPowerManagementDefaultLimit()
}

func (g guardedDevice) GetPowerManagementLimit() (uint32, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetPowerManagementLimit") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetPowerManagementLimit()
}

func (g guardedDevice) GetPowerManagementLimitConstraints() (uint32, uint32, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetPowerManagementLimitConstraints") {
		return 0, 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetPowerManagementLimitConstraints()
}

func (g guardedDevice) GetPowerUsage() (uint32, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetPowerUsage") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetPowerUsage()
}

func (g guardedDevice) GetRemappedRows() (int, int, bool, bool, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetRemappedRows") {
		return 0, 0, false, false, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetRemappedRows()
}

func (g guardedDevice) GetRemappedRows_v2() (nvml.RemappedRowsInfo_v2, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetRemappedRows_v2") {
		var z0 nvml.RemappedRowsInfo_v2

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetRemappedRows_v2()
}

func (g guardedDevice) GetRetiredPages(p0 nvml.PageRetirementCause) ([]uint64, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetRetiredPages") {
		return nil, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetRetiredPages(p0)
}

func (g guardedDevice) GetRetiredPagesPendingStatus() (nvml.EnableState, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetRetiredPagesPendingStatus") {
		var z0 nvml.EnableState

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetRetiredPagesPendingStatus()
}

func (g guardedDevice) GetRowRemapperHistogram() (nvml.RowRemapperHistogramValues, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetRowRemapperHistogram") {
		var z0 nvml.RowRemapperHistogramValues

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetRowRemapperHistogram()
}

func (g guardedDevice) GetSerial() (string, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetSerial") {
		return "", nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetSerial()
}

func (g guardedDevice) GetSramEccErrorStatus() (nvml.EccSramErrorStatus, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetSramEccErrorStatus") {
		var z0 nvml.EccSramErrorStatus

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetSramEccErrorStatus()
}

func (g guardedDevice) GetSupportedClocksEventReasons() (uint64, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetSupportedClocksEventReasons") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetSupportedClocksEventReasons()
}

func (g guardedDevice) GetTemperature(p0 nvml.TemperatureSensors) (uint32, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetTemperature") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetTemperature(p0)
}

func (g guardedDevice) GetTotalEccErrors(p0 nvml.MemoryErrorType, p1 nvml.EccCounterType) (uint64, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetTotalEccErrors") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetTotalEccErrors(p0, p1)
}

func (g guardedDevice) GetTotalEnergyConsumption() (uint64, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetTotalEnergyConsumption") {
		return 0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetTotalEnergyConsumption()
}

func (g guardedDevice) GetUtilizationRates() (nvml.Utilization, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetUtilizationRates") {
		var z0 nvml.Utilization

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetUtilizationRates()
}

func (g guardedDevice) GetUUID() (string, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetUUID") {
		return "", nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetUUID()
}

func (g guardedDevice) GetVbiosVersion() (string, nvml.Return) {
	if !g.avail.has("nvmlDeviceGetVbiosVersion") {
		return "", nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GetVbiosVersion()
}

func (g guardedDevice) GpmQueryDeviceSupport() (nvml.GpmSupport, nvml.Return) {
	if !g.avail.has("nvmlGpmQueryDeviceSupport") {
		var z0 nvml.GpmSupport

		return z0, nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.GpmQueryDeviceSupport()
}

func (g guardedDevice) RegisterEvents(p0 uint64, p1 nvml.EventSet) nvml.Return {
	if !g.avail.has("nvmlDeviceRegisterEvents") {
		return nvml.ERROR_FUNCTION_NOT_FOUND
	}

	return g.dev.RegisterEvents(p0, p1)
}
