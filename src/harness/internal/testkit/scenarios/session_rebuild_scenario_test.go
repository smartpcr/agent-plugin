//go:build scenario

package scenarios

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestRequestsReceivedEqualRequestsRebuilt: what dsh handed the signed-in client
// for each attempt equals what is rebuilt from the log (areas/core-session.md
// DH-E-SESSION-1; DH-D-SESSION-4, DH-D-SESSION-3, DH-D-SESSION-1, on the client
// route of areas/llm.md DH-D-LLM-12). Authored in DH-S-11 and retargeted onto
// the signed-in client routes by gap-closure-plan 17.4.4 (M1-D-CLOSE-4 A);
// executed in DH-S-17, which requires the agent loop (DH-S-11) and the headless
// launcher (DH-S-16).
//
// The boundary. A client route starts the user's own official client, which
// talks to its vendor itself, so no proxy can sit between the client and the
// vendor. SP-1 holds at the client invocation (M1-D-PIPE-4 A, DH-ADR-LLM-10):
// each settlement records the argument list (the attempt directory written as
// {attempt}), its digest and the digest of the standard input dsh wrote. That
// the client received exactly those bytes is proved locally against a stand-in
// client (DH-T-LLM-20, DH-T-LLM-24); what the client then sends its vendor is
// declared opaque.
//
// Given the built dsh, a fresh temporary home, no API key anywhere, the user's
// own installed official client signed in as themselves, its route registered by
// a headless layer that sends no request but the turns' (no title route), and a
// workspace holding one text file, when, per route, (1) the host starts
// headless, (2) a prompt that makes the model read the file with a tool is sent
// and the turn ends, (3) a second prompt is sent and its turn ends and (4) the
// host is asked, twice, to rebuild from the log the client invocation of every
// settlement at its requestAsOf, then the two rebuilds are the same bytes; every
// settlement (assistant/message and assistant/attempt) records a complete
// invocation of the route's client with a client session id of its own; the
// rebuild has exactly one entry per settlement, in log order; each rebuilt
// argument list and its digest equal the logged ones; and each rebuilt standard
// input has the logged digest, recomputed here with SHA-256. A failed attempt's
// invocation matches as well. Cleanup: the launcher stops the host and deletes
// the home; the workspace lies under t.TempDir(); nothing of the user's sign-in
// or client settings is touched. On failure the export is kept (its path is
// logged) and the client invocations are logged.
//
// The legs: claude-code must pass; copilot-cli is run and recorded either way
// (M1-D-LOOP-6 A; forEachRoute).
//
// Inputs: DSH_SCENARIO_SESSION1_LAYER a headless layer, a template over {route}
// and {workspace}, that registers that client route and sends no request other
// than the turns'; DSH_SCENARIO_<route>_MODEL (<route> is CLAUDE_CODE or
// COPILOT_CLI) the model; DSH_SCENARIO_SESSION1_PROMPT a prompt, a template over
// {workspace}, that reads notes.txt with scenario_read and answers;
// DSH_SCENARIO_SESSION1_REPLAY the session/query text whose result is the client
// invocations rebuilt from the log (rebuiltInvocations). The client is the one
// on the PATH, signed in beforehand by the user.
//
//dsh:scenario DH-E-SESSION-1
func TestRequestsReceivedEqualRequestsRebuilt(t *testing.T) {
	selfCheckInvocationOracle(t)
	layerT := need(t, "DSH_SCENARIO_SESSION1_LAYER", "a headless layer over {route} and {workspace} that registers that client route")
	promptT := need(t, "DSH_SCENARIO_SESSION1_PROMPT", "a prompt that reads notes.txt with scenario_read")
	replay := need(t, "DSH_SCENARIO_SESSION1_REPLAY", "the session/query text that rebuilds the client invocations")
	bin := dshBinary(t)
	forEachRoute(t, func(t *testing.T, r clientRoute) {
		model := r.model(t)
		ws := t.TempDir()
		writeFiles(t, ws, map[string]string{"notes.txt": "the answer is forty-two\n"})
		vars := map[string]string{"workspace": filepath.ToSlash(ws), "route": r.ID}

		h := startSDK(t, bin, nil, []string{"--layer", writeLayer(t, expand(layerT, vars))}, readTool(ws, nil))
		session := h.create(t, &protocol.ModelRoute{Provider: r.ID, Model: model})
		h.keepExport(t, session)

		run1 := h.awaitRun(t, session, h.prompt(t, session, expand(promptT, vars)), 10*time.Minute)
		evs1 := inRun(h.history(t, session), run1.FirstSeq, run1.LastSeq)
		logInvocations(t, evs1)
		if n := len(ofType(evs1, "tool/result")); n == 0 {
			t.Fatal("turn 1 made no tool call: the prompt must make the model read the file")
		}
		run2 := h.awaitRun(t, session, h.prompt(t, session, "Thank you. In one word, what was the answer?"), 10*time.Minute)
		logInvocations(t, inRun(h.history(t, session), run2.FirstSeq, run2.LastSeq))

		settlements := ofType(h.history(t, session), "assistant/message", "assistant/attempt")
		assertInvocationsRebuilt(t, h, session, replay, settlements, r)
	})
}

// readTool is the client tool scenario_read over the files of ws; seen, when set,
// is told every name it is asked for (it may be called concurrently).
func readTool(ws string, seen func(name string)) sdkTool {
	return sdkTool{
		Decl: protocol.ToolDeclaration{
			Name:        "scenario_read",
			Description: "Reads a file of the scenario's workspace by its name.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		},
		Answer: func(p protocol.ToolCallParams) protocol.ToolCallResult {
			var a struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(p.Arguments, &a); err != nil {
				return toolError(err)
			}
			name := filepath.Base(a.Path)
			if seen != nil {
				seen(name)
			}
			data, err := os.ReadFile(filepath.Join(ws, name))
			if err != nil {
				return toolError(err)
			}
			return protocol.ToolCallResult{Content: []protocol.ContentBlock{{Type: "text", Text: string(data)}}}
		},
	}
}

// toolError is a client tool's failed result carrying err.
func toolError(err error) protocol.ToolCallResult {
	return protocol.ToolCallResult{IsError: true, Content: []protocol.ContentBlock{{Type: "text", Text: err.Error()}}}
}

// writeFiles writes name -> text into dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// wireRebuilt is one client invocation the host rebuilt from the log: the
// argument list the route starts for the request rebuilt at the settlement's
// requestAsOf (attempt directory as {attempt}, the logged client session id),
// its digest, and that request rendered as the client's standard input, with
// its digest.
type wireRebuilt struct {
	Args        []string `json:"args"`
	ArgsDigest  string   `json:"args_digest"`
	StdinDigest string   `json:"stdin_digest"`
	Stdin       *string  `json:"stdin"`
}

// rebuiltInvocations reads a replay's result: the rebuilt client invocations as
// a JSON array in settlement order, or an object holding them under
// "invocations".
func rebuiltInvocations(t *testing.T, raw json.RawMessage) []wireRebuilt {
	t.Helper()
	var list []wireRebuilt
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	var wrapped struct {
		Invocations []wireRebuilt `json:"invocations"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil || wrapped.Invocations == nil {
		t.Fatalf("the replay answered neither an invocation array nor {\"invocations\": [...]}: %s", raw)
	}
	return wrapped.Invocations
}

// sha256Digest is "sha256:<hex>" of b, the form the invocation record uses.
func sha256Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// argsDigest is the digest of an argument list as the invocation record takes
// it: over its JSON encoding, HTML characters not escaped.
func argsDigest(args []string) string {
	if args == nil {
		args = []string{}
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(args)
	return sha256Digest(bytes.TrimSuffix(b.Bytes(), []byte("\n")))
}

// invocationMismatch is nil when the rebuilt invocation is the settled one: the
// same argument list and digest, the rebuilt list digesting to it, the same
// standard-input digest, and the rebuilt standard input digesting to it.
func invocationMismatch(settled *wireInvocation, rebuilt wireRebuilt) error {
	switch {
	case settled == nil:
		return errors.New("the settlement records no client invocation")
	case !slices.Equal(rebuilt.Args, settled.Args):
		return fmt.Errorf("rebuilt arguments %q, logged %q", rebuilt.Args, settled.Args)
	case rebuilt.ArgsDigest != settled.ArgsDigest || argsDigest(rebuilt.Args) != settled.ArgsDigest:
		return fmt.Errorf("rebuilt argument digest %s (recomputed %s), logged %s", rebuilt.ArgsDigest, argsDigest(rebuilt.Args), settled.ArgsDigest)
	case rebuilt.StdinDigest != settled.StdinDigest:
		return fmt.Errorf("rebuilt standard-input digest %s, logged %s", rebuilt.StdinDigest, settled.StdinDigest)
	case rebuilt.Stdin == nil:
		return errors.New("the rebuild carries no standard input")
	case sha256Digest([]byte(*rebuilt.Stdin)) != settled.StdinDigest:
		return fmt.Errorf("the rebuilt standard input digests to %s, logged %s", sha256Digest([]byte(*rebuilt.Stdin)), settled.StdinDigest)
	}
	return nil
}

// assertInvocationsRebuilt asks the host, twice, to rebuild session's client
// invocations from its log with replay, checks the two answers are the same
// bytes, that every settlement records a complete invocation of r's client with
// a client session id of its own, and that the rebuilt invocations equal the
// settled ones one for one, in order. It returns the rebuilt invocations.
func assertInvocationsRebuilt(t *testing.T, h *sdkHost, session, replay string, settlements []protocol.Event, r clientRoute) []wireRebuilt {
	t.Helper()
	rebuild := func() json.RawMessage {
		var q protocol.SessionQueryResult
		h.call(t, "session/query", protocol.SessionQueryParams{SessionID: session, Content: []protocol.ContentBlock{{Type: "text", Text: replay}}}, &q)
		return q.Result
	}
	first, second := rebuild(), rebuild()
	if len(bytes.TrimSpace(first)) == 0 || strings.TrimSpace(string(first)) == "null" {
		t.Fatalf("%q rebuilt no invocations", replay)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("two rebuilds differ:\n%s\n---\n%s", first, second)
	}
	rebuilt := rebuiltInvocations(t, first)
	if len(settlements) == 0 || len(rebuilt) != len(settlements) {
		t.Fatalf("the log rebuilt %d client invocations, it has %d settlements: an attempt with no rebuildable invocation, or one never settled", len(rebuilt), len(settlements))
	}
	sessions := map[string]bool{}
	for i, e := range settlements {
		inv := requireInvocation(t, e, r)
		if inv.SessionID == "" || sessions[inv.SessionID] {
			t.Fatalf("%s %d names client session %q, want a new one per attempt", e.Type, e.Seq, inv.SessionID)
		}
		sessions[inv.SessionID] = true
		if err := invocationMismatch(inv, rebuilt[i]); err != nil {
			t.Fatalf("%s %d: %v", e.Type, e.Seq, err)
		}
	}
	return rebuilt
}

// selfCheckInvocationOracle proves the invocation oracle needs no client and no
// sign-in: a rebuilt invocation equal to the settled one passes, and one that
// differs in its arguments, either digest or its standard input, or that has no
// standard input or no settled invocation, is refused.
func selfCheckInvocationOracle(t *testing.T) {
	t.Helper()
	args := []string{"-p", "--system-prompt-file", "{attempt}/system.txt", "--model", "m"}
	stdin := "The conversation so far <is> below & continues."
	settled := &wireInvocation{Client: "c", Version: "1.0.0", Args: args, ArgsDigest: argsDigest(args), StdinDigest: sha256Digest([]byte(stdin)), SessionID: "s"}
	good := wireRebuilt{Args: slices.Clone(args), ArgsDigest: settled.ArgsDigest, StdinDigest: settled.StdinDigest, Stdin: &stdin}
	if err := invocationMismatch(settled, good); err != nil {
		t.Fatalf("self-check: the oracle refused a matching invocation: %v", err)
	}
	other := stdin + " (changed)"
	otherArgs := append(slices.Clone(args), "--effort", "high")
	for name, bad := range map[string]wireRebuilt{
		"other arguments":        {Args: otherArgs, ArgsDigest: settled.ArgsDigest, StdinDigest: good.StdinDigest, Stdin: &stdin},
		"other argument digest":  {Args: good.Args, ArgsDigest: argsDigest(otherArgs), StdinDigest: good.StdinDigest, Stdin: &stdin},
		"other stdin digest":     {Args: good.Args, ArgsDigest: good.ArgsDigest, StdinDigest: sha256Digest([]byte(other)), Stdin: &stdin},
		"other standard input":   {Args: good.Args, ArgsDigest: good.ArgsDigest, StdinDigest: good.StdinDigest, Stdin: &other},
		"no standard input":      {Args: good.Args, ArgsDigest: good.ArgsDigest, StdinDigest: good.StdinDigest},
		"HTML-escaped arguments": {Args: []string{"<a>"}, ArgsDigest: sha256Digest([]byte(`["<a>"]`)), StdinDigest: good.StdinDigest, Stdin: &stdin},
	} {
		s := settled
		if name == "HTML-escaped arguments" {
			c := *settled
			c.Args, c.ArgsDigest = []string{"<a>"}, sha256Digest([]byte(`["<a>"]`))
			s = &c
		}
		if err := invocationMismatch(s, bad); err == nil {
			t.Fatalf("self-check: the oracle accepted %s", name)
		}
	}
	if err := invocationMismatch(nil, good); err == nil {
		t.Fatal("self-check: the oracle accepted a settlement with no invocation")
	}
}
