package herr_test

import (
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// The code frame of a position: the line, a caret at the column and the message,
// with its neighbours numbered; the first line, the last line, a column past the
// end, a tab-indented line and an empty file; and no escape character, even when
// the source or the message holds one.
//
//dsh:test DH-T-CODEFRAME-1
func TestFrame(t *testing.T) {
	src := "a: 1\nb: 2\nc: 3\nd: 4\ne: 5\nf: 6\ng: 7\n"
	for _, c := range []struct {
		name      string
		src       string
		line, col int
		msg       string
		want      string
	}{
		{"first line", src, 1, 4, "bad value",
			"> 1 | a: 1\n    |    ^ bad value\n  2 | b: 2\n  3 | c: 3\n  4 | d: 4"},
		{"middle", src, 4, 1, "here",
			"  2 | b: 2\n  3 | c: 3\n> 4 | d: 4\n    | ^ here\n  5 | e: 5\n  6 | f: 6\n  7 | g: 7"},
		{"last line, a column past the end", "x: 1\ny: 22", 2, 40, "end",
			"  1 | x: 1\n> 2 | y: 22\n    |      ^ end"},
		{"a tab-indented line", "list:\n\t\t- [a\nnext: 1", 2, 6, "not closed",
			"  1 | list:\n> 2 | \t\t- [a\n    | \t\t   ^ not closed\n  3 | next: 1"},
		{"an empty file", "", 1, 1, "empty document",
			"> 1 |\n    | ^ empty document"},
		{"a line past the end, a column below one", "only", 9, 0, "",
			"> 1 | only\n    | ^"},
		{"the line after a final newline", "a: 1\n", 2, 1, "unexpected end",
			"  1 | a: 1\n> 2 |\n    | ^ unexpected end"},
		{"wide gutter", strings.Repeat("l\n", 11) + "x", 10, 1, "m",
			"   8 | l\n   9 | l\n> 10 | l\n     | ^ m\n  11 | l\n  12 | x"},
		{"multibyte column", "név: \"é\"\n", 1, 6, "quoted",
			"> 1 | név: \"é\"\n    |      ^ quoted\n  2 |"},
		{"escape bytes and CRLF", "\x1b[31mred\x1b[0m: 1\r\nok: 2\r\n", 1, 2, "bad\x1b[0m\nnext",
			"> 1 | \uFFFD[31mred\uFFFD[0m: 1\n    |  ^ bad\uFFFD[0m next\n  2 | ok: 2\n  3 |"},
		{"invalid bytes and C1 controls", "a\xffb\u009bc", 1, 4, "x",
			"> 1 | a\uFFFDb\uFFFDc\n    |    ^ x"},
	} {
		got := herr.Frame(c.src, c.line, c.col, c.msg)
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
		if strings.ContainsAny(got, "\x1b\r\x7f") || strings.ContainsRune(got, 0x9b) {
			t.Errorf("%s: the frame holds an escape or control character: %q", c.name, got)
		}
	}
}
