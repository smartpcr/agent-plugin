// Command claudestandin is an ephemeral stand-in of the Claude Code client
// (`claude`) for built-binary tests of the claude-code route: it plays the
// client's print mode as the route starts it, so the real route adapter runs in
// dsh with no sign-in.
//
// `--version` prints Version. A run starts the listing-only MCP server named in
// --mcp-config as the real client does, lists its tools, then POSTs the run
// (arguments, standard input, listed tools) to the URL in URLEnv and copies the
// stream-json lines the answer carries to standard output as they arrive, with
// {{session_id}}, {{cwd}}, {{version}}, "{{tools}}" and "{{mcp_servers}}"
// filled in. An answer the test holds open holds the run open, until the route
// stops the process tree.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// URLEnv names the variable carrying the loopback URL the stand-in asks.
const URLEnv = "CLAUDESTANDIN_URL"

// Version is the stand-in's --version line.
const Version = "2.1.312 (Claude Code)"

// Run is what the stand-in POSTs for one run.
type Run struct {
	Args  []string `json:"args"`
	Stdin string   `json:"stdin"`
	Tools []string `json:"tools"`
}

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if slices.Equal(args, []string{"--version"}) {
		fmt.Println(Version)
		return 0
	}
	in, _ := io.ReadAll(os.Stdin)
	cwd, _ := os.Getwd()
	tools, mcpServers := []string{}, "[]"
	if i := slices.Index(args, "--mcp-config"); i >= 0 && i+1 < len(args) {
		srv, err := startServer(args[i+1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "claudestandin: listing server:", err)
			return 1
		}
		defer srv.close()
		names, err := srv.list()
		if err != nil {
			fmt.Fprintln(os.Stderr, "claudestandin: listing server:", err)
			return 1
		}
		for _, n := range names {
			tools = append(tools, "mcp__dsh__"+n)
		}
		mcpServers = `[{"name":"dsh","status":"connected"}]`
	}
	sid := ""
	if i := slices.Index(args, "--session-id"); i >= 0 && i+1 < len(args) {
		sid = args[i+1]
	}
	body, _ := json.Marshal(Run{Args: args, Stdin: string(in), Tools: tools})
	resp, err := http.Post(os.Getenv(URLEnv), "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudestandin:", err)
		return 1
	}
	defer resp.Body.Close()
	toolsJSON, _ := json.Marshal(tools)
	cwdJSON, _ := json.Marshal(cwd)
	version, _, _ := strings.Cut(Version, " ")
	fill := strings.NewReplacer(`"{{tools}}"`, string(toolsJSON), `"{{mcp_servers}}"`, mcpServers, `"{{cwd}}"`, string(cwdJSON),
		"{{session_id}}", sid, "{{version}}", version)
	r := bufio.NewReader(resp.Body)
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			_, _ = os.Stdout.WriteString(fill.Replace(line))
		}
		if err != nil {
			return 0
		}
	}
}

type server struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

// startServer starts the "dsh" stdio server of the MCP configuration at path.
func startServer(path string) (*server, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	srv, ok := cfg.MCPServers["dsh"]
	if !ok || srv.Type != "stdio" {
		return nil, fmt.Errorf("no stdio server dsh in %s", path)
	}
	cmd := exec.Command(srv.Command, srv.Args...)
	cmd.Env = os.Environ()
	for k, v := range srv.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &server{cmd: cmd, in: in, out: bufio.NewReader(out)}, nil
}

func (s *server) call(id int, method string, params any) (map[string]any, error) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if _, err := s.in.Write(append(b, '\n')); err != nil {
		return nil, err
	}
	line, err := s.out.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	if e, ok := resp["error"]; ok {
		return nil, fmt.Errorf("%s: %v", method, e)
	}
	return resp, nil
}

// list initialises the server and returns its tools' names.
func (s *server) list() ([]string, error) {
	if _, err := s.call(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "claudestandin"}}); err != nil {
		return nil, err
	}
	if _, err := s.in.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")); err != nil {
		return nil, err
	}
	resp, err := s.call(2, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	res, _ := resp["result"].(map[string]any)
	ts, _ := res["tools"].([]any)
	var names []string
	for _, t := range ts {
		if m, ok := t.(map[string]any); ok {
			if n, _ := m["name"].(string); n != "" {
				names = append(names, n)
			}
		}
	}
	return names, nil
}

func (s *server) close() {
	_ = s.in.Close()
	_ = s.cmd.Wait()
}
