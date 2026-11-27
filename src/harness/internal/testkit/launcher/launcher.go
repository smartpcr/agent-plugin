// Package launcher is the test kit's scenario launcher (DH-D-UTIL-6). It builds a
// program, starts it through internal/world/subprocess (the one place a program
// starts) against a private temp home, and returns a JSON-RPC client
// (src/sdk/jsonrpc) on its standard streams. `//go:build scenario` files use it to
// drive a real binary; every release -- the client, the pipes, the process tree
// and the home -- is registered with t.Cleanup (QG-5).
package launcher

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// Grace is how long a launched program has to exit on its own once its standard
// input closes, and then again after the polite stop, before it is killed.
const Grace = 5 * time.Second

// Build compiles package pkg (an import path or a path relative to dir) with
// `go build` run in dir, passing tags as -tags, into a temporary directory, and
// returns the binary's absolute path. A failed build fails the test with the
// compiler's output.
func Build(t testing.TB, dir, pkg string, tags ...string) string {
	t.Helper()
	env := subprocess.Env(os.Environ())
	gobin, err := subprocess.Lookup("go", env)
	if err != nil {
		t.Fatalf("launcher: %v", err)
	}
	out := filepath.Join(t.TempDir(), filepath.Base(pkg))
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	argv := []string{gobin, "build", "-o", out}
	if len(tags) > 0 {
		tagList := tags[0]
		for _, tag := range tags[1:] {
			tagList += "," + tag
		}
		argv = append(argv, "-tags", tagList)
	}
	var output testkit.Buffer
	p, err := subprocess.Start(t.Context(), subprocess.Spec{Argv: append(argv, pkg), Dir: dir, Env: env, Stdout: &output, Stderr: &output, Grace: Grace, Launch: sandbox.Trusted("launcher: go build of the program under test")})
	if err != nil {
		t.Fatalf("launcher: go build %s: %v", pkg, err)
	}
	if err := p.Wait(); err != nil || p.ExitCode() != 0 {
		t.Fatalf("launcher: go build %s: %v (exit %d)\n%s", pkg, err, p.ExitCode(), output.String())
	}
	return out
}

// Program says what to launch.
type Program struct {
	// Argv is the binary (absolute, as Build returns) and its arguments.
	Argv []string
	// Env are "NAME=value" entries added to the host environment, which loses its
	// credentials, its secret names and its DSH_* names first; DSH_HOME always
	// names the temp home, and a DSH_HOME entry here is overridden.
	Env []string
	// Secrets is the store whose named references, and every credential-shaped
	// name, Start removes from the host environment (SP-11); nil is an empty
	// store, which removes the credential-shaped names. An entry of Env is the
	// test's own choice and is kept.
	Secrets *secret.Store
	// Dir is the working directory; empty is the temp home's parent.
	Dir string
	// RPC configures the client; its methods answer the program's requests.
	RPC jsonrpc.Options
	// MaxOutput, when positive, caps each of the standard output and error Spawn
	// and Run capture at that many bytes; the rest is dropped and counted
	// (Host.Stdout.Dropped). Start ignores it.
	MaxOutput int
}

// Session is one launched program.
type Session struct {
	// Home is the program's private home, created before the start.
	Home home.Layout
	// Process is the started program and the tree it owns.
	Process *subprocess.Process
	// Client speaks JSON-RPC over the program's standard input and output.
	Client *jsonrpc.Conn
	// Stderr is everything the program wrote to its standard error.
	Stderr *testkit.Buffer
}

// Start launches p against a fresh temp home. When the test ends the client is
// closed, the program's standard input reaches end of file, and the program has
// Grace to exit before its whole tree is stopped; a tree that outlives the stop
// fails the test, as does a home that cannot be removed.
func Start(t testing.TB, p Program) *Session {
	t.Helper()
	if len(p.Argv) == 0 {
		t.Fatal("launcher: no program")
	}
	l := temphome.New(t)
	dir := p.Dir
	if dir == "" {
		dir = filepath.Dir(l.Root())
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatalf("launcher: %v", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		closeAll(inR, inW)
		t.Fatalf("launcher: %v", err)
	}
	// Standard error is a pipe the launcher drains, so a grandchild that inherits
	// it does not hold the process's exit open.
	errR, errW, err := os.Pipe()
	if err != nil {
		closeAll(inR, inW, outR, outW)
		t.Fatalf("launcher: %v", err)
	}
	// DSH_HOME is managed, so it is applied after p.Env: an entry there cannot point
	// the program at a home other than the one Session.Home names and cleanup removes.
	store := p.Secrets
	if store == nil {
		store = secret.NewStore()
	}
	env := subprocess.BuildEnv(os.Environ(), subprocess.EnvOptions{Secrets: store, Entries: p.Env, Managed: []string{"DSH_HOME=" + l.Root()}})
	s := &Session{Home: l, Stderr: &testkit.Buffer{}}
	proc, err := subprocess.Start(context.Background(), subprocess.Spec{Argv: p.Argv, Dir: dir, Env: env, Stdin: inR, Stdout: outW, Stderr: errW, Grace: Grace, Launch: sandbox.Trusted("launcher: the program under test, built by the test")})
	closeAll(inR, outW, errW)
	if err != nil {
		closeAll(inW, outR, errR)
		t.Fatalf("launcher: start %s: %v", p.Argv[0], err)
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(s.Stderr, errR)
	}()
	s.Process = proc
	register(t, proc)
	s.Client = jsonrpc.Serve(context.Background(), outR, inW, p.RPC)
	t.Cleanup(func() {
		_ = s.Client.Close()
		_ = inW.Close()
		// Members that outlive an exited direct child are orphans: they get no
		// grace. A child still running after Grace gets the polite stop first.
		grace := time.Duration(0)
		select {
		case <-proc.Exited():
		case <-time.After(Grace):
			grace = Grace
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*Grace)
		defer cancel()
		if err := proc.StopTree(ctx, grace); err != nil {
			t.Errorf("launcher: the tree of %s outlives the test: %v", p.Argv[0], err)
		}
		_ = proc.Wait()
		closeAll(outR, errR)
		<-drained
	})
	return s
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}
