package sdk

import (
	"fmt"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TC-15b: with DSH_BIN set to the dsh this test builds once and the keyless
// fake route, the client starts, creates a session, Runs a prompt, gets the
// fake's text, and Close exits the child with status 0.
//
//dsh:test DH-T-SDK-16
func TestClientDrivesTheBuiltBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds dsh")
	}
	harness, _ := filepath.Abs(filepath.Join("..", "..", "harness"))
	if _, err := os.Stat(filepath.Join(harness, "cmd", "dsh")); err != nil {
		t.Skipf("no harness module beside src/sdk: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "dsh")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-buildvcs=false", "-tags", "testhook", "-o", bin, "./cmd/dsh")
	build.Dir = harness
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	t.Setenv(BinEnv, bin)

	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: text\ndata: {\"text\":\"hello from the fake\"}\n\n")
		fmt.Fprint(w, "event: usage\ndata: {\"input\":3,\"output\":1}\n\n")
		fmt.Fprint(w, "event: finish\ndata: {\"reason\":\"stop\"}\n\n")
	}))
	t.Cleanup(model.Close)

	var stderr strings.Builder
	o := Options{Home: filepath.Join(t.TempDir(), "home"), Dir: t.TempDir(), Stderr: &stderr}
	// dsh makes the home private on its first start; the layer written into it
	// afterwards inherits that.
	first, err := Start(t.Context(), o)
	if err != nil {
		t.Fatalf("first start: %v\n%s", err, stderr.String())
	}
	if err := first.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	layer := filepath.Join(o.Home, "config", "profiles", "sdk", "layer.yml")
	if err := os.MkdirAll(filepath.Dir(layer), 0o700); err != nil {
		t.Fatal(err)
	}
	route := "- insert:\n    - id: route\n      name: llm.testkit-route\n      config:\n        id: fake\n        url: " + strconv.Quote(model.URL) + "\n"
	if err := os.WriteFile(layer, []byte(route), 0o600); err != nil {
		t.Fatal(err)
	}

	h, err := Start(t.Context(), o)
	if err != nil {
		t.Fatalf("start: %v\n%s", err, stderr.String())
	}
	s, err := h.Create(t.Context(), CreateOptions{Model: &protocol.ModelRoute{Provider: "fake", Model: "scripted"}})
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	r, err := s.Run(t.Context(), Input{Text: "say hello"})
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	if r.FinalResponse != "hello from the fake" {
		t.Fatalf("final response %q", r.FinalResponse)
	}
	start := time.Now()
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if e := h.Ending(); e != (Ending{Rung: RungShutdown, ExitCode: 0}) {
		t.Fatalf("ending %+v after %v, want shutdown with 0:\n%s", e, time.Since(start), stderr.String())
	}
	waitPidGone(t, h.Pid())
}

// architecture.md sec. 3.4 rule 5: the package imports the standard library,
// src/sdk/jsonrpc and src/sdk/protocol only.
//
//dsh:test DH-T-SDK-17
func TestImportsOnlyTheSDKCodecAndProtocol(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	allowed := map[string]bool{
		"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc":  true,
		"github.com/smartpcr/agent-plugin/src/sdk/protocol": true,
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range parsed.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			first, _, _ := strings.Cut(path, "/")
			if !allowed[path] && strings.Contains(first, ".") {
				t.Errorf("%s imports %s", f, path)
			}
		}
	}
}
