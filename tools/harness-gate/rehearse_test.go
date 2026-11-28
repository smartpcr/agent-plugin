package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestRehearseSnapshotSixArchives runs the rehearse subcommand on the repository
// (TC-01b, DH-D-TOOLCHAIN-5; plan 19.1.1, the Stage 19.1 scenario "The snapshot
// yields six archives and one checksum file"): the pinned goreleaser runs
// .goreleaser.dsh.yaml in snapshot mode with GOWORK=off; out/dist holds six archives
// and checksums.txt, every archive's SHA-256 matches its line, each holds LICENSE and
// a dsh built with CGO_ENABLED=0 and -trimpath for its target, extracted to out with
// the printed SHA-256; and nothing is published -- the module proxy is off, the
// repository's tags are unchanged and its own dist/ is not touched.
//
// This is the snapshot half of DH-T-TOOLCHAIN-14 (six archives, one checksum file,
// the licence notice). Its publish half -- the vulnerability check before a publish
// step, a second run against an existing tag, the three module tags on one commit --
// belongs to the published release, DH-D-TOOLCHAIN-6 (DH-S-46). The replay clause of
// the scenario is plan 19.1.2 (ReplayCheck) and is asserted open here.
//
//dsh:test DH-T-TOOLCHAIN-14
func TestRehearseSnapshotSixArchives(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// Fill the module cache first -- dsh's modules, and goreleaser built from the tools
	// module -- so the rehearsal itself can run with the proxy off.
	for _, step := range []struct {
		dir  string
		args []string
	}{
		{filepath.Join(root, "src", "harness"), []string{"mod", "download"}},
		{filepath.Join(root, filepath.FromSlash(GoReleaserModule)), []string{"tool", "-n", "goreleaser"}},
	} {
		cmd := exec.Command("go", step.args...)
		cmd.Dir = step.dir
		cmd.Env = setEnv(os.Environ(), "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(step.args, " "), err, out)
		}
	}
	tags := func() string {
		out, err := exec.Command("git", "-C", root, "for-each-ref", "refs/tags").CombinedOutput()
		if err != nil {
			t.Fatalf("git for-each-ref: %v\n%s", err, out)
		}
		return string(out)
	}
	repoDist := func() string {
		entries, err := os.ReadDir(filepath.Join(root, "dist"))
		if err != nil {
			return "absent"
		}
		var b strings.Builder
		for _, e := range entries {
			info, _ := e.Info()
			fmt.Fprintf(&b, "%s %d %v\n", e.Name(), info.Size(), info.ModTime())
		}
		return b.String()
	}
	tagsBefore, distBefore := tags(), repoDist()

	t.Setenv("GOPROXY", "off")
	t.Setenv("CGO_ENABLED", "1") // the rehearsal must override the parent's cgo setting
	t.Setenv("GOFLAGS", "-race") // and ignore the parent's flags
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"rehearse", "--root", root, "--out", dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("rehearse exited %d:\n%s%s", code, stdout.String(), stderr.String())
	}

	// Six archives and one checksum file, each archive verified.
	sums, err := readChecksums(filepath.Join(dir, "dist", ChecksumFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 6 {
		t.Fatalf("%s lists %d files, want 6 archives: %v", ChecksumFile, len(sums), sums)
	}
	for _, tgt := range Targets {
		var found bool
		for name, want := range sums {
			if !strings.HasSuffix(name, archiveSuffix(tgt.GOOS, tgt.GOARCH)) {
				continue
			}
			found = true
			data, err := os.ReadFile(filepath.Join(dir, "dist", name))
			if err != nil {
				t.Fatal(err)
			}
			if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != want {
				t.Errorf("%s: SHA-256 %x, %s records %s", name, sum, ChecksumFile, want)
			}
		}
		if !found {
			t.Errorf("no archive for %s/%s in %v", tgt.GOOS, tgt.GOARCH, sums)
		}
	}

	// The six binaries, extracted from the verified archives.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
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
		line := regexp.MustCompile(`(?m)^` + a.SHA256 + `  ` + regexp.QuoteMeta(name) + `  version=(\S+) vcs\.revision=\S+ vcs\.modified=\S+$`)
		m := line.FindStringSubmatch(stdout.String())
		if m == nil {
			t.Errorf("output lacks the SHA-256 and version line of %s:\n%s", name, stdout.String())
			continue
		}
		if tgt.GOOS == runtime.GOOS && tgt.GOARCH == runtime.GOARCH {
			// DH-E-TOOLCHAIN-2: the unpacked binary prints the version the rehearsal recorded.
			out, err := exec.Command(filepath.Join(dir, name), "--version").Output()
			if want := "dsh " + m[1] + " ("; err != nil || !strings.HasPrefix(string(out), want) {
				t.Errorf("%s --version = %q (err %v), want the recorded version, %q...", name, out, err, want)
			}
		}
	}
	for _, text := range []string{
		"goreleaser release --snapshot --clean -f " + GoReleaserConfig,
		"rehearse: 6 archives and " + ChecksumFile,
		"every archive verified against " + ChecksumFile,
		"OPEN, not built",
		"rehearse: 6 binaries, nothing published",
	} {
		if !strings.Contains(stdout.String(), text) {
			t.Errorf("output lacks %q:\n%s", text, stdout.String())
		}
	}
	if after := tags(); after != tagsBefore {
		t.Errorf("tags changed:\nbefore:\n%s\nafter:\n%s", tagsBefore, after)
	}
	if after := repoDist(); after != distBefore {
		t.Errorf("the repository's dist/ changed:\nbefore:\n%s\nafter:\n%s", distBefore, after)
	}
}

// fakeSnapshot writes a dist directory of six archives, each holding LICENSE and a
// fake dsh, and its checksum file; edit may change the files before the sums are
// written.
func fakeSnapshot(t *testing.T, edit func(files map[string]map[string]string)) string {
	t.Helper()
	dist := t.TempDir()
	archives := map[string]map[string]string{}
	for _, tgt := range Targets {
		exe := "dsh"
		if tgt.GOOS == "windows" {
			exe += ".exe"
		}
		archives["dsh_0.0.0-SNAPSHOT-abc"+archiveSuffix(tgt.GOOS, tgt.GOARCH)] = map[string]string{LicenseFile: "licence", exe: "not a binary"}
	}
	if edit != nil {
		edit(archives)
	}
	var sums strings.Builder
	for name, files := range archives {
		var buf bytes.Buffer
		if strings.HasSuffix(name, ".zip") {
			zw := zip.NewWriter(&buf)
			for n, body := range files {
				f, err := zw.Create(n)
				if err != nil {
					t.Fatal(err)
				}
				io.WriteString(f, body)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
		} else {
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			for n, body := range files {
				if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
					t.Fatal(err)
				}
				io.WriteString(tw, body)
			}
			if err := errors.Join(tw.Close(), gz.Close()); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dist, name), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(buf.Bytes())
		fmt.Fprintf(&sums, "%x  %s\n", sum, name)
	}
	if err := os.WriteFile(filepath.Join(dist, ChecksumFile), []byte(sums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dist
}

// TestVerifySnapshotNamesEachBadArchive: VerifySnapshot checks every target and
// names each failure's target and archive -- an archive whose SHA-256 is not its
// line, a target with no archive, an archive without the licence notice or without
// dsh, a checksum line that is not one archive per target -- and an unreadable
// checksum file fails it (plan 19.1.1).
func TestVerifySnapshotNamesEachBadArchive(t *testing.T) {
	t.Run("tampered archive", func(t *testing.T) {
		dist := fakeSnapshot(t, nil)
		name := "dsh_0.0.0-SNAPSHOT-abc" + archiveSuffix("linux", "arm64")
		f, err := os.OpenFile(filepath.Join(dist, name), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString("x")
		f.Close()
		_, err = VerifySnapshot(dist, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "linux/arm64: "+name+": SHA-256 ") {
			t.Errorf("VerifySnapshot = %v, want the linux/arm64 archive's SHA-256 named", err)
		}
	})
	for _, tc := range []struct {
		name string
		edit func(map[string]map[string]string)
		want string
	}{
		{"missing target", func(a map[string]map[string]string) {
			delete(a, "dsh_0.0.0-SNAPSHOT-abc"+archiveSuffix("windows", "arm64"))
		}, "windows/arm64: no dsh_*_windows_arm64.zip archive"},
		{"no licence", func(a map[string]map[string]string) {
			delete(a["dsh_0.0.0-SNAPSHOT-abc"+archiveSuffix("darwin", "amd64")], LicenseFile)
		}, "darwin/amd64: dsh_0.0.0-SNAPSHOT-abc_darwin_amd64.tar.gz: holds no LICENSE"},
		{"no binary", func(a map[string]map[string]string) {
			delete(a["dsh_0.0.0-SNAPSHOT-abc"+archiveSuffix("windows", "amd64")], "dsh.exe")
		}, "windows/amd64: dsh_0.0.0-SNAPSHOT-abc_windows_amd64.zip: holds no dsh.exe"},
		{"extra file", func(a map[string]map[string]string) {
			a["dsh_0.0.0-SNAPSHOT-abc_plan9_amd64.tar.gz"] = map[string]string{LicenseFile: "licence"}
		}, "lists files that are not one archive per target: dsh_0.0.0-SNAPSHOT-abc_plan9_amd64.tar.gz"},
		{"not a binary", nil, "linux/amd64: dsh_0.0.0-SNAPSHOT-abc_linux_amd64.tar.gz: read build info"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			arts, err := VerifySnapshot(fakeSnapshot(t, tc.edit), t.TempDir())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("VerifySnapshot = %v, want %q", err, tc.want)
			}
			if len(arts) != 0 {
				t.Errorf("VerifySnapshot accepted %d fake archives", len(arts))
			}
		})
	}
	t.Run("no checksum file", func(t *testing.T) {
		if _, err := VerifySnapshot(t.TempDir(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "the checksum file") {
			t.Errorf("VerifySnapshot = %v, want the checksum file named", err)
		}
	})
}

// TestReadChecksumsRejectsMalformedLines: a line that is not "<sha256>  <file>", a
// digest that is not hex and a file listed twice each fail with its line number.
func TestReadChecksumsRejectsMalformedLines(t *testing.T) {
	hexSum := strings.Repeat("ab", sha256.Size)
	for body, want := range map[string]string{
		"abc  dsh.tar.gz\n": ":1: ",
		strings.Repeat("zz", sha256.Size) + "  dsh.tar.gz\n": "is not a SHA-256",
		hexSum + "  a.zip\n" + hexSum + "  a.zip\n":          ":2: a.zip is listed twice",
		"\n": "lists no file",
		hexSum + "  a.zip\n" + hexSum + "  *b.zip\n" + "\n\n\n": "",
	} {
		p := filepath.Join(t.TempDir(), ChecksumFile)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		sums, err := readChecksums(p)
		if want == "" {
			if err != nil || len(sums) != 2 || sums["b.zip"] != hexSum {
				t.Errorf("readChecksums(%q) = %v, %v; want a.zip and b.zip", body, sums, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("readChecksums(%q) = %v, want %q", body, err, want)
		}
	}
}

// TestRehearseRefusesConfigWithDist: the rehearsal owns goreleaser's dist directory
// (out/dist), so a configuration that sets dist: -- which --clean would remove -- is
// refused before goreleaser runs; a root without .goreleaser.dsh.yaml fails naming it.
func TestRehearseRefusesConfigWithDist(t *testing.T) {
	root := scratch(t, "cross-broken")
	cfg := newTestConfig(t, root)
	if _, err := cfg.Rehearse(context.Background(), &bytes.Buffer{}, t.TempDir()); err == nil || !strings.Contains(err.Error(), GoReleaserConfig) {
		t.Errorf("Rehearse without %s = %v, want it named", GoReleaserConfig, err)
	}
	if err := os.WriteFile(filepath.Join(root, GoReleaserConfig), []byte("version: 2\ndist: dist\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := cfg.Rehearse(context.Background(), &out, t.TempDir()); err == nil || !strings.Contains(err.Error(), "sets dist:") {
		t.Errorf("Rehearse = %v, want the dist: key refused", err)
	}
	if strings.Contains(out.String(), "goreleaser release") {
		t.Errorf("goreleaser ran with a refused configuration:\n%s", out.String())
	}
}

// TestRehearseWithoutDshModuleFails: a root without cmd/dsh fails naming it.
func TestRehearseWithoutDshModuleFails(t *testing.T) {
	cfg := newTestConfig(t, scratch(t, "race-seeded"))
	if _, err := cfg.Rehearse(context.Background(), &bytes.Buffer{}, t.TempDir()); err == nil || !strings.Contains(err.Error(), "holds cmd/dsh") {
		t.Errorf("Rehearse = %v, want a failure naming cmd/dsh", err)
	}
}

// TestRehearseReplaySeam: while ReplayCheck is nil the replay clause is reported open,
// never passed (plan 19.1.2 is not built); once set, it runs against the host's
// binary, and its failure fails the rehearsal naming that binary.
func TestRehearseReplaySeam(t *testing.T) {
	saved := ReplayCheck
	t.Cleanup(func() { ReplayCheck = saved })
	host := Artifact{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Path: filepath.Join(t.TempDir(), "dsh-host")}
	arts := []Artifact{{GOOS: "plan9", GOARCH: "amd64", Path: "other"}, host}
	cfg := &Config{}

	ReplayCheck = nil
	var out bytes.Buffer
	if err := cfg.replay(context.Background(), &out, arts); err != nil || !strings.Contains(out.String(), "replay of dsh-host: OPEN, not built") || strings.Contains(out.String(), "passed") {
		t.Errorf("replay with no ReplayCheck = %v:\n%s", err, out.String())
	}

	var got string
	ReplayCheck = func(_ context.Context, _ io.Writer, bin string) error { got = bin; return nil }
	out.Reset()
	if err := cfg.replay(context.Background(), &out, arts); err != nil || got != host.Path || !strings.Contains(out.String(), "replay of dsh-host passed") {
		t.Errorf("replay = %v, ran on %q:\n%s", err, got, out.String())
	}

	ReplayCheck = func(context.Context, io.Writer, string) error { return errors.New("scenario failed") }
	if err := cfg.replay(context.Background(), &bytes.Buffer{}, arts); err == nil || !strings.Contains(err.Error(), "replay of dsh-host: scenario failed") {
		t.Errorf("replay = %v, want the failure naming the host binary", err)
	}
}

// TestGoReleaserDshConfig: the committed .goreleaser.dsh.yaml builds dsh pure Go for
// the six targets with archives and checksums.txt, disables the release, leaves dist
// to the rehearsal; .goreleaser.yaml, the file release.yml runs, builds no dsh, so a
// release tag publishes nothing new before DH-S-46 (plan 19.1.1).
func TestGoReleaserDshConfig(t *testing.T) {
	root := filepath.Join("..", "..")
	data, err := os.ReadFile(filepath.Join(root, GoReleaserConfig))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(data)
	for _, want := range []string{
		"version: 2\n", "  - id: dsh\n", "    dir: src/harness\n", "    main: ./cmd/dsh\n",
		"      - CGO_ENABLED=0\n", "      - GOWORK=off\n", "    goos: [linux, darwin, windows]\n",
		"    goarch: [amd64, arm64]\n", "      - -trimpath\n", "      - " + LicenseFile + "\n",
		"  name_template: " + ChecksumFile + "\n", "release:\n  disable: true\n",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("%s lacks %q", GoReleaserConfig, want)
		}
	}
	if distKey.MatchString(cfg) {
		t.Errorf("%s sets dist:", GoReleaserConfig)
	}
	var goos, goarch []string
	for _, tgt := range Targets {
		if !slices.Contains(goos, tgt.GOOS) {
			goos = append(goos, tgt.GOOS)
		}
		if !slices.Contains(goarch, tgt.GOARCH) {
			goarch = append(goarch, tgt.GOARCH)
		}
	}
	if len(goos)*len(goarch) != len(Targets) || !slices.Equal(goos, []string{"linux", "darwin", "windows"}) || !slices.Equal(goarch, []string{"amd64", "arm64"}) {
		t.Errorf("Targets %v are not the goos x goarch product of %s", Targets, GoReleaserConfig)
	}
	main, err := os.ReadFile(filepath.Join(root, ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`(?m)cmd/dsh|binary:\s*dsh\b`).Match(main) {
		t.Errorf(".goreleaser.yaml builds dsh; release.yml would publish it before DH-S-46")
	}
}

// TestSnapshotVersion: the version a rehearsal records is the one goreleaser linked
// into every dsh as main.version, read from the snapshot's metadata.json; a snapshot
// without a usable version fails the rehearsal rather than recording "(devel)"
// (DH-E-TOOLCHAIN-2: "dsh --version" prints the version matching the record).
func TestSnapshotVersion(t *testing.T) {
	for _, tc := range []struct{ name, metadata, want, err string }{
		{"snapshot", `{"project_name":"dsh","version":"0.1.4-SNAPSHOT-fa1186d","commit":"fa1186d"}`, "0.1.4-SNAPSHOT-fa1186d", ""},
		{"absent", "", "", "the snapshot's version"},
		{"not json", "version: 1", "", MetadataFile},
		{"empty version", `{"version":""}`, "", `names version ""`},
		{"spaced version", `{"version":"0.1 4"}`, "", `names version "0.1 4"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dist := t.TempDir()
			if tc.metadata != "" {
				if err := os.WriteFile(filepath.Join(dist, MetadataFile), []byte(tc.metadata), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := snapshotVersion(dist)
			if tc.err == "" {
				if err != nil || got != tc.want {
					t.Errorf("snapshotVersion = %q, %v; want %q", got, err, tc.want)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("snapshotVersion = %q, %v; want an error containing %q", got, err, tc.err)
			}
		})
	}
}
