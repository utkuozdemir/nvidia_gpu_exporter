package nvidiasmi

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"
)

// InfoField is one gpu_info identity field: the query field it comes from and
// the label it is exposed under.
type InfoField struct {
	QField QField
	Label  string
}

// ResolvedFields is the outcome of field resolution: which fields to query, how
// their returned column names map back, and the ordered identity fields backing
// the gpu_info metric.
type ResolvedFields struct {
	Query    []QField
	Returned map[QField]RField
	Info     []InfoField
}

// infoFields is the fixed identity set backing the gpu_info metric, in the
// order its labels are exposed. These fields are always queried and are never
// dropped by exclusion.
var infoFields = []InfoField{
	{QField: UUIDQField, Label: "uuid"},
	{QField: nameQField, Label: "name"},
	{QField: driverModelCurrentQField, Label: "driver_model_current"},
	{QField: driverModelPendingQField, Label: "driver_model_pending"},
	{QField: vBiosVersionQField, Label: "vbios_version"},
	{QField: driverVersionQField, Label: "driver_version"},
	{QField: pciBusIDQField, Label: "pci_bus_id"},
	{QField: serialQField, Label: "serial"},
	{QField: computeCapQField, Label: "compute_cap"},
	{QField: pciSubDeviceIDQField, Label: "pci_sub_device_id"},
	{QField: indexQField, Label: "index"},
}

// ResolveFields determines the query fields and their returned names, running
// nvidia-smi for auto-detection and for the initial field mapping. Each
// nvidia-smi call is bounded by timeout individually (0 disables the bound),
// while ctx propagates shutdown. Auto mode falls back to the built-in field
// list when nvidia-smi cannot be queried; an explicit field list that the
// built-in list cannot cover fails instead of being silently replaced.
func ResolveFields(
	ctx context.Context,
	command string,
	qFieldsRaw string,
	qFieldsExcludeRaw string,
	timeout time.Duration,
	run RunFunc,
	logger *slog.Logger,
) (ResolvedFields, error) {
	qFieldsSeparated := strings.Split(qFieldsRaw, ",")

	qFields := toQFieldSlice(qFieldsSeparated)
	for _, infoField := range infoFields {
		qFields = append(qFields, infoField.QField)
	}

	qFields = removeDuplicates(qFields)

	auto := len(qFieldsSeparated) == 1 && qFieldsSeparated[0] == qFieldsAuto
	if auto {
		parsed, err := autoQFields(ctx, command, timeout, run)
		if err != nil {
			logger.Warn(
				"failed to auto-determine query field names, falling back to the built-in list",
				"err",
				err,
			)

			return builtinResolvedFields(qFieldsExcludeRaw, logger), nil
		}

		qFields = parsed
	}

	qFields = filterExcludedQFields(qFields, qFieldsExcludeRaw, logger)

	rFields, err := queryRFields(ctx, command, qFields, timeout, run, logger)
	if err != nil {
		// In auto mode the discovered list may contain fields newer than the
		// built-in mapping knows, so mapping the discovered list would fail
		// startup. Fall back to the built-in list wholesale instead: auto mode
		// must always come up. An explicit user-provided list keeps failing on
		// fields the built-in mapping cannot cover, so it is never silently
		// replaced.
		if auto {
			logger.Warn("falling back to the built-in field list", "err", err)

			return builtinResolvedFields(qFieldsExcludeRaw, logger), nil
		}

		return ResolvedFields{}, err
	}

	returned := make(map[QField]RField, len(qFields))
	for i, q := range qFields {
		returned[q] = rFields[i]
	}

	return ResolvedFields{
		Query:    qFields,
		Returned: returned,
		Info:     slices.Clone(infoFields),
	}, nil
}

// builtinResolvedFields resolves from the built-in field mapping, used in auto
// mode whenever nvidia-smi cannot be queried during startup.
func builtinResolvedFields(qFieldsExcludeRaw string, logger *slog.Logger) ResolvedFields {
	keys, rFieldMap := fallbackQFieldToRFieldMapExcluding(qFieldsExcludeRaw, logger)

	return ResolvedFields{
		Query:    keys,
		Returned: rFieldMap,
		Info:     slices.Clone(infoFields),
	}
}

// autoQFields discovers the queryable fields from nvidia-smi --help-query-gpu,
// bounded by timeout.
func autoQFields(
	ctx context.Context,
	command string,
	timeout time.Duration,
	run RunFunc,
) ([]QField, error) {
	callCtx, cancel := withOptionalTimeout(ctx, timeout)
	defer cancel()

	return ParseAutoQFields(callCtx, command, run)
}

// queryRFields runs the initial field-mapping query, bounded by timeout,
// falling back to the built-in mapping when the query fails.
func queryRFields(
	ctx context.Context,
	command string,
	qFields []QField,
	timeout time.Duration,
	run RunFunc,
	logger *slog.Logger,
) ([]RField, error) {
	callCtx, cancel := withOptionalTimeout(ctx, timeout)
	defer cancel()

	table, _, err := Query(callCtx, command, qFields, run)
	if err != nil {
		logger.Warn(
			"failed to run the initial query, using the built-in list for field mapping",
			"err",
			err,
		)

		return fallbackRFields(qFields)
	}

	return table.RFields, nil
}

// fallbackRFields maps query fields to returned names using the built-in list.
// It fails on fields the list does not know, so an explicit user-provided field
// list is never silently replaced.
func fallbackRFields(qFields []QField) ([]RField, error) {
	rFields := make([]RField, len(qFields))

	counter := 0

	for _, q := range qFields {
		val, contains := fallbackQFieldToRFieldMap[q]
		if !contains {
			return nil, fmt.Errorf("unexpected query field: %q", q)
		}

		rFields[counter] = val
		counter++
	}

	return rFields, nil
}

// fallbackQFieldToRFieldMapExcluding returns the built-in fallback field mapping
// with the excluded fields removed, used when nvidia-smi cannot be queried for
// the available fields.
func fallbackQFieldToRFieldMapExcluding(
	excludeRaw string,
	logger *slog.Logger,
) ([]QField, map[QField]RField) {
	keys := slices.Collect(maps.Keys(fallbackQFieldToRFieldMap))
	keys = filterExcludedQFields(keys, excludeRaw, logger)

	return keys, subsetRFieldMap(fallbackQFieldToRFieldMap, keys)
}

// subsetRFieldMap returns the entries of full whose keys appear in keys.
func subsetRFieldMap(full map[QField]RField, keys []QField) map[QField]RField {
	subset := make(map[QField]RField, len(keys))

	for _, key := range keys {
		if rField, ok := full[key]; ok {
			subset[key] = rField
		}
	}

	return subset
}

// withOptionalTimeout bounds ctx by d, where a zero d means no bound. A plain
// context.WithTimeout with a zero duration would return an already-expired
// context, which is not what "disabled" means, hence the explicit check.
func withOptionalTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d == 0 {
		return ctx, func() {}
	}

	return context.WithTimeout(ctx, d)
}

func removeDuplicates[T comparable](fields []T) []T {
	valMap := make(map[T]struct{})

	var uniques []T

	for _, field := range fields {
		_, exists := valMap[field]
		if !exists {
			uniques = append(uniques, field)
			valMap[field] = struct{}{}
		}
	}

	return uniques
}
