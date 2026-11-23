package patch

import (
	"errors"
	"strings"
	"testing"
)

func update(t *testing.T, text string) Hunk {
	t.Helper()
	p, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return p.Hunks[0]
}

//dsh:test DH-T-FS-5
func TestApplyPasses(t *testing.T) {
	cases := []struct {
		name, original, patch, want string
	}{
		{"exact", "a\nb\nc\n", "@@\n a\n-b\n+B\n", "a\nB\nc\n"},
		{"trailing space ignored", "a  \nb\t\n", "@@\n-a\n-b\n+x\n", "x\n"},
		{"both ends trimmed", "  a\n\tb\n", "@@\n-a\n-b\n+x\n", "x\n"},
		{"typographic folded", "x = \u201chi\u201d \u2014 it\u2019s\u00a0ok\n", "@@\n-x = \"hi\" - it's ok\n+y\n", "y\n"},
		{"context anchor", "def f():\n    pass\ndef g():\n    pass\n", "@@ def g():\n-    pass\n+    return 1\n",
			"def f():\n    pass\ndef g():\n    return 1\n"},
		{"end of file", "x\ny\nx\n", "@@\n-x\n+z\n*** End of File\n", "x\ny\nz\n"},
		{"first match without eof", "x\ny\nx\n", "@@\n-x\n+z\n", "z\ny\nx\n"},
		{"trailing empty line retry", "a\nb\n", "@@\n-b\n-\n+c\n+\n", "a\nc\n"},
		{"pure addition appends", "a\n", "@@\n+b\n", "a\nb\n"},
		{"addition before trailing blank", "a\n\n", "@@\n+b\n", "a\nb\n\n"},
		{"two chunks in order", "1\n2\n3\n4\n", "@@\n-1\n+one\n@@\n-4\n+four\n", "one\n2\n3\nfour\n"},
		{"final newline added", "a\nb", "@@\n-b\n+c\n", "a\nc\n"},
		{"crlf kept", "a\r\nb\r\nc\r\n", "@@\n a\n-b\n+B\n", "a\r\nB\r\nc\r\n"},
		{"crlf patch on lf file", "a\nb\n", "@@\r\n-a\r\n+x\r\n", "x\nb\n"},
		{"emptied file", "a\n", "@@\n-a\n", ""},
		{"empty file grows", "", "@@\n+a\n", "a\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := update(t, "*** Begin Patch\n*** Update File: f\n"+c.patch+"*** End Patch")
			got, err := h.Apply(c.original)
			if err != nil || got != c.want {
				t.Fatalf("Apply = %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

//dsh:test DH-T-FS-5
func TestApplyFailures(t *testing.T) {
	cases := []struct {
		name, original, patch string
		line                  int
		stage                 string
	}{
		{"missing context", "a\n", "@@ nope\n-a\n+b\n", 3, StageContext},
		{"missing lines", "a\nb\n", "@@\n a\n-c\n", 3, StageLines},
		{"second chunk missing", "a\nb\n", "@@\n-a\n+x\n@@\n-zzz\n", 6, StageLines},
		{"eof chunk not at end", "a\nb\n", "@@\n-a\n*** End of File\n", 3, StageLines},
		{"eof chunk overlaps", "a\nb\n", "@@\n-a\n-b\n+c\n@@\n-b\n*** End of File\n", 7, StageLines},
		{"pattern longer than file", "a\n", "@@\n-a\n-b\n", 3, StageLines},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := update(t, "*** Begin Patch\n*** Update File: f.txt\n"+c.patch+"*** End Patch")
			got, err := h.Apply(c.original)
			var pe *Error
			if !errors.As(err, &pe) || got != "" {
				t.Fatalf("Apply = %q, %v; want a mismatch and no text", got, err)
			}
			if pe.Reason() != ReasonMismatch || pe.Line != c.line || pe.Stage != c.stage || pe.Path != "f.txt" {
				t.Fatalf("error %+v; want line %d stage %s", pe, c.line, c.stage)
			}
			if !strings.Contains(pe.Error(), "f.txt") {
				t.Fatalf("Error() = %q", pe.Error())
			}
		})
	}
}

//dsh:test DH-T-FS-5
func TestApplyAddAndDelete(t *testing.T) {
	p, err := Parse("*** Begin Patch\n*** Add File: a\n+x\n*** Delete File: b\n*** End Patch")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := p.Hunks[0].Apply("ignored"); got != "x\n" {
		t.Fatalf("add: %q", got)
	}
	if got, err := p.Hunks[1].Apply("gone"); got != "" || err != nil {
		t.Fatalf("delete: %q %v", got, err)
	}
}

// TestRoundTrip is TC-04a: a patch Made from a to b and applied to a gives b, byte
// for byte.
//
//dsh:test DH-T-FS-5
func TestRoundTrip(t *testing.T) {
	cases := []struct{ name, a, b string }{
		{"identical", "a\nb\n", "a\nb\n"},
		{"one line", "a\nb\nc\n", "a\nB\nc\n"},
		{"insert at start", "a\nb\n", "x\na\nb\n"},
		{"append", "a\nb\n", "a\nb\nc\n"},
		{"delete all", "a\nb\n", ""},
		{"create from empty", "", "a\nb\n"},
		{"repeated lines", "x\nx\nx\nx\nx\nx\nx\nx\n", "x\nx\nx\nx\nx\nx\nx\ny\n"},
		{"repeated blocks", strings.Repeat("{\n  a\n}\n", 6), strings.Repeat("{\n  a\n}\n", 5) + "{\n  b\n}\n"},
		{"crlf", "a\r\nb\r\nc\r\n", "a\r\nB\r\nc\r\n"},
		{"add final newline", "a\nb", "a\nb\n"},
		{"blank lines", "a\n\n\nb\n", "a\n\nb\n\n"},
		{"many hunks", numbered(40, nil), numbered(40, map[int]string{3: "x", 20: "y", 37: "z"})},
		{"indentation", "if x:\n    a\n    b\n", "if x:\n  a\n    b\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, err := Make("f", c.a, c.b)
			if err != nil {
				t.Fatal(err)
			}
			p, err := Parse(text)
			if err != nil {
				t.Fatalf("Make wrote an unparsable patch: %v\n%s", err, text)
			}
			got := c.a
			for _, h := range p.Hunks {
				if got, err = h.Apply(got); err != nil {
					t.Fatalf("apply: %v\n%s", err, text)
				}
			}
			if got != c.b {
				t.Fatalf("round trip gave %q, want %q\npatch:\n%s", got, c.b, text)
			}
		})
	}
}

func numbered(n int, change map[int]string) string {
	var b strings.Builder
	for i := range n {
		if s, ok := change[i]; ok {
			b.WriteString(s + "\n")
			continue
		}
		b.WriteString("line " + strings.Repeat("i", i%7) + "\n")
	}
	return b.String()
}

//dsh:test DH-T-FS-6
func TestMakeLimits(t *testing.T) {
	// Every case is a byte change the format cannot carry: Make refuses it rather
	// than returning a patch whose result differs from the requested text.
	for _, c := range []struct{ name, a, b string }{
		{"drop final newline", "a\n", "a"},
		{"drop final newline with edit", "a\nb\n", "a\nc"},
		{"LF to CRLF", "a\nb\n", "a\r\nb\r\n"},
		{"CRLF to LF", "a\r\nb\r\n", "a\nb\n"},
		{"LF to CRLF with edit", "a\nb\n", "a\r\nc\r\n"},
		{"lone CR", "a\n", "b\r"},
	} {
		if text, err := Make("f", c.a, c.b); !errors.Is(err, ErrInexpressible) {
			t.Errorf("%s: err %v, patch:\n%s", c.name, err, text)
		}
	}
	// Equal texts need no change, whatever their endings.
	for _, s := range []string{"", "a", "a\r\nb\r\n"} {
		if _, err := Make("f", s, s); err != nil {
			t.Errorf("equal %q: %v", s, err)
		}
	}
}
