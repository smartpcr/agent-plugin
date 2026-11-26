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

// A handler that ignores Done and its closed connection, beside a hijacked socket
// whose handler does the same, cannot hold unload past the grace period: Close
// spends Shutdown, forced closing and the wait for handlers inside one budget and
// reports what it left running.
//
//dsh:test DH-T-HOST-5
func TestUnloadWithinGraceWhenHandlersHang(t *testing.T) {
	const grace = time.Second
	r, logs := cordisLogs(t)
	s, err := listen.Start(r, listen.Config{Port: anyPort(), ShutdownGrace: grace}, closedReady())
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	t.Cleanup(func() { close(release); s.Close() })
	hungOpen, sockOpen := make(chan struct{}), make(chan struct{})
	hung := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(hungOpen)
		<-release
	})
	socket := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: test\r\nConnection: Upgrade\r\n\r\n")
		rw.Flush()
		close(sockOpen)
		<-release
	})
	if err := mount(t, r, s, "hung", listen.Mount{Prefix: "/hung", Classify: anon, Serve: hung}); err != nil {
		t.Fatal(err)
	}
	if err := mount(t, r, s, "socket", listen.Mount{Prefix: "/ws", Classify: anon, Stream: always, Serve: socket}); err != nil {
		t.Fatal(err)
	}
	resp, err := client(t).Get(s.Origin() + "/hung")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	raw, err := net.Dial("tcp", strings.TrimPrefix(s.Origin(), "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	io.WriteString(raw, "GET /ws HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: test\r\nConnection: Upgrade\r\n\r\n")
	br := bufio.NewReader(raw)
	if status, err := br.ReadString('\n'); err != nil || !strings.Contains(status, "101") {
		t.Fatalf("socket status %q, %v; want 101", status, err)
	}
	<-hungOpen
	<-sockOpen

	began := time.Now()
	s.Close()
	if took := time.Since(began); took >= grace {
		t.Errorf("Close took %s with hung handlers, want under the grace period %s", took, grace)
	}
	raw.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadAll(br); isTimeout(err) {
		t.Errorf("hijacked socket still open after Close: %v", err)
	}
	if !logged(logs, "handlers still running") {
		t.Error("Close left handlers running without reporting it")
	}
}

// dropped reports how long conn stayed open before the listener closed it; a read
// that only times out means the listener never closed it.
func dropped(t *testing.T, conn net.Conn, r io.Reader, since time.Time) time.Duration {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadAll(r); isTimeout(err) {
		t.Fatalf("connection still open after %s", time.Since(since))
	}
	return time.Since(since)
}

// A slow header, an oversized header and an idle connection are cut at the
// configured limits, and a panicking handler yields 500 without spoiling the next
// request.
//
//dsh:test DH-T-HOST-8
func TestLimits(t *testing.T) {
	const headerBy, idleBy = 300 * time.Millisecond, 400 * time.Millisecond
	r := cordisRoot(t)
	s, err := listen.Start(r, listen.Config{
		Port:              anyPort(),
		ReadHeaderTimeout: headerBy,
		IdleTimeout:       idleBy,
		MaxHeaderBytes:    4 << 10,
	}, closedReady())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })
	fault := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("handler") })
	if err := mount(t, r, s, "ok", listen.Mount{Prefix: "/ok", Classify: anon, Serve: ok}); err != nil {
		t.Fatal(err)
	}
	if err := mount(t, r, s, "fault", listen.Mount{Prefix: "/fault", Classify: anon, Serve: fault}); err != nil {
		t.Fatal(err)
	}
	addr := strings.TrimPrefix(s.Origin(), "http://")
	dial := func() net.Conn {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}

	t.Run("slow header", func(t *testing.T) {
		conn := dial()
		began := time.Now()
		io.WriteString(conn, "GET /ok HTTP/1.1\r\nHost: 127.0.0.1\r\n")
		if took := dropped(t, conn, conn, began); took < headerBy || took > headerBy+2*time.Second {
			t.Errorf("unfinished header closed after %s, want about %s", took, headerBy)
		}
	})

	t.Run("oversized header", func(t *testing.T) {
		conn := dial()
		io.WriteString(conn, "GET /ok HTTP/1.1\r\nHost: 127.0.0.1\r\nX-Big: "+strings.Repeat("a", 64<<10)+"\r\n\r\n")
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
			t.Errorf("oversized header = %d, want 431", resp.StatusCode)
		}
	})

	t.Run("idle", func(t *testing.T) {
		conn := dial()
		br := bufio.NewReader(conn)
		io.WriteString(conn, "GET /ok HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n")
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatal(err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
		began := time.Now()
		if took := dropped(t, conn, br, began); took > idleBy+2*time.Second {
			t.Errorf("idle connection closed after %s, want about %s", took, idleBy)
		}
	})

	t.Run("panic", func(t *testing.T) {
		c := client(t)
		if code, _ := get(t, c, s.Origin()+"/fault"); code != http.StatusInternalServerError {
			t.Errorf("GET /fault = %d, want 500", code)
		}
		if code, body := get(t, c, s.Origin()+"/ok"); code != http.StatusOK || body != "ok" {
			t.Errorf("GET /ok after a panic = %d %q, want 200 ok", code, body)
		}
	})
}

func closedReady() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func logged(logs *cordis.MemoryExporter, text string) bool {
	for _, m := range logs.Messages() {
		if strings.Contains(m.Text, text) {
			return true
		}
	}
	return false
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
		for prefix, cc := range map[string]string{"/fixed": "no-transform", "/fixed-list": "public, max-age=60 ,No-Transform"} {
			fixed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Cache-Control", cc)
				serveHTML(w, r)
			})
			if err := mount(t, r, l, prefix, listen.Mount{Prefix: prefix, Classify: anon, Serve: fixed}); err != nil {
				t.Fatal(err)
			}
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
		for _, path := range []string{"/fixed", "/fixed-list"} {
			if gz, body := gzipped(t, l.Origin(), path); gz || body != html {
				t.Errorf("compression %v: %s (Cache-Control no-transform, %d bytes) gzip %v, want it as written", on, path, len(html), gz)
			}
		}
	}
}
