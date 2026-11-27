package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret/file"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
)

// adminRun runs one command line in process with stdin, a working directory and
// the trace switch in the launch environment.
func adminRun(t *testing.T, stdin string, args ...string) run {
	t.Helper()
	var out, errOut bytes.Buffer
	env := Env{
		Request: launch.Request{Args: append([]string{"dsh"}, args...), Cwd: t.TempDir(), OS: runtime.GOOS,
			Environ: []string{TraceEnv + "=1"}},
		Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errOut,
	}
	status := Main(context.Background(), env)
	return run{status, out.String(), errOut.String()}
}

// stored reports whether the managed store of root holds ref.
func stored(t *testing.T, root string, ref secret.Ref) bool {
	t.Helper()
	st, err := file.New(home.New(root).Secrets(), file.Options{}).Stat(context.Background(), ref)
	if err != nil {
		t.Fatalf("stat %s: %v", ref, err)
	}
	return st.Present
}

// TC-13b, in process: the value of `dsh secret set` comes from standard input
// only; a value given as an argument, a one-word name and an empty input are
// refused, the refused value appears on no stream and nothing is stored.
//
//dsh:test DH-T-CLI-16
func TestSecretSetReadsStandardInputOnly(t *testing.T) {
	root := t.TempDir()
	ref := secret.MustParse("secret://test/k")
	r := adminRun(t, "v\n", "--home", root, "secret", "set", "TEST_K")
	if r.status != output.OK || r.stdout != "stored secret://test/k (revision 1)\n" {
		t.Fatalf("secret set: %+v", r)
	}
	if r.stderr != tracePrefix+"human.secrets\n" {
		t.Errorf("trace = %q, want the secret store only", r.stderr)
	}
	if !stored(t, root, ref) {
		t.Fatal("the store does not hold secret://test/k")
	}

	other := t.TempDir()
	for _, args := range [][]string{
		{"--home", other, "secret", "set", "TEST_K", "sk-in-argv"},
		{"--home", other, "secret", "set", "sk-in-argv"},
		{"--home", other, "secret", "set", "secret://test/k", "--json", "sk-in-argv"},
	} {
		r := adminRun(t, "", args...)
		if r.status != output.Usage || strings.Contains(r.stdout+r.stderr, "sk-in-argv") {
			t.Errorf("dsh %v: status %d, want %d without the value on a stream: %+v", args, r.status, output.Usage, r)
		}
	}
	if r := adminRun(t, "\n", "--home", other, "secret", "set", "TEST_K"); r.status == output.OK || stored(t, other, ref) {
		t.Errorf("an empty value was stored: %+v", r)
	}
}

// Reaching the store: the reference, its path and its environment name name one
// secret.
func TestSecretNames(t *testing.T) {
	for name, want := range map[string]string{
		"DEEPSEEK_API_KEY":          "secret://deepseek/api-key",
		"deepseek/api-key":          "secret://deepseek/api-key",
		"secret://deepseek/api-key": "secret://deepseek/api-key",
	} {
		ref, err := secretRef(name)
		if err != nil || ref.String() != want || ref.EnvName() != "DEEPSEEK_API_KEY" {
			t.Errorf("secretRef(%q) = %v, %v; want %s", name, ref, err, want)
		}
	}
	for _, bad := range []string{"k", "sk-abc", "secret://k", "A__B"} {
		if _, err := secretRef(bad); err == nil {
			t.Errorf("secretRef(%q) is accepted", bad)
		}
	}
}

// buildDsh builds cmd/dsh from this checkout (testkit/launcher.Build).
func buildDsh(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the dsh binary")
	}
	return launcher.Build(t, filepath.Join("..", ".."), "./cmd/dsh")
}

// dshProcess runs bin with args and stdin in its own process with the trace
// switch on, and returns its exit status and streams.
func dshProcess(t *testing.T, bin, stdin string, args ...string) run {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), TraceEnv+"=1")
	cmd.Dir = t.TempDir()
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatalf("%s %v: %v", bin, args, err)
	}
	return run{cmd.ProcessState.ExitCode(), out.String(), errOut.String()}
}

// TC-13b with the built binary: `dsh sandbox status` exits 0 with the C37 report,
// its component trace lists no agent, model client or carrier, and the read-only
// view creates nothing in the home; `dsh secret set k` takes the value from
// standard input, and a value in argv is a usage error.
//
//dsh:test DH-T-CLI-16
func TestAdminTreeBuildsNoAgent(t *testing.T) {
	bin := buildDsh(t)
	root := filepath.Join(t.TempDir(), "home")

	r := dshProcess(t, bin, "", "--home", root, "sandbox", "status")
	if r.status != output.OK || !strings.HasPrefix(r.stdout, "state:   ") || !strings.Contains(r.stdout, "\nbackend: ") {
		t.Fatalf("sandbox status: %+v", r)
	}
	if runtime.GOOS == "windows" && !strings.HasPrefix(r.stdout, "state:   Unconfined\nbackend: none\n") {
		t.Errorf("sandbox status on Windows is not Unconfined under none:\n%s", r.stdout)
	}
	trace := traceLine(t, r.stderr)
	for _, part := range strings.Split(trace, ",") {
		if strings.HasPrefix(part, "agent.") || strings.HasPrefix(part, "llm.") || strings.HasPrefix(part, "wire.") ||
			strings.HasPrefix(part, "cli.") || strings.HasPrefix(part, "plugin.") || strings.HasPrefix(part, "host.") {
			t.Errorf("sandbox status built %s", part)
		}
	}
	if trace != "sandbox.local" {
		t.Errorf("trace = %q, want sandbox.local only", trace)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("the read-only view created the home: %v", err)
	}

	r = dshProcess(t, bin, "v\n", "--home", root, "secret", "set", "TEST_K")
	if r.status != output.OK || traceLine(t, r.stderr) != "human.secrets" {
		t.Fatalf("secret set: %+v", r)
	}
	if !stored(t, root, secret.MustParse("secret://test/k")) {
		t.Error("the store does not hold secret://test/k")
	}
	r = dshProcess(t, bin, "", "--home", root, "secret", "set", "TEST_K", "v-in-argv")
	if r.status != output.Usage || strings.Contains(r.stdout+r.stderr, "v-in-argv") {
		t.Errorf("secret set with the value in argv: %+v", r)
	}
}

// traceLine is the component list of the trace line on stderr.
func traceLine(t *testing.T, stderr string) string {
	t.Helper()
	for _, line := range strings.Split(stderr, "\n") {
		if list, ok := strings.CutPrefix(strings.TrimSuffix(line, "\r"), tracePrefix); ok {
			return list
		}
	}
	t.Fatalf("no component trace on stderr:\n%s", stderr)
	return ""
}
