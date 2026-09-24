package goff

import (
	"html"
	"html/template"
	"strings"
	"unicode"
)

var operators = map[string]bool{
	"eq": true, "ne": true, "lt": true, "gt": true, "le": true, "ge": true,
	"co": true, "sw": true, "ew": true, "in": true, "pr": true, "not": true,
}

var logicals = map[string]bool{
	"and": true, "or": true,
}

var literals = map[string]bool{
	"true": true, "false": true, "null": true,
}

// Highlight renders a nikunjy/rules targeting query as span-wrapped HTML.
func Highlight(q string) template.HTML {
	var b strings.Builder
	runes := []rune(q)

	for i := 0; i < len(runes); {
		c := runes[i]

		switch {
		case c == '(' || c == ')':
			b.WriteString(`<span class="q-paren">` + string(c) + `</span>`)
			i++

		case c == '"' || c == '\'':
			j := i + 1
			for j < len(runes) && runes[j] != c {
				if runes[j] == '\\' && j+1 < len(runes) {
					j++
				}
				j++
			}
			if j < len(runes) {
				j++
			}
			b.WriteString(`<span class="q-str">` + html.EscapeString(string(runes[i:j])) + `</span>`)
			i = j

		case unicode.IsSpace(c):
			b.WriteRune(c)
			i++

		case unicode.IsDigit(c):
			j := i
			for j < len(runes) && (unicode.IsDigit(runes[j]) || runes[j] == '.') {
				j++
			}
			b.WriteString(`<span class="q-num">` + string(runes[i:j]) + `</span>`)
			i = j

		case isWordRune(c):
			j := i
			for j < len(runes) && isWordRune(runes[j]) {
				j++
			}
			b.WriteString(classify(string(runes[i:j])))
			i = j

		default:
			b.WriteString(html.EscapeString(string(c)))
			i++
		}
	}

	return template.HTML(b.String())
}

func classify(word string) string {
	lower := strings.ToLower(word)
	escaped := html.EscapeString(word)

	switch {
	case logicals[lower]:
		return `<span class="q-logic">` + escaped + `</span>`
	case operators[lower]:
		return `<span class="q-op">` + escaped + `</span>`
	case literals[lower]:
		return `<span class="q-lit">` + escaped + `</span>`
	default:
		return `<span class="q-prop">` + escaped + `</span>`
	}
}

func isWordRune(c rune) bool {
	return unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '.' || c == '-'
}
