// Package listing is the listing-only MCP server of the signed-in client routes
// (DH-ADR-LLM-10, M1-D-PIPE-6 A). A client (Claude Code, the GitHub Copilot CLI)
// starts it from the attempt's MCP configuration as the harness executable in a
// hidden mode, Mode, and it serves the Model Context Protocol over its standard
// streams: initialize, ping and tools/list from the attempt's tool schema file.
// It runs nothing: every tools/call is refused with an error and recorded in
// the attempt's record file, so the adapter can turn the client's breach of
// "every call is denied" into a failed attempt. It holds no channel to the host.
//
// Only the standard library and the shared JSON-RPC codec are linked, so the
// harness binary serves it without the model runtime. A test binary serves it
// by calling ServeIfAsked from its TestMain, exactly as cmd/dsh does.
package listing

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"

	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// Mode is the hidden first word that selects the server in the harness
// executable: `dsh mcp-listing --tools <file> --record <file>`.
const Mode = "mcp-listing"

// ServerName is the server's name in the attempt's MCP configuration, so the
// clients name its tools mcp__dsh__<tool>.
const ServerName = "dsh"

// ProtocolVersion is the MCP revision answered when the client asks for one this
// server does not know.
const ProtocolVersion = "2025-06-18"

// known are the MCP revisions this server answers with the client's own.
var known = []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"}

// ReasonCallRefused is the JSON-RPC reason of a refused tools/call.
const ReasonCallRefused = "mcp-listing/call-refused"

// Args are the arguments after the executable that start the server on tools
// (the schema file) recording refused calls in record.
func Args(tools, record string) []string {
	return []string{Mode, "--tools", tools, "--record", record}
}

// Tool is one entry of the schema file and of tools/list.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ServeIfAsked runs the server when args (os.Args[1:]) start with Mode and
// returns its exit status and true; otherwise it returns false at once.
func ServeIfAsked(args []string, in io.Reader, out, errw io.Writer) (int, bool) {
	if len(args) == 0 || args[0] != Mode {
		return 0, false
	}
	return Main(args[1:], in, out, errw), true
}

// Main is the mode's entry point: it parses --tools and --record and serves
// until in ends. A bad flag or an unreadable schema file is status 2.
func Main(args []string, in io.Reader, out, errw io.Writer) int {
	fs := flag.NewFlagSet(Mode, flag.ContinueOnError)
	fs.SetOutput(errw)
	tools := fs.String("tools", "", "the tool schema file")
	record := fs.String("record", "", "the file each refused tools/call is recorded in")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *tools == "" || *record == "" || fs.NArg() != 0 {
		fmt.Fprintln(errw, "mcp-listing: --tools and --record are required, and nothing else")
		return 2
	}
	list, err := Load(*tools)
	if err != nil {
		fmt.Fprintln(errw, "mcp-listing:", err)
		return 2
	}
	conn := Serve(context.Background(), list, *record, in, out)
	<-conn.Done()
	return 0
}

// Load reads a schema file: a JSON array of tools, each named once.
func Load(path string) ([]Tool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("the tool schema file cannot be read")
	}
	var list []Tool
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, errors.New("the tool schema file is not a JSON array of tools")
	}
	seen := map[string]bool{}
	for _, t := range list {
		if t.Name == "" || seen[t.Name] || !json.Valid(t.InputSchema) {
			return nil, fmt.Errorf("the tool schema file has a blank, repeated or schemaless tool %q", t.Name)
		}
		seen[t.Name] = true
	}
	return list, nil
}

// Serve answers the client on in and out until in ends.
func Serve(ctx context.Context, tools []Tool, record string, in io.Reader, out io.Writer) *jsonrpc.Conn {
	tools = slices.Clone(tools)
	if tools == nil {
		tools = []Tool{}
	}
	var mu sync.Mutex
	return jsonrpc.Serve(ctx, in, out, jsonrpc.Options{
		Methods: map[string]jsonrpc.Handler{
			"initialize": func(_ context.Context, params json.RawMessage) (any, error) {
				var p struct {
					ProtocolVersion string `json:"protocolVersion"`
				}
				_ = json.Unmarshal(params, &p)
				v := ProtocolVersion
				if slices.Contains(known, p.ProtocolVersion) {
					v = p.ProtocolVersion
				}
				return map[string]any{
					"protocolVersion": v,
					"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
					"serverInfo":      map[string]any{"name": ServerName, "version": "1"},
					"instructions":    "These tools are run by the dsh harness, not by this client.",
				}, nil
			},
			"ping":       func(context.Context, json.RawMessage) (any, error) { return map[string]any{}, nil },
			"tools/list": func(context.Context, json.RawMessage) (any, error) { return map[string]any{"tools": tools}, nil },
			"tools/call": func(_ context.Context, params json.RawMessage) (any, error) {
				var p struct {
					Name string `json:"name"`
				}
				_ = json.Unmarshal(params, &p)
				mu.Lock()
				recordCall(record, p.Name)
				mu.Unlock()
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeApplication, Reason: ReasonCallRefused,
					Message: "dsh lists its tools here and runs none: the harness runs every tool call itself"}
			},
		},
	})
}

// recordCall appends one line naming the tool (never its arguments) to record.
// A record that cannot be written is still a refusal; the adapter sees the
// client's own tool result for the call.
func recordCall(record, name string) {
	f, err := os.OpenFile(record, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	b, _ := json.Marshal(map[string]string{"method": "tools/call", "name": name})
	_, _ = f.Write(append(b, '\n'))
	_ = f.Close()
}

// Calls is the number of tools/call requests recorded in record: 0 when the
// file does not exist.
func Calls(record string) (int, error) {
	raw, err := os.ReadFile(record)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, errors.New("the listing server's record cannot be read")
	}
	n := 0
	for _, b := range raw {
		if b == '\n' {
			n++
		}
	}
	return n, nil
}
