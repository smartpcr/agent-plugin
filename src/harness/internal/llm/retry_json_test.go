package llm_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
)

// A retry policy's delays, bound and cap are durations' text on the wire; a
// number is refused (plan 14.4.1).
//
//dsh:test DH-T-UTIL-2
func TestRetryPolicyDurationsAreText(t *testing.T) {
	p := llm.DefaultRetry()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"base_delay":"500ms"`, `"max_delay":"10s"`, `"max_elapsed":"30m0s"`, `"retry_after_cap":"1m0s"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("Marshal = %s, lacks %s", b, want)
		}
	}
	var back llm.RetryPolicy
	if err := json.Unmarshal(b, &back); err != nil || back != p {
		t.Fatalf("Unmarshal = %+v, %v", back, err)
	}
	if err := json.Unmarshal([]byte(`{"base_delay":500000000}`), &back); err == nil || !strings.Contains(err.Error(), "/base_delay") {
		t.Fatalf("a number delay: %v", err)
	}
}
