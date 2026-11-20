package mediatype

import (
	"mime"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTable is DH-T-MIME-1.
func TestTable(t *testing.T) {
	for _, e := range table {
		pref, ok := PreferredExtension(e.typ)
		if !ok || !slices.Contains(Extensions(e.typ), pref) {
			t.Errorf("%s: preferred %q not among %v", e.typ, pref, Extensions(e.typ))
		}
		for _, x := range e.exts {
			if got, ok := ByPath("dir/file" + x); !ok || got != e.typ {
				t.Errorf("ByPath(file%s) = %q, %v; want %s", x, got, ok, e.typ)
			}
			if x != strings.ToLower(x) || !strings.HasPrefix(x, ".") {
				t.Errorf("%s: extension %q is not lower case with a dot", e.typ, x)
			}
		}
	}
	if got, ok := ByPath(`C:\Docs\REPORT.PDF`); !ok || got != "application/pdf" {
		t.Errorf("case and Windows path: %q %v", got, ok)
	}
	if got, ok := PreferredExtension("Text/Markdown; charset=utf-8"); !ok || got != ".md" {
		t.Errorf("parameters not ignored: %q %v", got, ok)
	}
	if _, ok := ByPath("file.unknownext"); ok {
		t.Error("an unknown extension has a type")
	}
	if _, ok := ByPath("noext"); ok {
		t.Error("a name without extension has a type")
	}
	if Extensions("x/none") != nil {
		t.Error("unknown type has extensions")
	}
	ex := Extensions("image/jpeg")
	ex[0] = "mutated"
	if Extensions("image/jpeg")[0] != ".jpg" {
		t.Error("Extensions exposes the table")
	}
}

// TestProcessTableCannotChangeAnswers is the in-package part of DH-T-MIME-2.
func TestProcessTableCannotChangeAnswers(t *testing.T) {
	if err := mime.AddExtensionType(".md", "x/y"); err != nil {
		t.Fatal(err)
	}
	if got, _ := ByPath("a.md"); got != "text/markdown" {
		t.Fatalf("ByPath(.md) = %q after mime.AddExtensionType", got)
	}
}

// TestLanguage is the mediatype part of DH-T-UTIL-8.
func TestLanguage(t *testing.T) {
	for in, want := range map[string]string{
		"Dockerfile":         "dockerfile",
		"build/Dockerfile":   "dockerfile",
		"Dockerfile.dev":     "dockerfile",
		`src\GNUmakefile`:    "makefile",
		"Makefile":           "makefile",
		"main.go":            "go",
		"a/b/App.TSX":        "tsx",
		"script.ps1":         "powershell",
		"notes.unknownthing": "plaintext",
		"README":             "plaintext",
	} {
		if got := Language(in); got != want {
			t.Errorf("Language(%q) = %q, want %q", in, got, want)
		}
	}
	for _, m := range []map[string]string{langByName, langByExt} {
		for k, l := range m {
			if !slices.Contains(Languages, l) {
				t.Errorf("%q maps to %q, which the highlighter does not accept", k, l)
			}
		}
	}
}

func TestSafeName(t *testing.T) {
	long := strings.Repeat("é", 200) + ".txt"
	for _, tc := range []struct{ name, typ, ext, want string }{
		{"report.pdf", "application/pdf", "", "report.pdf"},
		{"report", "application/pdf", "", "report.pdf"},
		{"photo.jpeg", "image/jpeg", "", "photo.jpeg"},
		{"photo.png", "image/jpeg", "", "photo.png.jpg"},
		{"a<b>:c?.txt", "text/plain", "", "a_b__c_.txt"},
		{"../../etc/passwd", "text/plain", "", "passwd.txt"},
		{"CON.txt", "text/plain", "", "_CON.txt"},
		{"lpt1", "text/plain", "", "_lpt1.txt"},
		{"trailing. . ", "text/plain", "", "trailing.txt"},
		{"", "application/zip", "", "download.zip"},
		{"blob", "x/unknown", "bin", "blob.bin"},
		{"blob.dat", "x/unknown", "", "blob.dat"},
	} {
		if got := SafeName(tc.name, tc.typ, tc.ext); got != tc.want {
			t.Errorf("SafeName(%q, %q, %q) = %q, want %q", tc.name, tc.typ, tc.ext, got, tc.want)
		}
	}
	got := SafeName(long, "text/plain", "")
	if len(got) > maxNameBytes || !utf8.ValidString(got) || !strings.HasSuffix(got, ".txt") {
		t.Errorf("long name: %d bytes, valid %v, %q", len(got), utf8.ValidString(got), got[len(got)-8:])
	}
}
