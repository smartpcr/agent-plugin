//go:build scenario

package scenarios

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	//lint:ignore scenarioimport checks the legacy shell helpers' process trees (StopCause, exit), which only internal/world/subprocess exposes
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	//lint:ignore scenarioimport checks the legacy shell helpers' process trees (StopCause, exit), which only internal/world/subprocess/subprocesstest exposes
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// abandonAfter is how long after its deadline scenario.Main ends the test binary
// over a tracked scenario that still runs.
const abandonAfter = 30 * time.Second

// A shell command still running when its scenario's trees are stopped -- what
// the scenario deadline does -- ends with its whole tree, a descendant holding
// its output included, well before the test binary would be abandoned. The
// stop comes only once the shell itself has exited 0 (echo hold leaves the
// grandchild holding the output), so the command must report the stop rather
// than the shell's own clean exit.
func TestShellTreeStopsAtTheScenarioDeadline(t *testing.T) {
	bin := launcher.Build(t, "../launcher", "./testdata/echo")
	r := startShell(t, bin+" hold")
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	shellPid := r.run.host.Process.Pid()
	for r.run.host.Stdout.String() == "" || !subprocesstest.Exited(shellPid) {
		select {
		case <-r.done:
			t.Fatalf("%s ended before its tree was stopped: %v\n%s", r.line, r.err, r.out)
		case <-tick.C:
		}
	}
	start := time.Now()
	if err := launcher.StopTrees(t.Name()); err != nil {
		t.Fatalf("StopTrees: %v", err)
	}
	select {
	case <-r.done:
	case <-time.After(abandonAfter):
		t.Fatalf("%s still runs %s after its trees were stopped", r.line, abandonAfter)
	}
	if took := time.Since(start); took > 3*launcher.Grace {
		t.Errorf("the stopped shell ended after %s; want within %s", took, 3*launcher.Grace)
	}
	if code := r.run.host.Process.ExitCode(); code != 0 {
		t.Errorf("the shell exited %d; want 0, the case where only the stop cause tells", code)
	}
	if !errors.Is(r.err, subprocess.ErrStopped) {
		t.Errorf("the stopped shell reported %v; want %v", r.err, subprocess.ErrStopped)
	}
	fields := strings.Fields(r.out)
	if len(fields) == 0 {
		t.Fatalf("no grandchild pid in %q (err %v)", r.out, r.err)
	}
	grandchild, err := strconv.Atoi(fields[0])
	if err != nil || grandchild == 0 {
		t.Fatalf("no grandchild pid in %q", r.out)
	}
	if !subprocesstest.Exited(grandchild) {
		t.Errorf("the grandchild %d holding the output survives the stopped shell", grandchild)
	}
}

// A shell command line reaches the shell unchanged: a quoted program path with a
// space runs (on Windows the editor commands quote C:\Program Files), and the
// line's exit code is the command's.
func TestShellLineQuotesAPathWithASpace(t *testing.T) {
	bin := launcher.Build(t, "../launcher", "./testdata/echo")
	dir := filepath.Join(t.TempDir(), "with space")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	spaced := filepath.Join(dir, filepath.Base(bin))
	if err := os.WriteFile(spaced, data, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := shell(t, `"`+spaced+`" tags`); err != nil {
		t.Errorf("quoted path with a space: %v\n%s", err, out)
	}
	if out, err := shell(t, "exit 3"); err == nil || !strings.Contains(err.Error(), "exit 3") {
		t.Errorf("exit 3 = %v (%q); want exit 3", err, out)
	}
}

// A modification time that moves, size unchanged, to a time before the change
// started is this client's attribute cache catching up on its own earlier write
// (CIFS does it a few milliseconds after a write), not the change; the change
// itself, and a time from its start on, are. Each case is a real stat of a file.
func TestStatChangeTellsTheClientsOwnRefreshFromTheChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watched.txt")
	if err := os.WriteFile(path, []byte("start\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stat := func() os.FileInfo {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	setTime := func(at time.Time) os.FileInfo {
		t.Helper()
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
		return stat()
	}
	before := stat()
	started := before.ModTime().Add(time.Hour)
	if got := statChange(before, stat(), started); got != unchanged {
		t.Errorf("the same stat = %v, want unchanged", got)
	}
	refreshed := setTime(started.Add(-30 * time.Minute))
	if got := statChange(before, refreshed, started); got != ownRefresh {
		t.Errorf("a time-only move to before the change started = %v, want the client's own refresh (%v)", got, ownRefresh)
	}
	if got := statChange(refreshed, setTime(started.Add(time.Second)), started); got != changeSeen {
		t.Errorf("a time-only move to after the change started = %v, want the change (%v)", got, changeSeen)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("changed\n")
	if err = errors.Join(err, f.Close()); err != nil {
		t.Fatal(err)
	}
	if got := statChange(refreshed, setTime(started.Add(-30*time.Minute)), started); got != changeSeen {
		t.Errorf("a size change with an earlier time = %v, want the change (%v)", got, changeSeen)
	}
}

// A shell command run from a cleanup callback, where the test's context has
// ended, still runs to completion instead of being canceled at once.
func TestShellRunsFromACleanup(t *testing.T) {
	var out string
	var err error
	t.Run("run", func(t *testing.T) {
		t.Cleanup(func() { out, err = shell(t, "echo ran") })
	})
	if err != nil || !strings.Contains(out, "ran") {
		t.Errorf("shell from a cleanup = %q, %v; want ran", out, err)
	}
}

// failingFile fails every write after the first n bytes, as a full disk does,
// and can fail its close too.
type failingFile struct {
	room     int
	closeErr error
	closed   bool
}

var errDiskFull = errors.New("no space left on device")

func (f *failingFile) Write(p []byte) (int, error) {
	if len(p) > f.room {
		n := f.room
		f.room = 0
		return n, errDiskFull
	}
	f.room -= len(p)
	return len(p), nil
}

func (f *failingFile) Close() error { f.closed = true; return f.closeErr }

func TestTimingReportSurfacesWriteAndCloseErrors(t *testing.T) {
	full := &failingFile{room: 20}
	r := newTimingReport(full)
	if err := r.add(map[string]int{"a": 1}); err != nil {
		t.Fatalf("first line fits, got %v", err)
	}
	if err := r.add(map[string]string{"repo": strings.Repeat("x", 40)}); !errors.Is(err, errDiskFull) {
		t.Fatalf("a short write: add returned %v, want %v", err, errDiskFull)
	}
	if err := r.add(map[string]int{"b": 2}); !errors.Is(err, errDiskFull) {
		t.Fatalf("after a failed write add must keep failing, got %v", err)
	}
	if err := r.close(); !errors.Is(err, errDiskFull) || !full.closed {
		t.Fatalf("close after a failed write returned %v (closed %v), want %v", err, full.closed, errDiskFull)
	}

	errClose := errors.New("close: input/output error")
	r = newTimingReport(&failingFile{room: 1 << 10, closeErr: errClose})
	if err := r.add(map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if err := r.close(); !errors.Is(err, errClose) {
		t.Fatalf("a failed close returned %v, want %v", err, errClose)
	}

	r = newTimingReport(&failingFile{room: 1 << 10})
	if err := r.add(map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if err := r.close(); err != nil {
		t.Fatalf("a complete report: close returned %v", err)
	}
}

func TestRGFileListKeepsNewlinesAndCarriageReturns(t *testing.T) {
	out := []byte("./plain.go\x00./dir/line\nbreak.txt\x00./ends-in-cr\r\x00./\r\nboth\x00no-prefix.md\x00")
	got := rgFileList(out)
	want := map[string]bool{"plain.go": true, "dir/line\nbreak.txt": true, "ends-in-cr\r": true, "\r\nboth": true, "no-prefix.md": true}
	if !maps.Equal(got, want) {
		t.Fatalf("rgFileList = %q, want %q", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
	if got := rgFileList(nil); len(got) != 0 {
		t.Fatalf("no output: got %q", slices.Sorted(maps.Keys(got)))
	}
}
