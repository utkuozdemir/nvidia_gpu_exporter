package nvidiasmi

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// ProcessList holds the running compute and graphics processes reported by
// nvidia-smi -q -d PIDS across all GPUs. A process with both contexts (such as
// Type C+G or M+C+G) appears in both slices, matching NVML's separate
// compute/MPS and graphics process queries.
type ProcessList struct {
	Compute  []ComputeApp
	Graphics []ComputeApp
}

// PmonKey identifies a process on a specific GPU for attaching pmon readings.
type PmonKey struct {
	GPUUUID string
	PID     string
}

const (
	// pmonMaxDevicesPerQuery is nvidia-smi pmon's hard cap on monitored GPUs
	// per invocation. Systems with more active GPUs query pmon in chunks via -i.
	pmonMaxDevicesPerQuery = 4

	migModeCurrentQField QField = "mig.mode.current"
)

// QueryProcesses runs nvidia-smi -q -d PIDS and parses the running compute
// (C, M) and graphics (G) processes along with untruncated process names and
// MIG instance IDs.
func QueryProcesses(
	ctx context.Context,
	command string,
	table *Table,
	run RunFunc,
	logger *slog.Logger,
) (ProcessList, error) {
	stdout, _, err := execQuery(ctx, command, run, "-q", "-d", "PIDS")
	if err != nil {
		return ProcessList{}, err
	}

	return ParseProcesses(stdout, table, logger)
}

type rawProcessEntry struct {
	uuid       string
	pid        string
	gi         string
	ci         string
	procType   string
	name       string
	usedMemory string
}

type processesParser struct {
	busToUUID    map[string]string
	ordUUIDs     []string
	result       ProcessList
	seenCompute  map[string]int
	seenGraphics map[string]int
	curUUID      string
	gpuOrd       int
	inProcesses  bool
	pendingGI    string
	pendingCI    string
	curProc      *rawProcessEntry
	logger       *slog.Logger
}

// ParseProcesses parses nvidia-smi -q (or -q -d PIDS) output and maps each GPU
// section to its normalized UUID via table's PCI bus IDs (falling back to GPU
// UUID lines in full -q output or row order).
func ParseProcesses(output string, table *Table, logger *slog.Logger) (ProcessList, error) {
	if !looksLikeQueryOutput(output) {
		return ProcessList{}, fmt.Errorf(
			"unexpected nvidia-smi -q -d PIDS output: %q",
			strings.TrimSpace(output),
		)
	}

	busToUUID, ordUUIDs := tableBusLookup(table)
	parser := &processesParser{
		busToUUID:    busToUUID,
		ordUUIDs:     ordUUIDs,
		seenCompute:  map[string]int{},
		seenGraphics: map[string]int{},
		gpuOrd:       -1,
		logger:       logger,
	}

	for line := range strings.SplitSeq(output, "\n") {
		parser.consumeLine(strings.TrimRight(line, "\r"))
	}

	parser.flush()

	return parser.result, nil
}

func (p *processesParser) flush() {
	if p.curProc == nil {
		return
	}

	entry := *p.curProc
	p.curProc = nil

	appendProcessEntry(&p.result, p.seenCompute, p.seenGraphics, entry, p.logger)
}

func (p *processesParser) consumeLine(line string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}

	indent := len(line) - len(strings.TrimLeft(line, " "))

	if indent == 0 && strings.HasPrefix(line, "GPU ") {
		p.startGPUSection(line)

		return
	}

	if indent <= 4 {
		p.consumeSectionHeader(trimmed)

		return
	}

	if p.inProcesses {
		p.consumeProcessField(trimmed)
	}
}

func (p *processesParser) startGPUSection(line string) {
	p.flush()

	p.gpuOrd++
	bus := strings.TrimSpace(strings.TrimPrefix(line, "GPU "))
	p.curUUID = p.busToUUID[canonicalBusID(bus)]

	if p.curUUID == "" && p.gpuOrd >= 0 && p.gpuOrd < len(p.ordUUIDs) {
		p.curUUID = p.ordUUIDs[p.gpuOrd]
	}

	p.inProcesses = false
	p.pendingGI = ""
	p.pendingCI = ""
}

func (p *processesParser) consumeSectionHeader(trimmed string) {
	p.flush()

	p.pendingGI = ""
	p.pendingCI = ""

	key, val, ok := strings.Cut(trimmed, ":")
	if !ok {
		p.inProcesses = strings.EqualFold(trimmed, "Processes")

		return
	}

	fieldKey := strings.TrimSpace(key)
	fieldVal := strings.TrimSpace(val)

	if strings.EqualFold(fieldKey, "GPU UUID") && p.curUUID == "" && !IsKnownAbsentValue(fieldVal) {
		p.curUUID = NormalizeUUID(fieldVal)
	}

	p.inProcesses = strings.EqualFold(fieldKey, "Processes") && !strings.EqualFold(fieldVal, "None")
}

func (p *processesParser) consumeProcessField(trimmed string) {
	key, val, ok := strings.Cut(trimmed, ":")
	if !ok {
		return
	}

	fieldKey := strings.TrimSpace(key)
	fieldVal := strings.TrimSpace(val)

	switch {
	case strings.EqualFold(fieldKey, "GPU instance ID"):
		p.flush()

		p.pendingGI = parseMIGInstanceID(fieldVal)
	case strings.EqualFold(fieldKey, "Compute instance ID"):
		if p.curProc != nil {
			p.curProc.ci = parseMIGInstanceID(fieldVal)
		} else {
			p.pendingCI = parseMIGInstanceID(fieldVal)
		}
	case strings.EqualFold(fieldKey, "Process ID"):
		p.flush()

		p.curProc = &rawProcessEntry{
			uuid: p.curUUID,
			pid:  fieldVal,
			gi:   p.pendingGI,
			ci:   p.pendingCI,
		}
		p.pendingGI = ""
		p.pendingCI = ""
	case p.curProc != nil:
		applyProcessAttribute(p.curProc, fieldKey, fieldVal)
	}
}

func applyProcessAttribute(entry *rawProcessEntry, key, val string) {
	switch {
	case strings.EqualFold(key, "Type"):
		entry.procType = val
	case strings.EqualFold(key, "Name"):
		entry.name = val
	case strings.EqualFold(key, "Used GPU Memory"):
		entry.usedMemory = val
	}
}

func looksLikeQueryOutput(output string) bool {
	for line := range strings.SplitSeq(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "NVSMI LOG") ||
			strings.HasPrefix(trimmed, "Attached GPUs") ||
			strings.HasPrefix(line, "GPU ") {
			return true
		}
	}

	return false
}

func tableBusLookup(table *Table) (map[string]string, []string) {
	if table == nil {
		return nil, nil
	}

	busToUUID := make(map[string]string, len(table.Rows))
	ordUUIDs := make([]string, 0, len(table.Rows))

	for _, row := range table.Rows {
		uuid := NormalizeUUID(row.QFieldToCells[UUIDQField].RawValue)
		ordUUIDs = append(ordUUIDs, uuid)

		bus := canonicalBusID(row.QFieldToCells[pciBusIDQField].RawValue)
		if bus != "" && uuid != "" {
			busToUUID[bus] = uuid
		}
	}

	return busToUUID, ordUUIDs
}

func canonicalBusID(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return ""
	}

	parts := strings.Split(trimmed, ":")
	if len(parts) == 3 {
		domain := strings.TrimLeft(parts[0], "0")
		if domain == "" {
			domain = "0"
		}

		parts[0] = domain

		return strings.Join(parts, ":")
	}

	return trimmed
}

func parseMIGInstanceID(raw string) string {
	id, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 32)
	if err != nil {
		return ""
	}

	return strconv.FormatUint(id, 10)
}

func appendProcessEntry(
	result *ProcessList,
	seenCompute map[string]int,
	seenGraphics map[string]int,
	entry rawProcessEntry,
	logger *slog.Logger,
) {
	if _, err := strconv.ParseUint(entry.pid, 10, 64); err != nil {
		if !IsKnownAbsentValue(entry.pid) {
			logger.Warn("skipping process entry with unparseable pid", "pid", entry.pid)
		}

		return
	}

	if entry.uuid == "" {
		logger.Warn("skipping process entry with no gpu uuid", "pid", entry.pid)

		return
	}

	app := buildComputeAppFromEntry(entry)
	dedupKey := entry.uuid + "\x00" + entry.pid + "\x00" + entry.gi + "\x00" + entry.ci
	upperType := strings.ToUpper(entry.procType)

	if strings.Contains(upperType, "C") || strings.Contains(upperType, "M") {
		upsertProcessSlice(&result.Compute, seenCompute, dedupKey, app)
	}

	if strings.Contains(upperType, "G") {
		upsertProcessSlice(&result.Graphics, seenGraphics, dedupKey, app)
	}
}

func buildComputeAppFromEntry(entry rawProcessEntry) ComputeApp {
	name := entry.name
	if name == "" {
		name = "[Not Found]"
	}

	usedMem := entry.usedMemory
	if strings.Contains(strings.ToLower(usedMem), "not available") {
		usedMem = "[N/A]"
	}

	return ComputeApp{
		GPUUUID:           entry.uuid,
		PID:               entry.pid,
		ProcessName:       name,
		UsedMemory:        usedMem,
		GPUInstanceID:     entry.gi,
		ComputeInstanceID: entry.ci,
	}
}

func upsertProcessSlice(list *[]ComputeApp, seen map[string]int, key string, app ComputeApp) {
	if idx, exists := seen[key]; exists {
		(*list)[idx] = preferFiniteAppMemory((*list)[idx], app)

		return
	}

	seen[key] = len(*list)
	*list = append(*list, app)
}

func preferFiniteAppMemory(existing, candidate ComputeApp) ComputeApp {
	if IsKnownAbsentValue(existing.UsedMemory) && !IsKnownAbsentValue(candidate.UsedMemory) {
		existing.UsedMemory = candidate.UsedMemory
	}

	return existing
}

// AttachPmonUtilization runs nvidia-smi pmon -c 1 for the GPUs that have
// active processes and populates each matching ComputeApp.Utilization. Failures
// are logged at debug level and leave Utilization nil, since pmon is
// unsupported on some GPU and platform combinations.
func AttachPmonUtilization(
	ctx context.Context,
	command string,
	table *Table,
	compute []ComputeApp,
	graphics []ComputeApp,
	run RunFunc,
	logger *slog.Logger,
) {
	if len(compute) == 0 && len(graphics) == 0 || table == nil || len(table.Rows) == 0 {
		return
	}

	indexToUUID, uuidToIndex := tableGPUIndices(table)

	active, maxIdx := activeGPUIndices(uuidToIndex, compute, graphics)
	if len(active) == 0 {
		return
	}

	byKey, ok := queryPmonBatches(ctx, command, len(table.Rows), active, maxIdx, indexToUUID, run, logger)
	if !ok {
		return
	}

	applyPmonMap(compute, byKey)
	applyPmonMap(graphics, byKey)
}

func tableGPUIndices(table *Table) (map[string]string, map[string]int) {
	indexToUUID := make(map[string]string, len(table.Rows))
	uuidToIndex := make(map[string]int, len(table.Rows))

	for ord, row := range table.Rows {
		if strings.EqualFold(strings.TrimSpace(row.QFieldToCells[migModeCurrentQField].RawValue), "Enabled") {
			continue
		}

		uuid := NormalizeUUID(row.QFieldToCells[UUIDQField].RawValue)
		if uuid == "" {
			continue
		}

		idx := ord
		if parsed, err := strconv.Atoi(strings.TrimSpace(row.QFieldToCells[indexQField].RawValue)); err == nil {
			idx = parsed
		}

		indexToUUID[strconv.Itoa(idx)] = uuid
		uuidToIndex[uuid] = idx
	}

	return indexToUUID, uuidToIndex
}

func activeGPUIndices(uuidToIndex map[string]int, compute, graphics []ComputeApp) ([]int, int) {
	activeSet := map[int]struct{}{}
	maxIdx := 0

	for _, list := range [][]ComputeApp{compute, graphics} {
		for idx := range list {
			if list[idx].GPUInstanceID != "" {
				continue
			}

			if gpuIdx, ok := uuidToIndex[list[idx].GPUUUID]; ok {
				activeSet[gpuIdx] = struct{}{}
				maxIdx = max(maxIdx, gpuIdx)
			}
		}
	}

	active := make([]int, 0, len(activeSet))
	for gpuIdx := range activeSet {
		active = append(active, gpuIdx)
	}

	slices.Sort(active)

	return active, maxIdx
}

func queryPmonBatches(
	ctx context.Context,
	command string,
	totalGPUs int,
	active []int,
	maxIdx int,
	indexToUUID map[string]string,
	run RunFunc,
	logger *slog.Logger,
) (map[PmonKey]*ProcessUtilization, bool) {
	if totalGPUs <= pmonMaxDevicesPerQuery && maxIdx < pmonMaxDevicesPerQuery {
		stdout, _, err := execQuery(ctx, command, run, "pmon", "-c", "1")
		if err != nil {
			logger.Debug("skipping pmon per-process utilization", "err", err)

			return nil, false
		}

		return ParsePmon(stdout, indexToUUID), true
	}

	byKey := map[PmonKey]*ProcessUtilization{}

	for start := 0; start < len(active); start += pmonMaxDevicesPerQuery {
		end := min(start+pmonMaxDevicesPerQuery, len(active))
		ids := make([]string, end-start)

		for pos, gpuIdx := range active[start:end] {
			ids[pos] = strconv.Itoa(gpuIdx)
		}

		stdout, _, err := execQuery(ctx, command, run, "pmon", "-c", "1", "-i", strings.Join(ids, ","))
		if err != nil {
			logger.Debug("skipping pmon per-process utilization batch", "gpus", strings.Join(ids, ","), "err", err)

			continue
		}

		maps.Copy(byKey, ParsePmon(stdout, indexToUUID))
	}

	return byKey, len(byKey) > 0
}

func applyPmonMap(apps []ComputeApp, byKey map[PmonKey]*ProcessUtilization) {
	for idx := range apps {
		if apps[idx].GPUInstanceID != "" {
			continue
		}

		if util, ok := byKey[PmonKey{GPUUUID: apps[idx].GPUUUID, PID: apps[idx].PID}]; ok {
			apps[idx].Utilization = util
		}
	}
}

type pmonColumns struct {
	gpu int
	pid int
	sm  int
	mem int
	enc int
	dec int
	jpg int
	ofa int
	min int
}

// ParsePmon parses nvidia-smi pmon output for the non-MIG GPUs in indexToUUID
// and returns per-process utilization using normalized GPU UUID and PID as the
// key. For columns present in the pmon schema, "-" reports 0, while columns
// absent from older pmon headers (such as pre-R550 jpg and ofa) stay nil.
func ParsePmon(output string, indexToUUID map[string]string) map[PmonKey]*ProcessUtilization {
	var (
		cols      pmonColumns
		hasHeader bool
		result    = map[PmonKey]*ProcessUtilization{}
	)

	for line := range strings.SplitSeq(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if rest, ok := strings.CutPrefix(trimmed, "#"); ok {
			fields := strings.Fields(rest)
			if len(fields) > 0 && strings.EqualFold(fields[0], "gpu") {
				cols = parsePmonHeader(fields)
				hasHeader = true
			}

			continue
		}

		key, util, ok := parsePmonDataLine(trimmed, cols, hasHeader, indexToUUID)
		if ok {
			result[key] = util
		}
	}

	return result
}

func parsePmonDataLine(
	trimmed string,
	headerCols pmonColumns,
	hasHeader bool,
	indexToUUID map[string]string,
) (PmonKey, *ProcessUtilization, bool) {
	tokens := strings.Fields(trimmed)
	lineCols := headerCols

	if !hasHeader {
		var ok bool

		lineCols, ok = inferPmonColumns(tokens)
		if !ok {
			return PmonKey{}, nil, false
		}
	}

	if len(tokens) < lineCols.min || lineCols.gpu < 0 || lineCols.pid < 0 {
		return PmonKey{}, nil, false
	}

	pid := tokens[lineCols.pid]
	if pid == "-" {
		return PmonKey{}, nil, false
	}

	if _, err := strconv.ParseUint(pid, 10, 64); err != nil {
		return PmonKey{}, nil, false
	}

	uuid := indexToUUID[tokens[lineCols.gpu]]
	if uuid == "" {
		return PmonKey{}, nil, false
	}

	// The trailing command column is at least one token and may contain
	// spaces (for example "NVIDIA Share.exe"), so engine columns must sit
	// strictly before the last token.
	limit := len(tokens) - 1
	util := &ProcessUtilization{
		SMRatio:      parsePmonRatio(tokens, lineCols.sm, limit),
		MemoryRatio:  parsePmonRatio(tokens, lineCols.mem, limit),
		EncoderRatio: parsePmonRatio(tokens, lineCols.enc, limit),
		DecoderRatio: parsePmonRatio(tokens, lineCols.dec, limit),
		JPEGRatio:    parsePmonRatio(tokens, lineCols.jpg, limit),
		OFARatio:     parsePmonRatio(tokens, lineCols.ofa, limit),
	}

	if !hasAnyPmonRatio(util) {
		return PmonKey{}, nil, false
	}

	return PmonKey{GPUUUID: uuid, PID: pid}, util, true
}

func hasAnyPmonRatio(util *ProcessUtilization) bool {
	return util.SMRatio != nil || util.MemoryRatio != nil ||
		util.EncoderRatio != nil || util.DecoderRatio != nil ||
		util.JPEGRatio != nil || util.OFARatio != nil
}

func parsePmonHeader(fields []string) pmonColumns {
	cols := pmonColumns{
		gpu: -1, pid: -1,
		sm: -1, mem: -1, enc: -1, dec: -1, jpg: -1, ofa: -1,
		min: len(fields),
	}

	for colIdx, name := range fields {
		switch strings.ToLower(name) {
		case "gpu":
			cols.gpu = colIdx
		case "pid":
			cols.pid = colIdx
		case "sm":
			cols.sm = colIdx
		case "mem":
			cols.mem = colIdx
		case "enc":
			cols.enc = colIdx
		case "dec":
			cols.dec = colIdx
		case "jpg":
			cols.jpg = colIdx
		case "ofa":
			cols.ofa = colIdx
		}
	}

	return cols
}

func inferPmonColumns(tokens []string) (pmonColumns, bool) {
	if len(tokens) < 8 {
		return pmonColumns{}, false
	}

	if len(tokens) >= 10 && isPmonUtilToken(tokens[7]) && isPmonUtilToken(tokens[8]) {
		return pmonColumns{
			gpu: 0, pid: 1,
			sm: 3, mem: 4, enc: 5, dec: 6, jpg: 7, ofa: 8,
			min: 10,
		}, true
	}

	return pmonColumns{
		gpu: 0, pid: 1,
		sm: 3, mem: 4, enc: 5, dec: 6, jpg: -1, ofa: -1,
		min: 8,
	}, true
}

func isPmonUtilToken(tok string) bool {
	if tok == "-" {
		return true
	}

	val, err := strconv.ParseUint(tok, 10, 32)

	return err == nil && val <= 100
}

func parsePmonRatio(tokens []string, idx, limit int) *float64 {
	if idx < 0 || idx >= limit {
		return nil
	}

	raw := tokens[idx]
	if raw == "-" {
		zero := 0.0

		return &zero
	}

	pct, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || pct > 100 {
		return nil
	}

	val := float64(pct) / 100.0

	return &val
}
