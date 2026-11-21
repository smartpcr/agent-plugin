//go:build scenario

package scenarios

import (
	"strconv"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestBadKeyIsAFact: a failure is a fact -- no retry and no fallback on a bad key
// (areas/llm.md DH-E-LLM-3; DH-D-LLM-1, DH-D-LLM-2, DH-D-LLM-3; SP-2). Authored in
// DH-S-9; executed in DH-S-17, which requires the agent loop (DH-S-11) and the
// headless launcher (DH-S-16).
//
// Given the built dsh and a fresh temporary home, two routes -- deepseek-official
// with a deliberately invalid key and a second working keyed route whose real key
// is supplied at run time -- retry mode persistent, and a recording proxy in front
// of both routes, when one prompt is sent to deepseek-official and then one with
// MaxTokens set too low for the answer on the second route, then the first turn
// ends with llm/auth, its HTTP status and request id recorded, exactly one
// assistant/attempt, llm/retry-abandoned with reason not-retryable and no
// llm/retry, and the proxy shows exactly one request to deepseek-official and none
// to the second route; the second turn, on the working route, ends with MaxTokens
// and no executed tool call. Cleanup: the proxy stops and the home is deleted; on
// failure the exported logs are kept and the provider request ids logged.
//
// Inputs: DSH_SCENARIO_LLM3_OFFICIAL is the base URL of DeepSeek's Messages
// endpoint and DSH_SCENARIO_LLM3_SECOND_URL the second route's; the proxy fronts
// them as {official} and {second}. DSH_SCENARIO_LLM3_LAYER is the layer text over
// {official} and {second} that points deepseek-official at {official} with its key
// from DEEPSEEK_API_KEY, defines the second route at {second} keyed from
// DSH_SCENARIO_LLM3_KEY_ENV, and sets retry mode persistent;
// DSH_SCENARIO_LLM3_MODEL is the model on deepseek-official;
// DSH_SCENARIO_LLM3_SECOND_ROUTE and DSH_SCENARIO_LLM3_SECOND_MODEL name the second
// route and its model; DSH_SCENARIO_LLM3_KEY_ENV is the environment variable the
// second route's key is read from and DSH_SCENARIO_LLM3_KEY its real value;
// DSH_SCENARIO_LLM3_PROMPT is a prompt whose answer exceeds
// DSH_SCENARIO_LLM3_MAX_TOKENS output tokens.
//
//dsh:scenario DH-E-LLM-3
func TestBadKeyIsAFact(t *testing.T) {
	officialURL := need(t, "DSH_SCENARIO_LLM3_OFFICIAL", "the base URL of DeepSeek's Messages endpoint")
	secondURL := need(t, "DSH_SCENARIO_LLM3_SECOND_URL", "the base URL of the second route")
	layerT := need(t, "DSH_SCENARIO_LLM3_LAYER", "layer text over {official} and {second}: two routes, retry mode persistent")
	model := need(t, "DSH_SCENARIO_LLM3_MODEL", "the model on deepseek-official")
	secondRoute := need(t, "DSH_SCENARIO_LLM3_SECOND_ROUTE", "the second route's id")
	secondModel := need(t, "DSH_SCENARIO_LLM3_SECOND_MODEL", "the second route's model")
	keyEnv := need(t, "DSH_SCENARIO_LLM3_KEY_ENV", "the variable the second route's key is read from")
	key := need(t, "DSH_SCENARIO_LLM3_KEY", "the second route's real key")
	prompt := need(t, "DSH_SCENARIO_LLM3_PROMPT", "a prompt with a long answer")
	maxTokens, err := strconv.Atoi(need(t, "DSH_SCENARIO_LLM3_MAX_TOKENS", "an output bound too low for the answer"))
	if err != nil || maxTokens <= 0 {
		t.Fatalf("DSH_SCENARIO_LLM3_MAX_TOKENS: want a positive count: %v", err)
	}

	proxy := startProxy(t, map[string]string{"official": officialURL, "second": secondURL})
	layer := writeLayer(t, expand(layerT, map[string]string{"official": proxy.base("official"), "second": proxy.base("second")}))
	env := []string{"DEEPSEEK_API_KEY=sk-dsh-scenario-deliberately-invalid", keyEnv + "=" + key}
	h := startSDK(t, dshBinary(t), env, []string{"--layer", layer})

	// The bad key: one attempt, no retry, no fallback.
	bad := h.create(t, &protocol.ModelRoute{Provider: "deepseek-official", Model: model})
	h.keepExport(t, bad)
	run1 := h.awaitRun(t, bad, h.prompt(t, bad, prompt), 5*time.Minute)
	evs1 := inRun(h.history(t, bad), run1.FirstSeq, run1.LastSeq)
	logRequestIDs(t, evs1)
	end1 := turnEnd(t, evs1)
	if end1.Reason.Failure == nil || end1.Reason.Failure.Code != "llm/auth" {
		t.Fatalf("turn 1 ended %+v, want the failure llm/auth", end1.Reason)
	}
	settled := ofType(evs1, "assistant/attempt", "assistant/message")
	if len(settled) != 1 || settled[0].Type != "assistant/attempt" {
		t.Fatalf("turn 1 settled %d attempts, want exactly one assistant/attempt", len(settled))
	}
	var attempt wireSettlement
	decode(t, settled[0], &attempt)
	if f := attempt.Failure; f == nil || f.Code != "llm/auth" || f.Class != "permanent" || f.Status == 0 || f.RequestID == "" {
		t.Fatalf("the attempt's failure %+v, want llm/auth, permanent, with HTTP status and request id", f)
	}
	if n := len(ofType(evs1, "llm/retry")); n != 0 {
		t.Fatalf("%d llm/retry events on a bad key, want none", n)
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
	if n := len(proxy.requests("official")); n != 1 {
		t.Fatalf("the proxy saw %d requests to deepseek-official, want exactly 1", n)
	}
	if n := len(proxy.requests("second")); n != 0 {
		t.Fatalf("the proxy saw %d requests to the second route after a bad key, want none (no fallback)", n)
	}

	// The working route with an output bound too low for the answer.
	good := h.create(t, &protocol.ModelRoute{Provider: secondRoute, Model: secondModel, MaxOutputTokens: maxTokens})
	h.keepExport(t, good)
	run2 := h.awaitRun(t, good, h.prompt(t, good, prompt), 5*time.Minute)
	evs2 := inRun(h.history(t, good), run2.FirstSeq, run2.LastSeq)
	if end2 := turnEnd(t, evs2); end2.Reason.Kind != "max-tokens" {
		t.Fatalf("turn 2 ended %+v, want max-tokens", end2.Reason)
	}
	if n := len(ofType(evs2, "tool/result")); n != 0 {
		t.Fatalf("turn 2 executed %d tool calls, want none", n)
	}
	if len(proxy.requests("second")) == 0 {
		t.Fatal("the second turn's request did not pass the proxy")
	}
}
