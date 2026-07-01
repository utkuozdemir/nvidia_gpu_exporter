package nvidiasmi

import (
	"log/slog"
	"regexp"
	"strings"
)

// parseFieldExcludePatterns turns a comma-separated list of field name globs
// into matchers. Names match literally except for "*", which matches any
// sequence of characters (for example "remapped_rows.histogram.*").
func parseFieldExcludePatterns(raw string) []*regexp.Regexp {
	parts := strings.Split(raw, ",")
	patterns := make([]*regexp.Regexp, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		expr := "^" + strings.ReplaceAll(regexp.QuoteMeta(part), `\*`, ".*") + "$"
		patterns = append(patterns, regexp.MustCompile(expr))
	}

	return patterns
}

func matchesAnyPattern(s string, patterns []*regexp.Regexp) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(s) {
			return true
		}
	}

	return false
}

// filterExcludedQFields drops query fields matching any of the exclude patterns.
// Required fields backing the gpu_info metric are never dropped: excluding one
// is ignored with a warning so the metric and the uuid label stay intact.
func filterExcludedQFields(qFields []QField, excludeRaw string, logger *slog.Logger) []QField {
	patterns := parseFieldExcludePatterns(excludeRaw)
	if len(patterns) == 0 {
		return qFields
	}

	required := make(map[QField]struct{}, len(infoFields))
	for _, infoField := range infoFields {
		required[infoField.QField] = struct{}{}
	}

	kept := make([]QField, 0, len(qFields))

	var excluded []string

	for _, qField := range qFields {
		if !matchesAnyPattern(string(qField), patterns) {
			kept = append(kept, qField)

			continue
		}

		if _, isRequired := required[qField]; isRequired {
			logger.Warn("ignoring exclusion of required query field", "field", qField)
			kept = append(kept, qField)

			continue
		}

		excluded = append(excluded, string(qField))
	}

	if len(excluded) > 0 {
		logger.Info("excluding query fields", "fields", strings.Join(excluded, ", "))
	}

	return kept
}
