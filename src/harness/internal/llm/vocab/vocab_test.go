package vocab

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/archtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/schema"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

//go:embed *.go
var sources embed.FS

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func ptr[T any](v T) *T { return &v }

// sample builds one message with every block kind; data's keys are inserted
// in the given order.
func sample(t *testing.T, order []string) (Message, Message) {
	t.Helper()
	data := map[string]any{}
	var raw strings.Builder
	raw.WriteString("{")
	for i, k := range order {
		data[k] = k + "!"
		if i > 0 {
			raw.WriteString(",")
		}
		fmt.Fprintf(&raw, "%q: {%q: 1, \"z\": [true, null, 1.50]}", k, k+"2")
	}
	raw.WriteString("}")
	src := Source{Kind: "compaction", Producers: []string{"a14"}, Data: json.RawMessage(raw.String())}
	asst, err := RestoreMessage("msg_1", RoleAssistant, &src,
		TextBlock("<b>& é \u2028"),
		ReasoningBlock("thinking", "sig", false),
		ToolUseBlock("call_1", "read", json.RawMessage(raw.String())),
	)
	if err != nil {
		t.Fatal(err)
	}
	payload := must(json.Marshal(data))
	tool, err := RestoreMessage("msg_2", RoleTool, &Source{Kind: "tool", Producers: []string{"x"}, Data: payload},
		ToolResultBlock("call_1", false, TextPart("ok"), ImagePart("image/png", "att_1")))
	if err != nil {
		t.Fatal(err)
	}
	return asst, tool
}

// TestCanonicalStable is the stage scenario "Canonical encoding is stable"
// (DH-T-LLM-1, TC-04a).
func TestCanonicalStable(t *testing.T) {
	a1, t1 := sample(t, []string{"alpha", "beta", "gamma", "delta"})
	a2, t2 := sample(t, []string{"delta", "gamma", "alpha", "beta"})
	for _, pair := range [][2]Message{{a1, a2}, {t1, t2}} {
		b1, err1 := Encode(pair[0])
		b2, err2 := Encode(pair[1])
		if err1 != nil || err2 != nil {
			t.Fatal(err1, err2)
		}
		if !bytes.Equal(b1, b2) {
			t.Fatalf("encodings differ:\n%s\n%s", b1, b2)
		}
		d1, d2 := must(Digest(pair[0])), must(Digest(pair[1]))
		if d1 != d2 || !strings.HasPrefix(d1, "sha256:") || len(d1) != len("sha256:")+64 {
			t.Fatalf("digests %s and %s", d1, d2)
		}
	}
	b := must(Encode(a1))
	for _, want := range []string{`"<b>& é`, `"z":[true,null,1.50]`, `{"alpha":{"alpha2":1,`} {
		if !bytes.Contains(b, []byte(want)) {
			t.Errorf("canonical bytes lack %s:\n%s", want, b)
		}
	}
	if bytes.Contains(b, []byte("\n")) || bytes.Contains(b, []byte(`": `)) {
		t.Errorf("canonical bytes hold white space:\n%s", b)
	}
	if must(Digest(a1)) == must(Digest(t1)) {
		t.Fatal("different messages share a digest")
	}
}

// values is one valid value of every persisted vocabulary type.
func values(t *testing.T) []any {
	asst, tool := sample(t, []string{"k", "j"})
	user := must(NewMessage(RoleUser, TextBlock("hi"), ImageBlock("image/jpeg", "att_9")))
	return []any{
		&asst, &tool, &user,
		ptr(TextBlock("t")),
		ptr(Source{Kind: "k", Producers: []string{"p"}}),
		&ToolSchema{Name: "read", Description: "Read a file", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
		&Controls{Effort: "high", Temperature: ptr(0.0), TopP: ptr(0.9), MaxOutputTokens: 4096, Stop: []string{"END"}, ToolChoice: ToolChoice{Mode: ToolChoiceTool, Name: "read"}},
		&Controls{},
		&CallConfig{Controls: Controls{Effort: "low"}, Defaulted: []string{"temperature"}},
		&Meta{Session: "s1", Purpose: "title"},
		&Usage{Input: 1, CacheRead: 2, CacheWrite: 3, Output: 4, Total: 10},
		&Failure{Code: CodeRateLimit, Class: Transient, Message: "slow down", Status: 429, RetryAfter: 2 * time.Second, RequestID: "r"},
	}
}

// TestReencode is the DH-T-LLM-1 clause "every type re-encodes to the same
// canonical bytes": encode, decode strictly into a fresh value, encode again.
func TestReencode(t *testing.T) {
	for _, v := range values(t) {
		first, err := Encode(v)
		if err != nil {
			t.Fatalf("%T: %v", v, err)
		}
		fresh := reflect.New(reflect.TypeOf(v).Elem()).Interface()
		if err := Decode(first, fresh); err != nil {
			t.Fatalf("%T: decode %s: %v", v, first, err)
		}
		second := must(Encode(fresh))
		if !bytes.Equal(first, second) {
			t.Fatalf("%T re-encodes differently:\n%s\n%s", v, first, second)
		}
		if must(Canonical(first)) == nil || !bytes.Equal(must(Canonical(first)), first) {
			t.Fatalf("%T: canonical form is not a fixed point", v)
		}
	}
}

// persisted lists every vocabulary type that is written to a log or table,
// under the name its golden line carries; Message is fingerprinted through its
// persisted shape.
var persisted = []struct {
	name string
	typ  reflect.Type
}{
	{"vocab.Message", reflect.TypeFor[messageJSON]()},
	{"vocab.Block", reflect.TypeFor[Block]()},
	{"vocab.Source", reflect.TypeFor[Source]()},
	{"vocab.ToolSchema", reflect.TypeFor[ToolSchema]()},
	{"vocab.Controls", reflect.TypeFor[Controls]()},
	{"vocab.CallConfig", reflect.TypeFor[CallConfig]()},
	{"vocab.Meta", reflect.TypeFor[Meta]()},
	{"vocab.Usage", reflect.TypeFor[Usage]()},
	{"vocab.Failure", reflect.TypeFor[Failure]()},
}

// TestFingerprints is the stage scenario "Vocabulary fingerprints unchanged"
// (TC-04a): a changed persisted shape changes its line, which DH-D-STORE-5 then
// refuses to load silently. Record with `go test ./internal/llm/vocab -run
// TestFingerprints -update`.
func TestFingerprints(t *testing.T) {
	var lines strings.Builder
	for _, p := range persisted {
		ty, err := schema.ForType(p.typ)
		if err != nil {
			t.Fatalf("%s: %v", p.name, err)
		}
		fmt.Fprintf(&lines, "%s %s\n", p.name, ty.Fingerprint())
	}
	golden.Assert(t, "fingerprints", lines.String())
}

// TestSchemasAcceptValues checks each persisted schema accepts the canonical
// encoding of a real value, so a fingerprint describes what is written.
func TestSchemasAcceptValues(t *testing.T) {
	byType := map[reflect.Type]*schema.Type{}
	for _, p := range persisted {
		byType[p.typ] = must(schema.ForType(p.typ))
	}
	for _, v := range values(t) {
		typ := reflect.TypeOf(v).Elem()
		if typ == reflect.TypeFor[Message]() {
			typ = reflect.TypeFor[messageJSON]()
		}
		s, ok := byType[typ]
		if !ok {
			t.Fatalf("%v is persisted but has no fingerprint line", typ)
		}
		if issues := s.Validate(json.RawMessage(must(Encode(v)))); len(issues) > 0 {
			t.Errorf("%v: %v", typ, issues)
		}
	}
}

// TestNoRequestType is the stage scenario "vocab holds no request type" (TC-05
// rule, RM-6): the archtest predicate over this package's own sources, embedded.
func TestNoRequestType(t *testing.T) {
	found, err := archtest.RequestTypes(sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Fatalf("internal/llm/vocab declares a request type: %v", found)
	}
}

func TestMessageConstructors(t *testing.T) {
	args := json.RawMessage(`{"a":1}`)
	blocks := []Block{ToolUseBlock("c", "n", args)}
	m := must(NewMessage(RoleAssistant, blocks...))
	other := must(NewMessage(RoleAssistant, blocks...))
	if !strings.HasPrefix(m.ID(), "msg_") || m.ID() == other.ID() {
		t.Fatalf("ids %q and %q", m.ID(), other.ID())
	}
	blocks[0].ToolUse.Args[2] = 'X'
	got := m.Blocks()
	got[0].ToolUse.Name = "changed"
	if b := m.Blocks()[0].ToolUse; string(b.Args) != `{"a":1}` || b.Name != "n" {
		t.Fatalf("a message shares memory with its caller: %+v", b)
	}
	src := Source{Kind: "k", Producers: []string{"p", "q"}}
	with := must(m.WithSource(src))
	src.Producers[0] = "changed"
	if s, _ := with.Source(); s.Producers[0] != "p" || with.ID() != m.ID() {
		t.Fatalf("WithSource = %+v", s)
	}
	if _, ok := m.Source(); ok {
		t.Fatal("WithSource changed the original")
	}
	if _, err := json.Marshal(Message{}); err == nil {
		t.Fatal("the zero message encoded")
	}

	bad := []struct {
		name string
		err  func() error
	}{
		{"unknown role", func() error { _, err := NewMessage("robot", TextBlock("x")); return err }},
		{"tool result from user", func() error { _, err := NewMessage(RoleUser, ToolResultBlock("c", false)); return err }},
		{"text from tool", func() error { _, err := NewMessage(RoleTool, TextBlock("x")); return err }},
		{"tool call from user", func() error { _, err := NewMessage(RoleUser, ToolUseBlock("c", "n", args)); return err }},
		{"args not an object", func() error {
			_, err := NewMessage(RoleAssistant, ToolUseBlock("c", "n", json.RawMessage(`[1]`)))
			return err
		}},
		{"two variants", func() error {
			b := TextBlock("x")
			b.Image = &Image{MediaType: "image/png", Ref: "r"}
			_, err := NewMessage(RoleUser, b)
			return err
		}},
		{"unknown kind", func() error { _, err := NewMessage(RoleUser, Block{Kind: "audio", Text: &Text{}}); return err }},
		{"svg image", func() error { _, err := NewMessage(RoleUser, ImageBlock("image/svg+xml", "r")); return err }},
		{"redacted without signature", func() error {
			_, err := NewMessage(RoleAssistant, ReasoningBlock("", "", true))
			return err
		}},
		{"blank id", func() error { _, err := RestoreMessage("", RoleUser, nil); return err }},
		{"source without producer", func() error {
			_, err := RestoreMessage("m", RoleUser, &Source{Kind: "k"})
			return err
		}},
		{"unknown field", func() error {
			var m Message
			return Decode([]byte(`{"id":"m","role":"user","blocks":[],"extra":1}`), &m)
		}},
		{"missing blocks", func() error { var m Message; return Decode([]byte(`{"id":"m","role":"user"}`), &m) }},
		{"trailing data", func() error { var u Usage; return Decode([]byte(`{"input":1} {}`), &u) }},
	}
	for _, tc := range bad {
		if tc.err() == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
}

func TestValues(t *testing.T) {
	bad := []interface{ Validate() error }{
		Controls{Temperature: ptr(2.5)},
		Controls{TopP: ptr(-0.1)},
		Controls{MaxOutputTokens: -1},
		Controls{Stop: []string{""}},
		Controls{ToolChoice: ToolChoice{Mode: ToolChoiceTool}},
		Controls{ToolChoice: ToolChoice{Mode: ToolChoiceAuto, Name: "x"}},
		Controls{ToolChoice: ToolChoice{Mode: "any"}},
		CallConfig{Defaulted: []string{"seed"}},
		Meta{Session: "s"},
		Usage{Output: -1},
		ToolSchema{Name: "a b", Parameters: json.RawMessage(`{"type":"object"}`)},
		ToolSchema{Name: "a", Parameters: json.RawMessage(`{"type":"string"}`)},
		ToolSchema{Name: "a", Parameters: json.RawMessage(`not json`)},
		Failure{Code: "llm/nope", Class: Permanent},
		Failure{Code: CodeAuth, Class: Transient},
		RouteID(" deepseek"),
	}
	for _, v := range bad {
		if v.Validate() == nil {
			t.Errorf("%#v accepted", v)
		}
	}
	for _, v := range values(t) {
		if err := v.(interface{ Validate() error }).Validate(); err != nil {
			t.Errorf("%T: %v", v, err)
		}
	}
	c := Controls{Temperature: ptr(1.0), Stop: []string{"a"}}
	d := c.Clone()
	*d.Temperature, d.Stop[0] = 2, "b"
	if *c.Temperature != 1 || c.Stop[0] != "a" {
		t.Fatal("Controls.Clone shares memory")
	}
}

// TestFailureClasses pins the class table of docs/harness/areas/llm.md 6.1.
func TestFailureClasses(t *testing.T) {
	want := map[Class][]Code{
		Cancelled:  {CodeAborted},
		Transient:  {CodeRateLimit, CodeServer, CodeTimeout, CodeTransport, CodeStreamClosed, CodeMalformedResponse, CodeEmptyResponse},
		Repairable: {CodeContextWindowExceeded, CodeImageOffloadRequired, CodeAttachmentUnreadable},
	}
	count := 0
	for cl, codes := range want {
		for _, c := range codes {
			if got, ok := ClassOf(c); !ok || got != cl {
				t.Errorf("ClassOf(%s) = %s, want %s", c, got, cl)
			}
			count++
		}
	}
	for _, c := range Codes() {
		if !strings.HasPrefix(string(c), "llm/") {
			t.Errorf("code %s lacks the llm/ prefix", c)
		}
		if cl, _ := ClassOf(c); cl == Permanent {
			count++
		}
	}
	if count != len(Codes()) || len(Codes()) != 29 {
		t.Errorf("the table has %d codes, %d classified", len(Codes()), count)
	}
	for _, tc := range []struct {
		code Code
		cl   Class
		ok   bool
	}{
		{HTTPCode(404), Permanent, true},
		{HTTPCode(418), Permanent, true},
		{HTTPCode(503), Transient, true},
		{HTTPCode(302), "", false},
		{"llm/http-4xx", "", false},
		{"llm/http-0404", "", false},
		{"llm/http-", "", false},
	} {
		if cl, ok := ClassOf(tc.code); cl != tc.cl || ok != tc.ok {
			t.Errorf("ClassOf(%s) = %s, %v", tc.code, cl, ok)
		}
	}
	f := must(NewFailure(CodeContextWindowExceeded, "too long"))
	var err error = f
	var target *Failure
	if !errors.As(err, &target) || target.Class != Repairable || err.Error() != "llm/context-window-exceeded: too long" {
		t.Fatalf("failure = %v", err)
	}
	if _, err := NewFailure("llm/unknown", ""); err == nil {
		t.Fatal("an unknown code built a failure")
	}
}

// TestChunksSealed lists the seven chunk types; a new one must be added here
// and to every consumer switch (DH-T-LLM-1).
func TestChunksSealed(t *testing.T) {
	chunks := []Chunk{BlockStart{}, TextDelta{}, ReasoningDelta{}, ToolCallDelta{}, BlockEnd{}, UsageChunk{}, Finish{Reason: FinishStop}}
	seen := map[reflect.Type]bool{}
	for _, c := range chunks {
		seen[reflect.TypeOf(c)] = true
	}
	if len(seen) != 7 {
		t.Fatalf("%d chunk types", len(seen))
	}
}

func FuzzCanonical(f *testing.F) {
	for _, s := range []string{`{"b":1,"a":[1,2,{"d":null,"c":"<&>"}]}`, `"x"`, `1e400`, `{"a":1,"a":2}`, `[] `} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		c, err := Canonical([]byte(s))
		if err != nil {
			return
		}
		again, err := Canonical(c)
		if err != nil || !bytes.Equal(again, c) {
			t.Fatalf("Canonical is not idempotent on %q: %q then %q (%v)", s, c, again, err)
		}
		if !json.Valid(c) {
			t.Fatalf("Canonical(%q) = %q is not JSON", s, c)
		}
	})
}
