package listing_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute/listing"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

func writeTools(t *testing.T, doc string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tools.json")
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const probeTools = `[{"name":"probe","description":"Echo a text","inputSchema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}}]`

// client connects a JSON-RPC client to a server running Main on pipes, and
// returns it with the server's exit status channel and a function that ends
// the server's input.
func client(t *testing.T, args []string) (*jsonrpc.Conn, <-chan int, func()) {
	t.Helper()
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	status := make(chan int, 1)
	go func() {
		status <- listing.Main(args, sr, sw, io.Discard)
		_ = sw.Close()
	}()
	ctx, cancel := context.WithCancel(context.Background())
	conn := jsonrpc.Serve(ctx, cr, cw, jsonrpc.Options{})
	t.Cleanup(func() {
		_ = cw.Close()
		cancel()
		_ = cr.Close()
		<-conn.Done()
	})
	return conn, status, func() { _ = cw.Close() }
}

// TestListingServesToolsAndRefusesCalls: the listing-only server answers
// initialize (with the client's known protocol revision), ping and tools/list
// from the schema file; every tools/call is refused with call-refused and
// recorded by name only, never with its arguments; the server ends when its
// input ends.
//
//dsh:test DH-T-LLM-19
func TestListingServesToolsAndRefusesCalls(t *testing.T) {
	record := filepath.Join(t.TempDir(), "calls.log")
	conn, status, end := client(t, []string{"--tools", writeTools(t, probeTools), "--record", record})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools map[string]any `json:"tools"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := conn.Call(ctx, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}}, &init); err != nil {
		t.Fatal(err)
	}
	if init.ProtocolVersion != "2025-03-26" || init.ServerInfo.Name != listing.ServerName || init.Capabilities.Tools == nil {
		t.Fatalf("initialize = %+v", init)
	}
	if err := conn.Notify("notifications/initialized", nil); err != nil {
		t.Fatal(err)
	}
	var pong map[string]any
	if err := conn.Call(ctx, "ping", nil, &pong); err != nil || len(pong) != 0 {
		t.Fatalf("ping = %v, %v", pong, err)
	}
	var list struct {
		Tools []listing.Tool `json:"tools"`
	}
	if err := conn.Call(ctx, "tools/list", map[string]any{}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 1 || list.Tools[0].Name != "probe" || !strings.Contains(string(list.Tools[0].InputSchema), `"required":["text"]`) {
		t.Fatalf("tools/list = %+v", list)
	}
	n, err := listing.Calls(record)
	if err != nil || n != 0 {
		t.Fatalf("calls before any tools/call = %d, %v", n, err)
	}
	for range 2 {
		err := conn.Call(ctx, "tools/call", map[string]any{"name": "probe", "arguments": map[string]any{"text": "sk-ant-planted"}}, nil)
		if !jsonrpc.HasReason(err, listing.ReasonCallRefused) {
			t.Fatalf("tools/call = %v, want %s", err, listing.ReasonCallRefused)
		}
	}
	if err := conn.Call(ctx, "resources/list", nil, nil); !jsonrpc.HasReason(err, jsonrpc.ReasonMethodNotFound) {
		t.Fatalf("resources/list = %v, want method-not-found", err)
	}
	if n, err = listing.Calls(record); err != nil || n != 2 {
		t.Fatalf("calls = %d, %v; want 2", n, err)
	}
	raw, _ := os.ReadFile(record)
	if bytes.Contains(raw, []byte("planted")) || !bytes.Contains(raw, []byte(`"name":"probe"`)) {
		t.Fatalf("the record holds %q: the tool's name only, never its arguments", raw)
	}
	var unknown struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := conn.Call(ctx, "initialize", map[string]any{"protocolVersion": "1999-01-01"}, &unknown); err != nil || unknown.ProtocolVersion != listing.ProtocolVersion {
		t.Fatalf("initialize with an unknown revision = %+v, %v", unknown, err)
	}
	end()
	select {
	case code := <-status:
		if code != 0 {
			t.Fatalf("status = %d", code)
		}
	case <-ctx.Done():
		t.Fatal("the server did not end with its input")
	}
}

// TestListingRefusesBadInvocations: a missing flag, an extra argument and a
// schema file that is missing, not an array, or names a tool twice are status
// 2; ServeIfAsked serves only under the hidden mode word.
//
//dsh:test DH-T-LLM-19
func TestListingRefusesBadInvocations(t *testing.T) {
	record := filepath.Join(t.TempDir(), "calls.log")
	good := writeTools(t, probeTools)
	for name, args := range map[string][]string{
		"no tools":    {"--record", record},
		"no record":   {"--tools", good},
		"extra":       {"--tools", good, "--record", record, "more"},
		"bad flag":    {"--nope"},
		"missing":     {"--tools", filepath.Join(t.TempDir(), "none.json"), "--record", record},
		"not array":   {"--tools", writeTools(t, `{"name":"probe"}`), "--record", record},
		"twice":       {"--tools", writeTools(t, `[{"name":"a","inputSchema":{}},{"name":"a","inputSchema":{}}]`), "--record", record},
		"no schema":   {"--tools", writeTools(t, `[{"name":"a"}]`), "--record", record},
		"blank name":  {"--tools", writeTools(t, `[{"name":"","inputSchema":{}}]`), "--record", record},
		"empty input": nil,
	} {
		if code := listing.Main(args, strings.NewReader(""), io.Discard, io.Discard); code != 2 {
			t.Errorf("%s: status %d, want 2", name, code)
		}
	}
	if _, ok := listing.ServeIfAsked([]string{"version"}, nil, io.Discard, io.Discard); ok {
		t.Error("ServeIfAsked served without the mode word")
	}
	if _, ok := listing.ServeIfAsked(nil, nil, io.Discard, io.Discard); ok {
		t.Error("ServeIfAsked served with no arguments")
	}
	code, ok := listing.ServeIfAsked(listing.Args(good, record), strings.NewReader(""), io.Discard, io.Discard)
	if !ok || code != 0 {
		t.Errorf("ServeIfAsked(Args(...)) = %d, %v; want a served, clean end on empty input", code, ok)
	}
	if _, err := listing.Calls(t.TempDir()); err == nil {
		t.Error("Calls read a record that is a directory; an unreadable record must fail closed")
	}
	var tools []listing.Tool
	if err := json.Unmarshal([]byte(probeTools), &tools); err != nil || len(tools) != 1 {
		t.Fatal(err)
	}
}
