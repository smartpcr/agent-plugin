//go:build scenario

package scenarios

import (
	"errors"
	"os"
	"path/filepath"
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
