package listen_test

import (
	"bufio"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/assets"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
)

func anon(*http.Request) listen.Class { return listen.Anonymous }
func always(*http.Request) bool       { return true }

// settled waits for the goroutine count to fall back to at most n.
func settled(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for runtime.NumGoroutine() > n {
		select {
		case <-deadline:
			buf := make([]byte, 1<<16)
			t.Fatalf("%d goroutines remain, want at most %d:\n%s", runtime.NumGoroutine(), n, buf[:runtime.Stack(buf, true)])
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// Unloading host.listen with an open Stream response and an open hijacked socket
// (a WebSocket) returns within the grace period: both handlers see Done, the socket
// is closed by the listener, and no goroutine remains.
//
//dsh:test DH-T-HOST-5
func TestUnloadEndsStreamsAndSockets(t *testing.T) {
	before := runtime.NumGoroutine()
	const grace = 3 * time.Second
	r := cordisRoot(t)
	f, err := r.Use(listen.Component, listen.Config{Port: anyPort(), ShutdownGrace: grace})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Dispose)
	if err := f.Wait(); err != nil {
		t.Fatal(err)
	}
	var l listen.Listener
	if err := load(t, r, "get listener", func(ctx *cordis.Context) (err error) { l, err = listen.Key.Get(ctx); return err }, listen.Key.Name()); err != nil {
		t.Fatal(err)
	}

	streamOpen, streamSawDone := make(chan struct{}), make(chan struct{})
	sockOpen, sockSawDone, sockEnded := make(chan struct{}), make(chan struct{}), make(chan struct{})
	stream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(streamOpen)
		<-l.Done()
		close(streamSawDone)
	})
	socket := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		defer close(sockEnded)
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: test\r\nConnection: Upgrade\r\n\r\n")
		rw.Flush()
		close(sockOpen)
		<-l.Done()
		close(sockSawDone)
		// Left open on purpose: the listener must close what remains. The read
		// returns only when it does.
		conn.Read(make([]byte, 1))
	})
	if err := mount(t, r, l, "stream", listen.Mount{Prefix: "/stream", Classify: anon, Stream: always, Serve: stream}); err != nil {
		t.Fatal(err)
	}
	if err := mount(t, r, l, "socket", listen.Mount{Prefix: "/ws", Classify: anon, Stream: always, Serve: socket}); err != nil {
		t.Fatal(err)
	}

	c := client(t)
	resp, err := c.Get(l.Origin() + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	raw, err := net.Dial("tcp", strings.TrimPrefix(l.Origin(), "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	io.WriteString(raw, "GET /ws HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: test\r\nConnection: Upgrade\r\n\r\n")
	if status, err := bufio.NewReader(raw).ReadString('\n'); err != nil || !strings.Contains(status, "101") {
		t.Fatalf("socket status %q, %v; want 101", status, err)
	}
	<-streamOpen
	<-sockOpen

	began := time.Now()
	f.Dispose()
	if took := time.Since(began); took >= grace {
		t.Errorf("unload took %s, want under the grace period %s", took, grace)
	}
	if !isClosed(streamSawDone) || !isClosed(sockSawDone) || !isClosed(sockEnded) {
		t.Errorf("after unload: stream saw Done %v, socket saw Done %v, socket handler returned %v; want all",
			isClosed(streamSawDone), isClosed(sockSawDone), isClosed(sockEnded))
	}
	raw.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := raw.Read(make([]byte, 64)); err == nil || isTimeout(err) {
		t.Errorf("client socket read after unload: %v, want the connection closed", err)
	}
	// The stream's body ends with the unload; how it ends (EOF or reset) is the client's.
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	raw.Close()
	c.CloseIdleConnections()
	settled(t, before)
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

const page = "<!doctype html><title>dsh</title><p>hello</p>"

// gzipped fetches path asking for gzip and reports whether the answer is gzip, with
// the decoded body.
func gzipped(t *testing.T, origin, path string, header ...string) (bool, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, origin+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	c := &http.Client{Transport: &http.Transport{DisableCompression: true}, Timeout: 10 * time.Second}
	t.Cleanup(c.CloseIdleConnections)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body io.Reader = resp.Body
	gz := resp.Header.Get("Content-Encoding") == "gzip"
	if gz {
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		body = zr
	}
	b, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	return gz, string(b)
}

// Compression is off by default; when on, an HTML response is gzip and a Stream
// route, an asset and a range response are not.
//
//dsh:test DH-T-HOST-7
func TestCompressionOptIn(t *testing.T) {
	html := strings.Repeat(page, 64)
	serveHTML := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "page.html", time.Time{}, strings.NewReader(html))
	})
	for _, on := range []bool{false, true} {
		r := cordisRoot(t)
		cfg := listen.Config{Port: anyPort()}
		if on {
			// MinBytes 1 so the small embedded asset would be compressed if it were wrapped.
			cfg.Compression = listen.Compression{Mode: listen.CompressionGzip, MinBytes: 1}
		}
		l, err := start(t, r, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := mount(t, r, l, "page", listen.Mount{Prefix: "/page", Classify: anon, Serve: serveHTML}); err != nil {
			t.Fatal(err)
		}
		if err := mount(t, r, l, "events", listen.Mount{Prefix: "/events", Classify: anon, Stream: always, Serve: serveHTML}); err != nil {
			t.Fatal(err)
		}
		f, err := r.Use(assets.Component, cordis.None{})
		if err != nil || f.Wait() != nil {
			t.Fatal(err, f.Wait())
		}
		t.Cleanup(f.Dispose)

		if gz, body := gzipped(t, l.Origin(), "/page"); gz != on || body != html {
			t.Errorf("compression %v: HTML gzip %v (body intact %v), want gzip %v", on, gz, body == html, on)
		}
		if gz, _ := gzipped(t, l.Origin(), "/events"); gz {
			t.Errorf("compression %v: a Stream route was gzip", on)
		}
		if gz, body := gzipped(t, l.Origin(), "/assets/favicon.svg"); gz || !strings.Contains(body, "<svg") {
			t.Errorf("compression %v: asset gzip %v, want plain svg", on, gz)
		}
		if gz, body := gzipped(t, l.Origin(), "/page", "Range", "bytes=0-9"); gz || body != html[:10] {
			t.Errorf("compression %v: range gzip %v body %q, want the plain first 10 bytes", on, gz, body)
		}
	}
}
