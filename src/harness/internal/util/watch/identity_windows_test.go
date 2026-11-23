//go:build windows

package watch_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch/watchtest"
)

// shortName returns the 8.3 short form of path, or "" when the volume keeps none.
func shortName(t *testing.T, path string) string {
	t.Helper()
	long, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetShortPathName(long, &buf[0], uint32(len(buf)))
	if err != nil {
		t.Fatal(err)
	}
	if short := windows.UTF16ToString(buf[:n]); !strings.EqualFold(short, path) {
		return short
	}
	return ""
}

// TestShortNameAndCaseOneWatch: one file watched by its long name, its 8.3 short
// name and in a different letter case is one registration of its directory, and a
// change reaches every watch once (DH-LIB-CHOKIDAR-R8).
//
//dsh:test DH-T-CHOKIDAR-6
func TestShortNameAndCaseOneWatch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "A Long Directory Name")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "settings file.json")
	write(t, f, "1")
	spellings := []string{f, strings.ToUpper(f)}
	if short := shortName(t, f); short != "" {
		spellings = append(spellings, short)
	} else {
		t.Logf("the volume of %s keeps no 8.3 names; the short-name row is not run", f)
	}
	fake := watchtest.New()
	s := service(t, watch.Options{NewNotifier: fake.Factory()})
	var recs []recorder
	for _, p := range spellings {
		_, r := start(t, s, watch.Target{Path: p, Mode: watch.Notify})
		recs = append(recs, r)
	}
	if got, want := fake.Watched(), []string{canon(t, dir)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("watched %q, want the one directory %q", got, want)
	}
	write(t, f, "2")
	fake.Send(filepath.Join(canon(t, dir), filepath.Base(f)))
	for i, r := range recs {
		if err := r.one(t, spellings[i]); err != nil {
			t.Fatalf("%s: notice carried %v", spellings[i], err)
		}
	}
}
