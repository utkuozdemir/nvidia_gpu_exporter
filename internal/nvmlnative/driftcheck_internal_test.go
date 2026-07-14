package nvmlnative

import (
	"encoding/csv"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/captures"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

// TestCatalogDriftAgainstCaptures checks the catalog against every embedded
// Linux capture, so a newly contributed capture immediately reveals driver
// drift. The newest capture (by driver version) carries the hard assertions:
// a rename, removal, header change, deprecation change, or a new field with
// real values fails the build with a message saying what to fix. Older
// captures only verify the headers they share with the catalog and report
// the rest, since old drivers legitimately lack newer fields.
func TestCatalogDriftAgainstCaptures(t *testing.T) {
	t.Parallel()

	names := linuxCaptureNames(t)
	require.NotEmpty(t, names)

	maxVersion := maxDriverVersion(t, names)
	t.Logf("newest driver (hard assertions): %v", maxVersion)

	aliasExercised := false

	for _, name := range names {
		version := versionComponents(t, driverVersionFromCaptureName(t, name))
		if version[0] < eventReasonsMinDriverMajor {
			aliasExercised = true
		}

		isNewest := !versionLess(version, maxVersion)

		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkCaptureDrift(t, name, isNewest)
		})
	}

	if !aliasExercised {
		t.Logf("note: no capture predates driver %d, so the clocks_throttle_reasons alias "+
			"spelling is still unexercised; contributing an older capture would close that gap",
			eventReasonsMinDriverMajor)
	}
}

// checkCaptureDrift runs the per-capture assertions.
//
//nolint:cyclop,funlen // one linear pass over the drift rules keeps them readable
func checkCaptureDrift(t *testing.T, name string, isNewest bool) {
	t.Helper()

	query := parseCaptureQuery(t, name)
	vocabulary := resolveVocabularyFor(t, query.driverVersion)

	covered := 0

	for cellIdx, qField := range query.qFields {
		wantHeader, inCatalog := vocabulary[qField]
		if !inCatalog {
			continue
		}

		covered++

		// rule 1: header parity, byte for byte, on every capture
		if string(wantHeader) != query.headers[cellIdx] {
			t.Errorf("catalog header mismatch (driver drift?)\n"+
				"  capture: %s\n  field:   %s\n  capture header: %q\n  catalog header: %q\n"+
				"  fix: internal/nvmlnative/catalog.go (catalogRFields), and the formatter if the unit changed",
				name, qField, query.headers[cellIdx], wantHeader)
		}

		// rule 2: deprecation-list drift, both directions, on every capture
		// of the verified driver generation (the hardcoded list is pinned to
		// >= 590; older drivers may legitimately still serve these fields)
		deprecationChecked := versionComponents(t, query.driverVersion)[0] >= deprecatedFieldsMinDriverMajor
		deprecated := strings.EqualFold(strings.TrimSpace(query.values[cellIdx]), tokenDeprecated)
		hardcoded := isHardcodedDeprecated(qField)

		if deprecated && !hardcoded && deprecationChecked {
			t.Errorf("field newly deprecated by nvidia-smi\n"+
				"  capture: %s\n  field:   %s\n  value:   %q\n"+
				"  fix: add it to deprecatedFields in internal/nvmlnative/catalog.go",
				name, qField, query.values[cellIdx])
		}

		if !deprecated && hardcoded && deprecationChecked {
			t.Errorf("field no longer deprecated by nvidia-smi\n"+
				"  capture: %s\n  field:   %s\n  value:   %q\n"+
				"  fix: remove it from deprecatedFields in internal/nvmlnative/catalog.go and collect it",
				name, qField, query.values[cellIdx])
		}
	}

	// rule 3: on the newest capture, every catalog field must still exist in
	// the driver's vocabulary; a silent disappearance is a rename or removal
	if isNewest {
		advertised := make(map[nvidiasmi.QField]bool, len(query.qFields))
		for _, advertisedField := range query.qFields {
			advertised[advertisedField] = true
		}

		for catalogField := range vocabulary {
			if !advertised[catalogField] {
				t.Errorf("catalog field missing from the newest driver (rename or removal?)\n"+
					"  capture: %s\n  field:   %s\n"+
					"  fix: if renamed, add an alias in internal/nvmlnative/catalog.go (throttleAliases pattern); "+
					"if removed, drop it from the catalog",
					name, catalogField)
			}
		}
	}

	// rule 4: unknown fields. On the newest capture a field with a real
	// value fails: the exec backend would export it and this backend cannot.
	// Absent or consciously deferred fields are reported for visibility.
	var unknownAbsent, unknownReal, unknownDeferred []string

	for cellIdx, qField := range query.qFields {
		if _, inCatalog := vocabulary[qField]; inCatalog {
			continue
		}

		switch {
		case isDeferredField(qField):
			unknownDeferred = append(unknownDeferred, string(qField))
		case isAbsentCell(query.values[cellIdx]):
			unknownAbsent = append(unknownAbsent, string(qField))
		case isNewest:
			t.Errorf("driver advertises a field with real values that the catalog does not serve\n"+
				"  capture: %s\n  field:   %s\n  value:   %q\n"+
				"  fix: add it to the catalog and collector in internal/nvmlnative "+
				"(catalog.go, backend_nvml.go), or record the deferral in deferredFields",
				name, qField, query.values[cellIdx])
		default:
			unknownReal = append(unknownReal, string(qField))
		}
	}

	if len(unknownDeferred) > 0 {
		t.Logf("%s: %d advertised fields are recorded deferrals: %s",
			name, len(unknownDeferred), strings.Join(unknownDeferred, ", "))
	}

	if len(unknownAbsent) > 0 {
		t.Logf("%s: %d advertised fields are not in the catalog (absent on this hardware): %s",
			name, len(unknownAbsent), strings.Join(unknownAbsent, ", "))
	}

	if len(unknownReal) > 0 {
		t.Logf("%s: %d advertised fields with REAL values are not in the catalog "+
			"(this older driver predates the hard assertion; the newest capture governs): %s",
			name, len(unknownReal), strings.Join(unknownReal, ", "))
	}

	if isNewest {
		// the newest capture must genuinely exercise the catalog, otherwise
		// this test silently proves nothing
		require.Greater(t, covered, 170,
			"the newest capture covers too little of the catalog; is its query-gpu section intact?")
	}
}

// resolveVocabularyFor returns qfield -> expected header for the capture's
// driver generation, going through Resolve so the alias selection (throttle
// vs event spelling) is exercised exactly as the backend would at startup.
func resolveVocabularyFor(t *testing.T, driverVersion string) map[nvidiasmi.QField]nvidiasmi.RField {
	t.Helper()

	resolved, err := Resolve("AUTO", "", driverVersion, slogt.New(t))
	require.NoError(t, err)

	return resolved.Returned
}

func isHardcodedDeprecated(q nvidiasmi.QField) bool {
	for _, d := range deprecatedFields {
		if canonicalQField(q) == d || q == d {
			return true
		}
	}

	return false
}

// isAbsentCell reports whether a captured cell value is one of the absent
// states, in which case neither backend exports a metric for it.
func isAbsentCell(value string) bool {
	if nvidiasmi.IsKnownAbsentValue(value) {
		return true
	}

	switch strings.ToLower(strings.TrimSpace(value)) {
	case strings.ToLower(tokenDeprecated), strings.ToLower(tokenFunctionNotFound):
		return true
	default:
		return false
	}
}

// captureQuery is the parsed query-gpu section of one capture: the query
// field list, the returned header row, and the first data row.
type captureQuery struct {
	driverVersion string
	qFields       []nvidiasmi.QField
	headers       []string
	values        []string
}

var captureQueryCommandRegex = regexp.MustCompile(`(?m)^# \$ nvidia-smi --query-gpu=(\S+) --format=csv\s*$`)

// parseCaptureQuery extracts the first (idle) query-gpu section. The query
// field list comes from the recorded command line, which is exactly what
// exec AUTO queried on that box.
func parseCaptureQuery(t *testing.T, name string) captureQuery {
	t.Helper()

	data, err := captures.FS.ReadFile(name)
	require.NoError(t, err)

	match := captureQueryCommandRegex.FindStringSubmatch(string(data))
	require.NotNil(t, match, "capture %s has no query-gpu section", name)

	fields := strings.Split(match[1], ",")

	// the header and first data row follow the command line, after comment
	// and separator lines
	rest := string(data)[strings.Index(string(data), match[0])+len(match[0]):]
	lines := strings.Split(rest, "\n")

	var rows [][]string

	for _, line := range lines {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			if len(rows) > 0 {
				break
			}

			continue
		}

		reader := csv.NewReader(strings.NewReader(line))
		reader.TrimLeadingSpace = true

		cells, csvErr := reader.Read()
		require.NoError(t, csvErr, "capture %s: cannot parse a query-gpu row as CSV", name)

		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}

		rows = append(rows, cells)

		if len(rows) == 2 {
			break
		}
	}

	require.Len(t, rows, 2, "capture %s: query-gpu section has no header+data rows", name)
	require.Len(t, rows[0], len(fields),
		"capture %s: header cell count does not match the query field count", name)
	require.Len(t, rows[1], len(fields),
		"capture %s: data cell count does not match the query field count", name)

	qFields := make([]nvidiasmi.QField, len(fields))
	for i, f := range fields {
		qFields[i] = nvidiasmi.QField(f)
	}

	return captureQuery{
		driverVersion: driverVersionFromCaptureName(t, name),
		qFields:       qFields,
		headers:       rows[0],
		values:        rows[1],
	}
}

func linuxCaptureNames(t *testing.T) []string {
	t.Helper()

	entries, err := captures.FS.ReadDir(".")
	require.NoError(t, err)

	var names []string

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "linux-") && strings.HasSuffix(entry.Name(), ".txt") {
			names = append(names, entry.Name())
		}
	}

	return names
}

var captureVersionRegex = regexp.MustCompile(`__([0-9.]+)\.txt$`)

func driverVersionFromCaptureName(t *testing.T, name string) string {
	t.Helper()

	match := captureVersionRegex.FindStringSubmatch(name)
	require.NotNil(t, match, "capture name %s does not end in a driver version", name)

	return match[1]
}

// newestByDriverVersion picks the capture with the highest driver version,
// comparing numerically per component.
func newestByDriverVersion(t *testing.T, names []string) string {
	t.Helper()

	newest := names[0]
	newestVersion := versionComponents(t, driverVersionFromCaptureName(t, newest))

	for _, name := range names[1:] {
		version := versionComponents(t, driverVersionFromCaptureName(t, name))
		if versionLess(newestVersion, version) {
			newest, newestVersion = name, version
		}
	}

	return newest
}

// maxDriverVersion returns the highest driver version among the captures;
// every capture at that version carries the hard assertions, so a tie means
// several anchors.
func maxDriverVersion(t *testing.T, names []string) []int {
	t.Helper()

	return versionComponents(t, driverVersionFromCaptureName(t, newestByDriverVersion(t, names)))
}

func versionComponents(t *testing.T, version string) []int {
	t.Helper()

	parts := strings.Split(version, ".")
	components := make([]int, len(parts))

	for i, part := range parts {
		value, err := strconv.Atoi(part)
		require.NoError(t, err, "unparseable driver version %q", version)

		components[i] = value
	}

	return components
}

func versionLess(left, right []int) bool {
	for i := 0; i < len(left) && i < len(right); i++ {
		if left[i] != right[i] {
			return left[i] < right[i]
		}
	}

	return len(left) < len(right)
}

// TestDriftRulesSelfCheck pins the helpers the drift rules depend on, so a
// refactor cannot quietly neuter the checks.
func TestDriftRulesSelfCheck(t *testing.T) {
	t.Parallel()

	assert.True(t, isAbsentCell("[N/A]"))
	assert.True(t, isAbsentCell("N/A"))
	assert.True(t, isAbsentCell("[Requested functionality has been deprecated]"))
	assert.True(t, isAbsentCell("[Function Not Found]"))
	assert.False(t, isAbsentCell("100.00 %"))
	assert.False(t, isAbsentCell("0"))

	assert.True(t, isHardcodedDeprecated("display_mode"))
	assert.False(t, isHardcodedDeprecated("power.draw"))

	assert.True(t, isDeferredField("power_smoothing.enabled"))
	assert.True(t, isDeferredField("kmd_version"))
	assert.False(t, isDeferredField("power.draw"))
}
