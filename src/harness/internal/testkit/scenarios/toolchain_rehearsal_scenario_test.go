//go:build scenario

package scenarios

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// rehearsalTargets are the six binaries the release rehearsal builds (QG-8).
var rehearsalTargets = []string{
	"dsh-darwin-amd64", "dsh-darwin-arm64",
	"dsh-linux-amd64", "dsh-linux-arm64",
	"dsh-windows-amd64.exe", "dsh-windows-arm64.exe",
}

// replayScenario is the keyless headless replay scenario every rehearsed binary
// answers, relative to the repository root. It is recorded with the replay runner
// of DH-S-16 (`dsh headless`); until it exists this scenario fails, it never skips.
const replayScenario = "src/harness/internal/testkit/scenarios/testdata/rehearsal-replay"

// rehearsalLine is one binary line of "harness-gate rehearse":
// "<sha256>  <name>  version=<v> vcs.revision=<rev> vcs.modified=<bool>".
var rehearsalLine = regexp.MustCompile(`^([0-9a-f]{64})  (dsh-\S+)  version=(\S+) vcs\.revision=(\S+) vcs\.modified=(\S+)$`)

// versionLine is what "dsh --version" prints: version, revision, Go, platform.
var versionLine = regexp.MustCompile(`^dsh (\S+) \((\S+); go\S+; (\S+/\S+)\)$`)

// TestToolchainRehearsal: a release rehearsal yields a runnable binary on every
// platform (upstream/toolchain.md DH-E-TOOLCHAIN-2; DH-D-TOOLCHAIN-5,
// DH-ADR-TOOLCHAIN-13, DH-ADR-15). Executed in DH-S-18 (step 18.1) on hosted Linux,
// macOS and Windows runners at a commit where the harness job passes. Its Given
// was aligned with the signed-in client routes of areas/llm.md DH-D-LLM-12 by
// gap-closure-plan 18.1.1 (M1-D-CLOSE-4 A): under OWNER-2 no API key exists and a
// real model is reached only through the user's own signed-in client, which a
// replay never starts, so the rehearsal needs neither.
//
// Given a clean checkout, no API key anywhere and every client route signed out
// (each client's configuration home an empty temporary directory, every
// credential variable emptied), when "harness-gate rehearse" builds cmd/dsh for
// the six targets (pure Go, -trimpath, nothing published), then six binaries
// exist, each matches the SHA-256 the rehearsal printed and embeds the checked-out
// commit with no "modified" marker; the binary of this runner's platform reports
// that version from "dsh --version" and passes one keyless, signed-out headless
// replay. Scope: the three binaries matching the runners' processors are executed;
// the other three are built and checksummed only. Cleanup: everything, the empty
// configuration homes included, is under t.TempDir(), except the binaries of a
// DH-S-18 run, which go to the directory DSH_REHEARSAL_DIR names (the runner owns
// and removes it) with a manifest of their SHA-256s once every check passed, so the
// later DH-S-18 scenarios run on them; nothing of the user's
// sign-in is touched; no tag or release is created (the tag list is compared
// before and after).
//
//dsh:scenario DH-E-TOOLCHAIN-2
func TestToolchainRehearsal(t *testing.T) {
	root := repositoryRoot(t)
	head := strings.TrimSpace(run(t, root, nil, "git", "rev-parse", "HEAD"))
	if status := strings.TrimSpace(run(t, root, nil, "git", "status", "--porcelain")); status != "" {
		t.Fatalf("the checkout is not clean; a rehearsal of a modified tree proves nothing:\n%s", status)
	}
	tagsBefore := run(t, root, nil, "git", "tag", "--list")

	// Under "harness-gate scenarios --stage DH-S-18" the binaries go to the
	// directory the run owns (rehearsalDirEnv), so every later DH-S-18 scenario
	// runs on them (impl.md 18.1); otherwise they die with the test.
	out := strings.TrimSpace(os.Getenv(rehearsalDirEnv))
	if out == "" {
		out = t.TempDir()
	} else if entries, err := os.ReadDir(out); err != nil || len(entries) != 0 {
		t.Fatalf("%s=%s must name an empty directory (%d entries, %v)", rehearsalDirEnv, out, len(entries), err)
	}
	report := run(t, filepath.Join(root, "tools", "harness-gate"), []string{"GOWORK=off"},
		"go", "run", ".", "rehearse", "--root", "../..", "--out", out)
	t.Logf("rehearse:\n%s", report)

	type binary struct{ sha, version, revision, modified string }
	got := map[string]binary{}
	sc := bufio.NewScanner(strings.NewReader(report))
	for sc.Scan() {
		if m := rehearsalLine.FindStringSubmatch(strings.TrimSpace(sc.Text())); m != nil {
			got[m[2]] = binary{sha: m[1], version: m[3], revision: m[4], modified: m[5]}
		}
	}
	names := make([]string, 0, len(got))
	for name := range got {
		names = append(names, name)
	}
	slices.Sort(names)
	if !slices.Equal(names, rehearsalTargets) {
		t.Fatalf("rehearsed binaries = %v, want %v", names, rehearsalTargets)
	}
	for _, name := range rehearsalTargets {
		b := got[name]
		data, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != b.sha {
			t.Errorf("%s: SHA-256 %x, rehearsal printed %s", name, sum, b.sha)
		}
		if b.revision != head {
			t.Errorf("%s: embedded vcs.revision %s, want the checked-out commit %s", name, b.revision, head)
		}
		if b.modified != "false" || strings.Contains(b.version, "+dirty") {
			t.Errorf("%s: carries a modified marker (vcs.modified=%s, version %s)", name, b.modified, b.version)
		}
	}

	native := "dsh-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		native += ".exe"
	}
	b, ok := got[native]
	if !ok {
		t.Fatalf("no rehearsed binary for this runner (%s/%s)", runtime.GOOS, runtime.GOARCH)
	}
	bin := filepath.Join(out, native)
	home := t.TempDir()
	// Keyless and signed out: every credential variable a model route could read
	// is emptied (noKeys, as in DH-E-TOOLCHAIN-1), and every client route's
	// configuration home is an empty directory, so the replay has no API key and
	// no sign-in in reach whatever the operator's shell holds. Under OWNER-2 there
	// is no API-key route at all; the replay answers from its recording.
	env := append(slices.Clone(noKeys), "DSH_HOME="+home)
	for _, r := range clientRoutes {
		env = append(env, r.signedOut(t))
	}
	// "dsh <version> (<revision>[-modified]; <go>; <os>/<arch>)" (cli.versionLine):
	// the version and revision must be the ones the rehearsal recorded, unmodified.
	v := strings.TrimSpace(run(t, home, env, bin, "--version"))
	if m := versionLine.FindStringSubmatch(v); m == nil {
		t.Errorf("%s --version = %q, want \"dsh <version> (<revision>; <go>; <os>/<arch>)\"", native, v)
	} else if m[1] != b.version || m[2] != b.revision || m[3] != runtime.GOOS+"/"+runtime.GOARCH {
		t.Errorf("%s --version = %q, want version %s, revision %s (no -modified) and %s/%s as recorded",
			native, v, b.version, b.revision, runtime.GOOS, runtime.GOARCH)
	}

	t.Run("keyless headless replay", func(t *testing.T) {
		scenario := filepath.Join(root, filepath.FromSlash(replayScenario))
		if _, err := os.Stat(scenario); err != nil {
			t.Fatalf("replay scenario %s: %v (recorded with the replay runner of DH-S-16)", replayScenario, err)
		}
		run(t, home, env, bin, "headless", "--replay", scenario)
	})

	if after := run(t, root, nil, "git", "tag", "--list"); after != tagsBefore {
		t.Errorf("the rehearsal changed the tags:\nbefore:\n%s\nafter:\n%s", tagsBefore, after)
	}

	// The hand-off: the manifest exists only for a rehearsal whose every check
	// passed, so the runner never hands a later scenario an unverified binary.
	if !t.Failed() {
		var manifest strings.Builder
		for _, name := range rehearsalTargets {
			manifest.WriteString(got[name].sha + "  " + name + "\n")
		}
		if err := os.WriteFile(filepath.Join(out, rehearsalManifest), []byte(manifest.String()), 0o644); err != nil {
			t.Fatalf("rehearsal manifest: %v", err)
		}
	}
}

// The rehearsal hand-off of "harness-gate scenarios" (tools/harness-gate
// scenariorun.go RehearsalDirEnv, RehearsalManifest).
const (
	rehearsalDirEnv   = "DSH_REHEARSAL_DIR"
	rehearsalManifest = "rehearsal.sha256"
)

// repositoryRoot is the checkout this package lives in.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "tools", "harness-gate", "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root (tools/harness-gate/go.mod) not found above the package")
		}
		dir = parent
	}
}

// run runs name with args in dir, the parent environment with env set, and returns
// its standard output; a failure fails the test with the combined output.
func run(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s%s", name, strings.Join(args, " "), err, out, stderr.String())
	}
	return string(out)
}
