package driver_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
)

// A budget's time limit is a duration's text in a config ("30m"); a number is
// refused, and an absent limit stays absent (plan 14.4.1).
//
//dsh:test DH-T-UTIL-2
func TestBudgetDurationIsText(t *testing.T) {
	b := driver.Budget{MaxSteps: 3, MaxDuration: 30 * time.Minute}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"maxSteps":3,"maxDuration":"30m0s"}`; string(raw) != want {
		t.Fatalf("Marshal = %s, want %s", raw, want)
	}
	var back driver.Budget
	if err := json.Unmarshal([]byte(`{"maxSteps":3,"maxDuration":"30m"}`), &back); err != nil || back != b {
		t.Fatalf("Unmarshal = %+v, %v", back, err)
	}
	if err := json.Unmarshal([]byte(`{"maxSteps":3,"maxDuration":1800000000000}`), &back); err == nil || !strings.Contains(err.Error(), "/maxDuration") {
		t.Fatalf("a number limit: %v", err)
	}
	none, _ := json.Marshal(driver.DefaultBudget())
	if strings.Contains(string(none), "maxDuration") {
		t.Fatalf("no time limit was written as one: %s", none)
	}
}
