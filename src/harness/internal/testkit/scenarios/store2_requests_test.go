//go:build scenario

package scenarios

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// store2Requests splits DSH_SCENARIO_STORE2_REQUEST's output into the request
// bodies, one a non-empty line, in the order sent.
func store2Requests(out string) []string {
	var reqs []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			reqs = append(reqs, l)
		}
	}
	return reqs
}

// store2FirstRequest judges the first request sent after the first before ones
// (DH-E-STORE-2, TC-19s2): not ready while none is sent; otherwise it must carry
// prompt and the interrupted call's outcome-unknown result. A later request
// never stands in for it.
func store2FirstRequest(reqs []string, before int, prompt string) (string, bool, error) {
	if len(reqs) <= before {
		return "", false, nil
	}
	first := reqs[before]
	switch {
	case !strings.Contains(first, prompt):
		return first, true, fmt.Errorf("model request %d, the first after the prompt, does not carry it", before+1)
	case !strings.Contains(first, "tools/outcome-unknown") && !strings.Contains(strings.ToLower(first), "outcome unknown"):
		return first, true, errors.New("the first model request after the resume lacks the interrupted call's result")
	}
	return first, true, nil
}

// TestStore2FirstRequestIsTheOneJudged: only the first request after the
// boundary counts; a later request carrying the result does not excuse a first
// one without it, and requests before the boundary are not judged.
func TestStore2FirstRequestIsTheOneJudged(t *testing.T) {
	const prompt = "What happened?"
	good := `{"messages":["` + prompt + `","tools/outcome-unknown"]}`
	bad := `{"messages":["` + prompt + `"]}`
	old := `{"messages":["first prompt","tools/outcome-unknown"]}`
	for _, tc := range []struct {
		name         string
		out          string
		before       int
		ready, valid bool
	}{
		{"none sent yet", old + "\n", 1, false, false},
		{"first good", old + "\n" + good + "\n", 1, true, true},
		{"first bad, later good", old + "\n" + bad + "\n" + good + "\n", 1, true, false},
		{"result only before the boundary", good + "\n" + bad + "\n", 1, true, false},
		{"first is not the prompt's", old + "\n" + old + "\n" + good + "\n", 1, true, false},
		{"blank lines and CRLF", "\r\n" + old + "\r\n\r\n" + good + "\r\n", 1, true, true},
	} {
		_, ready, err := store2FirstRequest(store2Requests(tc.out), tc.before, prompt)
		if ready != tc.ready || (err == nil) != (tc.valid || !tc.ready) {
			t.Errorf("%s: ready %v, err %v; want ready %v, valid %v", tc.name, ready, err, tc.ready, tc.valid)
		}
	}
}
