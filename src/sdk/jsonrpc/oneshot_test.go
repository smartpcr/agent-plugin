package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

// lockedBuffer is an io.Writer the test reads after ServeOne has returned.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Split(strings.TrimSuffix(l.b.String(), "\n"), "\n")
}

var echo = map[string]Handler{
	"echo": func(_ context.Context, params json.RawMessage) (any, error) { return params, nil },
}

// TestServeOneServesOneCall is the stage scenario "One-shot serves one call": one
// request over a pipe gets exactly one reply, ServeOne returns nil and closes the
// reader, so a second line is refused by the pipe.
//
//dsh:test DH-T-SDK-1
func TestServeOneServesOneCall(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	var out lockedBuffer
	served := make(chan error, 1)
	go func() { served <- ServeOne(context.Background(), pr, &out, Options{Methods: echo}) }()
	if _, err := pw.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"echo","params":{"a":1}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := await(t, served); err != nil {
		t.Fatalf("ServeOne = %v", err)
	}
	if got := out.lines(); len(got) != 1 || got[0] != `{"jsonrpc":"2.0","id":1,"result":{"a":1}}` {
		t.Fatalf("written = %q, want exactly the one reply", got)
	}
	if _, err := pw.Write([]byte(`{"jsonrpc":"2.0","id":2,"method":"echo"}` + "\n")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("a second line after the reply: %v, want io.ErrClosedPipe", err)
	}
}

// TestServeOneRefusesASecondFrame: a frame that arrives while the request runs is
// answered jsonrpc/invalid-request; the request is still answered, and ServeOne
// returns the refusal.
//
//dsh:test DH-T-SDK-1
func TestServeOneRefusesASecondFrame(t *testing.T) {
	in, feed := io.Pipe()
	fromConn, out := io.Pipe()
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		feed.Close()
		fromConn.Close()
	})
	served := make(chan error, 1)
	go func() {
		served <- ServeOne(context.Background(), in, out, Options{Methods: map[string]Handler{
			"hold": func(context.Context, json.RawMessage) (any, error) {
				close(started)
				<-release
				return "held", nil
			},
		}})
	}()
	sent := make(chan error, 1)
	go func() {
		_, err := feed.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"hold"}` + "\n"))
		if err == nil {
			<-started
			_, err = feed.Write([]byte(`{"jsonrpc":"2.0","id":2,"method":"hold"}` + "\n"))
		}
		sent <- err
	}()
	rd := NewReader(fromConn, 0)
	read := func() *Response {
		t.Helper()
		ch := make(chan Frame, 1)
		go func() {
			f, _ := rd.Read()
			ch <- f
		}()
		r, ok := await(t, ch).(*Response)
		if !ok {
			t.Fatal("expected a response")
		}
		return r
	}
	if r := read(); r.ID != Int64ID(2) || r.Error == nil || r.Error.Reason != ReasonInvalidRequest {
		t.Fatalf("second frame answered %+v, want %s", r, ReasonInvalidRequest)
	}
	if err := await(t, sent); err != nil {
		t.Fatal(err)
	}
	close(release)
	if r := read(); r.ID != Int64ID(1) || string(r.Result) != `"held"` {
		t.Fatalf("reply = %+v", r)
	}
	if err := await(t, served); !HasReason(err, ReasonInvalidRequest) {
		t.Fatalf("ServeOne = %v, want %s", err, ReasonInvalidRequest)
	}
}

// TestServeOneFirstFrame: a first frame that is not a request is answered with its
// table error and returned; a method outside the allow-list is answered and is not
// a ServeOne error; input that ends with no frame is jsonrpc/closed.
//
//dsh:test DH-T-SDK-1, DH-T-SDK-2
func TestServeOneFirstFrame(t *testing.T) {
	for _, tc := range []struct {
		name, in, reason, reply string
	}{
		{"notification", `{"jsonrpc":"2.0","method":"echo"}`, ReasonInvalidRequest, `{"jsonrpc":"2.0","id":null,"error":`},
		{"response", `{"jsonrpc":"2.0","id":1,"result":1}`, ReasonInvalidRequest, `{"jsonrpc":"2.0","id":null,"error":`},
		{"not json", `nope`, ReasonParseError, `{"jsonrpc":"2.0","id":null,"error":`},
		{"bad envelope", `{"jsonrpc":"1.0","id":4,"method":"echo"}`, ReasonInvalidRequest, `{"jsonrpc":"2.0","id":4,"error":`},
		{"over the bound", strings.Repeat("x", 300), ReasonLineTooLong, `{"jsonrpc":"2.0","id":null,"error":`},
		{"empty", "\n\n", ReasonClosed, ""},
		{"unknown method", `{"jsonrpc":"2.0","id":3,"method":"nope"}`, "", `{"jsonrpc":"2.0","id":3,"error":{"code":-32601`},
		{"half-closed", `{"jsonrpc":"2.0","id":5,"method":"echo","params":[1]}`, "", `{"jsonrpc":"2.0","id":5,"result":[1]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out lockedBuffer
			err := ServeOne(context.Background(), strings.NewReader(tc.in+"\n"), &out, Options{Methods: echo, MaxFrameBytes: 256})
			switch {
			case tc.reason == "" && err != nil:
				t.Fatalf("ServeOne = %v, want nil", err)
			case tc.reason != "" && !HasReason(err, tc.reason):
				t.Fatalf("ServeOne = %v, want %s", err, tc.reason)
			}
			got := out.lines()
			if tc.reply == "" {
				if len(got) != 1 || got[0] != "" {
					t.Fatalf("written = %q, want nothing", got)
				}
				return
			}
			if len(got) != 1 || !strings.HasPrefix(got[0], tc.reply) {
				t.Fatalf("written = %q, want one frame starting %s", got, tc.reply)
			}
		})
	}
}

// TestServeOneFailures: a reply that cannot be written and an ended context are
// returned as the connection's error.
//
//dsh:test DH-T-SDK-1
func TestServeOneFailures(t *testing.T) {
	err := ServeOne(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"echo"}`+"\n"), failingWriter{}, Options{Methods: echo})
	if !HasReason(err, ReasonClosed) || !strings.Contains(err.Error(), "broken pipe") {
		t.Fatalf("ServeOne with a failing writer = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	started := make(chan struct{})
	served := make(chan error, 1)
	go func() {
		served <- ServeOne(ctx, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"wait"}`+"\n"), io.Discard, Options{Methods: map[string]Handler{
			"wait": func(ctx context.Context, _ json.RawMessage) (any, error) {
				close(started)
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}})
	}()
	await(t, started)
	cancel()
	if err := await(t, served); !HasReason(err, ReasonClosed) || !strings.Contains(err.Error(), "context") {
		t.Fatalf("ServeOne after its context ended = %v", err)
	}
}
