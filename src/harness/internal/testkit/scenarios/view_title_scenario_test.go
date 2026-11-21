//go:build scenario

package scenarios

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestModelTitlesSession: a real model titles a session, and a failing provider
// leaves the fallback (areas/session-views.md DH-E-VIEW-1; DH-D-VIEW-4). Authored
// in DH-S-9; executed in DH-S-17, which requires the agent loop (DH-S-11) and the
// titles and headless launcher of DH-S-16. Nothing here produces a title: the
// scenario only reads what the product's session/list and session/history return.
//
// Given a built dsh, an empty temporary home, one real model API key supplied at
// run time and the default title configuration, with a recording proxy in front
// of the provider, when (1) one prompt is sent, (2) the session's events are read
// and (3) a second session is started with the title route keyed by a key the
// provider rejects and one prompt is sent, then (1) the list shows a fallback
// title and then a generated one; (2) the log holds session/title-request before
// any session/title-attempt, one attempt with outcome accepted and a
// session/title that cites it, and the request text equals what the proxy saw;
// (3) the second session keeps its fallback, holds one attempt with outcome
// failed, and its turn completed normally. Cleanup: the home is deleted by the
// launcher; on failure both session exports are kept (their paths are logged).
//
// Inputs: DSH_DEEPSEEK_API_KEY is the real key (the workflow's secret), handed to
// the program as DEEPSEEK_API_KEY; DSH_SCENARIO_VIEW1_OFFICIAL the base URL of
// DeepSeek's Messages endpoint, fronted by the proxy as {official};
// DSH_SCENARIO_VIEW1_LAYER the layer text over {official} that points the model
// and the default title route at {official}; DSH_SCENARIO_VIEW1_BAD_LAYER the same
// with the title route keyed from SCENARIO_VIEW1_TITLE_KEY, which the scenario
// sets to a rejected key; DSH_SCENARIO_VIEW1_MODEL the model on
// deepseek-official; DSH_SCENARIO_VIEW1_PROMPT the prompt.
//
//dsh:scenario DH-E-VIEW-1
func TestModelTitlesSession(t *testing.T) {
	key := need(t, "DSH_DEEPSEEK_API_KEY", "a real DeepSeek API key")
	officialURL := need(t, "DSH_SCENARIO_VIEW1_OFFICIAL", "the base URL of DeepSeek's Messages endpoint")
	goodT := need(t, "DSH_SCENARIO_VIEW1_LAYER", "layer text over {official} with the default title configuration")
	badT := need(t, "DSH_SCENARIO_VIEW1_BAD_LAYER", "layer text over {official} keying the title route from SCENARIO_VIEW1_TITLE_KEY")
	model := need(t, "DSH_SCENARIO_VIEW1_MODEL", "the model on deepseek-official")
	prompt := need(t, "DSH_SCENARIO_VIEW1_PROMPT", "a prompt")
	bin := dshBinary(t)
	route := &protocol.ModelRoute{Provider: "deepseek-official", Model: model}

	// (1)-(2) A real model titles the session.
	proxy := startProxy(t, map[string]string{"official": officialURL})
	vars := map[string]string{"official": proxy.base("official")}
	h := startSDK(t, bin, []string{"DEEPSEEK_API_KEY=" + key}, []string{"--layer", writeLayer(t, expand(goodT, vars))})
	session := h.create(t, route)
	h.keepExport(t, session)
	receipt := h.prompt(t, session, prompt)
	titles := h.watchTitles(t, session, 5*time.Minute, func() bool {
		return len(ofType(h.history(t, session), "session/title")) > 0
	})
	run := h.awaitRun(t, session, receipt, 5*time.Minute)
	if end := turnEnd(t, inRun(h.history(t, session), run.FirstSeq, run.LastSeq)); end.Reason.Kind != "completed" {
		t.Fatalf("the titled session's turn ended %q, want completed", end.Reason.Kind)
	}
	evs := h.history(t, session)
	titleEvs := ofType(evs, "session/title")
	if len(titleEvs) != 1 {
		t.Fatalf("%d session/title events, want 1", len(titleEvs))
	}
	var title struct {
		Title string `json:"title"`
	}
	decode(t, titleEvs[0], &title)
	if len(titles) < 2 || titles[len(titles)-1] != title.Title {
		t.Fatalf("the list showed titles %q, want a fallback and then the generated %q", titles, title.Title)
	}
	requests, attempts := ofType(evs, "session/title-request"), ofType(evs, "session/title-attempt")
	if len(requests) == 0 || len(attempts) != 1 || requests[0].Seq > attempts[0].Seq {
		t.Fatalf("%d title requests and %d attempts, want a session/title-request before the one session/title-attempt", len(requests), len(attempts))
	}
	if outcome := titleOutcome(t, attempts[0]); outcome != "accepted" {
		t.Fatalf("title attempt outcome %q, want accepted", outcome)
	}
	if !slices.Contains(titleEvs[0].Sources, seqString(attempts[0].Seq)) {
		t.Fatalf("session/title cites %v, want the accepted attempt %d", titleEvs[0].Sources, attempts[0].Seq)
	}
	var request struct {
		Text string `json:"text"`
	}
	decode(t, requests[0], &request)
	if request.Text == "" || !proxySaw(proxy, "official", request.Text) {
		t.Fatalf("the title request text %q is not what the proxy saw", request.Text)
	}

	// (3) A provider that rejects the title route's key leaves the fallback.
	bad := startSDK(t, bin, []string{"DEEPSEEK_API_KEY=" + key, "SCENARIO_VIEW1_TITLE_KEY=sk-dsh-scenario-deliberately-invalid"},
		[]string{"--layer", writeLayer(t, expand(badT, vars))})
	kept := bad.create(t, route)
	bad.keepExport(t, kept)
	run2 := bad.awaitRun(t, kept, bad.prompt(t, kept, prompt), 5*time.Minute)
	keptTitles := bad.watchTitles(t, kept, 5*time.Minute, func() bool {
		return len(ofType(bad.history(t, kept), "session/title-attempt")) > 0
	})
	evs2 := bad.history(t, kept)
	if end := turnEnd(t, inRun(evs2, run2.FirstSeq, run2.LastSeq)); end.Reason.Kind != "completed" {
		t.Fatalf("the failing-title session's turn ended %q, want completed normally", end.Reason.Kind)
	}
	attempts2 := ofType(evs2, "session/title-attempt")
	if len(attempts2) != 1 || titleOutcome(t, attempts2[0]) != "failed" {
		t.Fatalf("%d title attempts, want one with outcome failed", len(attempts2))
	}
	if n := len(ofType(evs2, "session/title")); n != 0 {
		t.Fatalf("%d session/title events with a rejected key, want none", n)
	}
	if len(keptTitles) != 1 || keptTitles[0] == "" {
		t.Fatalf("the list showed titles %q, want the fallback alone", keptTitles)
	}
}

// watchTitles polls session/list until done holds, and returns the distinct
// non-empty titles the list showed for session, in order.
func (h *sdkHost) watchTitles(t *testing.T, session string, within time.Duration, done func() bool) []string {
	t.Helper()
	var titles []string
	deadline := time.Now().Add(within)
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	for {
		var page protocol.SessionListResult
		h.call(t, "session/list", protocol.SessionListParams{Limit: 100}, &page)
		for _, s := range page.Sessions {
			if s.SessionID == session && s.Title != "" && (len(titles) == 0 || titles[len(titles)-1] != s.Title) {
				titles = append(titles, s.Title)
			}
		}
		if done() {
			return titles
		}
		if time.Now().After(deadline) {
			t.Fatalf("the title of %s did not settle within %s (titles %q)", session, within, titles)
		}
		<-poll.C
	}
}

// titleOutcome is the outcome of a session/title-attempt.
func titleOutcome(t *testing.T, e protocol.Event) string {
	t.Helper()
	var a struct {
		Outcome string `json:"outcome"`
	}
	decode(t, e, &a)
	return a.Outcome
}

// seqString writes a seq the way an event's sources cite it, in decimal.
func seqString(seq int64) string { return strconv.FormatInt(seq, 10) }

// proxySaw reports whether a request the proxy forwarded to upstream carried text,
// in its JSON-escaped form.
func proxySaw(p *recordingProxy, upstream, text string) bool {
	quoted, _ := json.Marshal(text)
	quoted = bytes.Trim(quoted, `"`)
	for _, r := range p.requests(upstream) {
		if bytes.Contains(r.Body, quoted) {
			return true
		}
	}
	return false
}
