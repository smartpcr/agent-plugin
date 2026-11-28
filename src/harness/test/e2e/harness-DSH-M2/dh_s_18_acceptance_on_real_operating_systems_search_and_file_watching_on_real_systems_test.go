//go:build e2e

package e2e

// Step definitions of
// dh_s_18_acceptance_on_real_operating_systems_search_and_file_watching_on_real_systems.feature
// (DH-S-18 Stage 8.7: DH-E-RIPGREP-1, -2, DH-E-CHOKIDAR-1, -2; TC-21s3).
//
// Each scenario runs the product's own scenario tests, unmodified, built with
// -tags scenario for the variant and run there: this host when its OS matches,
// else the default WSL distribution through wsl.exe (a Windows host's Linux
// variant). The inputs are the operator's when set (DSH_S18_<LINUX|WINDOWS>_<NAME
// without DSH_> overrides one per variant), else an ephemeral instance this file
// stands up on the variant's real volumes. The verdict is the rows the tests
// write to DSH_SCENARIO_REPORT, read back, with the timing report RIPGREP-1
// writes beside it and the log lines that show each clause ran.

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

const (
	s87Pkg       = "./internal/testkit/scenarios"
	s87RunWithin = 2 * time.Hour
	// s87Searches is the size of the fixed search set of DH-E-RIPGREP-1
	// (searchSet in search_scenario_test.go): the timing report has one line per
	// repository and search.
	s87Searches = 10
	// s87SmallBelow and s87VeryLargeMin bound the checkout sizes of
	// DH-E-RIPGREP-1: the budget is agreed on a 100,000-file checkout.
	s87SmallBelow   = 1000
	s87VeryLargeMin = 100000
)

// s87SmallRepo is the pinned small public repository: URL, tag, commit.
var s87SmallRepo = [3]string{"https://github.com/fsnotify/fsnotify", "v1.10.1", "76b01a6e8f502187fecedea8b025e79e5a86085c"}

var s87OuterT *testing.T

// s87RG is the pinned ripgrep release fetched as the comparison oracle where a
// variant has no rg of its own; the digests are the release's .sha256 assets.
var s87RG = map[string][2]string{
	"linux/amd64":   {"https://github.com/BurntSushi/ripgrep/releases/download/14.1.1/ripgrep-14.1.1-x86_64-unknown-linux-musl.tar.gz", "4cf9f2741e6c465ffdb7c26f38056a59e2a2544b51f7cc128ef28337eeae4d8e"},
	"linux/arm64":   {"https://github.com/BurntSushi/ripgrep/releases/download/14.1.1/ripgrep-14.1.1-aarch64-unknown-linux-gnu.tar.gz", "c827481c4ff4ea10c9dc7a4022c8de5db34a5737cb74484d62eb94a95841ab2f"},
	"windows/amd64": {"https://github.com/BurntSushi/ripgrep/releases/download/14.1.1/ripgrep-14.1.1-x86_64-pc-windows-msvc.zip", "d0f534024c42afd6cb4d38907c25cd2b249b79bbe6cc1dbee8e3e37c2b6e25a1"},
}

// ---- variants ----

type s87Variant struct {
	goos, goarch string
	wsl          bool
	distro       string
}

func (v *s87Variant) String() string {
	if v.wsl {
		return "linux (WSL " + v.distro + ")"
	}
	return v.goos
}

func s87Reach(goos string) (*s87Variant, error) {
	if runtime.GOOS == goos {
		return &s87Variant{goos: goos, goarch: runtime.GOARCH}, nil
	}
	if goos != "linux" || runtime.GOOS != "windows" {
		return nil, fmt.Errorf("the %s variant is not reachable from this %s host", goos, runtime.GOOS)
	}
	v := &s87Variant{goos: "linux", wsl: true}
	out, err := v.must("sh", "-c", `uname -m; printf '%s\n' "$WSL_DISTRO_NAME"`)
	if err != nil {
		return nil, fmt.Errorf("the Linux variant is not reachable through wsl.exe: %v", err)
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return nil, fmt.Errorf("the Linux variant answered %q to uname -m and WSL_DISTRO_NAME", out)
	}
	v.distro = f[1]
	switch f[0] {
	case "x86_64":
		v.goarch = "amd64"
	case "aarch64":
		v.goarch = "arm64"
	default:
		return nil, fmt.Errorf("the Linux variant runs on %q, no build target", f[0])
	}
	return v, nil
}

// s87Env is this process's environment without the Go build variables, with set
// added; a PATH in set replaces this process's.
func s87Env(set ...string) []string {
	drop := []string{"GOWORK", "GOFLAGS", "GOOS", "GOARCH", "CGO_ENABLED"}
	for _, kv := range set {
		if k, _, _ := strings.Cut(kv, "="); strings.EqualFold(k, "PATH") {
			drop = append(drop, "PATH")
		}
	}
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(strings.ToUpper(kv), "=")
		return slices.Contains(drop, k)
	})
	return append(env, set...)
}

func (v *s87Variant) run(ctx context.Context, dir string, env []string, argv ...string) (string, int, error) {
	var cmd *exec.Cmd
	if v.wsl {
		args := []string{}
		if dir != "" {
			args = append(args, "--cd", dir)
		}
		args = append(append(append(args, "-e", "env"), env...), argv...)
		cmd = exec.CommandContext(ctx, "wsl.exe", args...)
		cmd.Env = s87Env()
	} else {
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir, cmd.Env = dir, s87Env(env...)
	}
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), exit.ExitCode(), nil
	}
	return string(out), 0, err
}

func (v *s87Variant) must(argv ...string) (string, error) {
	out, code, err := v.run(context.Background(), "", nil, argv...)
	if err != nil || code != 0 {
		return out, fmt.Errorf("%v on %s: exit %d %v\n%s", argv, v, code, err, out)
	}
	return strings.TrimSpace(out), nil
}

// pipe runs argv on the variant with stdin and returns its trimmed output.
func (v *s87Variant) pipe(stdin []byte, argv ...string) (string, error) {
	return v.exec(stdin, false, argv...)
}

// asRoot runs argv as root on the Linux variant: wsl.exe -u root under WSL,
// else directly when this process is root, else sudo -n (never a prompt).
func (v *s87Variant) asRoot(stdin []byte, argv ...string) (string, error) {
	if v.goos != "linux" {
		return "", fmt.Errorf("asRoot on %s", v)
	}
	return v.exec(stdin, true, argv...)
}

func (v *s87Variant) exec(stdin []byte, root bool, argv ...string) (string, error) {
	var cmd *exec.Cmd
	switch {
	case v.wsl && root:
		cmd = exec.Command("wsl.exe", append([]string{"-u", "root", "-e"}, argv...)...)
	case v.wsl:
		cmd = exec.Command("wsl.exe", append([]string{"-e"}, argv...)...)
	case root && os.Geteuid() != 0:
		cmd = exec.Command("sudo", append([]string{"-n", "--"}, argv...)...)
	default:
		cmd = exec.Command(argv[0], argv[1:]...)
	}
	cmd.Env, cmd.Stdin = s87Env(), bytes.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%v on %s (root %v): %v\n%s%s", argv, v, root, err, out.String(), errOut.String())
	}
	return strings.TrimSpace(out.String()), nil
}

// path is a host path as the variant names it.
func (v *s87Variant) path(p string) (string, error) {
	if !v.wsl {
		return p, nil
	}
	return v.must("wslpath", "-a", filepath.ToSlash(p))
}

func (v *s87Variant) join(dir string, elem ...string) string {
	if v.goos == "windows" {
		return filepath.Join(append([]string{dir}, elem...)...)
	}
	return strings.Join(append([]string{dir}, elem...), "/")
}

// tempDir is a scratch directory on the variant's own filesystem, removed at the end.
func (v *s87Variant) tempDir(prefix string) (string, error) {
	if !v.wsl {
		d, err := os.MkdirTemp("", prefix)
		if err == nil {
			s87OuterT.Cleanup(func() { _ = os.RemoveAll(d) })
		}
		return d, err
	}
	d, err := v.must("mktemp", "-d", "/tmp/"+prefix+"XXXXXX")
	if err == nil {
		s87OuterT.Cleanup(func() { _, _ = v.must("rm", "-rf", d) })
	}
	return d, err
}

func (v *s87Variant) writeFile(p string, data []byte) error {
	if !v.wsl {
		return os.WriteFile(p, data, 0o600)
	}
	cmd := exec.Command("wsl.exe", "-e", "sh", "-c", `cat > "$1"`, "sh", p)
	cmd.Env, cmd.Stdin = s87Env(), bytes.NewReader(data)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("writing %s on %s: %v %s", p, v, err, out)
	}
	return nil
}

func (v *s87Variant) readFile(p string) ([]byte, error) {
	if !v.wsl {
		return os.ReadFile(p)
	}
	cmd := exec.Command("wsl.exe", "-e", "cat", p)
	cmd.Env = s87Env()
	return cmd.Output()
}

// lookPath is program's path on the variant, or "".
func (v *s87Variant) lookPath(program string) string {
	if !v.wsl {
		p, _ := exec.LookPath(program)
		return p
	}
	p, _ := v.must("sh", "-c", `command -v "$1"`, "sh", program)
	return p
}

// fsType names the filesystem holding dir, as the OS reports it.
func (v *s87Variant) fsType(dir string) (string, error) {
	if v.goos == "windows" {
		return v.must("powershell", "-NoProfile", "-NonInteractive", "-Command",
			"[System.IO.DriveInfo]::new([System.IO.Path]::GetPathRoot('"+strings.ReplaceAll(dir, "'", "''")+"')).DriveFormat")
	}
	out, err := v.must("stat", "-f", "-c", "%T", dir)
	if out == "ext2/ext3" {
		out = "ext4"
	}
	return out, err
}

// ---- builds ----

var (
	s87BuildMu sync.Mutex
	s87Built   = map[string]string{}
)

func s87Module() (string, error) {
	dir, err := filepath.Abs(".")
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "cmd", "dsh")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("the harness module (cmd/dsh, go.mod) is not above the working directory")
		}
		dir = parent
	}
}

// build builds the scenario test binary for v once per suite and returns its
// path as v names it.
func (v *s87Variant) build() (string, error) {
	s87BuildMu.Lock()
	defer s87BuildMu.Unlock()
	key := v.goos + "-" + v.goarch + "-scenarios"
	if p, ok := s87Built[key]; ok {
		return p, nil
	}
	mod, err := s87Module()
	if err != nil {
		return "", err
	}
	out := filepath.Join(s87OuterT.TempDir(), key)
	if v.goos == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "test", "-c", "-trimpath", "-tags", "scenario", "-o", out, s87Pkg)
	cmd.Dir, cmd.Env = mod, append(s87Env(), "GOWORK=off", "GOOS="+v.goos, "GOARCH="+v.goarch, "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go test -c -tags scenario for %s: %v\n%s", v, err, b)
	}
	p, err := v.path(out)
	if err == nil {
		s87Built[key] = p
	}
	return p, err
}

// pkgDir is the scenario package's source directory as the variant names it
// (scenario.Main reads its directives there).
func (v *s87Variant) pkgDir() (string, error) {
	mod, err := s87Module()
	if err != nil {
		return "", err
	}
	return v.path(filepath.Join(mod, filepath.FromSlash(strings.TrimPrefix(s87Pkg, "./"))))
}

// ---- state ----

type s87Leg struct {
	v        *s87Variant
	op       map[string]string // the inputs the operator set
	inputs   map[string]string // the run's DSH_* inputs
	path     string            // PATH of the run when the suite put an rg on it
	rg       string            // the comparison rg, and where it came from
	volumes  []string          // the filesystems the leg's directories are on
	files    map[string]int    // the files of each checkout searched
	remote   []string          // the kinds of remote directory the suite stood up
	report   string
	log      string
	logPath  string
	code     int
	rows     map[string]map[string]any
	detail   []byte
	ran      error
	evidence []string
}

type s87State struct {
	ids, tests []string
	legs       []*s87Leg
	budget     time.Duration
}

func s87Input(v *s87Variant, name string) string {
	if s := strings.TrimSpace(os.Getenv("DSH_S18_" + strings.ToUpper(v.goos) + "_" + strings.TrimPrefix(name, "DSH_"))); s != "" {
		return s
	}
	return strings.TrimSpace(os.Getenv(name))
}

func (s *s87State) legsFor(names ...string) error {
	s.legs = nil
	var errs []error
	for _, goos := range []string{"linux", "windows"} {
		v, err := s87Reach(goos)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		l := &s87Leg{v: v, op: map[string]string{}, inputs: map[string]string{}}
		for _, n := range names {
			if val := s87Input(v, n); val != "" {
				l.op[n] = val
			}
		}
		maps.Copy(l.inputs, l.op)
		s.legs = append(s.legs, l)
	}
	return errors.Join(errs...)
}

func (l *s87Leg) set(name, value string) {
	if _, ok := l.op[name]; !ok {
		l.inputs[name] = value
	}
}

func (l *s87Leg) lane() string {
	if len(l.op) == 0 {
		return "ephemeral instance"
	}
	return "ephemeral instance with the operator's " + strings.Join(slices.Sorted(maps.Keys(l.op)), ", ")
}

// each runs f on every leg, all of them even when one fails.
func (s *s87State) each(f func(l *s87Leg) error) error {
	var errs []error
	for _, l := range s.legs {
		if err := f(l); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", l.v, err))
		}
	}
	return errors.Join(errs...)
}

// eachParallel runs f on every leg at once: each leg works on its own
// variant's volumes.
func (s *s87State) eachParallel(f func(l *s87Leg) error) error {
	errs := make([]error, len(s.legs))
	var wg sync.WaitGroup
	for i, l := range s.legs {
		wg.Go(func() {
			if err := f(l); err != nil {
				errs[i] = fmt.Errorf("%s: %w", l.v, err)
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

// ---- search provisioning ----

func (s *s87State) givenSearchVariants() error {
	s.ids = []string{"DH-E-RIPGREP-1", "DH-E-RIPGREP-2"}
	s.tests = []string{"TestSearchMeetsBudget", "TestFileListingMatchesGit"}
	if err := s.legsFor("DSH_SCENARIO_REPOS", "DSH_SCENARIO_LISTING_EXPLAINED", "DSH_SCENARIO_WHOLE_SEARCH_TIMEOUT"); err != nil {
		return err
	}
	return s.eachParallel(func(l *s87Leg) error {
		if l.v.lookPath("git") == "" {
			return errors.New("service:filesystem: git is not on the PATH of the variant; RIPGREP-2 compares with git ls-files")
		}
		if l.inputs["DSH_SCENARIO_REPOS"] == "" {
			repos, err := l.checkouts()
			if err != nil {
				return err
			}
			l.set("DSH_SCENARIO_REPOS", strings.Join(repos, ";"))
		}
		l.files = map[string]int{}
		for _, repo := range strings.Split(l.inputs["DSH_SCENARIO_REPOS"], ";") {
			if repo = strings.TrimSpace(repo); repo == "" {
				continue
			}
			fs, err := l.v.fsType(repo)
			if err != nil {
				return fmt.Errorf("checkout %s: %v", repo, err)
			}
			n, err := l.v.must("git", "-C", repo, "-c", "safe.directory=*", "ls-files", "--cached", "--others", "--exclude-standard", "-z")
			if err != nil {
				return fmt.Errorf("checkout %s is not a Git work tree: %v", repo, err)
			}
			l.files[repo] = strings.Count(n, "\x00")
			l.volumes = append(l.volumes, fmt.Sprintf("%s %s (%d files)", fs, repo, l.files[repo]))
		}
		if err := s87Sizes(l.files, s87SmallBelow, s87VeryLargeMin); err != nil {
			return fmt.Errorf("DSH_SCENARIO_REPOS %s: %w", l.inputs["DSH_SCENARIO_REPOS"], err)
		}
		return l.provideRG()
	})
}

// s87Sizes requires the checkouts of DH-E-RIPGREP-1 (libs/vscode-ripgrep.md:
// a small, a medium and a very large public repository; the budget is set on a
// 100,000-file checkout): one under small files, one between, and one of at
// least large files.
func s87Sizes(files map[string]int, small, large int) error {
	var have [3]bool
	for _, n := range files {
		switch {
		case n < small:
			have[0] = true
		case n < large:
			have[1] = true
		default:
			have[2] = true
		}
	}
	var miss []string
	for i, want := range []string{fmt.Sprintf("a small one (under %d files)", small), fmt.Sprintf("a medium one (%d to %d files)", small, large-1), fmt.Sprintf("a very large one (at least %d files)", large)} {
		if !have[i] {
			miss = append(miss, want)
		}
	}
	if len(miss) > 0 {
		return fmt.Errorf("the checkouts %v lack %s", files, strings.Join(miss, ", "))
	}
	return nil
}

// checkouts stands up the three checkouts on the variant's own filesystem: the
// pinned small public repository, this repository at HEAD (medium), and a very
// large one built from it.
func (l *s87Leg) checkouts() ([]string, error) {
	dir, err := l.v.tempDir("dsh-s87-repos-")
	if err != nil {
		return nil, err
	}
	small, err := l.cloneSmall(dir)
	if err != nil {
		return nil, err
	}
	medium, err := l.cloneThisRepo(dir)
	if err != nil {
		return nil, err
	}
	large, err := l.veryLarge(dir, medium)
	if err != nil {
		return nil, err
	}
	return []string{small, medium, large}, nil
}

// cloneSmall clones the pinned small public repository and checks its commit.
func (l *s87Leg) cloneSmall(dir string) (string, error) {
	co := l.v.join(dir, "fsnotify")
	if _, err := l.v.must("git", "-c", "advice.detachedHead=false", "clone", "--quiet", "--depth", "1", "--branch", s87SmallRepo[1], s87SmallRepo[0], co); err != nil {
		return "", fmt.Errorf("service:filesystem: the small public checkout %s %s: %w", s87SmallRepo[0], s87SmallRepo[1], err)
	}
	if head, err := l.v.must("git", "-C", co, "rev-parse", "HEAD"); err != nil || head != s87SmallRepo[2] {
		return "", fmt.Errorf("%s %s is at %q, want the pinned %s (%v)", s87SmallRepo[0], s87SmallRepo[1], head, s87SmallRepo[2], err)
	}
	return co, l.warm(co)
}

// veryLarge builds a Git repository whose one commit holds enough copies of the
// medium checkout's tree, side by side, to reach s87VeryLargeMin files: real
// public content at the documented size, checked out like any clone.
func (l *s87Leg) veryLarge(dir, medium string) (string, error) {
	co := l.v.join(dir, "very-large")
	if _, err := l.v.must("git", "-c", "safe.directory=*", "clone", "--quiet", "--no-checkout", medium, co); err != nil {
		return "", err
	}
	tree, err := l.v.must("git", "-C", co, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return "", err
	}
	names, err := l.v.must("git", "-C", co, "ls-tree", "-r", "-z", "--name-only", "HEAD")
	if err != nil {
		return "", err
	}
	n := strings.Count(names, "\x00")
	if n == 0 {
		return "", fmt.Errorf("%s has no files", medium)
	}
	var entries strings.Builder
	for i := range s87VeryLargeMin/n + 1 {
		fmt.Fprintf(&entries, "040000 tree %s\tcopy%03d\x00", tree, i+1)
	}
	root, err := l.v.pipe([]byte(entries.String()), "git", "-C", co, "mktree", "-z")
	if err != nil {
		return "", err
	}
	commit, err := l.v.must("git", "-C", co, "-c", "user.name=dsh-s87", "-c", "user.email=dsh-s87@invalid", "commit-tree", root, "-m", "very large checkout")
	if err != nil {
		return "", err
	}
	if _, err := l.v.must("git", "-C", co, "-c", "advice.detachedHead=false", "checkout", "--quiet", commit); err != nil {
		return "", err
	}
	return co, l.warm(co)
}

// warm reads a fresh checkout through once, as a checkout at rest on a working
// machine has been, so an on-access scan of the files just written is not timed
// as the search.
func (l *s87Leg) warm(co string) error {
	if _, err := l.v.must("git", "-C", co, "grep", "-q", "--no-index", "-I", "-e", "dsh-s87-warm-no-such-line"); err != nil && !strings.Contains(err.Error(), ": exit 1 ") {
		return err
	}
	return nil
}

// cloneThisRepo clones this repository at HEAD into dir on the variant's own
// filesystem: a real public checkout (github.com/smartpcr/agent-plugin).
func (l *s87Leg) cloneThisRepo(dir string) (string, error) {
	mod, err := s87Module()
	if err != nil {
		return "", err
	}
	common, err := exec.Command("git", "-C", mod, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", fmt.Errorf("the Git directory of this repository: %v", err)
	}
	head, err := exec.Command("git", "-C", mod, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	src, err := l.v.path(filepath.FromSlash(strings.TrimSpace(string(common))))
	if err != nil {
		return "", err
	}
	co := l.v.join(dir, "agent-plugin")
	if _, err := l.v.must("git", "-c", "safe.directory=*", "clone", "--quiet", "--no-checkout", "--no-hardlinks", src, co); err != nil {
		return "", err
	}
	if _, err := l.v.must("git", "-C", co, "-c", "advice.detachedHead=false", "checkout", "--quiet", strings.TrimSpace(string(head))); err != nil {
		return "", err
	}
	return co, l.warm(co)
}

// provideRG finds the variant's own rg, or fetches the pinned release, checks
// its digest and puts it first on the run's PATH. rg is the comparison only: the
// scenario test puts a decoy rg ahead of it before the harness searches run.
func (l *s87Leg) provideRG() error {
	if p := l.v.lookPath("rg"); p != "" {
		l.rg = p + " (the variant's own)"
		return nil
	}
	rel, ok := s87RG[l.v.goos+"/"+l.v.goarch]
	if !ok {
		return fmt.Errorf("service:filesystem: rg is not on the PATH of the variant and no pinned release serves %s/%s", l.v.goos, l.v.goarch)
	}
	dir, err := l.v.tempDir("dsh-s87-rg-")
	if err != nil {
		return err
	}
	bin := l.v.join(dir, "bin")
	if l.v.goos == "windows" {
		if err := s87FetchWindowsRG(rel[0], rel[1], bin); err != nil {
			return err
		}
		l.path = bin + string(os.PathListSeparator) + os.Getenv("PATH")
	} else {
		script := `set -e; cd "$1"; curl -fsSL --retry 3 -o rg.tgz "$2"; echo "$3  rg.tgz" | sha256sum -c - >/dev/null; tar -xzf rg.tgz; mkdir -p bin; mv ripgrep-*/rg bin/rg; ./bin/rg --version | head -n 1`
		ver, err := l.v.must("sh", "-c", script, "sh", dir, rel[0], rel[1])
		if err != nil {
			return fmt.Errorf("service:filesystem: fetching the comparison rg: %v", err)
		}
		cur, err := l.v.must("sh", "-c", `printf '%s' "$PATH"`)
		if err != nil {
			return err
		}
		l.path = bin + ":" + cur
		s87OuterT.Logf("s87 %s: %s", l.v, ver)
	}
	l.rg = l.v.join(bin, "rg") + " (pinned " + rel[0] + ", sha256 " + rel[1] + ")"
	return nil
}

func s87FetchWindowsRG(url, digest, bin string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("service:filesystem: fetching the comparison rg: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("service:filesystem: fetching the comparison rg: %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != digest {
		return fmt.Errorf("service:filesystem: %s has sha256 %x, want %s", url, sum, digest)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		if filepath.Base(f.Name) != "rg.exe" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(bin, 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(bin, "rg.exe"), b, 0o700)
	}
	return fmt.Errorf("%s holds no rg.exe", url)
}

// ---- watch provisioning ----

func (s *s87State) givenEditorVariants() error {
	s.ids = []string{"DH-E-CHOKIDAR-1", "DH-E-CHOKIDAR-2"}
	s.tests = []string{"TestWatchRealEditorSaves", "TestWatchRemoteFilesystemsPoll"}
	if err := s.legsFor("DSH_SCENARIO_EDITORS", "DSH_SCENARIO_REMOTE_DIRS", "DSH_SCENARIO_REMOTE_CHANGE"); err != nil {
		return err
	}
	return s.each(func(l *s87Leg) error {
		if l.inputs["DSH_SCENARIO_EDITORS"] != "" {
			return nil
		}
		editors, err := l.editors()
		if err != nil {
			return err
		}
		l.set("DSH_SCENARIO_EDITORS", strings.Join(editors, ";"))
		return nil
	})
}

// editors are the save strategies of the ephemeral instance: vim (when the
// variant has it), write a copy then rename it over the file, append in place,
// and back up then rewrite in place. Each saves {file} with {marker} appended.
func (l *s87Leg) editors() ([]string, error) {
	if l.v.goos == "windows" {
		var out []string
		vim := l.v.lookPath("vim")
		if vim == "" {
			if p := filepath.Join(os.Getenv("ProgramFiles"), "Git", "usr", "bin", "vim.exe"); s87IsFile(p) {
				vim = p
			}
		}
		if vim != "" {
			out = append(out, `"`+vim+`" -u NONE -i NONE -n -Es -c "normal Go{marker}" -c wq "{file}"`)
		}
		// The redirection comes first: a digit just before > would name a handle.
		return append(out,
			`copy /y "{file}" "{file}.dsh-tmp" >nul && >>"{file}.dsh-tmp" echo {marker}&& move /y "{file}.dsh-tmp" "{file}" >nul`,
			`>>"{file}" echo {marker}`,
			`copy /y "{file}" "{file}~" >nul && (type "{file}~" & echo {marker})>"{file}"`,
		), nil
	}
	dir, err := l.v.tempDir("dsh-s87-editors-")
	if err != nil {
		return nil, err
	}
	scripts := map[string]string{
		"rename":  `cp "$1" "$1.dsh-tmp" && printf '%s\n' "$2" >> "$1.dsh-tmp" && mv -f "$1.dsh-tmp" "$1"`,
		"inplace": `printf '%s\n' "$2" >> "$1"`,
		"backup":  `cp "$1" "$1~" && { cat "$1~"; printf '%s\n' "$2"; } > "$1"`,
	}
	order := []string{"rename", "inplace", "backup"}
	if l.v.lookPath("vim") != "" {
		scripts["vim"] = `exec vim -u NONE -i NONE -n -Es -c "normal Go$2" -c wq "$1"`
		order = append([]string{"vim"}, order...)
	}
	var out []string
	for _, name := range order {
		p := l.v.join(dir, "save-"+name+".sh")
		if err := l.v.writeFile(p, []byte("#!/bin/sh\nset -e\n"+scripts[name]+"\n")); err != nil {
			return nil, err
		}
		out = append(out, "sh "+p+" {file} {marker}")
	}
	return out, nil
}

func s87IsFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// ---- remote directories: an NFS export and an SMB share stood up on the Linux variant ----

// s87Shares is the suite's ephemeral NFS and SMB service, served by the Linux
// variant's own kernel NFS server and a private smbd (its own configuration,
// state, port and account password, none of the system's), torn down at the end.
// The Linux variant mounts both over loopback; the Windows variant maps the SMB
// share through its SMB client on the share's port. A change "from the other
// side" is written to the exported directory itself, beside the client.
type s87Shares struct {
	v                   *s87Variant
	base, user, ip      string
	port                int
	password            string
	nfsExport, nfsMount string
	smbShare, smbMount  string
}

var (
	s87SharesMu  sync.Mutex
	s87SharesUp  *s87Shares
	s87SharesErr error
)

const s87SharesUpSh = `set -eu
base=$1; user=$2; port=$3
IFS= read -r pw
uid=$(id -u "$user"); gid=$(id -g "$user")
pkgs=""
command -v exportfs >/dev/null 2>&1 && command -v mount.nfs >/dev/null 2>&1 || pkgs="$pkgs nfs-kernel-server"
command -v smbd >/dev/null 2>&1 && command -v smbpasswd >/dev/null 2>&1 || pkgs="$pkgs samba"
command -v mount.cifs >/dev/null 2>&1 || pkgs="$pkgs cifs-utils"
if [ -n "$pkgs" ]; then
  command -v apt-get >/dev/null 2>&1 || { echo "service:filesystem: missing$pkgs and no apt-get to install them" >&2; exit 3; }
  export DEBIAN_FRONTEND=noninteractive
  apt-get -qq update >/dev/null
  apt-get -qq install -y --no-install-recommends $pkgs >/dev/null
  # The suite runs its own smbd and starts nfs-server when it needs it; the
  # system services the packages just installed are not left running.
  case "$pkgs" in *samba*) systemctl disable --now smbd nmbd >/dev/null 2>&1 || true ;; esac
  case "$pkgs" in *nfs-kernel-server*) systemctl disable --now nfs-server >/dev/null 2>&1 || true ;; esac
fi
chmod 755 "$base"
mkdir -p "$base/nfs-export" "$base/nfs" "$base/smb-share" "$base/smb" "$base/state"
chown "$uid:$gid" "$base/nfs-export" "$base/smb-share"
chmod 700 "$base/state"
if ! systemctl is-active --quiet nfs-server; then
  systemctl start nfs-server
  : > "$base/state/started-nfs-server"
fi
exportfs -o rw,sync,no_subtree_check,insecure,all_squash,anonuid=$uid,anongid=$gid "127.0.0.1:$base/nfs-export"
: > "$base/state/exported"
# Mount options follow evidence/runbook-smb-share.md: NFS defaults, CIFS vers=3.1.1.
mount -t nfs -o vers=4.2 "127.0.0.1:$base/nfs-export" "$base/nfs"
gw=$(ip -4 route show default 2>/dev/null | awk '{print $3; exit}')
s="$base/state"
cat > "$s/smb.conf" <<EOF
[global]
smb ports = $port
disable netbios = yes
server role = standalone server
passdb backend = tdbsam:$s/passdb.tdb
private dir = $s
lock directory = $s
state directory = $s
cache directory = $s
pid directory = $s
log file = $s/log.%m
map to guest = never
hosts allow = 127.0.0.1 $gw
server min protocol = SMB2_10
[dsh-s87]
path = $base/smb-share
read only = no
valid users = $user
EOF
printf '%s\n%s\n' "$pw" "$pw" | smbpasswd -c "$s/smb.conf" -s -a "$user" >/dev/null
smbd -D -s "$s/smb.conf"
i=0
until ss -ltn | grep -q ":$port "; do
  i=$((i+1)); [ $i -lt 100 ] || { echo "smbd did not listen on $port" >&2; cat "$s"/log.* >&2 || true; exit 4; }
  sleep 0.1
done
printf 'username=%s\npassword=%s\n' "$user" "$pw" > "$s/cifs.cred"
chmod 600 "$s/cifs.cred"
mount -t cifs -o "port=$port,credentials=$s/cifs.cred,uid=$uid,gid=$gid,vers=3.1.1" //127.0.0.1/dsh-s87 "$base/smb"
echo "ip=$(hostname -I | awk '{print $1}')"
`

const s87SharesDownSh = `base=$1
umount "$base/smb" 2>/dev/null || umount -l "$base/smb" 2>/dev/null
umount "$base/nfs" 2>/dev/null || umount -l "$base/nfs" 2>/dev/null
[ -f "$base/state/exported" ] && exportfs -u "127.0.0.1:$base/nfs-export"
[ -f "$base/state/smbd.pid" ] && kill "$(cat "$base/state/smbd.pid")" 2>/dev/null
[ -f "$base/state/started-nfs-server" ] && systemctl stop nfs-server
rm -rf "$base"
`

// s87ProvideShares stands the shares up once per suite on the Linux variant.
func s87ProvideShares() (*s87Shares, error) {
	s87SharesMu.Lock()
	defer s87SharesMu.Unlock()
	if s87SharesUp != nil || s87SharesErr != nil {
		return s87SharesUp, s87SharesErr
	}
	s87SharesUp, s87SharesErr = s87StartShares()
	return s87SharesUp, s87SharesErr
}

func s87StartShares() (*s87Shares, error) {
	v, err := s87Reach("linux")
	if err != nil {
		return nil, fmt.Errorf("service:filesystem: the NFS and SMB service runs on the Linux variant: %v", err)
	}
	user, err := v.must("id", "-un")
	if err != nil {
		return nil, err
	}
	pw := make([]byte, 18)
	if _, err := rand.Read(pw); err != nil {
		return nil, err
	}
	port, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return nil, err
	}
	sh := &s87Shares{v: v, user: user, port: 40000 + int(port.Int64()), password: base64.RawURLEncoding.EncodeToString(pw)}
	scripts, err := v.tempDir("dsh-s87-shares-sh-")
	if err != nil {
		return nil, err
	}
	up, down := v.join(scripts, "up.sh"), v.join(scripts, "down.sh")
	if err := errors.Join(v.writeFile(up, []byte(s87SharesUpSh)), v.writeFile(down, []byte(s87SharesDownSh))); err != nil {
		return nil, err
	}
	if sh.base, err = v.asRoot(nil, "mktemp", "-d", "/tmp/dsh-s87-shares-XXXXXX"); err != nil {
		return nil, fmt.Errorf("service:filesystem: root on the Linux variant for the NFS and SMB service: %w", err)
	}
	// Registered before the shares exist, so a half-made service is torn down too.
	s87OuterT.Cleanup(func() {
		if out, err := v.asRoot(nil, "sh", down, sh.base); err != nil {
			s87OuterT.Logf("s87 tearing the shares down: %v %s", err, out)
		}
	})
	out, err := v.asRoot([]byte(sh.password+"\n"), "sh", up, sh.base, user, fmt.Sprint(sh.port))
	if err != nil {
		return nil, fmt.Errorf("service:filesystem: standing up the NFS export and the SMB share: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if ip, ok := strings.CutPrefix(strings.TrimSpace(line), "ip="); ok {
			sh.ip = ip
		}
	}
	sh.nfsExport, sh.nfsMount = v.join(sh.base, "nfs-export"), v.join(sh.base, "nfs")
	sh.smbShare, sh.smbMount = v.join(sh.base, "smb-share"), v.join(sh.base, "smb")
	return sh, nil
}

func (s *s87State) givenRemoteDirs() error {
	return s.each(func(l *s87Leg) error {
		if l.inputs["DSH_SCENARIO_REMOTE_DIRS"] != "" && l.inputs["DSH_SCENARIO_REMOTE_CHANGE"] != "" {
			for _, d := range strings.Split(l.inputs["DSH_SCENARIO_REMOTE_DIRS"], ";") {
				if d = strings.TrimSpace(d); d != "" {
					l.volumes = append(l.volumes, "the operator's "+d)
				}
			}
			return nil
		}
		if len(l.op) > 0 && (l.op["DSH_SCENARIO_REMOTE_DIRS"] != "" || l.op["DSH_SCENARIO_REMOTE_CHANGE"] != "") {
			return errors.New("DSH_SCENARIO_REMOTE_DIRS and DSH_SCENARIO_REMOTE_CHANGE go together: set both, or neither for the ephemeral instance")
		}
		sh, err := s87ProvideShares()
		if err != nil {
			return err
		}
		if l.v.goos == "windows" {
			return l.remoteWindows(sh)
		}
		return l.remoteLinux(sh)
	})
}

// s87ChangePS changes {name} in {dir} from the other side of the directory: the
// exported directory behind the SMB share, or the WSL distribution behind
// \\wsl.localhost, both written by the Linux variant itself. The change appends
// the marker, so the size changes.
const s87ChangePS = `param([string]$Dir, [string]$Name, [string]$Marker)
$ErrorActionPreference = 'Stop'
$distro = '@@DISTRO@@'
$unc = '@@UNC@@'
$p = $null
if ($Dir.StartsWith($unc, [System.StringComparison]::OrdinalIgnoreCase)) {
  $p = '@@SHARE@@' + ($Dir.Substring($unc.Length) -replace '\\', '/')
} elseif ($Dir -match '^\\\\wsl(?:\.localhost|\$)\\([^\\]+)(\\.*)$') {
  $distro = $Matches[1]
  $p = $Matches[2] -replace '\\', '/'
}
if ($p -eq $null) {
  Write-Error "no other side is known for $Dir"
  exit 3
}
& wsl.exe -d $distro -e sh -c 'printf %s $1 >> $2' sh $Marker ($p + '/' + $Name)
exit $LASTEXITCODE
`

// remoteWindows gives the Windows variant a directory on the suite's SMB share,
// mapped through Windows' SMB client, and a directory of the WSL distribution
// seen from Windows.
func (l *s87Leg) remoteWindows(sh *s87Shares) error {
	if !sh.v.wsl {
		return errors.New("service:filesystem: the Windows variant reaches the shares through WSL")
	}
	unc := `\\` + sh.ip + `\dsh-s87`
	env := []string{"S87_SMB_PATH=" + unc, "S87_SMB_PORT=" + fmt.Sprint(sh.port), "S87_SMB_USER=" + sh.user, "S87_SMB_PASSWORD=" + sh.password}
	ps := func(script string) (string, error) {
		out, code, err := l.v.run(context.Background(), "", env, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
		if err == nil && code != 0 {
			err = fmt.Errorf("exit %d", code)
		}
		return out, err
	}
	if out, err := ps(`$ErrorActionPreference='Stop'; New-SmbMapping -RemotePath $env:S87_SMB_PATH -TcpPort ([int]$env:S87_SMB_PORT) -UserName $env:S87_SMB_USER -Password $env:S87_SMB_PASSWORD -Persistent $false | Out-Null`); err != nil {
		return fmt.Errorf("service:filesystem: mapping the SMB share %s (port %d) on Windows: %v %s", unc, sh.port, err, out)
	}
	s87OuterT.Cleanup(func() {
		_, _ = ps(`Remove-SmbMapping -RemotePath $env:S87_SMB_PATH -Force -ErrorAction SilentlyContinue`)
	})
	name := fmt.Sprintf("windows-%s", s87RunID)
	smbDir := unc + `\` + name
	if err := os.Mkdir(smbDir, 0o755); err != nil {
		return fmt.Errorf("service:filesystem: the SMB share %s: %v", unc, err)
	}
	dirs := []string{smbDir}
	l.volumes = append(l.volumes, fmt.Sprintf("SMB %s (smbd on %s port %d, exported %s)", smbDir, sh.v, sh.port, sh.v.join(sh.smbShare, name)))
	l.remote = append(l.remote, "smb")

	d, err := sh.v.tempDir("dsh-s87-crossed-")
	if err != nil {
		return err
	}
	var seen string
	for _, root := range []string{`\\wsl.localhost\`, `\\wsl$\`} {
		if p := root + sh.v.distro + strings.ReplaceAll(d, "/", `\`); s87IsDir(p) {
			seen = p
			break
		}
	}
	if seen == "" {
		return fmt.Errorf("service:filesystem: the WSL directory %s is not reachable from Windows through \\\\wsl.localhost or \\\\wsl$", d)
	}
	dirs = append(dirs, seen)
	l.volumes = append(l.volumes, "WSL "+seen)
	l.remote = append(l.remote, "cross-os")

	scratch, err := l.v.tempDir("dsh-s87-change-")
	if err != nil {
		return err
	}
	script := filepath.Join(scratch, "change.ps1")
	q := func(s string) string { return strings.ReplaceAll(s, "'", "''") }
	body := strings.NewReplacer("@@DISTRO@@", q(sh.v.distro), "@@UNC@@", q(unc), "@@SHARE@@", q(sh.smbShare)).Replace(s87ChangePS)
	if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
		return err
	}
	l.set("DSH_SCENARIO_REMOTE_DIRS", strings.Join(dirs, ";"))
	l.set("DSH_SCENARIO_REMOTE_CHANGE", `powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "`+script+`" "{dir}" "{name}" "{marker}"`)
	return nil
}

func s87IsDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// remoteLinux gives the Linux variant a directory on the NFS mount and one on
// the SMB mount of the suite's shares, each changed in the exported directory
// behind it, and under WSL a directory of this Windows host seen from WSL
// (drvfs, 9p), changed from Windows through WSL interop.
func (l *s87Leg) remoteLinux(sh *s87Shares) error {
	name := "linux-" + s87RunID
	var dirs []string
	for _, m := range [][3]string{{"nfs", sh.nfsMount, sh.nfsExport}, {"smb", sh.smbMount, sh.smbShare}} {
		dir := l.v.join(m[1], name)
		if _, err := l.v.must("mkdir", dir); err != nil {
			return fmt.Errorf("service:filesystem: the %s mount %s: %v", m[0], m[1], err)
		}
		fs, err := l.v.fsType(dir)
		if err != nil {
			return err
		}
		dirs = append(dirs, dir)
		l.volumes = append(l.volumes, fmt.Sprintf("%s %s (exported %s)", fs, dir, l.v.join(m[2], name)))
		l.remote = append(l.remote, m[0])
	}
	scratch, err := l.v.tempDir("dsh-s87-change-")
	if err != nil {
		return err
	}
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	body := "#!/bin/sh\nset -e\nd=$1 n=$2 m=$3\ncase \"$d\" in\n" +
		q(sh.nfsMount) + "/*) p=" + q(sh.nfsExport) + "\"${d#" + q(sh.nfsMount) + "}\" ;;\n" +
		q(sh.smbMount) + "/*) p=" + q(sh.smbShare) + "\"${d#" + q(sh.smbMount) + "}\" ;;\n"
	if l.v.wsl {
		host, err := os.MkdirTemp("", "dsh-s87-crossed-")
		if err != nil {
			return err
		}
		s87OuterT.Cleanup(func() { _ = os.RemoveAll(host) })
		dir, err := l.v.path(host)
		if err != nil {
			return err
		}
		fs, err := l.v.fsType(dir)
		if err != nil {
			return err
		}
		dirs = append(dirs, dir)
		l.volumes = append(l.volumes, fs+" "+dir)
		l.remote = append(l.remote, "cross-os")
		ps, err := l.v.path(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"))
		if err != nil {
			return err
		}
		body += "/mnt/*) w=$(wslpath -w \"$d/$n\")\n  exec " + q(ps) + " -NoProfile -NonInteractive -Command \"[System.IO.File]::AppendAllText('$w', '$m')\" ;;\n"
	}
	body += "*) echo \"no other side is known for $d\" >&2; exit 3 ;;\nesac\nprintf %s \"$m\" >> \"$p/$n\"\n"
	script := l.v.join(scratch, "change.sh")
	if err := l.v.writeFile(script, []byte(body)); err != nil {
		return err
	}
	l.set("DSH_SCENARIO_REMOTE_DIRS", strings.Join(dirs, ";"))
	l.set("DSH_SCENARIO_REMOTE_CHANGE", "sh "+script+" {dir} {name} {marker}")
	return nil
}

// ---- the run ----

func (s *s87State) whenSearch(budget string) error {
	d, err := time.ParseDuration(budget)
	if err != nil {
		return err
	}
	s.budget = d
	for _, l := range s.legs {
		l.set("DSH_SCENARIO_SEARCH_BUDGET", budget)
		if b, err := time.ParseDuration(l.inputs["DSH_SCENARIO_SEARCH_BUDGET"]); err == nil && b != d {
			s87OuterT.Logf("s87 %s: the operator's search budget %v replaces %v", l.v, b, d)
		}
	}
	return s.run()
}

// run builds each variant's binary, then runs the scenario tests on every leg at
// once, each on its own variant's volumes, so the very large checkout's
// searches on both variants fit within go test's default ten-minute deadline;
// every leg runs even when one fails.
func (s *s87State) run() error {
	if len(s.legs) == 0 {
		return errors.New("no variant was reached")
	}
	for _, l := range s.legs {
		if _, err := l.v.build(); err != nil {
			return err
		}
	}
	var wg sync.WaitGroup
	for _, l := range s.legs {
		wg.Go(func() { l.ran = s.runLeg(l) })
	}
	wg.Wait()
	var errs []error
	for _, l := range s.legs {
		if l.ran != nil {
			errs = append(errs, l.ran)
		}
		if err := s.persist(l); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *s87State) runLeg(l *s87Leg) error {
	bin, err := l.v.build()
	if err != nil {
		return err
	}
	pkg, err := l.v.pkgDir()
	if err != nil {
		return err
	}
	dir, err := l.v.tempDir("dsh-s87-report-")
	if err != nil {
		return err
	}
	l.report = l.v.join(dir, "report.jsonl")
	env := []string{"DSH_SCENARIO_REPORT=" + l.report, "DSH_REQUIRE_SCENARIOS=" + strings.Join(s.ids, ",")}
	if l.path != "" {
		env = append(env, "PATH="+l.path)
	}
	for _, k := range slices.Sorted(maps.Keys(l.inputs)) {
		env = append(env, k+"="+l.inputs[k])
	}
	ctx, cancel := context.WithTimeout(context.Background(), s87RunWithin)
	defer cancel()
	out, code, err := l.v.run(ctx, pkg, env, bin, "-test.run", "^("+strings.Join(s.tests, "|")+")$", "-test.v", "-test.count=1", "-test.timeout", s87RunWithin.String())
	if err != nil {
		return fmt.Errorf("%v on %s: %v", s.tests, l.v, err)
	}
	l.log, l.code = out, code
	if f, ferr := os.CreateTemp("", "dsh-s87-"+l.v.goos+"-*.log"); ferr == nil {
		_, _ = f.WriteString(out)
		_ = f.Close()
		l.logPath = f.Name()
		s87OuterT.Logf("s87 %v on %s (%s): exit %d, log kept at %s", s.ids, l.v, l.lane(), code, f.Name())
	}
	l.rows = map[string]map[string]any{}
	data, rerr := l.v.readFile(l.report)
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var row map[string]any
		if json.Unmarshal(sc.Bytes(), &row) == nil && row["kind"] == "scenario" {
			if id, _ := row["id"].(string); slices.Contains(s.ids, id) {
				l.rows[id] = row
			}
		}
	}
	l.detail, _ = l.v.readFile(l.report + ".detail")
	var bad []string
	for _, id := range s.ids {
		if l.rows[id]["result"] != "passed" {
			bad = append(bad, fmt.Sprintf("%s row %v", id, l.rows[id]))
		}
	}
	if code != 0 || len(bad) > 0 {
		return fmt.Errorf("%v on %s (%s): exit %d, %s (report %v)\n...\n%s", s.tests, l.v, l.lane(), code,
			strings.Join(bad, "; "), rerr, out[max(0, len(out)-4000):])
	}
	return nil
}

// ---- evidence ----

var s87RunID string

type s87Evidence struct {
	Kind       string `json:"kind"`
	Run        string `json:"run"`
	ID         string `json:"id"`
	OS         string `json:"os"`
	Variant    string `json:"variant"`
	Lane       string `json:"lane"`
	Result     string `json:"result"`
	Reason     string `json:"reason,omitempty"`
	DurationMS any    `json:"durationMs,omitempty"`
	Volumes    string `json:"volumes,omitempty"`
	RG         string `json:"rg,omitempty"`
	Report     string `json:"report,omitempty"`
	Log        string `json:"log,omitempty"`
	At         string `json:"at"`
}

func s87EvidenceDir() string {
	if d := strings.TrimSpace(os.Getenv("DSH_S18_EVIDENCE_DIR")); d != "" {
		return d
	}
	return filepath.Join(os.TempDir(), "dsh-s18-evidence")
}

// persist appends one row per scenario ID of the leg to <dir>/<id>-<os>.jsonl.
func (s *s87State) persist(l *s87Leg) error {
	dir := s87EvidenceDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, id := range s.ids {
		e := s87Evidence{Kind: "s18-leg", Run: s87RunID, ID: id, OS: l.v.goos, Variant: l.v.String(), Lane: l.lane(),
			Volumes: strings.Join(l.volumes, "; "), RG: l.rg, Report: l.report, Log: l.logPath, At: time.Now().UTC().Format(time.RFC3339)}
		if row := l.rows[id]; row != nil {
			e.Result, _ = row["result"].(string)
			e.Reason, _ = row["reason"].(string)
			e.DurationMS = row["durationMs"]
		} else {
			e.Result = "not run"
			if l.ran != nil {
				e.Reason = l.ran.Error()
			}
		}
		p := filepath.Join(dir, strings.ToLower(id)+"-"+l.v.goos+".jsonl")
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		b, _ := json.Marshal(e)
		_, err = f.Write(append(b, '\n'))
		if err = errors.Join(err, f.Close()); err != nil {
			return err
		}
		l.evidence = append(l.evidence, p)
	}
	return nil
}

// ---- Then ----

func (s *s87State) thenRows(a, b string) error {
	return s.each(func(l *s87Leg) error {
		if l.ran != nil {
			return fmt.Errorf("did not pass: %v", l.ran)
		}
		for _, id := range []string{a, b} {
			row := l.rows[id]
			if row == nil || row["result"] != "passed" || row["os"] != l.v.goos {
				return fmt.Errorf("%s row %v, want passed on %s", id, row, l.v.goos)
			}
		}
		for _, p := range l.evidence {
			if _, err := os.Stat(p); err != nil {
				return fmt.Errorf("the evidence row %s was not kept: %v", p, err)
			}
		}
		return nil
	})
}

// logs requires every leg's run to have passed the named tests and its log to
// hold none of the failure lines of bad.
func (s *s87State) logs(tests []string, bad *regexp.Regexp, check func(l *s87Leg) error) error {
	return s.each(func(l *s87Leg) error {
		if l.ran != nil {
			return fmt.Errorf("did not pass: %v", l.ran)
		}
		for _, t := range tests {
			if !strings.Contains(l.log, "--- PASS: "+t+" ") {
				return fmt.Errorf("%s did not pass", t)
			}
		}
		if bad != nil {
			if m := bad.FindString(l.log); m != "" {
				return fmt.Errorf("the log reports %q", m)
			}
		}
		if check != nil {
			return check(l)
		}
		return nil
	})
}

type s87Timing struct {
	Repo       string   `json:"repo"`
	Pattern    string   `json:"pattern"`
	BudgetMS   int64    `json:"budget_ms"`
	FirstMS    int64    `json:"first_ms"`
	FirstLines int      `json:"first_lines"`
	HarnessMS  int64    `json:"harness_ms"`
	RGMS       *int64   `json:"rg_ms"`
	Ratio      *float64 `json:"ratio"`
	Files      int      `json:"files"`
	RGFiles    *int     `json:"rg_files"`
	TimedOut   bool     `json:"timed_out"`
}

func (s *s87State) thenTimings() error {
	return s.logs([]string{"TestSearchMeetsBudget"}, nil, func(l *s87Leg) error {
		budget := s.budget
		if b, err := time.ParseDuration(l.inputs["DSH_SCENARIO_SEARCH_BUDGET"]); err == nil {
			budget = b
		}
		perRepo := map[string]map[string]bool{}
		sc := bufio.NewScanner(bytes.NewReader(l.detail))
		for sc.Scan() {
			var r s87Timing
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				return fmt.Errorf("timing report line %q: %v", sc.Text(), err)
			}
			switch {
			case r.TimedOut:
				return fmt.Errorf("%s %q: the whole search timed out", r.Repo, r.Pattern)
			case r.BudgetMS != budget.Milliseconds():
				return fmt.Errorf("%s %q: recorded budget %d ms, want %d ms", r.Repo, r.Pattern, r.BudgetMS, budget.Milliseconds())
			case r.FirstMS > r.BudgetMS:
				return fmt.Errorf("%s %q: the first %d lines took %d ms, over the budget %d ms", r.Repo, r.Pattern, r.FirstLines, r.FirstMS, r.BudgetMS)
			case r.RGMS == nil || r.Ratio == nil || r.RGFiles == nil:
				return fmt.Errorf("%s %q: no rg time, ratio or file count is recorded", r.Repo, r.Pattern)
			case r.Files != *r.RGFiles:
				return fmt.Errorf("%s %q: %d files, rg %d", r.Repo, r.Pattern, r.Files, *r.RGFiles)
			}
			if perRepo[r.Repo] == nil {
				perRepo[r.Repo] = map[string]bool{}
			}
			perRepo[r.Repo][r.Pattern] = true
		}
		var repos []string
		for _, r := range strings.Split(l.inputs["DSH_SCENARIO_REPOS"], ";") {
			if r = strings.TrimSpace(r); r != "" {
				repos = append(repos, r)
			}
		}
		for _, r := range repos {
			if n := len(perRepo[r]); n != s87Searches {
				return fmt.Errorf("the timing report has %d of the %d searches for %s", n, s87Searches, r)
			}
		}
		if len(perRepo) != len(repos) {
			return fmt.Errorf("the timing report covers %d repositories, want %d", len(perRepo), len(repos))
		}
		return nil
	})
}

func (s *s87State) thenListing() error {
	return s.logs([]string{"TestFileListingMatchesGit"}, regexp.MustCompile(`unexplained differences[^\r\n]*`), nil)
}

func (s *s87State) thenNoRG() error {
	return s.logs([]string{"TestSearchMeetsBudget"}, regexp.MustCompile(`an rg was started[^\r\n]*|the decoy rg is not first on PATH[^\r\n]*`), nil)
}

func (s *s87State) thenSaves(window string) error {
	if window != "1s" {
		return fmt.Errorf("the save window of DH-E-CHOKIDAR-1 is 1s, not %s", window)
	}
	return s.logs([]string{"TestWatchRealEditorSaves"},
		regexp.MustCompile(`editor \d+ \([^\r\n]*(no report within|after the save, want within|one save reported)[^\r\n]*|content read back lacks[^\r\n]*`),
		func(l *s87Leg) error {
			for _, role := range []string{"credentials watch in ", "workspace watch in "} {
				if !strings.Contains(l.log, role) {
					return fmt.Errorf("no %q line: the %s file was not watched", role, strings.Fields(role)[0])
				}
			}
			return nil
		})
}

var s87Transitions = regexp.MustCompile(`: (create|burst|rename|delete): [^\r\n]*`)

func (s *s87State) thenTransitions() error {
	return s.logs([]string{"TestWatchRealEditorSaves", "TestWatchRemoteFilesystemsPoll"}, s87Transitions, func(l *s87Leg) error {
		if !strings.Contains(l.log, "workspace tree watch in ") {
			return errors.New("the workspace tree's transitions were not checked")
		}
		return nil
	})
}

func (s *s87State) thenPoll() error {
	return s.logs([]string{"TestWatchRemoteFilesystemsPoll"}, regexp.MustCompile(`mode, want poll[^\r\n]*`), func(l *s87Leg) error {
		if len(l.volumes) == 0 {
			return errors.New("no network or cross-OS directory was watched")
		}
		if _, ok := l.op["DSH_SCENARIO_REMOTE_DIRS"]; ok {
			return nil
		}
		want := []string{"smb", "cross-os"}
		if l.v.goos == "linux" {
			want = []string{"nfs", "smb"}
			if l.v.wsl {
				want = append(want, "cross-os")
			}
		}
		for _, k := range want {
			if !slices.Contains(l.remote, k) {
				return fmt.Errorf("no %s directory was watched (watched %v)", k, l.volumes)
			}
		}
		return nil
	})
}

// thenSizes requires the searched checkouts to have been a small, a medium and
// a very large one, each with its full search set in the timing report.
func (s *s87State) thenSizes(small, large int) error {
	return s.each(func(l *s87Leg) error {
		if err := s87Sizes(l.files, small, large); err != nil {
			return err
		}
		seen := map[string]int{}
		sc := bufio.NewScanner(bytes.NewReader(l.detail))
		for sc.Scan() {
			var r s87Timing
			if json.Unmarshal(sc.Bytes(), &r) == nil {
				seen[r.Repo]++
			}
		}
		for repo, n := range l.files {
			if seen[repo] != s87Searches {
				return fmt.Errorf("%s (%d files): %d of the %d searches in the timing report", repo, n, seen[repo], s87Searches)
			}
		}
		return nil
	})
}

// ---- registration ----

func InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_search_and_file_watching_on_real_systems(ctx *godog.ScenarioContext) {
	s := &s87State{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = s87State{}
		return c, nil
	})
	ctx.Step(`^the Linux and Windows variants, each with real Git checkouts of a small, a medium and a very large public repository on its own filesystem and rg for comparison only$`, s.givenSearchVariants)
	ctx.Step(`^RIPGREP-1 and RIPGREP-2 run on each variant with the search budget "([^"]*)"$`, s.whenSearch)
	ctx.Step(`^each variant's "([^"]*)" and "([^"]*)" rows read passed on its OS$`, s.thenRows)
	ctx.Step(`^on each variant the searched checkouts were a small one under (\d+) files, a medium one and a very large one of at least (\d+) files$`, s.thenSizes)
	ctx.Step(`^on each variant the timing report has every search of the set, its first matches within the budget and its files equal to rg's$`, s.thenTimings)
	ctx.Step(`^on each variant the file listing equals Git's with no unexplained difference$`, s.thenListing)
	ctx.Step(`^no external rg was spawned on any variant$`, s.thenNoRG)

	ctx.Step(`^the same variants, each with editor saves by vim, rename, in place and backup-then-write$`, s.givenEditorVariants)
	ctx.Step(`^on each variant an SMB share, on Linux also an NFS mount, and a cross-OS directory, each changed from its other side$`, s.givenRemoteDirs)
	ctx.Step(`^CHOKIDAR-1 and CHOKIDAR-2 run on each variant$`, s.run)
	ctx.Step(`^on each variant every save was reported exactly once within "([^"]*)"$`, s.thenSaves)
	ctx.Step(`^on each variant the create, burst, rename and delete of a watched tree were each reported exactly once within the bound$`, s.thenTransitions)
	ctx.Step(`^on each variant every share and cross-OS directory was watched in poll mode$`, s.thenPoll)
}

func TestE2E_dh_s_18_acceptance_on_real_operating_systems_search_and_file_watching_on_real_systems(t *testing.T) {
	s87OuterT = t
	s87RunID = fmt.Sprintf("%d", time.Now().UnixNano())
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_search_and_file_watching_on_real_systems,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_18_acceptance_on_real_operating_systems_search_and_file_watching_on_real_systems.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the search and file watching on real systems scenarios failed")
	}
}
