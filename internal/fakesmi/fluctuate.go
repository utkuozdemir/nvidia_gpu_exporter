package fakesmi

import (
	"math"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
)

// fluctuateRelativeSpread is the jitter half-width as a fraction of the
// captured value, and fluctuateMinSpread its absolute floor so a captured
// zero still moves a little.
const (
	fluctuateRelativeSpread = 0.10
	fluctuateMinSpread      = 1.0
)

// fluctuateFields is the whitelist of query fields that naturally move on a
// live GPU: utilization, temperature, power draw, current clocks, fan speed
// and used memory. Everything else — identity, capacities, limits, modes,
// enum states, counters — stays as recorded.
var fluctuateFields = map[string]struct{}{
	"utilization.gpu": {}, "utilization.memory": {}, "utilization.encoder": {},
	"utilization.decoder": {}, "utilization.jpeg": {}, "utilization.ofa": {},
	"temperature.gpu": {}, "temperature.memory": {},
	"fan.speed":  {},
	"power.draw": {}, "power.draw.average": {}, "power.draw.instant": {},
	"module.power.draw.average": {}, "module.power.draw.instant": {},
	"clocks.current.graphics": {}, "clocks.current.sm": {},
	"clocks.current.memory": {}, "clocks.current.video": {},
	"memory.used": {}, "used_gpu_memory": {},
}

// fluctuator jitters whitelisted cells around their captured values. It is
// deliberately not a valueGen: a generator cannot see the captured baseline,
// which is the whole premise here. Each field draws from its own source
// seeded from (seed, field), so field order never affects reproducibility,
// and sequential draws per row keep replicated GPU rows independent.
type fluctuator struct {
	seed    int64
	sources map[string]*rand.Rand
}

func newFluctuator(seed int64) *fluctuator {
	return &fluctuator{seed: seed, sources: map[string]*rand.Rand{}}
}

func (f *fluctuator) source(field string) *rand.Rand {
	src, ok := f.sources[field]
	if !ok {
		//nolint:gosec // not cryptographic: deterministic fake test data
		src = rand.New(rand.NewSource(fieldSeed(f.seed, "fluctuate:"+field)))
		f.sources[field] = src
	}

	return src
}

// apply jitters the whitelisted cells of one data row. A field with an
// explicit override belongs to the override and is skipped, and a cell that
// does not carry a plain number (e.g. "[N/A]", "[Not Supported]") is never
// touched, so an unsupported field never gains a value.
func (f *fluctuator) apply(cells, recorded []string, columnOf map[string]int, overridden map[string]valueGen) {
	for column, field := range recorded {
		if _, moves := fluctuateFields[field]; !moves {
			continue
		}

		if _, isOverridden := overridden[field]; isOverridden {
			continue
		}

		cell, ok := parseNumericCell(cells[column])
		if !ok {
			continue
		}

		cells[column] = cell.format(f.jitter(field, cell))
	}

	f.reconcileMemory(cells, columnOf, overridden)
}

// jitter draws a fresh value around the captured one, clamped to stay
// plausible: never negative, and never above 100 for a percentage.
func (f *fluctuator) jitter(field string, cell numericCell) float64 {
	spread := math.Max(fluctuateRelativeSpread*math.Abs(cell.value), fluctuateMinSpread)

	value := cell.value + (2*f.source(field).Float64()-1)*spread
	value = math.Max(value, 0)

	if cell.isPercent() {
		value = math.Min(value, 100)
	}

	return value
}

// reconcileMemory keeps the memory columns physically consistent after
// jittering: used is capped to what the card can actually allocate (total
// minus reserved), and free is recomputed from the jittered used, so
// used/free/total never contradict each other. An explicitly overridden used
// or free is the user's business and is left alone, and a row without the
// involved columns (a compute-apps row, a minimal capture) is untouched.
func (f *fluctuator) reconcileMemory(cells []string, columnOf map[string]int, overridden map[string]valueGen) {
	if _, isOverridden := overridden["memory.used"]; isOverridden {
		return
	}

	used, usedColumn, usedOK := numericCellAt(cells, columnOf, "memory.used")
	total, _, totalOK := numericCellAt(cells, columnOf, "memory.total")

	if !usedOK || !totalOK {
		return
	}

	reserved := 0.0
	if cell, _, reservedOK := numericCellAt(cells, columnOf, "memory.reserved"); reservedOK {
		reserved = cell.value
	}

	capacity := math.Max(total.value-reserved, 0)

	usedValue := math.Min(used.value, capacity)
	if usedValue != used.value {
		cells[usedColumn] = used.format(usedValue)
	}

	if _, freeOverridden := overridden["memory.free"]; freeOverridden {
		return
	}

	free, freeColumn, freeOK := numericCellAt(cells, columnOf, "memory.free")
	if !freeOK {
		return
	}

	cells[freeColumn] = free.format(math.Max(capacity-usedValue, 0))
}

// numericCellAt parses the named field's cell when the section records the
// field and the cell carries a plain number.
func numericCellAt(cells []string, columnOf map[string]int, field string) (numericCell, int, bool) {
	column, has := columnOf[field]
	if !has {
		return numericCell{}, 0, false
	}

	cell, ok := parseNumericCell(cells[column])

	return cell, column, ok
}

// numericCell is a parsed CSV cell of the shape "<number>[ <unit>]", e.g.
// "38 %", "39.32 W", "214 MiB" or a bare "40".
type numericCell struct {
	value    float64
	decimals int
	// suffix keeps everything after the number verbatim, leading space
	// included; empty for a bare number.
	suffix string
}

// numericPrefix matches the plain decimal number a cell may start with. It is
// deliberately narrower than strconv.ParseFloat: no exponent form, no
// hex, no inf/nan, mirroring what the exporter's own number extractor
// tolerates.
var numericPrefix = regexp.MustCompile(`^[+-]?\d+(\.\d+)?`)

// parseNumericCell parses a cell into its longest numeric prefix and unit
// tail ("38 %", "39.32W", a bare "40"). Anything else — "[N/A]", "N/A",
// "[Not Supported]", an empty cell — does not parse and is left alone by the
// caller. A tail containing digits is also refused: it means the cell is not
// the "<number><unit>" shape (an exponent form the exporter rejects, a
// timestamp, a second number), and rewriting it could turn a cell the
// exporter drops into one it accepts.
func parseNumericCell(cell string) (numericCell, bool) {
	token := numericPrefix.FindString(cell)
	if token == "" {
		return numericCell{}, false
	}

	suffix := cell[len(token):]
	if strings.ContainsAny(suffix, "0123456789") {
		return numericCell{}, false
	}

	value, err := strconv.ParseFloat(token, 64)
	if err != nil {
		return numericCell{}, false
	}

	decimals := 0
	if _, fraction, hasFraction := strings.Cut(token, "."); hasFraction {
		decimals = len(fraction)
	}

	return numericCell{value: value, decimals: decimals, suffix: suffix}, true
}

// format renders a new value in the captured cell's shape: same decimal
// places, same unit tail. Fixed-point only, which the exporter's number
// extractor requires.
func (c numericCell) format(value float64) string {
	return strconv.FormatFloat(value, 'f', c.decimals, 64) + c.suffix
}

func (c numericCell) isPercent() bool {
	return strings.TrimSpace(c.suffix) == "%"
}
