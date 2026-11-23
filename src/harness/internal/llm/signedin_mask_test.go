package llm_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
)

// tokenShape matches the token shapes a signed-in client's output could carry
// (plan 16.5.6; R-11 re-scoped): Anthropic keys and OAuth tokens (sk-ant-),
// GitHub tokens (gho_, ghu_, ghp_, ghs_, ghr_, github_pat_) and bearer
// credentials. The DH-S-9 manual check passes every printed line through it.
var tokenShape = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}|sk-ant-[A-Za-z0-9_-]+|\bgh[oupsr]_[A-Za-z0-9_]+|\bgithub_pat_[A-Za-z0-9_]+`)

// redact is text with every value store has released masked (secret.Store.Mask)
// and every token shape replaced by secret.Redacted.
func redact(store *secret.Store, text string) string {
	return tokenShape.ReplaceAllString(store.MaskString(text), secret.Redacted)
}

// maskID keeps the first eight characters of a client session or request id,
// so the operator can find it, and masks the rest; a token shape is masked
// whole.
func maskID(store *secret.Store, id string) string {
	id = redact(store, id)
	if id == "" || strings.Contains(id, secret.Redacted) {
		return id
	}
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "…"
}

// TestSignedInRedaction is the DH-S-9 manual check's filter, run in the normal
// gate so it is covered without the manual tag: each token shape is masked
// wherever it sits in a line, a released secret value is masked by the store,
// text that only resembles a shape is kept, and a session id keeps its first
// eight characters.
//
//dsh:test DH-T-LLM-22, DH-T-LLM-26
func TestSignedInRedaction(t *testing.T) {
	store := secret.NewStore(secret.EnvSource(func(name string) (string, bool) {
		return "released-value-123", name == "MANUAL_SECRET"
	}))
	if _, _, err := store.Resolve(bg, secret.MustParse("secret://manual/secret")); err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{
		"sk-ant-api03-AbC_d-9", "sk-ant-oat01-xyz", "gho_16C7e42F292c6912E7710c838347Ae178B4a", "ghu_abc", "ghp_abc", "ghs_abc",
		"ghr_abc", "github_pat_11ABCDEFG_xyz", "Bearer eyJhbGciOi.J9.x-y",
	} {
		got := redact(store, "before "+in+" after")
		if got != "before "+secret.Redacted+" after" {
			t.Errorf("redact(%q) = %q", in, got)
		}
	}
	if got := redact(store, "authorization: bearer abc/def+=="); got != "authorization: "+secret.Redacted {
		t.Errorf("a bearer header = %q", got)
	}
	if got := redact(store, "key=released-value-123;"); got != "key="+secret.Redacted+";" {
		t.Errorf("a released value = %q", got)
	}
	for _, keep := range []string{"session 8a1b2c3d-0000-4000-8000-000000000001", "the bearer of news", "high_ghost", "sk-other"} {
		if got := redact(store, keep); got != keep {
			t.Errorf("redact(%q) = %q, want it kept", keep, got)
		}
	}
	if got := maskID(store, "8a1b2c3d-0000-4000-8000-000000000001"); got != "8a1b2c3d…" {
		t.Errorf("maskID = %q", got)
	}
	if maskID(store, "short") != "short" || maskID(store, "") != "" || maskID(store, "gho_abcdefghijkl") != secret.Redacted {
		t.Error("maskID edge cases")
	}
}
