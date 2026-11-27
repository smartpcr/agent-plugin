//go:build replay

package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/build/constraint"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/dshbin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/fixture"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// RefreshEnv set to 1 rewrites the recordings and the log and request oracles.
const RefreshEnv = "DSH_REPLAY_REFRESH"

// envProbeEnv set to 1 makes the test binary print its environment as JSON and
// exit, so a test can see what a child started like a replay inherits.
const envProbeEnv = "DSH_REPLAY_ENV_PROBE"

func TestMain(m *testing.M) {
	if os.Getenv(envProbeEnv) == "1" {
		if err := json.NewEncoder(os.Stdout).Encode(os.Environ()); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	dshbin.Main(m)
}

// providerCredentials are the model and forge credential variables a replay must
// run without, blanked even when the host does not set them.
var providerCredentials = []string{
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
	"OPENAI_API_KEY", "GH_TOKEN", "GITHUB_TOKEN", "COPILOT_GITHUB_TOKEN",
}

// keylessEnv returns the Program.Env entries that blank, in a child launched
// with host, every provider credential and every host variable whose name is
// credential-shaped (subprocess.DefaultDeny). launcher.Spawn appends them after
// host and a later entry wins, so the replay runs without a key whatever the
// developer or CI runner has set.
func keylessEnv(host []string) []string {
	kept := subprocess.BuildEnv(host, subprocess.EnvOptions{})
	out := make([]string, 0, len(providerCredentials))
	for _, name := range providerCredentials {
		out = append(out, name+"=")
	}
	for _, kv := range host {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" || strings.HasPrefix(strings.ToUpper(name), subprocess.HarnessPrefix) || kept.Has(name) {
			continue
		}
		out = append(out, name+"=")
	}
	return out
}

// scenarios lists the scenario directories under testdata.
func scenarios(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join("testdata", e.Name()))
		}
	}
	if len(dirs) == 0 {
		t.Fatal("testdata holds no scenario")
	}
	return dirs
}

// outcome is one replay's normalised log and requests and its workspace.
type outcome struct {
	log       []byte
	requests  map[string][]byte
	workspace string
}

// replayWithin bounds one replay run of the binary, its whole tree included.
const replayWithin = 2 * time.Minute

// maxReplayOutput caps each of the standard output and error a replay keeps.
const maxReplayOutput = 4 << 20

// launchReplay runs p in a process tree of its own (launcher.Run) and returns its
// standard output. A tree that outlives within -- the binary hangs, or a
// descendant holds its output -- is force-stopped whole; that, a non-zero exit
// and output past p.MaxOutput are errors carrying what the run wrote.
func launchReplay(t testing.TB, within time.Duration, p launcher.Program) (string, error) {
	t.Helper()
	h, err := launcher.Run(t, within, p)
	out, errOut := h.Stdout.String(), h.Stderr.String()
	switch {
	case err != nil:
		return out, fmt.Errorf("%w within %s\nstdout:\n%s\nstderr:\n%s", err, within, out, errOut)
	case h.Process.ExitCode() != 0:
		return out, fmt.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", h.Process.ExitCode(), out, errOut)
	case h.Stdout.Dropped() > 0 || h.Stderr.Dropped() > 0:
		return out, fmt.Errorf("the run wrote past its output cap of %d bytes (%d and %d bytes dropped)", p.MaxOutput, h.Stdout.Dropped(), h.Stderr.Dropped())
	}
	return out, nil
}

// play replays the scenario at dir with the built bin into a scratch home.
func play(t *testing.T, bin, dir string, refresh bool) outcome {
	t.Helper()
	m, err := fixture.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	root := temphome.New(t).Root()
	ws, reqs := t.TempDir(), t.TempDir()
	if err := fixture.CopyTree(filepath.Join(dir, "workspace"), ws); err != nil {
		t.Fatal(err)
	}
	mode := "replay"
	if refresh {
		mode = "refresh"
	}
	temphome.Layer(t, root, profile.Headless, "- insert:\n    - id: replay\n      name: llm.replay\n      config:\n"+
		"        scenario: "+strconv.Quote(abs)+"\n        mode: "+mode+"\n        route: "+strconv.Quote(m.Route)+"\n"+
		"        model: "+strconv.Quote(m.Model)+"\n        requests: "+strconv.Quote(reqs)+"\n")

	ctx, cancel := context.WithTimeout(context.Background(), replayWithin)
	defer cancel()
	out, err := launchReplay(t, replayWithin, launcher.Program{Argv: []string{bin, "--home", root, "headless", "--json", m.Input}, Dir: ws, Env: keylessEnv(os.Environ()), MaxOutput: maxReplayOutput})
	if err != nil {
		t.Fatalf("%s: %v", dir, err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var res struct {
		Type, Session, Reason string
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil || res.Type != "run/result" || res.Session == "" {
		t.Fatalf("%s: the last stdout line is not a run/result (%v):\n%s", dir, err, out)
	}

	st, err := sqlite.Open(ctx, home.New(root).DB())
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	err = st.Export(ctx, &log, res.Session, sqlite.ExportOptions{})
	if cerr := st.Close(ctx); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}

	n := &fixture.Normalizer{Paths: map[string]string{root: "home", ws: "workspace", abs: "scenario", reqs: "requests"}}
	o := outcome{log: n.Normalize(log.Bytes()), requests: map[string][]byte{}, workspace: ws}
	files, err := fixture.Tree(reqs)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range files {
		o.requests[name] = n.Normalize(b)
	}
	return o
}

// TC-16g, TC-16b: each committed recording, replayed by the built binary
// without a key into a scratch home, persists the expected normalised log,
// leaves the expected workspace and sends the expected model requests, byte for
// byte.
func TestReplayMatchesTheCommittedOracle(t *testing.T) {
	bin := dshbin.Path(t, "testhook")
	refresh := os.Getenv(RefreshEnv) == "1"
	for _, dir := range scenarios(t) {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			o := play(t, bin, dir, refresh)
			if refresh {
				writeOracle(t, dir, o)
				return
			}
			want, err := os.ReadFile(filepath.Join(dir, "log.expected.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if d := fixture.Diff("log.expected.jsonl", o.log, want); d != "" {
				t.Error(d)
			}
			wantReqs, err := fixture.Tree(filepath.Join(dir, "requests.expected"))
			if err != nil {
				t.Fatal(err)
			}
			if !sameKeys(o.requests, wantReqs) {
				t.Errorf("requests: got %v, want %v", keys(o.requests), keys(wantReqs))
			}
			for name, b := range wantReqs {
				if d := fixture.Diff("requests.expected/"+name, o.requests[name], b); d != "" {
					t.Error(d)
				}
			}
			if err := fixture.CompareTrees(o.workspace, filepath.Join(dir, "workspace.expected")); err != nil {
				t.Errorf("workspace:\n%v", err)
			}
		})
	}
}

// DH-T-TOOLCHAIN-1 (keyless): a credential planted in the test's environment
// does not reach a child launched the way play launches the replay binary.
func TestReplayChildGetsNoKey(t *testing.T) {
	const planted = "planted-replay-credential"
	names := append(slices.Clone(providerCredentials), "Some_Vendor_Api_Key")
	for _, name := range names {
		t.Setenv(name, planted)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out, err := launchReplay(t, replayWithin, launcher.Program{
		Argv: []string{self}, Dir: t.TempDir(), MaxOutput: maxReplayOutput,
		Env: append(keylessEnv(os.Environ()), envProbeEnv+"=1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var env subprocess.Env
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &env); err != nil {
		t.Fatalf("the probe's output is not its environment (%v):\n%s", err, out)
	}
	for _, name := range names {
		if v := env.Get(name); v != "" {
			t.Errorf("%s reached the child as %q", name, v)
		}
	}
	for _, kv := range env {
		if strings.Contains(kv, planted) {
			t.Errorf("the planted credential reached the child: %s", kv)
		}
	}
}

// writeOracle rewrites the recording and the log and request oracles of dir.
func writeOracle(t *testing.T, dir string, o outcome) {
	t.Helper()
	for _, name := range []string{"session.jsonl", "log.expected.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), o.log, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reqs := filepath.Join(dir, "requests.expected")
	if err := os.RemoveAll(reqs); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(reqs, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, b := range o.requests {
		if err := os.WriteFile(filepath.Join(reqs, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("%s: refreshed the recording and the log and request oracles; review the diff", dir)
}

// DH-T-TOOLCHAIN-5: two replays of the same recording are byte-identical, and
// replaying leaves every committed fixture file as it was and a fixed point of
// the normalisation table.
//
//dsh:test DH-T-TOOLCHAIN-5
func TestReplayIsDeterministicAndLeavesTheFixture(t *testing.T) {
	if os.Getenv(RefreshEnv) == "1" {
		t.Skip("refreshing rewrites the fixture")
	}
	bin := dshbin.Path(t, "testhook")
	for _, dir := range scenarios(t) {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			before, err := fixture.Tree(dir)
			if err != nil {
				t.Fatal(err)
			}
			for name, b := range before {
				if err := fixture.Guard(b); err != nil {
					t.Errorf("%s: %v", name, err)
				}
			}
			a, b := play(t, bin, dir, false), play(t, bin, dir, false)
			if d := fixture.Diff("log", a.log, b.log); d != "" {
				t.Error("two replays differ: " + d)
			}
			for name := range a.requests {
				if d := fixture.Diff(name, a.requests[name], b.requests[name]); d != "" {
					t.Error("two replays differ: " + d)
				}
			}
			if err := fixture.CompareTrees(a.workspace, b.workspace); err != nil {
				t.Errorf("two replays left different workspaces:\n%v", err)
			}
			after, err := fixture.Tree(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !sameKeys(before, after) {
				t.Errorf("the fixture's files changed: %v, then %v", keys(before), keys(after))
			}
			for name, b := range before {
				if !bytes.Equal(b, after[name]) {
					t.Errorf("replaying changed %s", name)
				}
			}
		})
	}
}

// DH-T-TOOLCHAIN-1: every Go file of the replay layer, and every scenario file,
// carries its layer's tag, so the default build and test never compile it; the
// replay starts only the built binary (dshbin), and the scenario files are held
// to the test kit by the scenarioimport analyzer.
//
//dsh:test DH-T-TOOLCHAIN-1
func TestLayerFilesCarryTheirTag(t *testing.T) {
	for tag, glob := range map[string]string{
		"replay":   "*.go",
		"scenario": filepath.Join("..", "..", "internal", "testkit", "scenarios", "*_scenario_test.go"),
	} {
		files, err := filepath.Glob(glob)
		if err != nil || len(files) == 0 {
			t.Fatalf("%s: no files (%v)", glob, err)
		}
		for _, f := range files {
			if !needsTag(t, f, tag) {
				t.Errorf("%s does not build only with the %s tag", f, tag)
			}
		}
	}
}

// needsTag reports whether the file's //go:build line holds with tag and fails
// without it, whatever else is set.
func needsTag(t *testing.T, file, tag string) bool {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(b), "\n")
	expr, err := constraint.Parse(strings.TrimSpace(first))
	if err != nil {
		return false
	}
	return expr.Eval(func(string) bool { return true }) && !expr.Eval(func(s string) bool { return s != tag })
}

func sameKeys(a, b map[string][]byte) bool { return slices.Equal(keys(a), keys(b)) }

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
