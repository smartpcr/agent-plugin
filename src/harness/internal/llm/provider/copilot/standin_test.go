package copilot_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute/listing"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/copilot"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// The stand-in's settings, passed through the route's Config.Env. None of the
// names is one the route removes.
const (
	roleEnv    = "STANDIN_ROLE"    // "copilot" selects the stand-in
	recordEnv  = "STANDIN_RECORD"  // the directory it records what it saw in
	fixtureEnv = "STANDIN_FIXTURE" // the synthetic lines it replays
	versionEnv = "STANDIN_VERSION" // the first line of its --version output
	modeEnv    = "STANDIN_MODE"    // "hang": stall after the lines; "call": ask the listing server to run a tool
	stderrEnv  = "STANDIN_STDERR"  // its standard error
	exitEnv    = "STANDIN_EXIT"    // its exit status
)

// watched are the variables the stand-in reports present or absent.
var watched = []string{
	"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
	"COPILOT_PROVIDER_BASE_URL", "COPILOT_PROVIDER_TYPE", "COPILOT_PROVIDER_EXTRA", "COPILOT_PROVIDERS_CONFIG", "COPILOT_OFFLINE",
	"COPILOT_MODEL", "COPILOT_ALLOW_ALL", "COPILOT_ASSISTED_APPROVAL", "COPILOT_CLI_VERSION", "COPILOT_CLI_DIST_DIR", "DSH_HOME",
	"COPILOT_AUTO_UPDATE", "COPILOT_TELEMETRY_DISABLED", "COPILOT_HOME", "GH_HOST",
}

// TestMain is the test binary and, re-executed, the listing-only MCP server
// (as cmd/dsh serves it) or the stand-in `copilot` (EN-4). The real client is
// never run.
func TestMain(m *testing.M) {
	if code, ok := listing.ServeIfAsked(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); ok {
		os.Exit(code)
	}
	if os.Getenv(roleEnv) == "copilot" {
		os.Exit(standIn())
	}
	subprocesstest.Main(m)
}

// seen is what the stand-in recorded of one run.
type seen struct {
	Args    []string          `json:"args"`
	Stdin   string            `json:"stdin"`
	Cwd     string            `json:"cwd"`
	Files   []string          `json:"files"`
	Env     map[string]string `json:"env"` // each watched name that is set, with its value
	Absent  []string          `json:"absent"`
	Tools   []string          `json:"tools"`
	CallErr string            `json:"call_err"`
	Client  int               `json:"client"`
	Server  int               `json:"server"`
}

func record(name string, v any) {
	b, _ := json.Marshal(v)
	_ = os.WriteFile(filepath.Join(os.Getenv(recordEnv), name), b, 0o600)
}

// standIn plays `copilot`: the probe prints STANDIN_VERSION and the update
// hint; a run records its argument list, standard input, working directory,
// environment and the listing server's tools, then replays the fixture with
// the run's session id filled in.
func standIn() int {
	args := os.Args[1:]
	if slices.Equal(args, copilot.ProbeArgs) {
		f, _ := os.OpenFile(filepath.Join(os.Getenv(recordEnv), "probes"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		_, _ = f.WriteString("probe\n")
		_ = f.Close()
		fmt.Println(os.Getenv(versionEnv))
		fmt.Println("Run 'copilot update' to check for updates.")
		return 0
	}
	in, _ := io.ReadAll(os.Stdin)
	cwd, _ := os.Getwd()
	var files []string
	entries, _ := os.ReadDir(cwd)
	for _, e := range entries {
		files = append(files, e.Name())
	}
	s := seen{Args: args, Stdin: string(in), Cwd: cwd, Files: files, Env: map[string]string{}, Client: os.Getpid()}
	for _, n := range watched {
		if v, ok := os.LookupEnv(n); ok {
			s.Env[n] = v
		} else {
			s.Absent = append(s.Absent, n)
		}
	}
	// The listing server, started as the real client starts it: from the MCP
	// configuration file named after the "@".
	var server *exec.Cmd
	var toServer io.WriteCloser
	var fromServer *bufio.Reader
	if i := slices.Index(args, "--additional-mcp-config"); i >= 0 && i+1 < len(args) {
		server, toServer, fromServer = startServer(strings.TrimPrefix(args[i+1], "@"))
		if server != nil {
			s.Server = server.Process.Pid
			call := func(id int, method string, params any) map[string]any {
				b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
				_, _ = toServer.Write(append(b, '\n'))
				line, _ := fromServer.ReadBytes('\n')
				var resp map[string]any
				_ = json.Unmarshal(line, &resp)
				return resp
			}
			call(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "standin"}})
			_, _ = toServer.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"))
			list := call(2, "tools/list", map[string]any{})
			if res, ok := list["result"].(map[string]any); ok {
				ts, _ := res["tools"].([]any)
				for _, t := range ts {
					if m, ok := t.(map[string]any); ok {
						name, _ := m["name"].(string)
						s.Tools = append(s.Tools, name)
					}
				}
			}
			if os.Getenv(modeEnv) == "call" {
				resp := call(3, "tools/call", map[string]any{"name": "probe", "arguments": map[string]any{"text": "ping"}})
				if e, ok := resp["error"].(map[string]any); ok {
					data, _ := e["data"].(map[string]any)
					s.CallErr, _ = data["code"].(string)
				}
			}
		}
	}
	record("seen.json", s)
	sid := ""
	if i := slices.Index(args, "--session-id"); i >= 0 && i+1 < len(args) {
		sid = args[i+1]
	}
	raw, _ := os.ReadFile(os.Getenv(fixtureEnv))
	_, _ = os.Stdout.WriteString(strings.ReplaceAll(string(raw), "{{session_id}}", sid))
	fmt.Fprint(os.Stderr, os.Getenv(stderrEnv))
	if os.Getenv(modeEnv) == "hang" && fromServer != nil {
		// Stall like a client waiting on a slow model or about to act on a
		// denial: the server never writes unasked, so this read lasts until the
		// tree is stopped.
		_, _ = fromServer.ReadBytes('\n')
	}
	if server != nil {
		_ = toServer.Close()
		_ = server.Wait()
	}
	code, _ := strconv.Atoi(os.Getenv(exitEnv))
	return code
}

// startServer starts the listing server named in the MCP configuration path.
func startServer(path string) (*exec.Cmd, io.WriteCloser, *bufio.Reader) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil
	}
	var cfg struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			Tools   []string          `json:"tools"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return nil, nil, nil
	}
	srv, ok := cfg.MCPServers["dsh"]
	if !ok || srv.Type != "local" || !slices.Equal(srv.Tools, []string{"*"}) {
		return nil, nil, nil
	}
	cmd := exec.Command(srv.Command, srv.Args...)
	cmd.Env = os.Environ()
	for k, v := range srv.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if cmd.Start() != nil {
		return nil, nil, nil
	}
	return cmd, in, bufio.NewReader(out)
}
