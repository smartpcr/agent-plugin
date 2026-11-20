package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestRehearseBuildsSixPureGoBinaries runs the rehearse subcommand on the repository
// (TC-01b, DH-D-TOOLCHAIN-5): six binaries land in the --out directory, each built
// with CGO_ENABLED=0 and -trimpath for its target, the printed SHA-256 matches the
// file, and nothing is published -- the module proxy is off (no network fetch can
// succeed) and the repository's tags are unchanged.
func TestRehearseBuildsSixPureGoBinaries(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// Fill the module cache first, so the rehearsal itself can run with the proxy off.
	download := exec.Command("go", "mod", "download")
	download.Dir = filepath.Join(root, "src", "harness")
	download.Env = setEnv(os.Environ(), "GOWORK=off")
	if out, err := download.CombinedOutput(); err != nil {
		t.Fatalf("go mod download: %v\n%s", err, out)
	}
	tags := func() string {
		out, err := exec.Command("git", "-C", root, "for-each-ref", "refs/tags").CombinedOutput()
		if err != nil {
			t.Fatalf("git for-each-ref: %v\n%s", err, out)
		}
		return string(out)
	}
	before := tags()

	t.Setenv("GOPROXY", "off")
	t.Setenv("CGO_ENABLED", "1") // the rehearsal must override the parent's cgo setting
	t.Setenv("GOFLAGS", "-race") // and ignore the parent's flags
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"rehearse", "--root", root, "--out", dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("rehearse exited %d:\n%s%s", code, stdout.String(), stderr.String())
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{"dsh-darwin-amd64", "dsh-darwin-arm64", "dsh-linux-amd64", "dsh-linux-arm64", "dsh-windows-amd64.exe", "dsh-windows-arm64.exe"}
	if !slices.Equal(names, want) {
		t.Fatalf("binaries = %v, want %v", names, want)
	}
	for _, tgt := range Targets {
		name := "dsh-" + tgt.GOOS + "-" + tgt.GOARCH
		if tgt.GOOS == "windows" {
			name += ".exe"
		}
		a, err := inspect(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range map[string]string{"CGO_ENABLED": "0", "-trimpath": "true", "GOOS": tgt.GOOS, "GOARCH": tgt.GOARCH} {
			if a.Settings[k] != v {
				t.Errorf("%s: build setting %s = %q, want %q", name, k, a.Settings[k], v)
			}
		}
		if _, race := a.Settings["-race"]; race {
			t.Errorf("%s: built with the parent's GOFLAGS -race", name)
		}
		line := regexp.MustCompile(`(?m)^` + a.SHA256 + `  ` + regexp.QuoteMeta(name) + `  version=\S+ vcs\.revision=\S+ vcs\.modified=\S+$`)
		if !line.MatchString(stdout.String()) {
			t.Errorf("output lacks the SHA-256 and version line of %s:\n%s", name, stdout.String())
		}
	}
	if !strings.Contains(stdout.String(), "rehearse: 6 binaries, nothing published") {
		t.Errorf("output:\n%s", stdout.String())
	}
	if after := tags(); after != before {
		t.Errorf("tags changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestRehearseNamesFailingTarget(t *testing.T) {
	cfg := newTestConfig(t, scratch(t, "cross-broken"))
	var out bytes.Buffer
	arts, err := cfg.Rehearse(context.Background(), &out, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "windows/arm64:") {
		t.Fatalf("Rehearse = %v, want windows/arm64 named\n%s", err, out.String())
	}
	if len(arts) != 5 {
		t.Errorf("rehearsed %d binaries, want the 5 that build", len(arts))
	}
	if strings.Contains(out.String(), "binaries, nothing published") {
		t.Errorf("a failed rehearsal reported success:\n%s", out.String())
	}
}

func TestRehearseWithoutDshModuleFails(t *testing.T) {
	cfg := newTestConfig(t, scratch(t, "race-seeded"))
	if _, err := cfg.Rehearse(context.Background(), &bytes.Buffer{}, t.TempDir()); err == nil || !strings.Contains(err.Error(), "holds cmd/dsh") {
		t.Errorf("Rehearse = %v, want a failure naming cmd/dsh", err)
	}
}
