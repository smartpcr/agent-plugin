//go:build windows

package subprocess_test

import (
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// A background child opens no window: it is created with CREATE_NO_WINDOW, so it
// has a console and no console window (DH-LIB-NODEPROC-R11, the other half of
// DH-T-NODEPROC-8).
//
//dsh:test DH-T-NODEPROC-8
func TestBackgroundChildOpensNoWindow(t *testing.T) {
	if got := strings.TrimSpace(subprocesstest.Output(t, subprocesstest.Spec(t, subprocesstest.RoleConsole))); got != "no-window" {
		t.Fatalf("the child reported %q; want no-window", got)
	}
}
