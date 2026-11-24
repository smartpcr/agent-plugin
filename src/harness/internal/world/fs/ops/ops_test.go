package ops_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/fstest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/ops"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

const sess session.ID = "s1"

type dirs struct{ root string }

func (d dirs) Access(session.ID) (workdir.Access, error) { return d, nil }
func (d dirs) Current() (workdir.WorkingDirectory, error) {
	return workdir.WorkingDirectory{Path: d.root, Root: d.root}, nil
}
func (d dirs) Path() (string, error) { return d.root, nil }
func (d dirs) Root() (string, error) { return d.root, nil }

// recorder logs the seam's events, and the provider's view of the target at
// each, in order.
type recorder struct {
	mu  sync.Mutex
	log []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, s)
}

func (r *recorder) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.log)
}

type rig struct {
	root string
	ctx  *cordis.Context
	fs   fs.FS
	res  *sandboxtest.Resolver
	ops  *ops.Service
	rec  *recorder
}

func newRig(t *testing.T, provider fs.FS, mode sandbox.Mode) *rig {
	t.Helper()
	root := t.TempDir()
	c := cordis.New()
	res := &sandboxtest.Resolver{Default: mode, Workdir: dirs{root}}
	r := &rig{root: root, ctx: c, fs: provider, res: res, rec: &recorder{}}
	r.ops = ops.New(c, provider, res, ops.Options{Workdir: dirs{root}})
	if _, err := fs.BeforeMutation.On(c, func(_ *cordis.Context, m fs.Mutation) error {
		r.rec.add("before " + string(m.Op) + " " + filepath.Base(m.Target.Display) + " present=" + r.present(m.Target.Display))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Observed.On(c, func(_ *cordis.Context, o fs.Observation) error {
		s := "observed " + string(o.Op) + " " + filepath.Base(o.Target.Display)
		if o.Present {
			s += " present"
		} else {
			s += " absent"
		}
		r.rec.add(s)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) present(p string) string {
	in, err := r.fs.Stat(context.Background(), fs.Ref{Path: p, NoFollowFinal: true})
	if err != nil {
		return "error"
	}
	if in.Exists {
		return "yes"
	}
	return "no"
}

var call = ops.Call{Session: sess, CallID: "c1"}

//dsh:test DH-T-FS-11
func TestOperationOrderOnEachMutation(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, fstest.NewMemFS(), sandbox.WorkspaceWrite)
	if _, err := r.ops.Write(ctx, call, fs.Ref{Path: "a.txt"}, []byte("a"), fs.NoGuard); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ops.Move(ctx, call, fs.Ref{Path: "a.txt"}, fs.Ref{Path: "b.txt"}, fs.MoveOptions{}); err != nil {
		t.Fatal(err)
	}
	rc, _, err := r.ops.Open(ctx, call, fs.Ref{Path: "b.txt"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(rc)
	_ = rc.Close()
	if _, err := r.ops.Delete(ctx, call, fs.Ref{Path: "b.txt"}, fs.NoGuard); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"before write a.txt present=no", "observed write a.txt present",
		"before move-source a.txt present=yes", "before move-destination b.txt present=no",
		"observed move-source a.txt absent", "observed move-destination b.txt present",
		"observed read b.txt present",
		"before delete b.txt present=yes", "observed delete b.txt absent",
	}
	if got := r.rec.got(); !slices.Equal(got, want) {
		t.Fatalf("events:\n got %q\nwant %q", got, want)
	}
}

//dsh:test DH-T-FS-11
func TestBeforeMutationFailureLeavesTheFileUntouched(t *testing.T) {
	ctx := context.Background()
	for name, listener := range map[string]func(*cordis.Context, fs.Mutation) error{
		"error": func(*cordis.Context, fs.Mutation) error { return errors.New("capture failed") },
		"panic": func(*cordis.Context, fs.Mutation) error { panic("capture panicked") },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, fstest.NewMemFS(), sandbox.WorkspaceWrite)
			if _, err := r.ops.Write(ctx, call, fs.Ref{Path: "f"}, []byte("old"), fs.NoGuard); err != nil {
				t.Fatal(err)
			}
			if _, err := fs.BeforeMutation.On(r.ctx, listener); err != nil {
				t.Fatal(err)
			}
			_, err := r.ops.Write(ctx, call, fs.Ref{Path: "f"}, []byte("new"), fs.NoGuard)
			if herr.Kind(err) != fs.CodeAborted {
				t.Fatalf("write: %v (code %q), want %q", err, herr.Kind(err), fs.CodeAborted)
			}
			if _, err := r.ops.Delete(ctx, call, fs.Ref{Path: "f"}, fs.NoGuard); herr.Kind(err) != fs.CodeAborted {
				t.Fatalf("delete: %v, want %q", err, fs.CodeAborted)
			}
			rc, _, err := r.ops.Open(ctx, call, fs.Ref{Path: "f"})
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(rc)
			_ = rc.Close()
			if string(b) != "old" {
				t.Fatalf("file holds %q, want the old bytes", b)
			}
		})
	}
}

//dsh:test DH-T-FS-11
func TestPanickingObserverDoesNotFailTheCall(t *testing.T) {
	r := newRig(t, fstest.NewMemFS(), sandbox.WorkspaceWrite)
	if _, err := fs.Observed.On(r.ctx, func(*cordis.Context, fs.Observation) error { panic("observer") }); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ops.Write(context.Background(), call, fs.Ref{Path: "f"}, []byte("x"), fs.NoGuard); err != nil {
		t.Fatalf("write with a panicking observer: %v", err)
	}
}

// The policy is resolved on every call: a mode change between two calls applies
// to the second, and a refused call emits nothing and changes nothing.
//
//dsh:test DH-T-SANDBOX-2
func TestPolicyIsResolvedPerCall(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, local.New(), sandbox.ReadOnly)
	_, err := r.ops.Write(ctx, call, fs.Ref{Path: "f"}, []byte("x"), fs.NoGuard)
	if herr.Kind(err) != fs.CodeSandboxDenied {
		t.Fatalf("write under read-only: %v (code %q), want %q", err, herr.Kind(err), fs.CodeSandboxDenied)
	}
	if got := r.rec.got(); len(got) != 0 {
		t.Fatalf("a refused write emitted %q", got)
	}
	if err := r.res.SetMode(ctx, sess, sandbox.WorkspaceWrite); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ops.Write(ctx, call, fs.Ref{Path: "f"}, []byte("x"), fs.NoGuard); err != nil {
		t.Fatalf("write after the switch to workspace-write: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(r.root, "f")); err != nil || string(b) != "x" {
		t.Fatalf("f = %q, %v; want the bytes written under the session's root", b, err)
	}

	// Outside the session's root stays refused, before any event.
	outside := filepath.Join(t.TempDir(), "o")
	n := len(r.rec.got())
	_, err = r.ops.Write(ctx, call, fs.Ref{Path: outside}, []byte("x"), fs.NoGuard)
	if herr.Kind(err) != fs.CodeSandboxDenied {
		t.Fatalf("write outside the root: %v, want %q", err, fs.CodeSandboxDenied)
	}
	if got := r.rec.got(); len(got) != n {
		t.Fatalf("a refused write emitted %q", got[n:])
	}
	if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside file: %v, want not to exist", err)
	}

	// An explicit, approved mode for one call outranks the session's.
	ro := call
	ro.Mode = sandbox.ReadOnly
	if _, err := r.ops.Delete(ctx, ro, fs.Ref{Path: "f"}, fs.NoGuard); herr.Kind(err) != fs.CodeSandboxDenied {
		t.Fatalf("delete with an explicit read-only mode: %v, want %q", err, fs.CodeSandboxDenied)
	}
}

func TestCallWithoutSessionIsRefused(t *testing.T) {
	r := newRig(t, fstest.NewMemFS(), sandbox.WorkspaceWrite)
	_, err := r.ops.Write(context.Background(), ops.Call{}, fs.Ref{Path: filepath.Join(r.root, "f")}, []byte("x"), fs.NoGuard)
	if herr.Kind(err) != sandbox.CodeUnavailable {
		t.Fatalf("write without a session: %v (code %q), want %q", err, herr.Kind(err), sandbox.CodeUnavailable)
	}
}
