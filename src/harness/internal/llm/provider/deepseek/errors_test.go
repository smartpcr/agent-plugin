package deepseek

import (
	"net/http"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

// The vendor table maps known types, codes and documented statuses; a business
// code nobody has listed (protocol.ts:245) stays unknown: the generic class of
// its status, with the raw code and body kept, never a guessed specific class.
func TestClassify(t *testing.T) {
	mask := func(s string) string { return strings.ReplaceAll(s, "sk-secret", "[redacted]") }
	c := Classify(mask)
	for _, tc := range []struct {
		name   string
		status int
		body   string
		code   vocab.Code
		class  vocab.Class
		keep   string
	}{
		{"messages auth", 401, `{"type":"error","error":{"type":"authentication_error","message":"bad key sk-secret"}}`, vocab.CodeAuth, vocab.Permanent, "[redacted]"},
		{"overloaded", 529, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, vocab.CodeServer, vocab.Transient, "Overloaded"},
		{"balance", 402, `{"error":{"message":"Insufficient Balance","type":"unknown_error","code":"insufficient_balance"}}`, vocab.CodeAccountQuota, vocab.Permanent, "Insufficient"},
		{"documented status", 402, `{"error":{"message":"Insufficient Balance","type":"x"}}`, vocab.CodeAccountQuota, vocab.Permanent, "Insufficient"},
		{"unknown code 400", 400, `{"error":{"message":"odd","type":"x","code":40017}}`, vocab.HTTPCode(400), vocab.Permanent, `"40017"`},
		{"unknown code 503", 503, `{"error":{"message":"odd","code":"biz_77"}}`, vocab.CodeServer, vocab.Transient, `"biz_77"`},
		{"unknown messages type", 418, `{"type":"error","error":{"type":"teapot_error","message":"tea"}}`, vocab.HTTPCode(418), vocab.Permanent, `"teapot_error"`},
		{"in-stream unknown", 0, `{"type":"error","error":{"type":"mystery","message":"m"}}`, vocab.CodeServer, vocab.Transient, `"mystery"`},
		{"not json", 500, `<html>oops</html>`, vocab.CodeServer, vocab.Transient, "500"},
	} {
		f := c(tc.status, http.Header{}, []byte(tc.body))
		if f.Code != tc.code || f.Class != tc.class || !strings.Contains(f.Message, tc.keep) {
			t.Errorf("%s: %+v, want %s/%s keeping %q", tc.name, f, tc.code, tc.class, tc.keep)
		}
		if strings.Contains(f.Message, "sk-secret") {
			t.Errorf("%s: unmasked: %q", tc.name, f.Message)
		}
	}
}

func TestRouteDefaults(t *testing.T) {
	if _, err := Route(Config{}); err == nil {
		t.Fatal("a route without a store or transport")
	}
	if Endpoint(BaseURL+"/") != "https://api.deepseek.com/anthropic/v1/messages" {
		t.Fatal(Endpoint(BaseURL))
	}
}
