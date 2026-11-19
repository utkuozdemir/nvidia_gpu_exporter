//go:build linux && cgo

// Package nvml provides the low-level NVML library bindings for Linux (via
// go-nvml) and Windows (via nvml.dll).
package nvml

/*
#cgo linux LDFLAGS: -Wl,--export-dynamic -Wl,--unresolved-symbols=ignore-in-object-files
#include <stdint.h>
#include <stdlib.h>

extern int nvmlSystemGetProcessName(unsigned int pid, char *name, unsigned int length);
extern int nvmlDeviceGetGpuUptime_v1(void *device, uint32_t *uptimeTimeSeconds);

typedef struct {
	uint32_t version;
	uint32_t sensorType;
	int32_t temperature;
} exporterNvmlTemperatureV1;

extern int nvmlDeviceGetTemperatureV(void *device, exporterNvmlTemperatureV1 *temperature);

typedef struct {
	uint32_t version;
	uint32_t fan;
	uint32_t speed;
} exporterNvmlFanSpeedInfoV1;

extern int nvmlDeviceGetFanSpeedRPM(void *device, exporterNvmlFanSpeedInfoV1 *fanSpeed);

typedef struct {
	int ret;
	uint32_t value;
} exporterUint32Result;

typedef struct {
	int ret;
	int32_t value;
} exporterInt32Result;

static exporterUint32Result exporterGetGpuUptimeV1(uintptr_t device) {
	exporterUint32Result out = {0, 0};
	out.ret = nvmlDeviceGetGpuUptime_v1((void *)device, &out.value);
	return out;
}

static exporterInt32Result exporterGetTemperatureV(uintptr_t device, uint32_t sensor) {
	exporterNvmlTemperatureV1 temp = {0, 0, 0};
	temp.version = (uint32_t)(sizeof(exporterNvmlTemperatureV1) | (1U << 24U));
	temp.sensorType = sensor;
	exporterInt32Result out = {0, 0};
	out.ret = nvmlDeviceGetTemperatureV((void *)device, &temp);
	out.value = temp.temperature;
	return out;
}

static exporterUint32Result exporterGetFanSpeedRPM(uintptr_t device, uint32_t fan) {
	exporterNvmlFanSpeedInfoV1 info = {0, 0, 0};
	info.version = (uint32_t)(sizeof(exporterNvmlFanSpeedInfoV1) | (1U << 24U));
	info.fan = fan;
	exporterUint32Result out = {0, 0};
	out.ret = nvmlDeviceGetFanSpeedRPM((void *)device, &info);
	out.value = info.speed;
	return out;
}

typedef struct {
	uint64_t timeStamp;
	uint32_t pid;
	uint32_t smUtil;
	uint32_t memUtil;
	uint32_t encUtil;
	uint32_t decUtil;
	uint32_t jpgUtil;
	uint32_t ofaUtil;
} exporterNvmlProcessUtilizationInfoV1;

typedef struct {
	uint32_t version;
	uint32_t processSamplesCount;
	uint64_t lastSeenTimeStamp;
	exporterNvmlProcessUtilizationInfoV1 *procUtilArray;
} exporterNvmlProcessesUtilizationInfoV1;

extern int nvmlDeviceGetProcessesUtilizationInfo(
	void *device,
	exporterNvmlProcessesUtilizationInfoV1 *procesesUtilInfo
);

typedef struct {
	int ret;
	uint32_t count;
} exporterProcUtilCall;

static exporterProcUtilCall exporterCallProcessesUtilizationInfo(
	uintptr_t device,
	uint64_t lastSeenTimeStamp,
	uintptr_t procUtilArray,
	uint32_t countIn
) {
	exporterNvmlProcessesUtilizationInfoV1 info = {0, 0, 0, NULL};
	info.version = (uint32_t)(sizeof(exporterNvmlProcessesUtilizationInfoV1) | (1U << 24U));
	info.processSamplesCount = countIn;
	info.lastSeenTimeStamp = lastSeenTimeStamp;
	info.procUtilArray = (exporterNvmlProcessUtilizationInfoV1 *)procUtilArray;
	exporterProcUtilCall out = {0, 0};
	out.ret = nvmlDeviceGetProcessesUtilizationInfo((void *)device, &info);
	out.count = info.processSamplesCount;
	return out;
}
*/
import "C"

import (
	"bytes"
	"reflect"
	"unsafe"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
)

type (
	BAR1Memory                  = gonvml.BAR1Memory
	BBXTimeData_v1              = gonvml.BBXTimeData_v1
	C2cModeInfo_v1              = gonvml.C2cModeInfo_v1
	ClockType                   = gonvml.ClockType
	ComputeMode                 = gonvml.ComputeMode
	Device                      = gonvml.Device
	DeviceAddressingMode        = gonvml.DeviceAddressingMode
	DevicePowerMizerModes_v1    = gonvml.DevicePowerMizerModes_v1
	DramEncryptionInfo          = gonvml.DramEncryptionInfo
	DriverModel                 = gonvml.DriverModel
	EccCounterType              = gonvml.EccCounterType
	EccSramErrorStatus          = gonvml.EccSramErrorStatus
	EnableState                 = gonvml.EnableState
	EventData                   = gonvml.EventData
	EventSet                    = gonvml.EventSet
	FanControlPolicy            = gonvml.FanControlPolicy
	FieldValue                  = gonvml.FieldValue
	GpmMetric                   = gonvml.GpmMetric
	GpmMetricMetricInfo         = gonvml.GpmMetricMetricInfo
	GpmMetricsGetType           = gonvml.GpmMetricsGetType
	GpmSample                   = gonvml.GpmSample
	GpmSupport                  = gonvml.GpmSupport
	GpuFabricInfo_v2            = gonvml.GpuFabricInfo_v2
	GpuOperationMode            = gonvml.GpuOperationMode
	GpuVirtualizationMode       = gonvml.GpuVirtualizationMode
	InforomObject               = gonvml.InforomObject
	MarginTemperature           = gonvml.MarginTemperature
	Memory                      = gonvml.Memory
	Memory_v2                   = gonvml.Memory_v2
	MemoryErrorType             = gonvml.MemoryErrorType
	MemoryLocation              = gonvml.MemoryLocation
	PageRetirementCause         = gonvml.PageRetirementCause
	PcieUtilCounter             = gonvml.PcieUtilCounter
	PciInfo                     = gonvml.PciInfo
	PciInfoExt                  = gonvml.PciInfoExt
	PlatformInfo                = gonvml.PlatformInfo
	PowerSource                 = gonvml.PowerSource
	ProcessInfo                 = gonvml.ProcessInfo
	ProcessUtilizationInfo_v1   = gonvml.ProcessUtilizationInfo_v1
	ProcessUtilizationSample    = gonvml.ProcessUtilizationSample
	Pstates                     = gonvml.Pstates
	RemappedRowsInfo_v2         = gonvml.RemappedRowsInfo_v2
	RepairStatus                = gonvml.RepairStatus
	Return                      = gonvml.Return
	RowRemapperHistogramValues  = gonvml.RowRemapperHistogramValues
	TemperatureSensors          = gonvml.TemperatureSensors
	TemperatureThresholds       = gonvml.TemperatureThresholds
	UnrepairableMemoryStatus_v1 = gonvml.UnrepairableMemoryStatus_v1
	Utilization                 = gonvml.Utilization
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

	FI_DEV_C2C_LINK_COUNT                              = gonvml.FI_DEV_C2C_LINK_COUNT
	FI_DEV_C2C_LINK_GET_STATUS                         = gonvml.FI_DEV_C2C_LINK_GET_STATUS
	FI_DEV_CLOCKS_EVENT_REASON_HW_POWER_BRAKE_SLOWDOWN = gonvml.FI_DEV_CLOCKS_EVENT_REASON_HW_POWER_BRAKE_SLOWDOWN
	FI_DEV_CLOCKS_EVENT_REASON_HW_THERM_SLOWDOWN       = gonvml.FI_DEV_CLOCKS_EVENT_REASON_HW_THERM_SLOWDOWN
	FI_DEV_CLOCKS_EVENT_REASON_SW_POWER_CAP            = gonvml.FI_DEV_CLOCKS_EVENT_REASON_SW_POWER_CAP
	FI_DEV_CLOCKS_EVENT_REASON_SW_THERM_SLOWDOWN       = gonvml.FI_DEV_CLOCKS_EVENT_REASON_SW_THERM_SLOWDOWN
	FI_DEV_CLOCKS_EVENT_REASON_SYNC_BOOST              = gonvml.FI_DEV_CLOCKS_EVENT_REASON_SYNC_BOOST
	FI_DEV_DRAIN_AND_RESET_STATUS                      = gonvml.FI_DEV_DRAIN_AND_RESET_STATUS
	FI_DEV_GET_GPU_RECOVERY_ACTION                     = gonvml.FI_DEV_GET_GPU_RECOVERY_ACTION
	FI_DEV_MEMORY_TEMP                                 = gonvml.FI_DEV_MEMORY_TEMP
	FI_DEV_NVLINK_COUNT_LINK_RECOVERY_EVENTS           = gonvml.FI_DEV_NVLINK_COUNT_LINK_RECOVERY_EVENTS
	FI_DEV_NVLINK_COUNT_LINK_RECOVERY_FAILED_EVENTS    = gonvml.FI_DEV_NVLINK_COUNT_LINK_RECOVERY_FAILED_EVENTS
	FI_DEV_NVLINK_GET_STATE                            = gonvml.FI_DEV_NVLINK_GET_STATE
	FI_DEV_NVLINK_LINK_COUNT                           = gonvml.FI_DEV_NVLINK_LINK_COUNT
	FI_DEV_NVLINK_THROUGHPUT_DATA_RX                   = gonvml.FI_DEV_NVLINK_THROUGHPUT_DATA_RX
	FI_DEV_NVLINK_THROUGHPUT_DATA_TX                   = gonvml.FI_DEV_NVLINK_THROUGHPUT_DATA_TX
	FI_DEV_NVSWITCH_CONNECTED_LINK_COUNT               = gonvml.FI_DEV_NVSWITCH_CONNECTED_LINK_COUNT
	FI_DEV_PCIE_COUNT_CORRECTABLE_ERRORS               = gonvml.FI_DEV_PCIE_COUNT_CORRECTABLE_ERRORS
	FI_DEV_PCIE_COUNT_FATAL_ERROR                      = gonvml.FI_DEV_PCIE_COUNT_FATAL_ERROR
	FI_DEV_PCIE_COUNT_NON_FATAL_ERROR                  = gonvml.FI_DEV_PCIE_COUNT_NON_FATAL_ERROR
	FI_DEV_PCIE_L0_TO_RECOVERY_COUNTER                 = gonvml.FI_DEV_PCIE_L0_TO_RECOVERY_COUNTER
	FI_DEV_PCIE_REPLAY_COUNTER                         = gonvml.FI_DEV_PCIE_REPLAY_COUNTER
	FI_DEV_PCIE_REPLAY_ROLLOVER_COUNTER                = gonvml.FI_DEV_PCIE_REPLAY_ROLLOVER_COUNTER
	FI_DEV_PERF_POLICY_BOARD_LIMIT                     = gonvml.FI_DEV_PERF_POLICY_BOARD_LIMIT
	FI_DEV_PERF_POLICY_RELIABILITY                     = gonvml.FI_DEV_PERF_POLICY_RELIABILITY
	FI_DEV_PERF_POLICY_THERMAL                         = gonvml.FI_DEV_PERF_POLICY_THERMAL
	FI_DEV_PERF_POLICY_TOTAL_BASE_CLOCKS               = gonvml.FI_DEV_PERF_POLICY_TOTAL_BASE_CLOCKS
	FI_DEV_POWER_AVERAGE                               = gonvml.FI_DEV_POWER_AVERAGE
	FI_DEV_POWER_CURRENT_LIMIT                         = gonvml.FI_DEV_POWER_CURRENT_LIMIT
	FI_DEV_POWER_DEFAULT_LIMIT                         = gonvml.FI_DEV_POWER_DEFAULT_LIMIT
	FI_DEV_POWER_INSTANT                               = gonvml.FI_DEV_POWER_INSTANT
	FI_DEV_POWER_MAX_LIMIT                             = gonvml.FI_DEV_POWER_MAX_LIMIT
	FI_DEV_POWER_MIN_LIMIT                             = gonvml.FI_DEV_POWER_MIN_LIMIT
	FI_DEV_POWER_REQUESTED_LIMIT                       = gonvml.FI_DEV_POWER_REQUESTED_LIMIT
	FI_DEV_RESET_STATUS                                = gonvml.FI_DEV_RESET_STATUS
	FI_DEV_TEMPERATURE_GPU_MAX_TLIMIT                  = gonvml.FI_DEV_TEMPERATURE_GPU_MAX_TLIMIT
	FI_DEV_TEMPERATURE_MEM_MAX_TLIMIT                  = gonvml.FI_DEV_TEMPERATURE_MEM_MAX_TLIMIT
	FI_DEV_TEMPERATURE_SHUTDOWN_TLIMIT                 = gonvml.FI_DEV_TEMPERATURE_SHUTDOWN_TLIMIT
	FI_DEV_TEMPERATURE_SLOWDOWN_TLIMIT                 = gonvml.FI_DEV_TEMPERATURE_SLOWDOWN_TLIMIT

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
	// TEMPERATURE_GPU_MAX postdates go-nvml v0.13.3-1's nvml.h (checked against nvidia-smi 620.70.06).
	TEMPERATURE_GPU_MAX = uint32(1)

	TEMPERATURE_THRESHOLD_SHUTDOWN      = gonvml.TEMPERATURE_THRESHOLD_SHUTDOWN
	TEMPERATURE_THRESHOLD_SLOWDOWN      = gonvml.TEMPERATURE_THRESHOLD_SLOWDOWN
	TEMPERATURE_THRESHOLD_MEM_MAX       = gonvml.TEMPERATURE_THRESHOLD_MEM_MAX
	TEMPERATURE_THRESHOLD_GPU_MAX       = gonvml.TEMPERATURE_THRESHOLD_GPU_MAX
	TEMPERATURE_THRESHOLD_ACOUSTIC_CURR = gonvml.TEMPERATURE_THRESHOLD_ACOUSTIC_CURR

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
	GpmSampleGet               = gonvml.GpmSampleGet
	Init                       = gonvml.Init
	Shutdown                   = gonvml.Shutdown
	SystemGetCudaDriverVersion = gonvml.SystemGetCudaDriverVersion
	SystemGetDriverVersion     = gonvml.SystemGetDriverVersion
)

// systemProcessNameBufferSize is the buffer size passed to
// nvmlSystemGetProcessName. go-nvml's SYSTEM_PROCESS_NAME_BUFFER_SIZE (256)
// truncates long command lines, whereas 2048 bytes stays within Grafana
// Mimir's default 2048-character Prometheus label value limit.
const systemProcessNameBufferSize = 2048

// SystemGetProcessName returns the process name for the given PID.
func SystemGetProcessName(pid int) (string, Return) {
	buf := make([]byte, systemProcessNameBufferSize)
	ret := Return(C.nvmlSystemGetProcessName(
		C.uint(pid),
		(*C.char)(unsafe.Pointer(&buf[0])),
		C.uint(systemProcessNameBufferSize),
	))

	if end := bytes.IndexByte(buf, 0); end >= 0 {
		buf = buf[:end]
	}

	return string(buf), ret
}

func rawDeviceHandle(device Device) (unsafe.Pointer, bool) {
	val := reflect.ValueOf(device)
	if !val.IsValid() || val.Kind() != reflect.Struct {
		return nil, false
	}

	handle := val.FieldByName("Handle")
	if !handle.IsValid() || handle.Kind() != reflect.Pointer {
		return nil, false
	}

	return handle.UnsafePointer(), true
}

// DeviceGetGpuUptimeV1 returns the GPU uptime in seconds since the last GPU reset or load.
func DeviceGetGpuUptimeV1(device Device) (uint32, Return) {
	handle, ok := rawDeviceHandle(device)
	if !ok {
		return 0, ERROR_INVALID_ARGUMENT
	}

	res := C.exporterGetGpuUptimeV1(C.uintptr_t(uintptr(handle)))

	return uint32(res.value), Return(res.ret)
}

// DeviceGetTemperatureV reads a versioned temperature sensor value. go-nvml's
// TemperatureHandler.V1 hardcodes SensorType to 0, so non-zero sensor types
// require a direct call.
func DeviceGetTemperatureV(device Device, sensor uint32) (int32, Return) {
	handle, ok := rawDeviceHandle(device)
	if !ok {
		return 0, ERROR_INVALID_ARGUMENT
	}

	res := C.exporterGetTemperatureV(C.uintptr_t(uintptr(handle)), C.uint32_t(sensor))

	return int32(res.value), Return(res.ret)
}

// DeviceGetFanSpeedRPM reads a fan's speed in RPM. go-nvml's GetFanSpeedRPM
// leaves Fan at 0, so per-fan queries require a direct call.
func DeviceGetFanSpeedRPM(device Device, fan int) (uint32, Return) {
	handle, ok := rawDeviceHandle(device)
	if !ok || fan < 0 {
		return 0, ERROR_INVALID_ARGUMENT
	}

	res := C.exporterGetFanSpeedRPM(C.uintptr_t(uintptr(handle)), C.uint32_t(fan))

	return uint32(res.value), Return(res.ret)
}

// DeviceGetProcessesUtilizationInfo reads recent per-process utilization
// samples including JPEG and OFA engines. go-nvml's Device method calls the C
// entry point once with a nil buffer without allocating the returned count, so
// the two-step count-then-read protocol is driven directly here.
func DeviceGetProcessesUtilizationInfo(
	device Device,
	lastSeenTimestamp uint64,
) ([]ProcessUtilizationInfo_v1, Return) {
	handle, ok := rawDeviceHandle(device)
	if !ok {
		return nil, ERROR_INVALID_ARGUMENT
	}

	devPtr := C.uintptr_t(uintptr(handle))
	call := C.exporterCallProcessesUtilizationInfo(devPtr, C.uint64_t(lastSeenTimestamp), 0, 0)
	ret := Return(call.ret)

	if ret != ERROR_INSUFFICIENT_SIZE && ret != SUCCESS {
		return nil, ret
	}

	const (
		maxRetries = 4
		headroom   = 8
		maxSamples = 1 << 20
	)

	elemSize := C.size_t(unsafe.Sizeof(C.exporterNvmlProcessUtilizationInfoV1{}))

	for range maxRetries {
		if call.count == 0 {
			return nil, SUCCESS
		}

		if call.count > maxSamples {
			return nil, ERROR_MEMORY
		}

		alloc := call.count + headroom

		cBuf := C.calloc(C.size_t(alloc), elemSize)
		if cBuf == nil {
			return nil, ERROR_MEMORY
		}

		call = C.exporterCallProcessesUtilizationInfo(
			devPtr,
			C.uint64_t(lastSeenTimestamp),
			C.uintptr_t(uintptr(cBuf)),
			alloc,
		)
		ret = Return(call.ret)

		if ret == SUCCESS {
			written := min(int(call.count), int(alloc))
			raw := unsafe.Slice((*C.exporterNvmlProcessUtilizationInfoV1)(cBuf), written)
			out := convertProcessUtilizationSamples(raw)
			C.free(cBuf)

			return out, SUCCESS
		}

		C.free(cBuf)

		if ret != ERROR_INSUFFICIENT_SIZE {
			return nil, ret
		}
	}

	return nil, ret
}

func convertProcessUtilizationSamples(
	raw []C.exporterNvmlProcessUtilizationInfoV1,
) []ProcessUtilizationInfo_v1 {
	out := make([]ProcessUtilizationInfo_v1, len(raw))

	for idx, sample := range raw {
		out[idx] = ProcessUtilizationInfo_v1{
			TimeStamp: uint64(sample.timeStamp),
			Pid:       uint32(sample.pid),
			SmUtil:    uint32(sample.smUtil),
			MemUtil:   uint32(sample.memUtil),
			EncUtil:   uint32(sample.encUtil),
			DecUtil:   uint32(sample.decUtil),
			JpgUtil:   uint32(sample.jpgUtil),
			OfaUtil:   uint32(sample.ofaUtil),
		}
	}

	return out
}
