//go:build scenario

package scenarios

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// abandonAfter is how long after its deadline scenario.Main ends the test binary
// over a tracked scenario that still runs.
const abandonAfter = 30 * time.Second

// A shell command still running when its scenario's trees are stopped -- what
// the scenario deadline does -- ends with its whole tree, a descendant holding
// its output included, well before the test binary would be abandoned.
func TestShellTreeStopsAtTheScenarioDeadline(t *testing.T) {
	bin := launcher.Build(t, "../launcher", "./testdata/echo")
	r := startShell(t, bin+" hold") // unquoted: cmd /C gets quotes escaped
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for r.run.host.Stdout.String() == "" {
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
	if r.err == nil {
		t.Errorf("the stopped shell reported success")
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
