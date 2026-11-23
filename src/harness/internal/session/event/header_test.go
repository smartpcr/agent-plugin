package event_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// DH-T-SESSION-1, plan 17.1.3: request/header version 2 round-trips the prompt
// assembly's fingerprint and manifest; a version-1 header decodes through its
// upgrade with neither; a header without them writes neither field.
//
//dsh:test DH-T-SESSION-1
func TestRequestHeaderV2RoundTrip(t *testing.T) {
	in := event.RequestHeaderPayload{
		Route: "r/a", Tools: []vocab.ToolSchema{{Name: "echo", Description: "Echoes.", Parameters: json.RawMessage(`{"type":"object"}`)}},
		Fingerprint: "sha256:ab",
		Manifest: []event.ManifestEntry{
			{Name: "identity", Order: 0, Owner: "tools.prompt"},
			{Name: "persona", Order: 100, Owner: "operator", Complete: true},
		},
	}
	d, err := event.RequestHeader.Draft(in)
	if err != nil {
		t.Fatal(err)
	}
	if d.Version() != 2 || event.RequestHeader.Version() != 2 {
		t.Fatalf("version %d, want 2", d.Version())
	}
	rec, err := d.Stamp(3, 100)
	if err != nil {
		t.Fatal(err)
	}
	out, err := event.RequestHeader.Decode(rec)
	if err != nil {
		t.Fatal(err)
	}
	if out.Fingerprint != in.Fingerprint || !slices.Equal(out.Manifest, in.Manifest) || out.Route != in.Route || len(out.Tools) != 1 {
		t.Fatalf("decoded %+v", out)
	}
	again, err := event.RequestHeader.Draft(out)
	if err != nil || again.Raw() != d.Raw() {
		t.Fatalf("re-encode differs:\n%s\n%s (%v)", d.Raw(), again.Raw(), err)
	}

	v1 := event.Record{Seq: 1, Type: "request/header", Version: 1, Payload: `{"route":"r/a","tools":[{"description":"Echoes.","name":"echo","parameters":{"type":"object"}}]}`}
	old, err := event.RequestHeader.Decode(v1)
	if err != nil {
		t.Fatalf("v1: %v", err)
	}
	if old.Fingerprint != "" || old.Manifest != nil || old.Route != "r/a" || len(old.Tools) != 1 {
		t.Fatalf("v1 decoded %+v", old)
	}
	bare, err := event.RequestHeader.Draft(event.RequestHeaderPayload{Route: "r/a"})
	if err != nil || bare.Raw() != `{"route":"r/a"}` {
		t.Fatalf("a header without an assembly writes %s (%v)", bare.Raw(), err)
	}
}
