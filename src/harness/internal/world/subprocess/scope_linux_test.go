//go:build linux

package subprocess_test

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// The real provider start (19.4.1): a re-executed host starts the provider with
// the user's systemd instance and starts one tree. A host whose own group is not
// writable and whose user manager answers (an SSH login, WSL with systemd) owns
// the tree by a control group inside the delegated scope, whose unit it names;
// without a manager the process-group rung is announced once. Which of the two
// happens is the host's environment; the test checks that what is reported is
// what the kernel says, and logs it as the evidence line.
//
//dsh:test DH-T-EXEC-4
func TestProviderStartOwnsTreesInsideADelegatedScope(t *testing.T) {
	if os.Getenv(scopeEnv) != "" {
		t.Skip("inside a delegated scope already: the own group is writable and no scope is asked for")
	}
	h := startProviderHost(t, "-")
	defer killHostOnly(t, h.host)
	b, err := os.ReadFile("/proc/" + strconv.Itoa(h.child) + "/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	group := strings.TrimSpace(string(b))
	t.Logf("owner: %s; scope unit: %s; child %d in %s; host stderr: %q", h.kind, h.scope, h.child, group, h.stderr.String())
	announces := strings.Count(h.stderr.String(), "announce: ")
	switch h.kind {
	case string(subprocess.ControlGroup):
		if h.scope != "-" && !strings.Contains(group, "/"+h.scope+"/dsh-") {
			t.Fatalf("child group %s is not a per-command group inside the scope %s", group, h.scope)
		}
		if announces != 0 {
			t.Fatalf("an exact owner was announced: %s", h.stderr)
		}
		if !strings.Contains(h.stderr.String(), "limits: enforced (memory.max=268435456, pids.max=4096)") {
			t.Fatalf("the delegated group did not take the limits: %s", h.stderr)
		}
	case string(subprocess.ProcessGroup):
		if announces != 1 || !strings.Contains(h.stderr.String(), "own control group not writable") {
			t.Fatalf("the process-group fallback was announced %d times; want once with its cause: %s", announces, h.stderr)
		}
		if !strings.Contains(h.stderr.String(), "limits: unavailable") {
			t.Fatalf("the process-group rung did not report its limits unavailable: %s", h.stderr)
		}
	default:
		t.Fatalf("owner %s on Linux", h.kind)
	}
}
