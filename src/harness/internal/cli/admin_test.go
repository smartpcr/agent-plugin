package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/logging"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret/file"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/local"
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

// The components of the admin composition's active entries: all of them for a
// writing command, and without the entries that write into the home for a
// read-only one.
const (
	writeTrace = "boot.logging,host.sharing,session.store,human.secrets,sandbox.local"
	readTrace  = "boot.logging,host.sharing,sandbox.local"
)

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
	if r.stderr != tracePrefix+writeTrace+"\n" {
		t.Errorf("trace = %q, want the admin composition %s", r.stderr, writeTrace)
	}
	if !stored(t, root, ref) {
		t.Fatal("the store does not hold secret://test/k")
	}

	other := t.TempDir()
	for _, args := range [][]string{
		{"--home", other, "secret", "set", "TEST_K", "sk-in-argv"},
		{"--home", other, "secret", "set", "sk-in-argv"},
		{"--home", other, "secret", "set", "secret://test/k", "--json", "sk-in-argv"},
		{"--home", other, "secret", "set", "TEST_K", "--json=sk-in-argv"},
		{"--home", other, "secret", "set", "TEST_K", "--sk-in-argv"},
		{"--home", other, "secret", "set", "TEST_K", "-sk-in-argv"},
		{"--home", other, "secret", "set", "TEST_K", "---sk-in-argv"},
		{"--home", other, "secret", "set", "TEST_K", "--json", "--sk-in-argv"},
		{"--home", other, "secret", "set", "--json", "TEST_K", "--sk-in-argv=v"},
	} {
		r := adminRun(t, "", args...)
		if r.status != output.Usage || strings.Contains(r.stdout+r.stderr, "sk-in-argv") {
			t.Errorf("dsh %v: status %d, want %d without the value on a stream: %+v", args, r.status, output.Usage, r)
		}
		if slices.Contains(args, "--json") && !strings.Contains(r.stdout+r.stderr, `"cli/usage"`) {
			t.Errorf("dsh %v: no JSON usage error: %+v", args, r)
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
		if profile.Acting(part, part) || strings.HasPrefix(part, "llm.") {
			t.Errorf("sandbox status built %s", part)
		}
	}
	if trace != readTrace {
		t.Errorf("trace = %q, want the read-only view %s", trace, readTrace)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("the read-only view created the home: %v", err)
	}

	r = dshProcess(t, bin, "v\n", "--home", root, "secret", "set", "TEST_K")
	if r.status != output.OK || traceLine(t, r.stderr) != writeTrace {
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

// The read-only view writes nothing: `dsh sandbox status` on a home that exists
// leaves every file of it as it was and opens no session database or owner
// record, while the same tree for a writing command loads the entries that do.
// A user layer that logs to a file at a path that does not exist yet, with the
// destination written literally or as an expression, and puts the sandbox
// canaries in the home, changes nothing of that.
//
//dsh:test DH-T-CLI-16
func TestReadOnlyViewWritesNothing(t *testing.T) {
	for name, destination := range map[string]string{
		"literal destination":    "file",
		"expression destination": `!expr '"fi" + "le"'`,
		"template destination":   `'${{ env.DSH_TEST_UNSET_DESTINATION ?? "file" }}'`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if r := adminRun(t, "v\n", "--home", root, "secret", "set", "TEST_K"); r.status != output.OK {
				t.Fatalf("secret set: %+v", r)
			}
			logPath, canaries := filepath.Join(root, "logs", "deep", "dsh.log"), filepath.Join(root, "canaries")
			if err := os.Mkdir(canaries, 0o700); err != nil {
				t.Fatal(err)
			}
			temphome.UserLayer(t, root, "- id: logging\n  merge:\n    destination: "+destination+"\n    path: "+strconv.Quote(logPath)+
				"\n- id: sandbox\n  merge:\n    canaryDir: "+strconv.Quote(canaries)+"\n")
			before := homeFiles(t, root)
			r := adminRun(t, "", "--home", root, "sandbox", "status", "--json")
			if r.status != output.OK || !strings.Contains(r.stdout, `"state":`) {
				t.Fatalf("sandbox status: %+v", r)
			}
			if got := traceLine(t, r.stderr); got != readTrace {
				t.Errorf("trace = %q, want %s", got, readTrace)
			}
			if after := homeFiles(t, root); !maps.Equal(before, after) {
				t.Errorf("the read-only view changed the home:\nbefore %v\nafter  %v", before, after)
			}
			// The layer is in force: the writing tree logs to the file it names.
			if r := adminRun(t, "v\n", "--home", root, "secret", "set", "TEST_K"); r.status != output.OK {
				t.Fatalf("secret set under the layer: %+v", r)
			}
			if _, err := os.Stat(logPath); err != nil {
				t.Errorf("the writing tree did not open the configured log: %v", err)
			}
		})
	}
}

// readView keeps configured writes out of the read-only tree: a file log
// becomes standard error, a configured canary directory a fresh temporary one,
// a nested entry included, while the settings that name no path stay.
//
//dsh:test DH-T-CLI-16
func TestReadViewRedirectsConfiguredWrites(t *testing.T) {
	composed := []loader.EntryOptions{
		{ID: "logging", Name: logging.Name, Config: json.RawMessage(`{"destination":"FILE","path":"/h/logs/dsh.log","level":"debug"}`)},
		{ID: "group", Name: "x.group", Children: []loader.EntryOptions{
			{ID: "sandbox", Name: local.Name, Config: json.RawMessage(`{"canaryDir":"/h/canaries","probeTimeout":5}`)},
			{ID: "secrets", Name: file.Name},
		}},
	}
	if err := readView(composed, "/h"); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"logging": `{"destination":"stderr","level":"debug"}`,
		"sandbox": `{"home":"/h","probeTimeout":5}`,
	}
	if got := string(composed[0].Config); got != want["logging"] {
		t.Errorf("logging config = %s, want %s", got, want["logging"])
	}
	if got := string(composed[1].Children[0].Config); got != want["sandbox"] {
		t.Errorf("sandbox config = %s, want %s", got, want["sandbox"])
	}
	if !composed[1].Children[1].Disabled {
		t.Error("the nested human.secrets entry is not disabled")
	}

	// An expression, in a field or as the whole config, cannot bring a path or a
	// file destination back.
	for raw, want := range map[string]string{
		`{"destination":{"$expr":"env.D"},"path":"${{ env.P }}","format":"json"}`: `{"destination":"stderr","format":"json"}`,
		`{"$expr":"env.LOGGING"}`: `{"destination":"stderr"}`,
	} {
		e := []loader.EntryOptions{{ID: "logging", Name: logging.Name, Config: json.RawMessage(raw)}}
		if err := readView(e, "/h"); err != nil || string(e[0].Config) != want {
			t.Errorf("readView(%s) = %s, %v; want %s", raw, e[0].Config, err, want)
		}
	}
	e := []loader.EntryOptions{{ID: "sandbox", Name: local.Name, Config: json.RawMessage(`{"$expr":"env.SANDBOX"}`)}}
	if err := readView(e, "/h"); err != nil || string(e[0].Config) != `{"home":"/h"}` {
		t.Errorf("readView of a whole sandbox expression = %s, %v", e[0].Config, err)
	}
}

// homeFiles maps each file under root to its size and modification time, and
// each directory to "dir" (a directory's time is not kept promptly everywhere).
func homeFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			files[path] = "dir"
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files[path] = info.ModTime().String() + " " + strconv.FormatInt(info.Size(), 10)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
