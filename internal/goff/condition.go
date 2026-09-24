package goff

import (
	"fmt"
	"strconv"
	"strings"
)

type Condition struct {
	Op        string       `json:"op,omitempty"`
	Not       bool         `json:"not,omitempty"`
	Children  []*Condition `json:"children,omitempty"`
	Attribute string       `json:"attribute,omitempty"`
	Operator  string       `json:"operator,omitempty"`
	Value     string       `json:"value,omitempty"`
}

func (c *Condition) IsGroup() bool {
	return c != nil && (c.Op == "and" || c.Op == "or")
}

func Group(op string, children ...*Condition) *Condition {
	return &Condition{Op: strings.ToLower(op), Children: children}
}

func NotGroup(op string, children ...*Condition) *Condition {
	return &Condition{Op: strings.ToLower(op), Not: true, Children: children}
}

func Leaf(attribute, operator, value string) *Condition {
	return &Condition{Attribute: attribute, Operator: operator, Value: value}
}

func CompileCondition(c *Condition) string {
	out, _ := compile(c, true)
	return out
}

func compile(c *Condition, top bool) (string, bool) {
	if c == nil {
		return "", false
	}

	if c.IsGroup() {
		parts := make([]string, 0, len(c.Children))
		for _, child := range c.Children {
			rendered, ok := compile(child, false)
			if !ok {
				continue
			}
			parts = append(parts, rendered)
		}
		if len(parts) == 0 {
			return "", false
		}
		if len(parts) == 1 {
			if c.Not {
				return "(not " + parts[0] + ")", true
			}
			return parts[0], true
		}
		joined := strings.Join(parts, " "+c.Op+" ")
		if c.Not {
			// `not` binds tighter than and/or, so the group always needs its own parens.
			return "(not (" + joined + "))", true
		}
		if top {
			return joined, true
		}
		return "(" + joined + ")", true
	}

	attr := strings.TrimSpace(c.Attribute)
	if attr == "" {
		return "", false
	}
	op, ok := LookupOperator(c.Operator)
	if !ok {
		return "", false
	}

	switch op.Arity {
	case 0:
		return fmt.Sprintf("(%s %s)", attr, op.Key), true
	case 2:
		items := splitList(c.Value)
		if op.Key == "notin" {
			if len(items) == 1 {
				return fmt.Sprintf("(%s ne %s)", attr, formatValue(items[0], "ne")), true
			}
			return fmt.Sprintf("(not (%s in %s))", attr, formatList(c.Value)), true
		}
		if len(items) == 1 {
			return fmt.Sprintf("(%s eq %s)", attr, formatValue(items[0], "eq")), true
		}
		return fmt.Sprintf("(%s %s %s)", attr, op.Key, formatList(c.Value)), true
	default:
		return fmt.Sprintf("(%s %s %s)", attr, op.Key, formatValue(c.Value, op.Key)), true
	}
}

type ParsedQuery struct {
	Condition *Condition
	Simple    bool
}

func ParseQuery(q string) ParsedQuery {
	q = strings.TrimSpace(q)
	if q == "" {
		return ParsedQuery{Simple: true}
	}

	cond, ok := parseExpr(q)
	if !ok {
		return ParsedQuery{Simple: false}
	}
	return ParsedQuery{Condition: cond, Simple: true}
}

func parseExpr(s string) (*Condition, bool) {
	s = strings.TrimSpace(s)
	s = stripOuterParens(s)

	if negated, ok := parseNegated(s); ok {
		return negated, true
	}

	groups, joins, ok := splitTopLevel(s)
	if !ok {
		return nil, false
	}

	if len(groups) == 1 {
		return parseLeaf(groups[0])
	}

	op := joins[0]
	for _, j := range joins {
		if j != op {
			return nil, false
		}
	}

	group := &Condition{Op: op}
	for _, raw := range groups {
		child, ok := parseExpr(raw)
		if !ok {
			return nil, false
		}
		group.Children = append(group.Children, child)
	}
	return group, true
}

func splitTopLevel(s string) (groups []string, joins []string, ok bool) {
	var (
		depth    int
		current  strings.Builder
		inString rune
	)

	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]

		if inString != 0 {
			current.WriteRune(c)
			if c == '\\' && i+1 < len(runes) {
				i++
				current.WriteRune(runes[i])
			} else if c == inString {
				inString = 0
			}
			continue
		}

		switch {
		case c == '"' || c == '\'':
			inString = c
			current.WriteRune(c)
		case c == '(':
			depth++
			current.WriteRune(c)
		case c == ')':
			depth--
			if depth < 0 {
				return nil, nil, false
			}
			current.WriteRune(c)
		case depth == 0 && isWordStart(runes, i):
			word, next := readWord(runes, i)
			lower := strings.ToLower(word)
			if lower == "and" || lower == "or" {
				joins = append(joins, lower)
				groups = append(groups, current.String())
				current.Reset()
				i = next - 1
				continue
			}
			current.WriteString(word)
			i = next - 1
		default:
			current.WriteRune(c)
		}
	}

	if depth != 0 {
		return nil, nil, false
	}
	groups = append(groups, current.String())

	for i := range groups {
		groups[i] = strings.TrimSpace(groups[i])
		if groups[i] == "" {
			return nil, nil, false
		}
	}
	return groups, joins, true
}

func parseLeaf(group string) (*Condition, bool) {
	s := stripOuterParens(strings.TrimSpace(group))

	if strings.ContainsAny(s, "()") {
		return parseExpr(s)
	}

	tokens := tokenize(s)
	if len(tokens) < 2 || len(tokens) > 3 {
		return nil, false
	}

	op, ok := LookupOperator(tokens[1])
	if !ok {
		return nil, false
	}

	c := &Condition{Attribute: tokens[0], Operator: op.Key}

	switch {
	case op.Arity == 0:
		if len(tokens) != 2 {
			return nil, false
		}
	case len(tokens) != 3:
		return nil, false
	case op.Arity == 2:
		c.Value = unformatList(tokens[2])
	default:
		c.Value = unquote(tokens[2])
	}

	return c, true
}

func parseNegated(s string) (*Condition, bool) {
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, "not ") && !strings.HasPrefix(lower, "not(") {
		return nil, false
	}

	inner := stripOuterParens(strings.TrimSpace(s[3:]))
	if inner == "" {
		return nil, false
	}

	// `not (x in [...])` round-trips as the friendlier notin leaf rather than a negated group.
	if tokens := tokenize(inner); len(tokens) == 3 && strings.ToLower(tokens[1]) == "in" {
		return &Condition{
			Attribute: tokens[0],
			Operator:  "notin",
			Value:     unformatList(tokens[2]),
		}, true
	}

	cond, ok := parseExpr(inner)
	if !ok {
		return nil, false
	}
	if cond.IsGroup() {
		if cond.Not {
			return Group("and", cond), true
		}
		cond.Not = true
		return cond, true
	}
	return &Condition{Op: "and", Not: true, Children: []*Condition{cond}}, true
}

func stripOuterParens(s string) string {
	for strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") && balanced(s[1:len(s)-1]) {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

func splitList(raw string) []string {
	var out []string
	for _, f := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(f); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func formatList(raw string) string {
	fields := strings.Split(raw, ",")
	items := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		items = append(items, formatValue(f, "eq"))
	}
	return "[" + strings.Join(items, ", ") + "]"
}

func formatValue(raw, op string) string {
	v := strings.TrimSpace(raw)

	switch strings.ToLower(v) {
	case "true", "false":
		return strings.ToLower(v)
	}

	if isNumericOperator(op) {
		if _, err := strconv.ParseFloat(v, 64); err == nil {
			return v
		}
	}

	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

func isNumericOperator(op string) bool {
	switch op {
	case "gt", "ge", "lt", "le":
		return true
	}
	return false
}

func unformatList(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")

	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, unquote(p))
	}
	return strings.Join(out, ", ")
}
