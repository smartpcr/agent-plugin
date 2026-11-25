package tools

import (
	iofs "io/fs"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
)

// A search sees each provider entry as io/fs does: the file type maps to the
// io/fs mode bits, and Info reports the entry's name, size, mode, time and
// kind with no system data.
func TestDirEntryIsTheEntryAsIOFSSeesIt(t *testing.T) {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, tc := range []struct {
		typ   fs.FileType
		mode  iofs.FileMode
		isDir bool
	}{
		{fs.TypeFile, 0, false},
		{fs.TypeDir, iofs.ModeDir, true},
		{fs.TypeSymlink, iofs.ModeSymlink, false},
		{fs.TypeOther, iofs.ModeIrregular, false},
	} {
		var d iofs.DirEntry = dirEntry{fs.DirEntry{Name: "n", Type: tc.typ, Size: 7, ModTime: when}}
		if d.Name() != "n" || d.IsDir() != tc.isDir || d.Type() != tc.mode {
			t.Fatalf("%s: name %q, dir %v, type %v; want dir %v, type %v", tc.typ, d.Name(), d.IsDir(), d.Type(), tc.isDir, tc.mode)
		}
		in, err := d.Info()
		if err != nil {
			t.Fatal(err)
		}
		if in.Name() != "n" || in.Size() != 7 || in.Mode() != tc.mode || !in.ModTime().Equal(when) || in.IsDir() != tc.isDir || in.Sys() != nil {
			t.Fatalf("%s: info %q %d %v %v %v %v", tc.typ, in.Name(), in.Size(), in.Mode(), in.ModTime(), in.IsDir(), in.Sys())
		}
	}
}

// clipBytes never splits a rune: a cut inside one backs off to its start.
func TestClipBytesCutsAtARuneBoundary(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
		cut  bool
	}{
		{"abc", 3, "abc", false},
		{"abc", 2, "ab", true},
		{"aéb", 2, "a", true},  // inside the two-byte é
		{"aéb", 3, "aé", true}, // just after it
		{"€", 2, "", true},     // inside a three-byte rune at the start
	} {
		got, cut := clipBytes(tc.in, tc.n)
		if got != tc.want || cut != tc.cut || !utf8.ValidString(got) {
			t.Fatalf("clipBytes(%q, %d) = %q, %v; want %q, %v", tc.in, tc.n, got, cut, tc.want, tc.cut)
		}
	}
}
