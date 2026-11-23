//go:build scenario

package scenarios

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestCancelledStreamKeepsWhatWasShown: cancelling a real stream keeps what was
// shown and records the cause (areas/agent-loop.md DH-E-AGENT-2; DH-D-AGENT-5,
// on the signed-in client route of areas/llm.md DH-D-LLM-12). Authored in
// DH-S-11 and retargeted onto the signed-in client routes by gap-closure-plan
// 17.4.4 (M1-D-CLOSE-4 A); executed in DH-S-17, which requires the agent loop
// (DH-S-11) and the headless launcher (DH-S-16).
//
// The boundary. On a keyed route a recording proxy showed that dsh closed the
// provider connection and that the next request carried the shown text. A
// client route starts the user's own client, which talks to its vendor itself,
// so no proxy can sit between them; the assertions move to the client
// invocation, where SP-1 holds (M1-D-PIPE-4 A, DH-ADR-LLM-10). That the cancel
// stops the client's whole process tree is proved locally against a stand-in
// client (DH-T-LLM-23, DH-T-LLM-27); here, nothing more of the cancelled attempt
// reaches dsh's stream after its turn ended.
//
// Given an empty temporary home, profile headless, no API key anywhere, the
// user's own installed official client signed in as themselves with its route
// registered by a layer and a small model, and a prompt that produces a long
// answer, when, per route, after the first text chunk arrives the SDK client
// cancels the turn (cause user) keeping the inbox, waits for idle and then sends
// "repeat your last sentence", then the first turn ends aborted with cause user;
// its one settlement is an interrupted assistant/message holding the text that
// was streamed, with no tool call (areas/agent-loop.md:556; the driver keeps a
// cancelled attempt's prefix as history), and recording the invocation of the
// route's client that was stopped; no chunk of the first turn is streamed after
// it ended; every settlement's client invocation equals the one rebuilt from the
// log (TestRequestsReceivedEqualRequestsRebuilt's oracle); and the standard
// input dsh wrote for the second turn's first attempt, rebuilt from the log and
// matching its logged digest, carries the shown text. Cleanup: the launcher
// stops dsh and deletes the home; nothing of the user's sign-in is touched. On
// failure the export is kept (its path is logged) and the client invocations
// are logged.
//
// The legs: claude-code must pass; copilot-cli is run and recorded either way
// (M1-D-LOOP-6 A; forEachRoute).
//
// Inputs: DSH_SCENARIO_AGENT2_LAYER a headless layer, a template over {route}
// and {workspace}, that registers that client route, with no title route;
// DSH_SCENARIO_<route>_MODEL (<route> is CLAUDE_CODE or COPILOT_CLI) a small
// model; DSH_SCENARIO_AGENT2_PROMPT a prompt whose answer is long enough to
// cancel mid-stream; DSH_SCENARIO_AGENT2_REPLAY the session/query text whose
// result is the client invocations rebuilt from the log (rebuiltInvocations).
//
//dsh:scenario DH-E-AGENT-2
func TestCancelledStreamKeepsWhatWasShown(t *testing.T) {
	selfCheckInvocationOracle(t)
	layerT := need(t, "DSH_SCENARIO_AGENT2_LAYER", "a headless layer over {route} that registers that client route")
	prompt := need(t, "DSH_SCENARIO_AGENT2_PROMPT", "a prompt with a long answer")
	replay := need(t, "DSH_SCENARIO_AGENT2_REPLAY", "the session/query text that rebuilds the client invocations")
	bin := dshBinary(t)
	forEachRoute(t, func(t *testing.T, r clientRoute) {
		model := r.model(t)
		vars := map[string]string{"route": r.ID, "workspace": filepath.ToSlash(t.TempDir())}
		h := startSDK(t, bin, nil, []string{"--layer", writeLayer(t, expand(layerT, vars))})
		session := h.create(t, &protocol.ModelRoute{Provider: r.ID, Model: model})
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
		logInvocations(t, evs1)
		if end := turnEnd(t, evs1); end.Reason.Kind != "aborted" || end.Reason.Cause != "user" {
			t.Fatalf("turn 1 ended %+v, want aborted with cause user", end.Reason)
		}
		h.mu.Lock()
		shown := streamedText(h, session, int64(run1.Turn))
		streamed1 := streamCount(h, session, int64(run1.Turn))
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
		// The client that was stopped is the one the settlement records.
		requireInvocation(t, settled[0], r)

		run2 := h.awaitRun(t, session, h.prompt(t, session, "repeat your last sentence"), 10*time.Minute)
		evs2 := inRun(h.history(t, session), run2.FirstSeq, run2.LastSeq)
		logInvocations(t, evs2)
		h.mu.Lock()
		late := streamCount(h, session, int64(run1.Turn)) - streamed1
		h.mu.Unlock()
		if late != 0 {
			t.Fatalf("%d chunks of the cancelled turn were streamed after it ended: the client was not stopped", late)
		}
		all := ofType(h.history(t, session), "assistant/message", "assistant/attempt")
		rebuilt := assertInvocationsRebuilt(t, h, session, replay, all, r)
		next := -1
		for i, e := range all {
			if e.Seq >= run2.FirstSeq && e.Seq <= run2.LastSeq {
				next = i
				break
			}
		}
		if next < 0 {
			t.Fatal("turn 2 settled no attempt")
		}
		// assertInvocationsRebuilt has checked that this text digests to what dsh
		// wrote to the client's standard input.
		if !strings.Contains(*rebuilt[next].Stdin, strings.TrimSpace(shown)) {
			t.Fatalf("the standard input of turn 2's first attempt does not carry the text shown before the cancel %q:\n%s", shown, *rebuilt[next].Stdin)
		}
	})
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

// streamCount is the number of stream notifications h received for turn of
// session; the caller holds h.mu.
func streamCount(h *sdkHost, session string, turn int64) int {
	n := 0
	for _, s := range h.streams {
		if s.SessionID == session && s.Turn == turn {
			n++
		}
	}
	return n
}
