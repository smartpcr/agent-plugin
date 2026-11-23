// Package deepseek is the deepseek-official route (architecture.md sec. 3.2):
// DeepSeek's Anthropic-compatible Messages endpoint on api.deepseek.com, reached
// through the shared model transport, authenticated by an API key the secret
// store resolves per attempt, with DeepSeek's errors mapped onto the closed
// failure table (Classify).
//
// Status: keyless, fake-tested; not proven against a real API (OWNER-2). Under
// decision M1-D-PIPE-5 A the route is kept and tested only against fakes (the
// test kit's fake transport and loopback servers serving hand-written stream
// fixtures, with a fake key);
// M1 holds no DeepSeek key, so no request of this package has reached
// api.deepseek.com. The M1 real-model routes are the signed-in client routes
// (llm/provider/claudecode and llm/provider/copilot, DH-D-LLM-12).
package deepseek

import (
	"context"
	"errors"
	"iter"
	"net/http"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/transport"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/wire/anthropic"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// RouteID is the route this package registers.
const RouteID vocab.RouteID = "deepseek-official"

// BaseURL is DeepSeek's Anthropic-compatible API root; the endpoint is
// BaseURL + "/v1/messages".
const BaseURL = "https://api.deepseek.com/anthropic"

// DefaultMaxTokens is the route default for max_tokens, which the dialect requires.
const DefaultMaxTokens = 8192

// KeyRef is the default reference of the API key (env DEEPSEEK_API_KEY).
var KeyRef = secret.MustParse("secret://deepseek/api-key")

// Models is the route's catalogue. Neither model lists an effort: the vendor's
// model listing carries none (README tech-debt item 1), so an effort is refused
// by the runtime before any request.
var Models = []llm.Model{{Name: "deepseek-chat"}, {Name: "deepseek-reasoner"}}

// Config configures the route.
type Config struct {
	// BaseURL overrides BaseURL (a gateway or a loopback test server).
	BaseURL string
	// Key is the API key's reference; zero is KeyRef.
	Key secret.Ref
	// Secrets resolves Key on every attempt; required.
	Secrets *secret.Store
	// Transport sends the requests; required. Production transports come from
	// transport.New under a model-request permit for the BaseURL host.
	Transport *transport.Transport
	// MaxConcurrent caps the route's concurrent attempts; zero is uncapped.
	MaxConcurrent int
	// Retry is the route's retry policy; zero is llm.DefaultRetry.
	Retry llm.RetryPolicy
	// Diagnose receives the encoder's diagnostics; nil drops them.
	Diagnose func(string)
}

// Route is the deepseek-official route over cfg.
func Route(cfg Config) (llm.Route, error) {
	if cfg.Secrets == nil {
		return llm.Route{}, errors.New("deepseek: no secret store")
	}
	if cfg.Transport == nil {
		return llm.Route{}, errors.New("deepseek: no transport")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = BaseURL
	}
	cfg.BaseURL = strings.TrimSuffix(cfg.BaseURL, "/")
	if cfg.Key.IsZero() {
		cfg.Key = KeyRef
	}
	cfg.Secrets.Name(cfg.Key)
	if cfg.Retry == (llm.RetryPolicy{}) {
		cfg.Retry = llm.DefaultRetry()
	}
	models := make([]llm.Model, len(Models))
	copy(models, Models)
	return llm.Route{
		ID:            RouteID,
		Models:        models,
		Defaults:      vocab.Controls{MaxOutputTokens: DefaultMaxTokens},
		MaxConcurrent: cfg.MaxConcurrent,
		Retry:         cfg.Retry,
		Adapter:       &adapter{cfg: cfg},
	}, nil
}

// Endpoint is the Messages endpoint of base.
func Endpoint(base string) string { return strings.TrimSuffix(base, "/") + "/v1/messages" }

type adapter struct{ cfg Config }

func (a *adapter) Prepare(_ context.Context, route vocab.RouteID, model string, cfg vocab.CallConfig) (llm.Call, error) {
	if route != RouteID {
		return nil, transport.Fail(vocab.CodeNoRoute, "deepseek: asked for route %q", route)
	}
	if cfg.Controls.Effort != "" {
		return nil, transport.Fail(vocab.CodeUnsupportedReasoningEffort, "deepseek: %s takes no effort", model)
	}
	return &call{cfg: a.cfg, model: model, call: cfg}, nil
}

type call struct {
	cfg   Config
	model string
	call  vocab.CallConfig
}

// Attempt encodes req, resolves the key, posts and decodes the stream. Every
// failure text passes through the store's mask, so the key never reaches a
// settlement or a log.
func (c *call) Attempt(ctx context.Context, req session.Request) iter.Seq2[vocab.Chunk, error] {
	return func(yield func(vocab.Chunk, error) bool) {
		mask := c.cfg.Secrets.MaskString
		enc, err := anthropic.Encode(req, c.model, c.call)
		if err != nil {
			yield(nil, err)
			return
		}
		if c.cfg.Diagnose != nil {
			for _, d := range enc.Diagnostics {
				c.cfg.Diagnose(d)
			}
		}
		key, _, err := c.cfg.Secrets.Resolve(ctx, c.cfg.Key)
		if err != nil {
			code := vocab.CodeInvalidCredential
			if herr.Has(err, secret.ReasonNotConfigured) {
				code = vocab.CodeMissingCredential
			}
			yield(nil, transport.Fail(code, "deepseek: %s", mask(err.Error())))
			return
		}
		h := Headers(key)
		resp, err := c.cfg.Transport.Post(ctx, transport.Request{
			URL: Endpoint(c.cfg.BaseURL), Header: h, Body: enc.Body, Classify: Classify(mask),
		})
		if err != nil {
			yield(nil, masked(err, mask))
			return
		}
		defer resp.Body.Close()
		for ch, err := range anthropic.Decode(resp.Body, anthropic.ErrorClassifier(Classify(mask))) {
			if err != nil {
				var f *vocab.Failure
				if errors.As(err, &f) && f.RequestID == "" {
					f.RequestID = resp.RequestID
				}
				err = masked(err, mask)
			}
			if f, ok := ch.(vocab.Finish); ok && f.Failure != nil {
				if f.Failure.RequestID == "" {
					f.Failure.RequestID = resp.RequestID
				}
				scrub(f.Failure, mask)
			}
			if !yield(ch, err) {
				return
			}
			if err != nil {
				return
			}
		}
	}
}

// Headers are the request headers for key: x-api-key, the dialect version and
// the content negotiation. The key is the only secret among them.
func Headers(key secret.Value) http.Header {
	h := http.Header{}
	h.Set("x-api-key", key.Reveal())
	h.Set("anthropic-version", anthropic.Version)
	h.Set("content-type", "application/json")
	h.Set("accept", "text/event-stream")
	return h
}

// scrub masks every textual field of f a settlement persists. It runs after the
// request id is attributed, so a header echoing the key is masked too.
func scrub(f *vocab.Failure, mask func(string) string) {
	f.Message = mask(f.Message)
	f.RequestID = mask(f.RequestID)
}

func masked(err error, mask func(string) string) error {
	var f *vocab.Failure
	if errors.As(err, &f) {
		scrub(f, mask)
		return f
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return transport.Fail(vocab.CodeTransport, "deepseek: %s", mask(err.Error()))
}
