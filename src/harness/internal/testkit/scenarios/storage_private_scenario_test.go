//go:build scenario

package scenarios

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// storage6Dirs are the stored-data directories below the home that the second
// account must not reach (areas/storage.md DH-E-STORAGE-6).
var storage6Dirs = []string{"blobs", "spill", "cache"}

// storage6Files returns the regular files below dir, at most limit of them.
func storage6Files(t *testing.T, dir string, limit int) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && len(files) < limit {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("listing %s as the owner: %v", dir, err)
	}
	return files
}

// storage6Permissive runs line with a permissive process mask on POSIX, so the
// modes the home gets are the ones dsh sets, not the ones the mask leaves.
func storage6Permissive(line string) string {
	if runtime.GOOS == "windows" {
		return line
	}
	return "umask 000; " + line
}

// TestAnotherLocalUserCannotReadStoredData: another local user cannot read stored
// data, and a spill directory others could write is refused while the rest of the
// host runs (areas/storage.md DH-E-STORAGE-6; DH-D-STORAGE-2, DH-ADR-STORAGE-8).
// Executed in DH-S-18 (step 18.10) once on each of Linux, macOS and Windows;
// authored in DH-S-6 (gap-closure Stage 13.6, M1-G-CLOSE-7). It writes only below
// the scratch directory it makes and never changes the second account.
//
// Given a machine with two ordinary user accounts, the scenario running as user 1
// and the second account supplied at run time, the binary built from this
// checkout, a permissive process mask, and a home in a fresh directory that both
// accounts can traverse (so only the home's own permissions can stop user 2),
// seeded through dsh with an attachment and a spill file, when (1) user 2 lists
// user 1's blobs, spill and cache directories and reads the files in them, and
// then, on POSIX, (2) user 1 makes the spill directory group-writable and
// restarts the host, then every access in (1) is denied and prints none of the
// stored names or bytes, and after (2) the spill component refuses to start with
// a message naming the directory while the rest of the host runs. A positive
// control first lists the shared directory as user 2, so a command that cannot
// run as user 2 at all fails the scenario instead of passing it. Cleanup: the
// mode is restored, the host stopped and the scratch directory deleted; on
// failure it is kept and named. The second account was not created by the
// scenario and is left as it was.
//
// Inputs. DSH_SCENARIO_STORAGE6_USER is the second account's name and
// DSH_SCENARIO_STORAGE6_PUBLIC a directory both accounts can traverse and list
// (for example /tmp, or C:\Users\Public). Command lines run through the platform
// shell over {dsh}, {home}, {log}, {user} and {path}, where {dsh} is the binary
// this scenario builds (each line that runs dsh must run {dsh}):
// DSH_SCENARIO_STORAGE6_SEED creates, through {dsh} on {home}, one attachment and
// one spill file and returns when both are stored; DSH_SCENARIO_STORAGE6_LIST
// lists the directory {path} as {user} and DSH_SCENARIO_STORAGE6_READ prints the
// file {path} as {user}, each exiting non-zero when access is denied. On POSIX
// also: DSH_SCENARIO_STORAGE6_START starts the host on {home} in the background,
// logging to {log}, and returns once it is ready or has failed, printing what it
// reported; DSH_SCENARIO_STORAGE6_STATUS exits 0 when the host on {home} runs;
// DSH_SCENARIO_STORAGE6_STOP stops it. A missing input fails the scenario, naming
// the variable; it never skips.
//
//dsh:scenario DH-E-STORAGE-6
func TestAnotherLocalUserCannotReadStoredData(t *testing.T) {
	posix := runtime.GOOS != "windows"
	user := need(t, "DSH_SCENARIO_STORAGE6_USER", "the second ordinary account, which runs the list and read commands")
	public := need(t, "DSH_SCENARIO_STORAGE6_PUBLIC", "a directory both accounts can traverse and list")
	seedT := need(t, "DSH_SCENARIO_STORAGE6_SEED", "command storing, through {dsh} on {home}, an attachment and a spill file")
	listT := need(t, "DSH_SCENARIO_STORAGE6_LIST", "command listing the directory {path} as {user}, non-zero when denied")
	readT := need(t, "DSH_SCENARIO_STORAGE6_READ", "command printing the file {path} as {user}, non-zero when denied")
	var startT, statusT, stopT string
	if posix {
		startT = need(t, "DSH_SCENARIO_STORAGE6_START", "command starting the host on {home} in the background, log to {log}, returning once ready or failed")
		statusT = need(t, "DSH_SCENARIO_STORAGE6_STATUS", "command exiting 0 when the host on {home} runs")
		stopT = need(t, "DSH_SCENARIO_STORAGE6_STOP", "command stopping the host on {home}")
	}
	for name, line := range map[string]string{"DSH_SCENARIO_STORAGE6_SEED": seedT, "DSH_SCENARIO_STORAGE6_START": startT, "DSH_SCENARIO_STORAGE6_STOP": stopT} {
		if line != "" && !strings.Contains(line, "{dsh}") {
			t.Fatalf("%s does not run {dsh}, the binary built from this checkout: %s", name, line)
		}
	}
	for name, line := range map[string]string{"DSH_SCENARIO_STORAGE6_LIST": listT, "DSH_SCENARIO_STORAGE6_READ": readT} {
		if !strings.Contains(line, "{path}") {
			t.Fatalf("%s does not name {path}: %s", name, line)
		}
	}

	bin := dshBinary(t)
	base, err := os.MkdirTemp(public, "dsh-storage6-")
	if err != nil {
		t.Fatalf("making the scratch directory in %s: %v", public, err)
	}
	if posix {
		// Traversable by user 2, so only the home's own modes can deny access.
		if err := os.Chmod(base, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	home := filepath.Join(base, "home")
	vars := map[string]string{"dsh": bin, "home": home, "log": filepath.Join(base, "host.log"), "user": user}
	as := func(template, path string) (string, error) {
		v := map[string]string{"path": path}
		for k, val := range vars {
			v[k] = val
		}
		return shell(t, expand(template, v))
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("kept the scratch directory %s (home %s, host log %s)", base, home, vars["log"])
			return
		}
		if err := os.RemoveAll(base); err != nil {
			t.Errorf("removing %s: %v", base, err)
		}
	})

	// The positive control: user 2 can list the shared directory.
	if out, err := as(listT, public); err != nil {
		t.Fatalf("DSH_SCENARIO_STORAGE6_LIST cannot list %s as %s, so a denial below would prove nothing: %v\n%s", public, user, err, out)
	}

	// The home holds an attachment and a spill file, written with a permissive mask.
	mustShell(t, storage6Permissive(expand(seedT, vars)))
	stored := map[string][]string{}
	for _, name := range storage6Dirs {
		dir := filepath.Join(home, name)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("the seeded home has no %s directory: %v", dir, err)
		}
		stored[name] = storage6Files(t, dir, 5)
	}
	for _, name := range []string{"blobs", "spill"} {
		if len(stored[name]) == 0 {
			t.Fatalf("DSH_SCENARIO_STORAGE6_SEED stored no file in %s", filepath.Join(home, name))
		}
	}

	t.Run("the second account is denied", func(t *testing.T) {
		for _, name := range storage6Dirs {
			dir := filepath.Join(home, name)
			out, err := as(listT, dir)
			if err == nil {
				t.Errorf("%s listed %s:\n%s", user, dir, out)
			}
			for _, f := range stored[name] {
				if strings.Contains(out, filepath.Base(f)) {
					t.Errorf("listing %s as %s printed the stored name %s:\n%s", dir, user, filepath.Base(f), out)
				}
			}
			for _, f := range stored[name] {
				data, rerr := os.ReadFile(f)
				if rerr != nil {
					t.Fatalf("reading %s as the owner: %v", f, rerr)
				}
				out, err := as(readT, f)
				if err == nil {
					t.Errorf("%s read %s:\n%s", user, f, out)
				}
				if head := strings.TrimSpace(string(data[:min(len(data), 32)])); len(head) >= 8 && strings.Contains(out, head) {
					t.Errorf("reading %s as %s printed its bytes:\n%s", f, user, out)
				}
			}
		}
	})

	if !posix {
		t.Logf("step 2 (a group-writable spill directory) is POSIX only (areas/storage.md DH-E-STORAGE-6); on %s the access checks above are the whole scenario", runtime.GOOS)
		return
	}
	t.Run("a group-writable spill directory is refused", func(t *testing.T) {
		spill := filepath.Join(home, "spill")
		if err := os.Chmod(spill, 0o770); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if out, err := shell(t, expand(stopT, vars)); err != nil {
				t.Errorf("stopping the host: %v\n%s", err, out)
			}
			if err := os.Chmod(spill, 0o700); err != nil {
				t.Errorf("restoring the mode of %s: %v", spill, err)
			}
		})
		out, err := shell(t, storage6Permissive(expand(startT, vars)))
		log, lerr := os.ReadFile(vars["log"])
		if lerr != nil && !os.IsNotExist(lerr) {
			t.Fatalf("reading the host log: %v", lerr)
		}
		reported := out + "\n" + string(log)
		if !strings.Contains(reported, spill) {
			t.Errorf("the restarted host does not name the refused spill directory %s (start: %v):\n%s", spill, err, reported)
		}
		if out, err := shell(t, expand(statusT, vars)); err != nil {
			t.Errorf("the rest of the host does not run after the spill component refused: %v\n%s", err, out)
		}
	})
}
