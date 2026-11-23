//go:build scenario

package scenarios

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestModelTitlesSession: a real model titles a session, and a failing title
// route leaves the fallback (areas/session-views.md DH-E-VIEW-1; DH-D-VIEW-4).
// Authored in DH-S-9 and re-authored onto the signed-in client routes by
// gap-closure-plan 16.5.8 (M1-D-CLOSE-4 A), "signed out" replacing "a key the
// provider rejects"; executed in DH-S-17, which requires the agent loop (DH-S-11)
// and the titles and headless launcher of DH-S-16. Nothing here produces a
// title: the scenario only reads what the product's session/list and
// session/history return.
//
// Given a built dsh, an empty temporary home, no API key anywhere, the user's own
// installed client of the route signed in as themselves, a layer that registers
// the route with the default title configuration, when, per route, (1) one prompt
// is sent, (2) the session's events are read and (3) a second dsh is started
// whose title route is the other client route with that client signed out (its
// configuration home an empty temporary directory), and one prompt is sent, then
// (1) the list shows a fallback title and then a generated one; (2) the log holds
// session/title-request before any session/title-attempt, one attempt with
// outcome accepted that records the client invocation of the route (client,
// version, argument and standard-input digests: the request reached the client
// through the logged invocation, where SP-1 holds, M1-D-PIPE-4 A), and a
// session/title that cites it; (3) the second session keeps its fallback, holds
// one attempt with outcome failed, and its turn completed normally. The byte
// equality of a logged request and what the client received is not re-checked
// here: it is DH-E-SESSION-1's and DH-T-LLM-20's. Cleanup: the homes and the
// empty configuration home are deleted; nothing of the user's sign-in is touched;
// on failure both session exports are kept (their paths are logged).
//
// The legs: claude-code must pass; copilot-cli is run and recorded either way
// (M1-D-LOOP-6 A; forEachRoute). Step (3) of the Claude leg signs the Copilot
// client out; if an empty COPILOT_HOME does not (signedin_routes_copilot_test.go),
// the layer must name a title route that fails by other means, because only the
// failed title attempt is asserted.
//
// Inputs: DSH_SCENARIO_VIEW1_LAYER the layer text over {route} that registers
// that client route with the default title configuration;
// DSH_SCENARIO_VIEW1_BAD_LAYER the layer text over {route} and {title} that
// registers both client routes and sets the title route to {title};
// DSH_SCENARIO_<route>_MODEL (<route> is CLAUDE_CODE or COPILOT_CLI) the model;
// DSH_SCENARIO_VIEW1_PROMPT the prompt.
//
//dsh:scenario DH-E-VIEW-1
func TestModelTitlesSession(t *testing.T) {
	goodT := need(t, "DSH_SCENARIO_VIEW1_LAYER", "layer text over {route} with the default title configuration")
	badT := need(t, "DSH_SCENARIO_VIEW1_BAD_LAYER", "layer text over {route} and {title} setting the title route to {title}")
	prompt := need(t, "DSH_SCENARIO_VIEW1_PROMPT", "a prompt")
	bin := dshBinary(t)
	forEachRoute(t, func(t *testing.T, r clientRoute) {
		title := r.other()
		good := expand(goodT, map[string]string{"route": r.ID})
		bad := expand(badT, map[string]string{"route": r.ID, "title": title.ID})
		modelTitlesSession(t, bin, r, title, good, bad, prompt)
	})
}

// modelTitlesSession is DH-E-VIEW-1 on route r, with title, signed out, as the
// failing title route of step (3).
func modelTitlesSession(t *testing.T, bin string, r, title clientRoute, goodLayer, badLayer, prompt string) {
	t.Helper()
	route := &protocol.ModelRoute{Provider: r.ID, Model: r.model(t)}

	// (1)-(2) A real model titles the session.
	h := startSDK(t, bin, nil, []string{"--layer", writeLayer(t, goodLayer)})
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
	var named struct {
		Title string `json:"title"`
	}
	decode(t, titleEvs[0], &named)
	if len(titles) < 2 || titles[len(titles)-1] != named.Title {
		t.Fatalf("the list showed titles %q, want a fallback and then the generated %q", titles, named.Title)
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
	if request.Text == "" {
		t.Fatal("the title request holds no text")
	}
	requireInvocation(t, attempts[0], r)

	// (3) A signed-out title route leaves the fallback.
	failing := startSDK(t, bin, []string{title.signedOut(t)}, []string{"--layer", writeLayer(t, badLayer)})
	kept := failing.create(t, route)
	failing.keepExport(t, kept)
	run2 := failing.awaitRun(t, kept, failing.prompt(t, kept, prompt), 5*time.Minute)
	keptTitles := failing.watchTitles(t, kept, 5*time.Minute, func() bool {
		return len(ofType(failing.history(t, kept), "session/title-attempt")) > 0
	})
	evs2 := failing.history(t, kept)
	if end := turnEnd(t, inRun(evs2, run2.FirstSeq, run2.LastSeq)); end.Reason.Kind != "completed" {
		t.Fatalf("the failing-title session's turn ended %q, want completed normally", end.Reason.Kind)
	}
	attempts2 := ofType(evs2, "session/title-attempt")
	if len(attempts2) != 1 || titleOutcome(t, attempts2[0]) != "failed" {
		t.Fatalf("%d title attempts, want one with outcome failed", len(attempts2))
	}
	if n := len(ofType(evs2, "session/title")); n != 0 {
		t.Fatalf("%d session/title events with a signed-out title route, want none", n)
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
