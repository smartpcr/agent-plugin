package file

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Every value in these tests is an obvious fake.
const (
	fakeStored  = "sk-store-FAKE-0001"
	fakeRotated = "sk-store-FAKE-0002"
	fakeEnv     = "sk-env-FAKE-0001"
	fakeProject = "sk-project-FAKE-0001"
	fakeHome    = "sk-home-FAKE-0001"
)

var ref = secret.MustParse("secret://deepseek/api-key")

// tempSecrets makes a temporary home and returns its secrets directory.
func tempSecrets(t *testing.T) string {
	t.Helper()
	l := home.New(filepath.Join(t.TempDir(), "home"))
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	return l.Secrets()
}

func resolve(t *testing.T, s *secret.Store) (string, string, error) {
	t.Helper()
	v, from, err := s.Resolve(context.Background(), ref)
	return v.Reveal(), from, err
}

// DH-T-HUMAN-11: the four layers resolve in order (managed store, launch
// environment, project .env, home .env); a stored value wins, Unset reveals the
// fallback, an empty value is refused, and Describe tells source, revision and
// protection without the value.
//
//dsh:test DH-T-HUMAN-11
func TestPrecedenceOverTheFourLayers(t *testing.T) {
	ctx := context.Background()
	dir := tempSecrets(t)
	work := t.TempDir()
	projectEnv, homeEnv := filepath.Join(work, "project.env"), filepath.Join(work, "home.env")
	env := map[string]string{}
	store := secret.NewStore(
		New(dir, Options{}),
		secret.EnvSource(func(n string) (string, bool) { v, ok := env[n]; return v, ok }),
		secret.DotEnvSource(projectEnv, secret.OriginProjectEnv),
		secret.DotEnvSource(homeEnv, secret.OriginHomeEnv),
	)
	if _, _, err := resolve(t, store); !herr.Has(err, secret.ReasonNotConfigured) {
		t.Fatalf("nothing configured: %v", err)
	}
	write := func(path, text string) {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(homeEnv, "# home\nDEEPSEEK_API_KEY="+fakeHome+"\n")
	write(projectEnv, "export DEEPSEEK_API_KEY='"+fakeProject+"'\nOTHER=1\n")
	env["DEEPSEEK_API_KEY"] = fakeEnv
	for _, step := range []struct {
		do         func()
		want, from string
	}{
		{func() {}, fakeEnv, "env"},
		{func() { delete(env, "DEEPSEEK_API_KEY") }, fakeProject, secret.OriginProjectEnv},
		{func() { write(projectEnv, "OTHER=1\n") }, fakeHome, secret.OriginHomeEnv},
		{func() { env["DEEPSEEK_API_KEY"] = fakeEnv }, fakeEnv, "env"},
	} {
		step.do()
		if v, from, err := resolve(t, store); err != nil || v != step.want || from != step.from {
			t.Fatalf("resolved %q from %q (%v); want %q from %q", v, from, err, step.want, step.from)
		}
	}

	if _, err := store.Set(ctx, ref, secret.NewValue(""), secret.AnyRevision); !herr.Has(err, secret.ReasonEmpty) {
		t.Fatalf("empty value: %v", err)
	}
	if _, err := New(dir, Options{}).Set(ctx, ref, secret.NewValue(""), secret.AnyRevision); !herr.Has(err, secret.ReasonEmpty) {
		t.Fatalf("empty value straight to the file store: %v", err)
	}
	rev, err := store.Set(ctx, ref, secret.NewValue(fakeStored), secret.RevisionAbsent)
	if err != nil {
		t.Fatal(err)
	}
	if v, from, err := resolve(t, store); err != nil || v != fakeStored || from != Origin {
		t.Fatalf("after Set: %q from %q (%v); the stored value must win", v, from, err)
	}
	info, err := store.Describe(ctx, ref)
	if err != nil || !info.Configured || info.Source != Origin || info.Revision != rev || info.Protection != ProtectionFile ||
		info.Preview != "****0001" {
		t.Fatalf("Describe = %+v, %v", info, err)
	}
	if fmt.Sprintf("%+v", info) != strings.ReplaceAll(fmt.Sprintf("%+v", info), fakeStored, "") {
		t.Fatalf("Describe printed the value: %+v", info)
	}
	if err := store.Unset(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if v, from, err := resolve(t, store); err != nil || v != fakeEnv || from != "env" {
		t.Fatalf("after Unset: %q from %q (%v); want the environment's fallback", v, from, err)
	}
	if err := store.Unset(ctx, ref); err != nil {
		t.Fatalf("unsetting an absent reference: %v", err)
	}
	if info, _ := store.Describe(ctx, ref); info.Revision != secret.RevisionAbsent || info.Source != "env" {
		t.Fatalf("Describe after Unset = %+v", info)
	}

	// A store holds no writer without the file source; the env layers are read-only.
	if _, err := secret.NewStore(secret.EnvSource(nil)).Set(ctx, ref, secret.NewValue(fakeStored), ""); !herr.Has(err, secret.ReasonReadOnly) {
		t.Fatalf("Set without a writable source: %v", err)
	}
}

// DH-T-HUMAN-11: a stale revision is refused and changes nothing; a write with
// the current revision succeeds; RevisionAbsent creates only.
//
//dsh:test DH-T-HUMAN-11
func TestStaleRevisionIsRefused(t *testing.T) {
	ctx := context.Background()
	dir := tempSecrets(t)
	a, b := New(dir, Options{}), New(dir, Options{}) // two handles, as two processes
	r1, err := a.Set(ctx, ref, secret.NewValue(fakeStored), secret.RevisionAbsent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Set(ctx, ref, secret.NewValue(fakeRotated), secret.RevisionAbsent); !herr.Has(err, secret.ReasonStale) {
		t.Fatalf("create over an existing entry: %v", err)
	}
	r2, err := b.Set(ctx, ref, secret.NewValue(fakeRotated), r1)
	if err != nil || r2 == r1 {
		t.Fatalf("Set with the current revision: %q, %v", r2, err)
	}
	_, err = a.Set(ctx, ref, secret.NewValue(fakeStored), r1)
	if !herr.Has(err, secret.ReasonStale) || strings.Contains(err.Error(), "FAKE") {
		t.Fatalf("stale Set: %v", err)
	}
	if v, ok, err := a.Lookup(ctx, ref); err != nil || !ok || v.Reveal() != fakeRotated {
		t.Fatalf("after the refused write: %q %v %v", v.Reveal(), ok, err)
	}
	if st, err := a.Stat(ctx, ref); err != nil || st.Revision != r2 || !st.Present {
		t.Fatalf("Stat = %+v, %v", st, err)
	}
}

// DH-T-HUMAN-11: the file is sealed: no value and no reference appears in it, it
// is owner-only, and a lost key reads as absent with reason key-lost while the
// environment fallback still resolves; a write is then refused.
//
//dsh:test DH-T-HUMAN-11
func TestSealedFileAndLostKey(t *testing.T) {
	ctx := context.Background()
	dir := tempSecrets(t)
	s := New(dir, Options{})
	if _, err := s.Set(ctx, ref, secret.NewValue(fakeStored), ""); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, StoreFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "FAKE") || strings.Contains(string(raw), "deepseek") || !strings.Contains(string(raw), Format) {
		t.Fatalf("the store file is not sealed:\n%s", raw)
	}
	for _, name := range []string{StoreFileName, KeyFileName} {
		if err := home.CheckPrivate(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if refs, err := s.Stored(ctx); err != nil || len(refs) != 1 || refs[0] != ref {
		t.Fatalf("Stored = %v, %v", refs, err)
	}

	if err := os.Remove(filepath.Join(dir, KeyFileName)); err != nil {
		t.Fatal(err)
	}
	store := secret.NewStore(s, secret.EnvSource(func(string) (string, bool) { return fakeEnv, true }))
	if v, from, err := resolve(t, store); err != nil || v != fakeEnv || from != "env" {
		t.Fatalf("with the key lost: %q from %q (%v); want the fallback", v, from, err)
	}
	if info, err := store.Describe(ctx, ref); err != nil || info.Reason != "key-lost" || info.Source != "env" {
		t.Fatalf("Describe = %+v, %v", info, err)
	}
	if _, err := s.Set(ctx, ref, secret.NewValue(fakeRotated), ""); !herr.Has(err, secret.ReasonKeyLost) {
		t.Fatalf("Set with the key lost: %v", err)
	}
	// A different key does not open the file either.
	if err := os.WriteFile(filepath.Join(dir, KeyFileName), make([]byte, KeySize), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Lookup(ctx, ref); ok || err != nil {
		t.Fatalf("a wrong key opened the store: %v %v", ok, err)
	}
}

// DH-T-HUMAN-11: a busy lock is waited for a bounded time and then reported.
//
//dsh:test DH-T-HUMAN-11
func TestLockWaitIsBounded(t *testing.T) {
	dir := tempSecrets(t)
	held, err := fsx.TryLock(filepath.Join(dir, LockFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Unlock()
	start := time.Now()
	_, err = New(dir, Options{LockWait: 100 * time.Millisecond}).Set(context.Background(), ref, secret.NewValue(fakeStored), "")
	if !herr.Has(err, secret.ReasonBusy) || time.Since(start) > 5*time.Second {
		t.Fatalf("Set under a held lock: %v after %s", err, time.Since(start))
	}
}

// --- re-exec children (service:filesystem) ---

// roleEnv selects a child role of the re-executed test binary.
const roleEnv = "SECRETFILE_TEST_ROLE"

func TestMain(m *testing.M) {
	role, ok := os.LookupEnv(roleEnv)
	if !ok {
		os.Exit(m.Run())
	}
	os.Exit(child(role, os.Args[1:]))
}

// child runs one role. It prints revisions and reasons, never a value.
func child(role string, args []string) int {
	ctx := context.Background()
	switch role {
	case "set-many": // dir owner count
		count, _ := strconv.Atoi(args[2])
		s := New(args[0], Options{})
		for i := range count {
			r := secret.MustParse(fmt.Sprintf("secret://%s/key-%d", args[1], i))
			if _, err := s.Set(ctx, r, secret.NewValue(fmt.Sprintf("sk-%s-FAKE-%d", args[1], i)), secret.RevisionAbsent); err != nil {
				fmt.Println("error", herr.Kind(err))
				return 1
			}
		}
		fmt.Println("done")
	case "set-if": // dir ref value revision
		rev, err := New(args[0], Options{}).Set(ctx, secret.MustParse(args[1]), secret.NewValue(args[2]), args[3])
		if err != nil {
			fmt.Println("refused", herr.Kind(err))
			return 0
		}
		fmt.Println("set", rev)
	case "stall": // dir ref value: stop for good between the write and the rename
		afterStage = func() {
			fmt.Println("staged")
			select {}
		}
		_, err := New(args[0], Options{}).Set(ctx, secret.MustParse(args[1]), secret.NewValue(args[2]), "")
		fmt.Println("error", herr.Kind(err))
		return 1
	default:
		fmt.Println("unknown role")
		return 2
	}
	return 0
}

// reexec starts the test binary in role with args.
func reexec(t *testing.T, ctx context.Context, role string, args ...string) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = append(os.Environ(), roleEnv+"="+role)
	cmd.Stderr = os.Stderr
	return cmd
}

func output(t *testing.T, role string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := reexec(t, ctx, role, args...).Output()
	if err != nil {
		t.Fatalf("child %s: %v: %s", role, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Scenario "Two processes set one secret" (gap-closure 19.5): two re-exec
// processes on one temp home each Set their own references at the same time;
// every write survives. Then one child writes with the revision it read and a
// second child with the same, now stale, revision is refused.
//
//dsh:test DH-T-HUMAN-11
func TestTwoProcessesSetOneStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dir := tempSecrets(t)
	const count = 15
	var children []*exec.Cmd
	outs := make([]*strings.Builder, 2)
	for i, owner := range []string{"alpha", "beta"} {
		cmd := reexec(t, ctx, "set-many", dir, owner, strconv.Itoa(count))
		outs[i] = &strings.Builder{}
		cmd.Stdout = outs[i]
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill() }) // a no-op once Wait has reaped it
		children = append(children, cmd)
	}
	for i, cmd := range children {
		if err := cmd.Wait(); err != nil || strings.TrimSpace(outs[i].String()) != "done" {
			t.Fatalf("child %d: %v: %s", i, err, outs[i])
		}
	}
	s := New(dir, Options{})
	refs, err := s.Stored(ctx)
	if err != nil || len(refs) != 2*count {
		t.Fatalf("%d references survive (%v); want %d", len(refs), err, 2*count)
	}
	for _, owner := range []string{"alpha", "beta"} {
		for i := range count {
			r := secret.MustParse(fmt.Sprintf("secret://%s/key-%d", owner, i))
			if v, ok, err := s.Lookup(ctx, r); err != nil || !ok || v.Reveal() != fmt.Sprintf("sk-%s-FAKE-%d", owner, i) {
				t.Fatalf("%s lost its write: %v %v", r, ok, err)
			}
		}
	}

	shared := "secret://shared/api-key"
	read, err := s.Set(ctx, secret.MustParse(shared), secret.NewValue("sk-shared-FAKE-0"), secret.RevisionAbsent)
	if err != nil {
		t.Fatal(err)
	}
	if got := output(t, "set-if", dir, shared, "sk-shared-FAKE-1", read); !strings.HasPrefix(got, "set ") {
		t.Fatalf("the first writer with revision %s: %q", read, got)
	}
	if got := output(t, "set-if", dir, shared, "sk-shared-FAKE-2", read); got != "refused "+string(secret.ReasonStale) {
		t.Fatalf("the second writer with the stale revision %s: %q", read, got)
	}
	if v, _, _ := s.Lookup(ctx, secret.MustParse(shared)); v.Reveal() != "sk-shared-FAKE-1" {
		t.Fatal("the refused write changed the value")
	}
}

// DH-T-HUMAN-11: a writer killed between writing the new file and renaming it
// leaves the old file whole, and its lock dies with it.
//
//dsh:test DH-T-HUMAN-11
func TestKillBetweenWriteAndRenameKeepsTheOldFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dir := tempSecrets(t)
	s := New(dir, Options{LockWait: 20 * time.Second})
	if _, err := s.Set(ctx, ref, secret.NewValue(fakeStored), ""); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, StoreFileName))
	if err != nil {
		t.Fatal(err)
	}
	cmd := reexec(t, ctx, "stall", dir, ref.String(), fakeRotated)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() }) // a no-op once the test has killed and reaped it
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "staged" {
		_ = cmd.Process.Kill()
		t.Fatalf("child said %q: %v", line, err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("the stalled child exited cleanly")
	}

	after, err := os.ReadFile(filepath.Join(dir, StoreFileName))
	if err != nil || string(after) != string(before) {
		t.Fatalf("the store file changed under a killed writer (%v)", err)
	}
	if v, ok, err := s.Lookup(ctx, ref); err != nil || !ok || v.Reveal() != fakeStored {
		t.Fatalf("old value: %v %v", ok, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	staged := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), fsx.StagePrefix) {
			staged++
		}
	}
	t.Logf("staged leftovers after the kill: %d", staged)
	// The kernel released the dead writer's lock: the next write goes through.
	if _, err := s.Set(ctx, ref, secret.NewValue(fakeRotated), ""); err != nil {
		t.Fatalf("write after the kill: %v", err)
	}
	if v, _, _ := s.Lookup(ctx, ref); v.Reveal() != fakeRotated {
		t.Fatal("the write after the kill was lost")
	}
}
