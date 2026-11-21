package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
)

// Endpoint is the URL the adapter posts to; nothing resolves it, so a request
// that escapes the scripted transport fails instead of reaching a network.
const Endpoint = "https://model.testkit.invalid/v1/stream"

// Adapter is an llm.Adapter speaking the kit's SSE dialect through an HTTP client.
type Adapter struct {
	Client *http.Client
	URL    string
}

// NewAdapter is an adapter whose requests go through rt.
func NewAdapter(rt http.RoundTripper) *Adapter {
	return &Adapter{Client: &http.Client{Transport: rt}, URL: Endpoint}
}

// Body is the JSON the adapter posts: enough for a test to tell attempts apart.
type Body struct {
	Route    vocab.RouteID `json:"route"`
	Model    string        `json:"model"`
	Digest   string        `json:"digest"`
	AsOf     int64         `json:"as_of"`
	Messages int           `json:"messages"`
}

// Prepare is the call for route and model; it does no I/O.
func (a *Adapter) Prepare(_ context.Context, route vocab.RouteID, model string, _ vocab.CallConfig) (llm.Call, error) {
	return &call{a: a, route: route, model: model}, nil
}

type call struct {
	a     *Adapter
	route vocab.RouteID
	model string
}

// Attempt posts req and decodes the stream.
func (c *call) Attempt(ctx context.Context, req session.Request) iter.Seq2[vocab.Chunk, error] {
	return func(yield func(vocab.Chunk, error) bool) {
		msgs, _ := req.Messages()
		body, err := json.Marshal(Body{Route: c.route, Model: c.model, Digest: req.Digest(), AsOf: int64(req.AsOf()), Messages: len(msgs)})
		if err != nil {
			yield(nil, err)
			return
		}
		hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.a.URL, bytes.NewReader(body))
		if err != nil {
			yield(nil, err)
			return
		}
		hreq.Header.Set("Content-Type", "application/json")
		hreq.Header.Set("Accept", "text/event-stream")
		resp, err := c.a.Client.Do(hreq)
		if err != nil {
			yield(nil, err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			f, err := vocab.NewFailure(statusCode(resp.StatusCode), fmt.Sprintf("status %d", resp.StatusCode))
			if err != nil {
				yield(nil, err)
				return
			}
			yield(vocab.Finish{Reason: vocab.FinishError, Failure: f}, nil)
			return
		}
		d := decoder{yield: yield}
		sc := bufio.NewScanner(resp.Body)
		var name, data string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if name != "" && !d.event(name, data) {
					return
				}
				name, data = "", ""
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if err := sc.Err(); err != nil {
			yield(nil, err)
		}
	}
}

func statusCode(status int) vocab.Code {
	switch {
	case status == http.StatusTooManyRequests:
		return vocab.CodeRateLimit
	case status == http.StatusUnauthorized:
		return vocab.CodeAuth
	case status >= 500:
		return vocab.CodeServer
	}
	return vocab.HTTPCode(status)
}

// decoder turns events into chunks: one text block, opened by the first delta
// and closed before the finish.
type decoder struct {
	yield func(vocab.Chunk, error) bool
	open  bool
	text  strings.Builder
}

func (d *decoder) event(name, data string) bool {
	switch name {
	case "text":
		var p struct{ Text string }
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			return d.yield(nil, err)
		}
		if !d.open {
			d.open = true
			if !d.yield(vocab.BlockStart{Index: 0, Kind: vocab.KindText}, nil) {
				return false
			}
		}
		d.text.WriteString(p.Text)
		return d.yield(vocab.TextDelta{Index: 0, Text: p.Text}, nil)
	case "usage":
		var u vocab.Usage
		if err := json.Unmarshal([]byte(data), &u); err != nil {
			return d.yield(nil, err)
		}
		return d.yield(vocab.UsageChunk{Usage: u}, nil)
	case "finish":
		var p struct{ Reason vocab.FinishReason }
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			return d.yield(nil, err)
		}
		return d.close() && d.yield(vocab.Finish{Reason: p.Reason}, nil)
	case "error":
		var p struct {
			Code    vocab.Code
			Message string
		}
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			return d.yield(nil, err)
		}
		f, err := vocab.NewFailure(p.Code, p.Message)
		if err != nil {
			return d.yield(nil, err)
		}
		return d.yield(vocab.Finish{Reason: vocab.FinishError, Failure: f}, nil)
	}
	return d.yield(nil, fmt.Errorf("model: unknown event %q", name))
}

func (d *decoder) close() bool {
	if !d.open {
		return true
	}
	d.open = false
	return d.yield(vocab.BlockEnd{Index: 0, Block: vocab.TextBlock(d.text.String())}, nil)
}

// ModelName is the one model the kit's routes list.
const ModelName = "scripted"

// Register registers route id on rt, served by an adapter over tr, and releases
// the registration when the test ends.
func Register(t testing.TB, rt *llm.Runtime, id vocab.RouteID, tr http.RoundTripper) {
	t.Helper()
	reg, err := rt.Register("testkit", llm.Route{ID: id, Models: []llm.Model{{Name: ModelName}}, Adapter: NewAdapter(tr)})
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	t.Cleanup(reg.Close)
}
