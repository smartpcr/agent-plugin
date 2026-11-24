//go:build e2e

package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/checkpoint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	spilllocal "github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/local"
	htools "github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/ops"
	fstools "github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

// sftDirs is the session's working directory: the workspace root.
type sftDirs struct{ root string }

func (d sftDirs) Access(session.ID) (workdir.Access, error) { return d, nil }
func (d sftDirs) Current() (workdir.WorkingDirectory, error) {
	return workdir.WorkingDirectory{Path: d.root, Root: d.root}, nil
}
func (d sftDirs) Path() (string, error) { return d.root, nil }
func (d sftDirs) Root() (string, error) { return d.root, nil }

// sftWorld is the real tool pipeline with fs.tools mounted over fs/local and
// the operation service, fenced (workspace-write) to a workspace in a temp
// directory, logging to an embedded SQLite store and spilling to a local spill
// store in a temp home.
type sftWorld struct {
	base  string
	ws    string
	root  *cordis.Context
	svc   htools.Service
	st    *sqlite.Store
	spill spill.Store
	n     int

	handles []session.Handle
	id      session.ID
	res     htools.Result
	logged  string

	bigFull   string
	emptyPath string
	spawns    *sftSpawnRecorder
	spawnBase int
	envSaved  map[string]*string
}

var sftSpillRef = regexp.MustCompile(`saved to spill (\S+)\.\)`)

func sftUnquote(s string) (string, error) {
	v, err := strconv.Unquote(`"` + s + `"`)
	if err != nil {
		return "", fmt.Errorf("step string %q is not a Go-escaped string: %w", s, err)
	}
	return v, nil
}

func sftDigest(s string) string {
	d := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(d[:])
}

// sftJSONText is s as it appears inside a JSON string (a Windows path's
// backslashes are escaped in the log).
func sftJSONText(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

func (w *sftWorld) setenv(k, v string) {
	if _, ok := w.envSaved[k]; !ok {
		if old, had := os.LookupEnv(k); had {
			w.envSaved[k] = &old
		} else {
			w.envSaved[k] = nil
		}
	}
	_ = os.Setenv(k, v)
}

func (w *sftWorld) cleanup() {
	if w.spawns != nil {
		w.spawns.Close()
	}
	for k, v := range w.envSaved {
		if v == nil {
			_ = os.Unsetenv(k)
		} else {
			_ = os.Setenv(k, *v)
		}
	}
	ctx := context.Background()
	for _, h := range w.handles {
		_ = h.Close(ctx)
	}
	if w.root != nil {
		w.root.Dispose()
	}
	if w.st != nil {
		_ = w.st.Close(ctx)
	}
	if w.base != "" {
		_ = os.RemoveAll(w.base)
	}
}

func (w *sftWorld) mkdir(name string) (string, error) {
	p := filepath.Join(w.base, name)
	return p, os.MkdirAll(p, 0o755)
}

func (w *sftWorld) givenRig(ctx context.Context) error {
	base, err := os.MkdirTemp("", "dsh-sft-e2e-")
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(base); err == nil {
		base = real
	}
	w.base = base
	if w.ws, err = w.mkdir("workspace"); err != nil {
		return err
	}
	homeDir, err := w.mkdir("home")
	if err != nil {
		return err
	}

	w.root = cordis.New()
	w.root.Loggers().ClearExporters()
	w.root.Loggers().AddExporter(&cordis.MemoryExporter{})
	f, err := w.root.Use(scope.Component, cordis.None{})
	if err == nil {
		err = f.Wait()
	}
	if err != nil {
		return fmt.Errorf("mount session scope: %w", err)
	}
	var tab scope.Table
	g, err := w.root.Inject([]string{scope.TableKey.Name()}, func(c *cordis.Context) error {
		var err error
		tab, err = scope.TableKey.Get(c)
		return err
	})
	if err == nil {
		err = g.Wait()
	}
	if err != nil {
		return fmt.Errorf("inject scope table: %w", err)
	}
	w.svc = core.NewWith(w.root, tab, core.Config{Checkpoint: checkpoint.Default{}})

	l := home.New(homeDir)
	if err := home.Ensure(l); err != nil {
		return err
	}
	if w.st, err = sqlite.OpenHome(ctx, l); err != nil {
		return fmt.Errorf("open embedded sqlite store: %w", err)
	}
	if w.spill, err = spilllocal.New(l, spilllocal.DefaultConfig, spilllocal.WithLineage(spilllocal.ForkLineage(w.st))); err != nil {
		return fmt.Errorf("open local spill store: %w", err)
	}

	wd := sftDirs{w.ws}
	o := ops.New(w.root, local.New(), &sandboxtest.Resolver{Default: sandbox.WorkspaceWrite, Workdir: wd}, ops.Options{Workdir: wd})
	if _, err := fstools.Mount(w.root, w.svc, o, w.spill, fstools.Config{}); err != nil {
		return fmt.Errorf("mount fs.tools: %w", err)
	}
	return nil
}

func (w *sftWorld) path(name string) string { return filepath.Join(w.ws, filepath.FromSlash(name)) }

func (w *sftWorld) put(name, content string) error {
	p := w.path(name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

func (w *sftWorld) disk(name string) (string, error) {
	b, err := os.ReadFile(w.path(name))
	return string(b), err
}

func (w *sftWorld) givenFile(name, content string) error {
	v, err := sftUnquote(content)
	if err != nil {
		return err
	}
	return w.put(name, v)
}

func (w *sftWorld) openStep(ctx context.Context, id session.ID, c htools.CallRequest) (session.Handle, error) {
	h, err := session.NewService(w.root, w.st, session.Config{}).Create(ctx, nil, session.CreateOptions{Header: session.Header{ID: string(id)}})
	if err != nil {
		return nil, err
	}
	w.handles = append(w.handles, h)
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("go"))
	if err != nil {
		return nil, err
	}
	asked, err := vocab.RestoreMessage("a1", vocab.RoleAssistant, nil, vocab.ToolUseBlock(c.ID, c.Name, json.RawMessage(c.Arguments)))
	if err != nil {
		return nil, err
	}
	for _, mk := range []func() (event.Draft, error){
		func() (event.Draft, error) { return event.TurnStartType.Draft(event.TurnStart{Turn: 1}) },
		func() (event.Draft, error) { return event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1}) },
		func() (event.Draft, error) {
			return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
		},
		func() (event.Draft, error) {
			return event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: asked})
		},
	} {
		d, err := mk()
		if err != nil {
			return nil, err
		}
		if _, err := h.Append(ctx, d); err != nil {
			return nil, err
		}
	}
	return h, nil
}

// whenRun executes one call through the pipeline in a fresh session step and
// keeps its result and the raw tool/result event the log holds for it.
func (w *sftWorld) whenRun(ctx context.Context, tool string, args *godog.DocString) error {
	var probe map[string]any
	if err := json.Unmarshal([]byte(args.Content), &probe); err != nil {
		return fmt.Errorf("arguments are not a JSON object: %w", err)
	}
	w.n++
	w.id = session.ID(fmt.Sprintf("s-sft-e2e-%d", w.n))
	c := htools.CallRequest{ID: "c1", Name: tool, Arguments: args.Content}
	h, err := w.openStep(ctx, w.id, c)
	if err != nil {
		return fmt.Errorf("open session step: %w", err)
	}
	v, err := w.svc.Resolve(ctx, htools.Target{Agent: agent.Ref{Session: w.id, Incarnation: 1}})
	if err != nil {
		return fmt.Errorf("resolve tool view: %w", err)
	}
	defer v.Release()
	res, err := w.svc.ExecuteBatch(ctx, htools.Batch{View: v, Log: h, Calls: []htools.CallRequest{c}})
	if err != nil {
		return fmt.Errorf("execute %s: %w", tool, err)
	}
	if len(res.Results) != 1 {
		return fmt.Errorf("%s returned %d results; want 1", tool, len(res.Results))
	}
	w.res = res.Results[0]
	evs, err := h.Read(ctx, 0, 0)
	if err != nil {
		return err
	}
	w.logged = ""
	for _, ev := range evs {
		if ev.Type() == "tool/result" {
			w.logged = ev.Raw()
		}
	}
	if w.logged == "" {
		return fmt.Errorf("%s: no tool/result was logged", tool)
	}
	return nil
}

func (w *sftWorld) thenSucceeds() error {
	if w.res.IsError {
		return fmt.Errorf("the call failed: %s", w.res.Text)
	}
	return nil
}

func (w *sftWorld) thenOnDisk(name, want string) error {
	v, err := sftUnquote(want)
	if err != nil {
		return err
	}
	got, err := w.disk(name)
	if err != nil {
		return err
	}
	if got != v {
		return fmt.Errorf("%s on disk is %q; want %q", name, got, v)
	}
	return nil
}

func (w *sftWorld) thenGone(name string) error {
	if _, err := os.Stat(w.path(name)); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s still exists on disk (stat: %v)", name, err)
	}
	return nil
}

func (w *sftWorld) thenNamesVersion(name string) error {
	content, err := w.disk(name)
	if err != nil {
		return err
	}
	line := w.path(name) + " (version " + sftDigest(content) + ")"
	if !strings.Contains(w.res.Text, line) {
		return fmt.Errorf("the result does not name %q:\n%s", line, w.res.Text)
	}
	if !strings.Contains(w.logged, sftJSONText(line)) {
		return fmt.Errorf("the logged tool/result does not name %q:\n%s", line, w.logged)
	}
	return nil
}

func (w *sftWorld) givenTree(n int, dir string) error {
	for i := range n {
		body := fmt.Sprintf("file %d\nnothing here\n", i)
		if i%10 == 0 {
			body += fmt.Sprintf("the needle %d\n", i)
		}
		if err := w.put(fmt.Sprintf("%s/d%02d/f%04d.txt", dir, i/100, i), body); err != nil {
			return err
		}
	}
	return nil
}

func (w *sftWorld) thenStartsWith(prefix string) error {
	v, err := sftUnquote(prefix)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(w.res.Text, v) {
		return fmt.Errorf("the result does not start with %q: %.500s", v, w.res.Text)
	}
	return nil
}

func (w *sftWorld) thenContains(sub string) error {
	v, err := sftUnquote(sub)
	if err != nil {
		return err
	}
	if !strings.Contains(w.res.Text, v) {
		return fmt.Errorf("the result does not contain %q: %.500s", v, w.res.Text)
	}
	return nil
}

// givenEmptyPath puts PATH on one empty directory: no program (rg or any
// other) can be found by name while it is set.
func (w *sftWorld) givenEmptyPath() error {
	bin, err := w.mkdir("empty-path")
	if err != nil {
		return err
	}
	w.emptyPath = bin
	w.setenv("PATH", bin)
	if found, err := exec.LookPath("rg"); err == nil {
		return fmt.Errorf("rg still resolves to %s on the scrubbed PATH", found)
	}
	return w.thenPathStillEmpty()
}

func (w *sftWorld) thenPathStillEmpty() error {
	if os.Getenv("PATH") != w.emptyPath {
		return fmt.Errorf("PATH is %q; want only the empty directory %q", os.Getenv("PATH"), w.emptyPath)
	}
	ents, err := os.ReadDir(w.emptyPath)
	if err != nil {
		return err
	}
	if len(ents) != 0 {
		return fmt.Errorf("the PATH directory %s is not empty: %d entries", w.emptyPath, len(ents))
	}
	return nil
}

// givenSpawnRecorder arms the subprocess fake: an OS-level recorder of every
// process the test process starts, by name or by absolute path.
func (w *sftWorld) givenSpawnRecorder() error {
	r, err := sftNewSpawnRecorder()
	if err != nil {
		return fmt.Errorf("arm the spawn recorder: %w", err)
	}
	w.spawns = r
	w.spawnBase, err = r.Count()
	return err
}

// givenControlSpawn proves the recorder is live: one deliberate start of the
// test binary by absolute path (it runs no tests) must be recorded.
func (w *sftWorld) givenControlSpawn() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if out, err := exec.Command(self, "-test.run=^$").CombinedOutput(); err != nil {
		return fmt.Errorf("control spawn of %s: %v\n%s", self, err, out)
	}
	n, err := w.spawns.Count()
	if err != nil {
		return err
	}
	if n-w.spawnBase < 1 {
		return fmt.Errorf("the spawn recorder missed the control spawn (count %d -> %d)", w.spawnBase, n)
	}
	w.spawnBase = n
	return nil
}

func (w *sftWorld) thenZeroSpawns() error {
	if w.spawns == nil {
		return errors.New("the spawn recorder was never armed")
	}
	n, err := w.spawns.Count()
	if err != nil {
		return err
	}
	if d := n - w.spawnBase; d != 0 {
		return fmt.Errorf("the spawn recorder recorded %d process start(s) during grep", d)
	}
	return w.thenPathStillEmpty()
}
func (w *sftWorld) givenBigFile(mib int, name string) error {
	var b strings.Builder
	for i := 0; b.Len() < mib<<20; i++ {
		fmt.Fprintf(&b, "%07d %s\n", i, strings.Repeat("x", 56))
	}
	w.bigFull = b.String()
	return w.put(name, w.bigFull)
}

func (w *sftWorld) thenTruncatedAt(bound int) error {
	if bound != fstools.DefaultReadMaxBytes {
		return fmt.Errorf("the feature's bound %d is not the default read bound %d", bound, fstools.DefaultReadMaxBytes)
	}
	text := w.res.Text
	i := strings.Index(text, "<content>\n")
	if i < 0 {
		return fmt.Errorf("no <content> block: %.300s", text)
	}
	content := text[i+len("<content>\n"):]
	j := strings.Index(content, "\n\n(")
	if j < 0 {
		return fmt.Errorf("no footer after the numbered lines: %.300s", text[max(0, len(text)-300):])
	}
	content = content[:j+1]
	if len(content) > bound || len(content) < bound-100 {
		return fmt.Errorf("the numbered lines are %d bytes; the bound is %d", len(content), bound)
	}
	if want := fmt.Sprintf("(Output capped at %d bytes. Showing lines 1-", bound); !strings.Contains(text, want) {
		return fmt.Errorf("no truncation footer %q: %s", want, text[max(0, len(text)-300):])
	}
	if len(text) >= len(w.bigFull) {
		return fmt.Errorf("the result (%d bytes) is not smaller than the file (%d bytes)", len(text), len(w.bigFull))
	}
	return nil
}

func (w *sftWorld) thenSpillCited(ctx context.Context) error {
	m := sftSpillRef.FindStringSubmatch(w.res.Text)
	if m == nil || w.res.Spill == nil || w.res.Spill.Path != m[1] {
		return fmt.Errorf("the text cites %v, Result.Spill is %+v: %s", m, w.res.Spill, w.res.Text[max(0, len(w.res.Text)-300):])
	}
	var p struct {
		Spill *event.Spill `json:"spill"`
	}
	if err := json.Unmarshal([]byte(w.logged), &p); err != nil || p.Spill == nil || *p.Spill != *w.res.Spill {
		return fmt.Errorf("the logged spill is %+v (%v); want %+v", p.Spill, err, w.res.Spill)
	}
	ok, err := w.st.Cites(ctx, string(w.id), "spill:"+w.res.Spill.Path)
	if err != nil || !ok {
		return fmt.Errorf("the session store does not track the citation of %s: %v", w.res.Spill.Path, err)
	}
	return nil
}

func (w *sftWorld) thenSpillEqualsFile(ctx context.Context) error {
	if w.res.Spill == nil {
		return errors.New("the result carries no spill reference")
	}
	rc, err := w.spill.Open(ctx, string(w.id), w.res.Spill.Path)
	if err != nil {
		return fmt.Errorf("open spill %s: %w", w.res.Spill.Path, err)
	}
	defer rc.Close()
	saved, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	if int64(len(saved)) != w.res.Spill.Bytes {
		return fmt.Errorf("the spill holds %d bytes; the reference says %d", len(saved), w.res.Spill.Bytes)
	}
	if string(saved) != w.bigFull {
		return fmt.Errorf("the spilled content is %d bytes and differs from the %d-byte file", len(saved), len(w.bigFull))
	}
	return nil
}

const sftQ = `"((?:[^"\\]|\\.)*)"`

func InitializeScenario_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_six_file_tools(ctx *godog.ScenarioContext) {
	w := &sftWorld{envSaved: map[string]*string{}}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.cleanup()
		return c, err
	})
	ctx.Step(`^a workspace in a temp directory with the six file tools mounted on the tool pipeline$`, w.givenRig)
	ctx.Step(`^the workspace file `+sftQ+` holds `+sftQ+`$`, w.givenFile)
	ctx.Step(`^"([^"]*)" runs with arguments:$`, w.whenRun)
	ctx.Step(`^the call succeeds$`, w.thenSucceeds)
	ctx.Step(`^`+sftQ+` on disk is `+sftQ+`$`, w.thenOnDisk)
	ctx.Step(`^"([^"]*)" no longer exists on disk$`, w.thenGone)
	ctx.Step(`^the result and its logged tool/result name "([^"]*)" at its on-disk version$`, w.thenNamesVersion)
	ctx.Step(`^(\d+) workspace files under "([^"]*)", every tenth holding a needle line$`, w.givenTree)
	ctx.Step(`^PATH is scrubbed to an empty directory$`, w.givenEmptyPath)
	ctx.Step(`^a subprocess fake records every process the test process starts$`, w.givenSpawnRecorder)
	ctx.Step(`^the subprocess fake records a control spawn of the test binary by absolute path$`, w.givenControlSpawn)
	ctx.Step(`^the result starts with `+sftQ+`$`, w.thenStartsWith)
	ctx.Step(`^the result contains `+sftQ+`$`, w.thenContains)
	ctx.Step(`^the subprocess fake records zero spawns since the control spawn and PATH is still the empty directory$`, w.thenZeroSpawns)
	ctx.Step(`^a (\d+) MiB workspace file "([^"]*)"$`, w.givenBigFile)
	ctx.Step(`^the numbered lines are truncated at the default read bound of (\d+) bytes$`, w.thenTruncatedAt)
	ctx.Step(`^the result carries a spill reference that the logged tool/result and the session store both cite$`, w.thenSpillCited)
	ctx.Step(`^the spilled content equals the full file$`, w.thenSpillEqualsFile)
}

func TestE2E_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_six_file_tools(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "dh_s_12_working_directory_filesystem_seam_and_policy_resolver_six_file_tools",
		ScenarioInitializer: InitializeScenario_dh_s_12_working_directory_filesystem_seam_and_policy_resolver_six_file_tools,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_12_working_directory_filesystem_seam_and_policy_resolver_six_file_tools.feature"},
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("six file tools e2e scenarios failed")
	}
}
