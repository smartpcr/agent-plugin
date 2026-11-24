//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/fstest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

// ffsSuiteT is the *testing.T of the entrypoint: fstest.Run needs a real
// *testing.T, and t.TempDir gives each scenario a directory removed at the end.
var ffsSuiteT *testing.T

type ffsSubtest struct {
	name            string
	failed, skipped bool
}

type ffsWorld struct {
	// conformance
	newSubject func(t *testing.T) fstest.Subject
	runOK      bool
	mu         sync.Mutex
	subtests   []ffsSubtest
	linkKinds  map[string]int
	linux      ffsLane

	// swap race
	r, o, sub string
	policy    sandbox.Policy
	swaps     int
	linked    int
	swapErrs  int
	writes    map[string]int
	otherErrs []string
	leakedAt  string
	directErr error
	raceKind  string

	// stale version
	file     string
	seam     fs.FS
	staleV1  fs.Version
	v2       []byte
	staleErr error
}

func ffsTempDir() (string, error) {
	if ffsSuiteT == nil {
		return "", errors.New("no suite *testing.T to make a temp dir on")
	}
	return ffsSuiteT.TempDir(), nil
}

// --- Conformance on the real provider ---

func (w *ffsWorld) givenProvider(provider, root string) error {
	if provider != "fs/local" || root != "t.TempDir()" {
		return fmt.Errorf("unexpected provider %q rooted at %q", provider, root)
	}
	w.linkKinds = map[string]int{}
	w.newSubject = func(t *testing.T) fstest.Subject {
		rec := ffsSubtest{}
		t.Cleanup(func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			rec.name, rec.failed, rec.skipped = t.Name(), t.Failed(), t.Skipped()
			w.subtests = append(w.subtests, rec)
		})
		return fstest.Subject{
			FS: local.New(),
			Symlink: func(target, link string) error {
				kind, err := ffsMakeLink(target, link)
				w.mu.Lock()
				defer w.mu.Unlock()
				if err != nil {
					return err
				}
				w.linkKinds[kind]++
				return nil
			},
		}
	}
	return nil
}

func (w *ffsWorld) whenRun(fn string) error {
	if fn != "fstest.Run" {
		return fmt.Errorf("unexpected runner %q", fn)
	}
	if ffsSuiteT == nil {
		return errors.New("no suite *testing.T to run the conformance suite on")
	}
	w.runOK = ffsSuiteT.Run(ffsConformanceName, func(t *testing.T) {
		fstest.Run(t, w.newSubject)
	})
	if ffsWSLLaneWanted() {
		w.linux = ffsRunLinuxLane()
	} else {
		w.linux = ffsLane{err: fmt.Errorf("WSL lane not requested (set %s=1 to run it from this host)", ffsWSLOptInEnv)}
	}
	return nil
}

const (
	ffsConformanceName = "fstest-conformance-fs-local"
	// ffsLinuxLaneEnv marks the Linux-lane child run, so it does not recurse.
	ffsLinuxLaneEnv = "DSH_E2E_FFS_LINUX_LANE"
	// ffsWSLOptInEnv opts a Windows host into also running the Linux lane under
	// WSL. Unset by default (and in CI, where Linux runs natively on its own
	// job), so a wsl.exe without a usable Go distro cannot fail the Windows lane.
	ffsWSLOptInEnv = "DSH_E2E_FFS_WSL"
	ffsEntrypoint  = "TestE2E_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_fenced_filesystem_seam"
)

func ffsWSLLaneWanted() bool {
	return runtime.GOOS == "windows" && os.Getenv(ffsLinuxLaneEnv) == "" && os.Getenv(ffsWSLOptInEnv) == "1"
}

// ffsLane is the outcome of the conformance suite run on the Linux lane from a
// Windows host: the whole godog suite of this stage, run under WSL.
type ffsLane struct {
	// ran is true only once the lane was provisioned (WSL distro with go) and
	// the suite was launched there; ran=false means the lane was not run.
	ran bool
	// provisionErr: the lane was requested but could not be launched or
	// provisioned (no wsl.exe, no distro, no go). Distinct from err.
	provisionErr error
	err          error // the suite ran on the lane and failed, or is not requested
	results      map[string]string
	summary      string
}

var ffsSubtestLine = regexp.MustCompile(`--- (PASS|FAIL|SKIP): \S+/` + ffsConformanceName + `/(\S+)`)

// ffsRunLinuxLane runs this suite under WSL (the Linux lane of a Windows host):
// the same package, built and run by the Linux Go toolchain on a Linux t.TempDir.
func ffsRunLinuxLane() ffsLane {
	wsl, err := exec.LookPath("wsl.exe")
	if err != nil {
		return ffsLane{provisionErr: fmt.Errorf("wsl.exe not found: %v", err)}
	}
	dir, err := os.Getwd()
	if err != nil {
		return ffsLane{provisionErr: err}
	}
	pctx, pcancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer pcancel()
	if out, err := exec.CommandContext(pctx, wsl, "--cd", dir, "--", "bash", "-lc", "go version").CombinedOutput(); err != nil {
		return ffsLane{provisionErr: fmt.Errorf("no WSL distro with a go toolchain at %s: %v\n%s", dir, err, bytes.TrimSpace(out))}
	}
	script := fmt.Sprintf(`%s=1 go test -tags e2e -count=1 -v -run '^%s$' .`, ffsLinuxLaneEnv, ffsEntrypoint)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	out, runErr := exec.CommandContext(ctx, wsl, "--cd", dir, "--", "bash", "-lc", script).CombinedOutput()
	tail := string(out)
	if len(tail) > 4000 {
		tail = tail[len(tail)-4000:]
	}
	// The suite never started (no entrypoint line): module download, build or
	// launch failed, which is provisioning, not a conformance result.
	if !bytes.Contains(out, []byte("=== RUN   "+ffsEntrypoint)) {
		return ffsLane{provisionErr: fmt.Errorf("suite did not start under WSL: %v\n%s", runErr, tail)}
	}
	lane := ffsLane{ran: true, results: map[string]string{}}
	for _, m := range ffsSubtestLine.FindAllStringSubmatch(string(out), -1) {
		lane.results[m[2]] = m[1]
	}
	if m := regexp.MustCompile(`\d+ scenarios \([^)]*\)`).Find(out); m != nil {
		lane.summary = string(m)
	}
	if runErr != nil {
		lane.err = fmt.Errorf("linux lane: %v\n%s", runErr, tail)
	}
	return lane
}

func ffsShort(name string) string {
	if i := strings.Index(name, ffsConformanceName+"/"); i >= 0 {
		return name[i+len(ffsConformanceName)+1:]
	}
	return name
}

// thenEverySubtestPasses: no conformance subtest may fail or skip, on this lane
// or, from a Windows host with DSH_E2E_FFS_WSL=1, on its Linux lane (WSL), which
// must pass as well: one lane never stands in for the other. Without the opt-in
// the Linux lane is reported not run here (CI runs it natively). On Windows
// without the symlink privilege
// the subject makes reparse points fs/local resolves as links (ffsMakeLink), so
// every subtest runs there too; the kinds made are logged.
func (w *ffsWorld) thenEverySubtestPasses() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.subtests) == 0 {
		return errors.New("fstest.Run executed no subtests")
	}
	var bad []string
	for _, s := range w.subtests {
		switch {
		case s.failed:
			bad = append(bad, s.name+" FAILED")
		case s.skipped:
			bad = append(bad, s.name+" SKIPPED")
		}
	}
	ffsSuiteT.Logf("%s lane: fs/local conformance: %d subtests, link kinds made %v", runtime.GOOS, len(w.subtests), w.linkKinds)
	if len(bad) > 0 || !w.runOK {
		return fmt.Errorf("%s lane: conformance run ok=%v; %d of %d subtests not passing: %v", runtime.GOOS, w.runOK, len(bad), len(w.subtests), bad)
	}
	if runtime.GOOS != "windows" || os.Getenv(ffsLinuxLaneEnv) != "" {
		return nil
	}
	l := w.linux
	if l.provisionErr != nil {
		// Requested explicitly via ffsWSLOptInEnv, so not running is an error,
		// but reported as "not run", never as a conformance failure.
		return fmt.Errorf("linux lane (WSL): NOT RUN, the lane could not be launched or provisioned (%s=1 was set): %v", ffsWSLOptInEnv, l.provisionErr)
	}
	if !l.ran {
		ffsSuiteT.Logf("linux lane: not run: %v (it runs natively on the Linux CI lane)", l.err)
		return nil
	}
	if l.err != nil {
		return l.err
	}
	var linuxBad []string
	for _, s := range w.subtests {
		if r := l.results[ffsShort(s.name)]; r != "PASS" {
			linuxBad = append(linuxBad, fmt.Sprintf("%s=%q", ffsShort(s.name), r))
		}
	}
	ffsSuiteT.Logf("linux lane (WSL): %d conformance subtests passed, %s", len(l.results), l.summary)
	if len(linuxBad) > 0 {
		return fmt.Errorf("linux lane: conformance subtests not passing: %v", linuxBad)
	}
	return nil
}

// --- Fence holds through a swapped link ---

func (w *ffsWorld) givenRootAndOutside(r, o, where string) error {
	if where != "t.TempDir()" {
		return fmt.Errorf("unexpected location %q", where)
	}
	tmp, err := ffsTempDir()
	if err != nil {
		return err
	}
	w.r, w.o = filepath.Join(tmp, r), filepath.Join(tmp, o)
	for _, d := range []string{w.r, w.o} {
		if err := os.Mkdir(d, 0o755); err != nil {
			return err
		}
	}
	w.policy = sandboxtest.Policy(sandbox.WorkspaceWrite, w.r)
	return nil
}

func (w *ffsWorld) relUnder(rel string) (string, error) {
	parts := strings.Split(rel, "/")
	if len(parts) < 2 || parts[0] != filepath.Base(w.r) {
		return "", fmt.Errorf("path %q is not under the write root %q", rel, filepath.Base(w.r))
	}
	return filepath.Join(append([]string{w.r}, parts[1:]...)...), nil
}

func (w *ffsWorld) whenSwapRace(subRel, o string, swaps int, fileRel string) error {
	if o != filepath.Base(w.o) {
		return fmt.Errorf("unexpected outside directory %q", o)
	}
	sub, err := w.relUnder(subRel)
	if err != nil {
		return err
	}
	file, err := w.relUnder(fileRel)
	if err != nil {
		return err
	}
	if filepath.Dir(file) != sub {
		return fmt.Errorf("%q is not directly under %q", fileRel, subRel)
	}
	w.sub, w.swaps = sub, swaps
	if err := os.Mkdir(sub, 0o755); err != nil {
		return err
	}
	probe := filepath.Join(filepath.Dir(w.r), "probe")
	kind, err := ffsMakeLink(w.o, probe)
	if err != nil {
		return fmt.Errorf("cannot make a directory link on this lane: %v", err)
	}
	_ = os.Remove(probe)
	w.raceKind = kind

	ctx := context.Background()
	seam := local.New()
	keep := sub + ".keep"
	var done atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	var panicked error
	go func() {
		defer wg.Done()
		defer done.Store(true)
		for attempt := 0; w.linked < swaps && attempt < 500*swaps; attempt++ {
			if err := os.Rename(sub, keep); err != nil {
				w.swapErrs++
				continue
			}
			if _, err := ffsMakeLink(w.o, sub); err != nil {
				w.swapErrs++
				ffsRestore(sub, keep)
				continue
			}
			w.linked++
			// os.Remove removes the link itself, never O's content.
			for j := 0; os.Remove(sub) != nil; j++ {
				if j == 10000 {
					panicked = errors.New("cannot remove the link " + sub)
					return
				}
			}
			ffsRestore(sub, keep)
		}
	}()

	w.writes = map[string]int{}
	target := fs.Ref{Path: file}
	for i := 0; !done.Load() || i == 0; i++ {
		_, err := seam.Write(ctx, target, []byte("x"), fs.NoGuard, w.policy)
		switch code := herr.Kind(err); {
		case err == nil:
			w.writes["written"]++
		case code == fs.CodeSandboxDenied:
			w.writes["sandbox-denied"]++
		default:
			w.otherErrs = append(w.otherErrs, fmt.Sprintf("write %d: %v (code %q)", i, err, code))
		}
		if w.leakedAt == "" {
			if names, err := ffsEntries(w.o); err != nil {
				return err
			} else if len(names) > 0 {
				w.leakedAt = fmt.Sprintf("write %d: %v", i, names)
			}
		}
	}
	wg.Wait()
	if panicked != nil {
		return panicked
	}
	ffsSuiteT.Logf("swap race (%s): swaps=%d linked=%d swapErrors=%d writes=%v", w.raceKind, swaps, w.linked, w.swapErrs, w.writes)
	if w.linked < swaps {
		return fmt.Errorf("the swapper made only %d of %d links; the race was not fully exercised", w.linked, swaps)
	}

	// With R/sub left as a link to O and no race, the write is refused outright.
	if err := os.RemoveAll(sub); err != nil {
		return err
	}
	if _, err := ffsMakeLink(w.o, sub); err != nil {
		return err
	}
	_, w.directErr = seam.Write(ctx, target, []byte("x"), fs.NoGuard, w.policy)
	if err := os.Remove(sub); err != nil {
		return fmt.Errorf("remove link %s: %v", sub, err)
	}
	return nil
}

func (w *ffsWorld) thenNothingUnder(o string) error {
	if o != filepath.Base(w.o) {
		return fmt.Errorf("unexpected outside directory %q", o)
	}
	if w.leakedAt != "" {
		return fmt.Errorf("a file appeared under %s during the race at %s", w.o, w.leakedAt)
	}
	names, err := ffsEntries(w.o)
	if err != nil {
		return err
	}
	if len(names) > 0 {
		return fmt.Errorf("files under %s after the race: %v", w.o, names)
	}
	return nil
}

func (w *ffsWorld) thenRefusedCode(want string) error {
	if len(w.otherErrs) > 0 {
		return fmt.Errorf("%d writes failed with a code other than %q: %v", len(w.otherErrs), want, w.otherErrs)
	}
	if got := herr.Kind(w.directErr); string(got) != want {
		return fmt.Errorf("write through %s -> %s: %v (code %q), want %q", w.sub, w.o, w.directErr, got, want)
	}
	return nil
}

func (w *ffsWorld) thenLinkKindRecorded() error {
	if w.raceKind != "symlink" && w.raceKind != "junction" {
		return fmt.Errorf("link kind %q not recorded", w.raceKind)
	}
	return nil
}

// ffsEntries lists every name below dir, recursively, without following links.
func ffsEntries(dir string) ([]string, error) {
	var got []string
	err := filepath.WalkDir(dir, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir {
			got = append(got, p)
		}
		return nil
	})
	sort.Strings(got)
	return got, err
}

// ffsRestore moves keep back to dir; if dir was made again meanwhile (a write
// re-created it), keep, a real directory, is removed instead.
func ffsRestore(dir, keep string) {
	if os.Rename(keep, dir) == nil {
		return
	}
	if fi, err := os.Lstat(keep); err == nil && fi.IsDir() {
		_ = os.RemoveAll(keep)
	}
}

// --- Stale version refused ---

func ffsRead(l fs.FS, f string) (fs.Version, []byte, error) {
	rc, in, err := l.Open(context.Background(), fs.Ref{Path: f})
	if err != nil {
		return fs.Version{}, nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	return in.Version, b, err
}

func (w *ffsWorld) givenStaleSetup(name string) error {
	tmp, err := ffsTempDir()
	if err != nil {
		return err
	}
	ctx := context.Background()
	w.policy = sandboxtest.Policy(sandbox.WorkspaceWrite, tmp)
	w.file = filepath.Join(tmp, name)
	a := local.New()
	if _, err := a.Write(ctx, fs.Ref{Path: w.file}, []byte("v1"), fs.NoGuard, w.policy); err != nil {
		return fmt.Errorf("write v1: %v", err)
	}
	v1, b, err := ffsRead(a, w.file)
	if err != nil {
		return fmt.Errorf("read v1: %v", err)
	}
	if string(b) != "v1" || !v1.HasDigest {
		return fmt.Errorf("read v1: %q, digest=%v", b, v1.HasDigest)
	}
	// Another handle (a second provider, as another process would be) rewrites f.
	w.v2 = []byte("v2-other-handle")
	if _, err := local.New().Write(ctx, fs.Ref{Path: w.file}, w.v2, fs.ReplaceIfVersion{Version: v1}, w.policy); err != nil {
		return fmt.Errorf("rewrite to v2: %v", err)
	}
	w.staleV1 = v1
	w.seam = a
	return nil
}

func (w *ffsWorld) whenStaleWrite() error {
	_, w.staleErr = w.seam.Write(context.Background(), fs.Ref{Path: w.file}, []byte("v3-stale"), fs.ReplaceIfVersion{Version: w.staleV1}, w.policy)
	return nil
}

func (w *ffsWorld) thenStaleCode(want string) error {
	if w.staleErr == nil {
		return fmt.Errorf("the write with v1 succeeded, want %q", want)
	}
	if got := herr.Kind(w.staleErr); string(got) != want {
		return fmt.Errorf("write with v1: %v (code %q), want %q", w.staleErr, got, want)
	}
	return nil
}

func (w *ffsWorld) thenHoldsV2() error {
	b, err := os.ReadFile(w.file)
	if err != nil {
		return err
	}
	if !bytes.Equal(b, w.v2) {
		return fmt.Errorf("file holds %q after the refused write, want v2 bytes %q", b, w.v2)
	}
	return nil
}

func InitializeScenario_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_fenced_filesystem_seam(ctx *godog.ScenarioContext) {
	w := &ffsWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = ffsWorld{}
		return c, nil
	})

	ctx.Step(`^"([^"]*)" rooted at "([^"]*)"$`, w.givenProvider)
	ctx.Step(`^"([^"]*)" executes on the Linux and Windows lanes$`, w.whenRun)
	ctx.Step(`^every subtest passes on every lane$`, w.thenEverySubtestPasses)

	ctx.Step(`^a write root "([^"]*)" and an outside directory "([^"]*)" in "([^"]*)"$`, w.givenRootAndOutside)
	ctx.Step(`^a helper goroutine swaps "([^"]*)" for a link to "([^"]*)" (\d+) times while the seam writes "([^"]*)"$`, w.whenSwapRace)
	ctx.Step(`^no file ever appears under "([^"]*)"$`, w.thenNothingUnder)
	ctx.Step(`^each refused write returns "([^"]*)"$`, w.thenRefusedCode)
	ctx.Step(`^the link kind used for the swap is recorded$`, w.thenLinkKindRecorded)

	ctx.Step(`^file "([^"]*)" read at version v1 and then rewritten to v2 by another handle$`, w.givenStaleSetup)
	ctx.Step(`^a write with v1 is attempted$`, w.whenStaleWrite)
	ctx.Step(`^it returns "([^"]*)"$`, w.thenStaleCode)
	ctx.Step(`^the file still holds v2 bytes$`, w.thenHoldsV2)
}

func TestE2E_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_fenced_filesystem_seam(t *testing.T) {
	ffsSuiteT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_fenced_filesystem_seam,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_12_working_directory_filesystem_seam_and_policy_resolver_fenced_filesystem_seam.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
