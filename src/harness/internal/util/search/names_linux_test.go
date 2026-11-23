//go:build linux

package search

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"unicode/utf8"
)

// TestNonUTF8Name: on Linux a file whose name is not valid UTF-8 is listed and
// searched, and the returned name opens it; a line with invalid UTF-8 in it comes
// back with the placeholder.
//
//dsh:test DH-T-RIPGREP-5
func TestNonUTF8Name(t *testing.T) {
	dir := t.TempDir()
	name := "caf\xe9.txt" // Latin-1 é: not UTF-8
	if utf8.ValidString(name) {
		t.Fatal("the fixture name is valid UTF-8")
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("a needle \xff here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := OpenDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	listed, _, err := List(context.Background(), FilesQuery{From: src})
	if err != nil || !reflect.DeepEqual(listed, []string{name}) {
		t.Fatalf("listed %q, %v; want %q", listed, err, name)
	}
	ms := grepAll(t, GrepQuery{From: src, Pattern: "needle"})
	if len(ms) != 1 || ms[0].Path != name || ms[0].Text != "a needle � here" {
		t.Fatalf("matches %+v", ms)
	}
	f, err := src.Open(ms[0].Path)
	if err != nil {
		t.Fatalf("the returned name does not open: %v", err)
	}
	defer f.Close()
	if b, err := io.ReadAll(f); err != nil || string(b) != "a needle \xff here\n" {
		t.Fatalf("read %q, %v", b, err)
	}
}
