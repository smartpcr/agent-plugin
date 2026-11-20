package patch

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseHunks(t *testing.T) {
	text := strings.Join([]string{
		"*** Begin Patch",
		"*** Add File: path/add.py",
		"+abc",
		"+def",
		"*** Delete File: path/delete.py",
		"*** Update File: path/update.py",
		"*** Move to: path/update2.py",
		"@@ def f():",
		"-    pass",
		"+    return 123",
		"*** End Patch",
	}, "\n")
	p, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	want := []Hunk{
		{Op: OpAdd, Path: "path/add.py", Contents: "abc\ndef\n", Line: 2},
		{Op: OpDelete, Path: "path/delete.py", Line: 5},
		{Op: OpUpdate, Path: "path/update.py", MoveTo: "path/update2.py", Line: 6, Chunks: []Chunk{{
			Context: "def f():", HasContext: true, Old: []string{"    pass"}, New: []string{"    return 123"}, Line: 8,
		}}},
	}
	if !reflect.DeepEqual(p.Hunks, want) {
		t.Fatalf("hunks\n%+v\nwant\n%+v", p.Hunks, want)
	}
}

func TestParseChunks(t *testing.T) {
	text := "*** Begin Patch\n" +
		"*** Update File: f\n" +
		" import foo\n" + // first chunk without @@
		"+bar\n" +
		"\n" + // an empty line is empty context
		"@@\n" +
		"-x\n" +
		"*** End of File\n" +
		"\n" + // blank line between chunks is skipped
		"@@ class C\n" +
		" y\n" +
		"*** End Patch\n"
	p, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	got := p.Hunks[0].Chunks
	want := []Chunk{
		{Old: []string{"import foo", ""}, New: []string{"import foo", "bar", ""}, Line: 3},
		{Old: []string{"x"}, EOF: true, Line: 6},
		{Context: "class C", HasContext: true, Old: []string{"y"}, New: []string{"y"}, Line: 10},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chunks\n%+v\nwant\n%+v", got, want)
	}
}

func TestParseLenient(t *testing.T) {
	body := "*** Begin Patch\n*** Delete File: a\n*** End Patch"
	for name, text := range map[string]string{
		"heredoc":          "<<EOF\n" + body + "\nEOF\n",
		"quoted heredoc":   "<<'EOF'\n" + body + "\nEOF",
		"dquoted heredoc":  "<<\"EOF\"\n" + body + "\n  EOF  \n",
		"crlf":             strings.ReplaceAll(body, "\n", "\r\n") + "\r\n",
		"padded markers":   "\n  *** Begin Patch \n\t*** Delete File: a  \n *** End Patch\u00a0\n\n",
		"unicode trimming": "\u2003*** Begin Patch\n*** Delete File: a\n*** End Patch\u3000",
	} {
		p, err := Parse(text)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(p.Hunks) != 1 || p.Hunks[0].Op != OpDelete || p.Hunks[0].Path != "a" {
			t.Errorf("%s: hunks %+v", name, p.Hunks)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name, text string
		line       int
		msg        string
	}{
		{"empty", "", 1, "The first line of the patch must be '*** Begin Patch'"},
		{"bad first line", "bad\n*** End Patch", 1, "The first line of the patch must be '*** Begin Patch'"},
		{"no end", "*** Begin Patch\n*** Delete File: a", 3, "The last line of the patch must be '*** End Patch'"},
		{"after end", "*** Begin Patch\n*** End Patch\nmore", 3, "The last line of the patch must be '*** End Patch'"},
		{"bad header", "*** Begin Patch\n*** Frobnicate File: a\n*** End Patch", 2,
			"'*** Frobnicate File: a' is not a valid hunk header. Valid hunk headers: '*** Add File: {path}', '*** Delete File: {path}', '*** Update File: {path}'"},
		{"empty update", "*** Begin Patch\n*** Update File: a\n*** End Patch", 2, "Update file hunk for path 'a' is empty"},
		{"marker only", "*** Begin Patch\n*** Update File: a\n@@\n*** End Patch", 4, "Update hunk does not contain any lines"},
		{"eof only", "*** Begin Patch\n*** Update File: a\n@@\n*** End of File\n*** End Patch", 4, "Update hunk does not contain any lines"},
		{"unexpected line", "*** Begin Patch\n*** Update File: a\n@@\nbad\n*** End Patch", 4,
			"Unexpected line found in update hunk: 'bad'. Every line should start with ' ' (context line), '+' (added line), or '-' (removed line)"},
		{"second chunk without marker", "*** Begin Patch\n*** Update File: a\n@@\n-x\n*** End of File\n+y\n*** End Patch", 6,
			"Expected update hunk to start with a @@ context marker, got: '+y'"},
		{"unclosed heredoc", "<<EOF\n*** Begin Patch\n*** End Patch", 4, "The heredoc wrapper of the patch is not closed with EOF"},
		{"add then junk", "*** Begin Patch\n*** Add File: a\n+x\njunk\n*** End Patch", 4,
			"'junk' is not a valid hunk header. Valid hunk headers: '*** Add File: {path}', '*** Delete File: {path}', '*** Update File: {path}'"},
	}
	for _, c := range cases {
		_, err := Parse(c.text)
		var pe *Error
		if !errors.As(err, &pe) {
			t.Errorf("%s: error %v is not an *Error", c.name, err)
			continue
		}
		if pe.Reason() != ReasonSyntax || pe.Line != c.line || pe.Msg != c.msg {
			t.Errorf("%s: got %s line %d %q; want line %d %q", c.name, pe.Reason(), pe.Line, pe.Msg, c.line, c.msg)
		}
		if !strings.Contains(pe.Error(), c.msg) {
			t.Errorf("%s: Error() = %q", c.name, pe.Error())
		}
	}
}

// TestFeedEqualsParse: feeding a patch line by line gives what Parse gives, and a
// Parser keeps returning its first error.
func TestFeedEqualsParse(t *testing.T) {
	text := "*** Begin Patch\n*** Update File: f\n@@ ctx\n a\n-b\n+c\n*** Add File: g\n+1\n*** End Patch\n"
	whole, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	var p Parser
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if err := p.Feed(line); err != nil {
			t.Fatal(err)
		}
	}
	fed, err := p.End()
	if err != nil || !reflect.DeepEqual(fed, whole) {
		t.Fatalf("fed %+v %v, parsed %+v", fed, err, whole)
	}

	var bad Parser
	first := bad.Feed("nope")
	if first == nil || bad.Feed("*** Begin Patch") != first {
		t.Fatal("a Parser did not keep its first error")
	}
	if _, err := bad.End(); err != first {
		t.Fatalf("End after an error: %v", err)
	}
}

func TestFormatParsesBack(t *testing.T) {
	hunks := []Hunk{
		Add("new.txt", "one\r\ntwo"),
		Delete("old.txt"),
		{Op: OpUpdate, Path: "u.txt", MoveTo: "v.txt", Chunks: []Chunk{
			{Context: "func f", HasContext: true, Old: []string{"a", ""}, New: []string{"b"}},
			{Old: []string{"z"}, New: nil, EOF: true},
		}},
	}
	p, err := Parse(Format(hunks...))
	if err != nil {
		t.Fatal(err)
	}
	for i := range p.Hunks {
		p.Hunks[i].Line = 0
		for j := range p.Hunks[i].Chunks {
			p.Hunks[i].Chunks[j].Line = 0
		}
	}
	if hunks[0].Contents != "one\ntwo\n" || !reflect.DeepEqual(p.Hunks, hunks) {
		t.Fatalf("parsed back\n%+v\nwant\n%+v", p.Hunks, hunks)
	}
	if got := Format(); got != BeginPatch+"\n"+EndPatch+"\n" {
		t.Fatalf("empty patch %q", got)
	}
}
