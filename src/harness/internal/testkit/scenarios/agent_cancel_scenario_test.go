//go:build scenario

package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestCancelledStreamKeepsWhatWasShown: cancelling a real stream keeps what was
// shown and records the cause (areas/agent-loop.md DH-E-AGENT-2; DH-D-AGENT-5).
// Authored in DH-S-11; executed in DH-S-17, which requires the agent loop
// (DH-S-11) and the headless launcher (DH-S-16).
//
// Given an empty temporary home, profile headless, one real provider and a small
// model, its key supplied at run time, a recording proxy between dsh and the
// provider and a prompt that produces a long answer, when after the first chunk
// frames arrive the client cancels the turn (cause user) keeping the inbox, waits
// for idle and then sends "repeat your last sentence", then the first turn ends
// aborted with cause user; its one settlement is an interrupted assistant/message
// holding the text that was streamed, with no tool call (areas/agent-loop.md:556;
// the driver keeps a cancelled attempt's prefix as history), and the proxy saw
// dsh close the provider connection before the response ended; and the second
// request the proxy forwarded carries
// that text. Cleanup: the launcher stops dsh and deletes the home. On failure the
// export is kept (its path is logged) and the proxy's requests are in the failure
// message.
//
// Inputs: DSH_DEEPSEEK_API_KEY is the real key, handed to the program as
// DEEPSEEK_API_KEY; DSH_SCENARIO_AGENT2_OFFICIAL the base URL of DeepSeek's
// Messages endpoint, fronted by the proxy as {official}; DSH_SCENARIO_AGENT2_MODEL
// a small model on route deepseek-official; DSH_SCENARIO_AGENT2_LAYER a headless
// layer, a template over {official}, with no title route;
// DSH_SCENARIO_AGENT2_PROMPT a prompt whose answer is long enough to cancel
// mid-stream.
//
//dsh:scenario DH-E-AGENT-2
func TestCancelledStreamKeepsWhatWasShown(t *testing.T) {
	selfCheckProxyClose(t)
	key := need(t, "DSH_DEEPSEEK_API_KEY", "a real DeepSeek API key")
	officialURL := need(t, "DSH_SCENARIO_AGENT2_OFFICIAL", "the base URL of DeepSeek's Messages endpoint")
	model := need(t, "DSH_SCENARIO_AGENT2_MODEL", "a small model on deepseek-official")
	layerT := need(t, "DSH_SCENARIO_AGENT2_LAYER", "a headless layer routing the model through {official}")
	prompt := need(t, "DSH_SCENARIO_AGENT2_PROMPT", "a prompt with a long answer")

	proxy := startProxy(t, map[string]string{"official": officialURL})
	vars := map[string]string{"official": proxy.base("official"), "workspace": filepath.ToSlash(t.TempDir())}
	h := startSDK(t, dshBinary(t), []string{"DEEPSEEK_API_KEY=" + key}, []string{"--layer", writeLayer(t, expand(layerT, vars))})
	session := h.create(t, &protocol.ModelRoute{Provider: "deepseek-official", Model: model})
	h.keepExport(t, session)

	receipt := h.prompt(t, session, prompt)
	h.until(t, 5*time.Minute, "the first text chunk", func() bool {
		return streamedText(h, session, 1) != ""
	})
	var cancelled protocol.SessionCancelResult
	h.call(t, "session/cancel", protocol.SessionCancelParams{SessionID: session, Scope: "turn", Inbox: "keep"}, &cancelled)
	if !cancelled.Cancelled {
		t.Fatal("session/cancel cancelled nothing")
	}
	run1 := h.awaitRun(t, session, receipt, 5*time.Minute)
	evs1 := inRun(h.history(t, session), run1.FirstSeq, run1.LastSeq)
	logRequestIDs(t, evs1)
	if end := turnEnd(t, evs1); end.Reason.Kind != "aborted" || end.Reason.Cause != "user" {
		t.Fatalf("turn 1 ended %+v, want aborted with cause user", end.Reason)
	}
	h.mu.Lock()
	shown := streamedText(h, session, int64(run1.Turn))
	h.mu.Unlock()
	// The driver persists a cancelled attempt that kept a prefix as the turn's
	// assistant/message (no failure field): the interruption is the turn's
	// aborted{user}, checked above, and the prefix is history with no tool call.
	settled := ofType(evs1, "assistant/message", "assistant/attempt")
	if len(settled) != 1 || settled[0].Type != "assistant/message" {
		t.Fatalf("turn 1 has settlements %v, want one interrupted assistant/message", eventTypes(settled))
	}
	var s wireSettlement
	decode(t, settled[0], &s)
	if s.Message == nil {
		t.Fatalf("the interrupted assistant/message has no message:\n%s", settled[0].Data)
	}
	var kept strings.Builder
	for _, b := range s.Message.Blocks {
		if b.ToolUse != nil {
			t.Fatalf("the interrupted message carries a tool call:\n%s", settled[0].Data)
		}
		if b.Text != nil {
			kept.WriteString(b.Text.Text)
		}
	}
	if shown == "" || !strings.HasPrefix(kept.String(), shown) {
		t.Fatalf("the interrupted message holds %q, want the streamed text %q", kept.String(), shown)
	}
	if fwd := proxy.settled(t, "official", 0, 30*time.Second); !fwd.ClientClosed {
		t.Fatal("the cancelled request's provider connection was not closed by dsh before the response ended")
	}

	run2 := h.awaitRun(t, session, h.prompt(t, session, "repeat your last sentence"), 10*time.Minute)
	logRequestIDs(t, inRun(h.history(t, session), run2.FirstSeq, run2.LastSeq))
	sent := sentBodies(proxy, "official")
	if len(sent) < 2 {
		t.Fatalf("the proxy forwarded %d requests, want the cancelled one and the next", len(sent))
	}
	if !bytes.Contains(sent[1], jsonText(shown)) {
		t.Fatalf("the second request does not carry the text shown before the cancel %q:\n%s", shown, sent[1])
	}
}

// selfCheckProxyClose proves the provider-close oracle needs no key and does not
// race the proxy: with every handler held before it records its outcome,
// settled does not return until the handler does; then a stream the client
// abandoned mid-body settles ClientClosed and a response read to its end does
// not.
func selfCheckProxyClose(t *testing.T) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: first\n\n"))
		w.(http.Flusher).Flush()
		if r.URL.Path == "/endless" {
			<-r.Context().Done()
		}
	}))
	t.Cleanup(upstream.Close)
	proxy := startProxy(t, map[string]string{"up": upstream.URL})
	hold := make(chan struct{})
	proxy.mu.Lock()
	proxy.hold = hold
	proxy.mu.Unlock()

	ctx, cancel := context.WithCancel(t.Context())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, proxy.base("up")+"/endless", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("self-check: %v", err)
	}
	if _, err := resp.Body.Read(make([]byte, 64)); err != nil {
		t.Fatalf("self-check: no first chunk: %v", err)
	}
	cancel()
	_ = resp.Body.Close()
	// The client is gone but the handler is held: its outcome is not recorded,
	// so a snapshot taken now would read not-closed, and settled must wait.
	got := make(chan proxiedRequest, 1)
	go func() { got <- proxy.settled(t, "up", 0, 10*time.Second) }()
	select {
	case r := <-got:
		t.Fatalf("self-check: settled returned %+v before the held handler recorded its outcome", r)
	default:
	}
	if snap := proxy.requests("up")[0]; snap.ClientClosed {
		t.Fatal("self-check: the held handler recorded its outcome early")
	}
	close(hold)
	if r := <-got; !r.ClientClosed {
		t.Fatal("self-check: an abandoned stream did not settle ClientClosed")
	}

	resp, err = http.Get(proxy.base("up") + "/once")
	if err != nil {
		t.Fatalf("self-check: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if got := proxy.settled(t, "up", 1, 10*time.Second); got.ClientClosed {
		t.Fatal("self-check: a response read to its end settled ClientClosed")
	}
}

// streamedText joins the text chunks h was streamed for turn of session; the
// caller holds h.mu, as until's match does.
func streamedText(h *sdkHost, session string, turn int64) string {
	var b strings.Builder
	for _, s := range h.streams {
		if s.SessionID == session && s.Turn == turn && s.Kind == "chunk" {
			b.WriteString(s.Text)
		}
	}
	return b.String()
}

// jsonText is text as it appears inside a JSON string, without the quotes.
func jsonText(text string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(text)
	out := bytes.TrimSpace(b.Bytes())
	return out[1 : len(out)-1]
}
