package vocab

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/schema"
)

// A failure's retry_after is a duration's text on the wire, through
// encoding/json and through the derived shape alike; a number is refused
// (plan 14.4.1).
//
//dsh:test DH-T-UTIL-2
func TestFailureRetryAfterIsText(t *testing.T) {
	f := Failure{Code: CodeServer, Class: Transient, Message: "busy", Status: 529, RetryAfter: 2 * time.Second}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"code":"llm/server","class":"transient","message":"busy","status":529,"retry_after":"2s"}`; string(b) != want {
		t.Fatalf("Marshal = %s, want %s", b, want)
	}
	var back Failure
	if err := json.Unmarshal(b, &back); err != nil || back != f {
		t.Fatalf("Unmarshal = %+v, %v", back, err)
	}
	typ := schema.MustOf[Failure]()
	if is := typ.Validate(f); is != nil {
		t.Fatalf("Validate = %v", is)
	}
	var dec Failure
	if is := typ.Decode(b, &dec); is != nil || dec != f {
		t.Fatalf("Decode = %+v, %v", dec, is)
	}
	if err := json.Unmarshal([]byte(`{"code":"llm/server","class":"transient","message":"m","retry_after":2000000000}`), &back); err == nil || !strings.Contains(err.Error(), "/retry_after") {
		t.Fatalf("a number retry_after: %v", err)
	}
	none, _ := json.Marshal(Failure{Code: CodeServer, Class: Transient})
	if strings.Contains(string(none), "retry_after") {
		t.Fatalf("a zero retry_after was written: %s", none)
	}
}
