package faultserver_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/egresstest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/faultserver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/permit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
)

// client is a recording egress client allowed to reach srv.
func client(t *testing.T, srv *faultserver.Server) *http.Client {
	t.Helper()
	r := egresstest.New(t, srv.URL())
	c, err := r.Service().Client("model", permit.New(t, "model", srv.URL()), egress.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Service().Release(c) })
	return c
}

// post sends one POST, keyed by the X-Caller header, under ctx.
func post(t *testing.T, ctx context.Context, c *http.Client, srv *faultserver.Server, key string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL()+"/v1/stream", strings.NewReader(`{"caller":"`+key+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Caller", key)
	return c.Do(req)
}

func sse(events ...model.Event) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("event: " + e.Name + "\ndata: " + e.Data + "\n\n")
	}
	return b.String()
}

// DH-T-UTIL-15: each behaviour gives its wire result over a real connection: a
// stream's exact bytes, a status with its Retry-After, a reset after a prefix and
// before any response, a stall that holds until the client goes away, and a
// chunk cut off mid-JSON.
//
//dsh:test DH-T-UTIL-15
func TestEachBehaviourGivesItsWireResult(t *testing.T) {
	prefix := []model.Event{model.Text("par")}
	srv := faultserver.New(t, faultserver.Options{Key: faultserver.HeaderKey("X-Caller")})
	c := client(t, srv)

	t.Run("stream", func(t *testing.T) {
		reply := model.Reply("hello").Events
		srv.Script("stream", faultserver.Stream(reply...))
		resp, err := post(t, t.Context(), c, srv, "stream")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" || string(body) != sse(reply...) {
			t.Fatalf("%d %q %q %v", resp.StatusCode, resp.Header.Get("Content-Type"), body, err)
		}
	})
	t.Run("status", func(t *testing.T) {
		srv.Script("status", faultserver.Status(http.StatusTooManyRequests, 1500*time.Millisecond), faultserver.Status(http.StatusServiceUnavailable, 0))
		for _, want := range []struct {
			code  int
			after string
		}{{http.StatusTooManyRequests, "2"}, {http.StatusServiceUnavailable, ""}} {
			resp, err := post(t, t.Context(), c, srv, "status")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != want.code || resp.Header.Get("Retry-After") != want.after {
				t.Fatalf("status %d retry-after %q, want %d %q", resp.StatusCode, resp.Header.Get("Retry-After"), want.code, want.after)
			}
		}
	})
	t.Run("reset mid-stream", func(t *testing.T) {
		srv.Script("reset", faultserver.Reset(prefix...))
		resp, err := post(t, t.Context(), c, srv, "reset")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err == nil || !bytes.Equal(body, []byte(sse(prefix...))) {
			t.Fatalf("read %q then %v, want the prefix then an error", body, err)
		}
	})
	t.Run("reset before a response", func(t *testing.T) {
		srv.Script("refuse", faultserver.Reset())
		resp, err := post(t, t.Context(), c, srv, "refuse")
		if err == nil {
			_ = resp.Body.Close()
			t.Fatalf("a reset before any response answered %d", resp.StatusCode)
		}
	})
	t.Run("stall", func(t *testing.T) {
		srv.Script("stall", faultserver.Stall(prefix...))
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		resp, err := post(t, ctx, c, srv, "stall")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		got := make([]byte, len(sse(prefix...)))
		if _, err := io.ReadFull(resp.Body, got); err != nil || string(got) != sse(prefix...) {
			t.Fatalf("prefix %q %v", got, err)
		}
		read := make(chan error, 1)
		go func() {
			_, err := resp.Body.Read(make([]byte, 1))
			read <- err
		}()
		cancel()
		if err := <-read; !errors.Is(err, context.Canceled) {
			t.Fatalf("a stalled read ended with %v, want the client's cancellation", err)
		}
	})
	t.Run("malformed", func(t *testing.T) {
		srv.Script("malformed", faultserver.Malformed(prefix...))
		resp, err := post(t, t.Context(), c, srv, "malformed")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || string(body) != sse(prefix...)+faultserver.MalformedChunk {
			t.Fatalf("%q %v", body, err)
		}
	})
}

// DH-T-UTIL-15: two keys advance separate cursors, also when their callers run
// concurrently; one server gives each caller its own faults.
//
//dsh:test DH-T-UTIL-15
func TestTwoKeysAdvanceSeparateCursors(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Key: faultserver.HeaderKey("X-Caller")})
	c := client(t, srv)
	srv.Script("a", faultserver.Reply("a1"), faultserver.Reply("a2"), faultserver.Reply("a3"))
	srv.Script("b", faultserver.Status(http.StatusServiceUnavailable, 0), faultserver.Status(http.StatusBadGateway, 0), faultserver.Reply("b3"))
	got := map[string][]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	for _, key := range []string{"a", "b"} {
		wg.Go(func() {
			for range 3 {
				resp, err := post(t, t.Context(), c, srv, key)
				if err != nil {
					t.Errorf("%s: %v", key, err)
					return
				}
				body, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				out := resp.Status[:3]
				if resp.StatusCode == http.StatusOK {
					out = strings.TrimSpace(body2text(string(body)))
				}
				mu.Lock()
				got[key] = append(got[key], out)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if a, b := strings.Join(got["a"], ","), strings.Join(got["b"], ","); a != "a1,a2,a3" || b != "503,502,b3" {
		t.Fatalf("a got %s, b got %s", a, b)
	}
	if srv.Served("a") != 3 || srv.Served("b") != 3 {
		t.Fatalf("cursors a=%d b=%d", srv.Served("a"), srv.Served("b"))
	}
}

// body2text is the text of a Reply stream.
func body2text(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if rest, ok := strings.CutPrefix(line, `data: {"text":"`); ok {
			return strings.TrimSuffix(rest, `"}`)
		}
	}
	return ""
}

// DH-T-UTIL-15: a request with nothing left for its key fails the test and is
// answered 599; a behaviour never requested fails it when it ends. A seeded mix
// is the same for the same seed.
//
//dsh:test DH-T-UTIL-15
func TestUnscriptedAndLeftoverBehavioursFailTheTest(t *testing.T) {
	rec := &recorder{TB: t}
	t.Cleanup(rec.cleanup)
	srv := faultserver.New(rec, faultserver.Options{Key: faultserver.HeaderKey("X-Caller")})
	c := client(t, srv)
	srv.Script("left", faultserver.Reply("never asked"))
	resp, err := post(t, t.Context(), c, srv, "nobody")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 599 {
		t.Fatalf("an unscripted request answered %d, want 599", resp.StatusCode)
	}
	if f := rec.failed(); len(f) != 1 || !strings.Contains(f[0], `unscripted request POST /v1/stream for key "nobody"`) {
		t.Fatalf("an unscripted request reported %q", f)
	}
	rec.cleanup()
	if f := rec.failed(); len(f) != 2 || !strings.Contains(f[1], `"left"`) {
		t.Fatalf("a leftover behaviour reported %q", f)
	}

	choices := []faultserver.Weighted{{Weight: 3, Behaviour: faultserver.Reply("ok")},
		{Weight: 1, Behaviour: faultserver.Status(http.StatusTooManyRequests, 0)}, {Weight: 1, Behaviour: faultserver.Reset()}}
	one, two := faultserver.Mix(t, 42, 20, choices...), faultserver.Mix(t, 42, 20, choices...)
	if len(one) != 20 {
		t.Fatalf("mix of %d", len(one))
	}
	for i := range one {
		if one[i].String() != two[i].String() {
			t.Fatalf("seed 42 drew %v then %v", one, two)
		}
	}
}
