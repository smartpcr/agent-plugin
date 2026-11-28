//go:build e2e

package e2e

// Step definitions of
// dh_s_18_acceptance_on_real_operating_systems_file_fence_and_working_directory_on_real_systems.feature
// (DH-S-18 Stage 8.6: DH-E-FS-1, DH-E-FS-2, DH-E-VIEW-5; TC-21s3, TC-21s4).
//
// Each scenario runs the product's own scenario test, unmodified, built with
// -tags scenario for the variant and run there: this host when its OS matches,
// else the default WSL distribution through wsl.exe (a Windows host's Linux
// variant). The test's inputs are the operator's when all of them are set (the
// provided instance; DSH_S18_<LINUX|WINDOWS>_<NAME without DSH_> overrides one per
// variant), else an ephemeral instance this file stands up. The verdict is the
// row the test writes to DSH_SCENARIO_REPORT, read back, plus the log lines that
// show each clause ran.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

const (
	s86Pkg          = "./internal/testkit/scenarios"
	s86StandInModel = "e2e-stand-in"
	s86RunWithin    = 24 * time.Hour
)

var s86OuterT *testing.T

// ---- variants ----

// s86Variant is a machine a scenario runs on.
type s86Variant struct {
	goos, goarch string
	wsl          bool // the Linux variant reached through wsl.exe from this Windows host
}

func (v *s86Variant) String() string {
	if v.wsl {
		return "linux (WSL)"
	}
	return v.goos
}

func s86Reach(goos string) (*s86Variant, error) {
	if runtime.GOOS == goos {
		return &s86Variant{goos: goos, goarch: runtime.GOARCH}, nil
	}
	if goos == "linux" && runtime.GOOS == "windows" {
		v := &s86Variant{goos: "linux", wsl: true}
		out, code, err := v.run(context.Background(), "", nil, nil, "uname", "-m")
		if err != nil || code != 0 {
			return nil, fmt.Errorf("the Linux variant is not reachable through wsl.exe: exit %d %v %s", code, err, out)
		}
		switch strings.TrimSpace(out) {
		case "x86_64":
			v.goarch = "amd64"
		case "aarch64":
			v.goarch = "arm64"
		default:
			return nil, fmt.Errorf("the Linux variant runs on %q, no release target", strings.TrimSpace(out))
		}
		return v, nil
	}
	return nil, fmt.Errorf("the %s variant is not reachable from this %s host", goos, runtime.GOOS)
}

// s86Env is this process's environment without the Go build variables, so a
// scenario test never takes them as a request for another dsh build.
func s86Env(set ...string) []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(strings.ToUpper(kv), "=")
		return slices.Contains([]string{"GOWORK", "GOFLAGS", "GOOS", "GOARCH", "CGO_ENABLED"}, k)
	})
	return append(env, set...)
}

// run runs argv on the variant in dir (a variant path; "" for the default) with
// env added and stdin, returning the combined output and the exit code.
func (v *s86Variant) run(ctx context.Context, dir string, env []string, stdin []byte, argv ...string) (string, int, error) {
	var cmd *exec.Cmd
	if v.wsl {
		args := []string{}
		if dir != "" {
			args = append(args, "--cd", dir)
		}
		args = append(append(append(args, "-e", "env"), env...), argv...)
		cmd = exec.CommandContext(ctx, "wsl.exe", args...)
		cmd.Env = s86Env()
	} else {
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir, cmd.Env = dir, s86Env(env...)
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), exit.ExitCode(), nil
	}
	return string(out), 0, err
}

func (v *s86Variant) must(argv ...string) (string, error) {
	out, code, err := v.run(context.Background(), "", nil, nil, argv...)
	if err != nil || code != 0 {
		return out, fmt.Errorf("%v on %s: exit %d %v\n%s", argv, v, code, err, out)
	}
	return strings.TrimSpace(out), nil
}

// path is a host path as the variant names it.
func (v *s86Variant) path(p string) (string, error) {
	if !v.wsl {
		return p, nil
	}
	return v.must("wslpath", "-a", filepath.ToSlash(p))
}

func (v *s86Variant) exe(p string) string {
	if v.goos == "windows" {
		return p + ".exe"
	}
	return p
}

// tempDir is a scratch directory on the variant's own filesystem, removed at the end.
func (v *s86Variant) tempDir(prefix string) (string, error) {
	if !v.wsl {
		d, err := os.MkdirTemp("", prefix)
		if err == nil {
			s86OuterT.Cleanup(func() { _ = os.RemoveAll(d) })
		}
		return d, err
	}
	d, err := v.must("mktemp", "-d", "/tmp/"+prefix+"XXXXXX")
	if err == nil {
		s86OuterT.Cleanup(func() { _, _ = v.must("rm", "-rf", d) })
	}
	return d, err
}

func (v *s86Variant) join(dir string, elem ...string) string {
	if v.goos == "windows" {
		return filepath.Join(append([]string{dir}, elem...)...)
	}
	return strings.Join(append([]string{dir}, elem...), "/")
}

func (v *s86Variant) writeFile(p string, data []byte) error {
	if !v.wsl {
		return os.WriteFile(p, data, 0o600)
	}
	out, code, err := v.run(context.Background(), "", nil, data, "sh", "-c", `cat > "$1"`, "sh", p)
	if err != nil || code != 0 {
		return fmt.Errorf("writing %s on %s: exit %d %v %s", p, v, code, err, out)
	}
	return nil
}

func (v *s86Variant) readFile(p string) ([]byte, error) {
	if !v.wsl {
		return os.ReadFile(p)
	}
	out, err := v.must("cat", p)
	return []byte(out), err
}

func (v *s86Variant) sha256(p string) (string, error) {
	if !v.wsl {
		b, err := os.ReadFile(p)
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:]), err
	}
	out, err := v.must("sha256sum", p)
	if err != nil {
		return "", err
	}
	return strings.Fields(out)[0], nil
}

func (v *s86Variant) has(program string) bool {
	if !v.wsl {
		_, err := exec.LookPath(program)
		return err == nil
	}
	_, err := v.must("sh", "-c", `command -v "$1"`, "sh", program)
	return err == nil
}

// shell runs a command line as the scenario tests' shell helper does.
func (v *s86Variant) shell(line string) (string, error) {
	if v.goos == "windows" {
		return v.must("cmd", "/C", line)
	}
	return v.must("sh", "-c", line)
}

// fsType names the filesystem holding dir: "ext4" (the ext2/3/4 magic), "NTFS", ...
func (v *s86Variant) fsType(dir string) (string, error) {
	if v.goos == "windows" {
		out, err := v.must("powershell", "-NoProfile", "-Command",
			"[System.IO.DriveInfo]::new([System.IO.Path]::GetPathRoot('"+strings.ReplaceAll(dir, "'", "''")+"')).DriveFormat")
		return out, err
	}
	out, err := v.must("stat", "-f", "-c", "%T", dir)
	if out == "ext2/ext3" {
		out = "ext4"
	}
	return out, err
}

// ---- builds ----

var (
	s86BuildMu sync.Mutex
	s86Built   = map[string]string{}
)

func s86Module() (string, error) {
	dir, err := filepath.Abs(".")
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "cmd", "dsh")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("the harness module (cmd/dsh, go.mod) is not above the working directory")
		}
		dir = parent
	}
}

// build builds what (the scenario test binary, the testhook dsh or the stand-in
// client) for v once per suite and returns its path as v names it.
func (v *s86Variant) build(what string) (string, error) {
	s86BuildMu.Lock()
	defer s86BuildMu.Unlock()
	key := v.goos + "/" + v.goarch + "/" + what
	if p, ok := s86Built[key]; ok {
		return p, nil
	}
	mod, err := s86Module()
	if err != nil {
		return "", err
	}
	out := v.exe(filepath.Join(s86OuterT.TempDir(), strings.ReplaceAll(key, "/", "-")))
	args := map[string][]string{
		"scenarios":     {"test", "-c", "-trimpath", "-tags", "scenario", "-o", out, s86Pkg},
		"dsh":           {"build", "-trimpath", "-tags", "testhook", "-o", out, "./cmd/dsh"},
		"claudestandin": {"build", "-trimpath", "-o", out, "./internal/testkit/claudestandin"},
	}[what]
	cmd := exec.Command("go", args...)
	cmd.Dir, cmd.Env = mod, append(s86Env(), "GOWORK=off", "GOOS="+v.goos, "GOARCH="+v.goarch, "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go %v for %s: %v\n%s", args, v, err, b)
	}
	p, err := v.path(out)
	if err == nil {
		s86Built[key] = p
	}
	return p, err
}

// ---- the ephemeral model behind the stand-in client ----

// s86Model is the loopback model the stand-in Claude Code client, started by the
// testhook dsh's real claude-code route, posts each client run to. It plays the
// FS-2 task (grep, line-range read, edit, multi-file patch, stop) and the VIEW-5
// prompts, deciding from the conversation dsh rendered.
type s86Model struct {
	srv  *httptest.Server
	mu   sync.Mutex
	runs int
	ids  int
}

var (
	s86FS2Task    = regexp.MustCompile(`FS-2 task \d+: in (\S+), search`)
	s86View5Write = regexp.MustCompile(`then use the write tool on (\S+) with the content original`)
)

func s86JSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func s86Event(ev string) string {
	return `{"type":"stream_event","event":` + ev + `,"session_id":"{{session_id}}","parent_tool_use_id":null}`
}

func s86Head(text string) []string {
	return []string{
		`{"type":"system","subtype":"init","cwd":"{{cwd}}","session_id":"{{session_id}}","tools":"{{tools}}","mcp_servers":"{{mcp_servers}}","model":"claude-sonnet-4-5","permissionMode":"dontAsk","slash_commands":[],"apiKeySource":"none","claude_code_version":"{{version}}","output_style":"default","agents":[],"skills":[],"plugins":[],"uuid":"00000000-0000-4000-8000-000000000086"}`,
		s86Event(`{"type":"message_start","message":{"id":"msg_s86","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1}}}`),
		s86Event(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		s86Event(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":` + s86JSON(text) + `}}`),
		s86Event(`{"type":"content_block_stop","index":0}`),
	}
}

// s86Call is one client run calling the harness tool, returned unexecuted as the
// real client does: dsh runs it and sends the result in the next run.
func s86Call(id, tool string, input map[string]any) string {
	name := "mcp__dsh__" + tool
	use := s86JSON(map[string]any{"type": "tool_use", "id": id, "name": name, "input": input})
	l := append(s86Head("Calling "+tool+"."),
		s86Event(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"`+id+`","name":"`+name+`","input":{}}}`),
		s86Event(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":`+s86JSON(s86JSON(input))+`}}`),
		s86Event(`{"type":"content_block_stop","index":1}`),
		`{"type":"assistant","message":{"id":"msg_s86","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"Calling `+tool+`."},`+use+`],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"output_tokens":1}},"parent_tool_use_id":null,"session_id":"{{session_id}}"}`,
		s86Event(`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":14}}`),
		s86Event(`{"type":"message_stop"}`),
		`{"type":"system","subtype":"permission_denied","tool_name":"`+name+`","tool_use_id":"`+id+`","session_id":"{{session_id}}"}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"`+id+`","content":"Permission to use `+name+` has been denied.","is_error":true}]},"parent_tool_use_id":null,"session_id":"{{session_id}}"}`,
		`{"type":"result","subtype":"error_max_turns","is_error":true,"duration_ms":10,"duration_api_ms":9,"num_turns":1,"session_id":"{{session_id}}","total_cost_usd":0,"usage":{"input_tokens":12,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":14},"modelUsage":{},"permission_denials":[{"tool_name":"`+name+`","tool_use_id":"`+id+`","tool_input":`+s86JSON(input)+`}],"stop_reason":"tool_use"}`)
	return strings.Join(l, "\n") + "\n"
}

func s86Close(text string) string {
	l := append(s86Head(text),
		`{"type":"assistant","message":{"id":"msg_s86","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":`+s86JSON(text)+`}],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":20,"output_tokens":1}},"parent_tool_use_id":null,"session_id":"{{session_id}}"}`,
		s86Event(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":4}}`),
		s86Event(`{"type":"message_stop"}`),
		`{"type":"result","subtype":"success","is_error":false,"duration_ms":10,"duration_api_ms":9,"num_turns":1,"session_id":"{{session_id}}","total_cost_usd":0,"usage":{"input_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":4},"modelUsage":{},"permission_denials":[],"result":`+s86JSON(text)+`,"stop_reason":"end_turn"}`)
	return strings.Join(l, "\n") + "\n"
}

func newS86Model() *s86Model {
	m := &s86Model{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var run struct {
			Stdin string `json:"stdin"`
		}
		_ = json.NewDecoder(r.Body).Decode(&run)
		m.mu.Lock()
		m.runs++
		m.ids++
		id := fmt.Sprintf("s86c%d", m.ids)
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, s86Answer(id, run.Stdin))
	}))
	return m
}

func (m *s86Model) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runs
}

// s86Answer is the model's next move: the prompt is the latest one the
// conversation holds, the step the number of tool results after it.
func s86Answer(id, conv string) string {
	prompts := []string{"FS-2 task ", "Change this session's working directory", "Use the write tool with the relative path", "Reply with the word ready."}
	at, prompt := -1, ""
	for _, p := range prompts {
		if i := strings.LastIndex(conv, p); i > at {
			at, prompt = i, p
		}
	}
	if at < 0 {
		return s86Close("Nothing to do.")
	}
	rest := conv[at:]
	step := strings.Count(rest, "<tool_result ")
	switch prompt {
	case "FS-2 task ":
		m := s86FS2Task.FindStringSubmatch(rest)
		if m == nil {
			return s86Close("The task names no checkout.")
		}
		co := m[1]
		switch step {
		case 0:
			return s86Call(id, "grep", map[string]any{"pattern": `be(ta|TA)\b`, "path": co})
		case 1:
			return s86Call(id, "read", map[string]any{"file_path": co + "/notes.txt", "offset": 2, "limit": 1})
		case 2:
			return s86Call(id, "edit", map[string]any{"file_path": co + "/notes.txt", "old_string": "beta", "new_string": "BETA"})
		case 3:
			patch := "*** Begin Patch\n*** Update File: " + co + "/notes.txt\n@@\n-gamma\n+GAMMA\n*** Update File: " + co + "/extra.txt\n@@\n-one\n+ONE\n*** End Patch\n"
			return s86Call(id, "apply_patch", map[string]any{"input": patch})
		}
		return s86Close("Done.")
	case "Change this session's working directory":
		return s86Close("None of my tools changes this session's working directory.")
	case "Use the write tool with the relative path":
		switch step {
		case 0:
			return s86Call(id, "write", map[string]any{"file_path": "view5-relative.txt", "content": "relative"})
		case 1:
			if m := s86View5Write.FindStringSubmatch(rest); m != nil {
				return s86Call(id, "write", map[string]any{"file_path": m[1], "content": "original"})
			}
		}
		return s86Close("Done.")
	}
	return s86Close("ready")
}

// ---- state ----

// s86Leg is one run of a scenario test on one variant.
type s86Leg struct {
	v        *s86Variant
	fs       string
	provided bool
	inputs   map[string]string // the run's DSH_* inputs
	model    *s86Model
	log      string
	row      map[string]any
	ran      error
}

type s86State struct {
	id, test string
	legs     []*s86Leg
	status   string
}

// s86Inputs are what each scenario test reads (their doc comments), with DSH_BIN.
var s86Inputs = map[string][]string{
	"DH-E-FS-1":   {"DSH_BIN", "DSH_SCENARIO_FS1_DIRS", "DSH_SCENARIO_FS1_SHARE", "DSH_SCENARIO_FS1_LAYER", "DSH_SCENARIO_FS1_ROUTE", "DSH_SCENARIO_FS1_MODEL"},
	"DH-E-FS-2":   {"DSH_BIN", "DSH_SCENARIO_FS2_TASKS", "DSH_SCENARIO_FS_CHECKOUTS", "DSH_SCENARIO_FS2_CHILDREN", "DSH_SCENARIO_FS2_LAYER", "DSH_SCENARIO_FS2_SHARD", "DSH_SCENARIO_CLAUDE_CODE_MODEL", "DSH_SCENARIO_FS3_EDITOR"},
	"DH-E-VIEW-5": {"DSH_BIN", "DSH_SCENARIO_VIEW5_LAYER", "DSH_SCENARIO_VIEW5_MOVE", "DSH_SCENARIO_CLAUDE_CODE_MODEL"},
}

func s86Input(v *s86Variant, name string) string {
	if s := strings.TrimSpace(os.Getenv("DSH_S18_" + strings.ToUpper(v.goos) + "_" + strings.TrimPrefix(name, "DSH_"))); s != "" {
		return s
	}
	return strings.TrimSpace(os.Getenv(name))
}

// leg reaches the variant, checks its filesystem and takes the provided inputs
// when every one is set; partly set inputs fail.
func (s *s86State) leg(goos, fs string) (*s86Leg, error) {
	v, err := s86Reach(goos)
	if err != nil {
		return nil, err
	}
	l := &s86Leg{v: v, fs: fs, inputs: map[string]string{}}
	var set, missing []string
	for _, n := range s86Inputs[s.id] {
		if val := s86Input(v, n); val != "" {
			l.inputs[n] = val
			set = append(set, n)
		} else {
			missing = append(missing, n)
		}
	}
	if len(set) > 0 && len(missing) > 0 {
		return nil, fmt.Errorf("%s on %s: the provided inputs are partly set: %s unset while %s are set", s.id, v, strings.Join(missing, ", "), strings.Join(set, ", "))
	}
	l.provided = len(missing) == 0
	if !l.provided {
		l.inputs = map[string]string{}
		if l.inputs["DSH_BIN"], err = v.build("dsh"); err != nil {
			return nil, err
		}
	}
	s.legs = append(s.legs, l)
	lane := map[bool]string{true: "provided instance", false: "ephemeral instance (testhook dsh)"}[l.provided]
	s86OuterT.Logf("s86 %s on %s: %s", s.id, v, lane)
	return l, nil
}

// scratch is a directory on the variant's filesystem under test.
func (l *s86Leg) scratch(prefix string) (string, error) {
	d, err := l.v.tempDir(prefix)
	if err != nil {
		return "", err
	}
	got, err := l.v.fsType(d)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(got, l.fs) {
		return "", fmt.Errorf("%s on %s is on %s, not %s", d, l.v, got, l.fs)
	}
	return d, nil
}

func (l *s86Leg) writeLayer(dir, text string) (string, error) {
	p := l.v.join(dir, "layer.yml")
	return p, l.v.writeFile(p, []byte(text))
}

func s86Quote(s string) string { return strconv.Quote(s) }

// fileLayer is the configuration layer of the ephemeral instance: the six file
// tools over the fenced seam in workspace-write, with extra write roots, and the
// real claude-code route over the stand-in client when client is set.
func (l *s86Leg) fileLayer(client, clientTemp string, extra ...string) string {
	var b strings.Builder
	b.WriteString("- insert:\n")
	if client != "" {
		b.WriteString("    - id: route\n      name: llm.testkit-claude-code\n      config:\n        program: " + s86Quote(client) +
			"\n        env: [" + s86Quote("CLAUDESTANDIN_URL="+l.model.srv.URL) + "]\n        tempDir: " + s86Quote(clientTemp) + "\n")
	}
	b.WriteString("    - id: fs.local\n      name: fs.local\n")
	b.WriteString("    - id: session.workdir\n      name: session.workdir\n")
	b.WriteString("    - id: sandbox.policy\n      name: sandbox.policy\n      config:\n        defaultMode: workspace-write\n")
	if len(extra) > 0 {
		b.WriteString("        extraWriteRoots: [" + strings.Join(extra, ", ") + "]\n")
	}
	b.WriteString("    - id: fs.ops\n      name: fs.ops\n    - id: fs.tools\n      name: fs.tools\n")
	return b.String()
}

// standIn starts the loopback model and returns the stand-in client's path and
// temporary directory on the variant.
func (l *s86Leg) standIn(dir string) (string, string, error) {
	client, err := l.v.build("claudestandin")
	if err != nil {
		return "", "", err
	}
	l.model = newS86Model()
	s86OuterT.Cleanup(l.model.srv.Close)
	return client, l.v.join(dir, "client"), nil
}

// ---- provisioning ----

func (s *s86State) givenFS1Variants(fsLinux, fsWindows string) error {
	s.id, s.test = "DH-E-FS-1", "TestFenceHoldsUnderALinkSwap"
	for _, want := range [][2]string{{"linux", fsLinux}, {"windows", fsWindows}} {
		l, err := s.leg(want[0], want[1])
		if err != nil {
			return err
		}
		if l.provided {
			continue
		}
		dir, err := l.scratch("dsh-s86-fs1-")
		if err != nil {
			return err
		}
		l.inputs["DSH_SCENARIO_FS1_DIRS"] = dir
		if l.v.goos == "windows" {
			// The loopback SMB share of this host: the same NTFS directory
			// reached through the SMB client (\\localhost\<drive>$\...).
			share, err := l.scratch("dsh-s86-fs1-share-")
			if err != nil {
				return err
			}
			unc := `\\localhost\` + share[:1] + `$` + share[2:]
			if _, err := os.Stat(unc); err != nil {
				return fmt.Errorf("the loopback SMB share %s is not reachable: %v", unc, err)
			}
			l.inputs["DSH_SCENARIO_FS1_SHARE"] = unc
		}
		// No catalogue item registers a route on an Anthropic Messages endpoint
		// (the scripted endpoint's dialect), so the layer names none: the
		// scenario test's session on the route fails, naming it.
		l.inputs["DSH_SCENARIO_FS1_LAYER"] = l.fileLayer("", "", "{extra}")
		l.inputs["DSH_SCENARIO_FS1_ROUTE"] = "fs1-scripted"
		l.inputs["DSH_SCENARIO_FS1_MODEL"] = "scripted"
	}
	return nil
}

func s86Children(goos string) string {
	if goos == "windows" {
		return `powershell -NoProfile -Command "$all=Get-CimInstance Win32_Process; $k=@({pid}); while($k.Count){ $c=@($all | Where-Object { $k -contains $_.ParentProcessId }); $c | ForEach-Object { $_.Name }; $k=@($c | ForEach-Object { $_.ProcessId }) }"`
	}
	return `ps -e -o pid= -o ppid= -o comm= | awk -v r={pid} 'BEGIN{k[r]=1} {p[NR]=$1; q[NR]=$2; c[NR]=$3} END{do{n=0; for(i=1;i<=NR;i++) if(k[q[i]] && !k[p[i]]){k[p[i]]=1; print c[i]; n=1}} while(n)}'`
}

func s86Editor(goos string) string {
	if goos == "windows" {
		// No double quotes: Go escapes them as \" for "cmd /C", which cmd does not read.
		return `powershell -NoProfile -Command Set-Content -NoNewline -LiteralPath '{file}.dsh-save' -Value saved; Move-Item -Force -LiteralPath '{file}.dsh-save' -Destination '{file}'`
	}
	return `printf saved > '{file}.dsh-save' && mv -f '{file}.dsh-save' '{file}'`
}

// gitAndEditor checks git (its version recorded) and the editor save script on
// the variant: the script replaces a probe file by rename.
func (l *s86Leg) gitAndEditor(dir string) error {
	if !l.v.has("git") {
		return fmt.Errorf("service:git-and-editor: git is not on the PATH of the %s variant", l.v)
	}
	ver, err := l.v.must("git", "--version")
	if err != nil {
		return err
	}
	s86OuterT.Logf("s86 %s: %s", l.v, ver)
	editor := l.inputs["DSH_SCENARIO_FS3_EDITOR"]
	if editor == "" {
		editor = s86Editor(l.v.goos)
	}
	probe := l.v.join(dir, "editor-probe.txt")
	if err := l.v.writeFile(probe, []byte("before")); err != nil {
		return err
	}
	if out, err := l.v.shell(strings.ReplaceAll(editor, "{file}", probe)); err != nil {
		return fmt.Errorf("service:git-and-editor: the editor save script failed: %v %s", err, out)
	}
	if b, err := l.v.readFile(probe); err != nil || strings.TrimSpace(string(b)) == "before" {
		return fmt.Errorf("service:git-and-editor: the editor save script left %s as %q (%v)", probe, b, err)
	}
	return nil
}

// signedIn checks the provided Claude Code client is installed on the variant.
func (l *s86Leg) signedIn() error {
	if l.provided && !l.v.has("claude") {
		return fmt.Errorf("service:model-route-claude-code: the claude client is not installed on the %s variant", l.v)
	}
	return nil
}

func (s *s86State) givenFS2(goos, fs string) error {
	s.id, s.test = "DH-E-FS-2", "TestLongSessionCorrectsItsOwnPatterns"
	l, err := s.leg(goos, fs)
	if err != nil {
		return err
	}
	if err := l.signedIn(); err != nil {
		return err
	}
	dir, err := l.scratch("dsh-s86-fs2-")
	if err != nil {
		return err
	}
	if err := l.gitAndEditor(dir); err != nil || l.provided {
		return err
	}
	var checkouts []string
	for i := range 2 {
		co := l.v.join(dir, fmt.Sprintf("repo%d", i))
		for _, argv := range [][]string{{"git", "init", "--quiet", co}} {
			if _, err := l.v.must(argv...); err != nil {
				return err
			}
		}
		if err := l.v.writeFile(l.v.join(co, "notes.txt"), []byte("alpha\nbeta\ngamma\n")); err != nil {
			return err
		}
		if err := l.v.writeFile(l.v.join(co, "extra.txt"), []byte("one\ntwo\n")); err != nil {
			return err
		}
		for _, argv := range [][]string{{"git", "-C", co, "add", "."}, {"git", "-C", co, "-c", "user.name=dsh", "-c", "user.email=dsh@example.invalid", "commit", "--quiet", "-m", "seed"}} {
			if _, err := l.v.must(argv...); err != nil {
				return err
			}
		}
		checkouts = append(checkouts, co)
	}
	var tasks strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&tasks, "FS-2 task %02d: in {checkout}, search notes.txt for beta, read line 2 of notes.txt, replace beta with BETA in notes.txt, then patch notes.txt and extra.txt in one patch.\n", i)
	}
	tasksFile := l.v.join(dir, "fs2-tasks.txt")
	if err := l.v.writeFile(tasksFile, []byte(tasks.String())); err != nil {
		return err
	}
	client, temp, err := l.standIn(dir)
	if err != nil {
		return err
	}
	l.inputs["DSH_SCENARIO_FS2_TASKS"] = tasksFile
	l.inputs["DSH_SCENARIO_FS_CHECKOUTS"] = strings.Join(checkouts, ";")
	l.inputs["DSH_SCENARIO_FS2_CHILDREN"] = s86Children(l.v.goos)
	l.inputs["DSH_SCENARIO_FS2_LAYER"] = l.fileLayer(client, temp)
	l.inputs["DSH_SCENARIO_FS2_SHARD"] = "all"
	l.inputs["DSH_SCENARIO_CLAUDE_CODE_MODEL"] = s86StandInModel
	l.inputs["DSH_SCENARIO_FS3_EDITOR"] = s86Editor(l.v.goos)
	return nil
}

func (s *s86State) givenFS2Linux(fs string) error   { return s.givenFS2("linux", fs) }
func (s *s86State) givenFS2Windows(fs string) error { return s.givenFS2("windows", fs) }

func (s *s86State) givenView5() error {
	s.id, s.test = "DH-E-VIEW-5", "TestHostMoveIntoARealWorktree"
	l, err := s.leg("linux", "ext4")
	if err != nil {
		return err
	}
	if err := l.signedIn(); err != nil {
		return err
	}
	if !l.v.has("bwrap") {
		return fmt.Errorf("service:linux-sandbox-bwrap: bwrap is not on the PATH of the %s variant", l.v)
	}
	dir, err := l.scratch("dsh-s86-view5-")
	if err != nil {
		return err
	}
	if err := l.gitAndEditor(dir); err != nil || l.provided {
		return err
	}
	client, temp, err := l.standIn(dir)
	if err != nil {
		return err
	}
	l.inputs["DSH_SCENARIO_VIEW5_LAYER"] = l.fileLayer(client, temp)
	l.inputs["DSH_SCENARIO_CLAUDE_CODE_MODEL"] = s86StandInModel
	// DSH_SCENARIO_VIEW5_MOVE stays the operator's: no host operation of the
	// product moves a session through session/query yet, so no value exists.
	if mv := s86Input(l.v, "DSH_SCENARIO_VIEW5_MOVE"); mv != "" {
		l.inputs["DSH_SCENARIO_VIEW5_MOVE"] = mv
	}
	return nil
}

// sandboxStatus runs "dsh sandbox status" on the variant (a fresh home; the CLI
// takes no --layer for it) and requires the report's head to read state (and
// backend when set).
func (s *s86State) sandboxStatus(backend, state string) error {
	l := s.legs[len(s.legs)-1]
	dir, err := l.v.tempDir("dsh-s86-status-")
	if err != nil {
		return err
	}
	home := l.v.join(dir, "home")
	out, _, err := l.v.run(context.Background(), "", []string{"DSH_HOME=" + home}, nil, l.inputs["DSH_BIN"], "--home", home, "sandbox", "status")
	if err != nil {
		return err
	}
	head := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, val, ok := strings.Cut(strings.TrimSpace(line), ":"); ok {
			if _, seen := head[k]; !seen {
				head[k] = strings.TrimSpace(val)
			}
		}
	}
	if head["state"] != state || (backend != "" && head["backend"] != backend) {
		return fmt.Errorf("dsh sandbox status on %s reads state %q backend %q, want state %q%s:\n%s", l.v, head["state"], head["backend"], state,
			map[bool]string{true: " backend " + strconv.Quote(backend)}[backend != ""], out)
	}
	s.status = fmt.Sprintf("state: %s, backend: %s", head["state"], head["backend"])
	s86OuterT.Logf("s86 %s row on %s records dsh sandbox status %s (the fence under test is the C32 filesystem seam when Unconfined)", s.id, l.v, s.status)
	return nil
}

func (s *s86State) givenUnconfined(state string) error       { return s.sandboxStatus("", state) }
func (s *s86State) givenBackend(backend, state string) error { return s.sandboxStatus(backend, state) }

// ---- the run ----

// run runs the scenario test on each leg; every leg runs even when one fails.
func (s *s86State) run() error {
	var failed []string
	for _, l := range s.legs {
		if l.ran = s.runLeg(l); l.ran != nil {
			failed = append(failed, l.ran.Error())
		}
	}
	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "\n\n"))
	}
	return nil
}

func (s *s86State) runLeg(l *s86Leg) error {
	bin, err := l.v.build("scenarios")
	if err != nil {
		return err
	}
	mod, err := s86Module()
	if err != nil {
		return err
	}
	pkg, err := l.v.path(filepath.Join(mod, filepath.FromSlash(strings.TrimPrefix(s86Pkg, "./"))))
	if err != nil {
		return err
	}
	dir, err := l.v.tempDir("dsh-s86-report-")
	if err != nil {
		return err
	}
	report := l.v.join(dir, "report.jsonl")
	env := []string{"DSH_SCENARIO_REPORT=" + report, "DSH_REQUIRE_SCENARIOS=" + s.id}
	for _, k := range slices.Sorted(maps.Keys(l.inputs)) {
		env = append(env, k+"="+l.inputs[k])
	}
	ctx, cancel := context.WithTimeout(context.Background(), s86RunWithin)
	defer cancel()
	out, code, err := l.v.run(ctx, pkg, env, nil, bin, "-test.run", "^"+s.test+"$", "-test.v", "-test.count=1", "-test.timeout", s86RunWithin.String())
	if err != nil {
		return err
	}
	f, ferr := os.CreateTemp("", "dsh-s86-"+strings.ToLower(s.id)+"-"+l.v.goos+"-*.log")
	if ferr == nil {
		_, _ = f.WriteString(out)
		_ = f.Close()
		s86OuterT.Logf("s86 %s on %s: exit %d, log kept at %s", s.id, l.v, code, f.Name())
	}
	l.log = out
	rows, rerr := l.v.readFile(report)
	sc := bufio.NewScanner(bytes.NewReader(rows))
	for sc.Scan() {
		var row map[string]any
		if json.Unmarshal(sc.Bytes(), &row) == nil && row["id"] == s.id {
			l.row = row
		}
	}
	if code != 0 || l.row == nil || l.row["result"] != "passed" {
		tail := out[max(0, len(out)-3000):]
		return fmt.Errorf("%s (%s) on %s, %s: exit %d, row %v (report %v)%s\n...\n%s", s.test, s.id, l.v,
			map[bool]string{true: "provided instance", false: "ephemeral instance"}[l.provided], code, l.row, rerr, s.diagnose(l), tail)
	}
	return nil
}

// diagnose names the known causes a failed run shows.
func (s *s86State) diagnose(l *s86Leg) string {
	var why []string
	for _, m := range regexp.MustCompile(`(DSH_[A-Z0-9_]+) is not set: ([^\r\n"]*)\r?\n`).FindAllStringSubmatch(l.log, -1) {
		why = append(why, m[1]+" is not set ("+m[2]+"): no product part provides it on this variant; the operator supplies it (evidence/DH-S-18.md, Stage 8.6 blockers)")
	}
	if strings.Contains(l.log, `--layer applies to a profile launch, not to "sandbox"`) {
		why = append(why, `the scenario test runs "dsh --layer L sandbox status", which the CLI refuses (--layer is launch-only)`)
	}
	if strings.Contains(l.log, "this host serves no permission presets") {
		why = append(why, `session/create with permissionMode "workspace-write" is refused (permission/unavailable): no item the dsh binary boots provides permission.presets (evidence/DH-S-14.md step 14.5)`)
	}
	if !l.provided && s.id == "DH-E-FS-1" {
		why = append(why, "no catalogue item registers a route on an Anthropic Messages endpoint, the scripted endpoint's dialect, so the ephemeral FS-1 layer names no route")
	}
	if !l.provided && l.model != nil && l.model.count() == 0 {
		why = append(why, "no client run reached the loopback model"+map[bool]string{true: " (from WSL the Windows loopback is reachable only under mirrored networking)"}[l.v.wsl])
	}
	if len(why) == 0 {
		return ""
	}
	return ":\n  - " + strings.Join(why, "\n  - ")
}

// ---- Then ----

func (s *s86State) thenRowPassed() error {
	for _, l := range s.legs {
		if l.ran != nil || l.row == nil {
			return fmt.Errorf("%s on %s did not run to a row: %v", s.id, l.v, l.ran)
		}
		if l.row["result"] != "passed" || l.row["os"] != l.v.goos {
			return fmt.Errorf("%s on %s: row %v, want passed on %s", s.id, l.v, l.row, l.v.goos)
		}
		sum, err := l.v.sha256(l.inputs["DSH_BIN"])
		if err != nil {
			return err
		}
		if l.row["build"] != sum {
			return fmt.Errorf("%s on %s: row build %v is not the SHA-256 %s of the dsh it ran", s.id, l.v, l.row["build"], sum)
		}
		if s.status != "" {
			s86OuterT.Logf("s86 %s on %s passed; its row records dsh sandbox status %s", s.id, l.v, s.status)
		}
	}
	return nil
}

func (s *s86State) logs(check func(l *s86Leg) error) error {
	for _, l := range s.legs {
		if l.ran != nil {
			return fmt.Errorf("%s on %s did not pass: %v", s.id, l.v, l.ran)
		}
		if err := check(l); err != nil {
			return fmt.Errorf("%s on %s: %w", s.id, l.v, err)
		}
	}
	return nil
}

var s86Swaps = regexp.MustCompile(`the helper swapped W/sub (\d+) times using a (.+)`)

func (s *s86State) thenSwapped() error {
	return s.logs(func(l *s86Leg) error {
		dirs := len(strings.Split(l.inputs["DSH_SCENARIO_FS1_DIRS"], ";")) + 1
		ms := s86Swaps.FindAllStringSubmatch(l.log, -1)
		if len(ms) != dirs {
			return fmt.Errorf("%d swap reports for %d filesystems", len(ms), dirs)
		}
		for _, m := range ms {
			if n, _ := strconv.Atoi(m[1]); n == 0 {
				return fmt.Errorf("the helper never swapped the link (%s)", m[2])
			}
		}
		return nil
	})
}

func (s *s86State) thenOutsideUnchanged() error {
	return s.logs(func(l *s86Leg) error {
		dirs := len(strings.Split(l.inputs["DSH_SCENARIO_FS1_DIRS"], ";")) + 1
		for i := 1; i <= dirs; i++ {
			if !strings.Contains(l.log, fmt.Sprintf("--- PASS: %s/fs%d ", s.test, i)) {
				return fmt.Errorf("the listing of O on filesystem %d was not proved unchanged (no passing fs%d leg)", i, i)
			}
		}
		if strings.Contains(l.log, "files outside every root changed") || strings.Contains(l.log, "an outside file changed") {
			return errors.New("the hash listing of the outside directory changed")
		}
		return nil
	})
}

func (s *s86State) thenTasksPassed() error {
	return s.logs(func(l *s86Leg) error {
		from, to := 1, 20
		if sh := l.inputs["DSH_SCENARIO_FS2_SHARD"]; sh != "all" {
			if _, err := fmt.Sscanf(sh, "%d-%d", &from, &to); err != nil {
				return fmt.Errorf("shard %q: %v", sh, err)
			}
		}
		for i := from; i <= to; i++ {
			if !strings.Contains(l.log, fmt.Sprintf("--- PASS: %s/task%02d ", s.test, i)) {
				return fmt.Errorf("task %02d of the shard did not pass", i)
			}
		}
		if !l.provided && l.model.count() == 0 {
			return errors.New("no client run reached the loopback model")
		}
		return nil
	})
}

// thenNoOutside: the scenario test lists each task's parent directory before
// and after and judges every write; a pass with no outside-change line is that
// clause holding.
func (s *s86State) thenNoOutside() error {
	return s.logs(func(l *s86Leg) error {
		for _, bad := range []string{"outside the write root changed", "files outside the working directory", "outside the working directory of its run"} {
			if strings.Contains(l.log, bad) {
				return fmt.Errorf("the log reports %q", bad)
			}
		}
		if !strings.Contains(l.log, "--- PASS: "+s.test+" ") {
			return errors.New("the scenario test did not pass")
		}
		return nil
	})
}

func InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_file_fence_and_working_directory_on_real_systems(ctx *godog.ScenarioContext) {
	var s *s86State
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		s = &s86State{}
		return c, nil
	})
	ctx.Step(`^the "([^"]*)" variant on Linux and the "([^"]*)" variant on Windows$`, func(a, b string) error { return s.givenFS1Variants(a, b) })
	ctx.Step(`^10,000 tool calls run under the link-swap helper on each variant$`, func() error { return s.run() })
	ctx.Step(`^each variant's DH-E-FS-1 row reads passed for the binary it ran$`, func() error { return s.thenRowPassed() })
	ctx.Step(`^on each variant the helper swapped the link while the calls ran$`, func() error { return s.thenSwapped() })
	ctx.Step(`^on each variant the hash listing of the outside directory is unchanged before and after$`, func() error { return s.thenOutsideUnchanged() })

	ctx.Step(`^the tester's Linux machine on "([^"]*)" with the signed-in Claude Code route, git and an editor save script$`, func(fs string) error { return s.givenFS2Linux(fs) })
	ctx.Step(`^the Windows variant machine on "([^"]*)" with the signed-in Claude Code CLI installed natively, git and an editor save script$`, func(fs string) error { return s.givenFS2Windows(fs) })
	ctx.Step(`^"dsh sandbox status" on the variant reads state "([^"]*)", recorded in the row$`, func(st string) error { return s.givenUnconfined(st) })
	ctx.Step(`^the long-running FS-2 run completes on the variant$`, func() error { return s.run() })
	ctx.Step(`^the long-running FS-2 run completes on the variant under mode "workspace-write"$`, func() error { return s.run() })
	ctx.Step(`^the variant's (DH-E-[A-Z0-9-]+) row reads passed for the binary it ran$`, func(string) error { return s.thenRowPassed() })
	ctx.Step(`^every task of the run's shard passed$`, func() error { return s.thenTasksPassed() })
	ctx.Step(`^every model-chosen write through the six file tools lands inside the session's write roots, with zero files outside them in the before and after listings$`, func() error { return s.thenNoOutside() })
	ctx.Step(`^no file appears outside the write roots in the before and after listings$`, func() error { return s.thenNoOutside() })

	ctx.Step(`^the tester's Linux machine with the signed-in route, a real git repository and bwrap$`, func() error { return s.givenView5() })
	ctx.Step(`^"dsh sandbox status" on the variant reads backend "([^"]*)" state "([^"]*)"$`, func(b, st string) error { return s.givenBackend(b, st) })
	ctx.Step(`^VIEW-5 runs on the variant under "workspace-write"$`, func() error { return s.run() })
	ctx.Step(`^every model-chosen write lands inside the session's working directory$`, func() error { return s.thenNoOutside() })
}

func TestE2E_dh_s_18_acceptance_on_real_operating_systems_file_fence_and_working_directory_on_real_systems(t *testing.T) {
	s86OuterT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_file_fence_and_working_directory_on_real_systems,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_18_acceptance_on_real_operating_systems_file_fence_and_working_directory_on_real_systems.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the file fence and working directory scenarios failed")
	}
}
