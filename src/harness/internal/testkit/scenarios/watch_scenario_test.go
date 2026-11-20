//go:build scenario

package scenarios

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
)

// saveWindow is how long after a save a watch must have reported it
// (DH-E-CHOKIDAR-1), and quiet is how long the test then waits for a duplicate.
const (
	saveWindow = time.Second
	quiet      = 2 * time.Second
)

// TestWatchRealEditorSaves: each editor save of a watched file is reported once
// (libs/chokidar.md DH-E-CHOKIDAR-1; DH-LIB-CHOKIDAR-R1; DH-D-UTIL-3). Executed in
// DH-S-18 (step 18.8) on Linux, macOS and Windows.
//
// Given a harness watch service with a watched credentials file under a temporary
// home and a watched file in a temporary workspace, when each real editor of
// DSH_SCENARIO_EDITORS saves the file (the editors cover rename, write in place and
// backup-then-write, e.g. Vim, VS Code and Notepad), then each save is reported
// exactly once within one second, and the content read back is the saved content.
// Inputs: DSH_SCENARIO_EDITORS is a ";"-separated list of shell command lines, each
// saving {file} with the line {marker} appended and exiting when saved, e.g.
// `vim -Es -c "normal Go{marker}" -c wq {file}`. Cleanup: the home and workspace
// are under t.TempDir().
//
//dsh:scenario DH-E-CHOKIDAR-1
func TestWatchRealEditorSaves(t *testing.T) {
	editors := items(need(t, "DSH_SCENARIO_EDITORS", "the editor save commands, ';'-separated, with {file} and {marker}"))
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
		var calls atomic.Int64
		notified := make(chan struct{}, 64)
		w, err := svc.Watch(t.Context(), watch.Target{Path: path, Mode: watch.Auto}, func(err error) {
			if err != nil {
				t.Errorf("%s watch failed: %v", role, err)
			}
			calls.Add(1)
			notified <- struct{}{}
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = w.Close() })
		t.Logf("%s watch in %s mode", role, w.Mode())

		for i, editor := range editors {
			marker := fmt.Sprintf("saved-%s-%d-%d", role, i, time.Now().UnixNano())
			before := calls.Load()
			mustShell(t, expand(editor, map[string]string{"file": path, "marker": marker}))
			saved := time.Now()
			select {
			case <-notified:
				if late := time.Since(saved); late > saveWindow {
					t.Errorf("%s, editor %d: reported after %v, want within %v", role, i, late, saveWindow)
				}
			case <-time.After(saveWindow):
				t.Errorf("%s, editor %d (%s): no report within %v", role, i, editor, saveWindow)
				continue
			}
			select {
			case <-notified:
				t.Errorf("%s, editor %d (%s): one save reported more than once", role, i, editor)
			case <-time.After(quiet):
			}
			if got := calls.Load() - before; got != 1 {
				t.Errorf("%s, editor %d: %d reports for one save", role, i, got)
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
// machine or side (e.g. over ssh, or through wsl.exe). Cleanup: the test files are
// removed; unmounting is the operator's. A wrong mode is logged with the directory.
//
//dsh:scenario DH-E-CHOKIDAR-2
func TestWatchRemoteFilesystemsPoll(t *testing.T) {
	dirs := items(need(t, "DSH_SCENARIO_REMOTE_DIRS", "directories on SMB, NFS and a WSL-crossed mount, ';'-separated"))
	change := need(t, "DSH_SCENARIO_REMOTE_CHANGE", "command changing {name} in {dir} from the other machine or side")
	const poll = 250 * time.Millisecond
	svc := watch.New(watch.Options{FilePoll: poll})
	t.Cleanup(func() { _ = svc.Close() })

	for _, dir := range dirs {
		name := fmt.Sprintf("dsh-watch-%d.txt", time.Now().UnixNano())
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("start\n"), 0o600); err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		t.Cleanup(func() { _ = os.Remove(path) })
		notified := make(chan struct{}, 16)
		w, err := svc.Watch(t.Context(), watch.Target{Path: path, Mode: watch.Auto}, func(error) { notified <- struct{}{} })
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		t.Cleanup(func() { _ = w.Close() })
		if w.Mode() != watch.Poll {
			t.Errorf("%s: watch is in %s mode, want poll", dir, w.Mode())
		}
		mustShell(t, expand(change, map[string]string{"dir": dir, "name": name}))
		changed := time.Now()
		select {
		case <-notified:
			if late := time.Since(changed); late > 2*poll {
				t.Errorf("%s: reported after %v, want within %v", dir, late, 2*poll)
			}
		case <-time.After(2*poll + saveWindow):
			t.Errorf("%s: the remote change was not reported", dir)
		}
	}
}
