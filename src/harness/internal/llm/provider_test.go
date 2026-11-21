package llm_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// fakeKey is the API key the tests resolve; it is not, and never was, a real key.
const fakeKey = "sk-dsh-test-0000000000000000"

const dsModel = "deepseek-chat"

// dsSession is a session on deepseek-official with one user message, and its store.
func dsSession(t *testing.T) (session.Handle, store.Store) {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	st := memstore.New()
	svc := session.NewService(root, st, session.Config{})
	h := must(svc.Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	t.Cleanup(func() { _ = h.Close(bg) })
	sys := must(vocab.RestoreMessage("s1", vocab.RoleSystem, nil, vocab.TextBlock("Be brief.")))
	user := must(vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("hello")))
	must(h.AppendGroup(bg, []event.Draft{
		must(event.TurnStartType.Draft(event.TurnStart{Turn: 1})),
		must(event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1})),
		must(event.RequestHeader.Draft(event.RequestHeaderPayload{Route: deepseek.RouteID})),
		must(event.SystemMessage.Draft(event.Append(), event.MessagePayload{Message: sys})),
		must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})),
	}))
	return h, st
}

func keyStore() *secret.Store {
	return secret.NewStore(secret.EnvSource(func(name string) (string, bool) {
		if name == deepseek.KeyRef.EnvName() {
			return fakeKey, true
		}
		return "", false
	}))
}

func dsRuntime(t *testing.T, tr *transport.Transport, base string, keys *secret.Store) *llm.Runtime {
	t.Helper()
	route := must(deepseek.Route(deepseek.Config{BaseURL: base, Secrets: keys, Transport: tr}))
	rt := llm.New()
	must(rt.Register("deepseek", route))
	return rt
}

// attemptOnce makes one attempt on the session's next request and returns the
// records the caller's sink settled.
func attemptOnce(t *testing.T, rt *llm.Runtime, h session.Handle) *recordingSink {
	t.Helper()
	p := must(rt.Prepare(bg, deepseek.RouteID, dsModel, vocab.Controls{}))
	sink := &recordingSink{h: h}
	_, fins := drain(p.Stream(bg, must(h.NextRequest()), sink))
	if len(fins) != 1 {
		t.Fatalf("finishes = %d, want 1", len(fins))
	}
	if len(sink.records) != 1 {
		t.Fatalf("settlements = %d, want exactly 1", len(sink.records))
	}
	return sink
}

func rebuilt(t *testing.T, st store.Store, h session.Handle, asOf event.Seq) surface.Built {
	t.Helper()
	r := must(st.OpenRead(bg, string(h.ID())))
	t.Cleanup(func() { _ = r.Close(bg) })
	read := func(ctx context.Context, from event.Offset, limit int) ([]event.Record, error) {
		return r.Read(ctx, from, limit, store.ReadOptions{WithAux: true})
	}
	return must(surface.Rebuild(bg, event.Default(), read, string(h.ID()), asOf))
}

// fixture is a recorded response: a raw HTTP/1.1 response file.
type fixture struct {
	status int
	header http.Header
	body   []byte
}

func loadFixture(t *testing.T, name string) fixture {
	t.Helper()
	raw := must(os.ReadFile(filepath.Join("testdata", "sse", name+".http")))
	if bytes.Contains(raw, []byte(fakeKey)) || bytes.Contains(raw, []byte("sk-")) {
		t.Fatalf("fixture %s holds a key", name)
	}
	resp := must(http.ReadResponse(bufio.NewReader(bytes.NewReader(raw)), nil))
	body := must(io.ReadAll(resp.Body))
	return fixture{status: resp.StatusCode, header: resp.Header, body: body}
}

// fixtureRT serves one fixture in memory and captures the request.
type fixtureRT struct {
	f    fixture
	mu   sync.Mutex
	seen []*http.Request
}

func (rt *fixtureRT) RoundTrip(req *http.Request) (*http.Response, error) {
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

// summary is the deterministic part of a settlement: everything but the
// random message id, the clock and the request attribution.
type summary struct {
	Kind    string         `json:"kind"`
	Finish  string         `json:"finish"`
	Failure *vocab.Failure `json:"failure,omitempty"`
	Blocks  []vocab.Block  `json:"blocks,omitempty"`
	Usage   *vocab.Usage   `json:"usage,omitempty"`
}

func summarise(t *testing.T, a llm.Attempt) string {
	t.Helper()
	d := must(a.Draft())
	s := summary{Kind: d.Type(), Finish: string(a.Finish.Reason), Failure: a.Finish.Failure, Usage: a.Usage}
	if a.Message != nil {
		s.Blocks = a.Message.Blocks()
	}
	b := must(json.MarshalIndent(s, "", "  "))
	return string(b) + "\n"
}

// fixtureCases are the recorded fixtures and what each must settle as.
var fixtureCases = []struct {
	name string
	kind string
	code vocab.Code
}{
	{"success", "assistant/message", ""},
	{"overloaded-retry-after", "assistant/attempt", vocab.CodeServer},
	{"bad-key", "assistant/attempt", vocab.CodeAuth},
	{"mid-stream-drop", "assistant/attempt", vocab.CodeStreamClosed},
}

// DH-T-LLM-5 (TC-09a): each SSE fixture fed through the driver in memory yields
// exactly one settlement of the expected kind, and the decoded result equals the
// committed golden.
func TestDeepSeekFixturesSettleOnce(t *testing.T) {
	for _, tc := range fixtureCases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := dsSession(t)
			rt := &fixtureRT{f: loadFixture(t, tc.name)}
			sink := attemptOnce(t, dsRuntime(t, transport.Over(rt, transport.Options{}), "", keyStore()), h)
			a := sink.records[0]
			if got := must(a.Draft()).Type(); got != tc.kind {
				t.Fatalf("kind = %s, want %s (finish %+v)", got, tc.kind, a.Finish)
			}
			if tc.code == "" && a.Finish.Failure != nil || tc.code != "" && (a.Finish.Failure == nil || a.Finish.Failure.Code != tc.code) {
				t.Fatalf("failure = %+v, want code %q", a.Finish.Failure, tc.code)
			}
			if len(rt.seen) != 1 || rt.seen[0].URL.String() != deepseek.Endpoint(deepseek.BaseURL) {
				t.Fatalf("requests = %v, want one to %s", rt.seen, deepseek.Endpoint(deepseek.BaseURL))
			}
			golden.Assert(t, "sse/"+tc.name, summarise(t, a))
		})
	}
}

// DH-T-LLM-5 / DH-S-9 exit evidence (TC-09a): the bytes at the transport are the
// request rebuilt from the log at the settlement's requestAsOf, and the
// settlement's requestDigest is the rebuilt request's digest.
func TestDeepSeekRequestEqualsRebuilt(t *testing.T) {
	h, st := dsSession(t)
	ev := func(typ, data string) model.Event { return model.Event{Name: typ, Data: data} }
	tr := model.NewTransport(t, model.Turn{Events: []model.Event{
		ev("message_start", `{"type":"message_start","message":{"usage":{"input_tokens":3,"output_tokens":1}}}`),
		ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`),
		ev("content_block_stop", `{"type":"content_block_stop","index":0}`),
		ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`),
		ev("message_stop", `{"type":"message_stop"}`),
	}})
	rt := dsRuntime(t, transport.Over(tr, transport.Options{}), "", keyStore())
	sink := attemptOnce(t, rt, h)
	a := sink.records[0]
	if !a.Succeeded() {
		t.Fatalf("attempt failed: %+v", a.Finish)
	}
	reqs := tr.Requests()
	if len(reqs) != 1 {
		t.Fatalf("captured %d requests, want 1", len(reqs))
	}
	re := rebuilt(t, st, h, a.RequestAsOf)
	if re.Digest() != a.RequestDigest {
		t.Fatalf("rebuilt digest %s, settlement requestDigest %s", re.Digest(), a.RequestDigest)
	}
	want := must(anthropic.Encode(re, dsModel, vocab.CallConfig{Controls: vocab.Controls{MaxOutputTokens: deepseek.DefaultMaxTokens}}))
	if !bytes.Equal(reqs[0].Body, want.Body) {
		t.Fatalf("captured body differs from the rebuilt request:\n got %s\nwant %s", reqs[0].Body, want.Body)
	}
	if got := reqs[0].Header.Get("x-api-key"); got != fakeKey {
		t.Fatalf("x-api-key = %q", got)
	}
	if !strings.Contains(string(want.Body), `"system":[{"type":"text","text":"Be brief."}]`) {
		t.Fatalf("system prompt not in the leading system field: %s", want.Body)
	}
}

// loopback serves fixture name on 127.0.0.1:0 and records what it saw.
type loopback struct {
	srv     *httptest.Server
	conns   atomic.Int32
	mu      sync.Mutex
	headers []http.Header
	bodies  [][]byte
}

func serveFixture(t *testing.T, f fixture, echo string) *loopback {
	t.Helper()
	lb := &loopback{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lb.mu.Lock()
		lb.headers = append(lb.headers, r.Header.Clone())
		lb.bodies = append(lb.bodies, body)
		lb.mu.Unlock()
		for k, vs := range f.header {
			w.Header()[k] = vs
		}
		w.WriteHeader(f.status)
		out := f.body
		if echo != "" {
			out = bytes.ReplaceAll(out, []byte("is invalid"), []byte("is invalid: "+echo))
		}
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close) // NewUnstartedServer listens on 127.0.0.1:0
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			lb.conns.Add(1)
		}
	}
	srv.Start()
	lb.srv = srv
	return lb
}

func egressTransport(t *testing.T, dest string) *transport.Transport {
	t.Helper()
	svc := egress.New(egress.Config{})
	t.Cleanup(svc.CloseIdleConnections)
	tr := must(transport.New(svc, permit.New(t, transport.Kind, dest), transport.Options{Label: "deepseek", HeaderWait: 10 * time.Second}))
	t.Cleanup(tr.Close)
	return tr
}

// DH-T-LLM-11 (TC-09b): over loopback HTTP, with a model-request permit for the
// test server, the provider streams the success fixture into one
// assistant/message, and the server saw the x-api-key from the secret store, the
// egress User-Agent and the request body rebuilt from the log.
func TestDeepSeekOverLoopback(t *testing.T) {
	h, st := dsSession(t)
	lb := serveFixture(t, loadFixture(t, "success"), "")
	rt := dsRuntime(t, egressTransport(t, lb.srv.URL), lb.srv.URL+"/anthropic", keyStore())
	sink := attemptOnce(t, rt, h)
	a := sink.records[0]
	if got := must(a.Draft()).Type(); got != "assistant/message" || !a.Succeeded() {
		t.Fatalf("settled %s (%+v), want one assistant/message", got, a.Finish)
	}
	if len(lb.headers) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(lb.headers))
	}
	hd := lb.headers[0]
	if hd.Get("X-Api-Key") != fakeKey || hd.Get("User-Agent") != "dsh" || hd.Get("Anthropic-Version") != anthropic.Version {
		t.Fatalf("server headers = %v", hd)
	}
	if hd.Get("Authorization") != "" || hd.Get("Cookie") != "" {
		t.Fatalf("unexpected credential headers: %v", hd)
	}
	re := rebuilt(t, st, h, a.RequestAsOf)
	want := must(anthropic.Encode(re, dsModel, vocab.CallConfig{Controls: vocab.Controls{MaxOutputTokens: deepseek.DefaultMaxTokens}}))
	if !bytes.Equal(lb.bodies[0], want.Body) || re.Digest() != a.RequestDigest {
		t.Fatalf("server body differs from the rebuilt request")
	}
	golden.Assert(t, "sse/success", summarise(t, a))
}

// DH-T-LLM-11: a permit that does not name the server opens no connection, and
// the attempt settles as a permanent failure naming egress/not-permitted.
func TestDeepSeekWithoutPermitDialsNothing(t *testing.T) {
	h, _ := dsSession(t)
	lb := serveFixture(t, loadFixture(t, "success"), "")
	rt := dsRuntime(t, egressTransport(t, "https://api.deepseek.com"), lb.srv.URL, keyStore())
	a := attemptOnce(t, rt, h).records[0]
	f := a.Finish.Failure
	if f == nil || f.Class != vocab.Permanent || !strings.Contains(f.Message, "egress/not-permitted") {
		t.Fatalf("failure = %+v, want permanent egress/not-permitted", f)
	}
	if n := lb.conns.Load(); n != 0 {
		t.Fatalf("server accepted %d connections without a permit", n)
	}
	if _, err := transport.New(egress.New(egress.Config{}), permit.New(t, "telemetry", lb.srv.URL), transport.Options{}); !egress.IsNotPermitted(err) {
		t.Fatalf("a telemetry permit gave a model transport: %v", err)
	}
}

// DH-T-LLM-16: a server echoing the key in its error body does not get the key
// into the settlement or its error text; a missing key fails before any dial.
func TestDeepSeekKeyNeverLeaks(t *testing.T) {
	h, _ := dsSession(t)
	lb := serveFixture(t, loadFixture(t, "bad-key"), fakeKey)
	rt := dsRuntime(t, egressTransport(t, lb.srv.URL), lb.srv.URL, keyStore())
	a := attemptOnce(t, rt, h).records[0]
	if a.Finish.Failure == nil || a.Finish.Failure.Code != vocab.CodeAuth || a.Finish.Failure.Status != http.StatusUnauthorized {
		t.Fatalf("failure = %+v, want llm/auth 401", a.Finish.Failure)
	}
	d := must(a.Draft())
	if strings.Contains(d.Raw(), fakeKey) || strings.Contains(a.Finish.Failure.Error(), fakeKey) {
		t.Fatalf("the key leaked: %s", d.Raw())
	}
	if !strings.Contains(a.Finish.Failure.Message, secret.Redacted) {
		t.Fatalf("the echoed key was not masked: %q", a.Finish.Failure.Message)
	}

	h2, _ := dsSession(t)
	empty := secret.NewStore(secret.EnvSource(func(string) (string, bool) { return "", false }))
	before := lb.conns.Load()
	a = attemptOnce(t, dsRuntime(t, egressTransport(t, lb.srv.URL), lb.srv.URL, empty), h2).records[0]
	if a.Finish.Failure == nil || a.Finish.Failure.Code != vocab.CodeMissingCredential {
		t.Fatalf("failure = %+v, want llm/missing-credential", a.Finish.Failure)
	}
	if lb.conns.Load() != before {
		t.Fatal("a request without a key was sent")
	}
}

// A tool_use block the vendor never stopped does not settle as a tool call: the
// attempt fails llm/malformed-response and keeps the usage it reported.
func TestDeepSeekUnfinishedToolCallFails(t *testing.T) {
	h, _ := dsSession(t)
	body := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":12,\"output_tokens\":1}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"read\",\"input\":{}}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	rt := &fixtureRT{f: fixture{status: 200, header: http.Header{"Content-Type": {"text/event-stream"}}, body: []byte(body)}}
	a := attemptOnce(t, dsRuntime(t, transport.Over(rt, transport.Options{}), "", keyStore()), h).records[0]
	if got := must(a.Draft()).Type(); got != "assistant/attempt" {
		t.Fatalf("kind = %s, want assistant/attempt", got)
	}
	if a.Finish.Failure == nil || a.Finish.Failure.Code != vocab.CodeMalformedResponse {
		t.Fatalf("failure = %+v, want llm/malformed-response", a.Finish.Failure)
	}
	if a.Message != nil {
		for _, b := range a.Message.Blocks() {
			if b.ToolUse != nil {
				t.Fatalf("an unfinished tool call settled: %+v", b.ToolUse)
			}
		}
	}
	if a.Usage == nil || a.Usage.Input != 12 || a.Usage.Output != 1 {
		t.Fatalf("usage = %+v, want the reported 12/1", a.Usage)
	}
}
