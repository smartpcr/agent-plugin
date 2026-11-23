//go:build scenario

package scenarios

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestSignedOutIsAFact: a failure is a fact -- no retry and no fallback when the
// client is signed out (areas/llm.md DH-E-LLM-3; DH-D-LLM-1, DH-D-LLM-2,
// DH-D-LLM-3, on the route of DH-D-LLM-12; SP-2). Authored in DH-S-9 as the
// bad-key scenario and re-authored onto the signed-in client routes by
// gap-closure-plan 16.5.8 (M1-D-CLOSE-4 A), "signed out" replacing "bad key";
// executed in DH-S-17, which requires the agent loop (DH-S-11) and the headless
// launcher (DH-S-16).
//
// Given the built dsh, fresh temporary homes and no API key anywhere, a layer
// that registers the route under test and the other client route as a second
// route and sets retry mode persistent, and the user's own installed client of
// the route, when, per route, (1) one prompt is sent with that client signed
// out (its configuration home an empty temporary directory), then, with the
// client signed in as the user, (2) one prompt with an output cap and (3) the
// same prompt without it, then (1) the turn ends with llm/sign-in-required,
// exactly one assistant/attempt (Permanent, with its client invocation),
// llm/retry-abandoned with reason not-retryable and no llm/retry, and no
// settlement of the session names the second route's client (no fallback); (2)
// the cap is refused before the client starts, since a client route takes no
// output cap (claude-code-cli.md, github-copilot-cli.md sec. 5.1): the turn ends
// error with llm/unsupported-option, no settlement records a client invocation
// and nothing is retried; (3) the turn completes with one assistant/message of
// the route's client and no executed tool call, so the failure of (1) was the
// sign-in and not the route. Cleanup: the homes and the empty configuration home
// are deleted; nothing of the user's sign-in is read or changed; on failure the
// exported logs are kept and the client invocations logged.
//
// The legs: claude-code must pass; copilot-cli is run and recorded either way
// (M1-D-LOOP-6 A; forEachRoute). The Copilot leg's signed-out state and code are
// open (signedin_routes_copilot_test.go).
//
// Inputs: DSH_SCENARIO_LLM3_LAYER is the layer text over {route} and {second}
// that registers both client routes and sets retry mode persistent;
// DSH_SCENARIO_<route>_MODEL (<route> is CLAUDE_CODE or COPILOT_CLI) the model;
// DSH_SCENARIO_LLM3_PROMPT a prompt the model answers in text without a tool;
// DSH_SCENARIO_LLM3_MAX_TOKENS a positive output cap.
//
//dsh:scenario DH-E-LLM-3
func TestSignedOutIsAFact(t *testing.T) {
	layerT := need(t, "DSH_SCENARIO_LLM3_LAYER", "layer text over {route} and {second}: both client routes, retry mode persistent")
	prompt := need(t, "DSH_SCENARIO_LLM3_PROMPT", "a prompt answered in text without a tool")
	maxTokens, err := strconv.Atoi(need(t, "DSH_SCENARIO_LLM3_MAX_TOKENS", "a positive output cap"))
	if err != nil || maxTokens <= 0 {
		t.Fatalf("DSH_SCENARIO_LLM3_MAX_TOKENS: want a positive count: %v", err)
	}
	bin := dshBinary(t)
	forEachRoute(t, func(t *testing.T, r clientRoute) {
		second := r.other()
		layer := writeLayer(t, expand(layerT, map[string]string{"route": r.ID, "second": second.ID}))
		signedOutIsAFact(t, bin, r, second, layer, prompt, maxTokens)
	})
}

// signedOutIsAFact is DH-E-LLM-3 on route r with second as the second route.
func signedOutIsAFact(t *testing.T, bin string, r, second clientRoute, layer, prompt string, maxTokens int) {
	t.Helper()
	route := &protocol.ModelRoute{Provider: r.ID, Model: r.model(t)}

	// (1) Signed out: one attempt, no retry, no fallback.
	out := startSDK(t, bin, []string{r.signedOut(t)}, []string{"--layer", layer})
	bad := out.create(t, route)
	out.keepExport(t, bad)
	run1 := out.awaitRun(t, bad, out.prompt(t, bad, prompt), 5*time.Minute)
	evs1 := inRun(out.history(t, bad), run1.FirstSeq, run1.LastSeq)
	logInvocations(t, evs1)
	end1 := turnEnd(t, evs1)
	if end1.Reason.Failure == nil || end1.Reason.Failure.Code != "llm/sign-in-required" {
		t.Fatalf("turn 1 ended %+v, want the failure llm/sign-in-required", end1.Reason)
	}
	settled := ofType(evs1, "assistant/attempt", "assistant/message")
	if len(settled) != 1 || settled[0].Type != "assistant/attempt" {
		t.Fatalf("turn 1 settled %d attempts, want exactly one assistant/attempt", len(settled))
	}
	var attempt wireSettlement
	decode(t, settled[0], &attempt)
	if f := attempt.Failure; f == nil || f.Code != "llm/sign-in-required" || f.Class != "permanent" {
		t.Fatalf("the attempt's failure %+v, want llm/sign-in-required, permanent", f)
	}
	requireInvocation(t, settled[0], r)
	if n := len(ofType(evs1, "llm/retry")); n != 0 {
		t.Fatalf("%d llm/retry events on a signed-out client, want none", n)
	}
	abandoned := ofType(evs1, "llm/retry-abandoned")
	if len(abandoned) != 1 {
		t.Fatalf("%d llm/retry-abandoned events, want 1", len(abandoned))
	}
	var why struct {
		Reason string `json:"reason"`
	}
	decode(t, abandoned[0], &why)
	if why.Reason != "not-retryable" {
		t.Fatalf("llm/retry-abandoned reason %q, want not-retryable", why.Reason)
	}
	for _, e := range ofType(out.history(t, bad), "assistant/attempt", "assistant/message") {
		if inv := invocationOf(t, e); inv == nil || inv.Client != r.Client {
			t.Fatalf("event %d %s records invocation %+v after a signed-out %s, want only %s (no fallback to %s)", e.Seq, e.Type, inv, r.ID, r.Client, second.ID)
		}
	}

	// (2) Signed in, with an output cap the client cannot take: refused before
	// the client starts, and not retried.
	in := startSDK(t, bin, nil, []string{"--layer", layer})
	capped := in.create(t, &protocol.ModelRoute{Provider: r.ID, Model: route.Model, MaxOutputTokens: maxTokens})
	in.keepExport(t, capped)
	run2 := in.awaitRun(t, capped, in.prompt(t, capped, prompt), 5*time.Minute)
	evs2 := inRun(in.history(t, capped), run2.FirstSeq, run2.LastSeq)
	var end2 struct {
		Reason struct {
			Kind   string `json:"kind"`
			Detail string `json:"detail"`
		} `json:"reason"`
	}
	ends := ofType(evs2, "turn/end")
	if len(ends) != 1 {
		t.Fatalf("%d turn/end events in the capped run, want 1", len(ends))
	}
	decode(t, ends[0], &end2)
	if end2.Reason.Kind != "error" || !strings.Contains(end2.Reason.Detail, "llm/unsupported-option") {
		t.Fatalf("the capped turn ended %+v, want error with llm/unsupported-option", end2.Reason)
	}
	if n := len(ofType(evs2, "assistant/message")); n != 0 {
		t.Fatalf("the capped turn holds %d assistant/message, want none", n)
	}
	for _, e := range ofType(evs2, "assistant/attempt") {
		if inv := invocationOf(t, e); inv != nil {
			t.Fatalf("the capped turn started the client (%+v), want the cap refused before it starts", inv)
		}
	}
	if n := len(ofType(evs2, "llm/retry")); n != 0 {
		t.Fatalf("%d llm/retry events on a refused option, want none", n)
	}

	// (3) Signed in, without the cap: the route works, so (1) was the sign-in.
	plain := in.create(t, route)
	in.keepExport(t, plain)
	run3 := in.awaitRun(t, plain, in.prompt(t, plain, prompt), 5*time.Minute)
	evs3 := inRun(in.history(t, plain), run3.FirstSeq, run3.LastSeq)
	logInvocations(t, evs3)
	if end3 := turnEnd(t, evs3); end3.Reason.Kind != "completed" {
		t.Fatalf("the signed-in turn ended %+v, want completed", end3.Reason)
	}
	msgs := ofType(evs3, "assistant/attempt", "assistant/message")
	if len(msgs) != 1 || msgs[0].Type != "assistant/message" {
		t.Fatalf("the signed-in turn settled %d attempts, want exactly one assistant/message", len(msgs))
	}
	requireInvocation(t, msgs[0], r)
	if n := len(ofType(evs3, "tool/result")); n != 0 {
		t.Fatalf("the signed-in turn executed %d tool calls, want none", n)
	}
}
