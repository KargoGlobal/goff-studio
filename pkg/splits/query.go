package splits

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/nikunjy/rules/parser"
)

// inPrefix names the synthetic boolean attributes that stand in for rewritten
// `in` checks. It must start with a letter to be a valid attribute name.
const inPrefix = "zzsplitsIn"

type membership struct {
	name   string
	path   []string
	values []string
}

// query is a GO Feature Flag (nikunjy) query whose `in [...]` checks are
// evaluated here with string semantics, then fed back as booleans.
type query struct {
	raw      string
	checks   []membership
	pool     sync.Pool
	matchAll bool
}

func compileQuery(raw string) (*query, error) {
	trimmed := strings.TrimSpace(raw)
	q := &query{raw: trimmed}
	if trimmed == "" {
		q.matchAll = true
		return q, nil
	}

	// The untouched query must be one GO Feature Flag itself accepts, or its own validator would reject the flag.
	if err := validNikunjy(trimmed); err != nil {
		return nil, err
	}
	rewritten, checks, err := rewriteIn(trimmed)
	if err != nil {
		return nil, err
	}
	q.checks = checks

	first, err := parser.NewEvaluator(rewritten)
	if err != nil {
		return nil, fmt.Errorf("parsing query %q: %w", trimmed, err)
	}
	if _, err := first.Process(map[string]any{}); err != nil {
		return nil, fmt.Errorf("invalid query %q: %w", trimmed, err)
	}
	q.pool.New = func() any {
		ev, _ := parser.NewEvaluator(rewritten)
		return ev
	}
	q.pool.Put(first)
	return q, nil
}

func (q *query) matches(attrs map[string]any) bool {
	if q.matchAll {
		return true
	}
	for _, c := range q.checks {
		value, ok := lookup(attrs, c.path)
		if !ok || value == nil {
			delete(attrs, c.name)
			continue
		}
		attrs[c.name] = isOneOf(value, c.values)
	}

	ev, _ := q.pool.Get().(*parser.Evaluator)
	if ev == nil {
		return false
	}
	defer q.pool.Put(ev)
	ok, err := ev.Process(attrs)
	return err == nil && ok
}

func lookup(attrs map[string]any, path []string) (any, bool) {
	var current any = attrs
	for _, part := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// isOneOf compares list entries as strings; numeric and boolean attributes
// match an entry that parses to the same value, so 3, 3.0 and "3" all match "3".
func isOneOf(value any, entries []string) bool {
	for _, s := range entries {
		if isOne(value, s) {
			return true
		}
	}
	return false
}

func isOne(value any, s string) bool {
	switch v := value.(type) {
	case string:
		return v == s
	case float64:
		f, err := strconv.ParseFloat(s, 64)
		return err == nil && f == v
	case float32:
		f, err := strconv.ParseFloat(s, 32)
		return err == nil && float32(f) == v
	case int:
		return intEquals(int64(v), s)
	case int8:
		return intEquals(int64(v), s)
	case int16:
		return intEquals(int64(v), s)
	case int32:
		return intEquals(int64(v), s)
	case int64:
		return intEquals(v, s)
	case uint:
		return uintEquals(uint64(v), s)
	case uint8:
		return uintEquals(uint64(v), s)
	case uint16:
		return uintEquals(uint64(v), s)
	case uint32:
		return uintEquals(uint64(v), s)
	case uint64:
		return uintEquals(v, s)
	case bool:
		b, err := strconv.ParseBool(s)
		return err == nil && b == v
	default:
		return fmt.Sprint(value) == s
	}
}

func intEquals(v int64, s string) bool {
	i, err := strconv.ParseInt(s, 0, 64)
	return err == nil && i == v
}

func uintEquals(v uint64, s string) bool {
	u, err := strconv.ParseUint(s, 0, 64)
	return err == nil && u == v
}

// rewriteIn replaces every `attr in [..]` with `zzsplitsInN eq true`, leaving
// the rest of the query to the GO Feature Flag parser.
func rewriteIn(src string) (string, []membership, error) {
	var (
		out    strings.Builder
		checks []membership
		i      int
	)

	for i < len(src) {
		c := src[i]
		if c == '"' {
			end, err := skipString(src, i)
			if err != nil {
				return "", nil, err
			}
			out.WriteString(src[i:end])
			i = end
			continue
		}

		if isInKeyword(src, i) {
			attrStart, attrEnd := precedingAttr(out.String())
			listStart := skipSpaces(src, i+2)
			if attrStart >= 0 && listStart < len(src) && src[listStart] == '[' {
				listEnd, values, err := parseList(src, listStart)
				if err != nil {
					return "", nil, err
				}
				built := out.String()
				attr := built[attrStart:attrEnd]
				name := inPrefix + strconv.Itoa(len(checks))
				checks = append(checks, membership{name: name, path: strings.Split(attr, "."), values: values})

				out.Reset()
				out.WriteString(built[:attrStart])
				out.WriteString(name)
				out.WriteString(built[attrEnd:])
				out.WriteString("eq true")
				i = listEnd
				continue
			}
		}

		out.WriteByte(c)
		i++
	}
	return out.String(), checks, nil
}

func isInKeyword(src string, i int) bool {
	if i+2 > len(src) {
		return false
	}
	word := src[i : i+2]
	if word != "in" && word != "IN" {
		return false
	}
	if i == 0 || !isSpace(src[i-1]) {
		return false
	}
	return i+2 < len(src) && isSpace(src[i+2])
}

// precedingAttr finds the attribute path just before the trailing whitespace of built.
func precedingAttr(built string) (int, int) {
	end := len(built)
	for end > 0 && isSpace(built[end-1]) {
		end--
	}
	start := end
	for start > 0 && isAttrChar(built[start-1]) {
		start--
	}
	if start == end || !isLetter(built[start]) {
		return -1, -1
	}
	return start, end
}

func parseList(src string, open int) (int, []string, error) {
	var values []string
	i := open + 1
	for {
		i = skipSpaces(src, i)
		if i >= len(src) {
			return 0, nil, fmt.Errorf("unterminated list in query")
		}
		switch src[i] {
		case ']':
			return i + 1, values, nil
		case ',':
			i++
			continue
		case '\'':
			return 0, nil, fmt.Errorf("list entries must use double quotes")
		case '"':
			end, err := skipString(src, i)
			if err != nil {
				return 0, nil, err
			}
			unquoted, err := strconv.Unquote(src[i:end])
			if err != nil {
				unquoted = src[i+1 : end-1]
			}
			values = append(values, unquoted)
			i = end
		default:
			start := i
			for i < len(src) && src[i] != ',' && src[i] != ']' && !isSpace(src[i]) {
				i++
			}
			values = append(values, src[start:i])
		}
	}
}

func skipString(src string, open int) (int, error) {
	for i := open + 1; i < len(src); i++ {
		switch src[i] {
		case '\\':
			i++
		case '"':
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("unterminated string in query")
}

func skipSpaces(src string, i int) int {
	for i < len(src) && isSpace(src[i]) {
		i++
	}
	return i
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func isAttrChar(c byte) bool {
	return isLetter(c) || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == ':' || c == '.'
}

func validNikunjy(q string) error {
	ev, err := parser.NewEvaluator(q)
	if err != nil {
		return fmt.Errorf("parsing query %q: %w", q, err)
	}
	if _, err := ev.Process(map[string]any{}); err != nil {
		return fmt.Errorf("invalid query %q: %w", q, err)
	}
	return nil
}
