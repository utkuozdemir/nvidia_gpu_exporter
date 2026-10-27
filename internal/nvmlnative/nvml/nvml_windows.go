//go:build windows

// Package nvml provides the low-level NVML library bindings for Linux (via
// go-nvml) and Windows (via nvml.dll).
package nvml

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type (
	ClockType           int32
	ComputeMode         int32
	DriverModel         int32
	EccCounterType      int32
	EnableState         int32
	GpuOperationMode    int32
	InforomObject       int32
	MemoryErrorType     int32
	MemoryLocation      int32
	PageRetirementCause int32
	PcieUtilCounter     int32
	Pstates             int32
	Return              int32
	TemperatureSensors  int32
	ValueType           int32
)

const (
	SUCCESS                       Return = 0
	ERROR_UNINITIALIZED           Return = 1
	ERROR_INVALID_ARGUMENT        Return = 2
	ERROR_NOT_SUPPORTED           Return = 3
	ERROR_NO_PERMISSION           Return = 4
	ERROR_NOT_FOUND               Return = 6
	ERROR_INSUFFICIENT_SIZE       Return = 7
	ERROR_DRIVER_NOT_LOADED       Return = 9
	ERROR_TIMEOUT                 Return = 10
	ERROR_IRQ_ISSUE               Return = 11
	ERROR_LIBRARY_NOT_FOUND       Return = 12
	ERROR_FUNCTION_NOT_FOUND      Return = 13
	ERROR_CORRUPTED_INFOROM       Return = 14
	ERROR_GPU_IS_LOST             Return = 15
	ERROR_RESET_REQUIRED          Return = 16
	ERROR_OPERATING_SYSTEM        Return = 17
	ERROR_LIB_RM_VERSION_MISMATCH Return = 18
	ERROR_IN_USE                  Return = 19
	ERROR_MEMORY                  Return = 20
	ERROR_NO_DATA                 Return = 21
	ERROR_DEPRECATED              Return = 26
	ERROR_GPU_NOT_FOUND           Return = 28
	ERROR_UNKNOWN                 Return = 999
)

// String returns the string representation of a Return.
func (r Return) String() string {
	return r.Error()
}

// Error returns the string representation of a Return.
//
//nolint:cyclop,funlen // lookup table matching go-nvml's defaultErrorStringFunc
func (r Return) Error() string {
	switch r {
	case SUCCESS:
		return "SUCCESS"
	case ERROR_UNINITIALIZED:
		return "ERROR_UNINITIALIZED"
	case ERROR_INVALID_ARGUMENT:
		return "ERROR_INVALID_ARGUMENT"
	case ERROR_NOT_SUPPORTED:
		return "ERROR_NOT_SUPPORTED"
	case ERROR_NO_PERMISSION:
		return "ERROR_NO_PERMISSION"
	case ERROR_NOT_FOUND:
		return "ERROR_NOT_FOUND"
	case ERROR_INSUFFICIENT_SIZE:
		return "ERROR_INSUFFICIENT_SIZE"
	case ERROR_DRIVER_NOT_LOADED:
		return "ERROR_DRIVER_NOT_LOADED"
	case ERROR_TIMEOUT:
		return "ERROR_TIMEOUT"
	case ERROR_IRQ_ISSUE:
		return "ERROR_IRQ_ISSUE"
	case ERROR_LIBRARY_NOT_FOUND:
		return "ERROR_LIBRARY_NOT_FOUND"
	case ERROR_FUNCTION_NOT_FOUND:
		return "ERROR_FUNCTION_NOT_FOUND"
	case ERROR_CORRUPTED_INFOROM:
		return "ERROR_CORRUPTED_INFOROM"
	case ERROR_GPU_IS_LOST:
		return "ERROR_GPU_IS_LOST"
	case ERROR_RESET_REQUIRED:
		return "ERROR_RESET_REQUIRED"
	case ERROR_OPERATING_SYSTEM:
		return "ERROR_OPERATING_SYSTEM"
	case ERROR_LIB_RM_VERSION_MISMATCH:
		return "ERROR_LIB_RM_VERSION_MISMATCH"
	case ERROR_IN_USE:
		return "ERROR_IN_USE"
	case ERROR_MEMORY:
		return "ERROR_MEMORY"
	case ERROR_NO_DATA:
		return "ERROR_NO_DATA"
	case ERROR_DEPRECATED:
		return "ERROR_DEPRECATED"
	case ERROR_GPU_NOT_FOUND:
		return "ERROR_GPU_NOT_FOUND"
	case ERROR_UNKNOWN:
		return "ERROR_UNKNOWN"
	default:
		return fmt.Sprintf("unknown return value: %d", r)
	}
}

const (
	FEATURE_DISABLED EnableState = 0
	FEATURE_ENABLED  EnableState = 1

	CLOCK_GRAPHICS ClockType = 0
	CLOCK_SM       ClockType = 1
	CLOCK_MEM      ClockType = 2
	CLOCK_VIDEO    ClockType = 3

	TEMPERATURE_GPU TemperatureSensors = 0

	PCIE_UTIL_TX_BYTES PcieUtilCounter = 0
	PCIE_UTIL_RX_BYTES PcieUtilCounter = 1

	VOLATILE_ECC  EccCounterType = 0
	AGGREGATE_ECC EccCounterType = 1

	MEMORY_ERROR_TYPE_CORRECTED   MemoryErrorType = 0
	MEMORY_ERROR_TYPE_UNCORRECTED MemoryErrorType = 1

	MEMORY_LOCATION_L1_CACHE       MemoryLocation = 0
	MEMORY_LOCATION_L2_CACHE       MemoryLocation = 1
	MEMORY_LOCATION_DRAM           MemoryLocation = 2
	MEMORY_LOCATION_DEVICE_MEMORY  MemoryLocation = 2
	MEMORY_LOCATION_REGISTER_FILE  MemoryLocation = 3
	MEMORY_LOCATION_TEXTURE_MEMORY MemoryLocation = 4
	MEMORY_LOCATION_CBU            MemoryLocation = 6
	MEMORY_LOCATION_SRAM           MemoryLocation = 7

	PAGE_RETIREMENT_CAUSE_MULTIPLE_SINGLE_BIT_ECC_ERRORS PageRetirementCause = 0
	PAGE_RETIREMENT_CAUSE_DOUBLE_BIT_ECC_ERROR           PageRetirementCause = 1

	INFOROM_OEM   InforomObject = 0
	INFOROM_ECC   InforomObject = 1
	INFOROM_POWER InforomObject = 2

	VALUE_TYPE_DOUBLE             ValueType = 0
	VALUE_TYPE_UNSIGNED_INT       ValueType = 1
	VALUE_TYPE_UNSIGNED_LONG      ValueType = 2
	VALUE_TYPE_UNSIGNED_LONG_LONG ValueType = 3
	VALUE_TYPE_SIGNED_LONG_LONG   ValueType = 4
	VALUE_TYPE_SIGNED_INT         ValueType = 5

	DEVICE_MIG_DISABLE = 0
	DEVICE_MIG_ENABLE  = 1

	EventTypeXidCriticalError = 8

	FI_DEV_CLOCKS_EVENT_REASON_SW_POWER_CAP            = 74
	FI_DEV_CLOCKS_EVENT_REASON_SYNC_BOOST              = 76
	FI_DEV_MEMORY_TEMP                                 = 82
	FI_DEV_POWER_AVERAGE                               = 185
	FI_DEV_POWER_INSTANT                               = 186
	FI_DEV_GET_GPU_RECOVERY_ACTION                     = 230
	FI_DEV_CLOCKS_EVENT_REASON_SW_THERM_SLOWDOWN       = 269
	FI_DEV_CLOCKS_EVENT_REASON_HW_THERM_SLOWDOWN       = 270
	FI_DEV_CLOCKS_EVENT_REASON_HW_POWER_BRAKE_SLOWDOWN = 271

	deviceInforomVersionBufferSize = 16
	deviceNameV2BufferSize         = 96
	deviceSerialBufferSize         = 30
	deviceUUIDV2BufferSize         = 96
	deviceVbiosVersionBufferSize   = 32
	systemDriverVersionBufferSize  = 80
	systemProcessNameBufferSize    = 256

	gpmMetricsGetVersion = 1
	gpmSupportVersion    = 1
)

type DeviceAddressingMode struct {
	Version uint32
	Value   uint32
}

type C2cModeInfo_v1 struct {
	IsC2cEnabled uint32
}

type ProcessInfo struct {
	Pid               uint32
	UsedGpuMemory     uint64
	GpuInstanceId     uint32
	ComputeInstanceId uint32
}

type processInfoV1 struct {
	Pid           uint32
	UsedGpuMemory uint64
}

type Memory struct {
	Total uint64
	Free  uint64
	Used  uint64
}

type Memory_v2 struct {
	Version  uint32
	Total    uint64
	Reserved uint64
	Free     uint64
	Used     uint64
}

type DramEncryptionInfo struct {
	Version         uint32
	EncryptionState uint32
}

type FieldValue struct {
	FieldId     uint32
	ScopeId     uint32
	Timestamp   int64
	LatencyUsec int64
	ValueType   uint32
	NvmlReturn  uint32
	Value       [8]byte
}

type GpuFabricInfo_v2 struct {
	Version     uint32
	ClusterUuid [16]uint8
	Status      uint32
	CliqueId    uint32
	State       uint8
	HealthMask  uint32
}

type hostnameV1 struct {
	Value [64]int8
}

type MarginTemperature struct {
	Version           uint32
	MarginTemperature int32
}

type PciInfo struct {
	BusIdLegacy    [16]int8
	Domain         uint32
	Bus            uint32
	Device         uint32
	PciDeviceId    uint32
	PciSubSystemId uint32
	BusId          [32]int8
}

type PciInfoExt struct {
	Version        uint32
	Domain         uint32
	Bus            uint32
	Device         uint32
	PciDeviceId    uint32
	PciSubSystemId uint32
	BaseClass      uint32
	SubClass       uint32
	BusId          [32]int8
}

type PlatformInfo struct {
	Version             uint32
	IbGuid              [16]uint8
	ChassisSerialNumber [16]uint8
	SlotNumber          uint8
	TrayIndex           uint8
	HostId              uint8
	PeerType            uint8
	ModuleId            uint8
	Pad_cgo_0           [3]byte
}

type RemappedRowsInfo_v2 struct {
	CorrActiveRemaps   uint32
	CorrInactiveRemaps uint32
	UncActiveRemaps    uint32
	UncInactiveRemaps  uint32
	BPending           uint32
	BFailureOccurred   uint32
}

type RowRemapperHistogramValues struct {
	Max     uint32
	High    uint32
	Partial uint32
	Low     uint32
	None    uint32
}

type EccSramErrorStatus struct {
	Version                 uint32
	AggregateUncParity      uint64
	AggregateUncSecDed      uint64
	AggregateCor            uint64
	VolatileUncParity       uint64
	VolatileUncSecDed       uint64
	VolatileCor             uint64
	AggregateUncBucketL2    uint64
	AggregateUncBucketSm    uint64
	AggregateUncBucketPcie  uint64
	AggregateUncBucketMcu   uint64
	AggregateUncBucketOther uint64
	BThresholdExceeded      uint32
	Pad_cgo_0               [4]byte
}

type Utilization struct {
	Gpu    uint32
	Memory uint32
}

type GpmSupport struct {
	Version           uint32
	IsSupportedDevice uint32
}

type GpmMetricMetricInfo struct {
	ShortName *int8
	LongName  *int8
	Unit      *int8
}

type GpmMetric struct {
	MetricId   uint32
	NvmlReturn uint32
	Value      float64
	MetricInfo GpmMetricMetricInfo
}

type GpmMetricsGetType struct {
	Version    uint32
	NumMetrics uint32
	Sample1    GpmSample
	Sample2    GpmSample
	Metrics    [333]GpmMetric
}

type rawGpmMetricsGet struct {
	Version    uint32
	NumMetrics uint32
	Sample1    uintptr
	Sample2    uintptr
	Metrics    [333]GpmMetric
}

type EventData struct {
	Device            Device
	EventType         uint64
	EventData         uint64
	GpuInstanceId     uint32
	ComputeInstanceId uint32
}

type rawEventData struct {
	Device            uintptr
	EventType         uint64
	EventData         uint64
	GpuInstanceId     uint32
	ComputeInstanceId uint32
}

// Compile-time checks pinning each ABI struct to its 64-bit C size in nvml.h.
// An unsigned subtraction in each direction overflows if the size drifts
// either up or down.
const (
	_ = uint(8-unsafe.Sizeof(DeviceAddressingMode{})) + uint(unsafe.Sizeof(DeviceAddressingMode{})-8)
	_ = uint(4-unsafe.Sizeof(C2cModeInfo_v1{})) + uint(unsafe.Sizeof(C2cModeInfo_v1{})-4)
	_ = uint(24-unsafe.Sizeof(ProcessInfo{})) + uint(unsafe.Sizeof(ProcessInfo{})-24)
	_ = uint(16-unsafe.Sizeof(processInfoV1{})) + uint(unsafe.Sizeof(processInfoV1{})-16)
	_ = uint(24-unsafe.Sizeof(Memory{})) + uint(unsafe.Sizeof(Memory{})-24)
	_ = uint(40-unsafe.Sizeof(Memory_v2{})) + uint(unsafe.Sizeof(Memory_v2{})-40)
	_ = uint(8-unsafe.Sizeof(DramEncryptionInfo{})) + uint(unsafe.Sizeof(DramEncryptionInfo{})-8)
	_ = uint(40-unsafe.Sizeof(FieldValue{})) + uint(unsafe.Sizeof(FieldValue{})-40)
	_ = uint(36-unsafe.Sizeof(GpuFabricInfo_v2{})) + uint(unsafe.Sizeof(GpuFabricInfo_v2{})-36)
	_ = uint(64-unsafe.Sizeof(hostnameV1{})) + uint(unsafe.Sizeof(hostnameV1{})-64)
	_ = uint(8-unsafe.Sizeof(MarginTemperature{})) + uint(unsafe.Sizeof(MarginTemperature{})-8)
	_ = uint(68-unsafe.Sizeof(PciInfo{})) + uint(unsafe.Sizeof(PciInfo{})-68)
	_ = uint(64-unsafe.Sizeof(PciInfoExt{})) + uint(unsafe.Sizeof(PciInfoExt{})-64)
	_ = uint(44-unsafe.Sizeof(PlatformInfo{})) + uint(unsafe.Sizeof(PlatformInfo{})-44)
	_ = uint(24-unsafe.Sizeof(RemappedRowsInfo_v2{})) + uint(unsafe.Sizeof(RemappedRowsInfo_v2{})-24)
	_ = uint(20-unsafe.Sizeof(RowRemapperHistogramValues{})) + uint(unsafe.Sizeof(RowRemapperHistogramValues{})-20)
	_ = uint(104-unsafe.Sizeof(EccSramErrorStatus{})) + uint(unsafe.Sizeof(EccSramErrorStatus{})-104)
	_ = uint(8-unsafe.Sizeof(Utilization{})) + uint(unsafe.Sizeof(Utilization{})-8)
	_ = uint(8-unsafe.Sizeof(GpmSupport{})) + uint(unsafe.Sizeof(GpmSupport{})-8)
	_ = uint(40-unsafe.Sizeof(GpmMetric{})) + uint(unsafe.Sizeof(GpmMetric{})-40)
	_ = uint(13344-unsafe.Sizeof(rawGpmMetricsGet{})) + uint(unsafe.Sizeof(rawGpmMetricsGet{})-13344)
	_ = uint(32-unsafe.Sizeof(rawEventData{})) + uint(unsafe.Sizeof(rawEventData{})-32)
)

// GpmSample represents an NVML GPM activity sample handle.
type GpmSample interface {
	Free() Return
}

// EventSet represents an NVML event set handle.
type EventSet interface {
	Free() Return
	Wait(timeoutms uint32) (EventData, Return)
}

// C2cModeInfoHandler selects the struct version for nvmlDeviceGetC2cModeInfoV.
type C2cModeInfoHandler struct {
	device winDevice
}

// V1 queries version 1 of the C2C mode info struct.
func (h C2cModeInfoHandler) V1() (C2cModeInfo_v1, Return) {
	var info C2cModeInfo_v1

	proc, ok := findProc("nvmlDeviceGetC2cModeInfoV")
	if !ok {
		return info, ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(h.device.handle, uintptr(unsafe.Pointer(&info)))

	return info, toReturn(r1)
}

// GpuFabricInfoHandler selects the struct version for nvmlDeviceGetGpuFabricInfoV.
type GpuFabricInfoHandler struct {
	device winDevice
}

// V2 queries version 2 of the GPU fabric info struct.
func (h GpuFabricInfoHandler) V2() (GpuFabricInfo_v2, Return) {
	var info GpuFabricInfo_v2

	info.Version = structVersion[GpuFabricInfo_v2](2)

	proc, ok := findProc("nvmlDeviceGetGpuFabricInfoV")
	if !ok {
		return info, ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(h.device.handle, uintptr(unsafe.Pointer(&info)))

	return info, toReturn(r1)
}

// Device represents an NVML device handle.
//
//nolint:interfacebloat // mirrors the go-nvml Device methods used by nvmlnative
type Device interface {
	GetAccountingBufferSize() (int, Return)
	GetAccountingMode() (EnableState, Return)
	GetAddressingMode() (DeviceAddressingMode, Return)
	GetC2cModeInfoV() C2cModeInfoHandler
	GetClockInfo(clockType ClockType) (uint32, Return)
	GetComputeInstanceId() (int, Return)
	GetComputeMode() (ComputeMode, Return)
	GetComputeRunningProcesses() ([]ProcessInfo, Return)
	GetConfComputeProtectedMemoryUsage() (Memory, Return)
	GetCudaComputeCapability() (int, int, Return)
	GetCurrentClocksEventReasons() (uint64, Return)
	GetCurrentClocksThrottleReasons() (uint64, Return)
	GetCurrPcieLinkGeneration() (int, Return)
	GetCurrPcieLinkWidth() (int, Return)
	GetDecoderUtilization() (uint32, uint32, Return)
	GetDisplayActive() (EnableState, Return)
	GetDisplayMode() (EnableState, Return)
	GetDramEncryptionMode() (DramEncryptionInfo, DramEncryptionInfo, Return)
	GetDriverModel() (DriverModel, DriverModel, Return)
	GetEccMode() (EnableState, EnableState, Return)
	GetEncoderStats() (int, uint32, uint32, Return)
	GetEncoderUtilization() (uint32, uint32, Return)
	GetEnforcedPowerLimit() (uint32, Return)
	GetFanSpeed() (uint32, Return)
	GetFieldValues(values []FieldValue) Return
	GetGpuFabricInfoV() GpuFabricInfoHandler
	GetGpuInstanceId() (int, Return)
	GetGpuMaxPcieLinkGeneration() (int, Return)
	GetGpuOperationMode() (GpuOperationMode, GpuOperationMode, Return)
	GetGraphicsRunningProcesses() ([]ProcessInfo, Return)
	GetGspFirmwareMode() (bool, bool, Return)
	GetHostname_v1() (string, Return)
	GetIndex() (int, Return)
	GetInforomImageVersion() (string, Return)
	GetInforomVersion(object InforomObject) (string, Return)
	GetJpgUtilization() (uint32, uint32, Return)
	GetMarginTemperature() (MarginTemperature, Return)
	GetMaxClockInfo(clockType ClockType) (uint32, Return)
	GetMaxMigDeviceCount() (int, Return)
	GetMaxPcieLinkGeneration() (int, Return)
	GetMaxPcieLinkWidth() (int, Return)
	GetMemoryErrorCounter(
		errorType MemoryErrorType,
		counterType EccCounterType,
		location MemoryLocation,
	) (uint64, Return)
	GetMemoryInfo() (Memory, Return)
	GetMemoryInfo_v2() (Memory_v2, Return)
	GetMigDeviceHandleByIndex(index int) (Device, Return)
	GetMigMode() (int, int, Return)
	GetName() (string, Return)
	GetOfaUtilization() (uint32, uint32, Return)
	GetPcieThroughput(counter PcieUtilCounter) (uint32, Return)
	GetPciInfo() (PciInfo, Return)
	GetPciInfoExt() (PciInfoExt, Return)
	GetPerformanceState() (Pstates, Return)
	GetPersistenceMode() (EnableState, Return)
	GetPlatformInfo() (PlatformInfo, Return)
	GetPowerManagementDefaultLimit() (uint32, Return)
	GetPowerManagementLimit() (uint32, Return)
	GetPowerManagementLimitConstraints() (uint32, uint32, Return)
	GetPowerUsage() (uint32, Return)
	GetRemappedRows() (int, int, bool, bool, Return)
	GetRemappedRows_v2() (RemappedRowsInfo_v2, Return)
	GetRetiredPages(cause PageRetirementCause) ([]uint64, Return)
	GetRetiredPagesPendingStatus() (EnableState, Return)
	GetRowRemapperHistogram() (RowRemapperHistogramValues, Return)
	GetSerial() (string, Return)
	GetSramEccErrorStatus() (EccSramErrorStatus, Return)
	GetSupportedClocksEventReasons() (uint64, Return)
	GetSupportedClocksThrottleReasons() (uint64, Return)
	GetTemperature(sensor TemperatureSensors) (uint32, Return)
	GetTotalEccErrors(errorType MemoryErrorType, counterType EccCounterType) (uint64, Return)
	GetTotalEnergyConsumption() (uint64, Return)
	GetUtilizationRates() (Utilization, Return)
	GetUUID() (string, Return)
	GetVbiosVersion() (string, Return)
	GpmQueryDeviceSupport() (GpmSupport, Return)
	RegisterEvents(eventTypes uint64, set EventSet) Return
}

//nolint:gochecknoglobals // process-wide nvml.dll handle
var dll = windows.NewLazySystemDLL("nvml.dll")

func toReturn(r1 uintptr) Return {
	return Return(r1) //nolint:gosec // NVML return codes fit in int32
}

func findProc(name string) (*windows.LazyProc, bool) {
	p := dll.NewProc(name)

	return p, p.Find() == nil
}

func findFirstProc(names ...string) (*windows.LazyProc, bool) {
	for _, name := range names {
		if p, ok := findProc(name); ok {
			return p, true
		}
	}

	return nil, false
}

// ExtendedInterface exposes symbol lookup on the loaded NVML library.
type ExtendedInterface struct{}

// Extensions returns the extended interface for symbol probing.
func Extensions() ExtendedInterface {
	return ExtendedInterface{}
}

// LookupSymbol reports whether nvml.dll exports the named entry point.
func (ExtendedInterface) LookupSymbol(name string) error {
	if err := dll.NewProc(name).Find(); err != nil {
		return fmt.Errorf("error looking up %s: %w", name, err)
	}

	return nil
}

// Init loads nvml.dll from System32 and initializes NVML.
func Init() Return {
	if err := dll.Load(); err != nil {
		return ERROR_LIBRARY_NOT_FOUND
	}

	proc, ok := findFirstProc("nvmlInit_v2", "nvmlInit")
	if !ok {
		return ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call()

	return toReturn(r1)
}

// Shutdown shuts down NVML.
func Shutdown() Return {
	proc, ok := findProc("nvmlShutdown")
	if !ok {
		return ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call()

	return toReturn(r1)
}

// DeviceGetCount returns the number of compute devices in the system.
func DeviceGetCount() (int, Return) {
	proc, ok := findFirstProc("nvmlDeviceGetCount_v2", "nvmlDeviceGetCount")
	if !ok {
		return 0, ERROR_FUNCTION_NOT_FOUND
	}

	var count uint32

	r1, _, _ := proc.Call(uintptr(unsafe.Pointer(&count)))

	return int(count), toReturn(r1)
}

// DeviceGetHandleByIndex returns the handle for a device by index.
//
//nolint:ireturn // matches go-nvml signature
func DeviceGetHandleByIndex(index int) (Device, Return) {
	proc, ok := findFirstProc("nvmlDeviceGetHandleByIndex_v2", "nvmlDeviceGetHandleByIndex")
	if !ok {
		return nil, ERROR_FUNCTION_NOT_FOUND
	}

	var handle uintptr

	r1, _, _ := proc.Call(uintptr(index), uintptr(unsafe.Pointer(&handle)))

	return winDevice{handle: handle}, toReturn(r1)
}

// SystemGetDriverVersion returns the installed NVIDIA driver version string.
func SystemGetDriverVersion() (string, Return) {
	proc, ok := findProc("nvmlSystemGetDriverVersion")
	if !ok {
		return "", ERROR_FUNCTION_NOT_FOUND
	}

	buf := make([]byte, systemDriverVersionBufferSize)
	r1, _, _ := proc.Call(uintptr(unsafe.Pointer(&buf[0])), systemDriverVersionBufferSize)

	return cString(buf), toReturn(r1)
}

// SystemGetCudaDriverVersion returns the CUDA driver version supported by the driver.
func SystemGetCudaDriverVersion() (int, Return) {
	proc, ok := findProc("nvmlSystemGetCudaDriverVersion")
	if !ok {
		return 0, ERROR_FUNCTION_NOT_FOUND
	}

	var cudaVersion int32

	r1, _, _ := proc.Call(uintptr(unsafe.Pointer(&cudaVersion)))

	return int(cudaVersion), toReturn(r1)
}

// SystemGetProcessName returns the process name for the given PID.
func SystemGetProcessName(pid int) (string, Return) {
	proc, ok := findProc("nvmlSystemGetProcessName")
	if !ok {
		return "", ERROR_FUNCTION_NOT_FOUND
	}

	buf := make([]byte, systemProcessNameBufferSize)
	r1, _, _ := proc.Call(uintptr(pid), uintptr(unsafe.Pointer(&buf[0])), systemProcessNameBufferSize)

	return cString(buf), toReturn(r1)
}

// DeviceValidateInforom validates the device's InfoROM checksums.
func DeviceValidateInforom(device Device) Return {
	dev, ok := device.(winDevice)
	if !ok {
		return ERROR_INVALID_ARGUMENT
	}

	proc, ok := findProc("nvmlDeviceValidateInforom")
	if !ok {
		return ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(dev.handle)

	return toReturn(r1)
}

type winGpmSample struct {
	handle uintptr
}

func (s winGpmSample) Free() Return {
	proc, ok := findProc("nvmlGpmSampleFree")
	if !ok {
		return ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(s.handle)

	return toReturn(r1)
}

// GpmSampleAlloc allocates a GPM sample handle.
//
//nolint:ireturn // matches go-nvml signature
func GpmSampleAlloc() (GpmSample, Return) {
	proc, ok := findProc("nvmlGpmSampleAlloc")
	if !ok {
		return nil, ERROR_FUNCTION_NOT_FOUND
	}

	var handle uintptr

	r1, _, _ := proc.Call(uintptr(unsafe.Pointer(&handle)))

	return winGpmSample{handle: handle}, toReturn(r1)
}

// GpmSampleFree frees a GPM sample handle.
func GpmSampleFree(sample GpmSample) Return {
	return sample.Free()
}

// GpmMigSampleGet reads a MIG GPU instance GPM sample.
func GpmMigSampleGet(device Device, gpuInstanceID int, sample GpmSample) Return {
	dev, ok := device.(winDevice)
	if !ok {
		return ERROR_INVALID_ARGUMENT
	}

	smp, ok := sample.(winGpmSample)
	if !ok {
		return ERROR_INVALID_ARGUMENT
	}

	proc, ok := findProc("nvmlGpmMigSampleGet")
	if !ok {
		return ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(dev.handle, uintptr(gpuInstanceID), smp.handle)

	return toReturn(r1)
}

// GpmMetricsGet computes GPM metrics between two samples.
func GpmMetricsGet(metricsGet *GpmMetricsGetType) Return {
	s1, ok := metricsGet.Sample1.(winGpmSample)
	if !ok {
		return ERROR_INVALID_ARGUMENT
	}

	s2, ok := metricsGet.Sample2.(winGpmSample)
	if !ok {
		return ERROR_INVALID_ARGUMENT
	}

	proc, ok := findProc("nvmlGpmMetricsGet")
	if !ok {
		return ERROR_FUNCTION_NOT_FOUND
	}

	raw := rawGpmMetricsGet{
		Version:    gpmMetricsGetVersion,
		NumMetrics: metricsGet.NumMetrics,
		Sample1:    s1.handle,
		Sample2:    s2.handle,
		Metrics:    metricsGet.Metrics,
	}

	r1, _, _ := proc.Call(uintptr(unsafe.Pointer(&raw)))
	metricsGet.Version = raw.Version
	metricsGet.Metrics = raw.Metrics

	return toReturn(r1)
}

type winEventSet struct {
	handle uintptr
}

func (s winEventSet) Free() Return {
	proc, ok := findProc("nvmlEventSetFree")
	if !ok {
		return ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(s.handle)

	return toReturn(r1)
}

func (s winEventSet) Wait(timeoutms uint32) (EventData, Return) {
	proc, ok := findFirstProc("nvmlEventSetWait_v2", "nvmlEventSetWait")
	if !ok {
		return EventData{}, ERROR_FUNCTION_NOT_FOUND
	}

	var raw rawEventData

	r1, _, _ := proc.Call(s.handle, uintptr(unsafe.Pointer(&raw)), uintptr(timeoutms))

	return EventData{
		Device:            winDevice{handle: raw.Device},
		EventType:         raw.EventType,
		EventData:         raw.EventData,
		GpuInstanceId:     raw.GpuInstanceId,
		ComputeInstanceId: raw.ComputeInstanceId,
	}, toReturn(r1)
}

// EventSetCreate creates a new event set handle.
//
//nolint:ireturn // matches go-nvml signature
func EventSetCreate() (EventSet, Return) {
	proc, ok := findProc("nvmlEventSetCreate")
	if !ok {
		return nil, ERROR_FUNCTION_NOT_FOUND
	}

	var handle uintptr

	r1, _, _ := proc.Call(uintptr(unsafe.Pointer(&handle)))

	return winEventSet{handle: handle}, toReturn(r1)
}

type winDevice struct {
	handle uintptr
}

func toEnableState(val uint32) EnableState {
	return EnableState(val) //nolint:gosec // NVML enable state values fit in int32
}

func (d winDevice) GetAccountingBufferSize() (int, Return) {
	val, ret := d.getUint32("nvmlDeviceGetAccountingBufferSize")

	return int(val), ret
}

func (d winDevice) GetAccountingMode() (EnableState, Return) {
	val, ret := d.getUint32("nvmlDeviceGetAccountingMode")

	return toEnableState(val), ret
}

func (d winDevice) GetAddressingMode() (DeviceAddressingMode, Return) {
	var mode DeviceAddressingMode

	mode.Version = structVersion[DeviceAddressingMode](1)

	proc, ok := findProc("nvmlDeviceGetAddressingMode")
	if !ok {
		return mode, ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&mode)))

	return mode, toReturn(r1)
}

func (d winDevice) GetC2cModeInfoV() C2cModeInfoHandler {
	return C2cModeInfoHandler{device: d}
}

func (d winDevice) GetClockInfo(clockType ClockType) (uint32, Return) {
	proc, ok := findProc("nvmlDeviceGetClockInfo")
	if !ok {
		return 0, ERROR_FUNCTION_NOT_FOUND
	}

	var clock uint32

	r1, _, _ := proc.Call(d.handle, uintptr(uint32(clockType)), uintptr(unsafe.Pointer(&clock))) //nolint:gosec // enum

	return clock, toReturn(r1)
}

func (d winDevice) GetComputeInstanceId() (int, Return) {
	val, ret := d.getUint32("nvmlDeviceGetComputeInstanceId")

	return int(val), ret
}

func (d winDevice) GetComputeMode() (ComputeMode, Return) {
	val, ret := d.getUint32("nvmlDeviceGetComputeMode")

	return ComputeMode(val), ret //nolint:gosec // enum
}

func (d winDevice) GetComputeRunningProcesses() ([]ProcessInfo, Return) {
	return d.getRunningProcesses(
		"nvmlDeviceGetComputeRunningProcesses_v3",
		"nvmlDeviceGetComputeRunningProcesses_v2",
		"nvmlDeviceGetComputeRunningProcesses",
	)
}

func (d winDevice) GetConfComputeProtectedMemoryUsage() (Memory, Return) {
	var mem Memory

	proc, ok := findProc("nvmlDeviceGetConfComputeProtectedMemoryUsage")
	if !ok {
		return mem, ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&mem)))

	return mem, toReturn(r1)
}

func (d winDevice) GetCudaComputeCapability() (int, int, Return) {
	proc, ok := findProc("nvmlDeviceGetCudaComputeCapability")
	if !ok {
		return 0, 0, ERROR_FUNCTION_NOT_FOUND
	}

	var major, minor int32

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&major)), uintptr(unsafe.Pointer(&minor)))

	return int(major), int(minor), toReturn(r1)
}

func (d winDevice) GetCurrentClocksEventReasons() (uint64, Return) {
	return d.getUint64("nvmlDeviceGetCurrentClocksEventReasons")
}

func (d winDevice) GetCurrentClocksThrottleReasons() (uint64, Return) {
	return d.getUint64("nvmlDeviceGetCurrentClocksThrottleReasons")
}

func (d winDevice) GetCurrPcieLinkGeneration() (int, Return) {
	val, ret := d.getUint32("nvmlDeviceGetCurrPcieLinkGeneration")

	return int(val), ret
}

func (d winDevice) GetCurrPcieLinkWidth() (int, Return) {
	val, ret := d.getUint32("nvmlDeviceGetCurrPcieLinkWidth")

	return int(val), ret
}

func (d winDevice) GetDecoderUtilization() (uint32, uint32, Return) {
	return d.getTwoUint32("nvmlDeviceGetDecoderUtilization")
}

func (d winDevice) GetDisplayActive() (EnableState, Return) {
	val, ret := d.getUint32("nvmlDeviceGetDisplayActive")

	return toEnableState(val), ret
}

func (d winDevice) GetDisplayMode() (EnableState, Return) {
	val, ret := d.getUint32("nvmlDeviceGetDisplayMode")

	return toEnableState(val), ret
}

func (d winDevice) GetDramEncryptionMode() (DramEncryptionInfo, DramEncryptionInfo, Return) {
	var current, pending DramEncryptionInfo

	current.Version = structVersion[DramEncryptionInfo](1)
	pending.Version = structVersion[DramEncryptionInfo](1)

	proc, ok := findProc("nvmlDeviceGetDramEncryptionMode")
	if !ok {
		return current, pending, ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&current)), uintptr(unsafe.Pointer(&pending)))

	return current, pending, toReturn(r1)
}

func (d winDevice) GetDriverModel() (DriverModel, DriverModel, Return) {
	proc, ok := findFirstProc("nvmlDeviceGetDriverModel_v2", "nvmlDeviceGetDriverModel")
	if !ok {
		return 0, 0, ERROR_FUNCTION_NOT_FOUND
	}

	var current, pending DriverModel

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&current)), uintptr(unsafe.Pointer(&pending)))

	return current, pending, toReturn(r1)
}

func (d winDevice) GetEccMode() (EnableState, EnableState, Return) {
	a, b, ret := d.getTwoUint32("nvmlDeviceGetEccMode")

	return toEnableState(a), toEnableState(b), ret
}

func (d winDevice) GetEncoderStats() (int, uint32, uint32, Return) {
	proc, ok := findProc("nvmlDeviceGetEncoderStats")
	if !ok {
		return 0, 0, 0, ERROR_FUNCTION_NOT_FOUND
	}

	var sessionCount, averageFps, averageLatency uint32

	r1, _, _ := proc.Call(
		d.handle,
		uintptr(unsafe.Pointer(&sessionCount)),
		uintptr(unsafe.Pointer(&averageFps)),
		uintptr(unsafe.Pointer(&averageLatency)),
	)

	return int(sessionCount), averageFps, averageLatency, toReturn(r1)
}

func (d winDevice) GetEncoderUtilization() (uint32, uint32, Return) {
	return d.getTwoUint32("nvmlDeviceGetEncoderUtilization")
}

func (d winDevice) GetEnforcedPowerLimit() (uint32, Return) {
	return d.getUint32("nvmlDeviceGetEnforcedPowerLimit")
}

func (d winDevice) GetFanSpeed() (uint32, Return) {
	return d.getUint32("nvmlDeviceGetFanSpeed")
}

func (d winDevice) GetFieldValues(values []FieldValue) Return {
	if len(values) == 0 {
		return SUCCESS
	}

	proc, ok := findProc("nvmlDeviceGetFieldValues")
	if !ok {
		return ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(d.handle, uintptr(len(values)), uintptr(unsafe.Pointer(&values[0])))

	return toReturn(r1)
}

func (d winDevice) GetGpuFabricInfoV() GpuFabricInfoHandler {
	return GpuFabricInfoHandler{device: d}
}

func (d winDevice) GetGpuInstanceId() (int, Return) {
	val, ret := d.getUint32("nvmlDeviceGetGpuInstanceId")

	return int(val), ret
}

func (d winDevice) GetGpuMaxPcieLinkGeneration() (int, Return) {
	val, ret := d.getUint32("nvmlDeviceGetGpuMaxPcieLinkGeneration")

	return int(val), ret
}

func (d winDevice) GetGpuOperationMode() (GpuOperationMode, GpuOperationMode, Return) {
	a, b, ret := d.getTwoUint32("nvmlDeviceGetGpuOperationMode")

	return GpuOperationMode(a), GpuOperationMode(b), ret //nolint:gosec // enum
}

func (d winDevice) GetGraphicsRunningProcesses() ([]ProcessInfo, Return) {
	return d.getRunningProcesses(
		"nvmlDeviceGetGraphicsRunningProcesses_v3",
		"nvmlDeviceGetGraphicsRunningProcesses_v2",
		"nvmlDeviceGetGraphicsRunningProcesses",
	)
}

func (d winDevice) GetGspFirmwareMode() (bool, bool, Return) {
	isEnabled, defaultMode, ret := d.getTwoUint32("nvmlDeviceGetGspFirmwareMode")

	return isEnabled != 0, defaultMode != 0, ret
}

func (d winDevice) GetHostname_v1() (string, Return) {
	proc, ok := findProc("nvmlDeviceGetHostname_v1")
	if !ok {
		return "", ERROR_FUNCTION_NOT_FOUND
	}

	var host hostnameV1

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&host)))

	return int8SliceToString(host.Value[:]), toReturn(r1)
}

func (d winDevice) GetIndex() (int, Return) {
	val, ret := d.getUint32("nvmlDeviceGetIndex")

	return int(val), ret
}

func (d winDevice) GetInforomImageVersion() (string, Return) {
	return d.getString("nvmlDeviceGetInforomImageVersion", deviceInforomVersionBufferSize)
}

func (d winDevice) GetInforomVersion(object InforomObject) (string, Return) {
	proc, ok := findProc("nvmlDeviceGetInforomVersion")
	if !ok {
		return "", ERROR_FUNCTION_NOT_FOUND
	}

	buf := make([]byte, deviceInforomVersionBufferSize)
	//nolint:gosec // enum
	r1, _, _ := proc.Call(
		d.handle,
		uintptr(uint32(object)),
		uintptr(unsafe.Pointer(&buf[0])),
		deviceInforomVersionBufferSize,
	)

	return cString(buf), toReturn(r1)
}

func (d winDevice) GetJpgUtilization() (uint32, uint32, Return) {
	return d.getTwoUint32("nvmlDeviceGetJpgUtilization")
}

func (d winDevice) GetMarginTemperature() (MarginTemperature, Return) {
	var info MarginTemperature

	info.Version = structVersion[MarginTemperature](1)

	proc, ok := findProc("nvmlDeviceGetMarginTemperature")
	if !ok {
		return info, ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&info)))

	return info, toReturn(r1)
}

func (d winDevice) GetMaxClockInfo(clockType ClockType) (uint32, Return) {
	proc, ok := findProc("nvmlDeviceGetMaxClockInfo")
	if !ok {
		return 0, ERROR_FUNCTION_NOT_FOUND
	}

	var clock uint32

	r1, _, _ := proc.Call(d.handle, uintptr(uint32(clockType)), uintptr(unsafe.Pointer(&clock))) //nolint:gosec // enum

	return clock, toReturn(r1)
}

func (d winDevice) GetMaxMigDeviceCount() (int, Return) {
	val, ret := d.getUint32("nvmlDeviceGetMaxMigDeviceCount")

	return int(val), ret
}

func (d winDevice) GetMaxPcieLinkGeneration() (int, Return) {
	val, ret := d.getUint32("nvmlDeviceGetMaxPcieLinkGeneration")

	return int(val), ret
}

func (d winDevice) GetMaxPcieLinkWidth() (int, Return) {
	val, ret := d.getUint32("nvmlDeviceGetMaxPcieLinkWidth")

	return int(val), ret
}

func (d winDevice) GetMemoryErrorCounter(
	errorType MemoryErrorType,
	counterType EccCounterType,
	location MemoryLocation,
) (uint64, Return) {
	proc, ok := findProc("nvmlDeviceGetMemoryErrorCounter")
	if !ok {
		return 0, ERROR_FUNCTION_NOT_FOUND
	}

	var count uint64

	r1, _, _ := proc.Call(
		d.handle,
		uintptr(uint32(errorType)),   //nolint:gosec // enum
		uintptr(uint32(counterType)), //nolint:gosec // enum
		uintptr(uint32(location)),    //nolint:gosec // enum
		uintptr(unsafe.Pointer(&count)),
	)

	return count, toReturn(r1)
}

func (d winDevice) GetMemoryInfo() (Memory, Return) {
	var mem Memory

	proc, ok := findProc("nvmlDeviceGetMemoryInfo")
	if !ok {
		return mem, ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&mem)))

	return mem, toReturn(r1)
}

func (d winDevice) GetMemoryInfo_v2() (Memory_v2, Return) {
	var mem Memory_v2

	mem.Version = structVersion[Memory_v2](2)

	proc, ok := findProc("nvmlDeviceGetMemoryInfo_v2")
	if !ok {
		return mem, ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&mem)))

	return mem, toReturn(r1)
}

//nolint:ireturn // matches go-nvml Device interface
func (d winDevice) GetMigDeviceHandleByIndex(index int) (Device, Return) {
	proc, ok := findProc("nvmlDeviceGetMigDeviceHandleByIndex")
	if !ok {
		return nil, ERROR_FUNCTION_NOT_FOUND
	}

	var mig uintptr

	r1, _, _ := proc.Call(d.handle, uintptr(index), uintptr(unsafe.Pointer(&mig)))

	return winDevice{handle: mig}, toReturn(r1)
}

func (d winDevice) GetMigMode() (int, int, Return) {
	current, pending, ret := d.getTwoUint32("nvmlDeviceGetMigMode")

	return int(current), int(pending), ret
}

func (d winDevice) GetName() (string, Return) {
	return d.getString("nvmlDeviceGetName", deviceNameV2BufferSize)
}

func (d winDevice) GetOfaUtilization() (uint32, uint32, Return) {
	return d.getTwoUint32("nvmlDeviceGetOfaUtilization")
}

func (d winDevice) GetPcieThroughput(counter PcieUtilCounter) (uint32, Return) {
	proc, ok := findProc("nvmlDeviceGetPcieThroughput")
	if !ok {
		return 0, ERROR_FUNCTION_NOT_FOUND
	}

	var val uint32

	r1, _, _ := proc.Call(d.handle, uintptr(uint32(counter)), uintptr(unsafe.Pointer(&val))) //nolint:gosec // enum

	return val, toReturn(r1)
}

func (d winDevice) GetPciInfo() (PciInfo, Return) {
	var info PciInfo

	proc, ok := findFirstProc("nvmlDeviceGetPciInfo_v3", "nvmlDeviceGetPciInfo_v2", "nvmlDeviceGetPciInfo")
	if !ok {
		return info, ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&info)))

	return info, toReturn(r1)
}

func (d winDevice) GetPciInfoExt() (PciInfoExt, Return) {
	var info PciInfoExt

	info.Version = structVersion[PciInfoExt](1)

	proc, ok := findProc("nvmlDeviceGetPciInfoExt")
	if !ok {
		return info, ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&info)))

	return info, toReturn(r1)
}

func (d winDevice) GetPerformanceState() (Pstates, Return) {
	val, ret := d.getUint32("nvmlDeviceGetPerformanceState")

	return Pstates(val), ret //nolint:gosec // enum
}

func (d winDevice) GetPersistenceMode() (EnableState, Return) {
	val, ret := d.getUint32("nvmlDeviceGetPersistenceMode")

	return toEnableState(val), ret
}

func (d winDevice) GetPlatformInfo() (PlatformInfo, Return) {
	var info PlatformInfo

	info.Version = structVersion[PlatformInfo](1)

	proc, ok := findProc("nvmlDeviceGetPlatformInfo")
	if !ok {
		return info, ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&info)))

	return info, toReturn(r1)
}

func (d winDevice) GetPowerManagementDefaultLimit() (uint32, Return) {
	return d.getUint32("nvmlDeviceGetPowerManagementDefaultLimit")
}

func (d winDevice) GetPowerManagementLimit() (uint32, Return) {
	return d.getUint32("nvmlDeviceGetPowerManagementLimit")
}

func (d winDevice) GetPowerManagementLimitConstraints() (uint32, uint32, Return) {
	return d.getTwoUint32("nvmlDeviceGetPowerManagementLimitConstraints")
}

func (d winDevice) GetPowerUsage() (uint32, Return) {
	return d.getUint32("nvmlDeviceGetPowerUsage")
}

func (d winDevice) GetRemappedRows() (int, int, bool, bool, Return) {
	proc, ok := findProc("nvmlDeviceGetRemappedRows")
	if !ok {
		return 0, 0, false, false, ERROR_FUNCTION_NOT_FOUND
	}

	var corrRows, uncRows, isPending, failureOccurred uint32

	r1, _, _ := proc.Call(
		d.handle,
		uintptr(unsafe.Pointer(&corrRows)),
		uintptr(unsafe.Pointer(&uncRows)),
		uintptr(unsafe.Pointer(&isPending)),
		uintptr(unsafe.Pointer(&failureOccurred)),
	)

	return int(corrRows), int(uncRows), isPending != 0, failureOccurred != 0, toReturn(r1)
}

func (d winDevice) GetRemappedRows_v2() (RemappedRowsInfo_v2, Return) {
	var info RemappedRowsInfo_v2

	proc, ok := findProc("nvmlDeviceGetRemappedRows_v2")
	if !ok {
		return info, ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&info)))

	return info, toReturn(r1)
}

func (d winDevice) GetRetiredPages(cause PageRetirementCause) ([]uint64, Return) {
	proc, ok := findProc("nvmlDeviceGetRetiredPages")
	if !ok {
		return nil, ERROR_FUNCTION_NOT_FOUND
	}

	var pageCount uint32 = 1

	for {
		addresses := make([]uint64, pageCount)

		r1, _, _ := proc.Call(
			d.handle,
			uintptr(uint32(cause)), //nolint:gosec // enum
			uintptr(unsafe.Pointer(&pageCount)),
			uintptr(unsafe.Pointer(&addresses[0])),
		)

		ret := toReturn(r1)
		if ret == SUCCESS {
			return addresses[:pageCount], SUCCESS
		}

		if ret != ERROR_INSUFFICIENT_SIZE {
			return nil, ret
		}

		pageCount *= 2
	}
}

func (d winDevice) GetRetiredPagesPendingStatus() (EnableState, Return) {
	val, ret := d.getUint32("nvmlDeviceGetRetiredPagesPendingStatus")

	return toEnableState(val), ret
}

func (d winDevice) GetRowRemapperHistogram() (RowRemapperHistogramValues, Return) {
	var values RowRemapperHistogramValues

	proc, ok := findProc("nvmlDeviceGetRowRemapperHistogram")
	if !ok {
		return values, ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&values)))

	return values, toReturn(r1)
}

func (d winDevice) GetSerial() (string, Return) {
	return d.getString("nvmlDeviceGetSerial", deviceSerialBufferSize)
}

func (d winDevice) GetSramEccErrorStatus() (EccSramErrorStatus, Return) {
	var status EccSramErrorStatus

	status.Version = structVersion[EccSramErrorStatus](1)

	proc, ok := findProc("nvmlDeviceGetSramEccErrorStatus")
	if !ok {
		return status, ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&status)))

	return status, toReturn(r1)
}

func (d winDevice) GetSupportedClocksEventReasons() (uint64, Return) {
	return d.getUint64("nvmlDeviceGetSupportedClocksEventReasons")
}

func (d winDevice) GetSupportedClocksThrottleReasons() (uint64, Return) {
	return d.getUint64("nvmlDeviceGetSupportedClocksThrottleReasons")
}

func (d winDevice) GetTemperature(sensor TemperatureSensors) (uint32, Return) {
	proc, ok := findProc("nvmlDeviceGetTemperature")
	if !ok {
		return 0, ERROR_FUNCTION_NOT_FOUND
	}

	var temp uint32

	r1, _, _ := proc.Call(d.handle, uintptr(uint32(sensor)), uintptr(unsafe.Pointer(&temp))) //nolint:gosec // enum

	return temp, toReturn(r1)
}

func (d winDevice) GetTotalEccErrors(errorType MemoryErrorType, counterType EccCounterType) (uint64, Return) {
	proc, ok := findProc("nvmlDeviceGetTotalEccErrors")
	if !ok {
		return 0, ERROR_FUNCTION_NOT_FOUND
	}

	var count uint64

	r1, _, _ := proc.Call(
		d.handle,
		uintptr(uint32(errorType)),   //nolint:gosec // enum
		uintptr(uint32(counterType)), //nolint:gosec // enum
		uintptr(unsafe.Pointer(&count)),
	)

	return count, toReturn(r1)
}

func (d winDevice) GetTotalEnergyConsumption() (uint64, Return) {
	return d.getUint64("nvmlDeviceGetTotalEnergyConsumption")
}

func (d winDevice) GetUtilizationRates() (Utilization, Return) {
	var util Utilization

	proc, ok := findProc("nvmlDeviceGetUtilizationRates")
	if !ok {
		return util, ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&util)))

	return util, toReturn(r1)
}

func (d winDevice) GetUUID() (string, Return) {
	return d.getString("nvmlDeviceGetUUID", deviceUUIDV2BufferSize)
}

func (d winDevice) GetVbiosVersion() (string, Return) {
	return d.getString("nvmlDeviceGetVbiosVersion", deviceVbiosVersionBufferSize)
}

func (d winDevice) GpmQueryDeviceSupport() (GpmSupport, Return) {
	var support GpmSupport

	support.Version = gpmSupportVersion

	proc, ok := findProc("nvmlGpmQueryDeviceSupport")
	if !ok {
		return support, ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&support)))

	return support, toReturn(r1)
}

func (d winDevice) RegisterEvents(eventTypes uint64, set EventSet) Return {
	evtSet, ok := set.(winEventSet)
	if !ok {
		return ERROR_INVALID_ARGUMENT
	}

	proc, ok := findProc("nvmlDeviceRegisterEvents")
	if !ok {
		return ERROR_FUNCTION_NOT_FOUND
	}

	r1, _, _ := proc.Call(d.handle, uintptr(eventTypes), evtSet.handle)

	return toReturn(r1)
}

func (d winDevice) getUint32(symbol string) (uint32, Return) {
	proc, ok := findProc(symbol)
	if !ok {
		return 0, ERROR_FUNCTION_NOT_FOUND
	}

	var val uint32

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&val)))

	return val, toReturn(r1)
}

func (d winDevice) getTwoUint32(symbol string) (uint32, uint32, Return) {
	proc, ok := findProc(symbol)
	if !ok {
		return 0, 0, ERROR_FUNCTION_NOT_FOUND
	}

	var a, b uint32

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&a)), uintptr(unsafe.Pointer(&b)))

	return a, b, toReturn(r1)
}

func (d winDevice) getUint64(symbol string) (uint64, Return) {
	proc, ok := findProc(symbol)
	if !ok {
		return 0, ERROR_FUNCTION_NOT_FOUND
	}

	var val uint64

	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&val)))

	return val, toReturn(r1)
}

func (d winDevice) getString(symbol string, size int) (string, Return) {
	proc, ok := findProc(symbol)
	if !ok {
		return "", ERROR_FUNCTION_NOT_FOUND
	}

	buf := make([]byte, size)
	r1, _, _ := proc.Call(d.handle, uintptr(unsafe.Pointer(&buf[0])), uintptr(size))

	return cString(buf), toReturn(r1)
}

func (d winDevice) getRunningProcesses(v3Symbol, v2Symbol, v1Symbol string) ([]ProcessInfo, Return) {
	if proc, ok := findProc(v3Symbol); ok {
		return d.getRunningProcessesV2OrV3(proc)
	}

	if proc, ok := findProc(v2Symbol); ok {
		return d.getRunningProcessesV2OrV3(proc)
	}

	if proc, ok := findProc(v1Symbol); ok {
		return d.getRunningProcessesV1(proc)
	}

	return nil, ERROR_FUNCTION_NOT_FOUND
}

func (d winDevice) getRunningProcessesV2OrV3(proc *windows.LazyProc) ([]ProcessInfo, Return) {
	var count uint32 = 1

	for {
		infos := make([]ProcessInfo, count)

		r1, _, _ := proc.Call(
			d.handle,
			uintptr(unsafe.Pointer(&count)),
			uintptr(unsafe.Pointer(&infos[0])),
		)

		ret := toReturn(r1)
		if ret == SUCCESS {
			return infos[:count], SUCCESS
		}

		if ret != ERROR_INSUFFICIENT_SIZE {
			return nil, ret
		}

		count *= 2
	}
}

func (d winDevice) getRunningProcessesV1(proc *windows.LazyProc) ([]ProcessInfo, Return) {
	var count uint32 = 1

	for {
		v1Infos := make([]processInfoV1, count)

		r1, _, _ := proc.Call(
			d.handle,
			uintptr(unsafe.Pointer(&count)),
			uintptr(unsafe.Pointer(&v1Infos[0])),
		)

		ret := toReturn(r1)
		if ret == SUCCESS {
			infos := make([]ProcessInfo, count)
			for i, p := range v1Infos[:count] {
				infos[i] = ProcessInfo{
					Pid:               p.Pid,
					UsedGpuMemory:     p.UsedGpuMemory,
					GpuInstanceId:     0xFFFFFFFF,
					ComputeInstanceId: 0xFFFFFFFF,
				}
			}

			return infos, SUCCESS
		}

		if ret != ERROR_INSUFFICIENT_SIZE {
			return nil, ret
		}

		count *= 2
	}
}

func structVersion[T any](version uint32) uint32 {
	var zero T

	return uint32(unsafe.Sizeof(zero)) | (version << 24) //nolint:gosec // C struct size fits in uint32
}

func cString(buf []byte) string {
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}

	return string(buf)
}

func int8SliceToString(s []int8) string {
	buf := make([]byte, 0, len(s))

	for _, c := range s {
		if c == 0 {
			break
		}

		buf = append(buf, byte(c)) //nolint:gosec // C char bytes
	}

	return string(buf)
}
