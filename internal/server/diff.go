package server

import (
	"fmt"
	"strings"
)

func UnifiedDiff(before, after string, context int) string {
	if before == after {
		return ""
	}
	if context <= 0 {
		context = 3
	}

	a := strings.Split(strings.TrimRight(before, "\n"), "\n")
	b := strings.Split(strings.TrimRight(after, "\n"), "\n")

	lcs := longestCommon(a, b)
	ops := backtrack(lcs, a, b, len(a), len(b))

	var out []string
	pending := 0

	for i, op := range ops {
		if op.kind == ' ' {
			near := false
			for j := max(0, i-context); j < min(len(ops), i+context+1); j++ {
				if ops[j].kind != ' ' {
					near = true
					break
				}
			}
			if !near {
				pending++
				continue
			}
		}
		if pending > 0 {
			out = append(out, fmt.Sprintf("@@ %d unchanged lines @@", pending))
			pending = 0
		}
		out = append(out, string(op.kind)+op.text)
	}

	return strings.Join(out, "\n")
}

type diffOp struct {
	kind byte
	text string
}

func longestCommon(a, b []string) [][]int {
	table := make([][]int, len(a)+1)
	for i := range table {
		table[i] = make([]int, len(b)+1)
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			switch {
			case a[i-1] == b[j-1]:
				table[i][j] = table[i-1][j-1] + 1
			case table[i-1][j] >= table[i][j-1]:
				table[i][j] = table[i-1][j]
			default:
				table[i][j] = table[i][j-1]
			}
		}
	}
	return table
}

func backtrack(table [][]int, a, b []string, i, j int) []diffOp {
	if i > 0 && j > 0 && a[i-1] == b[j-1] {
		return append(backtrack(table, a, b, i-1, j-1), diffOp{' ', a[i-1]})
	}
	if j > 0 && (i == 0 || table[i][j-1] >= table[i-1][j]) {
		return append(backtrack(table, a, b, i, j-1), diffOp{'+', b[j-1]})
	}
	if i > 0 && (j == 0 || table[i][j-1] < table[i-1][j]) {
		return append(backtrack(table, a, b, i-1, j), diffOp{'-', a[i-1]})
	}
	return nil
}
