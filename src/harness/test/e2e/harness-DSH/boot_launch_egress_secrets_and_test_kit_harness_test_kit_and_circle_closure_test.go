//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// htkcOuterT is the suite's *testing.T: the test kit's helpers take a testing.TB
// and register their releases with t.Cleanup, and the temp-home scenario needs a
// subtest whose end it can observe; godog's per-scenario TestingT exposes neither.
var htkcOuterT *testing.T

var htkcEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// htkcReportCwdEnv makes a re-executed test binary print "cwd <quoted path>"
// before its subprocesstest role runs, so the temp-home scenario can prove the
// stubborn child's working directory is the home.
const htkcReportCwdEnv = "HTKC_REPORT_CWD"

func init() {
	if _, ok := os.LookupEnv(subprocesstest.RoleEnv); !ok {
		return
	}
	if _, ok := os.LookupEnv(htkcReportCwdEnv); !ok {
		return
	}
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "htkc: getwd: %v\n", err)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stdout, "cwd %s\n", strconv.Quote(wd))
}

// htkcDH6 are the scenario IDs authored by DH-S-6 and the stage each sits under.
var htkcDH6 = map[string]string{
	"DH-E-EXEC-1": "DH-S-18",
	"DH-E-HOST-4": "DH-S-18",
	"DH-E-BOOT-1": "DH-S-22",
	"DH-E-UTIL-1": "DH-S-39",
}

type htkcState struct {
	// virtual clock
	clk     *clock.Manual
	done    chan time.Time
	wait    time.Duration
	advance time.Duration
	wall    time.Duration

	// fake model
	handle session.Handle
	route  vocab.RouteID
	tr     *model.Transport
	text   string
	events []event.Event

	// temp home
	end        chan struct{}
	result     chan bool
	passed     bool
	homeRoot   string
	layerFile  string
	checkErr   error
	homeChild  int
	childCwd   string
	cwdOK      bool
	child      int
	grandchild int

	// index
	root      string
	gateBin   string
	required  map[string][]string
	gateOut   string
	gateErr   error
	vetOut    string
	vetErr    error
	authored  map[string][]htkcBinding
	listedRaw string
}

type htkcBinding struct{ file, test string }

// ---- Virtual clock drives deadlines without sleeping ----

func (s *htkcState) componentWaiting(seconds int) error {
	t := htkcOuterT
	root := testkit.Tree(t)
	s.clk = clock.NewManual(htkcEpoch)
	s.done = make(chan time.Time, 1)
	s.wait = time.Duration(seconds) * time.Second
	wait := s.wait
	s.advance = 0
	s.wall = 0
	fiber, err := root.Apply("waiter", func(ctx *cordis.Context) error {
		deadline := s.clk.After(wait)
		go func() {
			select {
			case at := <-deadline:
				s.done <- at
			case <-ctx.Lifetime().Done():
			}
		}()
		return nil
	})
	if err != nil {
		return err
	}
	t.Cleanup(fiber.Dispose)
	if err := fiber.Wait(); err != nil {
		return err
	}
	if !s.clk.BlockUntil(1, time.Second) {
		return errors.New("the component never waited on the virtual clock")
	}
	return nil
}

func (s *htkcState) advanceShort(seconds int) error {
	start := time.Now()
	d := time.Duration(seconds)*time.Second - time.Nanosecond
	s.clk.Advance(d)
	s.advance += d
	s.wall += time.Since(start)
	return nil
}

func (s *htkcState) notCompleted() error {
	select {
	case at := <-s.done:
		return fmt.Errorf("the wait completed at %v, before its deadline", at)
	default:
	}
	if s.clk.Waiters() != 1 {
		return fmt.Errorf("%d timers pending, want the one waiting", s.clk.Waiters())
	}
	return nil
}

func (s *htkcState) advanceLast() error {
	start := time.Now()
	s.clk.Advance(time.Nanosecond)
	s.advance += time.Nanosecond
	select {
	case at := <-s.done:
		s.wall += time.Since(start)
		want := htkcEpoch.Add(s.wait)
		if !at.Equal(want) {
			return fmt.Errorf("the wait completed at %v, want %v", at, want)
		}
		s.done <- at
		return nil
	case <-time.After(time.Second):
		return fmt.Errorf("advancing the clock to %v did not complete the wait", s.wait)
	}
}

func (s *htkcState) completedAtDeadline(seconds int) error {
	select {
	case at := <-s.done:
		if want := htkcEpoch.Add(time.Duration(seconds) * time.Second); !at.Equal(want) || s.advance != time.Duration(seconds)*time.Second {
			return fmt.Errorf("completed at %v after advancing %v, want %v", at, s.advance, want)
		}
	default:
		return errors.New("the wait has not completed")
	}
	if want := htkcEpoch.Add(time.Duration(seconds) * time.Second); !s.clk.Now().Equal(want) || s.wait != time.Duration(seconds)*time.Second {
		return fmt.Errorf("the clock reads %v", s.clk.Now())
	}
	if s.clk.Waiters() != 0 {
		return fmt.Errorf("%d timers still pending", s.clk.Waiters())
	}
	return nil
}

func (s *htkcState) wallUnder(ms int) error {
	if s.wall >= time.Duration(ms)*time.Millisecond {
		return fmt.Errorf("the %v wait took %v of wall time, want under %d ms", s.wait, s.wall, ms)
	}
	return nil
}

// ---- Fake model scripts a turn ----

func htkcMust[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func (s *htkcState) openStep(route string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("opening the session: %v", r)
		}
	}()
	t := htkcOuterT
	ctx := context.Background()
	s.route = vocab.RouteID(route)
	svc := session.NewService(testkit.Tree(t), memstore.New(), session.Config{})
	h := htkcMust(svc.Create(ctx, nil, session.CreateOptions{Scope: scope.Key{}}))
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	user := htkcMust(vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("hello")))
	drafts := []event.Draft{
		htkcMust(event.TurnStartType.Draft(event.TurnStart{Turn: 1})),
		htkcMust(event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1})),
		htkcMust(event.RequestHeader.Draft(event.RequestHeaderPayload{Route: s.route})),
		htkcMust(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})),
	}
	htkcMust(h.AppendGroup(ctx, drafts))
	s.handle = h
	return nil
}

func (s *htkcState) scriptReply(text string) error {
	s.text = text
	s.tr = model.NewTransport(htkcOuterT, model.Reply(text))
	return nil
}

type htkcSink struct{ h session.Handle }

func (k htkcSink) Settle(a llm.Attempt) error {
	d, err := a.Draft()
	if err != nil {
		return err
	}
	_, err = k.h.Append(context.Background(), d)
	return err
}

func (s *htkcState) oneAttempt() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the attempt: %v", r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rt := llm.New()
	model.Register(htkcOuterT, rt, s.route, s.tr)
	p, err := rt.Prepare(ctx, s.route, "", vocab.Controls{})
	if err != nil {
		return fmt.Errorf("Prepare: %w", err)
	}
	req, err := s.handle.NextRequest()
	if err != nil {
		return fmt.Errorf("NextRequest: %w", err)
	}
	start := s.handle.Len()
	for range p.Stream(ctx, req, htkcSink{s.handle}) {
	}
	s.events, err = s.handle.Read(context.Background(), start, 10)
	return err
}

func (s *htkcState) settledWithText(typ string) error {
	if len(s.events) != 1 || s.events[0].Type() != typ {
		types := make([]string, 0, len(s.events))
		for _, e := range s.events {
			types = append(types, e.Type())
		}
		return fmt.Errorf("settled %v, want exactly one %s", types, typ)
	}
	var got struct {
		Message struct {
			Blocks []struct {
				Text struct{ Text string } `json:"text"`
			} `json:"blocks"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(s.events[0].Raw()), &got); err != nil {
		return err
	}
	if len(got.Message.Blocks) != 1 || got.Message.Blocks[0].Text.Text != s.text {
		return fmt.Errorf("settled message %s, want one text block %q", s.events[0].Raw(), s.text)
	}
	return nil
}

func (s *htkcState) transportSawOne(route string) error {
	reqs := s.tr.Requests()
	if len(reqs) != 1 {
		return fmt.Errorf("the transport saw %d requests, want one", len(reqs))
	}
	if a := reqs[0].Header.Get("Accept"); a != "text/event-stream" {
		return fmt.Errorf("Accept = %q, want a streaming request", a)
	}
	var body model.Body
	if err := json.Unmarshal(reqs[0].Body, &body); err != nil {
		return fmt.Errorf("request body %s: %v", reqs[0].Body, err)
	}
	if string(body.Route) != route || body.Model != model.ModelName || body.Digest == "" {
		return fmt.Errorf("request body %s, want route %q, model %q and a digest", reqs[0].Body, route, model.ModelName)
	}
	return nil
}

// ---- Temp home torn down by cleanup ----

// startTest runs a subtest that holds a kit home, a stubborn child working in it
// and a child/grandchild tree, and keeps it open until the "test ends" step.
func (s *htkcState) startTest() error {
	if htkcOuterT == nil {
		return errors.New("suite *testing.T not set")
	}
	s.end = make(chan struct{})
	s.result = make(chan bool, 1)
	ready := make(chan struct{})
	go func() {
		s.result <- htkcOuterT.Run("temp-home-and-child", func(t *testing.T) {
			l := temphome.New(t)
			s.homeRoot = l.Root()
			s.checkErr = home.Check(l)
			s.layerFile = temphome.Layer(t, l.Root(), "probe", "[]\n")

			spec := subprocesstest.Spec(t, subprocesstest.RoleStubborn)
			spec.Dir = l.Root()
			spec.Env = append(spec.Env, htkcReportCwdEnv+"=1")
			pr, pw := io.Pipe()
			spec.Stdout = pw
			subprocesstest.Start(t, spec)
			t.Cleanup(func() { _ = pr.Close() })
			br := bufio.NewReader(pr)
			line, err := br.ReadString('\n')
			if err != nil || !strings.HasPrefix(line, "cwd ") {
				t.Fatalf("stubborn child said %q: %v", line, err)
			}
			if s.childCwd, err = strconv.Unquote(strings.TrimSpace(strings.TrimPrefix(line, "cwd "))); err != nil {
				t.Fatalf("stubborn child said %q: %v", line, err)
			}
			s.cwdOK = htkcSameDir(s.childCwd, l.Root())
			line, err = br.ReadString('\n')
			if err != nil {
				t.Fatalf("stubborn child said %q: %v", line, err)
			}
			if _, err := fmt.Sscanf(line, "ready %d", &s.homeChild); err != nil {
				t.Fatalf("stubborn child said %q: %v", line, err)
			}
			go func() { _, _ = io.Copy(io.Discard, pr) }()

			_, s.child, s.grandchild = subprocesstest.Tree(t)
			close(ready)
			<-s.end
		})
	}()
	select {
	case <-ready:
		return nil
	case ok := <-s.result:
		s.result <- ok
		return fmt.Errorf("the test failed while creating the home and child (passed=%v)", ok)
	case <-time.After(60 * time.Second):
		return errors.New("the test did not create its home and child within 60 s")
	}
}

func (s *htkcState) homePassesCheck() error {
	if s.checkErr != nil {
		return fmt.Errorf("the kit home fails the boot permission check: %v", s.checkErr)
	}
	for _, p := range []string{s.homeRoot, s.layerFile} {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("%s while the test runs: %v", p, err)
		}
	}
	return nil
}

func (s *htkcState) childRunningInHome() error {
	for _, pid := range []int{s.homeChild, s.child, s.grandchild} {
		if pid <= 0 || !subprocesstest.Alive(pid) {
			return fmt.Errorf("process %d is not running (home child %d, child %d, grandchild %d)", pid, s.homeChild, s.child, s.grandchild)
		}
	}
	if !s.cwdOK {
		return fmt.Errorf("the home child's working directory is %q, want the home %q", s.childCwd, s.homeRoot)
	}
	return nil
}

// htkcSameDir reports whether a and b name the same directory after resolving
// symlinks (e.g. macOS /var -> /private/var) and, on Windows, short names or case.
func htkcSameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return false
	}
	if filepath.Clean(ra) == filepath.Clean(rb) {
		return true
	}
	ia, errA := os.Stat(ra)
	ib, errB := os.Stat(rb)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}

func (s *htkcState) testEnds() error {
	if s.end == nil {
		return errors.New("no test is running")
	}
	close(s.end)
	select {
	case s.passed = <-s.result:
		s.end = nil
		return nil
	case <-time.After(60 * time.Second):
		return errors.New("the test's cleanup did not finish within 60 s")
	}
}

func (s *htkcState) testPassed() error {
	if !s.passed {
		return errors.New("the test failed: its cleanup reported a leak (see the subtest log)")
	}
	return nil
}

func (s *htkcState) homeGone() error {
	for _, p := range []string{s.homeRoot, s.layerFile} {
		if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s survives its test (%v)", p, err)
		}
	}
	return nil
}

func (s *htkcState) noChildRemains() error {
	// Whether a child has exited is a fact only the OS makes true: the one named poll
	// helper waits for it (M1-D-GATE-24, rule 4).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var alive []int
	if err := procwait.Poll(ctx, func() bool {
		alive = alive[:0]
		for _, pid := range []int{s.homeChild, s.child, s.grandchild} {
			if !subprocesstest.Exited(pid) {
				alive = append(alive, pid)
			}
		}
		return len(alive) == 0
	}); err != nil {
		return fmt.Errorf("processes %v survive the test: %w", alive, err)
	}
	return nil
}

// ---- DH-S-6 scenarios authored and indexed ----

func htkcRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if htkcIsRegular(filepath.Join(dir, "go.work")) && htkcIsRegular(filepath.Join(dir, "test", "harness", "acceptance-index.yaml")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no repository root (go.work + test/harness/acceptance-index.yaml) above the working directory")
		}
		dir = parent
	}
}

func htkcIsRegular(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

var (
	htkcDirective = regexp.MustCompile(`^//dsh:scenario\s+(\S+)\s*$`)
	htkcTestFunc  = regexp.MustCompile(`^func (Test\w+)\(`)
)

// htkcScan finds every //dsh:scenario binding in the product modules' scenario files.
func htkcScan(root string) (map[string][]htkcBinding, error) {
	out := map[string][]htkcBinding{}
	for _, mod := range []string{"src/go", "src/sdk", "src/harness"} {
		base := filepath.Join(root, filepath.FromSlash(mod))
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if p != base && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), "_scenario_test.go") {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
			if len(lines) == 0 || strings.TrimSpace(lines[0]) != "//go:build scenario" {
				return fmt.Errorf("%s does not start with //go:build scenario", p)
			}
			rel, _ := filepath.Rel(root, p)
			for i, line := range lines {
				m := htkcDirective.FindStringSubmatch(strings.TrimSpace(line))
				if m == nil {
					continue
				}
				test := ""
				for _, next := range lines[i+1:] {
					if f := htkcTestFunc.FindStringSubmatch(next); f != nil {
						test = f[1]
						break
					}
				}
				out[m[1]] = append(out[m[1]], htkcBinding{file: filepath.ToSlash(rel), test: test})
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return out, nil
}

func htkcCommand(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (s *htkcState) givenFilesAndIndex(index string) error {
	root, err := htkcRepoRoot()
	if err != nil {
		return err
	}
	s.root = root
	if !htkcIsRegular(filepath.Join(root, filepath.FromSlash(index))) {
		return fmt.Errorf("%s is missing", index)
	}
	s.authored, err = htkcScan(root)
	if err != nil {
		return err
	}
	for id := range htkcDH6 {
		if len(s.authored[id]) == 0 {
			return fmt.Errorf("no scenario file binds %s", id)
		}
	}
	return nil
}

func (s *htkcState) runIndexCheck() error {
	bin := filepath.Join(htkcOuterT.TempDir(), "harness-gate")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	gateDir := filepath.Join(s.root, "tools", "harness-gate")
	if out, err := htkcCommand(gateDir, []string{"GOWORK=off"}, "go", "build", "-o", bin, "."); err != nil {
		return fmt.Errorf("building harness-gate: %v\n%s", err, out)
	}
	s.gateBin = bin
	s.required = map[string][]string{}
	for _, stage := range []string{"DH-S-18", "DH-S-22", "DH-S-39"} {
		out, err := htkcCommand(s.root, nil, bin, "require", "--root", s.root, "--stage", stage)
		if err != nil {
			return fmt.Errorf("harness-gate require --stage %s: %v\n%s", stage, err, out)
		}
		line := strings.TrimSpace(out)
		if line != "" {
			s.required[stage] = strings.Split(line, ",")
		}
	}
	ids := make([]string, 0, len(htkcDH6))
	for id := range htkcDH6 {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	ghOut := filepath.Join(htkcOuterT.TempDir(), "github-output")
	s.gateOut, s.gateErr = htkcCommand(s.root, []string{
		"DSH_DEEPSEEK_API_KEY=e2e-dummy-not-a-key",
		"DSH_REQUIRE_SCENARIOS=" + strings.Join(ids, ","),
		"GITHUB_OUTPUT=" + ghOut,
	}, bin, "preflight", "--root", s.root)
	return nil
}

func (s *htkcState) runVet(command string) error {
	args := strings.Fields(command)
	if len(args) < 2 || args[0] != "go" {
		return fmt.Errorf("unexpected command %q", command)
	}
	s.vetOut, s.vetErr = htkcCommand(s.root, nil, "go", args[1:]...)
	return nil
}

func (s *htkcState) sitUnder(a, b, stage string) error {
	for _, id := range []string{a, b} {
		if err := s.sitsUnder(id, stage); err != nil {
			return err
		}
	}
	return nil
}

func (s *htkcState) sitsUnder(id, stage string) error {
	if htkcDH6[id] != stage {
		return fmt.Errorf("%s is not one of the DH-S-6 IDs expected under %s", id, stage)
	}
	for _, got := range s.required[stage] {
		if got == id {
			return nil
		}
	}
	return fmt.Errorf("the index entry of %s is %v, want it to hold %s", stage, s.required[stage], id)
}

func (s *htkcState) exactlyOneFile() error {
	for id := range htkcDH6 {
		bs := s.authored[id]
		if len(bs) != 1 {
			return fmt.Errorf("%s is bound %d times: %+v", id, len(bs), bs)
		}
		if bs[0].test == "" {
			return fmt.Errorf("%s in %s is not bound to a TestXxx", id, bs[0].file)
		}
		if !strings.Contains(s.gateOut, id+"  "+bs[0].test+" (") {
			return fmt.Errorf("the gate does not bind %s to %s:\n%s", id, bs[0].test, s.gateOut)
		}
	}
	return nil
}

func (s *htkcState) gatePasses() error {
	if s.gateErr != nil {
		return fmt.Errorf("harness-gate preflight: %v\n%s", s.gateErr, s.gateOut)
	}
	if !strings.Contains(s.gateOut, fmt.Sprintf("%d required scenarios", len(htkcDH6))) {
		return fmt.Errorf("harness-gate preflight did not plan %d scenarios:\n%s", len(htkcDH6), s.gateOut)
	}
	return nil
}

func (s *htkcState) vetExits0() error {
	if s.vetErr != nil {
		return fmt.Errorf("vet -tags scenario: %v\n%s", s.vetErr, s.vetOut)
	}
	return nil
}

// defaultRunNone lists the tests of every package holding a DH-S-6 scenario: with
// the gate's explicit empty tag list (-tags=, as tools/harness-gate noTags, which
// overrides any GOFLAGS -tags) none is compiled in; with -tags scenario and GOFLAGS
// cleared every one is.
func (s *htkcState) defaultRunNone() error {
	pkgs := map[string]bool{}
	var tests []string
	for id := range htkcDH6 {
		b := s.authored[id][0]
		pkgs["./"+strings.TrimPrefix(filepath.ToSlash(filepath.Dir(b.file)), "src/harness/")] = true
		tests = append(tests, b.test)
	}
	var list []string
	for p := range pkgs {
		list = append(list, p)
	}
	sort.Strings(list)
	harness := filepath.Join(s.root, "src", "harness")
	out, err := htkcCommand(harness, nil, "go", append([]string{"test", "-tags=", "-list", ".", "-count=1"}, list...)...)
	if err != nil {
		return fmt.Errorf("go test -tags= -list: %v\n%s", err, out)
	}
	for _, name := range tests {
		if regexp.MustCompile(`(?m)^` + name + `\s*$`).MatchString(out) {
			return fmt.Errorf("the default run includes %s:\n%s", name, out)
		}
	}
	tagged, err := htkcCommand(harness, []string{"GOFLAGS="}, "go", append([]string{"test", "-tags", "scenario", "-list", ".", "-count=1"}, list...)...)
	if err != nil {
		return fmt.Errorf("go test -tags scenario -list: %v\n%s", err, tagged)
	}
	for _, name := range tests {
		if !regexp.MustCompile(`(?m)^` + name + `\s*$`).MatchString(tagged) {
			return fmt.Errorf("-tags scenario does not list %s:\n%s", name, tagged)
		}
	}
	s.listedRaw = out
	return nil
}

func InitializeScenario_boot_launch_egress_secrets_and_test_kit_harness_test_kit_and_circle_closure(ctx *godog.ScenarioContext) {
	s := &htkcState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = htkcState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if s.end != nil {
			close(s.end)
			select {
			case <-s.result:
			case <-time.After(60 * time.Second):
			}
			s.end = nil
		}
		return c, nil
	})

	ctx.Step(`^a test-kit tree with a component waiting (\d+) s on the virtual clock$`, s.componentWaiting)
	ctx.Step(`^the clock is advanced 1 ns short of (\d+) s$`, s.advanceShort)
	ctx.Step(`^the wait has not completed$`, s.notCompleted)
	ctx.Step(`^the clock is advanced the last 1 ns$`, s.advanceLast)
	ctx.Step(`^the wait completes at the (\d+) s deadline$`, s.completedAtDeadline)
	ctx.Step(`^the advance took under (\d+) ms of wall time$`, s.wallUnder)

	ctx.Step(`^a session with an open step on route "([^"]*)"$`, s.openStep)
	ctx.Step(`^the fake model scripted with one text reply "([^"]*)"$`, s.scriptReply)
	ctx.Step(`^the runtime makes one attempt through the fake transport$`, s.oneAttempt)
	ctx.Step(`^one "([^"]*)" is settled with the scripted text$`, s.settledWithText)
	ctx.Step(`^the fake transport saw one streaming request for route "([^"]*)"$`, s.transportSawOne)

	ctx.Step(`^a test that creates a test-kit temp home and a child process$`, s.startTest)
	ctx.Step(`^the home passes the boot permission check while the test runs$`, s.homePassesCheck)
	ctx.Step(`^the child process is running with its working directory in the home$`, s.childRunningInHome)
	ctx.Step(`^the test ends$`, s.testEnds)
	ctx.Step(`^the test passed$`, s.testPassed)
	ctx.Step(`^the home directory no longer exists$`, s.homeGone)
	ctx.Step(`^no child process remains$`, s.noChildRemains)

	ctx.Step(`^the four new scenario files and "([^"]*)"$`, s.givenFilesAndIndex)
	ctx.Step(`^the gate's index check runs$`, s.runIndexCheck)
	ctx.Step(`^"(go -C src/harness vet -tags scenario \./\.\.\.)" runs$`, s.runVet)
	ctx.Step(`^"([^"]*)" and "([^"]*)" sit under "([^"]*)"$`, s.sitUnder)
	ctx.Step(`^"([^"]*)" sits under "([^"]*)"$`, s.sitsUnder)
	ctx.Step(`^each of the four IDs has exactly one scenario file$`, s.exactlyOneFile)
	ctx.Step(`^the gate's index check passes$`, s.gatePasses)
	ctx.Step(`^vet exits 0$`, s.vetExits0)
	ctx.Step(`^the default gate run executes none of them$`, s.defaultRunNone)
}

func TestE2E_boot_launch_egress_secrets_and_test_kit_harness_test_kit_and_circle_closure(t *testing.T) {
	htkcOuterT = t
	defer func() { htkcOuterT = nil }()
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_boot_launch_egress_secrets_and_test_kit_harness_test_kit_and_circle_closure,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"boot_launch_egress_secrets_and_test_kit_harness_test_kit_and_circle_closure.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
