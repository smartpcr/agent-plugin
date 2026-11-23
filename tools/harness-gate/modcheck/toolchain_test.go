package modcheck

import (
	"go/version"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// goTool runs the go command from dir with the workspace selected by gowork ("" for
// the root go.work, "off" to ignore it). Nothing may be downloaded: GOPROXY is off.
func goTool(t *testing.T, dir, gowork string, args ...string) string {
	t.Helper()
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go toolchain is required: %v", err)
	}
	cmd := exec.Command(gobin, args...)
	cmd.Dir = dir
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		key, _, _ := strings.Cut(kv, "=")
		key = strings.ToUpper(key)
		return key == "GOWORK" || key == "GOFLAGS" || key == "GOPROXY"
	})
	if gowork != "" {
		env = append(env, "GOWORK="+gowork)
	}
	cmd.Env = append(env, "GOPROXY=off", "GOFLAGS=-mod=readonly")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s (in %s, GOWORK=%q): %v\n%s", strings.Join(args, " "), dir, gowork, err, out)
	}
	return string(out)
}

func TestWorkspaceBuildsThreeModules(t *testing.T) {
	patterns := []string{"./src/go/...", "./src/sdk/...", "./src/harness/..."}
	goTool(t, repoRoot, "", append([]string{"build"}, patterns...)...)
	goTool(t, repoRoot, "", append([]string{"vet"}, patterns...)...)

	got := strings.Fields(goTool(t, repoRoot, "", "list", "-m"))
	var want []string
	for _, m := range Modules {
		want = append(want, m.Path)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("go list -m = %v, want %v", got, want)
	}

	for _, m := range Modules {
		goTool(t, filepath.Join(repoRoot, filepath.FromSlash(m.Dir)), "off", "build", "./...")
	}
}

func TestReleaseBuildIgnoresWorkspace(t *testing.T) {
	harness := filepath.Join(repoRoot, "src", "harness")
	if err := CheckToolchain11(committedRepo(t)); err != nil {
		t.Fatalf("release build needs a replace-free src/harness/go.mod: %v", err)
	}
	binary := filepath.Join(t.TempDir(), "dsh")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	goTool(t, harness, "off", "build", "-o", binary, "./cmd/dsh")

	out, err := exec.Command(binary, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("dsh --version: %v\n%s", err, out)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 || fields[0] != "dsh" || fields[1] == "" {
		t.Errorf("dsh --version printed %q, want \"dsh <version> (...)\"", out)
	}

	versioned := filepath.Join(t.TempDir(), filepath.Base(binary))
	goTool(t, harness, "off", "build", "-ldflags", "-X main.version=v1.2.3", "-o", versioned, "./cmd/dsh")
	out, err = exec.Command(versioned, "--version").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "dsh v1.2.3 (") {
		t.Errorf("dsh built with -X main.version=v1.2.3 printed %q (err %v)", out, err)
	}
}

// git runs a read-only git command in the checkout.
func git(t *testing.T, args ...string) (string, error) {
	t.Helper()
	gitbin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is required to resolve the module pins offline: %v", err)
	}
	cmd := exec.Command(gitbin, args...)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	return string(out), err
}

// TestPinsResolveToCommittedModules proves, without a network, that every pin of
// src/harness/go.mod is usable by a GOWORK=off build even though cmd/dsh imports
// neither module yet: the pinned revision is in this repository's history, its
// committer time is the pseudo-version's timestamp, its go.mod declares the required
// module path at or below the go floor, and that go.mod's hash is the one go.sum records.
func TestPinsResolveToCommittedModules(t *testing.T) {
	repo := committedRepo(t)
	pins, err := Pins(repo.Mods["src/harness"])
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 2 {
		t.Fatalf("src/harness pins %d product modules, want 2", len(pins))
	}
	sums := ParseSums(repo.HarnessSum)
	for _, pin := range pins {
		pseudo, err := ParsePseudo(pin.Version)
		if err != nil {
			t.Errorf("%s: %v", pin.Directive, err)
			continue
		}
		commit, err := git(t, "rev-parse", "--verify", "--quiet", pseudo.Rev+"^{commit}")
		if err != nil {
			t.Errorf("%s: revision %s is not in this checkout's history (fetch it, or re-pin with GOWORK=off go get <module>@<commit>)", pin.Directive, pseudo.Rev)
			continue
		}
		commit = strings.TrimSpace(commit)
		unix, err := git(t, "show", "-s", "--format=%ct", commit)
		if err != nil {
			t.Fatal(err)
		}
		seconds, err := strconv.ParseInt(strings.TrimSpace(unix), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if at := time.Unix(seconds, 0).UTC(); !at.Equal(pseudo.Time) {
			t.Errorf("%s: revision %s was committed at %s, the pseudo-version says %s", pin.Directive, pseudo.Rev, at, pseudo.Time)
		}
		mod, err := git(t, "show", commit+":"+pin.Module.Dir+"/go.mod")
		if err != nil {
			t.Errorf("%s: revision %s has no %s/go.mod", pin.Directive, pseudo.Rev, pin.Module.Dir)
			continue
		}
		file, err := Parse(pin.Module.Dir+"/go.mod@"+pseudo.Rev, []byte(mod))
		if err != nil {
			t.Fatal(err)
		}
		if m := file.All("module"); len(m) != 1 || m[0].Args != pin.Module.Path {
			t.Errorf("%s: %s/go.mod at %s declares %v", pin.Directive, pin.Module.Dir, pseudo.Rev, m)
		}
		// At or below the floor, not equal to it: a revision pinned before the floor
		// was raised (go-floor-1-27-4) keeps its older go line, and the build still
		// runs on the floor because src/harness states it. A pin above the floor
		// would raise the harness's go line behind RM-2's back.
		if g := file.All("go"); len(g) != 1 || version.Compare("go"+g[0].Args, "go"+Floor) > 0 {
			t.Errorf("%s: %s/go.mod at %s is above the go %s floor: %v", pin.Directive, pin.Module.Dir, pseudo.Rev, Floor, g)
		}
		key := pin.Module.Path + " " + pin.Version + "/go.mod"
		if got := GoModHash([]byte(mod)); sums[key] != got {
			t.Errorf("%s: go.sum records %q for %s, the pinned go.mod hashes to %s", pin.Directive, sums[key], key, got)
		}
	}
}
