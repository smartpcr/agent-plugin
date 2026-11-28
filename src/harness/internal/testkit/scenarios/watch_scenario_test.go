//go:build scenario

package scenarios

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/util/watch until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
)

// saveWindow is how long after a save a watch must have reported it
// (DH-E-CHOKIDAR-1), and quiet is how long the test then waits for a duplicate.
//
// remoteGrace is how long after a remote change command exits its change may take
// to become visible on this machine (attribute caching of SMB and NFS clients).
const (
	saveWindow  = time.Second
	quiet       = 2 * time.Second
	remoteGrace = 5 * time.Second
)

// offsets renders report times relative to at, for a failure message.
func offsets(times []time.Time, at time.Time) []time.Duration {
	out := make([]time.Duration, len(times))
	for i, t := range times {
		out[i] = t.Sub(at)
	}
	return out
}

// burstWrites is how many writes one burst makes: DH-T-CHOKIDAR-2's burst of 100
// writes, which a watch reports once.
const burstWrites = 100

// checkTransitions watches the directory dir (created here, depth 0) through svc
// in mode, then makes the transitions of a watched tree one after another: create
// a file, a burst of burstWrites appends to it, rename it, delete it. Each
// transition must be reported exactly once within bound of the moment it finished
// (the last write of the burst), and nothing more within quiet after that. A watch
// callback with an error fails the test. It returns the watch's mode.
func checkTransitions(t *testing.T, svc *watch.Service, dir string, mode watch.Mode, bound time.Duration) watch.Mode {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var got arrivals
	w, err := svc.Watch(t.Context(), watch.Target{Path: dir, Mode: mode}, func(err error) {
		if err != nil {
			t.Errorf("%s: directory watch failed: %v", dir, err)
		}
		got.record()
	})
	if err != nil {
		t.Fatalf("%s: %v", dir, err)
	}
	t.Cleanup(func() { _ = w.Close() })

	file, renamed := filepath.Join(dir, "transition.txt"), filepath.Join(dir, "renamed.txt")
	steps := []struct {
		name string
		do   func() error
	}{
		// The file appears whole, renamed in from beside the watched directory:
		// written in place (os.WriteFile creates it empty first, as does fsx's
		// staging inside the directory), an on-access scanner holding the create
		// past a settle window makes a genuinely settled empty or staged file, a
		// second correct report.
		{"create", func() error {
			staged := dir + ".create"
			if err := os.WriteFile(staged, []byte("created\n"), 0o600); err != nil {
				return err
			}
			if err := os.Rename(staged, file); err != nil {
				return errors.Join(err, os.Remove(staged))
			}
			return nil
		}},
		{"burst", func() error {
			f, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				return err
			}
			for i := range burstWrites {
				if _, err := fmt.Fprintf(f, "write %d\n", i); err != nil {
					return errors.Join(err, f.Close())
				}
			}
			return f.Close()
		}},
		{"rename", func() error { return os.Rename(file, renamed) }},
		{"delete", func() error { return os.Remove(renamed) }},
	}
	for _, s := range steps {
		started := time.Now()
		if err := s.do(); err != nil {
			t.Fatalf("%s: %s: %v", dir, s.name, err)
		}
		done := time.Now()
		<-time.After(time.Until(done.Add(bound)) + quiet)
		reports := got.since(started)
		switch {
		case len(reports) == 0:
			t.Errorf("%s: %s: no report within %v", dir, s.name, bound)
		case reports[0].Sub(done) > bound:
			t.Errorf("%s: %s: reported %v after it finished, want within %v", dir, s.name, reports[0].Sub(done), bound)
		case len(reports) > 1:
			t.Errorf("%s: %s: reported %d times, at %v", dir, s.name, len(reports), offsets(reports, done))
		}
	}
	return w.Mode()
}

// The transitions check holds on a local directory in both modes a real system
// uses: notifications and polling (the assertions DH-T-CHOKIDAR-1 runs in both).
func TestWatchTransitionsCheck(t *testing.T) {
	const poll = 250 * time.Millisecond
	for mode, bound := range map[watch.Mode]time.Duration{watch.Notify: saveWindow, watch.Poll: 2 * poll} {
		t.Run(mode.String(), func(t *testing.T) {
			svc := watch.New(watch.Options{FilePoll: poll, DirPoll: poll})
			t.Cleanup(func() { _ = svc.Close() })
			if got := checkTransitions(t, svc, filepath.Join(t.TempDir(), "tree"), mode, bound); got != mode {
				t.Errorf("watch in %s mode, want %s", got, mode)
			}
		})
	}
}

// TestWatchRealEditorSaves: each editor save of a watched file is reported once
// (libs/chokidar.md DH-E-CHOKIDAR-1; DH-LIB-CHOKIDAR-R1; DH-D-UTIL-3). Executed in
// DH-S-18 (step 18.8) on Linux, macOS and Windows.
//
// Given a harness watch service with a watched credentials file under a temporary
// home and a watched file in a temporary workspace, when each real editor of
// DSH_SCENARIO_EDITORS saves the file (the editors cover rename, write in place and
// backup-then-write, e.g. Vim, VS Code and Notepad), then each save is reported
// exactly once within one second, and the content read back is the saved content.
// The save is timed when the file first changes (polled while the editor runs) and
// each report when its callback runs, so an editor that lingers after saving cannot
// hide a late report. Then, in a watched directory of the workspace, a file is
// created, written in a burst of burstWrites appends, renamed and deleted, and
// each of these transitions is reported exactly once within one second.
// Inputs: DSH_SCENARIO_EDITORS is a ";"-separated list of shell command lines, each
// saving {file} with the line {marker} appended and exiting when saved, e.g.
// `vim -Es -c "normal Go{marker}" -c wq {file}`. Cleanup: the home and workspace
// are under t.TempDir().
//
//dsh:scenario DH-E-CHOKIDAR-1
func TestWatchRealEditorSaves(t *testing.T) {
	editors := needList(t, "DSH_SCENARIO_EDITORS", "the editor save commands, ';'-separated, with {file} and {marker}")
	svc := watch.New(watch.Options{})
	t.Cleanup(func() { _ = svc.Close() })

	files := map[string]string{
		"credentials": filepath.Join(t.TempDir(), "home", "credentials.json"),
		"workspace":   filepath.Join(t.TempDir(), "workspace", "notes.txt"),
	}
	for role, path := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("start\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var got arrivals
		w, err := svc.Watch(t.Context(), watch.Target{Path: path, Mode: watch.Auto}, func(err error) {
			if err != nil {
				t.Errorf("%s watch failed: %v", role, err)
			}
			got.record()
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = w.Close() })
		t.Logf("%s watch in %s mode", role, w.Mode())

		for i, editor := range editors {
			marker := fmt.Sprintf("saved-%s-%d-%d", role, i, time.Now().UnixNano())
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			r := startShell(t, expand(editor, map[string]string{"file": path, "marker": marker}))
			// The save is the moment the file changes, not the moment the editor
			// exits; each report is timed when its callback runs.
			saved := changedAt(t, path, before, r, 0)
			r.wait(t)
			<-time.After(time.Until(saved.Add(saveWindow)) + quiet)
			reports := got.since(started)
			switch {
			case len(reports) == 0:
				t.Errorf("%s, editor %d (%s): no report within %v of the save", role, i, editor, saveWindow)
			case reports[0].Sub(saved) > saveWindow:
				t.Errorf("%s, editor %d (%s): reported %v after the save, want within %v", role, i, editor, reports[0].Sub(saved), saveWindow)
			case len(reports) > 1:
				t.Errorf("%s, editor %d (%s): one save reported %d times, at %v", role, i, editor, len(reports), offsets(reports, saved))
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s, editor %d: read back: %v", role, i, err)
			}
			if !strings.Contains(string(data), marker) {
				t.Errorf("%s, editor %d: content read back lacks the saved line %q:\n%s", role, i, marker, data)
			}
		}
	}
	mode := checkTransitions(t, svc, filepath.Join(t.TempDir(), "workspace", "tree"), watch.Auto, saveWindow)
	t.Logf("workspace tree watch in %s mode", mode)
}

// TestWatchRemoteFilesystemsPoll: a watch on a filesystem without notifications
// polls by itself and says so (libs/chokidar.md DH-E-CHOKIDAR-2;
// DH-LIB-CHOKIDAR-R5; DH-D-UTIL-3). Executed in DH-S-18 (step 18.8).
//
// Given a workspace on a real SMB share, one on a real NFS mount and a Windows
// directory opened from WSL, when a file is changed from another machine or from
// the other side, then the watch reports Poll mode and the change is reported
// within two poll intervals. Inputs: DSH_SCENARIO_REMOTE_DIRS, a ";"-separated list
// of such directories as this machine sees them; DSH_SCENARIO_REMOTE_CHANGE, a
// shell command line that changes {name} in the directory {dir} from the other
// machine or side (e.g. over ssh, or through wsl.exe), such as appending {marker}
// so its size changes. The change is timed when this machine first sees the file
// differ, and the report when its callback runs. Then, in a new directory on each
// remote directory, a file is created, written in a burst, renamed and deleted from
// this machine, and the watch, in Poll mode, reports each transition exactly once
// within two poll intervals. Cleanup: the test files and directories are
// removed; unmounting is the operator's. A wrong mode is logged with the directory.
//
//dsh:scenario DH-E-CHOKIDAR-2
func TestWatchRemoteFilesystemsPoll(t *testing.T) {
	dirs := needList(t, "DSH_SCENARIO_REMOTE_DIRS", "directories on SMB, NFS and a WSL-crossed mount, ';'-separated")
	change := need(t, "DSH_SCENARIO_REMOTE_CHANGE", "command changing {name} in {dir} from the other machine or side")
	const poll = 250 * time.Millisecond
	svc := watch.New(watch.Options{FilePoll: poll, DirPoll: poll})
	t.Cleanup(func() { _ = svc.Close() })

	for _, dir := range dirs {
		name := fmt.Sprintf("dsh-watch-%d.txt", time.Now().UnixNano())
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("start\n"), 0o600); err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		t.Cleanup(func() { _ = os.Remove(path) })
		before, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		var got arrivals
		w, err := svc.Watch(t.Context(), watch.Target{Path: path, Mode: watch.Auto}, func(error) { got.record() })
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		t.Cleanup(func() { _ = w.Close() })
		if w.Mode() != watch.Poll {
			t.Errorf("%s: watch is in %s mode, want poll", dir, w.Mode())
		}
		started := time.Now()
		r := startShell(t, expand(change, map[string]string{"dir": dir, "name": name, "marker": fmt.Sprint(started.UnixNano())}))
		// The change is timed when this machine first sees it, which a lingering
		// remote command cannot delay; the report is timed when its callback runs.
		changed := changedAt(t, path, before, r, remoteGrace)
		r.wait(t)
		<-time.After(time.Until(changed.Add(2 * poll)))
		// A report of this client's own refresh (changedAt) is not a report of the
		// change: only reports from the moment the change is visible count, less
		// the millisecond the two polls may be apart.
		reports := got.since(changed.Add(-2 * time.Millisecond))
		switch {
		case len(reports) == 0:
			t.Errorf("%s: the remote change was not reported within %v", dir, 2*poll)
		case reports[0].Sub(changed) > 2*poll:
			t.Errorf("%s: reported %v after the change was visible, want within %v", dir, reports[0].Sub(changed), 2*poll)
		}

		tree := filepath.Join(dir, fmt.Sprintf("dsh-watch-tree-%d", time.Now().UnixNano()))
		t.Cleanup(func() { _ = os.RemoveAll(tree) })
		if mode := checkTransitions(t, svc, tree, watch.Auto, 2*poll); mode != watch.Poll {
			t.Errorf("%s: directory watch is in %s mode, want poll", tree, mode)
		}
	}
}
