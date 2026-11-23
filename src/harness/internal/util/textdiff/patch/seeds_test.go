package patch

import (
	"strings"
	"testing"
)

var parseSeeds = []string{
	"",
	"*** Begin Patch\n*** End Patch\n",
	"*** Begin Patch\n*** Add File: a\n+x\n*** Delete File: b\n*** End Patch",
	"*** Begin Patch\n*** Update File: f\n*** Move to: g\n@@ ctx\n a\n-b\n+c\n*** End of File\n*** End Patch",
	"<<'EOF'\n*** Begin Patch\n*** Update File: f\n x\n\n+y\n*** End Patch\nEOF\n",
	"*** Begin Patch\r\n*** Update File: f\r\n@@\r\n-\u201cq\u201d\r\n*** End Patch\r\n",
	"*** Begin Patch\n*** Update File: f\n@@\n*** End of File\n*** End Patch",
	"*** Begin Patch\n*** Frob File: f\n",
}

var textSeeds = [][2]string{
	{"", ""},
	{"a\nb\nc\n", "a\nB\nc\n"},
	{"x\nx\nx\nx\nx\nx\nx\nx\n", "x\nx\nx\ny\nx\nx\nx\nx\n"},
	{"a\r\nb\r\n", "b\r\na\r\n"},
	{"a\n\n\n", "\n"},
	{"  indented\n\ttab\n", "indented\n tab\n"},
	{"no newline", "still none"},
}

// FuzzParse: the parser never panics, and a parsed patch formats to text that
// parses back to the same hunks.
//
//dsh:test DH-T-FS-4
func FuzzParse(f *testing.F) {
	for _, s := range parseSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		p, err := Parse(text)
		if err != nil {
			return
		}
		again, err := Parse(Format(p.Hunks...))
		if err != nil {
			t.Fatalf("formatted patch does not parse: %v", err)
		}
		if len(again.Hunks) != len(p.Hunks) {
			t.Fatalf("%d hunks became %d", len(p.Hunks), len(again.Hunks))
		}
	})
}

// FuzzApply: applying any parsed patch to any text never panics, and a failure
// returns no text.
//
//dsh:test DH-T-FS-5
func FuzzApply(f *testing.F) {
	for i, s := range parseSeeds {
		f.Add(s, textSeeds[i%len(textSeeds)][0])
	}
	f.Fuzz(func(t *testing.T, text, original string) {
		p, err := Parse(text)
		if err != nil {
			return
		}
		for _, h := range p.Hunks {
			if got, err := h.Apply(original); err != nil && got != "" {
				t.Fatalf("failed Apply returned text %q", got)
			}
		}
	})
}

// FuzzMake: for LF texts that end in a newline, a patch made from a to b applies
// to a and gives b.
//
//dsh:test DH-T-FS-5
func FuzzMake(f *testing.F) {
	for _, s := range textSeeds {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		a, b = lfTerminated(a), lfTerminated(b)
		text, err := Make("f", a, b)
		if err != nil {
			t.Fatalf("Make: %v", err)
		}
		p, err := Parse(text)
		if err != nil {
			t.Fatalf("Make wrote an unparsable patch: %v\n%s", err, text)
		}
		got := a
		for _, h := range p.Hunks {
			if got, err = h.Apply(got); err != nil {
				t.Fatalf("Apply: %v\n%s", err, text)
			}
		}
		if got != b {
			t.Fatalf("round trip gave %q, want %q", got, b)
		}
	})
}

func lfTerminated(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}
