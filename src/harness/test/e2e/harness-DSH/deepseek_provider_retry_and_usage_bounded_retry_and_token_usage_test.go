//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"iter"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/meter"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/deepseek"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/transport"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
)

// brtFakeKey is the API key the secret store resolves; it is not a real key.
const brtFakeKey = "sk-dsh-e2e-brt-000000000000"

var (
	brtBG     = context.Background()
	brtOuterT *testing.T
	brtSSEDir = filepath.Join("..", "..", "..", "internal", "llm", "testdata", "sse")
)

type brtAskPayload struct {
	Hint string `json:"hint"`
}

// brtAsk is an e2e plugin event type that describes an auxiliary request, the
// only kind Runtime.Complete takes.
var brtAsk = func() *event.Type[brtAskPayload] {
	typ := event.MustDefine(event.Default(), event.Spec[brtAskPayload]{Name: "ext/e2e-brt-summary", Version: 1})
	if err := surface.DeclareBuilder(event.Default(), typ.Name(), func(ev event.Event, at surface.View, b *surface.AuxBuilder) error {
		b.Purpose("summary")
		b.History(at.History())
		m, err := vocab.RestoreMessage("brt-summary-ask", vocab.RoleUser, nil, vocab.TextBlock("summarise"))
		if err != nil {
			return err
		}
		b.Message(m)
		return nil
	}); err != nil {
		panic(err)
	}
	return typ
}()

type brtFixture struct {
	status int
	header http.Header
	body   []byte
}

func brtLoadFixture(name string) (brtFixture, error) {
	raw, err := os.ReadFile(filepath.Join(brtSSEDir, name+".http"))
	if err != nil {
		return brtFixture{}, err
	}
	if bytes.Contains(raw, []byte("sk-")) {
		return brtFixture{}, fmt.Errorf("fixture %s holds a key", name)
	}
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(raw)), nil)
	if err != nil {
		return brtFixture{}, fmt.Errorf("parse fixture %s: %w", name, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return brtFixture{}, err
	}
	return brtFixture{status: resp.StatusCode, header: resp.Header, body: body}, nil
}

// brtSeqRT is the in-memory fake transport: it serves the fixtures in order,
// one per request, and fails any request past the last.
type brtSeqRT struct {
	fs    []brtFixture
	mu    sync.Mutex
	n     int
	extra int
}

func (rt *brtSeqRT) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	if rt.n >= len(rt.fs) {
		rt.extra++
		return nil, errors.New("brt: no fixture left")
	}
	f := rt.fs[rt.n]
	rt.n++
	return &http.Response{StatusCode: f.status, Status: http.StatusText(f.status), Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: f.header.Clone(), Body: io.NopCloser(bytes.NewReader(f.body)), Request: req}, nil
}

func (rt *brtSeqRT) requests() (served, extra int) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.n, rt.extra
}

// brtSink logs each settlement record as one event, as a caller would.
type brtSink struct {
	h       session.Handle
	records []llm.Attempt
}

func (s *brtSink) Settle(a llm.Attempt) error {
	s.records = append(s.records, a)
	d, err := a.Draft()
	if err != nil {
		return err
	}
	_, err = s.h.Append(brtBG, d)
	return err
}

// brtOther is a second route's adapter that would succeed if it were ever used.
type brtOther struct {
	prepares atomic.Int32
	attempts atomic.Int32
}

func (o *brtOther) Prepare(context.Context, vocab.RouteID, string, vocab.CallConfig) (llm.Call, error) {
	o.prepares.Add(1)
	return o, nil
}

func (o *brtOther) Attempt(context.Context, session.Request) iter.Seq2[vocab.Chunk, error] {
	o.attempts.Add(1)
	return func(yield func(vocab.Chunk, error) bool) {
		for _, c := range []vocab.Chunk{
			vocab.BlockStart{Index: 0, Kind: vocab.KindText},
			vocab.TextDelta{Index: 0, Text: "from elsewhere"},
			vocab.Finish{Reason: vocab.FinishStop},
		} {
			if !yield(c, nil) {
				return
			}
		}
	}
}

type brtResult struct {
	msg vocab.Message
	err error
}

type brtState struct {
	cleanups []func()

	// retry scenarios
	h     session.Handle
	req   session.Request
	start event.Offset
	tr    *brtSeqRT
	clk   *clock.Manual
	rt    *llm.Runtime
	waits []time.Duration
	wmu   sync.Mutex
	sink  *brtSink
	other *brtOther
	res   brtResult

	// usage scenario
	usageH    session.Handle
	settled   []event.Seq
	usage     meter.Usage
	reading   meter.Reading
	usageEvs  []event.Event
	firstSeen int

	// DH-S-9 closure scenario
	root     string
	ids      []string
	report   brtGateReport
	vetOut   string
	vetErr   error
	gateErr  error
	gateOutp string
}

func (s *brtState) cleanup(f func()) { s.cleanups = append(s.cleanups, f) }

func (s *brtState) close() {
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
	s.cleanups = nil
}

func brtNewSession(s *brtState) (session.Handle, error) {
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	s.cleanup(root.Dispose)
	svc := session.NewService(root, memstore.New(), session.Config{})
	h, err := svc.Create(brtBG, nil, session.CreateOptions{Scope: scope.Key{}})
	if err != nil {
		return nil, err
	}
	s.cleanup(func() { _ = h.Close(brtBG) })
	sys, err := vocab.RestoreMessage("s1", vocab.RoleSystem, nil, vocab.TextBlock("Be brief."))
	if err != nil {
		return nil, err
	}
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("hello"))
	if err != nil {
		return nil, err
	}
	var drafts []event.Draft
	for _, mk := range []func() (event.Draft, error){
		func() (event.Draft, error) { return event.TurnStartType.Draft(event.TurnStart{Turn: 1}) },
		func() (event.Draft, error) { return event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1}) },
		func() (event.Draft, error) {
			return event.RequestHeader.Draft(event.RequestHeaderPayload{Route: deepseek.RouteID})
		},
		func() (event.Draft, error) {
			return event.SystemMessage.Draft(event.Append(), event.MessagePayload{Message: sys})
		},
		func() (event.Draft, error) {
			return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
		},
	} {
		d, err := mk()
		if err != nil {
			return nil, err
		}
		drafts = append(drafts, d)
	}
	if _, err := h.AppendGroup(brtBG, drafts); err != nil {
		return nil, err
	}
	return h, nil
}

func brtKeyStore() *secret.Store {
	return secret.NewStore(secret.EnvSource(func(name string) (string, bool) {
		if name == deepseek.KeyRef.EnvName() {
			return brtFakeKey, true
		}
		return "", false
	}))
}

func brtSplit(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---- retry scenarios ----

func (s *brtState) anAuxiliaryRequest() error {
	h, err := brtNewSession(s)
	if err != nil {
		return err
	}
	d, err := brtAsk.Draft(brtAskPayload{Hint: "x"})
	if err != nil {
		return err
	}
	ask, err := h.Append(brtBG, d)
	if err != nil {
		return err
	}
	req, err := h.RequestFrom(ask.Seq)
	if err != nil {
		return err
	}
	if !req.Meta().Auxiliary() {
		return errors.New("the request is not auxiliary")
	}
	s.h, s.req, s.sink = h, req, &brtSink{h: h}
	return nil
}

func (s *brtState) fixturesInOrder(list string) error {
	var fs []brtFixture
	for _, name := range brtSplit(list) {
		f, err := brtLoadFixture(name)
		if err != nil {
			return err
		}
		fs = append(fs, f)
	}
	if len(fs) == 0 {
		return errors.New("no fixture named")
	}
	s.tr = &brtSeqRT{fs: fs}
	return nil
}

func (s *brtState) registerDeepseek(mode string) error {
	if s.tr == nil {
		return errors.New("no fake transport")
	}
	s.clk = clock.NewManual(time.Unix(1_700_000_000, 0))
	s.rt = llm.New(
		llm.WithClock(func() int64 { return s.clk.Now().UnixMilli() }),
		llm.WithTimer(func(d time.Duration) <-chan time.Time {
			s.wmu.Lock()
			s.waits = append(s.waits, d)
			s.wmu.Unlock()
			return s.clk.After(d)
		}),
		llm.WithJitter(func() float64 { return 0.5 }),
	)
	cfg := deepseek.Config{Secrets: brtKeyStore(), Transport: transport.Over(s.tr, transport.Options{})}
	if mode == "persistent" {
		cfg.Retry = llm.PersistentRetry()
	}
	route, err := deepseek.Route(cfg)
	if err != nil {
		return err
	}
	if mode == "default" && route.Retry.Normalised() != llm.DefaultRetry() {
		return fmt.Errorf("deepseek route retry %+v, want the bounded default %+v", route.Retry, llm.DefaultRetry())
	}
	_, err = s.rt.Register("deepseek", route)
	return err
}

func (s *brtState) secondRoute(id string) error {
	s.other = &brtOther{}
	_, err := s.rt.Register("other", llm.Route{ID: vocab.RouteID(id), Models: []llm.Model{{Name: "m1"}}, Adapter: s.other, Retry: llm.DefaultRetry()})
	return err
}

func (s *brtState) noted() []time.Duration {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return append([]time.Duration(nil), s.waits...)
}

// theCallRuns runs Complete and, each time it blocks on the virtual clock,
// advances the clock by exactly the wait it asked for.
func (s *brtState) theCallRuns() error {
	if s.rt == nil || s.sink == nil {
		return errors.New("runtime or request not set up")
	}
	start := s.h.Len()
	s.start = start
	ctx, cancel := context.WithCancel(brtBG)
	s.cleanup(cancel)
	done := make(chan brtResult, 1)
	go func() {
		m, err := s.rt.Complete(ctx, s.req, s.sink)
		done <- brtResult{m, err}
	}()
	deadline := time.After(30 * time.Second)
	advanced := 0
	for {
		select {
		case r := <-done:
			s.res = r
			return nil
		case <-deadline:
			return errors.New("Complete never returned")
		default:
		}
		if !s.clk.BlockUntil(1, 20*time.Millisecond) {
			continue
		}
		w := s.noted()
		if len(w) <= advanced {
			return fmt.Errorf("a waiter on the clock with %d waits noted, %d advanced", len(w), advanced)
		}
		if advanced >= 10 {
			return fmt.Errorf("more than 10 waits: %v", w)
		}
		s.clk.Advance(w[advanced])
		advanced++
	}
}

func (s *brtState) callReturnsMessage() error {
	if s.res.err != nil {
		return fmt.Errorf("Complete = %v", s.res.err)
	}
	if len(s.res.msg.Blocks()) == 0 {
		return errors.New("Complete returned no message")
	}
	last := s.sink.records[len(s.sink.records)-1]
	if !last.Succeeded() || last.Message == nil || !reflect.DeepEqual(last.Message.Blocks(), s.res.msg.Blocks()) {
		return fmt.Errorf("the returned message is not the successful attempt's: %+v", s.res.msg.Blocks())
	}
	return nil
}

func (s *brtState) logHolds(list string) error {
	want := brtSplit(list)
	var got []string
	for _, a := range s.sink.records {
		d, err := a.Draft()
		if err != nil {
			return err
		}
		got = append(got, d.Type())
	}
	if !slices.Equal(got, want) {
		return fmt.Errorf("settlement records %v, want %v", got, want)
	}
	if grew := int(s.h.Len() - s.start); grew != len(want) {
		return fmt.Errorf("the log grew by %d events, want %d settlements", grew, len(want))
	}
	evs, err := s.h.Read(brtBG, s.start, 0)
	if err != nil {
		return err
	}
	var logged []string
	for _, ev := range evs {
		logged = append(logged, ev.Type())
	}
	if !slices.Equal(logged, want) {
		return fmt.Errorf("logged %v, want %v", logged, want)
	}
	return nil
}

func (s *brtState) failedCarryRetryAfter(n, secs int) error {
	if len(s.sink.records) < n {
		return fmt.Errorf("%d settlements, want at least %d", len(s.sink.records), n)
	}
	for i, a := range s.sink.records[:n] {
		f := a.Finish.Failure
		if a.Succeeded() || f == nil || f.Class != vocab.Transient || f.RetryAfter != time.Duration(secs)*time.Second {
			return fmt.Errorf("settlement %d: %+v, want a transient failure with Retry-After %ds", i+1, a.Finish, secs)
		}
	}
	return nil
}

func (s *brtState) waitsWere(list string) error {
	var want []time.Duration
	for _, p := range brtSplit(list) {
		d, err := time.ParseDuration(p)
		if err != nil {
			return err
		}
		want = append(want, d)
	}
	if got := s.noted(); !slices.Equal(got, want) {
		return fmt.Errorf("waits %v, want %v", got, want)
	}
	return nil
}

func (s *brtState) transportSaw(n int) error {
	served, extra := s.tr.requests()
	if served != n || extra != 0 {
		return fmt.Errorf("transport served %d requests (%d past the fixtures), want exactly %d", served, extra, n)
	}
	if len(s.sink.records) != n {
		return fmt.Errorf("%d settlements for %d requests", len(s.sink.records), n)
	}
	return nil
}

func (s *brtState) callFails(code, class string) error {
	var fl *vocab.Failure
	if !errors.As(s.res.err, &fl) {
		return fmt.Errorf("Complete = %v, want a *vocab.Failure", s.res.err)
	}
	if string(fl.Code) != code || string(fl.Class) != class {
		return fmt.Errorf("Complete failed %s/%s, want %s/%s", fl.Code, fl.Class, code, class)
	}
	return nil
}

func (s *brtState) onlySettlement(route, code, class string) error {
	if len(s.sink.records) != 1 {
		return fmt.Errorf("%d settlements, want exactly 1", len(s.sink.records))
	}
	a := s.sink.records[0]
	if string(a.Route) != route {
		return fmt.Errorf("settled on route %s, want %s", a.Route, route)
	}
	f := a.Finish.Failure
	if f == nil || string(f.Code) != code || string(f.Class) != class {
		return fmt.Errorf("settled failure %+v, want %s/%s", f, code, class)
	}
	return nil
}

func (s *brtState) otherNeverUsed(string) error {
	if s.other == nil {
		return errors.New("no second route")
	}
	if p, a := s.other.prepares.Load(), s.other.attempts.Load(); p != 0 || a != 0 {
		return fmt.Errorf("the second route was prepared %d and attempted %d times", p, a)
	}
	return nil
}

// ---- usage scenario ----

func (s *brtState) sessionWithSettlements(tbl *godog.Table) error {
	h, err := brtNewSession(s)
	if err != nil {
		return err
	}
	s.usageH = h
	asOf := event.Seq(h.Len() - 1)
	atoi := func(c string) (int, error) { return strconv.Atoi(strings.TrimSpace(c)) }
	s.firstSeen = int(h.Len())
	for i, row := range tbl.Rows[1:] {
		var n [4]int
		for j := range n {
			if n[j], err = atoi(row.Cells[j+1].Value); err != nil {
				return err
			}
		}
		u := &vocab.Usage{Input: n[0], CacheRead: n[1], CacheWrite: n[2], Output: n[3]}
		kind := strings.TrimSpace(row.Cells[0].Value)
		var d event.Draft
		switch kind {
		case event.AssistantAttempt.Name():
			f, err := vocab.NewFailure(vocab.CodeServer, "overloaded")
			if err != nil {
				return err
			}
			d, err = event.AssistantAttempt.Draft(event.AssistantAttemptPayload{RequestAsOf: &asOf, RequestDigest: "sha256:ab", Usage: u, Failure: f})
			if err != nil {
				return err
			}
		case event.AssistantMessage.Name():
			m, err := vocab.RestoreMessage(fmt.Sprintf("a%d", i), vocab.RoleAssistant, nil, vocab.TextBlock("answer"))
			if err != nil {
				return err
			}
			d, err = event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: m, RequestAsOf: &asOf, RequestDigest: "sha256:ab", Usage: u})
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown settlement kind %q", kind)
		}
		r, err := h.Append(brtBG, d)
		if err != nil {
			return fmt.Errorf("append %s: %w", kind, err)
		}
		s.settled = append(s.settled, r.Seq)
		asOf = r.Seq
		if i == 0 {
			// The retry adds no user message; its request is as of the failed attempt.
			continue
		}
		if i == 1 {
			// The next step's request: the user speaks again after the retried step.
			um, err := vocab.RestoreMessage("u2", vocab.RoleUser, nil, vocab.TextBlock("more"))
			if err != nil {
				return err
			}
			ud, err := event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: um})
			if err != nil {
				return err
			}
			ur, err := h.Append(brtBG, ud)
			if err != nil {
				return err
			}
			asOf = ur.Seq
		}
	}
	if len(s.settled) != 3 {
		return fmt.Errorf("%d settlements, want 3", len(s.settled))
	}
	return nil
}

func (s *brtState) usageFoldRuns() error {
	m, err := meter.New(views.New(), meter.DefaultConfig())
	if err != nil {
		return err
	}
	if s.usage, err = m.Usage(brtBG, s.usageH); err != nil {
		return err
	}
	if s.reading, err = m.Pressure(brtBG, s.usageH); err != nil {
		return err
	}
	s.usageEvs, err = s.usageH.Read(brtBG, 0, 0)
	return err
}

func (s *brtState) billedIs(in, cr, cw, out int) error {
	want := vocab.Usage{Input: in, CacheRead: cr, CacheWrite: cw, Output: out}
	if s.usage.Billed != want {
		return fmt.Errorf("billed %+v, want %+v", s.usage.Billed, want)
	}
	return nil
}

func (s *brtState) countedAttempts(attempts, reported, total int) error {
	if s.usage.Attempts != attempts || s.usage.Reported != reported || meter.Total(s.usage.Billed) != total {
		return fmt.Errorf("attempts %d, reported %d, total %d; want %d, %d, %d",
			s.usage.Attempts, s.usage.Reported, meter.Total(s.usage.Billed), attempts, reported, total)
	}
	return nil
}

func (s *brtState) lastTotals(total int) error {
	if meter.Total(s.usage.Last) != total || s.usage.LastSeq != s.settled[len(s.settled)-1] {
		return fmt.Errorf("last %+v at %d, want %d tokens at %d", s.usage.Last, s.usage.LastSeq, total, s.settled[len(s.settled)-1])
	}
	return nil
}

func (s *brtState) occupancy(tokens int, frac float64, window int) error {
	if s.reading.Measured || s.reading.Tokens != tokens || s.reading.Fraction(window) != frac {
		return fmt.Errorf("pressure %+v (fraction %v), want %d tokens projected, %v of %d", s.reading, s.reading.Fraction(window), tokens, frac, window)
	}
	return nil
}

func (s *brtState) checkpointEqualsZero() error {
	d := meter.UsageView()
	whole := views.Refold(d, session.Header{}, s.usageEvs)
	if !reflect.DeepEqual(whole, s.usage) {
		return fmt.Errorf("refold %+v, service %+v", whole, s.usage)
	}
	cut := slices.IndexFunc(s.usageEvs, func(ev event.Event) bool { return ev.Seq() == s.settled[0] }) + 1
	if cut <= 0 {
		return errors.New("first settlement not in the log")
	}
	row, err := json.Marshal(views.Refold(d, session.Header{}, s.usageEvs[:cut]))
	if err != nil {
		return err
	}
	var restored meter.Usage
	if err := json.Unmarshal(row, &restored); err != nil {
		return err
	}
	if from := views.FoldFrom(d, restored, s.usageEvs[cut:]); !reflect.DeepEqual(from, whole) {
		return fmt.Errorf("from a checkpoint %+v, from zero %+v", from, whole)
	}
	return nil
}

// ---- DH-S-9 closure scenario ----

type brtEntry struct {
	Stage    string   `json:"stage"`
	ID       string   `json:"id"`
	Authored string   `json:"authored"`
	Requires []string `json:"requires"`
}

type brtScenario struct {
	ID   string `json:"id"`
	File string `json:"file"`
	Test string `json:"test"`
}

type brtGateReport struct {
	BuiltThrough string            `json:"built_through"`
	Entries      []brtEntry        `json:"entries"`
	Scenarios    []brtScenario     `json:"scenarios"`
	CheckErr     string            `json:"check_err"`
	BoundaryErr  string            `json:"boundary_err"`
	Preflight    map[string]string `json:"preflight"`
}

// brtGateProgram runs the gate's own scenario-index check (acceptance.Check and
// acceptance.CheckBoundary over the index and the scanned scenario files, as
// tools/harness-gate's scenario-index gate does) and the preflight's verdict on
// requiring each ID, and prints what it read.
const brtGateProgram = `package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

type entry struct {
	Stage    string   ` + "`json:\"stage\"`" + `
	ID       string   ` + "`json:\"id\"`" + `
	Authored string   ` + "`json:\"authored\"`" + `
	Requires []string ` + "`json:\"requires\"`" + `
}

type scenario struct {
	ID   string ` + "`json:\"id\"`" + `
	File string ` + "`json:\"file\"`" + `
	Test string ` + "`json:\"test\"`" + `
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func main() {
	root := os.Args[1]
	x, err := acceptance.ReadIndex(root)
	if err != nil {
		panic(err)
	}
	var mods []string
	for _, m := range []string{"src/go", "src/sdk", "src/harness"} {
		mods = append(mods, filepath.Join(root, filepath.FromSlash(m)))
	}
	sc, err := acceptance.ScanScenarios(root, mods)
	if err != nil {
		panic(err)
	}
	out := map[string]any{"built_through": x.BuiltThrough}
	var es []entry
	for _, s := range x.Stages {
		for _, e := range s.Scenarios {
			es = append(es, entry{s.ID, e.ID, e.Authored, e.Requires})
		}
	}
	var ss []scenario
	for _, s := range sc {
		ss = append(ss, scenario{s.ID, s.File, s.Test})
	}
	out["entries"], out["scenarios"] = es, ss
	out["check_err"] = errString(acceptance.Check(x, sc))
	out["boundary_err"] = errString(acceptance.CheckBoundary(root, x, sc))
	pre := map[string]string{}
	for _, id := range os.Args[2:] {
		_, err := acceptance.Preflight(map[string]string{acceptance.RequireEnv: id}, x, sc)
		pre[id] = errString(err)
	}
	out["preflight"] = pre
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		panic(err)
	}
}
`

func brtIsRegular(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func brtRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if brtIsRegular(filepath.Join(dir, "go.work")) && brtIsRegular(filepath.Join(dir, "test", "harness", "acceptance-index.yaml")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no repository root (go.work + test/harness/acceptance-index.yaml) above the working directory")
		}
		dir = parent
	}
}

func brtCommand(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (s *brtState) givenScenarioFiles(list, index string) error {
	root, err := brtRepoRoot()
	if err != nil {
		return err
	}
	s.root = root
	s.ids = brtSplit(list)
	if !brtIsRegular(filepath.Join(root, filepath.FromSlash(index))) {
		return fmt.Errorf("%s is missing", index)
	}
	dir := filepath.Join(root, "src", "harness", "internal", "testkit", "scenarios")
	found := map[string]bool{}
	files, err := filepath.Glob(filepath.Join(dir, "*_scenario_test.go"))
	if err != nil {
		return err
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		for _, id := range s.ids {
			if regexp.MustCompile(`(?m)^//dsh:scenario\s+` + regexp.QuoteMeta(id) + `\s*$`).Match(data) {
				found[id] = true
			}
		}
	}
	for _, id := range s.ids {
		if !found[id] {
			return fmt.Errorf("no scenario file binds %s", id)
		}
	}
	return nil
}

func (s *brtState) runIndexCheck() error {
	tmp := brtOuterT.TempDir()
	gate := filepath.Join(s.root, "tools", "harness-gate")
	gomod := fmt.Sprintf("module dsh.e2e/brtindex\n\ngo 1.26.0\n\nrequire github.com/smartpcr/agent-plugin/tools/harness-gate v0.0.0\n\nreplace github.com/smartpcr/agent-plugin/tools/harness-gate => %s\n",
		strconv.Quote(filepath.ToSlash(gate)))
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(gomod), 0o644); err != nil {
		return err
	}
	sum, err := os.ReadFile(filepath.Join(gate, "go.sum"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "go.sum"), sum, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte(brtGateProgram), 0o644); err != nil {
		return err
	}
	args := append([]string{"run", ".", s.root}, s.ids...)
	out, err := brtCommand(tmp, []string{"GOWORK=off", "GOFLAGS=-mod=mod"}, "go", args...)
	if err != nil {
		return fmt.Errorf("the gate's index check: %v\n%s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &s.report); err != nil {
		return fmt.Errorf("index check output: %v\n%s", err, out)
	}
	return nil
}

func (s *brtState) runVet(command string) error {
	args := strings.Fields(command)
	if len(args) < 2 || args[0] != "go" {
		return fmt.Errorf("unexpected command %q", command)
	}
	s.vetOut, s.vetErr = brtCommand(s.root, nil, "go", args[1:]...)
	return nil
}

func (s *brtState) indexCheckPasses() error {
	if s.report.CheckErr != "" {
		return fmt.Errorf("acceptance.Check: %s", s.report.CheckErr)
	}
	if s.report.BoundaryErr != "" {
		return fmt.Errorf("acceptance.CheckBoundary: %s", s.report.BoundaryErr)
	}
	return nil
}

func (s *brtState) bindings(id string) []brtScenario {
	var out []brtScenario
	for _, sc := range s.report.Scenarios {
		if sc.ID == id {
			out = append(out, sc)
		}
	}
	return out
}

func (s *brtState) exactlyOneFile() error {
	for _, id := range s.ids {
		bs := s.bindings(id)
		if len(bs) != 1 {
			return fmt.Errorf("%s is bound %d times: %+v", id, len(bs), bs)
		}
		if !strings.HasPrefix(bs[0].Test, "Test") || !strings.HasSuffix(bs[0].File, "_scenario_test.go") {
			return fmt.Errorf("%s binding %+v", id, bs[0])
		}
	}
	return nil
}

func (s *brtState) oneIndexEntry(stage, authored, requires string) error {
	want := brtSplit(requires)
	for _, id := range s.ids {
		var es []brtEntry
		for _, e := range s.report.Entries {
			if e.ID == id {
				es = append(es, e)
			}
		}
		if len(es) != 1 {
			return fmt.Errorf("%s has %d index entries: %+v", id, len(es), es)
		}
		e := es[0]
		if e.Stage != stage || e.Authored != authored || !slices.Equal(e.Requires, want) {
			return fmt.Errorf("%s entry %+v, want under %s authored %s requiring %v", id, e, stage, authored, want)
		}
	}
	return nil
}

func (s *brtState) imports(id string) ([]string, error) {
	bs := s.bindings(id)
	if len(bs) != 1 {
		return nil, fmt.Errorf("%s has %d files", id, len(bs))
	}
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(s.root, filepath.FromSlash(bs[0].File)), nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, spec := range f.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// brtLauncherImport derives the test kit launcher's import path from the
// harness module's go.mod and fails when the launcher package is missing,
// so a stale or moved launcher path cannot pass importsLimited silently.
func (s *brtState) brtLauncherImport() (string, error) {
	harness := filepath.Join(s.root, "src", "harness")
	data, err := os.ReadFile(filepath.Join(harness, "go.mod"))
	if err != nil {
		return "", err
	}
	var module string
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			module = strings.Trim(strings.TrimSpace(rest), `"`)
			break
		}
	}
	if module == "" {
		return "", fmt.Errorf("%s has no module directive", filepath.Join(harness, "go.mod"))
	}
	dir := filepath.Join(harness, "internal", "testkit", "launcher")
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		pf, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.PackageClauseOnly)
		if err != nil {
			return "", err
		}
		if pf.Name.Name == "launcher" {
			return module + "/internal/testkit/launcher", nil
		}
	}
	return "", fmt.Errorf("the test kit's scenario launcher package is missing from %s", dir)
}

func (s *brtState) importsLimited(string) error {
	const sdk = "github.com/smartpcr/agent-plugin/src/sdk/"
	launcher, err := s.brtLauncherImport()
	if err != nil {
		return err
	}
	for _, id := range s.ids {
		ps, err := s.imports(id)
		if err != nil {
			return err
		}
		for _, p := range ps {
			first, _, _ := strings.Cut(p, "/")
			if strings.Contains(first, ".") && !strings.HasPrefix(p, sdk) && p != launcher {
				return fmt.Errorf("%s imports %s", id, p)
			}
		}
	}
	return nil
}

func (s *brtState) noForbiddenImport(list string) error {
	for _, id := range s.ids {
		ps, err := s.imports(id)
		if err != nil {
			return err
		}
		for _, p := range ps {
			for _, bad := range brtSplit(list) {
				if strings.Contains(p, "/"+bad) {
					return fmt.Errorf("%s imports %s (%s)", id, p, bad)
				}
			}
		}
	}
	return nil
}

func (s *brtState) vetExits0() error {
	if s.vetErr != nil {
		return fmt.Errorf("vet -tags scenario: %v\n%s", s.vetErr, s.vetOut)
	}
	return nil
}

// defaultRunNone lists the tests of the scenario package with the gate's
// explicit empty tag list: none of the DH-S-9 tests is compiled in; with -tags
// scenario every one is.
func (s *brtState) defaultRunNone() error {
	pkgs := map[string]bool{}
	var tests []string
	for _, id := range s.ids {
		b := s.bindings(id)[0]
		pkgs["./"+strings.TrimPrefix(filepath.ToSlash(filepath.Dir(b.File)), "src/harness/")] = true
		tests = append(tests, b.Test)
	}
	var list []string
	for p := range pkgs {
		list = append(list, p)
	}
	sort.Strings(list)
	harness := filepath.Join(s.root, "src", "harness")
	out, err := brtCommand(harness, []string{"GOFLAGS="}, "go", append([]string{"test", "-tags=", "-list", ".", "-count=1"}, list...)...)
	if err != nil {
		return fmt.Errorf("go test -tags= -list: %v\n%s", err, out)
	}
	tagged, err := brtCommand(harness, []string{"GOFLAGS="}, "go", append([]string{"test", "-tags", "scenario", "-list", ".", "-count=1"}, list...)...)
	if err != nil {
		return fmt.Errorf("go test -tags scenario -list: %v\n%s", err, tagged)
	}
	for _, name := range tests {
		re := regexp.MustCompile(`(?m)^` + name + `\s*$`)
		if re.MatchString(out) {
			return fmt.Errorf("the default run includes %s:\n%s", name, out)
		}
		if !re.MatchString(tagged) {
			return fmt.Errorf("-tags scenario does not list %s:\n%s", name, tagged)
		}
	}
	return nil
}

func (s *brtState) preflightRefuses(stages string) error {
	for _, id := range s.ids {
		msg, ok := s.report.Preflight[id]
		if !ok {
			return fmt.Errorf("no preflight verdict for %s", id)
		}
		if msg == "" {
			return fmt.Errorf("the preflight admits %s before %s are built (built through %s)", id, stages, s.report.BuiltThrough)
		}
		for _, st := range brtSplit(stages) {
			if !strings.Contains(msg, st) || !strings.Contains(msg, "not built") {
				return fmt.Errorf("the preflight refuses %s for another reason: %s", id, msg)
			}
		}
	}
	return nil
}

func InitializeScenario_deepseek_provider_retry_and_usage_bounded_retry_and_token_usage(ctx *godog.ScenarioContext) {
	s := &brtState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		s.close()
		*s = brtState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return c, nil
	})

	ctx.Step(`^an auxiliary request on a deepseek-official session$`, s.anAuxiliaryRequest)
	ctx.Step(`^the fake transport serving the SSE fixtures "([^"]*)" in order$`, s.fixturesInOrder)
	ctx.Step(`^the deepseek-official route registered with its (default|persistent) retry policy on the virtual clock$`, s.registerDeepseek)
	ctx.Step(`^the deepseek-official route registered with the (default|persistent) retry policy on the virtual clock$`, s.registerDeepseek)
	ctx.Step(`^a second working route "([^"]*)" registered on the same runtime$`, s.secondRoute)
	ctx.Step(`^the call runs, the virtual clock advanced by each wait$`, s.theCallRuns)
	ctx.Step(`^the call returns the successful attempt's message$`, s.callReturnsMessage)
	ctx.Step(`^the log holds the settlements "([^"]*)" in order$`, s.logHolds)
	ctx.Step(`^the (\d+) failed settlements carry a transient failure asking a Retry-After of (\d+) s$`, s.failedCarryRetryAfter)
	ctx.Step(`^the waits on the virtual clock were "([^"]*)"$`, s.waitsWere)
	ctx.Step(`^the fake transport saw exactly (\d+) requests, so no fourth attempt occurred$`, s.transportSaw)
	ctx.Step(`^the call fails with code "([^"]*)" and class "([^"]*)"$`, s.callFails)
	ctx.Step(`^the only settlement is on route "([^"]*)" with failure code "([^"]*)" and class "([^"]*)"$`, s.onlySettlement)
	ctx.Step(`^the route "([^"]*)" was never prepared nor attempted$`, s.otherNeverUsed)

	ctx.Step(`^the DH-S-9 scenario files for "([^"]*)" and "([^"]*)" in the checkout$`, s.givenScenarioFiles)
	ctx.Step(`^the gate's index check runs on the checkout$`, s.runIndexCheck)
	ctx.Step(`^"(go -C src/harness vet -tags scenario \./\.\.\.)" runs$`, s.runVet)
	ctx.Step(`^the gate's index check passes$`, s.indexCheckPasses)
	ctx.Step(`^each of the IDs has exactly one scenario file bound to a TestXxx$`, s.exactlyOneFile)
	ctx.Step(`^each of the IDs has exactly one index entry, under "([^"]*)", authored "([^"]*)" and requiring "([^"]*)"$`, s.oneIndexEntry)
	ctx.Step(`^each file imports only "([^"]*)", the test kit's scenario launcher and the standard library$`, s.importsLimited)
	ctx.Step(`^no file imports "([^"]*)"$`, s.noForbiddenImport)
	ctx.Step(`^vet exits 0$`, s.vetExits0)
	ctx.Step(`^the default gate run executes none of them$`, s.defaultRunNone)
	ctx.Step(`^requiring any of them before "([^"]*)" are built is refused by the preflight$`, s.preflightRefuses)

	ctx.Step(`^a session whose log holds three settlements with known token counts$`, s.sessionWithSettlements)
	ctx.Step(`^the usage fold runs over the session$`, s.usageFoldRuns)
	ctx.Step(`^the billed usage is input (\d+), cache read (\d+), cache write (\d+) and output (\d+)$`, s.billedIs)
	ctx.Step(`^the fold counted (\d+) attempts, (\d+) of them reporting usage, totalling (\d+) tokens$`, s.countedAttempts)
	ctx.Step(`^the last reported usage totals (\d+) tokens at the last settlement$`, s.lastTotals)
	ctx.Step(`^the context occupancy is (\d+) tokens, ([0-9.]+) of a (\d+)-token window$`, s.occupancy)
	ctx.Step(`^folding the session's log from a checkpoint after the first settlement equals folding it from zero$`, s.checkpointEqualsZero)
}

func TestE2E_deepseek_provider_retry_and_usage_bounded_retry_and_token_usage(t *testing.T) {
	brtOuterT = t
	defer func() { brtOuterT = nil }()
	suite := godog.TestSuite{
		Name:                "deepseek_provider_retry_and_usage_bounded_retry_and_token_usage",
		ScenarioInitializer: InitializeScenario_deepseek_provider_retry_and_usage_bounded_retry_and_token_usage,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"deepseek_provider_retry_and_usage_bounded_retry_and_token_usage.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
