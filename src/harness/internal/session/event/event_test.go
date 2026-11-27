package event_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

func msg(t *testing.T, role vocab.Role, text string) vocab.Message {
	t.Helper()
	m, err := vocab.NewMessage(role, vocab.TextBlock(text))
	if err != nil {
		t.Fatalf("NewMessage(%s): %v", role, err)
	}
	return m
}

type note struct {
	Text string `json:"text"`
}

// DH-T-SESSION-1: a declaration is refused when duplicated, malformed or late.
//
//dsh:test DH-T-SESSION-1
func TestDefineRefusals(t *testing.T) {
	r := event.NewRegistry()
	if _, err := event.Define(r, event.Spec[note]{Name: "ext/note", Version: 1}); err != nil {
		t.Fatalf("Define: %v", err)
	}
	cases := []struct {
		name string
		spec event.Spec[note]
		want error
	}{
		{"duplicate", event.Spec[note]{Name: "ext/note", Version: 1}, event.ErrDuplicate},
		{"no namespace", event.Spec[note]{Name: "note", Version: 1}, event.ErrInvalidDeclaration},
		{"upper case", event.Spec[note]{Name: "Ext/Note", Version: 1}, event.ErrInvalidDeclaration},
		{"plugin prefix", event.Spec[note]{Name: "plugin:x/y", Version: 1}, event.ErrInvalidDeclaration},
		{"reserved namespace", event.Spec[note]{Name: "session/mine", Version: 1}, event.ErrReservedType},
		{"version zero", event.Spec[note]{Name: "ext/zero", Version: 0}, event.ErrInvalidDeclaration},
		{"gap in upgrades", event.Spec[note]{Name: "ext/gap", Version: 3, Upgrades: map[int]event.Upgrade{1: idUpgrade}}, event.ErrInvalidDeclaration},
		{"extra upgrade", event.Spec[note]{Name: "ext/extra", Version: 1, Upgrades: map[int]event.Upgrade{1: idUpgrade}}, event.ErrInvalidDeclaration},
		{"queueable not detached", event.Spec[note]{Name: "ext/queue", Version: 1, Queueable: true}, event.ErrInvalidDeclaration},
		{"cold not omitempty", event.Spec[note]{Name: "ext/cold", Version: 1, Cold: "text"}, event.ErrInvalidDeclaration},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := event.Define(r, c.spec); !errors.Is(err, c.want) {
				t.Fatalf("Define(%s) = %v, want %v", c.spec.Name, err, c.want)
			}
		})
	}

	r.Seal()
	if _, err := event.Define(r, event.Spec[note]{Name: "ext/late", Version: 1}); !errors.Is(err, event.ErrSealed) {
		t.Fatalf("Define after Seal = %v, want ErrSealed", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("MustDefine after Seal did not panic")
		}
	}()
	event.MustDefine(r, event.Spec[note]{Name: "ext/later", Version: 1})
}

func idUpgrade(raw json.RawMessage) (json.RawMessage, error) { return raw, nil }

// DH-T-SESSION-1: every core type is declared once, with the surface flag on
// exactly the five surface types.
//
//dsh:test DH-T-SESSION-1
func TestCoreVocabulary(t *testing.T) {
	surface := map[string]bool{"system/message": true, "developer/message": true, "user/message": true,
		"assistant/message": true, "tool/result": true}
	types := event.Default().Types()
	if len(types) != 14 {
		t.Fatalf("Default declares %d types, want 14", len(types))
	}
	for _, info := range types {
		if info.Surface != surface[info.Name] {
			t.Errorf("%s: surface = %v", info.Name, info.Surface)
		}
		if info.Reserved != (info.Name == "session/end-seed") {
			t.Errorf("%s: reserved = %v", info.Name, info.Reserved)
		}
		if !strings.HasPrefix(info.Fingerprint(), "sha256:") {
			t.Errorf("%s: fingerprint %q", info.Name, info.Fingerprint())
		}
	}
}

// DH-T-SESSION-1: encode, decode and re-encode give the same bytes, and the cold
// part is split off and merged back.
//
//dsh:test DH-T-SESSION-1
func TestRoundTripStable(t *testing.T) {
	asOf := event.Seq(3)
	in := event.AssistantMessagePayload{
		Message: msg(t, vocab.RoleAssistant, "hi <there> & you"), RequestAsOf: &asOf, RequestDigest: "sha256:ab",
		Usage: &vocab.Usage{Input: 3, Output: 4}, Timing: &event.Timing{Start: 1, End: 2},
		Stream: json.RawMessage(`[{"delta":"hi"}]`),
	}
	d, err := event.AssistantMessage.Draft(event.Append(), in)
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if d.ColdRaw() != `[{"delta":"hi"}]` || strings.Contains(d.Raw(), "stream") {
		t.Fatalf("cold split: hot %s cold %s", d.Raw(), d.ColdRaw())
	}
	rec, err := d.Stamp(5, 100)
	if err != nil {
		t.Fatalf("Stamp: %v", err)
	}
	if err := event.Default().Validate(rec); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	out, err := event.AssistantMessage.Decode(rec)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if string(out.Stream) != string(in.Stream) || *out.RequestAsOf != 3 || out.Message.Blocks()[0].Text == nil {
		t.Fatalf("decoded %+v", out)
	}
	d2, err := event.AssistantMessage.Draft(event.Append(), out)
	if err != nil {
		t.Fatalf("re-Draft: %v", err)
	}
	if d2.Raw() != d.Raw() || d2.ColdRaw() != d.ColdRaw() {
		t.Fatalf("re-encode differs:\n%s\n%s", d.Raw(), d2.Raw())
	}
	canon, err := event.Canonical(d.Raw())
	if err != nil || canon != d.Raw() {
		t.Fatalf("Canonical(%s) = %s, %v", d.Raw(), canon, err)
	}

	// Without the cold part loaded, the hot fields still decode.
	rec.Cold = ""
	hot, err := event.AssistantMessage.Decode(rec)
	if err != nil || hot.Stream != nil || hot.Usage.Output != 4 {
		t.Fatalf("hot-only decode = %+v, %v", hot, err)
	}
}

// DH-T-SESSION-1: an older version converts to the current struct.
//
//dsh:test DH-T-SESSION-1
func TestOlderVersionConverts(t *testing.T) {
	m, err := vocab.NewMessage(vocab.RoleTool, vocab.ToolResultBlock("c1", false, vocab.TextPart("ok")))
	if err != nil {
		t.Fatal(err)
	}
	mb, _ := json.Marshal(m)
	rec := event.Record{Seq: 4, Type: "tool/result", Version: 1, Op: event.OpAppend,
		Payload: `{"id":"c1","message":` + string(mb) + `}`}
	got, err := event.ToolResult.Decode(rec)
	if err != nil {
		t.Fatalf("Decode v1: %v", err)
	}
	if got.CallID != "c1" || got.Spill != nil {
		t.Fatalf("v1 upcast = %+v", got)
	}
}

// DH-T-SESSION-1: a failure written before durations became text (turn/end v1,
// assistant/attempt v1 to v3, retry_after in integer nanoseconds) converts to the
// text form and decodes; one already in text, a payload without a failure and a
// failure without retry_after pass through; and the current version writes the
// text (plan 14.4.1).
//
//dsh:test DH-T-SESSION-1
func TestRetryAfterBecomesText(t *testing.T) {
	for _, v := range []int{1, 2, 3} {
		rec := event.Record{Seq: 2, Type: "assistant/attempt", Version: v,
			Payload: `{"failure":{"code":"llm/server","class":"transient","message":"busy","retry_after":2000000000}}`}
		got, err := event.AssistantAttempt.Decode(rec)
		if err != nil || got.Failure == nil || got.Failure.RetryAfter != 2*time.Second {
			t.Fatalf("assistant/attempt v%d = %+v, %v", v, got.Failure, err)
		}
	}
	old := event.Record{Seq: 3, Type: "turn/end", Version: 1,
		Payload: `{"turn":1,"reason":{"kind":"error","failure":{"code":"llm/server","class":"transient","message":"busy","retry_after":1500000000}}}`}
	end, err := event.TurnEndType.Decode(old)
	if err != nil || end.Reason.Failure == nil || end.Reason.Failure.RetryAfter != 1500*time.Millisecond {
		t.Fatalf("turn/end v1 = %+v, %v", end.Reason, err)
	}
	for _, payload := range []string{
		`{"turn":1,"reason":{"kind":"completed"}}`,
		`{"turn":1,"reason":{"kind":"error","failure":{"code":"llm/server","class":"transient","message":"busy"}}}`,
		`{"turn":1,"reason":{"kind":"error","failure":{"code":"llm/server","class":"transient","message":"busy","retry_after":"3s"}}}`,
		`{"turn":1,"reason":{"kind":"error","failure":null}}`,
	} {
		if _, err := event.TurnEndType.Decode(event.Record{Seq: 4, Type: "turn/end", Version: 1, Payload: payload}); err != nil {
			t.Errorf("turn/end v1 %s: %v", payload, err)
		}
	}
	bad := event.Record{Seq: 5, Type: "turn/end", Version: 1,
		Payload: `{"turn":1,"reason":{"kind":"error","failure":{"code":"llm/server","class":"transient","message":"busy","retry_after":1.5}}}`}
	if _, err := event.TurnEndType.Decode(bad); err == nil {
		t.Error("a fractional nanosecond retry_after decoded")
	}
	f := &vocab.Failure{Code: vocab.CodeServer, Class: vocab.Transient, Message: "busy", RetryAfter: 2 * time.Second}
	d, err := event.TurnEndType.Draft(event.TurnEnd{Turn: 1, Reason: event.Reason{Kind: event.ReasonError, Failure: f}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Raw(), `"retry_after":"2s"`) {
		t.Fatalf("turn/end v2 payload = %s", d.Raw())
	}
}

// DH-T-SESSION-2 / TC-05: tool/result version 3 against a registry that knows
// version 2 is a typed newer-payload error, and no value is fabricated.
//
//dsh:test DH-T-SESSION-2
func TestUnknownVersionSurfaces(t *testing.T) {
	rec := event.Record{Seq: 9, Type: "tool/result", Version: 3, Op: event.OpAppend,
		Payload: `{"call_id":"c1","message":{"role":"tool","blocks":[]},"new_field":1}`}
	got, err := event.ToolResult.Decode(rec)
	var newer *event.NewerPayloadError
	if !errors.As(err, &newer) || !errors.Is(err, event.ErrNewerPayload) {
		t.Fatalf("Decode v3 = %v, want *NewerPayloadError", err)
	}
	if newer.Type != "tool/result" || newer.Version != 3 || newer.Known != 2 || newer.Seq != 9 {
		t.Fatalf("error = %+v", newer)
	}
	if !got.Message.IsZero() || got.CallID != "" || got.IsError || got.Spill != nil {
		t.Fatalf("a value was fabricated: %+v", got)
	}
	if err := event.Default().Classify(rec); !errors.As(err, &newer) {
		t.Fatalf("Classify v3 = %v", err)
	}
	if v, err := event.Default().DecodeAny(rec); v != nil || !errors.Is(err, event.ErrNewerPayload) {
		t.Fatalf("DecodeAny v3 = %v, %v", v, err)
	}
	// Marked ignorable, the same record is kept as raw strings.
	rec.Ignorable = true
	if err := event.Default().Classify(rec); err != nil {
		t.Fatalf("Classify ignorable v3 = %v", err)
	}
}

// DH-T-SESSION-1: an undeclared required type is refused by name; an ignorable
// one is kept.
//
//dsh:test DH-T-SESSION-1
func TestUnknownType(t *testing.T) {
	r := event.Default()
	err := r.Classify(event.Record{Seq: 2, Type: "future/thing", Version: 1, Payload: `{}`})
	var unknown *event.UnknownTypeError
	if !errors.As(err, &unknown) || unknown.Type != "future/thing" || unknown.Seq != 2 || !errors.Is(err, event.ErrUnknownType) {
		t.Fatalf("Classify = %v", err)
	}
	if err := r.Classify(event.Record{Type: "future/thing", Version: 1, Ignorable: true, Payload: `{}`}); err != nil {
		t.Fatalf("Classify ignorable = %v", err)
	}
	if _, err := r.DecodeAny(event.Record{Type: "future/thing", Version: 1, Payload: `{}`}); !errors.Is(err, event.ErrUnknownType) {
		t.Fatalf("DecodeAny = %v", err)
	}
}

// DH-T-SESSION-1: plugin records follow the grammar and are always ignorable.
//
//dsh:test DH-T-SESSION-1
func TestPluginRecords(t *testing.T) {
	d, err := event.PluginDraft("acme.tools", "cursor", json.RawMessage(`{"b":1,"a":2}`), 1)
	if err != nil {
		t.Fatalf("PluginDraft: %v", err)
	}
	if d.Type() != "plugin:acme.tools/cursor" || d.Version() != 0 || !d.Ignorable() || d.Raw() != `{"a":2,"b":1}` {
		t.Fatalf("draft = %s v%d ign=%v %s", d.Type(), d.Version(), d.Ignorable(), d.Raw())
	}
	rec, err := d.Stamp(2, 0)
	if err != nil {
		t.Fatalf("Stamp: %v", err)
	}
	if err := event.Default().Validate(rec); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := event.Default().Classify(rec); err != nil {
		t.Fatalf("Classify: %v", err)
	}
	for _, bad := range [][2]string{{"", "x"}, {"Acme", "x"}, {"a/b", "x"}, {"a", strings.Repeat("x", 65)}} {
		if _, err := event.PluginDraft(bad[0], bad[1], nil); !errors.Is(err, event.ErrInvalidEvent) {
			t.Errorf("PluginDraft(%q, %q) = %v", bad[0], bad[1], err)
		}
	}
	if _, err := event.PluginDraft("a", "b", json.RawMessage(`{`)); !errors.Is(err, event.ErrInvalidEvent) {
		t.Errorf("PluginDraft(bad json) = %v", err)
	}
	if _, _, ok := event.ParsePluginType("plugin:a"); ok {
		t.Error("ParsePluginType accepted a name with no record")
	}
}

// DH-T-SESSION-2: invalid envelopes and drafts are refused with typed errors.
//
//dsh:test DH-T-SESSION-2
func TestInvalidEnvelopes(t *testing.T) {
	r := event.Default()
	user, _ := json.Marshal(event.UserMessagePayload{Message: msg(t, vocab.RoleUser, "q")})
	call := `{"arguments":"{}","call_id":"c","name":"n"}`
	cases := []struct {
		name string
		rec  event.Record
		want error
	}{
		{"surface without op", event.Record{Seq: 3, Type: "user/message", Version: 1, Payload: string(user)}, event.ErrInvalidSurface},
		{"log-only with op", event.Record{Seq: 3, Type: "tool/call", Version: 1, Op: event.OpAppend, Payload: call}, event.ErrInvalidSurface},
		{"replace forward", event.Record{Seq: 3, Type: "user/message", Version: 1, Op: event.OpReplace, Start: 1, End: 3, Payload: string(user)}, event.ErrInvalidSurface},
		{"replace reversed", event.Record{Seq: 3, Type: "user/message", Version: 1, Op: event.OpReplace, Start: 2, End: 1, Payload: string(user)}, event.ErrInvalidSurface},
		{"append with range", event.Record{Seq: 3, Type: "user/message", Version: 1, Op: event.OpAppend, End: 1, Payload: string(user)}, event.ErrInvalidSurface},
		{"later source", event.Record{Seq: 3, Type: "tool/call", Version: 1, Sources: []event.Seq{3}, Payload: call}, event.ErrInvalidEvent},
		{"duplicate source", event.Record{Seq: 3, Type: "tool/call", Version: 1, Sources: []event.Seq{1, 1}, Payload: call}, event.ErrInvalidEvent},
		{"sources on assistant", event.Record{Seq: 3, Type: "assistant/message", Version: 1, Op: event.OpAppend, Sources: []event.Seq{1}, Payload: `{}`}, event.ErrInvalidEvent},
		{"payload not json", event.Record{Seq: 3, Type: "tool/call", Version: 1, Payload: `{`}, event.ErrInvalidEvent},
		{"plugin with version", event.Record{Seq: 3, Type: "plugin:a/b", Version: 1, Ignorable: true, Payload: `{}`}, event.ErrInvalidEvent},
		{"plugin not ignorable", event.Record{Seq: 3, Type: "plugin:a/b", Payload: `{}`}, event.ErrInvalidEvent},
		{"plugin outside grammar", event.Record{Seq: 3, Type: "plugin:A", Ignorable: true, Payload: `{}`}, event.ErrInvalidEvent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := r.Validate(c.rec); !errors.Is(err, c.want) {
				t.Fatalf("Validate = %v, want %v", err, c.want)
			}
		})
	}

	if _, err := event.UserMessage.Draft(event.Op{}, event.UserMessagePayload{Message: msg(t, vocab.RoleUser, "q")}); !errors.Is(err, event.ErrInvalidSurface) {
		t.Errorf("surface Draft without op = %v", err)
	}
	if _, err := event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: msg(t, vocab.RoleAssistant, "q")}); !errors.Is(err, event.ErrInvalidEvent) {
		t.Errorf("Draft with wrong role = %v", err)
	}
	if _, err := event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: msg(t, vocab.RoleAssistant, "a")}, 1); !errors.Is(err, event.ErrInvalidEvent) {
		t.Errorf("assistant Draft with sources = %v", err)
	}
	d, err := event.UserMessage.Draft(event.Replace(2, 4), event.UserMessagePayload{Message: msg(t, vocab.RoleUser, "q")})
	if err != nil {
		t.Fatalf("Draft replace: %v", err)
	}
	if _, err := d.Stamp(4, 0); !errors.Is(err, event.ErrInvalidSurface) {
		t.Errorf("Stamp replace over itself = %v", err)
	}
	if _, err := (event.Draft{}).Stamp(0, 0); !errors.Is(err, event.ErrInvalidEvent) {
		t.Errorf("zero Draft Stamp = %v", err)
	}
	if _, err := event.ToolCall.Decode(event.Record{Type: "tool/result", Version: 1, Payload: call}); !errors.Is(err, event.ErrInvalidEvent) {
		t.Errorf("Decode wrong type = %v", err)
	}
}

// session/end-seed is reserved to the core: a draft of it from outside fails the
// admission check.
func TestReservedDraft(t *testing.T) {
	d, err := event.EndSeed.Draft(event.EndSeedPayload{Epoch: 1})
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if err := d.Check(false); !errors.Is(err, event.ErrReservedType) {
		t.Fatalf("Check(false) = %v", err)
	}
	if err := d.Check(true); err != nil {
		t.Fatalf("Check(true) = %v", err)
	}
	td, _ := event.TurnStartType.Draft(event.TurnStart{Turn: 1})
	if err := td.Check(false); err != nil {
		t.Fatalf("turn/start Check = %v", err)
	}
}

// The core lends only the title names of its namespace: a lent type is declared
// outside the core and its drafts pass the admission check; any other session/
// name is still refused.
func TestLentNames(t *testing.T) {
	r := event.NewRegistry()
	if _, err := event.DefineLent(r, event.Spec[note]{Name: "session/mine", Version: 1}); !errors.Is(err, event.ErrReservedType) {
		t.Fatalf("DefineLent(session/mine) = %v", err)
	}
	if _, err := event.DefineLent(r, event.Spec[note]{Name: "session/title", Version: 0}); !errors.Is(err, event.ErrInvalidDeclaration) {
		t.Fatalf("a lent name still gets the declaration checks: %v", err)
	}
	typ, err := event.DefineLent(r, event.Spec[note]{Name: "session/title", Version: 1})
	if err != nil {
		t.Fatalf("DefineLent(session/title) = %v", err)
	}
	d, err := typ.Draft(note{Text: "t"})
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if err := d.Check(false); err != nil || d.Reserved() {
		t.Fatalf("a lent draft: Check(false) = %v, reserved %v", err, d.Reserved())
	}
}

// An unknown turn/end reason kind written by a newer build is preserved.
func TestUnknownReasonPreserved(t *testing.T) {
	rec := event.Record{Seq: 1, Type: "turn/end", Version: 1, Payload: `{"reason":{"kind":"hibernated"},"turn":2}`}
	got, err := event.TurnEndType.Decode(rec)
	if err != nil || got.Reason.Kind != "hibernated" {
		t.Fatalf("Decode = %+v, %v", got, err)
	}
	d, _ := event.TurnEndType.Draft(got)
	if d.Raw() != rec.Payload {
		t.Fatalf("re-encode %s, want %s", d.Raw(), rec.Payload)
	}
}

// Event's accessors return copies.
func TestEventCopies(t *testing.T) {
	rec := event.Record{Seq: 3, Type: "tool/call", Version: 1, Sources: []event.Seq{1, 2}, Payload: `{}`}
	e := event.Of(rec)
	rec.Sources[0] = 9
	s := e.Sources()
	s[1] = 9
	if got := e.Sources(); got[0] != 1 || got[1] != 2 {
		t.Fatalf("Sources = %v", got)
	}
}

// A payload that is not a JSON object is refused with a typed error, even when a
// cold part would be merged into it; no value is fabricated.
func TestNonObjectPayloadRefused(t *testing.T) {
	for _, tc := range []struct {
		name          string
		payload, cold string
	}{
		{"null with cold", "null", "[]"},
		{"null", "null", ""},
		{"array with cold", "[1]", "[]"},
		{"string", `"x"`, ""},
		{"number", "1", ""},
		{"object with bad cold", `{"message":{}}`, "[{"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := event.Record{Seq: 3, Type: "assistant/message", Version: 1, Op: event.OpAppend, Payload: tc.payload, Cold: tc.cold}
			got, err := event.AssistantMessage.Decode(rec)
			if !errors.Is(err, event.ErrInvalidEvent) {
				t.Fatalf("Decode = %v, want ErrInvalidEvent", err)
			}
			if got.Stream != nil || got.RequestDigest != "" || got.Usage != nil {
				t.Fatalf("a value was fabricated: %+v", got)
			}
			if v, err := event.Default().DecodeAny(rec); v != nil || !errors.Is(err, event.ErrInvalidEvent) {
				t.Fatalf("DecodeAny = %v, %v", v, err)
			}
		})
	}
}

// The registry snapshots a declaration's upgrade chain: changing the caller's map
// after Define, even concurrently, cannot change a sealed registry's codec.
func TestUpgradesSnapshotted(t *testing.T) {
	r := event.NewRegistry()
	ups := map[int]event.Upgrade{1: func(raw json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(strings.Replace(string(raw), `"old"`, `"text"`, 1)), nil
	}}
	typ, err := event.Define(r, event.Spec[note]{Name: "ext/renamed", Version: 2, Upgrades: ups})
	if err != nil {
		t.Fatal(err)
	}
	r.Seal()
	done := make(chan struct{})
	t.Cleanup(func() { <-done }) // P-C: the writer has no gate; the cleanup joins it on every exit path
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			delete(ups, 1)
			ups[1] = nil
		}
	}()
	for i := 0; i < 1000; i++ {
		got, err := typ.Decode(event.Record{Type: "ext/renamed", Version: 1, Payload: `{"old":"hi"}`})
		if err != nil || got.Text != "hi" {
			t.Fatalf("decode after the caller changed its map: %+v, %v", got, err)
		}
	}
	<-done
}
