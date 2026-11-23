package subprocesstest

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
)

// A running process has not exited; once it is killed it has, even before its
// parent reaps it, which on Unix leaves a zombie that Alive still finds.
func TestExitedCountsAnUnreapedZombie(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := os.StartProcess(exe, []string{exe}, &os.ProcAttr{Env: childEnv(RoleStubborn), Files: []*os.File{nil, nil, nil}})
	if err != nil {
		t.Fatal(err)
	}
	reaped := false
	t.Cleanup(func() {
		if !reaped {
			_ = p.Kill()
			_, _ = p.Wait()
		}
	})
	if !Alive(p.Pid) || Exited(p.Pid) {
		t.Fatalf("a running child: Alive %v, Exited %v; want alive and not exited", Alive(p.Pid), Exited(p.Pid))
	}
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	procwait.Until(ctx, t, func() bool { return Exited(p.Pid) })
	if runtime.GOOS == "linux" && !Alive(p.Pid) {
		t.Fatalf("killed child %d is gone before this parent reaped it; want a zombie", p.Pid)
	}
	if _, err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	reaped = true
	if Alive(p.Pid) || !Exited(p.Pid) {
		t.Fatalf("a reaped child: Alive %v, Exited %v; want gone", Alive(p.Pid), Exited(p.Pid))
	}
}
