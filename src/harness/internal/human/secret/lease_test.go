package secret_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret/file"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// tempStore is a managed store on a fresh temporary home over a fallback that
// supplies env.
func tempStore(t *testing.T, env map[string]string) (*secret.Store, string) {
	t.Helper()
	l := home.New(filepath.Join(t.TempDir(), "home"))
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	return secret.NewStore(
		file.New(l.Secrets(), file.Options{}),
		secret.EnvSource(func(n string) (string, bool) { v, ok := env[n]; return v, ok }),
	), l.Secrets()
}

// closed reports whether the lease's signal has fired, waiting briefly.
func closed(l *secret.Lease) bool {
	select {
	case <-l.Revoked():
		return true
	case <-time.After(time.Second):
		return false
	}
}

// DH-T-HUMAN-12, the lease half: unsetting a reference fires the signal of every
// lease on it, and so does rotating it; a lease on another reference is not
// touched; a write by another process is signalled by Refresh; a released lease
// leaves the store's table.
//
//dsh:test DH-T-HUMAN-12
func TestLeaseSignalOnUnsetAndRotation(t *testing.T) {
	ctx := context.Background()
	ref := secret.MustParse("secret://deepseek/api-key")
	other := secret.MustParse("secret://acme/service-token")
	store, dir := tempStore(t, map[string]string{"DEEPSEEK_API_KEY": "sk-lease-env-FAKE", "ACME_SERVICE_TOKEN": "tok-lease-FAKE"})
	if _, err := store.Set(ctx, ref, secret.NewValue("sk-lease-stored-FAKE-1"), secret.RevisionAbsent); err != nil {
		t.Fatal(err)
	}

	a, err := store.Acquire(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Acquire(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	o, err := store.Acquire(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	if a.Reveal() != "sk-lease-stored-FAKE-1" || a.Source() != file.Origin || o.Source() != "env" || a.Err() != nil {
		t.Fatalf("leases: %s %s, %v", a.Source(), o.Source(), a.Err())
	}
	if err := store.Unset(ctx, ref); err != nil {
		t.Fatal(err)
	}
	for _, l := range []*secret.Lease{a, b} {
		if !closed(l) || !herr.Has(l.Err(), secret.ReasonRevoked) || !strings.Contains(l.Err().Error(), "unset") {
			t.Fatalf("Unset did not fire the lease: %v", l.Err())
		}
	}
	select {
	case <-o.Revoked():
		t.Fatal("a lease on another reference fired")
	default:
	}

	// The fallback now supplies the value; rotating it in the store fires again.
	c, err := store.Acquire(ctx, ref)
	if err != nil || c.Source() != "env" {
		t.Fatalf("after Unset the lease comes from %q: %v", c.Source(), err)
	}
	rev, err := store.Set(ctx, ref, secret.NewValue("sk-lease-stored-FAKE-2"), secret.RevisionAbsent)
	if err != nil {
		t.Fatal(err)
	}
	if !closed(c) || !strings.Contains(c.Err().Error(), "rotated") {
		t.Fatalf("Set did not fire the lease: %v", c.Err())
	}

	// Another process (here a second store on the same directory) rotates the
	// key; Refresh, run when the file changes, signals the holder.
	d, err := store.Acquire(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := store.Refresh(ctx); n != 0 || err != nil {
		t.Fatalf("Refresh with nothing changed revoked %d: %v", n, err)
	}
	if _, err := file.New(dir, file.Options{}).Set(ctx, ref, secret.NewValue("sk-lease-stored-FAKE-3"), rev); err != nil {
		t.Fatal(err)
	}
	if n, err := store.Refresh(ctx); n != 1 || err != nil || !closed(d) {
		t.Fatalf("Refresh after another writer revoked %d: %v", n, err)
	}

	o.Release()
	o.Release()
	if !closed(o) || !strings.Contains(o.Err().Error(), "released") {
		t.Fatalf("Release: %v", o.Err())
	}
	if n, err := store.Refresh(ctx); n != 0 || err != nil {
		t.Fatalf("released and revoked leases are still held: %d, %v", n, err)
	}
}

// DH-T-HUMAN-12: Set writes nothing into the process environment, and a child
// built from the host environment through BuildEnv holds no stored reference
// and no .env entry, though both resolve in the parent.
//
//dsh:test DH-T-HUMAN-12
func TestSetAndDotEnvLeaveTheEnvironment(t *testing.T) {
	ctx := context.Background()
	t.Setenv("HUMAN12_PLAIN", "kept")
	before := os.Environ()
	l := home.New(filepath.Join(t.TempDir(), "home"))
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("HUMAN12_DOTENV_ENTRY=dotenv-h12-dummy-never-leaks\nACME_DOTENV_TOKEN=tok-h12-dotenv-never-leaks\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stored := secret.MustParse("secret://human12/stored-pat")
	full := secret.NewStore(file.New(l.Secrets(), file.Options{}), secret.DotEnvSource(envFile, secret.OriginProjectEnv))
	if _, err := full.Set(ctx, stored, secret.NewValue("pat-h12-stored-never-leaks"), secret.RevisionAbsent); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"secret://human12/stored-pat", "secret://human12/dotenv-entry", "secret://acme/dotenv-token"} {
		if _, _, err := full.Resolve(ctx, secret.MustParse(r)); err != nil {
			t.Fatalf("%s: %v", r, err)
		}
	}
	after := os.Environ()
	slices.Sort(before)
	slices.Sort(after)
	if !slices.Equal(before, after) {
		t.Fatal("Set or a .env lookup changed the process environment")
	}

	spec := subprocesstest.Spec(t, subprocesstest.RoleEnviron)
	spec.Env = subprocess.BuildEnv(os.Environ(), subprocess.EnvOptions{Secrets: full, Entries: spec.Env})
	out := subprocesstest.Output(t, spec)
	if !strings.Contains(out, "HUMAN12_PLAIN=kept") {
		t.Fatalf("the child did not print the built environment:\n%s", out)
	}
	for _, leaked := range []string{"never-leaks", "HUMAN12_DOTENV_ENTRY", "ACME_DOTENV_TOKEN", "HUMAN12_STORED_PAT"} {
		if strings.Contains(strings.ToUpper(out), strings.ToUpper(leaked)) {
			t.Errorf("the child's environment holds %s", leaked)
		}
	}
}
