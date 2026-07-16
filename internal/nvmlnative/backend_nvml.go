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
	"sync/atomic"
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
	init              func() nvml.Return
	shutdown          func() nvml.Return
	deviceCount       func() (int, nvml.Return)
	deviceByIndex     func(int) (nvml.Device, nvml.Return)
	driverVersion     func() (string, nvml.Return)
	cudaDriverVersion func() (int, nvml.Return)
	processName       func(int) (string, nvml.Return)
	validateInforom   func(nvml.Device) nvml.Return
	// the GPM entry points are package-level in go-nvml AND its sample
	// mock cannot pass through the real metrics call (a private concrete
	// type assertion), so they must live on the seam
	gpmSampleAlloc  func() (nvml.GpmSample, nvml.Return)
	gpmSampleFree   func(nvml.GpmSample) nvml.Return
	gpmMigSampleGet func(nvml.Device, int, nvml.GpmSample) nvml.Return
	gpmMetricsGet   func(*nvml.GpmMetricsGetType) nvml.Return
	// eventSetCreate is package-level in go-nvml, hence on the seam; the
	// set's Wait/Free are interface methods and mock naturally
	eventSetCreate func() (nvml.EventSet, nvml.Return)
	// lookupSymbol probes whether the driver library exports a symbol. A
	// getter whose export is missing entirely does NOT answer with a polite
	// FUNCTION_NOT_FOUND return: the lazily bound call crashes the process
	// (observed live on driver 590 with the v2 remapped-rows entry point),
	// so maybe-absent direct getters must be probed before the first call.
	lookupSymbol func(string) error
}

func realNVML() nvmlAPI {
	return nvmlAPI{
		init:          nvml.Init,
		shutdown:      nvml.Shutdown,
		deviceCount:   nvml.DeviceGetCount,
		deviceByIndex: func(i int) (nvml.Device, nvml.Return) { return nvml.DeviceGetHandleByIndex(i) },
		driverVersion: nvml.SystemGetDriverVersion,
		// deliberately the unversioned entry point: the _v2 variant asks
		// libcuda and fails without it, while this one falls back to the
		// driver's known supported version. The utility-only container
		// capability (the documented deployment) injects no libcuda.
		cudaDriverVersion: nvml.SystemGetCudaDriverVersion,
		processName:       nvml.SystemGetProcessName,
		validateInforom:   nvml.DeviceValidateInforom,
		gpmSampleAlloc:    nvml.GpmSampleAlloc,
		gpmSampleFree:     nvml.GpmSampleFree,
		gpmMigSampleGet:   nvml.GpmMigSampleGet,
		gpmMetricsGet:     nvml.GpmMetricsGet,
		eventSetCreate:    nvml.EventSetCreate,
		lookupSymbol:      func(name string) error { return nvml.Extensions().LookupSymbol(name) },
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
	fnfLogged   bool
	// lastCUDAVersion retains the most recent successfully read CUDA version
	// so a transient per-cycle failure does not flap the gpu_info series to
	// an empty cuda_version label. Guarded by mu like the rest of the cycle
	// state.
	lastCUDAVersion string
	// extrasWarned makes a persistent extras failure visible exactly once
	// per family, mirroring permLogged/fnfLogged.
	extrasWarned map[string]bool
	// symbolsPresent caches driver-library symbol probes (constant for the
	// process lifetime). Guarded by mu like the rest of the cycle state.
	symbolsPresent map[string]bool
	// gpm retains one activity sample per GPU instance across cycles, so
	// utilization can be computed over the inter-collection window. Guarded
	// by mu like the rest of the cycle state; every sample is freed before
	// any NVML shutdown.
	gpm map[string]*gpmState
	// now is the clock, injectable so the GPM window guards are testable.
	// Set once at construction, immutable afterwards (the XID watcher reads
	// it without the cycle lock).
	now func() time.Time
	// lifecycleMu is the barrier between NVML shutdown and the XID
	// watcher's in-flight driver calls: the watcher holds it shared around
	// every NVML operation, shutdown takes it exclusively (bounded, so a
	// wedged watcher call cannot hang a collection forever). Lock ordering:
	// mu before lifecycleMu, never the reverse.
	lifecycleMu sync.RWMutex
	// generation counts successful NVML initializations and genLive tells
	// whether the current generation is usable. Atomics: the watcher reads
	// them without the cycle lock, and shutdown must be able to flip
	// genLive even when the exclusive lifecycle lock cannot be acquired.
	generation atomic.Uint64
	genLive    atomic.Bool
	// xids accumulates the XID error events observed by the watcher. It
	// has its own lock and is read at scrape time, outside the snapshot
	// pipeline.
	xids   xidAccumulator
	logger *slog.Logger
}

// New dlopens and initializes NVML. Failure here is a startup error: the
// library or driver is absent or unusable.
func New(logger *slog.Logger) (*Backend, error) {
	return newWithAPI(realNVML(), logger)
}

func newWithAPI(api nvmlAPI, logger *slog.Logger) (*Backend, error) {
	backend := &Backend{api: api, now: time.Now, logger: logger}
	if ret := backend.api.init(); ret != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to initialize NVML: %s", ret.String())
	}

	backend.initialized = true
	backend.generation.Store(1)
	backend.genLive.Store(true)

	return backend, nil
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

	if !b.initialized {
		return
	}

	b.freeGPMSamples()

	b.initialized = false
	b.genLive.Store(false)

	if !b.tryLifecycleLock() {
		b.logger.Warn("skipping NVML shutdown: the XID watcher is stuck inside the driver")

		return
	}
	defer b.lifecycleMu.Unlock()

	_ = b.api.shutdown()
}

// lifecycleErrors are NVML returns that mean the driver/GPU state is gone,
// not that a field is unavailable: the collection fails (never rendering a
// healthy scrape with missing series) and the next cycle re-initializes.
// Verified empirically that these cannot be provoked in software on a healthy
// box (the kernel blocks device removal while a client holds it), so this
// path is covered by mock tests, not captures.
func isLifecycleError(ret nvml.Return) bool {
	//nolint:exhaustive // every other return is by definition not lifecycle-class
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
	//nolint:exhaustive // every unclassified return renders as the unknown token
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
func (b *Backend) QueryFunc(fields nvidiasmi.ResolvedFields, opts CollectOptions) collect.QueryFunc {
	return func(ctx context.Context) (collect.Reading, int, error) {
		type outcome struct {
			reading collect.Reading
			code    int
			err     error
		}

		resultCh := make(chan outcome, 1)

		go func() {
			reading, code, err := b.collectCycle(ctx, fields, opts)
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
	opts CollectOptions,
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

	if opts.ComputeApps {
		reading.AppsAttempted = true

		apps, appsErr := b.collectComputeApps(ctx)
		if appsErr != nil {
			reading.AppsErr = appsErr
		} else {
			reading.Apps = apps
			reading.AppsSuccess = true
		}
	}

	reading.Extras = b.collectExtras(ctx, opts)

	return reading, code, nil
}

// collectTable runs one GPU collection cycle over all devices. The caller
// holds the backend lock.
//
//nolint:cyclop,funlen // one linear pass: init, count, per-device collect, assemble
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
		b.generation.Add(1)
		b.genLive.Store(true)
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

	for deviceIdx := range count {
		if err := ctx.Err(); err != nil {
			return nil, -1, fmt.Errorf("collection interrupted: %w", err)
		}

		dev, ret := b.api.deviceByIndex(deviceIdx)
		if ret != nvml.SUCCESS {
			return nil, int(ret), b.lifecycle(ret, fmt.Errorf("failed to get device %d", deviceIdx))
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

		for _, qField := range fields.Query {
			raw, ok := values[canonicalQField(qField)]
			if !ok {
				// a catalogued field this collector version has no reading
				// for: emit the absent token so the behavior matches an
				// unsupported field instead of a hole in the table
				raw = tokenNotAvailable
			}

			cell := nvidiasmi.Cell{QField: qField, RField: fields.Returned[qField], RawValue: raw}
			cells = append(cells, cell)
			rowCells[qField] = cell
			qFieldToCells[qField] = append(qFieldToCells[qField], cell)
		}

		rows = append(rows, nvidiasmi.Row{QFieldToCells: rowCells, Cells: cells})
	}

	rFields := make([]nvidiasmi.RField, len(fields.Query))
	for i, qField := range fields.Query {
		rFields[i] = fields.Returned[qField]
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

// markLost flags the backend for re-initialization on the next cycle. The
// retained GPM samples die with the NVML generation, so they are freed
// (best-effort) before the shutdown, and the generation is declared dead
// first so the XID watcher stops touching it even when the shutdown itself
// has to be skipped behind a wedged watcher call.
func (b *Backend) markLost() {
	if !b.initialized {
		return
	}

	b.freeGPMSamples()

	b.initialized = false
	b.genLive.Store(false)

	if !b.tryLifecycleLock() {
		b.logger.Warn("skipping NVML shutdown: the XID watcher is stuck inside the driver")

		return
	}
	defer b.lifecycleMu.Unlock()

	_ = b.api.shutdown()
}

// lifecycleLockDeadline bounds how long a shutdown waits for the XID watcher
// to leave the driver. The watcher's wait calls are themselves bounded to a
// second, so the deadline only fires when a driver call is genuinely wedged.
const lifecycleLockDeadline = 2 * time.Second

// tryLifecycleLock acquires the exclusive lifecycle lock, bounded: process
// health must never hang forever behind an unkillable driver call. The
// acquisition must BLOCK (in a goroutine raced against the deadline) rather
// than poll TryLock: only a pending blocking acquisition gets writer
// priority over the watcher, which holds the shared lock for the full
// duration of each bounded driver wait and releases it only momentarily
// (verified empirically: a TryLock poll virtually never lands in that gap).
func (b *Backend) tryLifecycleLock() bool {
	acquired := make(chan struct{})

	go func() {
		b.lifecycleMu.Lock()
		close(acquired)
	}()

	select {
	case <-acquired:
		return true
	case <-time.After(lifecycleLockDeadline):
		// the pending acquisition will land eventually (when the wedged
		// call returns); it must release immediately then, or the watcher
		// would starve forever afterwards
		go func() {
			<-acquired
			b.lifecycleMu.Unlock()
		}()

		return false
	}
}

// tryRecover is the watcher's scrape-independent recovery path: when event
// registration fails because the driver generation died, waiting for a
// scrape to re-initialize NVML would leave event collection deaf on an idle
// exporter. It funnels through the same state transitions as a collection
// cycle, best-effort: when a cycle holds the lock, that cycle handles the
// recovery anyway.
func (b *Backend) tryRecover() {
	if !b.mu.TryLock() {
		return
	}
	defer b.mu.Unlock()

	if b.initialized {
		// the generation died without the cycle path noticing yet
		b.markLost()
	}

	if ret := b.api.init(); ret != nvml.SUCCESS {
		return
	}

	b.logger.Info("re-initialized NVML after a lifecycle error (event watcher recovery)")

	b.initialized = true
	b.generation.Add(1)
	b.genLive.Store(true)
}

// lifecycleRetError carries a lifecycle-class return code out of a device
// collection pass.
type lifecycleRetError struct {
	ret nvml.Return
}

func (e *lifecycleRetError) Error() string {
	return "device collection hit a lifecycle error: " + e.ret.String()
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
	for _, qField := range fields.Query {
		requested[canonicalQField(qField)] = true
	}

	return plan{requested: requested}
}

// want reports whether any of the given fields was requested.
func (reqs plan) want(fields ...nvidiasmi.QField) bool {
	for _, f := range fields {
		if reqs.requested[f] {
			return true
		}
	}

	return false
}

// devCollector accumulates one device's readings and remembers the first
// lifecycle-class return and any permission-denied fields it sees.
type devCollector struct {
	values   map[nvidiasmi.QField]string
	fatal    *nvml.Return
	denied   []nvidiasmi.QField
	notFound []nvidiasmi.QField
}

// classify tracks a non-success return's collection-level consequences.
func (c *devCollector) classify(field nvidiasmi.QField, ret nvml.Return) {
	if isLifecycleError(ret) && c.fatal == nil {
		c.fatal = &ret
	}

	if ret == nvml.ERROR_NO_PERMISSION {
		c.denied = append(c.denied, field)
	}

	if ret == nvml.ERROR_FUNCTION_NOT_FOUND {
		c.notFound = append(c.notFound, field)
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
//nolint:funlen,maintidx,cyclop,gocognit,gocyclo,goconst // deliberately one linear pass mirroring the catalog order
func (b *Backend) collectDevice(
	ctx context.Context,
	dev nvml.Device,
	shared sharedValues,
	reqs plan,
) (map[nvidiasmi.QField]string, error) {
	coll := &devCollector{values: make(map[nvidiasmi.QField]string, len(fieldOrder))}

	coll.values["timestamp"] = shared.timestamp
	coll.set("driver_version", shared.driverVersionRet, func() string { return shared.driverVersion })
	coll.set("count", shared.countRet, func() string { return strconv.Itoa(shared.count) })

	if reqs.want("name") {
		name, ret := dev.GetName()
		coll.set("name", ret, func() string { return name })
	}

	if reqs.want("serial") {
		serial, ret := dev.GetSerial()
		coll.set("serial", ret, func() string { return serial })
	}

	if reqs.want("uuid") {
		uuid, ret := dev.GetUUID()
		coll.set("uuid", ret, func() string { return uuid })
	}

	if reqs.want("index") {
		index, ret := dev.GetIndex()
		coll.set("index", ret, func() string { return strconv.Itoa(index) })
	}

	if reqs.want("pci.bus_id", "pci.domain", "pci.bus", "pci.device", "pci.baseClass",
		"pci.subClass", "pci.device_id", "pci.sub_device_id") {
		pci, ret := dev.GetPciInfoExt()
		coll.set("pci.bus_id", ret, func() string { return i8str(pci.BusId[:]) })
		coll.set("pci.domain", ret, func() string { return fmt.Sprintf("0x%04X", pci.Domain) })
		coll.set("pci.bus", ret, func() string { return fmt.Sprintf("0x%02X", pci.Bus) })
		coll.set("pci.device", ret, func() string { return fmt.Sprintf("0x%02X", pci.Device) })
		coll.set("pci.baseClass", ret, func() string { return fmt.Sprintf("0x%X", pci.BaseClass) })
		coll.set("pci.subClass", ret, func() string { return fmt.Sprintf("0x%X", pci.SubClass) })
		coll.set("pci.device_id", ret, func() string { return fmt.Sprintf("0x%08X", pci.PciDeviceId) })
		coll.set("pci.sub_device_id", ret, func() string { return fmt.Sprintf("0x%08X", pci.PciSubSystemId) })
	}

	if reqs.want("pcie.link.gen.current", "pcie.link.gen.gpucurrent") {
		gen, ret := dev.GetCurrPcieLinkGeneration()
		coll.set("pcie.link.gen.current", ret, func() string { return strconv.Itoa(gen) })
		coll.set("pcie.link.gen.gpucurrent", ret, func() string { return strconv.Itoa(gen) })
	}

	if reqs.want("pcie.link.gen.max") {
		maxGen, ret := dev.GetMaxPcieLinkGeneration()
		coll.set("pcie.link.gen.max", ret, func() string { return strconv.Itoa(maxGen) })
	}

	if reqs.want("pcie.link.gen.gpumax") {
		gpuMaxGen, ret := dev.GetGpuMaxPcieLinkGeneration()
		coll.set("pcie.link.gen.gpumax", ret, func() string { return strconv.Itoa(gpuMaxGen) })
	}

	if reqs.want("pcie.link.width.current") {
		width, ret := dev.GetCurrPcieLinkWidth()
		coll.set("pcie.link.width.current", ret, func() string { return strconv.Itoa(width) })
	}

	if reqs.want("pcie.link.width.max") {
		maxWidth, ret := dev.GetMaxPcieLinkWidth()
		coll.set("pcie.link.width.max", ret, func() string { return strconv.Itoa(maxWidth) })
	}

	// the deprecation-listed fields (see deprecatedFields) get the token
	// regardless of what NVML would answer, matching nvidia-smi, which does
	// not even call the getters for them. Pinned to the verified driver
	// generation; the corpus drift test guards the list.
	for _, field := range deprecatedFields {
		coll.values[field] = tokenDeprecated
	}

	if reqs.want("display_attached") {
		dispMode, ret := dev.GetDisplayMode()
		coll.set("display_attached", ret, func() string { return yesNo(dispMode == nvml.FEATURE_ENABLED) })
	}

	if reqs.want("display_active") {
		dispActive, ret := dev.GetDisplayActive()
		coll.set("display_active", ret, func() string { return onOff(dispActive == nvml.FEATURE_ENABLED) })
	}

	if reqs.want("persistence_mode") {
		persistence, ret := dev.GetPersistenceMode()
		coll.set("persistence_mode", ret, func() string { return onOff(persistence == nvml.FEATURE_ENABLED) })
	}

	if reqs.want("addressing_mode") {
		addrMode, ret := dev.GetAddressingMode()
		coll.set("addressing_mode", ret, func() string { return addressingModeStr(addrMode.Value) })
	}

	if reqs.want("accounting.mode") {
		accMode, ret := dev.GetAccountingMode()
		coll.set("accounting.mode", ret, func() string { return onOff(accMode == nvml.FEATURE_ENABLED) })
	}

	if reqs.want("accounting.buffer_size") {
		accBuf, ret := dev.GetAccountingBufferSize()
		coll.set("accounting.buffer_size", ret, func() string { return strconv.Itoa(accBuf) })
	}

	if reqs.want("driver_model.current", "driver_model.pending") {
		dmCur, dmPend, ret := dev.GetDriverModel()
		coll.set("driver_model.current", ret, func() string { return driverModelStr(int32(dmCur)) })
		coll.set("driver_model.pending", ret, func() string { return driverModelStr(int32(dmPend)) })
	}

	if reqs.want("vbios_version") {
		vbios, ret := dev.GetVbiosVersion()
		coll.set("vbios_version", ret, func() string { return vbios })
	}

	if reqs.want("inforom.img") {
		img, ret := dev.GetInforomImageVersion()
		coll.set("inforom.img", ret, func() string { return img })
	}

	if reqs.want("inforom.oem") {
		oem, ret := dev.GetInforomVersion(nvml.INFOROM_OEM)
		coll.set("inforom.oem", ret, func() string { return oem })
	}

	if reqs.want("inforom.ecc") {
		eccVer, ret := dev.GetInforomVersion(nvml.INFOROM_ECC)
		coll.set("inforom.ecc", ret, func() string { return eccVer })
	}

	if reqs.want("inforom.pwr") {
		pwr, ret := dev.GetInforomVersion(nvml.INFOROM_POWER)
		coll.set("inforom.pwr", ret, func() string { return pwr })
	}

	if reqs.want("inforom.checksum_validation") {
		// "valid" is capture-verified; the corruption spelling is not
		ret := b.api.validateInforom(dev)
		coll.set("inforom.checksum_validation", ret, func() string { return "valid" })
	}

	if reqs.want("gom.current", "gom.pending") {
		gomCur, gomPend, ret := dev.GetGpuOperationMode()
		coll.set("gom.current", ret, func() string { return gomStr(int32(gomCur)) })
		coll.set("gom.pending", ret, func() string { return gomStr(int32(gomPend)) })
	}

	if reqs.want("fan.speed") {
		fan, ret := dev.GetFanSpeed()
		coll.set("fan.speed", ret, func() string { return pct(fan) })
	}

	if reqs.want("pstate") {
		pstate, ret := dev.GetPerformanceState()
		coll.set("pstate", ret, func() string { return fmt.Sprintf("P%d", pstate) })
	}

	if reqs.want("clocks_event_reasons.supported") {
		supported, ret := dev.GetSupportedClocksEventReasons()
		coll.set("clocks_event_reasons.supported", ret,
			func() string { return fmt.Sprintf("0x%016X", supported) })
	}

	if reqs.want("clocks_event_reasons.active", "clocks_event_reasons.gpu_idle",
		"clocks_event_reasons.applications_clocks_setting", "clocks_event_reasons.sw_power_cap",
		"clocks_event_reasons.hw_slowdown", "clocks_event_reasons.sync_boost",
		"clocks_event_reasons.sw_thermal_slowdown", "clocks_event_reasons.hw_thermal_slowdown",
		"clocks_event_reasons.hw_power_brake_slowdown") {
		active, ret := dev.GetCurrentClocksEventReasons()
		coll.set("clocks_event_reasons.active", ret, func() string { return fmt.Sprintf("0x%016X", active) })

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
			if !reqs.want(field) {
				// keep the permission diagnostics limited to requested fields
				continue
			}

			coll.set(field, ret, func() string { return activeNotActive(active, bit) })
		}
	}

	b.collectFieldValues(dev, coll, reqs)

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("device collection interrupted: %w", err)
	}

	if reqs.want("memory.total", "memory.reserved", "memory.used", "memory.free") {
		mem, ret := dev.GetMemoryInfo_v2()
		coll.set("memory.total", ret, func() string { return mib(mem.Total) })
		coll.set("memory.reserved", ret, func() string { return mib(mem.Reserved) })
		coll.set("memory.used", ret, func() string { return mib(mem.Used) })
		coll.set("memory.free", ret, func() string { return mib(mem.Free) })
	}

	if reqs.want("compute_mode") {
		cm, ret := dev.GetComputeMode()
		coll.set("compute_mode", ret, func() string { return computeModeStr(int32(cm)) })
	}

	if reqs.want("compute_cap") {
		major, minor, ret := dev.GetCudaComputeCapability()
		coll.set("compute_cap", ret, func() string { return fmt.Sprintf("%d.%d", major, minor) })
	}

	if reqs.want("utilization.gpu", "utilization.memory") {
		util, ret := dev.GetUtilizationRates()
		coll.set("utilization.gpu", ret, func() string { return pct(util.Gpu) })
		coll.set("utilization.memory", ret, func() string { return pct(util.Memory) })
	}

	if reqs.want("utilization.encoder") {
		encUtil, _, ret := dev.GetEncoderUtilization()
		coll.set("utilization.encoder", ret, func() string { return pct(encUtil) })
	}

	if reqs.want("utilization.decoder") {
		decUtil, _, ret := dev.GetDecoderUtilization()
		coll.set("utilization.decoder", ret, func() string { return pct(decUtil) })
	}

	if reqs.want("utilization.jpeg") {
		jpgUtil, _, ret := dev.GetJpgUtilization()
		coll.set("utilization.jpeg", ret, func() string { return pct(jpgUtil) })
	}

	if reqs.want("utilization.ofa") {
		ofaUtil, _, ret := dev.GetOfaUtilization()
		coll.set("utilization.ofa", ret, func() string { return pct(ofaUtil) })
	}

	if reqs.want("encoder.stats.sessionCount", "encoder.stats.averageFps", "encoder.stats.averageLatency") {
		sessions, fps, latency, ret := dev.GetEncoderStats()
		coll.set("encoder.stats.sessionCount", ret, func() string { return strconv.Itoa(sessions) })
		coll.set("encoder.stats.averageFps", ret, func() string { return strconv.FormatUint(uint64(fps), 10) })
		coll.set("encoder.stats.averageLatency", ret, func() string { return strconv.FormatUint(uint64(latency), 10) })
	}

	if reqs.want("dramEncryption.mode.current", "dramEncryption.mode.pending") {
		dramCur, dramPend, ret := dev.GetDramEncryptionMode()
		coll.set("dramEncryption.mode.current", ret, func() string { return onOff(dramCur.EncryptionState != 0) })
		coll.set("dramEncryption.mode.pending", ret, func() string { return onOff(dramPend.EncryptionState != 0) })
	}

	if reqs.want("ecc.mode.current", "ecc.mode.pending") {
		eccCur, eccPend, ret := dev.GetEccMode()
		coll.set("ecc.mode.current", ret, func() string { return onOff(eccCur == nvml.FEATURE_ENABLED) })
		coll.set("ecc.mode.pending", ret, func() string { return onOff(eccPend == nvml.FEATURE_ENABLED) })
	}

	collectEccCounters(dev, coll, reqs)
	collectSramEcc(dev, coll, reqs)

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("device collection interrupted: %w", err)
	}

	if reqs.want("retired_pages.single_bit_ecc.count") {
		sbePages, ret := dev.GetRetiredPages(nvml.PAGE_RETIREMENT_CAUSE_MULTIPLE_SINGLE_BIT_ECC_ERRORS)
		coll.set("retired_pages.single_bit_ecc.count", ret, func() string { return strconv.Itoa(len(sbePages)) })
	}

	if reqs.want("retired_pages.double_bit.count") {
		dbePages, ret := dev.GetRetiredPages(nvml.PAGE_RETIREMENT_CAUSE_DOUBLE_BIT_ECC_ERROR)
		coll.set("retired_pages.double_bit.count", ret, func() string { return strconv.Itoa(len(dbePages)) })
	}

	if reqs.want("retired_pages.pending") {
		retiredPending, ret := dev.GetRetiredPagesPendingStatus()
		coll.set("retired_pages.pending", ret, func() string { return yesNo(retiredPending == nvml.FEATURE_ENABLED) })
	}

	if reqs.want("remapped_rows.correctable_inactive", "remapped_rows.uncorrectable_inactive") {
		// only the inactive counts come from the v2 call: the four classic
		// fields stay on the unversioned call, so drivers without the v2
		// entry point keep serving them (the inactive series just stay
		// absent there). The export must be probed first: driver 590 ships
		// no v2 symbol at all, and calling a missing export crashes the
		// process instead of returning FUNCTION_NOT_FOUND (verified live).
		var info nvml.RemappedRowsInfo_v2

		ret := nvml.ERROR_FUNCTION_NOT_FOUND
		if b.symbolPresent("nvmlDeviceGetRemappedRows_v2") {
			info, ret = dev.GetRemappedRows_v2()
		}

		coll.set("remapped_rows.correctable_inactive", ret,
			func() string { return strconv.FormatUint(uint64(info.CorrInactiveRemaps), 10) })
		coll.set("remapped_rows.uncorrectable_inactive", ret,
			func() string { return strconv.FormatUint(uint64(info.UncInactiveRemaps), 10) })
	}

	if reqs.want("remapped_rows.correctable", "remapped_rows.uncorrectable",
		"remapped_rows.pending", "remapped_rows.failure") {
		corrRows, uncRows, isPending, failed, ret := dev.GetRemappedRows()
		coll.set("remapped_rows.correctable", ret, func() string { return strconv.Itoa(corrRows) })
		coll.set("remapped_rows.uncorrectable", ret, func() string { return strconv.Itoa(uncRows) })
		coll.set("remapped_rows.pending", ret, func() string { return yesNo(isPending) })
		coll.set("remapped_rows.failure", ret, func() string { return yesNo(failed) })
	}

	if reqs.want("remapped_rows.histogram.max", "remapped_rows.histogram.high",
		"remapped_rows.histogram.partial", "remapped_rows.histogram.low", "remapped_rows.histogram.none") {
		hist, ret := dev.GetRowRemapperHistogram()
		coll.set("remapped_rows.histogram.max", ret, func() string { return strconv.FormatUint(uint64(hist.Max), 10) })
		coll.set(
			"remapped_rows.histogram.high",
			ret,
			func() string { return strconv.FormatUint(uint64(hist.High), 10) },
		)
		coll.set("remapped_rows.histogram.partial", ret,
			func() string { return strconv.FormatUint(uint64(hist.Partial), 10) })
		coll.set("remapped_rows.histogram.low", ret, func() string { return strconv.FormatUint(uint64(hist.Low), 10) })
		coll.set(
			"remapped_rows.histogram.none",
			ret,
			func() string { return strconv.FormatUint(uint64(hist.None), 10) },
		)
	}

	if reqs.want("temperature.gpu") {
		temp, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
		coll.set("temperature.gpu", ret, func() string { return strconv.FormatUint(uint64(temp), 10) })
	}

	if reqs.want("temperature.gpu.tlimit") {
		margin, ret := dev.GetMarginTemperature()
		coll.set("temperature.gpu.tlimit", ret, func() string {
			return strconv.FormatInt(int64(margin.MarginTemperature), 10)
		})
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("device collection interrupted: %w", err)
	}

	if reqs.want("power.draw") {
		powerUsage, ret := dev.GetPowerUsage()
		coll.set("power.draw", ret, func() string { return milliwatts(powerUsage) })
	}

	if reqs.want("power.limit") {
		limit, ret := dev.GetPowerManagementLimit()
		coll.set("power.limit", ret, func() string { return milliwatts(limit) })
	}

	if reqs.want("enforced.power.limit") {
		enforced, ret := dev.GetEnforcedPowerLimit()
		coll.set("enforced.power.limit", ret, func() string { return milliwatts(enforced) })
	}

	if reqs.want("power.default_limit") {
		defLimit, ret := dev.GetPowerManagementDefaultLimit()
		coll.set("power.default_limit", ret, func() string { return milliwatts(defLimit) })
	}

	if reqs.want("power.min_limit", "power.max_limit") {
		minLimit, maxLimit, ret := dev.GetPowerManagementLimitConstraints()
		coll.set("power.min_limit", ret, func() string { return milliwatts(minLimit) })
		coll.set("power.max_limit", ret, func() string { return milliwatts(maxLimit) })
	}

	for field, clockType := range map[nvidiasmi.QField]nvml.ClockType{
		"clocks.current.graphics": nvml.CLOCK_GRAPHICS,
		"clocks.current.sm":       nvml.CLOCK_SM,
		"clocks.current.memory":   nvml.CLOCK_MEM,
		"clocks.current.video":    nvml.CLOCK_VIDEO,
	} {
		if !reqs.want(field) {
			continue
		}

		clock, cret := dev.GetClockInfo(clockType)
		coll.set(field, cret, func() string { return mhz(clock) })
	}

	for field, clockType := range map[nvidiasmi.QField]nvml.ClockType{
		"clocks.max.graphics": nvml.CLOCK_GRAPHICS,
		"clocks.max.sm":       nvml.CLOCK_SM,
		"clocks.max.memory":   nvml.CLOCK_MEM,
	} {
		if !reqs.want(field) {
			continue
		}

		clock, cret := dev.GetMaxClockInfo(clockType)
		coll.set(field, cret, func() string { return mhz(clock) })
	}

	if reqs.want("mig.mode.current", "mig.mode.pending") {
		migCur, migPend, ret := dev.GetMigMode()
		coll.set("mig.mode.current", ret, func() string { return onOff(migCur == 1) })
		coll.set("mig.mode.pending", ret, func() string { return onOff(migPend == 1) })
	}

	if reqs.want("gsp.mode.current", "gsp.mode.default") {
		gspEnabled, gspDefault, ret := dev.GetGspFirmwareMode()
		coll.set("gsp.mode.current", ret, func() string { return onOff(gspEnabled) })
		coll.set("gsp.mode.default", ret, func() string { return onOff(gspDefault) })
	}

	if reqs.want("c2c.mode") {
		c2c, ret := dev.GetC2cModeInfoV().V1()
		coll.set("c2c.mode", ret, func() string { return onOff(c2c.IsC2cEnabled != 0) })
	}

	if reqs.want("protected_memory.total", "protected_memory.used", "protected_memory.free") {
		protected, ret := dev.GetConfComputeProtectedMemoryUsage()
		coll.set("protected_memory.total", ret, func() string { return mib(protected.Total) })
		coll.set("protected_memory.used", ret, func() string { return mib(protected.Used) })
		coll.set("protected_memory.free", ret, func() string { return mib(protected.Free) })
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("device collection interrupted: %w", err)
	}

	collectFabric(dev, coll, reqs)
	collectPlatform(dev, coll, reqs)

	if reqs.want("hostname") {
		hostname, ret := dev.GetHostname_v1()
		coll.set("hostname", ret, func() string { return hostname })
	}

	if len(coll.denied) > 0 && !b.permLogged {
		b.permLogged = true

		b.logger.Warn("some NVML readings are permission-denied and will be absent",
			"fields", fmt.Sprintf("%v", coll.denied))
	}

	// a required NVML function being unavailable usually means driver drift
	// (the library dropped or renamed an entry point this backend expects);
	// users on brand-new drivers are the earliest signal, so say it once
	if len(coll.notFound) > 0 && !b.fnfLogged {
		b.fnfLogged = true

		b.logger.Warn("required NVML functions are unavailable in this driver; "+
			"the affected fields will be absent - please report this on the project's issue tracker",
			"fields", fmt.Sprintf("%v", coll.notFound),
			"driver_version", shared.driverVersion)
	}

	if coll.fatal != nil {
		return nil, &lifecycleRetError{ret: *coll.fatal}
	}

	return coll.values, nil
}

// collectFieldValues batches everything served by nvmlDeviceGetFieldValues.
// The field-ID choices are trace-verified against nvidia-smi's own calls.
//
//nolint:cyclop,funlen // one linear pass over the batched entries and their outcomes
func (b *Backend) collectFieldValues(dev nvml.Device, coll *devCollector, reqs plan) {
	entries := []struct {
		field  nvidiasmi.QField
		id     uint32
		bare   bool
		format func(v float64) string
	}{
		{
			"power.draw.average", nvml.FI_DEV_POWER_AVERAGE, false,
			func(v float64) string { return fmt.Sprintf("%.2f W", v/1000.0) },
		},
		{
			"power.draw.instant", nvml.FI_DEV_POWER_INSTANT, false,
			func(v float64) string { return fmt.Sprintf("%.2f W", v/1000.0) },
		},
		{
			"temperature.memory", nvml.FI_DEV_MEMORY_TEMP, true,
			func(v float64) string { return strconv.FormatInt(int64(v), 10) },
		},
		{
			"clocks_event_reasons_counters.sw_power_cap", nvml.FI_DEV_CLOCKS_EVENT_REASON_SW_POWER_CAP, false,
			func(v float64) string { return fmt.Sprintf("%d us", int64(v)) },
		},
		{
			"clocks_event_reasons_counters.sync_boost", nvml.FI_DEV_CLOCKS_EVENT_REASON_SYNC_BOOST, false,
			func(v float64) string { return fmt.Sprintf("%d us", int64(v)) },
		},
		{
			"clocks_event_reasons_counters.sw_thermal_slowdown",
			nvml.FI_DEV_CLOCKS_EVENT_REASON_SW_THERM_SLOWDOWN,
			false,
			func(v float64) string { return fmt.Sprintf("%d us", int64(v)) },
		},
		{
			"clocks_event_reasons_counters.hw_thermal_slowdown",
			nvml.FI_DEV_CLOCKS_EVENT_REASON_HW_THERM_SLOWDOWN,
			false,
			func(v float64) string { return fmt.Sprintf("%d us", int64(v)) },
		},
		{
			"clocks_event_reasons_counters.hw_power_brake_slowdown",
			nvml.FI_DEV_CLOCKS_EVENT_REASON_HW_POWER_BRAKE_SLOWDOWN, false,
			func(v float64) string { return fmt.Sprintf("%d us", int64(v)) },
		},
		{
			"gpu_recovery_action", nvml.FI_DEV_GET_GPU_RECOVERY_ACTION, false,
			func(v float64) string { return recoveryActionStr(uint64(v)) },
		},
		{
			"edpp_multiplier", edppMultiplierFieldID, false,
			func(v float64) string { return fmt.Sprintf("%.2f %%", v) },
		},
	}

	wanted := entries[:0]

	for _, entry := range entries {
		if reqs.want(entry.field) {
			wanted = append(wanted, entry)
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
		for _, entry := range wanted {
			if entry.bare {
				coll.setBare(entry.field, ret, nil)
			} else {
				coll.set(entry.field, ret, nil)
			}
		}

		return
	}

	for i, entry := range wanted {
		fieldValue := values[i]
		//nolint:gosec // G115: the field carries an nvmlReturn_t
		fret := nvml.Return(fieldValue.NvmlReturn)
		value, decodeOK := decodeFieldValue(fieldValue)

		switch {
		case fret != nvml.SUCCESS && entry.bare:
			coll.setBare(entry.field, fret, nil)
		case fret != nvml.SUCCESS:
			coll.set(entry.field, fret, nil)
		case !decodeOK:
			// an unknown value type from a newer driver: absent, never a panic
			b.logger.Warn("cannot decode NVML field value",
				"field", entry.field, "value_type", fieldValue.ValueType)
			coll.set(entry.field, nvml.SUCCESS, nil)
		default:
			coll.values[entry.field] = entry.format(value)
		}
	}
}

//nolint:gosec // G115: reinterpreting the C value union's bytes is the point
func decodeFieldValue(fv nvml.FieldValue) (float64, bool) {
	raw := fv.Value[:]

	switch fv.ValueType {
	case uint32(nvml.VALUE_TYPE_DOUBLE):
		return math.Float64frombits(binary.LittleEndian.Uint64(raw)), true
	case uint32(nvml.VALUE_TYPE_UNSIGNED_INT):
		return float64(binary.LittleEndian.Uint32(raw)), true
	case uint32(nvml.VALUE_TYPE_UNSIGNED_LONG), uint32(nvml.VALUE_TYPE_UNSIGNED_LONG_LONG):
		return float64(binary.LittleEndian.Uint64(raw)), true
	case uint32(nvml.VALUE_TYPE_SIGNED_LONG_LONG):
		return float64(int64(binary.LittleEndian.Uint64(raw))), true
	case uint32(nvml.VALUE_TYPE_SIGNED_INT):
		return float64(int32(binary.LittleEndian.Uint32(raw))), true
	default:
		return 0, false
	}
}

func collectEccCounters(dev nvml.Device, coll *devCollector, reqs plan) {
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
				if !reqs.want(field) {
					continue
				}

				v, ret := dev.GetMemoryErrorCounter(errType, cntType, loc)
				coll.set(field, ret, func() string { return strconv.FormatUint(v, 10) })
			}

			field := nvidiasmi.QField(fmt.Sprintf("ecc.errors.%s.%s.total", kindName, cntName))
			if !reqs.want(field) {
				continue
			}

			v, ret := dev.GetTotalEccErrors(errType, cntType)
			coll.set(field, ret, func() string { return strconv.FormatUint(v, 10) })
		}
	}
}

func collectSramEcc(dev nvml.Device, coll *devCollector, reqs plan) {
	sramFields := []nvidiasmi.QField{
		"ecc.errors.uncorrected.volatile.sram.parity", "ecc.errors.uncorrected.volatile.sram.secded",
		"ecc.errors.uncorrected.aggregate.sram.parity", "ecc.errors.uncorrected.aggregate.sram.secded",
		"ecc.errors.uncorrected.aggregate.sram.thresholdExceeded",
		"ecc.errors.uncorrected.aggregate.sram.l2", "ecc.errors.uncorrected.aggregate.sram.sm",
		"ecc.errors.uncorrected.aggregate.sram.mcu", "ecc.errors.uncorrected.aggregate.sram.pcie",
		"ecc.errors.uncorrected.aggregate.sram.other",
	}
	if !reqs.want(sramFields...) {
		return
	}

	status, ret := dev.GetSramEccErrorStatus()

	set := func(field nvidiasmi.QField, v uint64) {
		coll.set(field, ret, func() string { return strconv.FormatUint(v, 10) })
	}

	set("ecc.errors.uncorrected.volatile.sram.parity", status.VolatileUncParity)
	set("ecc.errors.uncorrected.volatile.sram.secded", status.VolatileUncSecDed)
	set("ecc.errors.uncorrected.aggregate.sram.parity", status.AggregateUncParity)
	set("ecc.errors.uncorrected.aggregate.sram.secded", status.AggregateUncSecDed)
	coll.set("ecc.errors.uncorrected.aggregate.sram.thresholdExceeded", ret,
		func() string { return yesNo(status.BThresholdExceeded != 0) })
	set("ecc.errors.uncorrected.aggregate.sram.l2", status.AggregateUncBucketL2)
	set("ecc.errors.uncorrected.aggregate.sram.sm", status.AggregateUncBucketSm)
	set("ecc.errors.uncorrected.aggregate.sram.mcu", status.AggregateUncBucketMcu)
	set("ecc.errors.uncorrected.aggregate.sram.pcie", status.AggregateUncBucketPcie)
	set("ecc.errors.uncorrected.aggregate.sram.other", status.AggregateUncBucketOther)
}

// collectFabric fills the fabric.* fields. When there is no fabric (state 0
// or the call fails), every field prints the bare N/A token
// (capture-verified). Lifecycle-class failures still poison the cycle.
func collectFabric(dev nvml.Device, coll *devCollector, reqs plan) {
	fabricFields := []nvidiasmi.QField{
		"fabric.state", "fabric.status", "fabric.cliqueId", "fabric.clusterUuid",
	}
	if !reqs.want(fabricFields...) {
		return
	}

	info, ret := dev.GetGpuFabricInfoV().V2()
	if ret != nvml.SUCCESS || info.State == 0 {
		coll.classify("fabric.state", ret)

		for _, f := range fabricFields {
			coll.values[f] = tokenBareNotAvailable
		}

		return
	}

	coll.values["fabric.state"] = fabricStateStr(info.State)

	status := "Success"

	fabricStatus := nvml.Return(info.Status) //nolint:gosec // G115: the field carries an nvmlReturn_t
	if fabricStatus != nvml.SUCCESS {
		// a completed probe can still carry a lifecycle-class status
		coll.classify("fabric.status", fabricStatus)

		status = fabricStatus.String()
	}

	coll.values["fabric.status"] = status
	coll.values["fabric.cliqueId"] = strconv.FormatUint(uint64(info.CliqueId), 10)
	coll.values["fabric.clusterUuid"] = uuidBytes(info.ClusterUuid)
}

func collectPlatform(dev nvml.Device, coll *devCollector, reqs plan) {
	if !reqs.want("platform.chassis_serial_number", "platform.slot_number", "platform.tray_index",
		"platform.host_id", "platform.peer_type", "platform.module_id", "platform.gpu_fabric_guid") {
		return
	}

	info, ret := dev.GetPlatformInfo()

	coll.set("platform.chassis_serial_number", ret, func() string { return cstr(info.ChassisSerialNumber[:]) })
	coll.set("platform.slot_number", ret, func() string { return strconv.FormatUint(uint64(info.SlotNumber), 10) })
	coll.set("platform.tray_index", ret, func() string { return strconv.FormatUint(uint64(info.TrayIndex), 10) })
	coll.set("platform.host_id", ret, func() string { return strconv.FormatUint(uint64(info.HostId), 10) })
	coll.set("platform.peer_type", ret, func() string {
		if info.PeerType == 0 {
			return "Direct Connected"
		}

		return fmt.Sprintf("Unknown(%d)", info.PeerType)
	})
	coll.set("platform.module_id", ret, func() string { return strconv.FormatUint(uint64(info.ModuleId), 10) })
	coll.set("platform.gpu_fabric_guid", ret, func() string {
		return fmt.Sprintf("0x%016X", binary.BigEndian.Uint64(info.IbGuid[:8]))
	})
}

// collectComputeApps lists processes with a compute context, matching the
// exec backend's --query-compute-apps output. It fails softly per the
// Reading contract, but lifecycle-class returns still mark the backend for
// re-initialization so the next cycle recovers. The caller holds the
// backend lock.
//
//nolint:cyclop // one linear pass over devices and their process lists
func (b *Backend) collectComputeApps(ctx context.Context) ([]nvidiasmi.ComputeApp, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("per-process collection interrupted: %w", err)
	}

	count, ret := b.api.deviceCount()
	if ret != nvml.SUCCESS {
		return nil, b.softLifecycle(ret, errors.New("failed to count devices"))
	}

	var apps []nvidiasmi.ComputeApp

	for deviceIdx := range count {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("per-process collection interrupted: %w", err)
		}

		dev, ret := b.api.deviceByIndex(deviceIdx)
		if ret != nvml.SUCCESS {
			return nil, b.softLifecycle(ret, fmt.Errorf("failed to get device %d", deviceIdx))
		}

		uuid, ret := dev.GetUUID()
		if ret != nvml.SUCCESS {
			return nil, b.softLifecycle(ret, fmt.Errorf("failed to get device %d uuid", deviceIdx))
		}

		procs, ret := dev.GetComputeRunningProcesses()
		if ret != nvml.SUCCESS {
			return nil, b.softLifecycle(ret, fmt.Errorf("failed to list processes of device %d", deviceIdx))
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
				GPUUUID:           nvidiasmi.NormalizeUUID(uuid),
				PID:               strconv.FormatUint(uint64(proc.Pid), 10),
				ProcessName:       name,
				UsedMemory:        usedMemory,
				GPUInstanceID:     migAppID(proc.GpuInstanceId),
				ComputeInstanceID: migAppID(proc.ComputeInstanceId),
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

// pcieThroughputKBMultiplier converts the driver's PCIe throughput reading
// to bytes per second. NVML documents the unit as "KB/s"; whether that is
// 1000 or 1024 bytes is pending live calibration, 1000 until proven
// otherwise.
const pcieThroughputKBMultiplier = 1000

// collectExtras gathers the backend-specific readings that live outside the
// query-field schema. Extras fail softly per the Reading contract: a family
// that cannot be read is simply absent and the collection stays green. A
// lifecycle-class return still marks the backend for re-initialization and
// aborts the remaining extras work, since after markLost the library is
// already shut down; whatever was collected before the abort is kept. The
// caller holds the backend lock.
//
//nolint:cyclop // one linear pass over the extras families with per-device fallbacks
func (b *Backend) collectExtras(ctx context.Context, opts CollectOptions) collect.Extras {
	extras := collect.Extras{CUDAVersion: b.lastCUDAVersion}

	if !b.initialized {
		// a lifecycle error earlier in this cycle already tore NVML down
		return extras
	}

	version, ret := b.api.cudaDriverVersion()

	switch {
	case ret == nvml.SUCCESS:
		b.lastCUDAVersion = cudaVersionStr(version)
		extras.CUDAVersion = b.lastCUDAVersion
	case isLifecycleError(ret):
		b.markLost()

		return extras
	default:
		b.warnOnce("cuda-version", "cannot read the CUDA version", ret)
	}

	if !opts.Energy && !opts.PCIeThroughput && !opts.MIG {
		return extras
	}

	count, ret := b.api.deviceCount()
	if ret != nvml.SUCCESS {
		b.extrasFailure("extras", "failed to enumerate devices for the extra metrics", ret)

		return extras
	}

	seenGIs := map[string]bool{}

	for deviceIdx := range count {
		if ctx.Err() != nil {
			return extras
		}

		if !b.collectDeviceExtras(ctx, deviceIdx, opts, &extras, seenGIs) {
			return extras
		}
	}

	if opts.MIG {
		// only after a complete pass: an aborted cycle must not mistake
		// unvisited GPU instances for disappeared ones
		b.dropOrphanGPMStates(seenGIs)
	}

	return extras
}

// collectDeviceExtras gathers one device's extras families. Reports whether
// extras collection may continue: a non-lifecycle failure skips just this
// device, a lifecycle-class one aborts the pass.
func (b *Backend) collectDeviceExtras(
	ctx context.Context,
	deviceIdx int,
	opts CollectOptions,
	extras *collect.Extras,
	seenGIs map[string]bool,
) bool {
	dev, ret := b.api.deviceByIndex(deviceIdx)
	if ret != nvml.SUCCESS {
		return b.extrasFailure("extras", "failed to get a device for the extra metrics", ret)
	}

	uuid, ret := dev.GetUUID()
	if ret != nvml.SUCCESS {
		return b.extrasFailure("extras", "failed to get a device uuid for the extra metrics", ret)
	}

	uuid = nvidiasmi.NormalizeUUID(uuid)

	if opts.Energy && !b.collectEnergy(dev, uuid, extras) {
		return false
	}

	if opts.PCIeThroughput && !b.collectPcie(ctx, dev, uuid, extras) {
		return false
	}

	if opts.MIG && !b.collectMIG(dev, uuid, extras, seenGIs) {
		return false
	}

	return true
}

// collectEnergy appends one device's cumulative energy reading. A device
// that cannot report it (pre-Volta) is skipped silently; other persistent
// failures are logged once. Reports whether extras collection may continue.
func (b *Backend) collectEnergy(dev nvml.Device, uuid string, extras *collect.Extras) bool {
	millijoules, ret := dev.GetTotalEnergyConsumption()

	//nolint:exhaustive // every other return is a plain failure
	switch ret {
	case nvml.SUCCESS:
		extras.Energy = append(extras.Energy, collect.EnergyCounter{
			UUID:   uuid,
			Joules: float64(millijoules) / 1000.0,
		})
	case nvml.ERROR_NOT_SUPPORTED:
	default:
		return b.extrasFailure("energy", "cannot read the GPU energy counter", ret)
	}

	return true
}

// collectPcie appends one device's PCIe throughput sample. The two
// directions are sampled over two consecutive 20ms driver windows, not one
// simultaneous pair. Reports whether extras collection may continue.
func (b *Backend) collectPcie(
	ctx context.Context,
	dev nvml.Device,
	uuid string,
	extras *collect.Extras,
) bool {
	tx, ret := dev.GetPcieThroughput(nvml.PCIE_UTIL_TX_BYTES)
	if ret != nvml.SUCCESS {
		return b.extrasFailure("pcie", "cannot sample the PCIe throughput", ret)
	}

	if ctx.Err() != nil {
		return false
	}

	rx, ret := dev.GetPcieThroughput(nvml.PCIE_UTIL_RX_BYTES)
	if ret != nvml.SUCCESS {
		return b.extrasFailure("pcie", "cannot sample the PCIe throughput", ret)
	}

	extras.PCIe = append(extras.PCIe, collect.PCIeThroughput{
		UUID:             uuid,
		TXBytesPerSecond: float64(tx) * pcieThroughputKBMultiplier,
		RXBytesPerSecond: float64(rx) * pcieThroughputKBMultiplier,
	})

	return true
}

// extrasFailure folds a failed extras call: a lifecycle-class return marks
// the backend for re-initialization and stops the remaining extras work,
// anything else skips just this reading. Either way the failure is logged
// once per family. Reports whether extras collection may continue.
func (b *Backend) extrasFailure(family, msg string, ret nvml.Return) bool {
	b.warnOnce(family, msg, ret)

	if isLifecycleError(ret) {
		b.markLost()

		return false
	}

	return true
}

// symbolPresent reports whether the driver library exports the symbol,
// cached for the process lifetime. The caller holds the backend lock.
func (b *Backend) symbolPresent(name string) bool {
	present, ok := b.symbolsPresent[name]
	if ok {
		return present
	}

	if b.symbolsPresent == nil {
		b.symbolsPresent = map[string]bool{}
	}

	present = b.api.lookupSymbol(name) == nil
	b.symbolsPresent[name] = present

	if !present {
		b.logger.Info("driver library lacks an optional entry point, "+
			"the fields it serves stay absent", "symbol", name)
	}

	return present
}

// warnOnce logs a warning the first time a family fails, so a persistent
// extras failure stays visible without flooding the log on every cycle.
func (b *Backend) warnOnce(family, msg string, ret nvml.Return) {
	if b.extrasWarned[family] {
		return
	}

	if b.extrasWarned == nil {
		b.extrasWarned = map[string]bool{}
	}

	b.extrasWarned[family] = true

	b.logger.Warn(msg, "family", family, "nvml_return", ret.String())
}
