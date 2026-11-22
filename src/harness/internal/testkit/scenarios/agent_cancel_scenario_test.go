//go:build scenario

package scenarios

import (
	"bytes"
	"encoding/json"
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
// aborted with cause user; its one settlement is an interrupted assistant message
// holding the text that was streamed, whose failure is the cancel (the provider
// connection closed by it); and the second request the proxy forwarded carries
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
	settled := ofType(evs1, "assistant/message", "assistant/attempt")
	if len(settled) != 1 || !bytes.Contains(settled[0].Data, []byte("interrupted")) {
		t.Fatalf("turn 1 has %d settlements, want one interrupted assistant message: %v", len(settled), eventTypes(settled))
	}
	var s wireSettlement
	decode(t, settled[0], &s)
	if s.Failure == nil || s.Failure.Class != "cancelled" {
		t.Fatalf("the interrupted attempt's failure is %+v, want class cancelled (the connection closed by the cancel)", s.Failure)
	}
	if !bytes.Contains(settled[0].Data, jsonText(shown)) {
		t.Fatalf("the interrupted message does not hold the streamed text %q:\n%s", shown, settled[0].Data)
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
