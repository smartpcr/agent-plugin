package shellenv_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret/file"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	shellenv "github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/env"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

func has(env subprocess.Env, name string) bool {
	return slices.ContainsFunc(env, func(kv string) bool {
		n, _, _ := strings.Cut(kv, "=")
		return strings.EqualFold(n, name)
	})
}

// TestSecretsNeverReachTheChild: a secret named DSH_TEST_TOKEN stored in
// human/secret and present in the parent environment, passed as an entry too,
// is absent from the built environment; so is a stored secret whose name has no
// DSH_ prefix, from the host, the entries and the managed snapshot alike. A
// fresh store learns the stored names from its managed store (M2-C-11, TC-06b).
//
//dsh:test DH-T-EXEC-13
func TestSecretsNeverReachTheChild(t *testing.T) {
	ctx := context.Background()
	l := home.New(filepath.Join(t.TempDir(), "home"))
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	dir := l.Secrets()
	token := secret.MustParse("secret://dsh/test-token")
	acme := secret.MustParse("secret://acme/api-key")
	if token.EnvName() != "DSH_TEST_TOKEN" || acme.EnvName() != "ACME_API_KEY" {
		t.Fatalf("env names = %s, %s", token.EnvName(), acme.EnvName())
	}
	writer := secret.NewStore(file.New(dir, file.Options{}))
	for _, r := range []secret.Ref{token, acme} {
		if _, err := writer.Set(ctx, r, secret.NewValue("tok-shellenv-FAKE-never-leaks"), secret.RevisionAbsent); err != nil {
			t.Fatal(err)
		}
	}
	host := []string{"DSH_TEST_TOKEN=tok-shellenv-FAKE-never-leaks", "ACME_API_KEY=tok-shellenv-FAKE-never-leaks", "PLAIN=kept"}
	reg := shellenv.NewRegistry(shellenv.Builtins{Home: "/home/dsh"})
	if _, err := reg.Contribute(shellenv.Contributor{
		Name:  "acme",
		Decls: []shellenv.Decl{{Name: "ACME_API_KEY", Description: "a managed value that shares a secret's name"}},
		Values: func(context.Context) ([]string, error) {
			return []string{"ACME_API_KEY=tok-shellenv-FAKE-never-leaks"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	for name, store := range map[string]*secret.Store{
		"writer": writer,
		"fresh":  secret.NewStore(secret.EnvSource(func(n string) (string, bool) { return "", false }), file.New(dir, file.Options{})),
	} {
		b := &shellenv.Builder{Registry: reg, Secrets: store, Host: func() []string { return host }}
		env, err := b.Environ(ctx, []string{"DSH_TEST_TOKEN=tok-shellenv-FAKE-never-leaks", "acme_api_key=tok-shellenv-FAKE-never-leaks", "FROM_CALL=1"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, leaked := range []string{"DSH_TEST_TOKEN", "ACME_API_KEY"} {
			if has(env, leaked) {
				t.Errorf("%s: the child environment holds %s: %q", name, leaked, env)
			}
		}
		if strings.Contains(strings.Join(env, "\n"), "never-leaks") {
			t.Errorf("%s: a secret value reached the child environment", name)
		}
		for _, kept := range []string{"PLAIN", "FROM_CALL", shellenv.Home} {
			if !has(env, kept) {
				t.Errorf("%s: the child environment lacks %s: %q", name, kept, env)
			}
		}
	}
}

// TestBuilderFailsClosed: a store that cannot list what it holds fails the
// build instead of letting a stored name through.
func TestBuilderFailsClosed(t *testing.T) {
	b := &shellenv.Builder{Secrets: secret.NewStore(brokenWriter{}), Host: func() []string { return nil }}
	if env, err := b.Environ(context.Background(), nil); herr.Kind(err) != shellenv.ReasonSecrets || env != nil {
		t.Fatalf("Environ = %q, %v; want %s", env, err, shellenv.ReasonSecrets)
	}
}

// brokenWriter is a managed store that cannot list what it holds.
type brokenWriter struct{}

func (brokenWriter) Origin() string { return "broken" }
func (brokenWriter) Lookup(context.Context, secret.Ref) (secret.Value, bool, error) {
	return secret.Value{}, false, nil
}
func (brokenWriter) Set(context.Context, secret.Ref, secret.Value, string) (string, error) {
	return "", errors.New("read-only")
}
func (brokenWriter) Unset(context.Context, secret.Ref) error { return errors.New("read-only") }
func (brokenWriter) Stat(context.Context, secret.Ref) (secret.Stat, error) {
	return secret.Stat{}, errors.New("broken")
}
func (brokenWriter) Stored(context.Context) ([]secret.Ref, error) { return nil, errors.New("broken") }

// TestRegistry: the built-ins come first; a duplicate, reserved, malformed or
// undocumented key is refused; undeclared output fails the snapshot; a
// release removes the contributor.
//
//dsh:test DH-T-EXEC-14
func TestRegistry(t *testing.T) {
	reg := shellenv.NewRegistry(shellenv.Builtins{Home: "/h", Executable: "/bin/dsh"})
	release, err := reg.Contribute(shellenv.Contributor{
		Name:   "plugin",
		Decls:  []shellenv.Decl{{Name: "PLUGIN_DIR", Description: "the plugin's directory"}},
		Values: func(context.Context) ([]string, error) { return []string{"PLUGIN_DIR=/p"}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	list := reg.List()
	if len(list) != 3 || list[0].Name != shellenv.Home || list[1].Name != shellenv.Executable || list[2].Contributor != "plugin" {
		t.Fatalf("List = %+v", list)
	}
	snap, err := reg.Snapshot(context.Background())
	if err != nil || !slices.Equal(snap, []string{"DSH_HOME=/h", "DSH_EXECUTABLE=/bin/dsh", "PLUGIN_DIR=/p"}) {
		t.Fatalf("Snapshot = %q, %v", snap, err)
	}
	for name, d := range map[string]shellenv.Decl{
		"duplicate":   {Name: "plugin_dir", Description: "again"},
		"builtin":     {Name: shellenv.Home, Description: "again"},
		"reserved":    {Name: "DSH_TOOL_TOKEN", Description: "x"},
		"malformed":   {Name: "1BAD", Description: "x"},
		"no describe": {Name: "OTHER", Description: " "},
	} {
		if _, err := reg.Contribute(shellenv.Contributor{Name: "other", Decls: []shellenv.Decl{d}}); herr.Kind(err) != shellenv.ReasonBadKey {
			t.Errorf("%s: Contribute = %v, want %s", name, err, shellenv.ReasonBadKey)
		}
	}
	undeclared, err := reg.Contribute(shellenv.Contributor{
		Name:   "liar",
		Decls:  []shellenv.Decl{{Name: "LIAR_A", Description: "a"}},
		Values: func(context.Context) ([]string, error) { return []string{"LIAR_B=1"}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Snapshot(context.Background()); herr.Kind(err) != shellenv.ReasonUndeclared {
		t.Fatalf("Snapshot(undeclared) = %v, want %s", err, shellenv.ReasonUndeclared)
	}
	undeclared()
	release()
	if n := len(reg.List()); n != 2 {
		t.Fatalf("List after release has %d keys, want the 2 built-ins", n)
	}
}

// TestMountLeavesWithTheFiber: a contributor mounted through a fiber's effect
// leaves when the effect is disposed.
//
//dsh:test DH-T-EXEC-14
func TestMountLeavesWithTheFiber(t *testing.T) {
	root := cordis.New()
	t.Cleanup(root.Dispose)
	reg := shellenv.NewRegistry(shellenv.Builtins{})
	h, err := shellenv.Mount(root, reg, shellenv.Contributor{Name: "fiber", Decls: []shellenv.Decl{{Name: "FIBER_X", Description: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(reg.List(), func(v shellenv.Var) bool { return v.Name == "FIBER_X" }) {
		t.Fatal("the mounted key is not listed")
	}
	h.Dispose()
	if slices.ContainsFunc(reg.List(), func(v shellenv.Var) bool { return v.Name == "FIBER_X" }) {
		t.Fatal("the key outlived its fiber's effect")
	}
}

func TestReasonsAreValid(t *testing.T) {
	for _, r := range shellenv.Reasons {
		if !r.Valid() {
			t.Errorf("reason %q is not valid", r)
		}
	}
}
