package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

func collect(t *testing.T, s string) ([]Event, error) {
	t.Helper()
	var out []Event
	for e, err := range Events(strings.NewReader(s)) {
		if err != nil {
			return out, err
		}
		out = append(out, e)
	}
	return out, nil
}

// The reader follows the standard: every line ending, a BOM, comments, joined
// data lines, the default type, and a cut final event dropped.
func TestEventsStandard(t *testing.T) {
	got, err := collect(t, "\xEF\xBB\xBF: hi\r\nevent: a\r\ndata: 1\r\ndata:2\r\n\r\ndata: x\rid: 9\r\rdata: \xff\n\nevent: cut\ndata: partial")
	if err != nil {
		t.Fatal(err)
	}
	want := []Event{{Type: "a", Data: "1\n2"}, {Type: "message", Data: "x", ID: "9"}, {Type: "message", Data: "\uFFFD", ID: "9"}}
	if len(got) != len(want) {
		t.Fatalf("events = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// stalledReader serves its chunks one Read at a time and then stands in for a
// writer that stays open: a further Read fails the test instead of blocking.
type stalledReader struct {
	t      *testing.T
	chunks []string
}

func (r *stalledReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		r.t.Fatal("reader waited for bytes past a complete event")
	}
	n := copy(p, r.chunks[0])
	r.chunks[0] = r.chunks[0][n:]
	if r.chunks[0] == "" {
		r.chunks = r.chunks[1:]
	}
	return n, nil
}

// A CR ends its line at once: the event is dispatched without reading on, and
// a CRLF pair split across reads still counts as one line ending.
func TestEventsCRDispatchesWithoutWaiting(t *testing.T) {
	for name, chunks := range map[string][]string{
		"cr only":    {"data: hello\r\r"},
		"split crlf": {"data: hel", "lo\r", "\ndata: x\r\n\r"},
	} {
		want := map[string]string{"cr only": "hello", "split crlf": "hello\nx"}[name]
		got := false
		for e, err := range Events(&stalledReader{t: t, chunks: chunks}) {
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if e.Data != want || e.Type != "message" {
				t.Fatalf("%s: event = %+v, want data %q", name, e, want)
			}
			got = true
			break
		}
		if !got {
			t.Fatalf("%s: no event dispatched", name)
		}
	}
}

func TestEventsBounded(t *testing.T) {
	if _, err := collect(t, "data: "+strings.Repeat("x", MaxLine+1)+"\n\n"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("long line: %v", err)
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		h    http.Header
		want time.Duration
		ok   bool
	}{
		{http.Header{"Retry-After": {"2"}}, 2 * time.Second, true},
		{http.Header{"Retry-After-Ms": {"1500"}, "Retry-After": {"9"}}, 1500 * time.Millisecond, true},
		{http.Header{"Retry-After": {now.Add(3 * time.Second).Format(http.TimeFormat)}}, 3 * time.Second, true},
		{http.Header{"Retry-After": {"-1"}}, 0, false},
		{http.Header{"Retry-After": {"999999999"}}, 0, false},
		{http.Header{"Retry-After": {"soon"}}, 0, false},
		{http.Header{}, 0, false},
	} {
		got, ok := RetryAfter(tc.h, now)
		if got != tc.want || ok != tc.ok {
			t.Errorf("RetryAfter(%v) = %v %v, want %v %v", tc.h, got, ok, tc.want, tc.ok)
		}
	}
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func respond(status int, h http.Header, body io.ReadCloser) rtFunc {
	return func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: h, Body: body, Request: r}, nil
	}
}

// A non-2xx response is a failure carrying status, retry-after and request id;
// without a classifier the status alone decides.
func TestPostStatusFailure(t *testing.T) {
	tr := Over(respond(429, http.Header{"Retry-After": {"3"}, "Request-Id": {"r1"}}, io.NopCloser(strings.NewReader("{}"))), Options{})
	_, err := tr.Post(context.Background(), Request{URL: "http://model.test/v1"})
	var f *vocab.Failure
	if !errors.As(err, &f) || f.Code != vocab.CodeRateLimit || f.Status != 429 || f.RetryAfter != 3*time.Second || f.RequestID != "r1" {
		t.Fatalf("failure = %+v", err)
	}
	if f.Validate() != nil {
		t.Fatalf("invalid failure: %v", f.Validate())
	}
}

// stall is a body that sends one byte and then nothing until its request ends.
type stall struct {
	ctx  context.Context
	sent bool
}

func (s *stall) Read(p []byte) (int, error) {
	if !s.sent {
		s.sent = true
		p[0] = 'd'
		return 1, nil
	}
	<-s.ctx.Done()
	return 0, s.ctx.Err()
}
func (*stall) Close() error { return nil }

// A body silent for Idle fails with llm/timeout; the parent context is untouched.
func TestPostIdleTimeout(t *testing.T) {
	tr := Over(rtFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: &stall{ctx: r.Context()}, Request: r}, nil
	}), Options{Idle: 20 * time.Millisecond})
	ctx := context.Background()
	resp, err := tr.Post(ctx, Request{URL: "http://model.test/v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, err = io.ReadAll(resp.Body)
	var f *vocab.Failure
	if !errors.As(err, &f) || f.Code != vocab.CodeTimeout {
		t.Fatalf("read error = %v, want llm/timeout", err)
	}
}

// A non-2xx body that stalls after its headers is bounded by Idle too: the
// status still classifies the failure and the message says the body was cut.
func TestPostStalledErrorBody(t *testing.T) {
	tr := Over(rtFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Header: http.Header{"Request-Id": {"r2"}}, Body: &stall{ctx: r.Context()}, Request: r}, nil
	}), Options{Idle: 20 * time.Millisecond})
	_, err := tr.Post(context.Background(), Request{URL: "http://model.test/v1"})
	var f *vocab.Failure
	if !errors.As(err, &f) || f.Code != vocab.CodeAuth || f.Status != 401 || f.RequestID != "r2" {
		t.Fatalf("failure = %+v, want llm/auth status 401", err)
	}
	if !strings.Contains(f.Message, "cut short") || !strings.Contains(f.Message, string(vocab.CodeTimeout)) {
		t.Fatalf("message = %q, want the idle cut named", f.Message)
	}
}

// A caller cancelling during a stalled error body gets its own error back.
func TestPostErrorBodyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := Over(rtFunc(func(r *http.Request) (*http.Response, error) {
		time.AfterFunc(20*time.Millisecond, cancel)
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: &stall{ctx: r.Context()}, Request: r}, nil
	}), Options{Idle: time.Hour})
	if _, err := tr.Post(ctx, Request{URL: "http://model.test/v1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the context's", err)
	}
}

func TestPostCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tr := Over(rtFunc(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() }), Options{})
	if _, err := tr.Post(ctx, Request{URL: "http://model.test/v1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the context's", err)
	}
}
