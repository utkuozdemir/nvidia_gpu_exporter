//go:build linux && cgo

// Package nvml provides the low-level NVML library bindings for Linux (via
// go-nvml) and Windows (via nvml.dll).
package nvml

import gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"

type (
	C2cModeInfo_v1             = gonvml.C2cModeInfo_v1
	ClockType                  = gonvml.ClockType
	ComputeMode                = gonvml.ComputeMode
	Device                     = gonvml.Device
	DeviceAddressingMode       = gonvml.DeviceAddressingMode
	DramEncryptionInfo         = gonvml.DramEncryptionInfo
	DriverModel                = gonvml.DriverModel
	EccCounterType             = gonvml.EccCounterType
	EccSramErrorStatus         = gonvml.EccSramErrorStatus
	EnableState                = gonvml.EnableState
	EventData                  = gonvml.EventData
	EventSet                   = gonvml.EventSet
	FieldValue                 = gonvml.FieldValue
	GpmMetric                  = gonvml.GpmMetric
	GpmMetricMetricInfo        = gonvml.GpmMetricMetricInfo
	GpmMetricsGetType          = gonvml.GpmMetricsGetType
	GpmSample                  = gonvml.GpmSample
	GpmSupport                 = gonvml.GpmSupport
	GpuFabricInfo_v2           = gonvml.GpuFabricInfo_v2
	GpuOperationMode           = gonvml.GpuOperationMode
	InforomObject              = gonvml.InforomObject
	MarginTemperature          = gonvml.MarginTemperature
	Memory                     = gonvml.Memory
	Memory_v2                  = gonvml.Memory_v2
	MemoryErrorType            = gonvml.MemoryErrorType
	MemoryLocation             = gonvml.MemoryLocation
	PageRetirementCause        = gonvml.PageRetirementCause
	PcieUtilCounter            = gonvml.PcieUtilCounter
	PciInfo                    = gonvml.PciInfo
	PciInfoExt                 = gonvml.PciInfoExt
	PlatformInfo               = gonvml.PlatformInfo
	ProcessInfo                = gonvml.ProcessInfo
	Pstates                    = gonvml.Pstates
	RemappedRowsInfo_v2        = gonvml.RemappedRowsInfo_v2
	Return                     = gonvml.Return
	RowRemapperHistogramValues = gonvml.RowRemapperHistogramValues
	TemperatureSensors         = gonvml.TemperatureSensors
	Utilization                = gonvml.Utilization
)

const (
	AGGREGATE_ECC = gonvml.AGGREGATE_ECC
	VOLATILE_ECC  = gonvml.VOLATILE_ECC

	CLOCK_GRAPHICS = gonvml.CLOCK_GRAPHICS
	CLOCK_MEM      = gonvml.CLOCK_MEM
	CLOCK_SM       = gonvml.CLOCK_SM
	CLOCK_VIDEO    = gonvml.CLOCK_VIDEO

	DEVICE_MIG_DISABLE = gonvml.DEVICE_MIG_DISABLE
	DEVICE_MIG_ENABLE  = gonvml.DEVICE_MIG_ENABLE

	ERROR_CORRUPTED_INFOROM       = gonvml.ERROR_CORRUPTED_INFOROM
	ERROR_DEPRECATED              = gonvml.ERROR_DEPRECATED
	ERROR_DRIVER_NOT_LOADED       = gonvml.ERROR_DRIVER_NOT_LOADED
	ERROR_FUNCTION_NOT_FOUND      = gonvml.ERROR_FUNCTION_NOT_FOUND
	ERROR_GPU_IS_LOST             = gonvml.ERROR_GPU_IS_LOST
	ERROR_GPU_NOT_FOUND           = gonvml.ERROR_GPU_NOT_FOUND
	ERROR_IN_USE                  = gonvml.ERROR_IN_USE
	ERROR_INSUFFICIENT_SIZE       = gonvml.ERROR_INSUFFICIENT_SIZE
	ERROR_INVALID_ARGUMENT        = gonvml.ERROR_INVALID_ARGUMENT
	ERROR_IRQ_ISSUE               = gonvml.ERROR_IRQ_ISSUE
	ERROR_LIB_RM_VERSION_MISMATCH = gonvml.ERROR_LIB_RM_VERSION_MISMATCH
	ERROR_LIBRARY_NOT_FOUND       = gonvml.ERROR_LIBRARY_NOT_FOUND
	ERROR_MEMORY                  = gonvml.ERROR_MEMORY
	ERROR_NO_DATA                 = gonvml.ERROR_NO_DATA
	ERROR_NO_PERMISSION           = gonvml.ERROR_NO_PERMISSION
	ERROR_NOT_FOUND               = gonvml.ERROR_NOT_FOUND
	ERROR_NOT_SUPPORTED           = gonvml.ERROR_NOT_SUPPORTED
	ERROR_OPERATING_SYSTEM        = gonvml.ERROR_OPERATING_SYSTEM
	ERROR_RESET_REQUIRED          = gonvml.ERROR_RESET_REQUIRED
	ERROR_TIMEOUT                 = gonvml.ERROR_TIMEOUT
	ERROR_UNINITIALIZED           = gonvml.ERROR_UNINITIALIZED
	ERROR_UNKNOWN                 = gonvml.ERROR_UNKNOWN
	SUCCESS                       = gonvml.SUCCESS

	EventTypeXidCriticalError = gonvml.EventTypeXidCriticalError

	FEATURE_DISABLED = gonvml.FEATURE_DISABLED
	FEATURE_ENABLED  = gonvml.FEATURE_ENABLED

	FI_DEV_CLOCKS_EVENT_REASON_HW_POWER_BRAKE_SLOWDOWN = gonvml.FI_DEV_CLOCKS_EVENT_REASON_HW_POWER_BRAKE_SLOWDOWN
	FI_DEV_CLOCKS_EVENT_REASON_HW_THERM_SLOWDOWN       = gonvml.FI_DEV_CLOCKS_EVENT_REASON_HW_THERM_SLOWDOWN
	FI_DEV_CLOCKS_EVENT_REASON_SW_POWER_CAP            = gonvml.FI_DEV_CLOCKS_EVENT_REASON_SW_POWER_CAP
	FI_DEV_CLOCKS_EVENT_REASON_SW_THERM_SLOWDOWN       = gonvml.FI_DEV_CLOCKS_EVENT_REASON_SW_THERM_SLOWDOWN
	FI_DEV_CLOCKS_EVENT_REASON_SYNC_BOOST              = gonvml.FI_DEV_CLOCKS_EVENT_REASON_SYNC_BOOST
	FI_DEV_GET_GPU_RECOVERY_ACTION                     = gonvml.FI_DEV_GET_GPU_RECOVERY_ACTION
	FI_DEV_MEMORY_TEMP                                 = gonvml.FI_DEV_MEMORY_TEMP
	FI_DEV_POWER_AVERAGE                               = gonvml.FI_DEV_POWER_AVERAGE
	FI_DEV_POWER_INSTANT                               = gonvml.FI_DEV_POWER_INSTANT

	INFOROM_ECC   = gonvml.INFOROM_ECC
	INFOROM_OEM   = gonvml.INFOROM_OEM
	INFOROM_POWER = gonvml.INFOROM_POWER

	MEMORY_ERROR_TYPE_CORRECTED   = gonvml.MEMORY_ERROR_TYPE_CORRECTED
	MEMORY_ERROR_TYPE_UNCORRECTED = gonvml.MEMORY_ERROR_TYPE_UNCORRECTED

	MEMORY_LOCATION_CBU            = gonvml.MEMORY_LOCATION_CBU
	MEMORY_LOCATION_DEVICE_MEMORY  = gonvml.MEMORY_LOCATION_DEVICE_MEMORY
	MEMORY_LOCATION_DRAM           = gonvml.MEMORY_LOCATION_DRAM
	MEMORY_LOCATION_L1_CACHE       = gonvml.MEMORY_LOCATION_L1_CACHE
	MEMORY_LOCATION_L2_CACHE       = gonvml.MEMORY_LOCATION_L2_CACHE
	MEMORY_LOCATION_REGISTER_FILE  = gonvml.MEMORY_LOCATION_REGISTER_FILE
	MEMORY_LOCATION_SRAM           = gonvml.MEMORY_LOCATION_SRAM
	MEMORY_LOCATION_TEXTURE_MEMORY = gonvml.MEMORY_LOCATION_TEXTURE_MEMORY

	PAGE_RETIREMENT_CAUSE_DOUBLE_BIT_ECC_ERROR           = gonvml.PAGE_RETIREMENT_CAUSE_DOUBLE_BIT_ECC_ERROR
	PAGE_RETIREMENT_CAUSE_MULTIPLE_SINGLE_BIT_ECC_ERRORS = gonvml.PAGE_RETIREMENT_CAUSE_MULTIPLE_SINGLE_BIT_ECC_ERRORS

	PCIE_UTIL_RX_BYTES = gonvml.PCIE_UTIL_RX_BYTES
	PCIE_UTIL_TX_BYTES = gonvml.PCIE_UTIL_TX_BYTES

	TEMPERATURE_GPU = gonvml.TEMPERATURE_GPU

	VALUE_TYPE_DOUBLE             = gonvml.VALUE_TYPE_DOUBLE
	VALUE_TYPE_SIGNED_INT         = gonvml.VALUE_TYPE_SIGNED_INT
	VALUE_TYPE_SIGNED_LONG_LONG   = gonvml.VALUE_TYPE_SIGNED_LONG_LONG
	VALUE_TYPE_UNSIGNED_INT       = gonvml.VALUE_TYPE_UNSIGNED_INT
	VALUE_TYPE_UNSIGNED_LONG      = gonvml.VALUE_TYPE_UNSIGNED_LONG
	VALUE_TYPE_UNSIGNED_LONG_LONG = gonvml.VALUE_TYPE_UNSIGNED_LONG_LONG
)

//nolint:gochecknoglobals // thin pass-through to go-nvml on Linux
var (
	DeviceGetCount             = gonvml.DeviceGetCount
	DeviceGetHandleByIndex     = gonvml.DeviceGetHandleByIndex
	DeviceValidateInforom      = gonvml.DeviceValidateInforom
	EventSetCreate             = gonvml.EventSetCreate
	Extensions                 = gonvml.Extensions
	GpmMetricsGet              = gonvml.GpmMetricsGet
	GpmMigSampleGet            = gonvml.GpmMigSampleGet
	GpmSampleAlloc             = gonvml.GpmSampleAlloc
	GpmSampleFree              = gonvml.GpmSampleFree
	Init                       = gonvml.Init
	Shutdown                   = gonvml.Shutdown
	SystemGetCudaDriverVersion = gonvml.SystemGetCudaDriverVersion
	SystemGetDriverVersion     = gonvml.SystemGetDriverVersion
	SystemGetProcessName       = gonvml.SystemGetProcessName
)
