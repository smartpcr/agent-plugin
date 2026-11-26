package assets_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/assets"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
)

var files = fstest.MapFS{
	"app.css":         {Data: []byte("body{}")},
	"app.3f2a9c1b.js": {Data: []byte("let x")},
	"img/logo.svg":    {Data: []byte("<svg/>")},
	"blob.unknownext": {Data: []byte("??")},
}

func serve(method, target string, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	assets.Handler(files, "b1").ServeHTTP(rec, req)
	return rec
}

//dsh:test DH-T-HOST-6
func TestServesFilesWithTypesAndCaching(t *testing.T) {
	rec := serve("GET", "/app.css", nil)
	h := rec.Header()
	if rec.Code != 200 || rec.Body.String() != "body{}" || h.Get("Content-Type") != "text/css; charset=utf-8" ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "no-cache" || h.Get("ETag") != `"b1"` {
		t.Errorf("GET /app.css = %d %q %v", rec.Code, rec.Body.String(), h)
	}
	if rec := serve("GET", "/app.css", http.Header{"If-None-Match": {`"b1"`}}); rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Errorf("matching If-None-Match = %d with %d bytes, want 304 and none", rec.Code, rec.Body.Len())
	}
	if rec := serve("GET", "/app.3f2a9c1b.js", nil); rec.Code != 200 || rec.Header().Get("Cache-Control") != assets.Immutable || rec.Header().Get("ETag") != "" {
		t.Errorf("hashed file = %d %v, want immutable with no build ETag", rec.Code, rec.Header())
	}
	if rec := serve("HEAD", "/img/logo.svg", nil); rec.Code != 200 || rec.Header().Get("Content-Type") != "image/svg+xml" {
		t.Errorf("HEAD /img/logo.svg = %d %v", rec.Code, rec.Header())
	}
	if rec := serve("GET", "/blob.unknownext", nil); rec.Header().Get("Content-Type") != "application/octet-stream" {
		t.Errorf("unknown extension type %q", rec.Header().Get("Content-Type"))
	}
}

//dsh:test DH-T-HOST-6
func TestNeverServesOutsideOneFile(t *testing.T) {
	for _, c := range []struct {
		method, target string
		want           int
	}{
		{"GET", "/../app.css", 404},
		{"GET", "/img/../app.css", 404},
		{"GET", "/img%2flogo.svg", 404},
		{"GET", "/img%5clogo.svg", 404},
		{"GET", "/img", 404},
		{"GET", "/img/", 404},
		{"GET", "/", 404},
		{"GET", "/missing.css", 404},
		{"POST", "/app.css", 405},
		{"PUT", "/app.css", 405},
	} {
		rec := serve(c.method, c.target, nil)
		if rec.Code != c.want || strings.Contains(rec.Body.String(), "body{}") || strings.Contains(rec.Body.String(), "<svg/>") {
			t.Errorf("%s %s = %d %q, want %d and no file bytes", c.method, c.target, rec.Code, rec.Body.String(), c.want)
		}
	}
}

// host.assets mounts the embedded files at /assets/ as an anonymous mount: they
// load through the listener with no authenticator in the tree.
func TestComponentMountsEmbeddedFiles(t *testing.T) {
	r := cordis.New()
	r.Loggers().ClearExporters()
	r.Loggers().AddExporter(&cordis.MemoryExporter{})
	for _, use := range []struct {
		c   *cordis.Component
		cfg any
	}{{listen.Component, listen.Config{Address: "127.0.0.1:0"}}, {assets.Component, cordis.None{}}} {
		f, err := r.Use(use.c, use.cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(f.Dispose)
		if err := f.Wait(); err != nil {
			t.Fatalf("%s: %v", use.c.Name(), err)
		}
	}
	var origin string
	f, err := r.Apply("origin", func(ctx *cordis.Context) error {
		l, err := listen.Key.Get(ctx)
		if err == nil {
			origin = l.Origin()
		}
		return err
	}, listen.Key.Name())
	if err != nil || f.Wait() != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Dispose)
	c := &http.Client{Transport: &http.Transport{}, Timeout: 10 * time.Second}
	t.Cleanup(c.CloseIdleConnections)
	resp, err := c.Get(origin + "/assets/favicon.svg")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/svg+xml" || !strings.Contains(string(b), "<svg") {
		t.Errorf("GET /assets/favicon.svg = %d %v", resp.StatusCode, resp.Header)
	}
}
