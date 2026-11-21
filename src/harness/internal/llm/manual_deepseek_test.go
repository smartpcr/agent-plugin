//go:build manual

// The DH-S-9 manual check (impl.md DH-S-9; tech-spec A-3, EV-6): one streamed
// request to the real DeepSeek API with a good key and one with a bad key. It
// is `manual`, never a gate criterion: the build tag keeps it out of every
// default and gate run, and it reads the key at run time only. Run with
//
//	$env:DEEPSEEK_API_KEY = '<key>'   # never committed, never logged
//	go -C src/harness test -tags manual -count=1 -v -run TestManualDeepSeek ./internal/llm/
//
// and copy the printed lines into docs/stories/harness-DSH/evidence/DH-S-9.md.

package llm_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/deepseek"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

// manualBadKey is shaped like a key and is not one.
const manualBadKey = "sk-dsh-manual-check-not-a-key-0000"

func manualKeys(key string) *secret.Store {
	return secret.NewStore(secret.EnvSource(func(name string) (string, bool) {
		if name == deepseek.KeyRef.EnvName() && key != "" {
			return key, true
		}
		return "", false
	}))
}

// manualRuntime is a runtime on the real endpoint with a model-request permit
// for it, the default bounded policy (which retries transient failures, so a
// network fault ends after its bound rather than hanging the check) and a
// second route that would succeed if a failure ever fell back to it.
func manualRuntime(t *testing.T, key string) (*llm.Runtime, *fake) {
	t.Helper()
	tr := egressTransport(t, "https://api.deepseek.com")
	rt := llm.New()
	must(rt.Register("deepseek", must(deepseek.Route(deepseek.Config{Secrets: manualKeys(key), Transport: tr, Retry: llm.DefaultRetry()}))))
	other := &fake{scripts: []script{chunks(append(text("from elsewhere"), vocab.Finish{Reason: vocab.FinishStop})...)}}
	must(rt.Register("other", llm.Route{ID: "r/a", Models: []llm.Model{{Name: "m1"}}, Adapter: other, Retry: llm.DefaultRetry()}))
	return rt, other
}

func TestManualDeepSeekGoodKey(t *testing.T) {
	key := os.Getenv(deepseek.KeyRef.EnvName())
	if key == "" {
		t.Skipf("manual: set %s at run time", deepseek.KeyRef.EnvName())
	}
	rt, _ := manualRuntime(t, key)
	h, _ := dsSession(t)
	sink := attemptOnce(t, rt, h) // one streamed request, exactly one settlement
	a := sink.records[0]
	kind := must(a.Draft()).Type()
	if kind != "assistant/message" || a.Usage == nil {
		t.Fatalf("good key settled %s (usage %v, failure %+v), want assistant/message with usage", kind, a.Usage, a.Finish.Failure)
	}
	t.Logf("DH-S-9 good key: settlements=1 kind=%s seq=%d message=%s finish=%s usage=%+v",
		kind, sink.seqs[0], a.Message.ID(), a.Finish.Reason, *a.Usage)
}

func TestManualDeepSeekBadKey(t *testing.T) {
	rt, other := manualRuntime(t, manualBadKey)
	req, sink := auxRequest(t)
	ctx, cancel := context.WithTimeout(bg, 2*time.Minute)
	defer cancel()
	_, err := rt.Complete(ctx, req, sink)
	for i, a := range sink.records {
		t.Logf("settlement %d: %s failure %+v", i+1, must(a.Draft()).Type(), a.Finish.Failure)
	}
	var fl *vocab.Failure
	if !errors.As(err, &fl) || fl.Code != vocab.CodeAuth || fl.Class != vocab.Permanent {
		t.Fatalf("bad key: Complete = %v, want a permanent llm/auth", err)
	}
	if len(sink.records) != 1 || settledTypes(t, sink)[0] != "assistant/attempt" || sink.records[0].Route != deepseek.RouteID {
		t.Fatalf("bad key: settlements %v, want exactly one assistant/attempt on %s (no retry)", settledTypes(t, sink), deepseek.RouteID)
	}
	if other.prepares.Load() != 0 || other.attempts.Load() != 0 {
		t.Fatal("bad key: another route was tried")
	}
	for _, ev := range must(sink.h.Read(bg, 0, 0)) {
		if strings.HasPrefix(ev.Type(), "llm/retry") {
			t.Fatalf("bad key: a retry was logged: %s", ev.Type())
		}
	}
	f := sink.records[0].Finish.Failure
	t.Logf("DH-S-9 bad key: settlements=1 kind=assistant/attempt seq=%d code=%s class=%s status=%d request=%s retries=0 other-route-attempts=0",
		sink.seqs[0], f.Code, f.Class, f.Status, f.RequestID)
}
