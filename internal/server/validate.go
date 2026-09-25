package server

import (
	"strings"
	"unicode"
)

const (
	MaxNameLength  = 128
	MaxQueryLength = 4096
	MaxBodyBytes   = 1 << 20
	MaxHistory     = 100
	DefaultHistory = 20
)

// Keeps a client-supplied count from reaching a backend that would overflow it.
func boundedLimit(limit int) int {
	if limit <= 0 {
		return DefaultHistory
	}
	if limit > MaxHistory {
		return MaxHistory
	}
	return limit
}

// U+2028 and U+2029 are line breaks to a YAML parser but not control runes to Go.
func hasControlChars(s string) bool {
	for _, r := range s {
		if r == '\u2028' || r == '\u2029' || unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// A name reaching a YAML seed or a path segment must not be able to break out of either.
func validName(kind, value string) error {
	if hasControlChars(value) {
		return invalid("a %s cannot contain line breaks or control characters", kind)
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return invalid("a %s is required", kind)
	}
	if len(trimmed) > MaxNameLength {
		return invalid("a %s cannot be longer than %d characters", kind, MaxNameLength)
	}
	return nil
}

func validPathSegment(kind, value string) error {
	if err := validName(kind, value); err != nil {
		return err
	}
	trimmed := strings.TrimSpace(value)
	if strings.ContainsAny(trimmed, "/\\") {
		return invalid("a %s cannot contain a path separator", kind)
	}
	if trimmed == "." || trimmed == ".." || strings.HasPrefix(trimmed, ".") {
		return invalid("a %s cannot start with a dot", kind)
	}
	if strings.ContainsAny(trimmed, " \t") {
		return invalid("%q is not a valid %s; use letters, numbers and dashes", trimmed, kind)
	}
	return nil
}

func validEnvironment(env string) error {
	return validPathSegment("environment name", env)
}

// The seed file names a team, so the extension is Studio's to choose, not the caller's.
func seedFileName(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "flags.goff.yaml", nil
	}

	base := trimmed
	for _, suffix := range []string{".goff.yaml", ".goff.yml", ".yaml", ".yml"} {
		if stripped := strings.TrimSuffix(base, suffix); stripped != base {
			base = stripped
			break
		}
	}
	if err := validPathSegment("team name", base); err != nil {
		return "", err
	}
	return base + ".goff.yaml", nil
}

func validRuleName(name string) error {
	return validName("rule name", name)
}

func validQuery(query string) error {
	if len(query) > MaxQueryLength {
		return invalid("a targeting query cannot be longer than %d characters", MaxQueryLength)
	}
	if hasControlChars(query) {
		return invalid("a targeting query cannot contain line breaks or control characters")
	}
	return nil
}

func validPercentages(percentages map[string]float64) error {
	if len(percentages) == 0 {
		return nil
	}
	var total float64
	for name, pct := range percentages {
		if err := validName("variation name", name); err != nil {
			return err
		}
		if pct < 0 || pct > 100 {
			return invalid("the percentage for %q must be between 0 and 100", name)
		}
		total += pct
	}
	if total == 0 {
		return invalid("the percentages cannot all be zero")
	}
	return nil
}
