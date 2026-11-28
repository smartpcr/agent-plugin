//go:build e2e

package e2e

// Step definitions of
// dh_s_17_acceptance_on_a_real_model_record_then_replay_without_a_key.feature
// (DH-S-17 Stage 7.2, DH-E-TOOLCHAIN-1, TC-20s, M2-DEC-5).
//
//   - Record and replay: dsh built with the testhook tag records one session on
//     route "claude-code", served by an in-process loopback model instance, into a
//     scratch home and workspace. The persisted log is exported from the run's
//     SQLite store and normalized, and each request is rebuilt from that log. The
//     recording is then replayed twice by llm.replay with the route absent and no
//     key, and each replay's log, workspace and requests are compared to the
//     recording. On the S17 machine (DSH_SCENARIO_TOOLCHAIN1_* set) the real
//     scenario test on the signed-in route must pass as well.
//   - Evidence row: the committed DH-S-17 record and copies of it are read by the
//     harness-gate binary built from tools/harness-gate.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/fixture"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
)

const (
	s17rStage     = "DH-S-17"
	s17rScenario  = "DH-E-TOOLCHAIN-1"
	s17rTest      = "TestRecordThenReplayWithoutAKey"
	s17rRecordRel = "docs/stories/harness-DSH-M2/evidence/DH-S-17.md"
	s17rIndexRel  = "test/harness/acceptance-index.yaml"
	s17rWithin    = 2 * time.Minute
	// s17rLiveWithin bounds the live signed-in-route run of s17rTest.
	s17rLiveWithin = 15 * time.Minute
	s17rMaxOutput  = 4 << 20
	s17rReplays    = 2
)

// s17rRouteInputs are the inputs TestRecordThenReplayWithoutAKey needs from the S17
// machine's signed-in Claude Code route.
var s17rRouteInputs = []string{"DSH_SCENARIO_TOOLCHAIN1_DIR", "DSH_SCENARIO_TOOLCHAIN1_RECORD", "DSH_SCENARIO_TOOLCHAIN1_REPLAY"}

// s17rTurns are the route instance's replies in request order, in the kit's SSE
// dialect: a call of the unregistered tool "write", then a closing answer. The call
// id is in the Claude form, which the normalizer keeps, so the replayed request
// carries the very id the route sent.
var s17rTurns = []string{
	"event: text\ndata: {\"text\":\"I will write the note.\"}\n\n" +
		"event: tool\ndata: {\"id\":\"toolu_s17r01\",\"name\":\"write\",\"args\":{\"file_path\":\"note.txt\",\"content\":\"recorded on claude-code\\n\"}}\n\n" +
		"event: usage\ndata: {\"input\":10,\"output\":5}\n\n" +
		"event: finish\ndata: {\"reason\":\"tool_calls\"}\n\n",
	"event: text\ndata: {\"text\":\"No write tool is registered, so the workspace is unchanged.\"}\n\n" +
		"event: usage\ndata: {\"input\":20,\"output\":4}\n\n" +
		"event: finish\ndata: {\"reason\":\"stop\"}\n\n",
}

// s17rServedBy matches the served_by member of a settlement payload (escaped inside
// the log line's data string). A listener that serves an attempt in place of its
// route names itself there; an attempt the route served has none.
var s17rServedBy = regexp.MustCompile(`,\\"served_by\\":\\"([^"\\]*)\\"`)

var (
	s17rOuterT *testing.T
	s17rDsh    string

	s17rGateOnce sync.Once
	s17rGateBin  string
	s17rGateErr  error

	s17rSHA      = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	s17rChecksum = regexp.MustCompile(`(?:^|[^0-9a-fA-F])([0-9a-f]{64})(?:$|[^0-9a-fA-F])`)
	s17rModelID  = regexp.MustCompile(`(?i)\bmodel[ _-]?id\s*[:= ]\s*([A-Za-z0-9][\w.:/@-]*)`)
	s17rRunID    = regexp.MustCompile(`(?i)\brun[ _-]?id\s*[:= ]\s*([A-Za-z0-9][\w.:/@-]*)`)
)

// s17rRoute is the ephemeral instance of route "claude-code": a loopback server that
// answers s17rTurns and keeps every request body it received.
type s17rRoute struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies []model.Body
}

func newS17rRoute() *s17rRoute {
	r := &s17rRoute{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		var b model.Body
		_ = json.Unmarshal(raw, &b)
		r.mu.Lock()
		r.bodies = append(r.bodies, b)
		n := len(r.bodies) - 1
		r.mu.Unlock()
		if n >= len(s17rTurns) {
			http.Error(w, "unscripted request", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, s17rTurns[n])
	}))
	return r
}

func (r *s17rRoute) received() []model.Body {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.bodies)
}

// s17rRun is one dsh run's persisted state: the raw and normalized log, its
// served_by values, its normalized requests in order and its workspace.
type s17rRun struct {
	log      []byte
	servedBy []string
	requests [][]byte
	digests  []string // recording only: the digest of each request rebuilt from the log
	ws       string
}

type s17rState struct {
	repo, module string

	// Record and replay
	route    *s17rRoute
	home     string
	scenario string
	recorded s17rRun
	replays  []s17rRun
	marker   string

	// Evidence row
	record string
	row    map[string]string
	root   string
	stdout string
	code   int
}

func s17rModuleDir() (string, error) {
	dir, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(dir, "cmd", "dsh")); err != nil {
		return "", fmt.Errorf("%s is not the harness module: %v", dir, err)
	}
	return dir, nil
}

// s17rEnv is the host environment with GOWORK=off and no inherited GOFLAGS, plus set.
func s17rEnv(set ...string) []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		u := strings.ToUpper(kv)
		return strings.HasPrefix(u, "GOWORK=") || strings.HasPrefix(u, "GOFLAGS=")
	})
	return append(append(env, "GOWORK=off"), set...)
}

// ---- Scenario: DH-E-TOOLCHAIN-1 record and replay ----

func (s *s17rState) givenBuilt() error {
	if s17rDsh == "" {
		s17rDsh = launcher.Build(s17rOuterT, s.module, "./cmd/dsh", "testhook")
	}
	return nil
}

func (s *s17rState) givenRoute(id string) error {
	s.route = newS17rRoute()
	s.home = temphome.New(s17rOuterT).Root()
	temphome.Layer(s17rOuterT, s.home, profile.Headless, "- insert:\n    - id: route\n      name: llm.testkit-route\n      config:\n"+
		"        id: "+strconv.Quote(id)+"\n        url: "+strconv.Quote(s.route.srv.URL)+"\n")
	return nil
}

// s17rHeadless runs `dsh --home root headless --json input` in ws and returns the
// session the run/result line names.
func s17rHeadless(root, ws, input string, env []string) (string, error) {
	h, err := launcher.Run(s17rOuterT, s17rWithin, launcher.Program{
		Argv: []string{s17rDsh, "--home", root, "headless", "--json", input},
		Dir:  ws, Env: env, MaxOutput: s17rMaxOutput,
	})
	out, errOut := h.Stdout.String(), h.Stderr.String()
	switch {
	case err != nil:
		return "", fmt.Errorf("dsh headless: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	case h.Process.ExitCode() != 0:
		return "", fmt.Errorf("dsh headless exited %d\nstdout:\n%s\nstderr:\n%s", h.Process.ExitCode(), out, errOut)
	case h.Stdout.Dropped() > 0 || h.Stderr.Dropped() > 0:
		return "", errors.New("dsh headless wrote past its output cap")
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var res struct{ Type, Session string }
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil || res.Type != "run/result" || res.Session == "" {
		return "", fmt.Errorf("the last stdout line is not a run/result (%v):\n%s", err, out)
	}
	return res.Session, nil
}

// s17rPersisted exports session from root's SQLite store. With rebuild, each
// request the log settles is rebuilt from the stored prefix; its digest must be the
// one the log records.
func s17rPersisted(root, session string, rebuild bool) (log []byte, canon []string, digests []string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	st, err := sqlite.Open(ctx, home.New(root).DB())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("opening the run's SQLite store: %v", err)
	}
	defer func() {
		if cerr := st.Close(ctx); err == nil {
			err = cerr
		}
	}()
	var buf bytes.Buffer
	if err := st.Export(ctx, &buf, session, sqlite.ExportOptions{}); err != nil {
		return nil, nil, nil, fmt.Errorf("exporting session %s: %v", session, err)
	}
	if buf.Len() == 0 {
		return nil, nil, nil, errors.New("the SQLite store holds no log for the run")
	}
	if !rebuild {
		return buf.Bytes(), nil, nil, nil
	}
	rd, err := st.OpenRead(ctx, session)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("opening session %s for reading: %v", session, err)
	}
	defer func() { _ = rd.Close(ctx) }()
	read := func(ctx context.Context, from event.Offset, limit int) ([]event.Record, error) {
		return rd.Read(ctx, from, limit, store.ReadOptions{WithAux: true})
	}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var e struct{ Type, Data string }
		if json.Unmarshal([]byte(line), &e) != nil || (e.Type != "assistant/message" && e.Type != "assistant/attempt") {
			continue
		}
		var p struct {
			AsOf   *event.Seq `json:"request_as_of"`
			Digest string     `json:"request_digest"`
		}
		if err := json.Unmarshal([]byte(e.Data), &p); err != nil || p.AsOf == nil {
			return nil, nil, nil, fmt.Errorf("%s has no request_as_of: %s", e.Type, line)
		}
		b, err := surface.Rebuild(ctx, event.Default(), read, session, *p.AsOf)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("rebuilding the request at %d: %v", *p.AsOf, err)
		}
		if b.Digest() != p.Digest {
			return nil, nil, nil, fmt.Errorf("the request rebuilt at %d has digest %s, the log records %s", *p.AsOf, b.Digest(), p.Digest)
		}
		canon, digests = append(canon, b.Canonical()+"\n"), append(digests, p.Digest)
	}
	return buf.Bytes(), canon, digests, nil
}

// s17rNormalize normalizes log, then the requests in order, with one normalizer. The
// served_by attribution is collected and then dropped: it says who served the
// attempt, which is the one thing a replay changes by design, and is asserted on
// its own (thenServedByReplay).
func s17rNormalize(paths map[string]string, log []byte, requests [][]byte) s17rRun {
	n := &fixture.Normalizer{Paths: paths}
	r := s17rRun{log: n.Normalize(log)}
	for _, m := range s17rServedBy.FindAllSubmatch(log, -1) {
		r.servedBy = append(r.servedBy, string(m[1]))
	}
	r.log = s17rServedBy.ReplaceAll(r.log, nil)
	for _, q := range requests {
		r.requests = append(r.requests, n.Normalize(q))
	}
	return r
}

func s17rWriteFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func (s *s17rState) whenRecords(input, route string) error {
	t := s17rOuterT
	seed, ws := t.TempDir(), t.TempDir()
	if err := s17rWriteFile(filepath.Join(seed, "README.txt"), []byte("the S17 scratch workspace\n")); err != nil {
		return err
	}
	if err := fixture.CopyTree(seed, ws); err != nil {
		return err
	}
	session, err := s17rHeadless(s.home, ws, input, shkKeylessEnv(os.Environ()))
	if err != nil {
		return fmt.Errorf("record: %v", err)
	}
	raw, canon, digests, err := s17rPersisted(s.home, session, true)
	if err != nil {
		return fmt.Errorf("record: %v", err)
	}
	reqs := make([][]byte, len(canon))
	for i, c := range canon {
		reqs[i] = []byte(c)
	}
	s.recorded = s17rNormalize(map[string]string{s.home: "home", ws: "workspace"}, raw, reqs)
	s.recorded.digests, s.recorded.ws = digests, ws

	// The recording as llm.replay reads it: the manifest, the normalized log, the
	// workspace seed and the workspace the record left.
	s.scenario = t.TempDir()
	manifest, _ := json.Marshal(fixture.Manifest{Profile: "headless", Input: input, Recording: "live", Route: route, Model: model.ModelName})
	if err := s17rWriteFile(filepath.Join(s.scenario, "scenario.json"), append(manifest, '\n')); err != nil {
		return err
	}
	if err := s17rWriteFile(filepath.Join(s.scenario, "session.jsonl"), s.recorded.log); err != nil {
		return err
	}
	if err := fixture.CopyTree(seed, filepath.Join(s.scenario, "workspace")); err != nil {
		return err
	}
	return fixture.CopyTree(ws, filepath.Join(s.scenario, "workspace.expected"))
}

// s17rClaudeStub is a directory holding a "claude" that records being started, put
// first on the replay's PATH.
func (s *s17rState) s17rClaudeStub() (string, error) {
	dir := s17rOuterT.TempDir()
	s.marker = filepath.Join(dir, "claude-started")
	name, body := "claude", "#!/bin/sh\necho started > '"+s.marker+"'\nexit 1\n"
	if runtime.GOOS == "windows" {
		name, body = "claude.bat", "@echo started> \""+s.marker+"\"\r\n@exit /b 1\r\n"
	}
	return dir, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755)
}

func (s *s17rState) whenReplaysTwice() error {
	t := s17rOuterT
	m, err := fixture.ReadManifest(s.scenario)
	if err != nil {
		return err
	}
	stub, err := s.s17rClaudeStub()
	if err != nil {
		return err
	}
	env := append(shkKeylessEnv(os.Environ()), "PATH="+stub+string(os.PathListSeparator)+os.Getenv("PATH"))
	for i := range s17rReplays {
		root, ws, reqs := temphome.New(t).Root(), t.TempDir(), t.TempDir()
		if err := fixture.CopyTree(filepath.Join(s.scenario, "workspace"), ws); err != nil {
			return err
		}
		temphome.Layer(t, root, profile.Headless, "- insert:\n    - id: replay\n      name: llm.replay\n      config:\n"+
			"        scenario: "+strconv.Quote(s.scenario)+"\n        mode: replay\n        route: "+strconv.Quote(m.Route)+"\n"+
			"        model: "+strconv.Quote(m.Model)+"\n        requests: "+strconv.Quote(reqs)+"\n")
		session, err := s17rHeadless(root, ws, m.Input, env)
		if err != nil {
			return fmt.Errorf("replay %d: %v", i+1, err)
		}
		raw, _, _, err := s17rPersisted(root, session, false)
		if err != nil {
			return fmt.Errorf("replay %d: %v", i+1, err)
		}
		files, err := fixture.Tree(reqs)
		if err != nil {
			return err
		}
		names := make([]string, 0, len(files))
		for k := range files {
			names = append(names, k)
		}
		slices.Sort(names)
		var requests [][]byte
		for _, k := range names {
			requests = append(requests, files[k])
		}
		r := s17rNormalize(map[string]string{root: "home", ws: "workspace", s.scenario: "scenario", reqs: "requests"}, raw, requests)
		r.ws = ws
		s.replays = append(s.replays, r)
	}
	return nil
}

func (s *s17rState) thenRequestsRecorded(n int) error {
	got := s.route.received()
	if len(got) != n || len(s.recorded.requests) != n {
		return fmt.Errorf("the route received %d requests and the log settles %d, want %d each", len(got), len(s.recorded.requests), n)
	}
	for i, b := range got {
		if b.Route != "claude-code" || b.Model != model.ModelName {
			return fmt.Errorf("request %d went to route %q model %q, want claude-code/%s", i, b.Route, b.Model, model.ModelName)
		}
	}
	return nil
}

func (s *s17rState) thenRebuiltDigests() error {
	got := s.route.received()
	for i, d := range s.recorded.digests {
		if i >= len(got) || got[i].Digest != d {
			return fmt.Errorf("request %d rebuilt from the log has digest %s; the route received %v", i, d, got)
		}
	}
	return nil
}

func (s *s17rState) thenRouteUntouched() error {
	if got := len(s.route.received()); got != len(s.recorded.requests) {
		return fmt.Errorf("the route received %d requests in all, %d during the replays", got, got-len(s.recorded.requests))
	}
	if _, err := os.Stat(s.marker); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("a claude client was started during a replay (%s: %v)", s.marker, err)
	}
	return nil
}

func (s *s17rState) thenServedByReplay() error {
	n := len(s.recorded.requests)
	if len(s.recorded.servedBy) != 0 || len(s.route.received()) < n {
		return fmt.Errorf("the recording's attempts are attributed to %q, want all %d served by the route itself", s.recorded.servedBy, n)
	}
	for i, r := range s.replays {
		if len(r.servedBy) != n || slices.ContainsFunc(r.servedBy, func(v string) bool { return v != "replay" }) {
			return fmt.Errorf("replay %d's attempts are served by %q, want %d served by the replay", i+1, r.servedBy, n)
		}
	}
	return nil
}

func (s *s17rState) thenLogsEqual() error {
	if len(s.replays) != s17rReplays {
		return fmt.Errorf("%d replays ran, want %d", len(s.replays), s17rReplays)
	}
	for i, r := range s.replays {
		if d := fixture.Diff(fmt.Sprintf("replay %d session.jsonl", i+1), r.log, s.recorded.log); d != "" {
			return errors.New(d)
		}
	}
	return nil
}

func (s *s17rState) thenWorkspacesEqual() error {
	for i, r := range s.replays {
		if err := fixture.CompareTrees(r.ws, s.recorded.ws); err != nil {
			return fmt.Errorf("replay %d: %v", i+1, err)
		}
	}
	return nil
}

func (s *s17rState) thenRequestsEqual() error {
	for i, r := range s.replays {
		if len(r.requests) != len(s.recorded.requests) {
			return fmt.Errorf("replay %d sent %d requests, the recording %d", i+1, len(r.requests), len(s.recorded.requests))
		}
		for j := range r.requests {
			if d := fixture.Diff(fmt.Sprintf("replay %d request %03d", i+1, j), r.requests[j], s.recorded.requests[j]); d != "" {
				return errors.New(d)
			}
		}
	}
	return nil
}

// thenLiveOnS17 runs the real scenario test where the signed-in route's inputs are
// set; elsewhere the in-process route instance above is the proof.
func (s *s17rState) thenLiveOnS17() error {
	for _, name := range s17rRouteInputs {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			return nil
		}
	}
	// -count=1 defeats the test cache so the signed-in route is contacted afresh;
	// -timeout and the context deadline bound the run so it cannot hang the suite.
	ctx, cancel := context.WithTimeout(context.Background(), s17rLiveWithin+time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-tags", "scenario", "-count=1", "-timeout", s17rLiveWithin.String(), "-v", "-run", "^"+s17rTest+"$", "./internal/testkit/scenarios/")
	cmd.WaitDelay = 10 * time.Second
	cmd.Dir = s.module
	cmd.Env = s17rEnv("DSH_REQUIRE_SCENARIOS=" + s17rScenario)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: "+s17rTest) {
		return fmt.Errorf("on the signed-in route %s must pass (%v):\n%s", s17rTest, err, out)
	}
	return nil
}

// ---- Evidence row ----

func (s *s17rState) gate() (string, error) {
	s17rGateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "harness-gate-s17r-e2e-")
		if err != nil {
			s17rGateErr = err
			return
		}
		s17rGateBin = filepath.Join(dir, "harness-gate")
		if runtime.GOOS == "windows" {
			s17rGateBin += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", s17rGateBin, ".")
		cmd.Dir = filepath.Join(s.repo, "tools", "harness-gate")
		cmd.Env = s17rEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			s17rGateErr = fmt.Errorf("build harness-gate: %v\n%s", err, out)
		}
	})
	return s17rGateBin, s17rGateErr
}

func (s *s17rState) showHEAD(rel string) (string, error) {
	cmd := exec.Command("git", "show", "HEAD:"+rel)
	cmd.Dir = s.repo
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git show HEAD:%s: %v", rel, err)
	}
	return strings.ReplaceAll(string(out), "\r\n", "\n"), nil
}

// s17rSection is the body of the "## <heading>" section of md, up to the next "## ".
func s17rSection(md, heading string) (string, bool) {
	_, after, ok := strings.Cut(md, "\n"+heading+"\n")
	if !ok {
		return "", false
	}
	body, _, _ := strings.Cut(after, "\n## ")
	return body, true
}

func s17rCells(line string) []string {
	t := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), "|"), "|")
	cells := strings.Split(t, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// s17rToolchainRow is the one required TOOLCHAIN-1 row of the record's Scenarios
// table, keyed by lower-case column name, and the table's header.
func s17rToolchainRow(md string) ([]string, map[string]string, error) {
	body, ok := s17rSection(md, "## Scenarios")
	if !ok {
		return nil, nil, errors.New(`the record has no "## Scenarios" section`)
	}
	var header []string
	var found []map[string]string
	for _, l := range strings.Split(body, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "|") {
			if header != nil {
				break
			}
			continue
		}
		cells := s17rCells(l)
		switch {
		case header == nil:
			for _, c := range cells {
				header = append(header, strings.ToLower(c))
			}
		case strings.Trim(strings.Join(cells, ""), "-: ") == "" || len(cells) != len(header):
		default:
			r := map[string]string{}
			for i, h := range header {
				r[h] = cells[i]
			}
			if r["scenario"] == s17rScenario && strings.EqualFold(r["leg"], "required") {
				found = append(found, r)
			}
		}
	}
	if len(found) != 1 {
		return header, nil, fmt.Errorf("the record has %d required %s rows, want 1", len(found), s17rScenario)
	}
	return header, found[0], nil
}

func s17rPassed(r map[string]string) bool {
	l := strings.ToLower(r["result"])
	return l == "passed" || strings.HasPrefix(l, "passed (")
}

// s17rMissing names the M2-DEC-5 identities r lacks: commit, binary checksum, model
// id and run id.
func s17rMissing(r map[string]string) []string {
	var out []string
	if !s17rSHA.MatchString(r["commit"]) {
		out = append(out, "commit")
	}
	a := r["artifacts"]
	if !s17rChecksum.MatchString(a) {
		out = append(out, "binary checksum")
	}
	for _, p := range []struct {
		name string
		re   *regexp.Regexp
	}{{"model id", s17rModelID}, {"run id", s17rRunID}} {
		if m := p.re.FindStringSubmatch(a); m == nil || strings.EqualFold(strings.TrimRight(m[1], ".;,"), "id") {
			out = append(out, p.name)
		}
	}
	return out
}

func (s *s17rState) givenRecordOpen(rel, status string) error {
	md, err := s.showHEAD(rel)
	if err != nil {
		return err
	}
	disk, err := os.ReadFile(filepath.Join(s.repo, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	if strings.ReplaceAll(string(disk), "\r\n", "\n") != md {
		return fmt.Errorf("%s differs from its committed text; the evidence is the committed record", rel)
	}
	if n := strings.Count(md, "\n"+status+"\n"); n != 1 || strings.Count(md, "\nClosure status:") != 1 {
		return fmt.Errorf("the record does not read exactly one %q", status)
	}
	s.record, s.root = md, s.repo
	_, s.row, err = s17rToolchainRow(md)
	return err
}

func (s *s17rState) runReady() error {
	bin, err := s.gate()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "ready", "--stage", s17rStage, "--root", s.root)
	cmd.Env = s17rEnv()
	out, err := cmd.Output()
	s.stdout, s.code = strings.ReplaceAll(string(out), "\r\n", "\n"), 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		s.code = exit.ExitCode()
	case err != nil:
		return err
	}
	if s.code != 0 && s.code != 1 {
		return fmt.Errorf("ready exited %d:\n%s", s.code, s.stdout)
	}
	return nil
}

func (s *s17rState) blockers() []string {
	var out []string
	for _, l := range strings.Split(s.stdout, "\n") {
		if strings.HasPrefix(l, "  - "+s17rScenario+" ") || strings.HasPrefix(l, "  - "+s17rScenario+":") {
			out = append(out, l)
		}
	}
	return out
}

// verdict is the M2-DEC-5 evidence verdict on s.row after ready ran on s.root: the
// row counts only when it reads passed, the identity check finds nothing missing
// and ready names no blocker of it. why lists every reason it does not count.
func (s *s17rState) verdict() (counted bool, why []string) {
	if !s17rPassed(s.row) {
		why = append(why, fmt.Sprintf("result %q", s.row["result"]))
	}
	for _, m := range s17rMissing(s.row) {
		why = append(why, "no valid "+m)
	}
	why = append(why, s.blockers()...)
	return len(why) == 0, why
}

// thenCommittedIdentities checks the committed row's own commit and artifacts, not
// just its result: a copy of the record promotes the row to passed with exactly
// those cells, ready runs on it, and the verdict must count it iff the identity
// check finds all four. The checkout's ready output is restored afterwards.
func (s *s17rState) thenCommittedIdentities() error {
	miss := s17rMissing(s.row)
	if s17rPassed(s.row) && len(miss) > 0 {
		return fmt.Errorf("the committed row reads passed but lacks %s (M2-DEC-5): %v", strings.Join(miss, ", "), s.row)
	}
	row, record, root, stdout, code := s.row, s.record, s.root, s.stdout, s.code
	defer func() { s.row, s.record, s.root, s.stdout, s.code = row, record, root, stdout, code }()
	if err := s.givenCopy(s17rScenario, row["commit"], row["artifacts"]); err != nil {
		return err
	}
	if err := s.runReady(); err != nil {
		return err
	}
	counted, why := s.verdict()
	if counted != (len(miss) == 0) {
		return fmt.Errorf("the committed cells promoted to passed: counted=%v but identity check misses %q (why %q)", counted, miss, why)
	}
	if b := s.blockers(); strings.TrimSpace(row["commit"]) == "" && !slices.ContainsFunc(b, func(l string) bool { return strings.HasSuffix(l, ": ran but records no commit") }) {
		return fmt.Errorf("the committed row has no commit, yet ready on it promoted to passed does not say so: %q", b)
	}
	return nil
}

func (s *s17rState) thenCountedOnlyComplete() error {
	counted, why := s.verdict()
	if complete := s17rPassed(s.row) && len(s17rMissing(s.row)) == 0; counted != complete {
		return fmt.Errorf("verdict counted=%v for a row that is complete=%v: %q", counted, complete, why)
	}
	if s17rPassed(s.row) {
		return nil
	}
	want := fmt.Sprintf("  - %s [%s]: result %q: not run counts as open", s17rScenario, s.row["variant"], s.row["result"])
	if s.code != 1 || !slices.ContainsFunc(s.blockers(), func(l string) bool { return strings.HasPrefix(l, want) }) {
		return fmt.Errorf("ready (exit %d) does not hold the open row with %q:\n%s", s.code, want, s.stdout)
	}
	return nil
}

func (s *s17rState) thenCopiedComplete() error {
	if miss := s17rMissing(s.row); len(miss) > 0 {
		return fmt.Errorf("the copied row lacks %q: %v", miss, s.row)
	}
	return nil
}

func (s *s17rState) thenCopiedCounted() error {
	if counted, why := s.verdict(); !counted {
		return fmt.Errorf("the complete copied row is not counted: %q", why)
	}
	return nil
}

func (s *s17rState) thenCopiedNotCounted() error {
	if counted, _ := s.verdict(); counted {
		return fmt.Errorf("the incomplete copied row is counted: %v\n%s", s.row, s.stdout)
	}
	return nil
}

func (s *s17rState) thenMissingExactly(what string) error {
	if miss := s17rMissing(s.row); !slices.Equal(miss, []string{what}) {
		return fmt.Errorf("identity check misses %q, want exactly [%s]: artifacts %q", miss, what, s.row["artifacts"])
	}
	return nil
}

// thenReadyGap pins ready's own verdict on an identity-incomplete row: today it
// names no blocker, because closure.evidenceBlockers only requires Artifacts to be
// non-empty. It fails once ready learns the check, so the gap cannot close silently.
func (s *s17rState) thenReadyGap(id string) error {
	if b := s.blockers(); id != s17rScenario || len(b) > 0 {
		return fmt.Errorf("ready now names %s for a missing identity, the gap closed; move this case to the refusal outline: %q", id, b)
	}
	if s.code != 1 || !strings.Contains(s.stdout, "NOT READY") {
		return fmt.Errorf("ready exit %d, want 1 for the other open rows:\n%s", s.code, s.stdout)
	}
	return nil
}

func (s *s17rState) thenOwed(heading string) error {
	if s17rPassed(s.row) {
		return nil
	}
	res := strings.TrimSpace(s.row["result"])
	if !strings.HasPrefix(res, "not run (") || !strings.HasSuffix(res, ")") || len(res) <= len("not run ()") {
		return fmt.Errorf("row result %q is neither passed nor \"not run (<reason>)\"", res)
	}
	if c, a := strings.TrimSpace(s.row["commit"]), strings.TrimSpace(s.row["artifacts"]); c != "" || a != "" {
		return fmt.Errorf("a row that did not run claims commit %q and artifacts %q", c, a)
	}
	body, ok := s17rSection(s.record, "## "+heading)
	if !ok || !strings.Contains(body, "What clears it") || strings.Count(body, "\n|") < 3 {
		return fmt.Errorf("the record has no %q section with a table naming what clears it", heading)
	}
	return nil
}

// givenCopy writes the committed index and a copy of the committed record whose
// TOOLCHAIN-1 row reads passed with commit and artifacts into a scratch tree.
func (s *s17rState) givenCopy(scenario, commit, artifacts string) error {
	if scenario != s17rScenario {
		return fmt.Errorf("this stage edits %s, not %s", s17rScenario, scenario)
	}
	md, err := s.showHEAD(s17rRecordRel)
	if err != nil {
		return err
	}
	index, err := s.showHEAD(s17rIndexRel)
	if err != nil {
		return err
	}
	header, _, err := s17rToolchainRow(md)
	if err != nil {
		return err
	}
	lines, edited := strings.Split(md, "\n"), 0
	for i, l := range lines {
		cells := s17rCells(l)
		if !strings.HasPrefix(strings.TrimSpace(l), "|") || len(cells) != len(header) || cells[0] != s17rScenario {
			continue
		}
		for j, h := range header {
			switch h {
			case "environment":
				cells[j] = "E1 wsl:DEVBOX7/Ubuntu-24.04"
			case "commit":
				cells[j] = commit
			case "result":
				cells[j] = "passed"
			case "artifacts":
				cells[j] = artifacts
			}
		}
		lines[i] = "| " + strings.Join(cells, " | ") + " |"
		edited++
	}
	if edited != 1 {
		return fmt.Errorf("edited %d %s rows, want 1", edited, s17rScenario)
	}
	s.root = s17rOuterT.TempDir()
	for rel, text := range map[string]string{s17rRecordRel: strings.Join(lines, "\n"), s17rIndexRel: index} {
		if err := s17rWriteFile(filepath.Join(s.root, filepath.FromSlash(rel)), []byte(text)); err != nil {
			return err
		}
	}
	s.record = strings.Join(lines, "\n")
	_, s.row, err = s17rToolchainRow(s.record)
	return err
}

func (s *s17rState) thenExitsOne() error {
	if s.code != 1 || !strings.Contains(s.stdout, "NOT READY") {
		return fmt.Errorf("ready exit %d, want 1 for the other open rows:\n%s", s.code, s.stdout)
	}
	return nil
}

func (s *s17rState) thenNoBlocker(id string) error {
	if b := s.blockers(); id != s17rScenario || len(b) > 0 {
		return fmt.Errorf("ready still names %s: %q", id, b)
	}
	return nil
}

func (s *s17rState) thenNamesBlocker(line string) error {
	if s.code != 1 || !slices.ContainsFunc(strings.Split(s.stdout, "\n"), func(l string) bool { return strings.HasPrefix(l, "  - "+line) }) {
		return fmt.Errorf("ready (exit %d) does not name %q:\n%s", s.code, line, s.stdout)
	}
	return nil
}

func InitializeScenario_dh_s_17_acceptance_on_a_real_model_record_then_replay_without_a_key(ctx *godog.ScenarioContext) {
	s := &s17rState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = s17rState{}
		module, err := s17rModuleDir()
		if err != nil {
			return c, err
		}
		s.module, s.repo = module, filepath.Dir(filepath.Dir(module))
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if s.route != nil {
			s.route.srv.Close()
		}
		return c, err
	})

	ctx.Step(`^dsh built from this checkout with the testhook tag$`, s.givenBuilt)
	ctx.Step(`^a scratch home whose route "([^"]*)" is served by an in-process model instance$`, s.givenRoute)
	ctx.Step(`^the scenario records the session "([^"]*)" on route "([^"]*)" into a scratch workspace$`, s.whenRecords)
	ctx.Step(`^it replays the recording twice with the route disabled and no key, each into a fresh scratch home$`, s.whenReplaysTwice)
	ctx.Step(`^the recording holds (\d+) model requests, each sent to the route exactly once$`, s.thenRequestsRecorded)
	ctx.Step(`^each recorded request rebuilt from the persisted log has the digest the route received$`, s.thenRebuiltDigests)
	ctx.Step(`^the route received no request during either replay, and no claude client started$`, s.thenRouteUntouched)
	ctx.Step(`^every replayed attempt is served by the replay, never by the route$`, s.thenServedByReplay)
	ctx.Step(`^each replayed persisted log read back from its SQLite store equals the recorded log after normalization$`, s.thenLogsEqual)
	ctx.Step(`^each replayed workspace equals the recorded workspace$`, s.thenWorkspacesEqual)
	ctx.Step(`^each replayed model request equals the recorded request after normalization$`, s.thenRequestsEqual)
	ctx.Step(`^on the S17 machine TestRecordThenReplayWithoutAKey also passes on the signed-in route$`, s.thenLiveOnS17)

	ctx.Step(`^the committed record "([^"]*)" still reading "([^"]*)"$`, s.givenRecordOpen)
	ctx.Step(`^"harness-gate ready --stage DH-S-17" reads the "DH-E-TOOLCHAIN-1" row of the checkout$`, s.runReady)
	ctx.Step(`^the M2-DEC-5 identity check of the committed row finds every one of commit, binary checksum, model id and run id, or the row is not passed$`, s.thenCommittedIdentities)
	ctx.Step(`^the evidence verdict counts the committed row only when it reads passed with commit, binary checksum, model id and run id$`, s.thenCountedOnlyComplete)
	ctx.Step(`^a row that is not passed claims no commit and no artifacts, reads "not run \(<reason>\)" and the "([^"]*)" section names what clears it$`, s.thenOwed)
	ctx.Step(`^the M2-DEC-5 identity check of the copied row finds nothing missing$`, s.thenCopiedComplete)
	ctx.Step(`^the evidence verdict counts the copied row$`, s.thenCopiedCounted)
	ctx.Step(`^the evidence verdict does not count the copied row$`, s.thenCopiedNotCounted)
	ctx.Step(`^the M2-DEC-5 identity check of the copied row finds exactly its (binary checksum|model id|run id) missing$`, s.thenMissingExactly)
	ctx.Step(`^ready itself names no blocker of "([^"]*)", the gate gap: it checks only that Artifacts is non-empty$`, s.thenReadyGap)
	ctx.Step(`^a copy of the committed index and record whose "([^"]*)" row reads passed at commit "([^"]*)" with artifacts "([^"]*)"$`, s.givenCopy)
	ctx.Step(`^"harness-gate ready --stage DH-S-17" runs on the copy$`, s.runReady)
	ctx.Step(`^it exits 1 for the other open rows$`, s.thenExitsOne)
	ctx.Step(`^it names no blocker of "([^"]*)"$`, s.thenNoBlocker)
	ctx.Step(`^it names the blocker "(.*)"$`, s.thenNamesBlocker)
}

func TestE2E_dh_s_17_acceptance_on_a_real_model_record_then_replay_without_a_key(t *testing.T) {
	s17rOuterT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_17_acceptance_on_a_real_model_record_then_replay_without_a_key,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_17_acceptance_on_a_real_model_record_then_replay_without_a_key.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the record-then-replay e2e scenarios failed")
	}
}
