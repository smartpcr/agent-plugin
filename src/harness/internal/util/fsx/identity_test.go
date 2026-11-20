package fsx

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIdentity(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.WriteFile(a, []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	ida, err := ID(a)
	if err != nil {
		t.Fatal(err)
	}
	idb, _ := ID(b)
	if ida == idb {
		t.Fatalf("two files share the identity %v", ida)
	}
	if again, _ := ID(a); again != ida {
		t.Fatalf("identity unstable: %v then %v", ida, again)
	}
	if _, err := ID(dir); err != nil {
		t.Fatalf("directory identity: %v", err)
	}
	if _, err := ID(filepath.Join(dir, "none")); !os.IsNotExist(err) {
		t.Fatalf("missing file: %v", err)
	}

	link := filepath.Join(dir, "link")
	if err := os.Symlink(a, link); err != nil {
		t.Logf("no symbolic links here (%v); skipping the link case", err)
	} else {
		if idl, err := ID(link); err != nil || idl != ida {
			t.Fatalf("through a link: %v %v, want %v", idl, err, ida)
		}
		ca, _ := Canonical(a)
		cl, err := Canonical(link)
		if err != nil || cl != ca {
			t.Fatalf("Canonical(link) = %q %v, want %q", cl, err, ca)
		}
		cn, _ := Canonical(filepath.Join(link, "..", "new", "file"))
		if !strings.HasSuffix(cn, filepath.Join("new", "file")) {
			t.Fatalf("Canonical of a path to create: %q", cn)
		}
	}
	if hard := filepath.Join(dir, "hard"); os.Link(a, hard) == nil {
		if idh, _ := ID(hard); idh != ida {
			t.Fatalf("hard link identity %v, want %v", idh, ida)
		}
	}
}

func TestVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	v1, err := Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if v1.Size != 3 || strings.Count(v1.String(), ":") != 4 {
		t.Fatalf("version %+v token %q", v1, v1.String())
	}
	if !v1.Same(v1) {
		t.Fatal("a version is not the same as itself")
	}
	if err := WriteFile(path, []byte("two!"), Options{}); err != nil {
		t.Fatal(err)
	}
	v2, _ := Stat(path)
	if v1.Same(v2) {
		t.Fatal("a replaced file kept its version")
	}

	d1, err := v2.WithDigest(strings.NewReader("two!"))
	if err != nil {
		t.Fatal(err)
	}
	touched := d1
	touched.ModNs++
	touched.ChangeNs++
	if !d1.Same(touched) {
		t.Fatal("with digests, a time-only change counted as a change")
	}
	other := d1
	other.Digest = strings.Repeat("0", 64)
	if d1.Same(other) {
		t.Fatal("different digests compared equal")
	}
	if _, err := Stat(filepath.Join(filepath.Dir(path), "none")); err == nil {
		t.Fatal("Stat of a missing file succeeded")
	}
	if got, _ := Digest(strings.NewReader("")); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("digest of nothing: %s", got)
	}
}

func TestInside(t *testing.T) {
	sep := string(filepath.Separator)
	root := filepath.Join(sep+"a", "b")
	if runtime.GOOS == "windows" {
		root = `C:\a\b`
	}
	cases := []struct {
		path string
		want bool
	}{
		{root, true},
		{filepath.Join(root, "c"), true},
		{filepath.Join(root, "c", "..", "d"), true},
		{filepath.Join(root, ".."), false},
		{root + "c", false},
		{filepath.Dir(root), false},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases,
			struct {
				path string
				want bool
			}{`c:\A\B\x`, true},
			struct {
				path string
				want bool
			}{`D:\a\b`, false})
	}
	for _, c := range cases {
		if got := Inside(root, c.path); got != c.want {
			t.Errorf("Inside(%q, %q) = %v, want %v", root, c.path, got, c.want)
		}
	}
}

func TestIsRemote(t *testing.T) {
	remote, err := IsRemote(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if remote {
		t.Log("the temporary directory is on a network filesystem")
	}
	if _, err := IsRemote(filepath.Join(t.TempDir(), "none")); runtime.GOOS != "windows" && err == nil {
		t.Fatal("probe of a missing path succeeded")
	}
}

func TestIsText(t *testing.T) {
	nulAt9000 := bytes.Repeat([]byte("a"), 9000)
	nulAt9000 = append(nulAt9000, 0)
	cases := []struct {
		name string
		in   []byte
		want bool
	}{
		{"empty", nil, true},
		{"ascii", []byte("hello\n"), true},
		{"utf8", []byte("héllo — ok"), true},
		{"nul", []byte("a\x00b"), false},
		{"invalid", []byte{'a', 0xff, 'b'}, false},
		{"nul at byte 9000", nulAt9000, false},
		{"utf16le", Encode("hi\r\n", UTF16LE), true},
		{"utf16be", Encode("hi 🙂", UTF16BE), true},
		{"utf16 without a mark", Encode("hi", UTF16LE)[2:], false},
		{"utf16 odd length", []byte{0xFF, 0xFE, 'a'}, false},
		{"utf16 lone low surrogate", []byte{0xFF, 0xFE, 0x00, 0xDC}, false},
		{"utf16 unpaired high surrogate", []byte{0xFF, 0xFE, 0x00, 0xD8, 'a', 0}, false},
		{"utf16 high surrogate at end", []byte{0xFF, 0xFE, 0x00, 0xD8}, false},
	}
	for _, c := range cases {
		if got := IsText(c.in); got != c.want {
			t.Errorf("%s: IsText = %v, want %v", c.name, got, c.want)
		}
	}
	if !IsBinarySniff([]byte("a\x00")) || IsBinarySniff(nulAt9000) || IsBinarySniff(Encode("x", UTF16LE)) {
		t.Error("IsBinarySniff judged wrongly")
	}
}

func TestEOL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\nb\n", "\n"},
		{"a\r\nb\r\n", "\r\n"},
		{"a\r\nb\nc\r\n", "\r\n"},
		{"a\r\nb\nc\n", "\n"},
		{"", "\n"},
	}
	for _, c := range cases {
		if got := DominantEOL(c.in); got != c.want {
			t.Errorf("DominantEOL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := NormalizeEOL("a\r\nb\n"); got != "a\nb\n" {
		t.Fatalf("NormalizeEOL: %q", got)
	}
	if got := RestoreEOL("a\nb\r\n", "\r\n"); got != "a\r\nb\r\n" {
		t.Fatalf("RestoreEOL CRLF: %q", got)
	}
	if got := RestoreEOL("a\nb", "\n"); got != "a\nb" {
		t.Fatalf("RestoreEOL LF: %q", got)
	}
}

func TestEncoding(t *testing.T) {
	for _, enc := range []Encoding{UTF8, UTF8BOM, UTF16LE, UTF16BE} {
		const s = "héllo, 世界 🙂"
		got, gotEnc, err := Decode(Encode(s, enc))
		if err != nil || got != s || gotEnc != enc {
			t.Errorf("encoding %d: %q %d %v", enc, got, gotEnc, err)
		}
	}
	if _, _, err := Decode([]byte{0xFF, 0xFE, 'a'}); err != ErrBadUTF16 {
		t.Fatalf("odd UTF-16: %v", err)
	}
}
