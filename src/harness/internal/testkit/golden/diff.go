package golden

import (
	"fmt"
	"strings"
)

// maxDiffCells bounds the line-matching table of Diff; larger inputs are reported
// from their first differing line instead.
const maxDiffCells = 4 << 20

// Diff returns a line diff from want to got: "--- want" and "+++ got" headers, then
// every line prefixed with ' ' (both), '-' (want only) or '+' (got only). A line
// holding a carriage return is quoted, and a last line without a newline is marked,
// so differences in invisible bytes show.
func Diff(want, got string) string {
	a, b := lines(want), lines(got)
	var sb strings.Builder
	sb.WriteString("--- want\n+++ got\n")
	if len(a)*len(b) > maxDiffCells {
		i := 0
		for i < len(a) && i < len(b) && a[i] == b[i] {
			i++
		}
		fmt.Fprintf(&sb, "@@ first difference at line %d (%d want lines, %d got lines) @@\n", i+1, len(a), len(b))
		if i < len(a) {
			emit(&sb, '-', a[i])
		}
		if i < len(b) {
			emit(&sb, '+', b[i])
		}
		return sb.String()
	}
	// lcs[i][j] is the length of the longest common subsequence of a[i:] and b[j:].
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			emit(&sb, ' ', a[i])
			i, j = i+1, j+1
		case j == len(b) || (i < len(a) && lcs[i+1][j] >= lcs[i][j+1]):
			emit(&sb, '-', a[i])
			i++
		default:
			emit(&sb, '+', b[j])
			j++
		}
	}
	return sb.String()
}

// lines splits s after every newline; the newline stays part of its line.
func lines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.SplitAfter(s, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func emit(sb *strings.Builder, prefix byte, line string) {
	text, newline := strings.CutSuffix(line, "\n")
	sb.WriteByte(prefix)
	if strings.Contains(text, "\r") {
		fmt.Fprintf(sb, "%q", text)
	} else {
		sb.WriteString(text)
	}
	sb.WriteByte('\n')
	if !newline {
		sb.WriteString(`\ no newline at end of file` + "\n")
	}
}
