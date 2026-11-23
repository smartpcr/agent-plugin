package herr

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Frame lines shown around the marked line, as @babel/code-frame shows them.
const (
	frameAbove = 2
	frameBelow = 3
)

// Frame is the plain-text code frame of a position in src (DH-LIB-CODEFRAME,
// docs/harness/libs/babelcode-frame.md): up to two lines above and three below
// the line, each with its number, the line marked with ">", and under it a caret
// at the column followed by msg.
//
//	  1 | name: x
//	> 2 | 	port: [
//	    | 	      ^ a flow sequence is not closed
//	  3 | level: 2
//
// line and col are 1-based and col counts characters, as the YAML and JSON
// readers report them. A line past the end marks the last line; a column past the
// end of its line puts the caret just after it; a value below 1 is 1. The caret
// line repeats the tabs of the marked line so the caret stays under its column
// (other wide characters are counted as one column).
//
// The frame is plain text for a log, a file or a model (DH-LIB-CODEFRAME-R2): no
// colour, and every control character of src or msg other than a tab, every
// invalid byte and every C1 control is shown as U+FFFD, so no escape sequence a
// file holds is written to a terminal. A line break in msg is a space. The frame
// has no trailing line break.
func Frame(src string, line, col int, msg string) string {
	lines := strings.Split(src, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	line = min(max(line, 1), len(lines))
	marked := []rune(plain(lines[line-1]))
	col = min(max(col, 1), len(marked)+1)

	first, last := max(1, line-frameAbove), min(len(lines), line+frameBelow)
	width := len(strconv.Itoa(last))
	var b strings.Builder
	for n := first; n <= last; n++ {
		if n > first {
			b.WriteByte('\n')
		}
		gutter := "  "
		if n == line {
			gutter = "> "
		}
		num := strconv.Itoa(n)
		b.WriteString(gutter + strings.Repeat(" ", width-len(num)) + num + " |")
		if text := plain(lines[n-1]); text != "" {
			b.WriteString(" " + text)
		}
		if n != line {
			continue
		}
		b.WriteString("\n  " + strings.Repeat(" ", width) + " | ")
		for _, r := range marked[:col-1] {
			if r == '\t' {
				b.WriteByte('\t')
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteByte('^')
		if m := strings.TrimSpace(plain(strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(msg))); m != "" {
			b.WriteString(" " + m)
		}
	}
	return b.String()
}

// plain shows every control character but a tab, and every invalid byte, as
// U+FFFD, one rune for one, so columns stay where they were.
func plain(s string) string {
	if !needsPlain(s) {
		return s
	}
	var b strings.Builder
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		if r == utf8.RuneError || control(r) {
			r = utf8.RuneError
		}
		b.WriteRune(r)
	}
	return b.String()
}

func needsPlain(s string) bool {
	if !utf8.ValidString(s) {
		return true
	}
	for _, r := range s {
		if control(r) {
			return true
		}
	}
	return false
}

// control is a C0 control other than a tab, DEL, or a C1 control.
func control(r rune) bool {
	return (r < 0x20 && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}
