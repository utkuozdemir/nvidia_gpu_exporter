//go:build linux && cgo

package nvmlnative

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

// Available reports whether this build carries the NVML backend.
const Available = true

// nvmlAPI is the injection seam for the package-level NVML entry points, so
// the lifecycle and collection logic is testable with the go-nvml mock
// device. Device-level getters are already behind the nvml.Device interface.
type nvmlAPI struct {
	init            func() nvml.Return
	shutdown        func() nvml.Return
	deviceCount     func() (int, nvml.Return)
	deviceByIndex   func(int) (nvml.Device, nvml.Return)
	driverVersion   func() (string, nvml.Return)
	processName     func(int) (string, nvml.Return)
	validateInforom func(nvml.Device) nvml.Return
}

func realNVML() nvmlAPI {
	return nvmlAPI{
		init:            nvml.Init,
		shutdown:        nvml.Shutdown,
		deviceCount:     nvml.DeviceGetCount,
		deviceByIndex:   func(i int) (nvml.Device, nvml.Return) { return nvml.DeviceGetHandleByIndex(i) },
		driverVersion:   nvml.SystemGetDriverVersion,
		processName:     nvml.SystemGetProcessName,
		validateInforom: nvml.DeviceValidateInforom,
	}
}

// Backend collects GPU metrics directly from libnvidia-ml. NVML stays
// initialized across collections; after a lifecycle-class failure (GPU lost,
// driver reloaded) the next collection re-initializes from scratch.
type Backend struct {
	api         nvmlAPI
	mu          sync.Mutex
	initialized bool
	permLogged  bool
	logger      *slog.Logger
}

// New dlopens and initializes NVML. Failure here is a startup error: the
// library or driver is absent or unusable.
func New(logger *slog.Logger) (*Backend, error) {
	return newWithAPI(realNVML(), logger)
}

func newWithAPI(api nvmlAPI, logger *slog.Logger) (*Backend, error) {
	b := &Backend{api: api, logger: logger}
	if ret := b.api.init(); ret != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to initialize NVML: %s", ret.String())
	}

	b.initialized = true

	return b, nil
}

// DriverVersion reports the installed driver version, used to pick the
// driver-appropriate field spellings during resolution.
func (b *Backend) DriverVersion() string {
	version, ret := b.api.driverVersion()
	if ret != nvml.SUCCESS {
		return ""
	}

	return version
}

// Close shuts NVML down, best-effort: when a collection is stuck inside the
// driver and holds the lock, shutdown is skipped rather than hanging process
// exit behind an unkillable driver call (process exit is the ultimate
// cleanup).
func (b *Backend) Close() {
	if !b.mu.TryLock() {
		b.logger.Warn("skipping NVML shutdown: a collection is still holding the driver")

		return
	}
	defer b.mu.Unlock()

	if b.initialized {
		_ = b.api.shutdown()
		b.initialized = false
	}
}

// lifecycleErrors are NVML returns that mean the driver/GPU state is gone,
// not that a field is unavailable: the collection fails (never rendering a
// healthy scrape with missing series) and the next cycle re-initializes.
// Verified empirically that these cannot be provoked in software on a healthy
// box (the kernel blocks device removal while a client holds it), so this
// path is covered by mock tests, not captures.
func isLifecycleError(ret nvml.Return) bool {
	switch ret {
	case nvml.ERROR_GPU_IS_LOST, nvml.ERROR_UNINITIALIZED, nvml.ERROR_DRIVER_NOT_LOADED,
		nvml.ERROR_LIB_RM_VERSION_MISMATCH, nvml.ERROR_GPU_NOT_FOUND:
		return true
	default:
		return false
	}
}

// tok maps a non-success NVML return to the token nvidia-smi prints for it.
// The NOT_SUPPORTED -> [N/A] and DEPRECATED mappings are capture-verified.
// The rest were not observable on the test hardware (device-deny surfaces as
// ERROR_UNKNOWN at init, not per field); all tokens parse as absent in the
// shared transform layer either way, so the exported series stay identical.
func tok(ret nvml.Return) string {
	switch ret {
	case nvml.ERROR_NOT_SUPPORTED:
		return tokenNotAvailable
	case nvml.ERROR_NO_PERMISSION:
		return tokenNoPermission
	case nvml.ERROR_DEPRECATED:
		return tokenDeprecated
	case nvml.ERROR_FUNCTION_NOT_FOUND:
		return tokenFunctionNotFound
	default:
		return tokenUnknownError
	}
}

// QueryFunc builds the collection function for the collect package. The
// returned code is the NVML return code of the collection (0 on success,
// -1 when no NVML code applies: an abandoned or rejected cycle), exported
// as nvml_return_code.
//
// An in-process driver call cannot be killed the way a subprocess can, so
// the timeout contract is weaker than exec's: when ctx expires the
// collection is abandoned (reported failed, its late result discarded) but
// the stuck call keeps its goroutine until the driver returns. The backend
// mutex makes collections after an abandoned one fail fast instead of
// piling up behind the stuck call. A FatalError surfaced by the abandoned
// goroutine after abandonment is discarded with the rest of its result;
// shutdown-on-error then fires one cycle later, when the re-detection runs.
func (b *Backend) QueryFunc(fields nvidiasmi.ResolvedFields, computeApps bool) collect.QueryFunc {
	return func(ctx context.Context) (collect.Reading, int, error) {
		type outcome struct {
			reading collect.Reading
			code    int
			err     error
		}

		resultCh := make(chan outcome, 1)

		go func() {
			reading, code, err := b.collectCycle(ctx, fields, computeApps)
			resultCh <- outcome{reading: reading, code: code, err: err}
		}()

		select {
		case result := <-resultCh:
			return result.reading, result.code, result.err
		case <-ctx.Done():
			return collect.Reading{}, -1, fmt.Errorf("collection abandoned: %w", ctx.Err())
		}
	}
}

// collectCycle runs one full collection: the GPU table, plus the per-process
// list when enabled. One lock spans the whole cycle so its phases are
// coherent and an abandoned cycle cannot interleave with a new one. TryLock
// instead of Lock: when a previous collection is stuck inside the driver,
// subsequent scrapes must fail fast rather than queue up behind it.
func (b *Backend) collectCycle(
	ctx context.Context,
	fields nvidiasmi.ResolvedFields,
	computeApps bool,
) (collect.Reading, int, error) {
	if !b.mu.TryLock() {
		return collect.Reading{}, -1,
			errors.New("a previous collection is still in progress (stuck driver call?)")
	}
	defer b.mu.Unlock()

	table, code, err := b.collectTable(ctx, fields)
	if err != nil {
		return collect.Reading{}, code, err
	}

	reading := collect.Reading{Table: table}

	if computeApps {
		reading.AppsAttempted = true

		apps, appsErr := b.collectComputeApps(ctx)
		if appsErr != nil {
			reading.AppsErr = appsErr
		} else {
			reading.Apps = apps
			reading.AppsSuccess = true
		}
	}

	return reading, code, nil
}

// collectTable runs one GPU collection cycle over all devices. The caller
// holds the backend lock.
func (b *Backend) collectTable(
	ctx context.Context,
	fields nvidiasmi.ResolvedFields,
) (*nvidiasmi.Table, int, error) {
	if !b.initialized {
		if ret := b.api.init(); ret != nvml.SUCCESS {
			return nil, int(ret), &collect.FatalError{
				Err: fmt.Errorf("failed to re-initialize NVML: %s", ret.String()),
			}
		}

		b.logger.Info("re-initialized NVML after a lifecycle error")

		b.initialized = true
	}

	count, ret := b.api.deviceCount()
	if ret != nvml.SUCCESS {
		return nil, int(ret), b.lifecycle(ret, errors.New("failed to count devices"))
	}

	if count == 0 {
		// distinguishable from a healthy idle machine: with the exec backend
		// nvidia-smi also fails on zero devices, and a broken container
		// device mount must not look like a successful empty scrape
		return nil, -1, errors.New("no NVML devices found")
	}

	// shared per-cycle values, collected once (not per device): the same
	// reading appears in every row, keeping rows mutually consistent
	shared := sharedValues{timestamp: time.Now().Format(timestampLayout)}

	driverVersion, ret := b.api.driverVersion()
	shared.driverVersion, shared.driverVersionRet = driverVersion, ret
	shared.count, shared.countRet = count, nvml.SUCCESS

	plan := newPlan(fields)

	rows := make([]nvidiasmi.Row, 0, count)
	qFieldToCells := make(map[nvidiasmi.QField][]nvidiasmi.Cell, len(fields.Query))

	for i := range count {
		if err := ctx.Err(); err != nil {
			return nil, -1, fmt.Errorf("collection interrupted: %w", err)
		}

		dev, ret := b.api.deviceByIndex(i)
		if ret != nvml.SUCCESS {
			return nil, int(ret), b.lifecycle(ret, fmt.Errorf("failed to get device %d", i))
		}

		values, err := b.collectDevice(ctx, dev, shared, plan)
		if err != nil {
			var lifecycleErr *lifecycleRetError
			if errors.As(err, &lifecycleErr) {
				return nil, int(lifecycleErr.ret), b.lifecycle(lifecycleErr.ret, err)
			}

			// no NVML status to report (e.g. a context interruption)
			return nil, -1, err
		}

		cells := make([]nvidiasmi.Cell, 0, len(fields.Query))
		rowCells := make(map[nvidiasmi.QField]nvidiasmi.Cell, len(fields.Query))

		for _, q := range fields.Query {
			raw, ok := values[canonicalQField(q)]
			if !ok {
				// a catalogued field this collector version has no reading
				// for: emit the absent token so the behavior matches an
				// unsupported field instead of a hole in the table
				raw = tokenNotAvailable
			}

			cell := nvidiasmi.Cell{QField: q, RField: fields.Returned[q], RawValue: raw}
			cells = append(cells, cell)
			rowCells[q] = cell
			qFieldToCells[q] = append(qFieldToCells[q], cell)
		}

		rows = append(rows, nvidiasmi.Row{QFieldToCells: rowCells, Cells: cells})
	}

	rFields := make([]nvidiasmi.RField, len(fields.Query))
	for i, q := range fields.Query {
		rFields[i] = fields.Returned[q]
	}

	return &nvidiasmi.Table{Rows: rows, RFields: rFields, QFieldToCells: qFieldToCells}, 0, nil
}

// lifecycle folds a failed collection's error: lifecycle-class returns mark
// the backend for re-initialization and are wrapped as fatal for
// shutdown-on-error; anything else is a plain failed collection.
func (b *Backend) lifecycle(ret nvml.Return, err error) error {
	err = fmt.Errorf("%w: %s", err, ret.String())

	if isLifecycleError(ret) {
		b.markLost()

		return &collect.FatalError{Err: err}
	}

	return err
}

// markLost flags the backend for re-initialization on the next cycle.
func (b *Backend) markLost() {
	if b.initialized {
		b.initialized = false

		_ = b.api.shutdown()
	}
}

// lifecycleRetError carries a lifecycle-class return code out of a device
// collection pass.
type lifecycleRetError struct {
	ret nvml.Return
}

func (e *lifecycleRetError) Error() string {
	return fmt.Sprintf("device collection hit a lifecycle error: %s", e.ret.String())
}

// sharedValues are once-per-cycle readings injected into every device row.
type sharedValues struct {
	timestamp        string
	driverVersion    string
	driverVersionRet nvml.Return
	count            int
	countRet         nvml.Return
}

// plan is the per-cycle collection plan: which canonical fields were
// requested, so unrequested NVML getters are not called at all (an excluded
// slow or problematic field must cost nothing).
type plan struct {
	requested map[nvidiasmi.QField]bool
}

func newPlan(fields nvidiasmi.ResolvedFields) plan {
	requested := make(map[nvidiasmi.QField]bool, len(fields.Query))
	for _, q := range fields.Query {
		requested[canonicalQField(q)] = true
	}

	return plan{requested: requested}
}

// want reports whether any of the given fields was requested.
func (p plan) want(fields ...nvidiasmi.QField) bool {
	for _, f := range fields {
		if p.requested[f] {
			return true
		}
	}

	return false
}

// devCollector accumulates one device's readings and remembers the first
// lifecycle-class return and any permission-denied fields it sees.
type devCollector struct {
	values map[nvidiasmi.QField]string
	fatal  *nvml.Return
	denied []nvidiasmi.QField
}

// classify tracks a non-success return's collection-level consequences.
func (c *devCollector) classify(field nvidiasmi.QField, ret nvml.Return) {
	if isLifecycleError(ret) && c.fatal == nil {
		c.fatal = &ret
	}

	if ret == nvml.ERROR_NO_PERMISSION {
		c.denied = append(c.denied, field)
	}
}

// set records a field: the formatted value when the call succeeded, the
// error token otherwise. A nil format function records the absent token, so
// a decode gap can never panic the collector.
func (c *devCollector) set(field nvidiasmi.QField, ret nvml.Return, format func() string) {
	if ret == nvml.SUCCESS && format != nil {
		c.values[field] = format()

		return
	}

	if ret == nvml.SUCCESS {
		c.values[field] = tokenNotAvailable

		return
	}

	c.classify(field, ret)

	c.values[field] = tok(ret)
}

// setBare is set with the bracket-less N/A absence token (fabric.* and
// temperature.memory print bare N/A, capture-verified).
func (c *devCollector) setBare(field nvidiasmi.QField, ret nvml.Return, format func() string) {
	if ret == nvml.SUCCESS && format != nil {
		c.values[field] = format()

		return
	}

	c.classify(field, ret)

	c.values[field] = tokenBareNotAvailable
}

// clock-event reason bits (stable NVML ABI values).
const (
	reasonGpuIdle          uint64 = 0x1
	reasonAppClocksSetting uint64 = 0x2
	reasonSwPowerCap       uint64 = 0x4
	reasonHwSlowdown       uint64 = 0x8
	reasonSyncBoost        uint64 = 0x10
	reasonSwThermal        uint64 = 0x20
	reasonHwThermal        uint64 = 0x40
	reasonHwPowerBrake     uint64 = 0x80
)

// edppMultiplierFieldID is NVML_FI_DEV_EDPP_MULTIPLIER, not yet exposed as a
// go-nvml constant. Trace-verified: nvidia-smi queries this field ID for
// edpp_multiplier.
const edppMultiplierFieldID = 274

// collectDevice produces the raw cell strings for one device, exactly as
// nvidia-smi would print them, calling only the getters the plan requires.
//
//nolint:funlen,maintidx,cyclop,gocognit,gocyclo // deliberately one linear pass mirroring the catalog order
func (b *Backend) collectDevice(
	ctx context.Context,
	dev nvml.Device,
	shared sharedValues,
	p plan,
) (map[nvidiasmi.QField]string, error) {
	c := &devCollector{values: make(map[nvidiasmi.QField]string, len(fieldOrder))}

	c.values["timestamp"] = shared.timestamp
	c.set("driver_version", shared.driverVersionRet, func() string { return shared.driverVersion })
	c.set("count", shared.countRet, func() string { return strconv.Itoa(shared.count) })

	if p.want("name") {
		name, ret := dev.GetName()
		c.set("name", ret, func() string { return name })
	}

	if p.want("serial") {
		serial, ret := dev.GetSerial()
		c.set("serial", ret, func() string { return serial })
	}

	if p.want("uuid") {
		uuid, ret := dev.GetUUID()
		c.set("uuid", ret, func() string { return uuid })
	}

	if p.want("index") {
		index, ret := dev.GetIndex()
		c.set("index", ret, func() string { return strconv.Itoa(index) })
	}

	if p.want("pci.bus_id", "pci.domain", "pci.bus", "pci.device", "pci.baseClass",
		"pci.subClass", "pci.device_id", "pci.sub_device_id") {
		pci, ret := dev.GetPciInfoExt()
		c.set("pci.bus_id", ret, func() string { return i8str(pci.BusId[:]) })
		c.set("pci.domain", ret, func() string { return fmt.Sprintf("0x%04X", pci.Domain) })
		c.set("pci.bus", ret, func() string { return fmt.Sprintf("0x%02X", pci.Bus) })
		c.set("pci.device", ret, func() string { return fmt.Sprintf("0x%02X", pci.Device) })
		c.set("pci.baseClass", ret, func() string { return fmt.Sprintf("0x%X", pci.BaseClass) })
		c.set("pci.subClass", ret, func() string { return fmt.Sprintf("0x%X", pci.SubClass) })
		c.set("pci.device_id", ret, func() string { return fmt.Sprintf("0x%08X", pci.PciDeviceId) })
		c.set("pci.sub_device_id", ret, func() string { return fmt.Sprintf("0x%08X", pci.PciSubSystemId) })
	}

	if p.want("pcie.link.gen.current", "pcie.link.gen.gpucurrent") {
		gen, ret := dev.GetCurrPcieLinkGeneration()
		c.set("pcie.link.gen.current", ret, func() string { return strconv.Itoa(gen) })
		c.set("pcie.link.gen.gpucurrent", ret, func() string { return strconv.Itoa(gen) })
	}

	if p.want("pcie.link.gen.max") {
		maxGen, ret := dev.GetMaxPcieLinkGeneration()
		c.set("pcie.link.gen.max", ret, func() string { return strconv.Itoa(maxGen) })
	}

	if p.want("pcie.link.gen.gpumax") {
		gpuMaxGen, ret := dev.GetGpuMaxPcieLinkGeneration()
		c.set("pcie.link.gen.gpumax", ret, func() string { return strconv.Itoa(gpuMaxGen) })
	}

	if p.want("pcie.link.width.current") {
		width, ret := dev.GetCurrPcieLinkWidth()
		c.set("pcie.link.width.current", ret, func() string { return strconv.Itoa(width) })
	}

	if p.want("pcie.link.width.max") {
		maxWidth, ret := dev.GetMaxPcieLinkWidth()
		c.set("pcie.link.width.max", ret, func() string { return strconv.Itoa(maxWidth) })
	}

	// the deprecation-listed fields (see deprecatedFields) get the token
	// regardless of what NVML would answer, matching nvidia-smi, which does
	// not even call the getters for them. Pinned to the verified driver
	// generation; the corpus drift test guards the list.
	for _, field := range deprecatedFields {
		c.values[field] = tokenDeprecated
	}

	if p.want("display_attached") {
		dispMode, ret := dev.GetDisplayMode()
		c.set("display_attached", ret, func() string { return yesNo(dispMode == nvml.FEATURE_ENABLED) })
	}

	if p.want("display_active") {
		dispActive, ret := dev.GetDisplayActive()
		c.set("display_active", ret, func() string { return onOff(dispActive == nvml.FEATURE_ENABLED) })
	}

	if p.want("persistence_mode") {
		persistence, ret := dev.GetPersistenceMode()
		c.set("persistence_mode", ret, func() string { return onOff(persistence == nvml.FEATURE_ENABLED) })
	}

	if p.want("addressing_mode") {
		addrMode, ret := dev.GetAddressingMode()
		c.set("addressing_mode", ret, func() string { return addressingModeStr(addrMode.Value) })
	}

	if p.want("accounting.mode") {
		accMode, ret := dev.GetAccountingMode()
		c.set("accounting.mode", ret, func() string { return onOff(accMode == nvml.FEATURE_ENABLED) })
	}

	if p.want("accounting.buffer_size") {
		accBuf, ret := dev.GetAccountingBufferSize()
		c.set("accounting.buffer_size", ret, func() string { return strconv.Itoa(accBuf) })
	}

	if p.want("driver_model.current", "driver_model.pending") {
		dmCur, dmPend, ret := dev.GetDriverModel()
		c.set("driver_model.current", ret, func() string { return driverModelStr(int32(dmCur)) })
		c.set("driver_model.pending", ret, func() string { return driverModelStr(int32(dmPend)) })
	}

	if p.want("vbios_version") {
		vbios, ret := dev.GetVbiosVersion()
		c.set("vbios_version", ret, func() string { return vbios })
	}

	if p.want("inforom.img") {
		img, ret := dev.GetInforomImageVersion()
		c.set("inforom.img", ret, func() string { return img })
	}

	if p.want("inforom.oem") {
		oem, ret := dev.GetInforomVersion(nvml.INFOROM_OEM)
		c.set("inforom.oem", ret, func() string { return oem })
	}

	if p.want("inforom.ecc") {
		eccVer, ret := dev.GetInforomVersion(nvml.INFOROM_ECC)
		c.set("inforom.ecc", ret, func() string { return eccVer })
	}

	if p.want("inforom.pwr") {
		pwr, ret := dev.GetInforomVersion(nvml.INFOROM_POWER)
		c.set("inforom.pwr", ret, func() string { return pwr })
	}

	if p.want("inforom.checksum_validation") {
		// "valid" is capture-verified; the corruption spelling is not
		ret := b.api.validateInforom(dev)
		c.set("inforom.checksum_validation", ret, func() string { return "valid" })
	}

	if p.want("gom.current", "gom.pending") {
		gomCur, gomPend, ret := dev.GetGpuOperationMode()
		c.set("gom.current", ret, func() string { return gomStr(int32(gomCur)) })
		c.set("gom.pending", ret, func() string { return gomStr(int32(gomPend)) })
	}

	if p.want("fan.speed") {
		fan, ret := dev.GetFanSpeed()
		c.set("fan.speed", ret, func() string { return pct(fan) })
	}

	if p.want("pstate") {
		pstate, ret := dev.GetPerformanceState()
		c.set("pstate", ret, func() string { return fmt.Sprintf("P%d", pstate) })
	}

	if p.want("clocks_event_reasons.supported") {
		supported, ret := dev.GetSupportedClocksEventReasons()
		c.set("clocks_event_reasons.supported", ret,
			func() string { return fmt.Sprintf("0x%016X", supported) })
	}

	if p.want("clocks_event_reasons.active", "clocks_event_reasons.gpu_idle",
		"clocks_event_reasons.applications_clocks_setting", "clocks_event_reasons.sw_power_cap",
		"clocks_event_reasons.hw_slowdown", "clocks_event_reasons.sync_boost",
		"clocks_event_reasons.sw_thermal_slowdown", "clocks_event_reasons.hw_thermal_slowdown",
		"clocks_event_reasons.hw_power_brake_slowdown") {
		active, ret := dev.GetCurrentClocksEventReasons()
		c.set("clocks_event_reasons.active", ret, func() string { return fmt.Sprintf("0x%016X", active) })

		for field, bit := range map[nvidiasmi.QField]uint64{
			"clocks_event_reasons.gpu_idle":                    reasonGpuIdle,
			"clocks_event_reasons.applications_clocks_setting": reasonAppClocksSetting,
			"clocks_event_reasons.sw_power_cap":                reasonSwPowerCap,
			"clocks_event_reasons.hw_slowdown":                 reasonHwSlowdown,
			"clocks_event_reasons.sync_boost":                  reasonSyncBoost,
			"clocks_event_reasons.sw_thermal_slowdown":         reasonSwThermal,
			"clocks_event_reasons.hw_thermal_slowdown":         reasonHwThermal,
			"clocks_event_reasons.hw_power_brake_slowdown":     reasonHwPowerBrake,
		} {
			if !p.want(field) {
				// keep the permission diagnostics limited to requested fields
				continue
			}

			c.set(field, ret, func() string { return activeNotActive(active, bit) })
		}
	}

	b.collectFieldValues(dev, c, p)

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("device collection interrupted: %w", err)
	}

	if p.want("memory.total", "memory.reserved", "memory.used", "memory.free") {
		mem, ret := dev.GetMemoryInfo_v2()
		c.set("memory.total", ret, func() string { return mib(mem.Total) })
		c.set("memory.reserved", ret, func() string { return mib(mem.Reserved) })
		c.set("memory.used", ret, func() string { return mib(mem.Used) })
		c.set("memory.free", ret, func() string { return mib(mem.Free) })
	}

	if p.want("compute_mode") {
		cm, ret := dev.GetComputeMode()
		c.set("compute_mode", ret, func() string { return computeModeStr(int32(cm)) })
	}

	if p.want("compute_cap") {
		major, minor, ret := dev.GetCudaComputeCapability()
		c.set("compute_cap", ret, func() string { return fmt.Sprintf("%d.%d", major, minor) })
	}

	if p.want("utilization.gpu", "utilization.memory") {
		util, ret := dev.GetUtilizationRates()
		c.set("utilization.gpu", ret, func() string { return pct(util.Gpu) })
		c.set("utilization.memory", ret, func() string { return pct(util.Memory) })
	}

	if p.want("utilization.encoder") {
		encUtil, _, ret := dev.GetEncoderUtilization()
		c.set("utilization.encoder", ret, func() string { return pct(encUtil) })
	}

	if p.want("utilization.decoder") {
		decUtil, _, ret := dev.GetDecoderUtilization()
		c.set("utilization.decoder", ret, func() string { return pct(decUtil) })
	}

	if p.want("utilization.jpeg") {
		jpgUtil, _, ret := dev.GetJpgUtilization()
		c.set("utilization.jpeg", ret, func() string { return pct(jpgUtil) })
	}

	if p.want("utilization.ofa") {
		ofaUtil, _, ret := dev.GetOfaUtilization()
		c.set("utilization.ofa", ret, func() string { return pct(ofaUtil) })
	}

	if p.want("encoder.stats.sessionCount", "encoder.stats.averageFps", "encoder.stats.averageLatency") {
		sessions, fps, latency, ret := dev.GetEncoderStats()
		c.set("encoder.stats.sessionCount", ret, func() string { return strconv.Itoa(sessions) })
		c.set("encoder.stats.averageFps", ret, func() string { return strconv.FormatUint(uint64(fps), 10) })
		c.set("encoder.stats.averageLatency", ret, func() string { return strconv.FormatUint(uint64(latency), 10) })
	}

	if p.want("dramEncryption.mode.current", "dramEncryption.mode.pending") {
		dramCur, dramPend, ret := dev.GetDramEncryptionMode()
		c.set("dramEncryption.mode.current", ret, func() string { return onOff(dramCur.EncryptionState != 0) })
		c.set("dramEncryption.mode.pending", ret, func() string { return onOff(dramPend.EncryptionState != 0) })
	}

	if p.want("ecc.mode.current", "ecc.mode.pending") {
		eccCur, eccPend, ret := dev.GetEccMode()
		c.set("ecc.mode.current", ret, func() string { return onOff(eccCur == nvml.FEATURE_ENABLED) })
		c.set("ecc.mode.pending", ret, func() string { return onOff(eccPend == nvml.FEATURE_ENABLED) })
	}

	collectEccCounters(dev, c, p)
	collectSramEcc(dev, c, p)

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("device collection interrupted: %w", err)
	}

	if p.want("retired_pages.single_bit_ecc.count") {
		sbePages, ret := dev.GetRetiredPages(nvml.PAGE_RETIREMENT_CAUSE_MULTIPLE_SINGLE_BIT_ECC_ERRORS)
		c.set("retired_pages.single_bit_ecc.count", ret, func() string { return strconv.Itoa(len(sbePages)) })
	}

	if p.want("retired_pages.double_bit.count") {
		dbePages, ret := dev.GetRetiredPages(nvml.PAGE_RETIREMENT_CAUSE_DOUBLE_BIT_ECC_ERROR)
		c.set("retired_pages.double_bit.count", ret, func() string { return strconv.Itoa(len(dbePages)) })
	}

	if p.want("retired_pages.pending") {
		retiredPending, ret := dev.GetRetiredPagesPendingStatus()
		c.set("retired_pages.pending", ret, func() string { return yesNo(retiredPending == nvml.FEATURE_ENABLED) })
	}

	if p.want("remapped_rows.correctable", "remapped_rows.uncorrectable",
		"remapped_rows.pending", "remapped_rows.failure") {
		corrRows, uncRows, isPending, failed, ret := dev.GetRemappedRows()
		c.set("remapped_rows.correctable", ret, func() string { return strconv.Itoa(corrRows) })
		c.set("remapped_rows.uncorrectable", ret, func() string { return strconv.Itoa(uncRows) })
		c.set("remapped_rows.pending", ret, func() string { return yesNo(isPending) })
		c.set("remapped_rows.failure", ret, func() string { return yesNo(failed) })
	}

	if p.want("remapped_rows.histogram.max", "remapped_rows.histogram.high",
		"remapped_rows.histogram.partial", "remapped_rows.histogram.low", "remapped_rows.histogram.none") {
		hist, ret := dev.GetRowRemapperHistogram()
		c.set("remapped_rows.histogram.max", ret, func() string { return strconv.FormatUint(uint64(hist.Max), 10) })
		c.set("remapped_rows.histogram.high", ret, func() string { return strconv.FormatUint(uint64(hist.High), 10) })
		c.set("remapped_rows.histogram.partial", ret,
			func() string { return strconv.FormatUint(uint64(hist.Partial), 10) })
		c.set("remapped_rows.histogram.low", ret, func() string { return strconv.FormatUint(uint64(hist.Low), 10) })
		c.set("remapped_rows.histogram.none", ret, func() string { return strconv.FormatUint(uint64(hist.None), 10) })
	}

	if p.want("temperature.gpu") {
		temp, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
		c.set("temperature.gpu", ret, func() string { return strconv.FormatUint(uint64(temp), 10) })
	}

	if p.want("temperature.gpu.tlimit") {
		margin, ret := dev.GetMarginTemperature()
		c.set("temperature.gpu.tlimit", ret, func() string {
			return strconv.FormatInt(int64(margin.MarginTemperature), 10)
		})
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("device collection interrupted: %w", err)
	}

	if p.want("power.draw") {
		powerUsage, ret := dev.GetPowerUsage()
		c.set("power.draw", ret, func() string { return milliwatts(powerUsage) })
	}

	if p.want("power.limit") {
		limit, ret := dev.GetPowerManagementLimit()
		c.set("power.limit", ret, func() string { return milliwatts(limit) })
	}

	if p.want("enforced.power.limit") {
		enforced, ret := dev.GetEnforcedPowerLimit()
		c.set("enforced.power.limit", ret, func() string { return milliwatts(enforced) })
	}

	if p.want("power.default_limit") {
		defLimit, ret := dev.GetPowerManagementDefaultLimit()
		c.set("power.default_limit", ret, func() string { return milliwatts(defLimit) })
	}

	if p.want("power.min_limit", "power.max_limit") {
		minLimit, maxLimit, ret := dev.GetPowerManagementLimitConstraints()
		c.set("power.min_limit", ret, func() string { return milliwatts(minLimit) })
		c.set("power.max_limit", ret, func() string { return milliwatts(maxLimit) })
	}

	for field, clockType := range map[nvidiasmi.QField]nvml.ClockType{
		"clocks.current.graphics": nvml.CLOCK_GRAPHICS,
		"clocks.current.sm":       nvml.CLOCK_SM,
		"clocks.current.memory":   nvml.CLOCK_MEM,
		"clocks.current.video":    nvml.CLOCK_VIDEO,
	} {
		if !p.want(field) {
			continue
		}

		clock, cret := dev.GetClockInfo(clockType)
		c.set(field, cret, func() string { return mhz(clock) })
	}

	for field, clockType := range map[nvidiasmi.QField]nvml.ClockType{
		"clocks.max.graphics": nvml.CLOCK_GRAPHICS,
		"clocks.max.sm":       nvml.CLOCK_SM,
		"clocks.max.memory":   nvml.CLOCK_MEM,
	} {
		if !p.want(field) {
			continue
		}

		clock, cret := dev.GetMaxClockInfo(clockType)
		c.set(field, cret, func() string { return mhz(clock) })
	}

	if p.want("mig.mode.current", "mig.mode.pending") {
		migCur, migPend, ret := dev.GetMigMode()
		c.set("mig.mode.current", ret, func() string { return onOff(migCur == 1) })
		c.set("mig.mode.pending", ret, func() string { return onOff(migPend == 1) })
	}

	if p.want("gsp.mode.current", "gsp.mode.default") {
		gspEnabled, gspDefault, ret := dev.GetGspFirmwareMode()
		c.set("gsp.mode.current", ret, func() string { return onOff(gspEnabled) })
		c.set("gsp.mode.default", ret, func() string { return onOff(gspDefault) })
	}

	if p.want("c2c.mode") {
		c2c, ret := dev.GetC2cModeInfoV().V1()
		c.set("c2c.mode", ret, func() string { return onOff(c2c.IsC2cEnabled != 0) })
	}

	if p.want("protected_memory.total", "protected_memory.used", "protected_memory.free") {
		protected, ret := dev.GetConfComputeProtectedMemoryUsage()
		c.set("protected_memory.total", ret, func() string { return mib(protected.Total) })
		c.set("protected_memory.used", ret, func() string { return mib(protected.Used) })
		c.set("protected_memory.free", ret, func() string { return mib(protected.Free) })
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("device collection interrupted: %w", err)
	}

	collectFabric(dev, c, p)
	collectPlatform(dev, c, p)

	if p.want("hostname") {
		hostname, ret := dev.GetHostname_v1()
		c.set("hostname", ret, func() string { return hostname })
	}

	if len(c.denied) > 0 && !b.permLogged {
		b.permLogged = true

		b.logger.Warn("some NVML readings are permission-denied and will be absent",
			"fields", fmt.Sprintf("%v", c.denied))
	}

	if c.fatal != nil {
		return nil, &lifecycleRetError{ret: *c.fatal}
	}

	return c.values, nil
}

// collectFieldValues batches everything served by nvmlDeviceGetFieldValues.
// The field-ID choices are trace-verified against nvidia-smi's own calls.
func (b *Backend) collectFieldValues(dev nvml.Device, c *devCollector, p plan) {
	entries := []struct {
		field  nvidiasmi.QField
		id     uint32
		bare   bool
		format func(v float64) string
	}{
		{"power.draw.average", nvml.FI_DEV_POWER_AVERAGE, false,
			func(v float64) string { return fmt.Sprintf("%.2f W", v/1000.0) }},
		{"power.draw.instant", nvml.FI_DEV_POWER_INSTANT, false,
			func(v float64) string { return fmt.Sprintf("%.2f W", v/1000.0) }},
		{"temperature.memory", nvml.FI_DEV_MEMORY_TEMP, true,
			func(v float64) string { return strconv.FormatInt(int64(v), 10) }},
		{"clocks_event_reasons_counters.sw_power_cap", nvml.FI_DEV_CLOCKS_EVENT_REASON_SW_POWER_CAP, false,
			func(v float64) string { return fmt.Sprintf("%d us", int64(v)) }},
		{"clocks_event_reasons_counters.sync_boost", nvml.FI_DEV_CLOCKS_EVENT_REASON_SYNC_BOOST, false,
			func(v float64) string { return fmt.Sprintf("%d us", int64(v)) }},
		{"clocks_event_reasons_counters.sw_thermal_slowdown", nvml.FI_DEV_CLOCKS_EVENT_REASON_SW_THERM_SLOWDOWN, false,
			func(v float64) string { return fmt.Sprintf("%d us", int64(v)) }},
		{"clocks_event_reasons_counters.hw_thermal_slowdown", nvml.FI_DEV_CLOCKS_EVENT_REASON_HW_THERM_SLOWDOWN, false,
			func(v float64) string { return fmt.Sprintf("%d us", int64(v)) }},
		{"clocks_event_reasons_counters.hw_power_brake_slowdown",
			nvml.FI_DEV_CLOCKS_EVENT_REASON_HW_POWER_BRAKE_SLOWDOWN, false,
			func(v float64) string { return fmt.Sprintf("%d us", int64(v)) }},
		{"gpu_recovery_action", nvml.FI_DEV_GET_GPU_RECOVERY_ACTION, false,
			func(v float64) string { return recoveryActionStr(uint64(v)) }},
		{"edpp_multiplier", edppMultiplierFieldID, false,
			func(v float64) string { return fmt.Sprintf("%.2f %%", v) }},
	}

	wanted := entries[:0]

	for _, e := range entries {
		if p.want(e.field) {
			wanted = append(wanted, e)
		}
	}

	if len(wanted) == 0 {
		return
	}

	values := make([]nvml.FieldValue, len(wanted))
	for i, e := range wanted {
		values[i].FieldId = e.id
	}

	ret := dev.GetFieldValues(values)
	if ret != nvml.SUCCESS {
		for _, e := range wanted {
			if e.bare {
				c.setBare(e.field, ret, nil)
			} else {
				c.set(e.field, ret, nil)
			}
		}

		return
	}

	for i, e := range wanted {
		fieldValue := values[i]
		fret := nvml.Return(fieldValue.NvmlReturn)
		value, decodeOK := decodeFieldValue(fieldValue)

		switch {
		case fret != nvml.SUCCESS && e.bare:
			c.setBare(e.field, fret, nil)
		case fret != nvml.SUCCESS:
			c.set(e.field, fret, nil)
		case !decodeOK:
			// an unknown value type from a newer driver: absent, never a panic
			b.logger.Warn("cannot decode NVML field value",
				"field", e.field, "value_type", fieldValue.ValueType)
			c.set(e.field, nvml.SUCCESS, nil)
		default:
			c.values[e.field] = e.format(value)
		}
	}
}

func decodeFieldValue(fv nvml.FieldValue) (float64, bool) {
	b := fv.Value[:]

	switch fv.ValueType {
	case uint32(nvml.VALUE_TYPE_DOUBLE):
		return math.Float64frombits(binary.LittleEndian.Uint64(b)), true
	case uint32(nvml.VALUE_TYPE_UNSIGNED_INT):
		return float64(binary.LittleEndian.Uint32(b)), true
	case uint32(nvml.VALUE_TYPE_UNSIGNED_LONG), uint32(nvml.VALUE_TYPE_UNSIGNED_LONG_LONG):
		return float64(binary.LittleEndian.Uint64(b)), true
	case uint32(nvml.VALUE_TYPE_SIGNED_LONG_LONG):
		return float64(int64(binary.LittleEndian.Uint64(b))), true
	case uint32(nvml.VALUE_TYPE_SIGNED_INT):
		return float64(int32(binary.LittleEndian.Uint32(b))), true
	default:
		return 0, false
	}
}

func collectEccCounters(dev nvml.Device, c *devCollector, p plan) {
	locations := map[string]nvml.MemoryLocation{
		"device_memory":  nvml.MEMORY_LOCATION_DEVICE_MEMORY,
		"dram":           nvml.MEMORY_LOCATION_DRAM,
		"register_file":  nvml.MEMORY_LOCATION_REGISTER_FILE,
		"l1_cache":       nvml.MEMORY_LOCATION_L1_CACHE,
		"l2_cache":       nvml.MEMORY_LOCATION_L2_CACHE,
		"texture_memory": nvml.MEMORY_LOCATION_TEXTURE_MEMORY,
		"cbu":            nvml.MEMORY_LOCATION_CBU,
		"sram":           nvml.MEMORY_LOCATION_SRAM,
	}

	for kindName, errType := range map[string]nvml.MemoryErrorType{
		"corrected":   nvml.MEMORY_ERROR_TYPE_CORRECTED,
		"uncorrected": nvml.MEMORY_ERROR_TYPE_UNCORRECTED,
	} {
		for cntName, cntType := range map[string]nvml.EccCounterType{
			"volatile":  nvml.VOLATILE_ECC,
			"aggregate": nvml.AGGREGATE_ECC,
		} {
			for locName, loc := range locations {
				field := nvidiasmi.QField(fmt.Sprintf("ecc.errors.%s.%s.%s", kindName, cntName, locName))
				if !p.want(field) {
					continue
				}

				v, ret := dev.GetMemoryErrorCounter(errType, cntType, loc)
				c.set(field, ret, func() string { return strconv.FormatUint(v, 10) })
			}

			field := nvidiasmi.QField(fmt.Sprintf("ecc.errors.%s.%s.total", kindName, cntName))
			if !p.want(field) {
				continue
			}

			v, ret := dev.GetTotalEccErrors(errType, cntType)
			c.set(field, ret, func() string { return strconv.FormatUint(v, 10) })
		}
	}
}

func collectSramEcc(dev nvml.Device, c *devCollector, p plan) {
	sramFields := []nvidiasmi.QField{
		"ecc.errors.uncorrected.volatile.sram.parity", "ecc.errors.uncorrected.volatile.sram.secded",
		"ecc.errors.uncorrected.aggregate.sram.parity", "ecc.errors.uncorrected.aggregate.sram.secded",
		"ecc.errors.uncorrected.aggregate.sram.thresholdExceeded",
		"ecc.errors.uncorrected.aggregate.sram.l2", "ecc.errors.uncorrected.aggregate.sram.sm",
		"ecc.errors.uncorrected.aggregate.sram.mcu", "ecc.errors.uncorrected.aggregate.sram.pcie",
		"ecc.errors.uncorrected.aggregate.sram.other",
	}
	if !p.want(sramFields...) {
		return
	}

	s, ret := dev.GetSramEccErrorStatus()

	set := func(field nvidiasmi.QField, v uint64) {
		c.set(field, ret, func() string { return strconv.FormatUint(v, 10) })
	}

	set("ecc.errors.uncorrected.volatile.sram.parity", s.VolatileUncParity)
	set("ecc.errors.uncorrected.volatile.sram.secded", s.VolatileUncSecDed)
	set("ecc.errors.uncorrected.aggregate.sram.parity", s.AggregateUncParity)
	set("ecc.errors.uncorrected.aggregate.sram.secded", s.AggregateUncSecDed)
	c.set("ecc.errors.uncorrected.aggregate.sram.thresholdExceeded", ret,
		func() string { return yesNo(s.BThresholdExceeded != 0) })
	set("ecc.errors.uncorrected.aggregate.sram.l2", s.AggregateUncBucketL2)
	set("ecc.errors.uncorrected.aggregate.sram.sm", s.AggregateUncBucketSm)
	set("ecc.errors.uncorrected.aggregate.sram.mcu", s.AggregateUncBucketMcu)
	set("ecc.errors.uncorrected.aggregate.sram.pcie", s.AggregateUncBucketPcie)
	set("ecc.errors.uncorrected.aggregate.sram.other", s.AggregateUncBucketOther)
}

// collectFabric fills the fabric.* fields. When there is no fabric (state 0
// or the call fails), every field prints the bare N/A token
// (capture-verified). Lifecycle-class failures still poison the cycle.
func collectFabric(dev nvml.Device, c *devCollector, p plan) {
	fabricFields := []nvidiasmi.QField{
		"fabric.state", "fabric.status", "fabric.cliqueId", "fabric.clusterUuid",
	}
	if !p.want(fabricFields...) {
		return
	}

	info, ret := dev.GetGpuFabricInfoV().V2()
	if ret != nvml.SUCCESS || info.State == 0 {
		c.classify("fabric.state", ret)

		for _, f := range fabricFields {
			c.values[f] = tokenBareNotAvailable
		}

		return
	}

	c.values["fabric.state"] = fabricStateStr(info.State)

	status := "Success"
	if nvml.Return(info.Status) != nvml.SUCCESS {
		// a completed probe can still carry a lifecycle-class status
		c.classify("fabric.status", nvml.Return(info.Status))

		status = nvml.Return(info.Status).String()
	}

	c.values["fabric.status"] = status
	c.values["fabric.cliqueId"] = strconv.FormatUint(uint64(info.CliqueId), 10)
	c.values["fabric.clusterUuid"] = uuidBytes(info.ClusterUuid)
}

func collectPlatform(dev nvml.Device, c *devCollector, p plan) {
	if !p.want("platform.chassis_serial_number", "platform.slot_number", "platform.tray_index",
		"platform.host_id", "platform.peer_type", "platform.module_id", "platform.gpu_fabric_guid") {
		return
	}

	info, ret := dev.GetPlatformInfo()

	c.set("platform.chassis_serial_number", ret, func() string { return cstr(info.ChassisSerialNumber[:]) })
	c.set("platform.slot_number", ret, func() string { return strconv.FormatUint(uint64(info.SlotNumber), 10) })
	c.set("platform.tray_index", ret, func() string { return strconv.FormatUint(uint64(info.TrayIndex), 10) })
	c.set("platform.host_id", ret, func() string { return strconv.FormatUint(uint64(info.HostId), 10) })
	c.set("platform.peer_type", ret, func() string {
		if info.PeerType == 0 {
			return "Direct Connected"
		}

		return fmt.Sprintf("Unknown(%d)", info.PeerType)
	})
	c.set("platform.module_id", ret, func() string { return strconv.FormatUint(uint64(info.ModuleId), 10) })
	c.set("platform.gpu_fabric_guid", ret, func() string {
		return fmt.Sprintf("0x%016X", binary.BigEndian.Uint64(info.IbGuid[:8]))
	})
}

// collectComputeApps lists processes with a compute context, matching the
// exec backend's --query-compute-apps output. It fails softly per the
// Reading contract, but lifecycle-class returns still mark the backend for
// re-initialization so the next cycle recovers. The caller holds the
// backend lock.
func (b *Backend) collectComputeApps(ctx context.Context) ([]nvidiasmi.ComputeApp, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("per-process collection interrupted: %w", err)
	}

	count, ret := b.api.deviceCount()
	if ret != nvml.SUCCESS {
		return nil, b.softLifecycle(ret, errors.New("failed to count devices"))
	}

	var apps []nvidiasmi.ComputeApp

	for i := range count {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("per-process collection interrupted: %w", err)
		}

		dev, ret := b.api.deviceByIndex(i)
		if ret != nvml.SUCCESS {
			return nil, b.softLifecycle(ret, fmt.Errorf("failed to get device %d", i))
		}

		uuid, ret := dev.GetUUID()
		if ret != nvml.SUCCESS {
			return nil, b.softLifecycle(ret, fmt.Errorf("failed to get device %d uuid", i))
		}

		procs, ret := dev.GetComputeRunningProcesses()
		if ret != nvml.SUCCESS {
			return nil, b.softLifecycle(ret, fmt.Errorf("failed to list processes of device %d", i))
		}

		for _, proc := range procs {
			name, nret := b.api.processName(int(proc.Pid))
			if nret != nvml.SUCCESS {
				name = tok(nret)
			}

			usedMemory := mib(proc.UsedGpuMemory)
			if proc.UsedGpuMemory == math.MaxUint64 {
				// NVML reports "value unknown" as ~0; nvidia-smi prints the
				// absent token in that case (Windows WDDM behavior)
				usedMemory = tokenNotAvailable
			}

			apps = append(apps, nvidiasmi.ComputeApp{
				GPUUUID:     nvidiasmi.NormalizeUUID(uuid),
				PID:         strconv.FormatUint(uint64(proc.Pid), 10),
				ProcessName: name,
				UsedMemory:  usedMemory,
			})
		}
	}

	return apps, nil
}

// softLifecycle marks the backend for re-initialization on lifecycle-class
// returns but keeps the error plain: per-process failures never fail the
// collection or trigger shutdown-on-error, per the Reading contract.
func (b *Backend) softLifecycle(ret nvml.Return, err error) error {
	if isLifecycleError(ret) {
		b.markLost()
	}

	return fmt.Errorf("%w: %s", err, ret.String())
}
