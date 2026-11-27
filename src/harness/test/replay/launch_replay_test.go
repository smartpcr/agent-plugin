//go:build replay

package replay

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// echo builds the test kit launcher's test program (testdata/echo).
func echo(t *testing.T) string {
	t.Helper()
	return launcher.Build(t, "../../internal/testkit/launcher", "./testdata/echo")
}

// A replay whose child exits while a descendant still holds its standard output
// is not left waiting on that output: the whole tree is stopped at the bound,
// the descendant included, and the run fails with the timeout.
func TestLaunchReplayStopsATreeHoldingItsOutput(t *testing.T) {
	bin := echo(t)
	start := time.Now()
	out, err := launchReplay(t, 2*time.Second, launcher.Program{Argv: []string{bin, "hold"}, MaxOutput: maxReplayOutput})
	if took := time.Since(start); !errors.Is(err, launcher.ErrTimeout) || took > 2*time.Second+3*launcher.Grace {
		t.Fatalf("launchReplay = %v after %s; want %v within the bound and the stop", err, took, launcher.ErrTimeout)
	}
	grandchild, perr := strconv.Atoi(strings.TrimSpace(out))
	if perr != nil || grandchild == 0 {
		t.Fatalf("no grandchild pid in %q", out)
	}
	if !subprocesstest.Exited(grandchild) {
		t.Errorf("the grandchild %d holding the output survives the timed-out replay", grandchild)
	}
}

// Output past the cap is dropped, not buffered, and fails the run.
func TestLaunchReplayCapsItsOutput(t *testing.T) {
	out, err := launchReplay(t, time.Minute, launcher.Program{Argv: []string{echo(t), "flood"}, MaxOutput: 1 << 10})
	if err == nil || !strings.Contains(err.Error(), "output cap") || len(out) != 1<<10 {
		t.Errorf("launchReplay kept %d bytes, err %v; want 1024 and an output-cap error", len(out), err)
	}
}
