//go:build testhook

package launch_test

import (
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
)

// TestForcedWrittenCanaryRunsNothing: a testhook build told to report canary
// written reaches the host with a ready that fails its check; the host rejects
// and the command never runs (TC-04b).
//
//dsh:test DH-T-SANDBOX-4
func TestForcedWrittenCanaryRunsNothing(t *testing.T) {
	s := startStage(t, unconfinedPlan(), launch.TestHookCanaryEnv+"="+string(launch.CanaryWritten))
	ready, err := s.ch.ReadReady(timeout(t))
	if err != nil {
		t.Fatalf("ReadReady: %v", err)
	}
	if ready.Canary != launch.CanaryWritten {
		t.Fatalf("ready canary = %s, want the forced written", ready.Canary)
	}
	if err := ready.Agrees(s.plan); !herr.Has(err, sandbox.CodeLaunchFailed) {
		t.Fatalf("Agrees = %v, want %s", err, sandbox.CodeLaunchFailed)
	}
	s.ch.Reject()
	if code := s.exitCode(t); code != launch.ExitFailed {
		t.Errorf("the stage exited %d, want %d", code, launch.ExitFailed)
	}
	if s.markerExists(t) {
		t.Error("the command ran after a written canary")
	}
}
