//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/deepseek"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/transport"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/wire/anthropic"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/permit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
)

// dmpFakeKey is the API key the secret store resolves; it is not a real key.
const (
	dmpFakeKey = "sk-dsh-e2e-0000000000000000"
	dmpModel   = "deepseek-chat"
)

var (
	dmpBG     = context.Background()
	dmpOuterT *testing.T
)

// dmpSSEDir holds the committed fixtures and goldens of the llm package.
var dmpSSEDir = filepath.Join("..", "..", "..", "internal", "llm", "testdata", "sse")

type dmpFixture struct {
	status int
	header http.Header
	body   []byte
}

// dmpFixtureRT serves one fixture in memory and captures each request.
type dmpFixtureRT struct {
	f    dmpFixture
	mu   sync.Mutex
	seen []*http.Request
}

func (rt *dmpFixtureRT) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.seen = append(rt.seen, req)
	rt.mu.Unlock()
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	return &http.Response{StatusCode: rt.f.status, Status: http.StatusText(rt.f.status), Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: rt.f.header.Clone(), Body: io.NopCloser(bytes.NewReader(rt.f.body)), Request: req}, nil
}

// dmpSink logs each settlement record as one event, as a caller would.
type dmpSink struct {
	h       session.Handle
	records []llm.Attempt
}

func (s *dmpSink) Settle(a llm.Attempt) error {
	s.records = append(s.records, a)
	d, err := a.Draft()
	if err != nil {
		return err
	}
	_, err = s.h.Append(dmpBG, d)
	return err
}

type dmpState struct {
	cleanups []func()

	h  session.Handle
	st store.Store

	tr      *transport.Transport
	base    string
	fake    *model.Transport
	inMem   *dmpFixtureRT
	srv     *httptest.Server
	srvMu   sync.Mutex
	headers []http.Header
	bodies  [][]byte

	sink     *dmpSink
	finishes int
}

func (s *dmpState) cleanup(f func()) { s.cleanups = append(s.cleanups, f) }

func (s *dmpState) close() {
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
	s.cleanups = nil
}

func (s *dmpState) t() *testing.T {
	if dmpOuterT == nil {
		panic("deepseek e2e: no outer *testing.T")
	}
	return dmpOuterT
}

func dmpKeyStore() *secret.Store {
	return secret.NewStore(secret.EnvSource(func(name string) (string, bool) {
		if name == deepseek.KeyRef.EnvName() {
			return dmpFakeKey, true
		}
		return "", false
	}))
}

func dmpLoadFixture(name string) (dmpFixture, error) {
	raw, err := os.ReadFile(filepath.Join(dmpSSEDir, name+".http"))
	if err != nil {
		return dmpFixture{}, err
	}
	if bytes.Contains(raw, []byte("sk-")) {
		return dmpFixture{}, fmt.Errorf("fixture %s holds a key", name)
	}
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(raw)), nil)
	if err != nil {
		return dmpFixture{}, fmt.Errorf("parse fixture %s: %w", name, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return dmpFixture{}, err
	}
	return dmpFixture{status: resp.StatusCode, header: resp.Header, body: body}, nil
}

func (s *dmpState) aDeepseekSession() error {
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	s.cleanup(root.Dispose)
	s.st = memstore.New()
	svc := session.NewService(root, s.st, session.Config{})
	h, err := svc.Create(dmpBG, nil, session.CreateOptions{Scope: scope.Key{}})
	if err != nil {
		return err
	}
	s.h = h
	s.cleanup(func() { _ = h.Close(dmpBG) })
	sys, err := vocab.RestoreMessage("s1", vocab.RoleSystem, nil, vocab.TextBlock("Be brief."))
	if err != nil {
		return err
	}
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("hello"))
	if err != nil {
		return err
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
			return err
		}
		drafts = append(drafts, d)
	}
	_, err = h.AppendGroup(dmpBG, drafts)
	return err
}

func (s *dmpState) theFakeTransportCapturingBytes() error {
	ev := func(typ, data string) model.Event { return model.Event{Name: typ, Data: data} }
	s.fake = model.NewTransport(s.t(), model.Turn{Events: []model.Event{
		ev("message_start", `{"type":"message_start","message":{"usage":{"input_tokens":3,"output_tokens":1}}}`),
		ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`),
		ev("content_block_stop", `{"type":"content_block_stop","index":0}`),
		ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`),
		ev("message_stop", `{"type":"message_stop"}`),
	}})
	s.tr = transport.Over(s.fake, transport.Options{})
	return nil
}

func (s *dmpState) theSSEFixtureServedInMemory(name string) error {
	f, err := dmpLoadFixture(name)
	if err != nil {
		return err
	}
	s.inMem = &dmpFixtureRT{f: f}
	s.tr = transport.Over(s.inMem, transport.Options{})
	return nil
}

func (s *dmpState) theSuccessFixtureServedByHttptest() error {
	f, err := dmpLoadFixture("success")
	if err != nil {
		return err
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.srvMu.Lock()
		s.headers = append(s.headers, r.Header.Clone())
		s.bodies = append(s.bodies, body)
		s.srvMu.Unlock()
		for k, vs := range f.header {
			w.Header()[k] = vs
		}
		w.WriteHeader(f.status)
		_, _ = w.Write(f.body)
	}))
	s.cleanup(srv.Close)
	if !strings.HasPrefix(srv.URL, "http://127.0.0.1:") {
		return fmt.Errorf("httptest server is not on loopback: %s", srv.URL)
	}
	s.srv = srv
	s.base = srv.URL + "/anthropic"
	return nil
}

func (s *dmpState) aModelRequestPermitForThatHost() error {
	if s.srv == nil {
		return errors.New("no loopback server")
	}
	svc := egress.New(egress.Config{})
	s.cleanup(svc.CloseIdleConnections)
	tr, err := transport.New(svc, permit.New(s.t(), transport.Kind, s.srv.URL), transport.Options{Label: "deepseek", HeaderWait: 10 * time.Second})
	if err != nil {
		return fmt.Errorf("model-request transport for %s: %w", s.srv.URL, err)
	}
	s.cleanup(tr.Close)
	s.tr = tr
	return nil
}

func (s *dmpState) oneAttemptIsMade() error {
	if s.tr == nil || s.h == nil {
		return errors.New("session or transport not set up")
	}
	route, err := deepseek.Route(deepseek.Config{BaseURL: s.base, Secrets: dmpKeyStore(), Transport: s.tr})
	if err != nil {
		return err
	}
	rt := llm.New()
	if _, err := rt.Register("deepseek", route); err != nil {
		return err
	}
	p, err := rt.Prepare(dmpBG, deepseek.RouteID, dmpModel, vocab.Controls{})
	if err != nil {
		return err
	}
	req, err := s.h.NextRequest()
	if err != nil {
		return err
	}
	s.sink = &dmpSink{h: s.h}
	for c := range p.Stream(dmpBG, req, s.sink) {
		if _, ok := c.(vocab.Finish); ok {
			s.finishes++
		}
	}
	return nil
}

func (s *dmpState) one() (llm.Attempt, error) {
	if s.sink == nil {
		return llm.Attempt{}, errors.New("no attempt made")
	}
	if s.finishes != 1 {
		return llm.Attempt{}, fmt.Errorf("finishes = %d, want 1", s.finishes)
	}
	if len(s.sink.records) != 1 {
		return llm.Attempt{}, fmt.Errorf("settlements = %d, want exactly 1", len(s.sink.records))
	}
	return s.sink.records[0], nil
}

func (s *dmpState) rebuiltAt(asOf event.Seq) (surface.Built, error) {
	r, err := s.st.OpenRead(dmpBG, string(s.h.ID()))
	if err != nil {
		return surface.Built{}, err
	}
	s.cleanup(func() { _ = r.Close(dmpBG) })
	read := func(ctx context.Context, from event.Offset, limit int) ([]event.Record, error) {
		return r.Read(ctx, from, limit, store.ReadOptions{WithAux: true})
	}
	return surface.Rebuild(dmpBG, event.Default(), read, string(s.h.ID()), asOf)
}

func (s *dmpState) rebuiltBody(a llm.Attempt) (surface.Built, []byte, error) {
	re, err := s.rebuiltAt(a.RequestAsOf)
	if err != nil {
		return surface.Built{}, nil, err
	}
	want, err := anthropic.Encode(re, dmpModel, vocab.CallConfig{Controls: vocab.Controls{MaxOutputTokens: deepseek.DefaultMaxTokens}})
	if err != nil {
		return surface.Built{}, nil, err
	}
	return re, want.Body, nil
}

func (s *dmpState) exactlyOneSucceeded() error {
	a, err := s.one()
	if err != nil {
		return err
	}
	if !a.Succeeded() {
		return fmt.Errorf("attempt failed: %+v", a.Finish)
	}
	return nil
}

func (s *dmpState) capturedBodyEqualsRebuild() error {
	a, err := s.one()
	if err != nil {
		return err
	}
	reqs := s.fake.Requests()
	if len(reqs) != 1 {
		return fmt.Errorf("captured %d requests, want 1", len(reqs))
	}
	_, want, err := s.rebuiltBody(a)
	if err != nil {
		return err
	}
	if !bytes.Equal(reqs[0].Body, want) {
		return fmt.Errorf("captured body differs from the rebuilt request:\n got %s\nwant %s", reqs[0].Body, want)
	}
	if !strings.Contains(string(want), `"system":[{"type":"text","text":"Be brief."}]`) {
		return fmt.Errorf("system prompt not in the leading system field: %s", want)
	}
	if got := reqs[0].Header.Get("x-api-key"); got != dmpFakeKey {
		return fmt.Errorf("x-api-key = %q", got)
	}
	return nil
}

func (s *dmpState) digestMatches() error {
	a, err := s.one()
	if err != nil {
		return err
	}
	if a.RequestDigest == "" {
		return errors.New("settlement carries no requestDigest")
	}
	re, err := s.rebuiltAt(a.RequestAsOf)
	if err != nil {
		return err
	}
	if re.Digest() != a.RequestDigest {
		return fmt.Errorf("rebuilt digest %s, settlement requestDigest %s", re.Digest(), a.RequestDigest)
	}
	return nil
}

func (s *dmpState) oneSettlementOfKind(kind, code string) error {
	a, err := s.one()
	if err != nil {
		return err
	}
	d, err := a.Draft()
	if err != nil {
		return err
	}
	if d.Type() != kind {
		return fmt.Errorf("kind = %s, want %s (finish %+v)", d.Type(), kind, a.Finish)
	}
	f := a.Finish.Failure
	if code == "" && f != nil || code != "" && (f == nil || string(f.Code) != code) {
		return fmt.Errorf("failure = %+v, want code %q", f, code)
	}
	if s.inMem != nil {
		if len(s.inMem.seen) != 1 || s.inMem.seen[0].URL.String() != deepseek.Endpoint(deepseek.BaseURL) {
			return fmt.Errorf("requests = %v, want one to %s", s.inMem.seen, deepseek.Endpoint(deepseek.BaseURL))
		}
	}
	return nil
}

type dmpSummary struct {
	Kind    string         `json:"kind"`
	Finish  string         `json:"finish"`
	Failure *vocab.Failure `json:"failure,omitempty"`
	Blocks  []vocab.Block  `json:"blocks,omitempty"`
	Usage   *vocab.Usage   `json:"usage,omitempty"`
}

func dmpSummarise(a llm.Attempt) (string, error) {
	d, err := a.Draft()
	if err != nil {
		return "", err
	}
	sm := dmpSummary{Kind: d.Type(), Finish: string(a.Finish.Reason), Failure: a.Finish.Failure, Usage: a.Usage}
	if a.Message != nil {
		sm.Blocks = a.Message.Blocks()
	}
	b, err := json.MarshalIndent(sm, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}

func (s *dmpState) equalsGolden(name string) error {
	a, err := s.one()
	if err != nil {
		return err
	}
	got, err := dmpSummarise(a)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(dmpSSEDir, name+".golden"))
	if err != nil {
		return fmt.Errorf("committed golden %s: %w", name, err)
	}
	want := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if got != want {
		return fmt.Errorf("decoded settlement differs from golden %s:\n%s", name, golden.Diff(want, got))
	}
	return nil
}

func (s *dmpState) oneAssistantMessageSettled() error {
	if err := s.oneSettlementOfKind("assistant/message", ""); err != nil {
		return err
	}
	if !s.sink.records[0].Succeeded() {
		return fmt.Errorf("attempt failed: %+v", s.sink.records[0].Finish)
	}
	return s.equalsGolden("success")
}

func (s *dmpState) serverSawKeyHeader() error {
	s.srvMu.Lock()
	defer s.srvMu.Unlock()
	if len(s.headers) != 1 {
		return fmt.Errorf("server saw %d requests, want 1", len(s.headers))
	}
	hd := s.headers[0]
	if hd.Get("X-Api-Key") != dmpFakeKey {
		return fmt.Errorf("server x-api-key = %q, want the secret store's value", hd.Get("X-Api-Key"))
	}
	if hd.Get("Anthropic-Version") != anthropic.Version {
		return fmt.Errorf("anthropic-version = %q", hd.Get("Anthropic-Version"))
	}
	if hd.Get("Authorization") != "" || hd.Get("Cookie") != "" {
		return fmt.Errorf("unexpected credential headers: %v", hd)
	}
	return nil
}

func (s *dmpState) serverSawRebuiltBody() error {
	a, err := s.one()
	if err != nil {
		return err
	}
	re, want, err := s.rebuiltBody(a)
	if err != nil {
		return err
	}
	s.srvMu.Lock()
	defer s.srvMu.Unlock()
	if len(s.bodies) != 1 || !bytes.Equal(s.bodies[0], want) {
		return fmt.Errorf("server body differs from the rebuilt request")
	}
	if re.Digest() != a.RequestDigest {
		return fmt.Errorf("rebuilt digest %s, settlement requestDigest %s", re.Digest(), a.RequestDigest)
	}
	return nil
}

func InitializeScenario_deepseek_provider_retry_and_usage_deepseek_messages_provider(ctx *godog.ScenarioContext) {
	s := &dmpState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		s.close()
		*s = dmpState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return c, nil
	})
	ctx.Step(`^a deepseek session with a system prompt and one user message$`, s.aDeepseekSession)
	ctx.Step(`^the fake transport of the test kit capturing bytes$`, s.theFakeTransportCapturingBytes)
	ctx.Step(`^the SSE fixture "([^"]+)" served in memory$`, s.theSSEFixtureServedInMemory)
	ctx.Step(`^the success fixture served by httptest on 127\.0\.0\.1:0$`, s.theSuccessFixtureServedByHttptest)
	ctx.Step(`^a model-request permit for that loopback host$`, s.aModelRequestPermitForThatHost)
	ctx.Step(`^one attempt is made for the session$`, s.oneAttemptIsMade)
	ctx.Step(`^exactly one settlement is recorded and it succeeded$`, s.exactlyOneSucceeded)
	ctx.Step(`^the captured body equals surface\.Rebuild at the settlement's requestAsOf$`, s.capturedBodyEqualsRebuild)
	ctx.Step(`^the settlement's requestDigest matches the rebuilt request's digest$`, s.digestMatches)
	ctx.Step(`^exactly one settlement of kind "([^"]+)" is recorded with failure code "([^"]*)"$`, s.oneSettlementOfKind)
	ctx.Step(`^the decoded settlement equals the committed golden "([^"]+)"$`, s.equalsGolden)
	ctx.Step(`^one assistant/message is settled$`, s.oneAssistantMessageSettled)
	ctx.Step(`^the server saw the x-api-key header from the secret store$`, s.serverSawKeyHeader)
	ctx.Step(`^the server saw the request body rebuilt from the log$`, s.serverSawRebuiltBody)
}

func TestE2E_deepseek_provider_retry_and_usage_deepseek_messages_provider(t *testing.T) {
	dmpOuterT = t
	defer func() { dmpOuterT = nil }()
	suite := godog.TestSuite{
		Name:                "deepseek_provider_retry_and_usage_deepseek_messages_provider",
		ScenarioInitializer: InitializeScenario_deepseek_provider_retry_and_usage_deepseek_messages_provider,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"deepseek_provider_retry_and_usage_deepseek_messages_provider.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
