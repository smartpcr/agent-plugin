package tools_test

import (
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	htools "github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/bash"
	shelltools "github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/tools"
)

// TestDeclarationsNeedTheirDependencies: the tools are not declared, nor
// mounted, without the shell registry or the policy resolver; the catalogue
// item names the component and its configuration.
//
//dsh:test DH-T-EXEC-15
func TestDeclarationsNeedTheirDependencies(t *testing.T) {
	reg, _ := fakeRegistry(t)
	for name, deps := range map[string]shelltools.Deps{
		"no shell registry":  {Resolver: &sandboxtest.Resolver{}},
		"no policy resolver": {Shells: reg},
	} {
		if ds, err := shelltools.Declarations(deps, shelltools.OneShotConfig{}); err == nil || ds != nil {
			t.Errorf("%s: Declarations = %v, %v; want an error", name, ds, err)
		}
	}
	if _, err := shelltools.Mount(nil, nil, shelltools.Deps{}, shelltools.OneShotConfig{}); err == nil {
		t.Error("Mount without dependencies succeeded")
	}
	it := shelltools.Item()
	if it.Name != shelltools.Name || it.Component == nil || it.Config != reflect.TypeFor[shelltools.OneShotConfig]() {
		t.Fatalf("Item = %+v", it)
	}
}

// TestComponentMountsBothTools: the shellexec.tools component, given the shell
// registry, the policy resolver, the working directory and the tool registry
// in its context, mounts bash and pwsh; each validates its arguments through
// the pipeline before anything starts.
//
//dsh:test DH-T-EXEC-15
func TestComponentMountsBothTools(t *testing.T) {
	reg, seam := fakeRegistry(t)
	r := newRigMounted(t, reg, sandbox.DangerFullAccess, func(root *cordis.Context, svc htools.Service, deps shelltools.Deps) error {
		if _, err := shellexec.Key.Provide(root, deps.Shells); err != nil {
			return err
		}
		if _, err := sandbox.ResolverKey.Provide(root, deps.Resolver); err != nil {
			return err
		}
		if _, err := workdir.Key.Provide(root, deps.Workdir); err != nil {
			return err
		}
		if _, err := htools.Key.Provide(root, svc); err != nil {
			return err
		}
		f, err := root.Use(shelltools.Component, shelltools.OneShotConfig{})
		if err == nil {
			err = f.Wait()
		}
		return err
	})
	for _, name := range []string{shelltools.BashName, shelltools.PwshName} {
		res, _ := r.run(name, map[string]any{"command": "echo hi", "stdin": "x"})
		if !res.IsError || res.Code != htools.CodeInvalidArguments {
			t.Errorf("%s: result = %+v, want %s from a mounted tool", name, res, htools.CodeInvalidArguments)
		}
	}
	if n := seam.prepared.Load(); n != 0 {
		t.Fatalf("the seam was asked for %d plans: a command was started", n)
	}
}

// TestRefusedEscalationStartsNothing: a call that asks for a wider sandbox
// mode is decided before anything starts; refused, the call fails with
// sandbox/escalation-rejected and no plan is asked of the sandbox.
//
//dsh:test DH-T-EXEC-15
func TestRefusedEscalationStartsNothing(t *testing.T) {
	reg, seam := fakeRegistry(t)
	r := newRig(t, reg, sandbox.WorkspaceWrite)
	res, _ := r.run(shelltools.BashName, map[string]any{
		"command": "echo hi", "sandbox_permissions": string(sandbox.DangerFullAccess), "justification": "needs the network",
	})
	if !res.IsError || !strings.Contains(res.Text, string(sandbox.CodeEscalationRejected)) {
		t.Fatalf("result = %+v, want %s", res, sandbox.CodeEscalationRejected)
	}
	if n := seam.prepared.Load(); n != 0 {
		t.Fatalf("the seam was asked for %d plans", n)
	}
}

// failingDirs is a working-directory lookup whose session, or its path,
// cannot be read.
type failingDirs struct {
	access, path error
}

func (d failingDirs) Access(session.ID) (workdir.Access, error) {
	if d.access != nil {
		return nil, d.access
	}
	return d, nil
}
func (d failingDirs) Current() (workdir.WorkingDirectory, error) {
	return workdir.WorkingDirectory{}, d.path
}
func (d failingDirs) Path() (string, error) { return "", d.path }
func (d failingDirs) Root() (string, error) { return "", d.path }

// TestCallWithoutADirectoryStartsNothing: a call with no session is
// sandbox/unavailable, and a session whose working directory cannot be read
// fails with that error; in neither case is a plan asked of the sandbox.
//
//dsh:test DH-T-EXEC-15
func TestCallWithoutADirectoryStartsNothing(t *testing.T) {
	reg, seam := fakeRegistry(t)
	res := &sandboxtest.Resolver{Default: sandbox.DangerFullAccess}
	in := shelltools.In{Command: "echo hi"}
	run := htools.Run{Agent: agent.Ref{Session: "s-1", Incarnation: 1}}
	if _, err := shelltools.RunBash(bg, shelltools.Deps{Shells: reg, Resolver: res}, in, htools.Run{}); !herr.Has(err, sandbox.CodeUnavailable) {
		t.Errorf("no session: %v; want %s", err, sandbox.CodeUnavailable)
	}
	gone := errors.New("the directory is gone")
	for name, wd := range map[string]failingDirs{"access": {access: gone}, "path": {path: gone}} {
		if _, err := shelltools.RunBash(bg, shelltools.Deps{Shells: reg, Resolver: res, Workdir: wd}, in, run); !errors.Is(err, gone) {
			t.Errorf("%s: %v; want %v", name, err, gone)
		}
	}
	if n := seam.prepared.Load(); n != 0 {
		t.Fatalf("the seam was asked for %d plans", n)
	}
}

// TestTimedOutCommandReportsItsStop: a command that outlives its timeout is
// stopped and its result says so (outcome timed-out, stop reason timeout with
// the budget and the cause), not an error. The row fails, never skips, on
// Linux when bash is missing.
//
//dsh:test DH-T-EXEC-15
func TestTimedOutCommandReportsItsStop(t *testing.T) {
	h := shellexec.LocalHost(os.Environ())
	if _, err := bash.Find("", h); err != nil {
		if runtime.GOOS == "linux" {
			t.Fatalf("bash is required on the Linux lane: %v", err)
		}
		t.Skipf("no bash on this lane: %v", err)
	}
	e, err := bash.New(bash.Config{Timeout: 60 * time.Second}, h, shellexec.Options{Sandbox: localSandbox(t), Env: builder()})
	if err != nil {
		t.Fatal(err)
	}
	reg := shellexec.NewRegistry()
	if _, err := reg.Register(e); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, reg, sandbox.DangerFullAccess)
	res, _ := r.run(shelltools.BashName, map[string]any{"command": "sleep 30", "timeout": 300})
	if res.IsError {
		t.Fatalf("a timed-out command is an error: %s", res.Text)
	}
	out, ok := res.Value.(shelltools.Result)
	if !ok {
		t.Fatalf("value = %T", res.Value)
	}
	if out.Outcome != string(shellexec.TimedOut) || out.Stop == nil || out.Stop.Reason != shelltools.StopTimeout ||
		out.Stop.BudgetMs != 300 || out.Stop.Cause == "" {
		t.Fatalf("result = %+v, stop %+v; want timed out after 300 ms with a cause", out, out.Stop)
	}
}
