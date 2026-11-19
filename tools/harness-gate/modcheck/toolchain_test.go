package modcheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
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

	out, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("dsh: %v\n%s", err, out)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 || fields[0] != "dsh" || fields[1] == "" {
		t.Errorf("dsh printed %q, want \"dsh <version>\"", out)
	}

	versioned := filepath.Join(t.TempDir(), filepath.Base(binary))
	goTool(t, harness, "off", "build", "-ldflags", "-X main.version=v1.2.3", "-o", versioned, "./cmd/dsh")
	out, err = exec.Command(versioned).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "dsh v1.2.3" {
		t.Errorf("dsh built with -X main.version=v1.2.3 printed %q (err %v)", out, err)
	}
}
