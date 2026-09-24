package goff

import (
	"strings"
	"unicode"
)

func tokenize(s string) []string {
	var (
		tokens   []string
		current  strings.Builder
		inString rune
		depth    int
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
		case c == '[':
			depth++
			current.WriteRune(c)
		case c == ']':
			depth--
			current.WriteRune(c)
		case unicode.IsSpace(c) && depth == 0:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(c)
		}
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

func unquote(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			inner := s[1 : len(s)-1]
			return strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\'`, `'`).Replace(inner)
		}
	}
	return s
}

func balanced(s string) bool {
	depth := 0
	var inString rune
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if inString != 0 {
			if c == '\\' && i+1 < len(runes) {
				i++
			} else if c == inString {
				inString = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			inString = c
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

func isWordStart(runes []rune, i int) bool {
	if !unicode.IsLetter(runes[i]) {
		return false
	}
	if i > 0 && isWordRune(runes[i-1]) {
		return false
	}
	return true
}

func readWord(runes []rune, i int) (string, int) {
	j := i
	for j < len(runes) && isWordRune(runes[j]) {
		j++
	}
	return string(runes[i:j]), j
}
