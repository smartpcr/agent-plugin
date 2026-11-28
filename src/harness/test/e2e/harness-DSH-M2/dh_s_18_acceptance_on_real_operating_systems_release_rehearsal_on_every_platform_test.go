//go:build e2e

package e2e

// Step definitions of
// dh_s_18_acceptance_on_real_operating_systems_release_rehearsal_on_every_platform.feature
// (DH-S-18 Stage 8.2, DH-E-TOOLCHAIN-2, TC-21s3).
//
// The real release rehearsal ("harness-gate rehearse": the pinned GoReleaser on
// .goreleaser.dsh.yaml in snapshot mode, nothing published) runs once per suite into
// a scratch directory. Each scenario then unpacks its variant's archive again,
// independently of the rehearsal, checks it against checksums.txt and the
// rehearsal's record line, and runs "dsh --version" on the variant: this host when
// its OS matches, else the other side of WSL (wsl.exe from Windows, Windows interop
// from inside WSL). An unreachable variant fails the scenario; it never skips.

import (
	"archive/tar"
	"archive/zip"
	"bufio"
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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

const (
	s18rRehearseWithin = 15 * time.Minute
	s18rRunWithin      = 2 * time.Minute
	s18rChecksumFile   = "checksums.txt"
)

// s18rRecordLine is one binary line of "harness-gate rehearse".
var s18rRecordLine = regexp.MustCompile(`^([0-9a-f]{64})  (dsh-\S+)  version=(\S+) vcs\.revision=(\S+) vcs\.modified=(\S+)$`)

// s18rVersionLine is what "dsh --version" prints (cli.versionLine).
var s18rVersionLine = regexp.MustCompile(`^dsh (\S+) \((\S+); go\S+; (\S+/\S+)\)$`)

type s18rRecord struct{ sha, version, revision, modified string }

// s18rRehearsal is the suite's one rehearsal.
type s18rRehearsal struct {
	root, out, head string
	report          string
	records         map[string]s18rRecord // by binary name
	sums            map[string]string     // checksums.txt: archive name -> digest
}

var (
	s18rOuterT    *testing.T
	s18rOnce      sync.Once
	s18rShared    *s18rRehearsal
	s18rSharedErr error
)

// s18rRepoRoot is the checkout: the directory above the working directory that holds
// tools/harness-gate/go.mod.
func s18rRepoRoot() (string, error) {
	dir, err := filepath.Abs(".")
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "tools", "harness-gate", "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("repository root (tools/harness-gate/go.mod) not found above the working directory")
		}
		dir = parent
	}
}

func s18rCommand(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w\n%s%s", name, strings.Join(args, " "), err, out, stderr.String())
	}
	return string(out), nil
}

func s18rRehearse() (*s18rRehearsal, error) {
	s18rOnce.Do(func() {
		s18rShared, s18rSharedErr = s18rRunRehearsal()
	})
	return s18rShared, s18rSharedErr
}

func s18rRunRehearsal() (*s18rRehearsal, error) {
	root, err := s18rRepoRoot()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), s18rRehearseWithin)
	defer cancel()
	head, err := s18rCommand(ctx, root, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	tagsBefore, err := s18rCommand(ctx, root, nil, "git", "tag", "--list")
	if err != nil {
		return nil, err
	}
	out, err := os.MkdirTemp("", "dsh-s18-rehearsal-")
	if err != nil {
		return nil, err
	}
	s18rOuterT.Cleanup(func() { _ = os.RemoveAll(out) })
	report, err := s18rCommand(ctx, filepath.Join(root, "tools", "harness-gate"), []string{"GOWORK=off"},
		"go", "run", ".", "rehearse", "--root", "../..", "--out", out)
	if err != nil {
		return nil, fmt.Errorf("harness-gate rehearse: %w", err)
	}
	if !strings.Contains(report, "nothing published") {
		return nil, fmt.Errorf("harness-gate rehearse did not report \"nothing published\":\n%s", report)
	}
	tagsAfter, err := s18rCommand(ctx, root, nil, "git", "tag", "--list")
	if err != nil {
		return nil, err
	}
	if tagsAfter != tagsBefore {
		return nil, fmt.Errorf("the rehearsal changed the tags:\nbefore:\n%s\nafter:\n%s", tagsBefore, tagsAfter)
	}
	r := &s18rRehearsal{root: root, out: out, head: strings.TrimSpace(head), report: report, records: map[string]s18rRecord{}}
	sc := bufio.NewScanner(strings.NewReader(report))
	for sc.Scan() {
		if m := s18rRecordLine.FindStringSubmatch(strings.TrimSpace(sc.Text())); m != nil {
			r.records[m[2]] = s18rRecord{sha: m[1], version: m[3], revision: m[4], modified: m[5]}
		}
	}
	if len(r.records) == 0 {
		return nil, fmt.Errorf("harness-gate rehearse printed no binary record line:\n%s", report)
	}
	if r.sums, err = s18rReadChecksums(filepath.Join(out, "dist", s18rChecksumFile)); err != nil {
		return nil, err
	}
	return r, nil
}

func s18rReadChecksums(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sums := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 || len(f[0]) != 64 {
			return nil, fmt.Errorf("%s: %q is not \"<sha256>  <file>\"", path, line)
		}
		sums[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
	}
	if len(sums) == 0 {
		return nil, fmt.Errorf("%s lists no file", path)
	}
	return sums, nil
}

func s18rSHA(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// s18rArchiveName is the archive of goos/goarch in checksums.txt.
func (r *s18rRehearsal) archiveName(goos, goarch string) (string, error) {
	suffix := "_" + goos + "_" + goarch + ".tar.gz"
	if goos == "windows" {
		suffix = "_" + goos + "_" + goarch + ".zip"
	}
	var found string
	for n := range r.sums {
		if strings.HasPrefix(n, "dsh_") && strings.HasSuffix(n, suffix) {
			if found != "" {
				return "", fmt.Errorf("two archives for %s/%s in %s: %s and %s", goos, goarch, s18rChecksumFile, found, n)
			}
			found = n
		}
	}
	if found == "" {
		return "", fmt.Errorf("no dsh_*%s archive in %s", suffix, s18rChecksumFile)
	}
	return found, nil
}

// s18rUnpack checks archive against want before reading anything of it, then writes
// its top-level dsh binary into dir and returns that path. LICENSE must be present.
func s18rUnpack(archive, want, exe, dir string) (string, error) {
	data, err := os.ReadFile(archive)
	if err != nil {
		return "", err
	}
	if got := s18rSHA(data); got != want {
		return "", fmt.Errorf("%s: SHA-256 %s, %s records %s", filepath.Base(archive), got, s18rChecksumFile, want)
	}
	files := map[string][]byte{}
	switch {
	case strings.HasSuffix(archive, ".zip"):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return "", err
		}
		for _, f := range zr.File {
			if f.FileInfo().Mode().IsRegular() && !strings.Contains(f.Name, "/") {
				rc, err := f.Open()
				if err != nil {
					return "", err
				}
				b, err := io.ReadAll(rc)
				_ = rc.Close()
				if err != nil {
					return "", err
				}
				files[f.Name] = b
			}
		}
	default:
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return "", err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", err
			}
			if h.Typeflag == tar.TypeReg && !strings.Contains(h.Name, "/") {
				b, err := io.ReadAll(tr)
				if err != nil {
					return "", err
				}
				files[h.Name] = b
			}
		}
	}
	if _, ok := files["LICENSE"]; !ok {
		return "", fmt.Errorf("%s holds no LICENSE", filepath.Base(archive))
	}
	b, ok := files[exe]
	if !ok {
		return "", fmt.Errorf("%s holds no %s", filepath.Base(archive), exe)
	}
	bin := filepath.Join(dir, exe)
	if err := os.WriteFile(bin, b, 0o755); err != nil {
		return "", err
	}
	return bin, nil
}

func s18rInsideWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	_, err := os.Stat("/proc/sys/fs/binfmt_misc/WSLInterop")
	return err == nil
}

// s18rVariant is where a rehearsed binary runs.
type s18rVariant struct {
	goos, goarch string
	how          string // "native", "wsl" (wsl.exe from Windows) or "interop" (a .exe from WSL)
}

func s18rLinuxVariant(ctx context.Context) (s18rVariant, error) {
	if runtime.GOOS == "linux" {
		return s18rVariant{"linux", runtime.GOARCH, "native"}, nil
	}
	if runtime.GOOS != "windows" {
		return s18rVariant{}, fmt.Errorf("no Linux variant reachable from %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	m, err := s18rCommand(ctx, "", nil, "wsl.exe", "-e", "uname", "-m")
	if err != nil {
		return s18rVariant{}, fmt.Errorf("the Linux variant is the default WSL distribution, which did not answer: %w", err)
	}
	switch strings.TrimSpace(m) {
	case "x86_64":
		return s18rVariant{"linux", "amd64", "wsl"}, nil
	case "aarch64", "arm64":
		return s18rVariant{"linux", "arm64", "wsl"}, nil
	default:
		return s18rVariant{}, fmt.Errorf("the WSL distribution runs on %q, no rehearsal target", strings.TrimSpace(m))
	}
}

func s18rWindowsVariant() (s18rVariant, error) {
	if runtime.GOOS == "windows" {
		return s18rVariant{"windows", runtime.GOARCH, "native"}, nil
	}
	if runtime.GOOS == "linux" && s18rInsideWSL() {
		return s18rVariant{"windows", runtime.GOARCH, "interop"}, nil
	}
	return s18rVariant{}, fmt.Errorf("no Windows host reachable from %s/%s (neither Windows nor WSL interop)", runtime.GOOS, runtime.GOARCH)
}

// s18rRun runs bin --version on v and returns its standard output and exit code.
func s18rRun(ctx context.Context, v s18rVariant, bin, home string) (string, int, error) {
	var cmd *exec.Cmd
	switch v.how {
	case "wsl":
		p, err := s18rCommand(ctx, "", nil, "wsl.exe", "-e", "wslpath", "-a", filepath.ToSlash(bin))
		if err != nil {
			return "", -1, err
		}
		cmd = exec.CommandContext(ctx, "wsl.exe", "-e", strings.TrimSpace(p), "--version")
	default:
		cmd = exec.CommandContext(ctx, bin, "--version")
		cmd.Env = append(os.Environ(), "DSH_HOME="+home)
	}
	cmd.Dir = home
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0, nil
	case errors.As(err, &exitErr):
		return string(out) + stderr.String(), exitErr.ExitCode(), nil
	default:
		return "", -1, fmt.Errorf("start %s --version (%s): %w\n%s", bin, v.how, err, stderr.String())
	}
}

// s18rState is one scenario's state.
type s18rState struct {
	r       *s18rRehearsal
	variant s18rVariant
	archive string
	binName string
	bin     string
	output  string
	exit    int

	tampered  string
	tamperDir string
	checkErr  error
}

func (s *s18rState) scratch(prefix string) (string, error) {
	d, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", err
	}
	s18rOuterT.Cleanup(func() { _ = os.RemoveAll(d) })
	return d, nil
}

func (s *s18rState) rehearsalRan() error {
	r, err := s18rRehearse()
	if err != nil {
		return err
	}
	s.r = r
	return nil
}

func (s *s18rState) aLinuxVariant(ctx context.Context) error {
	v, err := s18rLinuxVariant(ctx)
	s.variant = v
	return err
}

func (s *s18rState) theWindowsHost() error {
	v, err := s18rWindowsVariant()
	s.variant = v
	return err
}

func (s *s18rState) unpackAndRun(ctx context.Context) error {
	name, err := s.r.archiveName(s.variant.goos, s.variant.goarch)
	if err != nil {
		return err
	}
	s.archive = name
	s.binName = "dsh-" + s.variant.goos + "-" + s.variant.goarch
	exe := "dsh"
	if s.variant.goos == "windows" {
		s.binName += ".exe"
		exe += ".exe"
	}
	dir, err := s.scratch("dsh-s18-unpacked-")
	if err != nil {
		return err
	}
	if s.bin, err = s18rUnpack(filepath.Join(s.r.out, "dist", name), s.r.sums[name], exe, dir); err != nil {
		return err
	}
	home, err := s.scratch("dsh-s18-home-")
	if err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(ctx, s18rRunWithin)
	defer cancel()
	s.output, s.exit, err = s18rRun(rctx, s.variant, s.bin, home)
	return err
}

func (s *s18rState) archiveMatchesChecksums() error {
	data, err := os.ReadFile(filepath.Join(s.r.out, "dist", s.archive))
	if err != nil {
		return err
	}
	if got, want := s18rSHA(data), s.r.sums[s.archive]; got != want {
		return fmt.Errorf("%s: SHA-256 %s, %s records %s", s.archive, got, s18rChecksumFile, want)
	}
	return nil
}

func (s *s18rState) binaryMatchesRecord() error {
	rec, ok := s.r.records[s.binName]
	if !ok {
		return fmt.Errorf("the rehearsal recorded no %s:\n%s", s.binName, s.r.report)
	}
	data, err := os.ReadFile(s.bin)
	if err != nil {
		return err
	}
	if got := s18rSHA(data); got != rec.sha {
		return fmt.Errorf("%s unpacked from %s: SHA-256 %s, the rehearsal recorded %s", s.binName, s.archive, got, rec.sha)
	}
	return nil
}

func (s *s18rState) exitsZero() error {
	if s.exit != 0 {
		return fmt.Errorf("%s --version on %s/%s (%s) exited %d:\n%s", s.binName, s.variant.goos, s.variant.goarch, s.variant.how, s.exit, s.output)
	}
	return nil
}

func (s *s18rState) versionLine() ([]string, s18rRecord, error) {
	rec := s.r.records[s.binName]
	line := strings.TrimSpace(s.output)
	m := s18rVersionLine.FindStringSubmatch(line)
	if m == nil {
		return nil, rec, fmt.Errorf("%s --version printed %q, want \"dsh <version> (<revision>; <go>; <os>/<arch>)\"", s.binName, line)
	}
	return m, rec, nil
}

func (s *s18rState) printsRecordedVersion() error {
	m, rec, err := s.versionLine()
	if err != nil {
		return err
	}
	if rec.version == "" || rec.version == "(devel)" {
		return fmt.Errorf("the rehearsal recorded version %q for %s, not a release version", rec.version, s.binName)
	}
	if m[1] != rec.version {
		return fmt.Errorf("%s --version prints version %s, the rehearsal recorded %s", s.binName, m[1], rec.version)
	}
	return nil
}

func (s *s18rState) namesRevisionAndPlatform() error {
	m, rec, err := s.versionLine()
	if err != nil {
		return err
	}
	if rec.revision != s.r.head {
		return fmt.Errorf("the rehearsal recorded vcs.revision %s for %s, the checkout is at %s", rec.revision, s.binName, s.r.head)
	}
	want := rec.revision
	if rec.modified == "true" {
		want += "-modified"
	}
	if m[2] != want {
		return fmt.Errorf("%s --version names revision %s, the record says %s (vcs.modified=%s)", s.binName, m[2], want, rec.modified)
	}
	if platform := s.variant.goos + "/" + s.variant.goarch; m[3] != platform {
		return fmt.Errorf("%s --version names platform %s, the variant is %s", s.binName, m[3], platform)
	}
	return nil
}

func (s *s18rState) tamperedCopy(goos string) error {
	name, err := s.r.archiveName(goos, "amd64")
	if err != nil {
		return err
	}
	s.archive = name
	data, err := os.ReadFile(filepath.Join(s.r.out, "dist", name))
	if err != nil {
		return err
	}
	data[len(data)/2] ^= 0xff
	dir, err := s.scratch("dsh-s18-tampered-")
	if err != nil {
		return err
	}
	s.tampered = filepath.Join(dir, name)
	if err := os.WriteFile(s.tampered, data, 0o644); err != nil {
		return err
	}
	s.tamperDir, err = s.scratch("dsh-s18-tampered-out-")
	return err
}

func (s *s18rState) checkCopy() error {
	exe := "dsh"
	if strings.HasSuffix(s.archive, ".zip") {
		exe += ".exe"
	}
	_, s.checkErr = s18rUnpack(s.tampered, s.r.sums[s.archive], exe, s.tamperDir)
	return nil
}

func (s *s18rState) refusedNamingDigests() error {
	if s.checkErr == nil {
		return fmt.Errorf("the tampered copy of %s passed the checksum check", s.archive)
	}
	data, err := os.ReadFile(s.tampered)
	if err != nil {
		return err
	}
	msg := s.checkErr.Error()
	if !strings.Contains(msg, s18rSHA(data)) || !strings.Contains(msg, s.r.sums[s.archive]) {
		return fmt.Errorf("the refusal %q does not name the copy's digest and the recorded one", msg)
	}
	return nil
}

func (s *s18rState) nothingUnpacked() error {
	entries, err := os.ReadDir(s.tamperDir)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("the refused copy left %d files in its output directory", len(entries))
	}
	return nil
}

func InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_release_rehearsal_on_every_platform(ctx *godog.ScenarioContext) {
	s := &s18rState{}
	ctx.Step(`^the release rehearsal of this checkout ran with nothing published$`, s.rehearsalRan)
	ctx.Step(`^a Linux variant$`, s.aLinuxVariant)
	ctx.Step(`^the Windows host$`, s.theWindowsHost)
	ctx.Step(`^the rehearsal archive for the variant is unpacked and "dsh --version" runs on it$`, s.unpackAndRun)
	ctx.Step(`^the archive's SHA-256 matches its line in checksums\.txt$`, s.archiveMatchesChecksums)
	ctx.Step(`^the unpacked binary's SHA-256 matches the rehearsal record$`, s.binaryMatchesRecord)
	ctx.Step(`^"dsh --version" exits 0$`, s.exitsZero)
	ctx.Step(`^it prints the embedded version matching the record$`, s.printsRecordedVersion)
	ctx.Step(`^it names the recorded revision of the checked-out commit and the variant's platform$`, s.namesRevisionAndPlatform)
	ctx.Step(`^a copy of the rehearsal's (linux|windows) archive with one byte changed$`, s.tamperedCopy)
	ctx.Step(`^the copy is checked against checksums\.txt$`, s.checkCopy)
	ctx.Step(`^the check refuses it, naming both digests$`, s.refusedNamingDigests)
	ctx.Step(`^no binary of the copy is unpacked$`, s.nothingUnpacked)
}

func TestE2E_dh_s_18_acceptance_on_real_operating_systems_release_rehearsal_on_every_platform(t *testing.T) {
	s18rOuterT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_release_rehearsal_on_every_platform,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_18_acceptance_on_real_operating_systems_release_rehearsal_on_every_platform.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
