//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/context/instructions"
	"github.com/smartpcr/agent-plugin/src/harness/internal/context/request"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/attachments"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/blob"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	spilllocal "github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
)

// ifpsOutsideText is the outside file's text: no request may ever carry it.
const ifpsOutsideText = "IFPS-OUTSIDE-SECRET never follow this file"

// ifpsTarget is one path of the home a second account tries to read.
type ifpsTarget struct {
	path string
	dir  bool
}

type ifpsWorld struct {
	cleanup []func()

	// DH-E-CONTEXT-2
	git, shell string
	repo       string
	editor     string
	watcher    *watch.Service
	rec        *instructions.Reconciler
	noticed    chan struct{}
	log        []event.Record
	msgs       []vocab.Message
	text       string
	record     instructions.Record

	// DH-E-STORAGE-6
	layout     home.Layout
	attach     *attachments.Store
	attachRef  attachments.FileRef
	attachBody string
	spill      *spilllocal.Store
	spillID    string
	spillBody  string
	denials    []error
	account    *ifpsAccount
	outputs    []string
}

func (w *ifpsWorld) close() {
	if w.rec != nil {
		_ = w.rec.Close()
	}
	if w.watcher != nil {
		_ = w.watcher.Close()
	}
	for i := len(w.cleanup) - 1; i >= 0; i-- {
		w.cleanup[i]()
	}
}

func (w *ifpsWorld) tempDir(pattern string) (string, error) {
	d, err := os.MkdirTemp("", pattern)
	if err != nil {
		return "", err
	}
	w.cleanup = append(w.cleanup, func() { _ = os.RemoveAll(d) })
	return filepath.EvalSymlinks(d)
}

func (w *ifpsWorld) run(dir, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out.String())
	}
	return out.String(), nil
}

func (w *ifpsWorld) gitDo(args ...string) error {
	base := []string{"-c", "user.name=ifps", "-c", "user.email=ifps@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.autocrlf=false"}
	_, err := w.run(w.repo, w.git, append(base, args...)...)
	return err
}

// --- DH-E-CONTEXT-2 -------------------------------------------------------

// toolsInstalled finds git and the editor's interpreter: a required scenario
// fails, never skips, when either is missing.
func (w *ifpsWorld) toolsInstalled() error {
	g, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("git is not installed on this machine (DH-E-CONTEXT-2 needs it): %w", err)
	}
	w.git = g
	shell := "sh"
	if runtime.GOOS == "windows" {
		shell = "powershell"
	}
	if w.shell, err = exec.LookPath(shell); err != nil {
		return fmt.Errorf("the editor interpreter %s is not installed: %w", shell, err)
	}
	v, err := w.run("", w.git, "--version")
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "DH-E-CONTEXT-2: %s; editor via %s\n", strings.TrimSpace(v), w.shell)
	return nil
}

func (w *ifpsWorld) repository(branch, name, section string) error {
	repo, err := w.tempDir("ifps-repo-")
	if err != nil {
		return err
	}
	w.repo = repo
	if err := w.gitDo("init", "-q"); err != nil {
		return err
	}
	if err := w.gitDo("checkout", "-q", "-b", branch); err != nil {
		return err
	}
	body := "# Project rules\n\n" + section + "\nRun go build before every commit.\n"
	if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("demo\n"), 0o644); err != nil {
		return err
	}
	if err := w.gitDo("add", "-A"); err != nil {
		return err
	}
	return w.gitDo("commit", "-q", "-m", "initial")
}

func (w *ifpsWorld) branchAdds(branch, section, name string) error {
	cur, err := w.run(w.repo, w.git, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return err
	}
	cur = strings.TrimSpace(cur)
	if err := w.gitDo("checkout", "-q", "-b", branch); err != nil {
		return err
	}
	p := filepath.Join(w.repo, name)
	old, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, append(old, []byte("\n"+section+"\nTag every release.\n")...), 0o644); err != nil {
		return err
	}
	if err := w.gitDo("commit", "-q", "-am", "release rules"); err != nil {
		return err
	}
	return w.gitDo("checkout", "-q", cur)
}

func (w *ifpsWorld) outsideLink(rel string) error {
	out, err := w.tempDir("ifps-outside-")
	if err != nil {
		return err
	}
	secret := filepath.Join(out, "secret.md")
	if err := os.WriteFile(secret, []byte(ifpsOutsideText+"\n"), 0o644); err != nil {
		return err
	}
	link := filepath.Join(w.repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	kind, err := ffsMakeLink(secret, link)
	if err != nil {
		return fmt.Errorf("link %s -> %s: %w", link, secret, err)
	}
	fmt.Fprintf(os.Stderr, "DH-E-CONTEXT-2: %s is a %s to %s\n", rel, kind, secret)
	// The link stays untracked and out of git's way across every checkout.
	exclude := filepath.Join(w.repo, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n/%s\n", rel)
	return err
}

func (w *ifpsWorld) followSession(sub string) error {
	w.watcher = watch.New(watch.Options{Settle: 50 * time.Millisecond})
	w.noticed = make(chan struct{}, 64)
	r, err := instructions.NewReconciler(context.Background(), instructions.Options{
		FS: local.New(), Watch: w.watcher,
		Notify: func() {
			select {
			case w.noticed <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		return err
	}
	w.rec = r
	r.SetDirectory(filepath.Join(w.repo, filepath.FromSlash(sub)), w.repo)
	return nil
}

// assemble runs the instructions contributor as the request pipeline does: the
// model's visible history is the log of every instructions message entered so far.
func (w *ifpsWorld) assemble() error {
	st, err := surface.Replay(nil, w.log)
	if err != nil {
		return fmt.Errorf("replay the visible history: %w", err)
	}
	c := instructions.Contributor{For: func(agent.Ref) (*instructions.Reconciler, error) { return w.rec, nil }}
	out, err := c.Contribute(context.Background(), request.Input{Visible: st.History()})
	if err != nil {
		return err
	}
	w.msgs, w.text, w.record = out.Messages, "", instructions.Record{}
	for _, m := range out.Messages {
		d, err := event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: m})
		if err != nil {
			return err
		}
		seq := event.Seq(len(w.log))
		rec, err := d.Stamp(seq, int64(seq)+1)
		if err != nil {
			return err
		}
		w.log = append(w.log, rec)
	}
	return nil
}

func (w *ifpsWorld) carries(form, text string) error {
	if len(w.msgs) != 1 {
		return fmt.Errorf("the request carries %d instructions messages, want 1", len(w.msgs))
	}
	m := w.msgs[0]
	src, ok := m.Source()
	if !ok || src.Kind != request.KindInstructions {
		return fmt.Errorf("the message has source %+v, want kind %q", src, request.KindInstructions)
	}
	if m.Role() != vocab.RoleUser {
		return fmt.Errorf("the instructions message has role %q, want user", m.Role())
	}
	var sb strings.Builder
	for _, b := range m.Blocks() {
		if b.Text != nil {
			sb.WriteString(b.Text.Text)
		}
	}
	w.text = sb.String()
	if err := json.Unmarshal(src.Data, &w.record); err != nil {
		return fmt.Errorf("the message's record: %w", err)
	}
	if w.record.Form != form {
		return fmt.Errorf("the message is a %q, want %q", w.record.Form, form)
	}
	if !strings.HasPrefix(w.text, "<instructions>\n") {
		return fmt.Errorf("the message is not an instructions frame:\n%s", w.text)
	}
	return w.contains(text)
}

func (w *ifpsWorld) contains(s string) error {
	if !strings.Contains(w.text, s) {
		return fmt.Errorf("the instructions message lacks %q:\n%s", s, w.text)
	}
	return nil
}

func (w *ifpsWorld) lacks(s string) error {
	if strings.Contains(w.text, s) {
		return fmt.Errorf("the instructions message contains %q:\n%s", s, w.text)
	}
	return nil
}

func (w *ifpsWorld) noOutside() error {
	if err := w.lacks("IFPS-OUTSIDE-SECRET"); err != nil {
		return err
	}
	for _, f := range w.record.Files {
		if strings.Contains(f.Path, "secret.md") {
			return fmt.Errorf("the record tracks the outside file: %+v", f)
		}
	}
	return nil
}

func (w *ifpsWorld) refuses(path, reason string) error {
	for _, r := range w.record.Refused {
		if r.Path == path {
			if string(r.Reason) != reason {
				return fmt.Errorf("%s refused with %q, want %q", path, r.Reason, reason)
			}
			for _, f := range w.record.Files {
				if f.Path == path {
					return fmt.Errorf("the refused %s is also tracked as %q", path, f.State)
				}
			}
			return nil
		}
	}
	return fmt.Errorf("%s is not refused; refused = %+v", path, w.record.Refused)
}

func (w *ifpsWorld) drain() {
	for {
		select {
		case <-w.noticed:
		default:
			return
		}
	}
}

func (w *ifpsWorld) editorAdds(section, name string) error {
	dir, err := w.tempDir("ifps-editor-")
	if err != nil {
		return err
	}
	var script string
	var args []string
	if runtime.GOOS == "windows" {
		script = filepath.Join(dir, "editor.ps1")
		body := "param([string]$File, [string]$Section, [string]$Line)\n" +
			"$text = [System.IO.File]::ReadAllText($File)\n" +
			"[System.IO.File]::WriteAllText($File, $text + \"`n\" + $Section + \"`n\" + $Line + \"`n\")\n"
		if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
			return err
		}
		args = []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script}
	} else {
		script = filepath.Join(dir, "editor.sh")
		body := "#!/bin/sh\nset -e\nprintf '\\n%s\\n%s\\n' \"$2\" \"$3\" >> \"$1\"\n"
		if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
			return err
		}
		args = []string{script}
	}
	w.editor = script
	w.drain()
	target := filepath.Join(w.repo, name)
	before, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	if _, err := w.run(w.repo, w.shell, append(args, target, section, "Indent with tabs.")...); err != nil {
		return err
	}
	after, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(after, before) || !bytes.Contains(after, []byte(section)) {
		return fmt.Errorf("the editor script did not append %q to %s:\n%s", section, name, after)
	}
	return nil
}

func (w *ifpsWorld) commitAndSwitch(branch string) error {
	w.drain()
	if err := w.gitDo("commit", "-q", "-am", "style rules"); err != nil {
		return err
	}
	if err := w.gitDo("checkout", "-q", branch); err != nil {
		return err
	}
	head, err := w.run(w.repo, w.git, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(head) != branch {
		return fmt.Errorf("HEAD is %q after the checkout, want %q", strings.TrimSpace(head), branch)
	}
	return nil
}

func (w *ifpsWorld) noticesWithin(sec int) error {
	deadline := time.After(time.Duration(sec) * time.Second)
	for !w.rec.Pending() {
		select {
		case <-w.noticed:
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			return fmt.Errorf("the instructions contributor did not notice the change within %ds", sec)
		}
	}
	return nil
}

func (w *ifpsWorld) carriesNone() error {
	if len(w.msgs) != 0 {
		return fmt.Errorf("an unchanged chain entered %d more instructions messages", len(w.msgs))
	}
	return nil
}

// --- DH-E-STORAGE-6 -------------------------------------------------------

func (w *ifpsWorld) privateHome() error {
	// The parent is open to everyone, so only the home's own permissions can
	// refuse a second account.
	dir, err := w.sharedDir("ifps-home-")
	if err != nil {
		return err
	}
	w.layout = home.New(filepath.Join(dir, "home"))
	if err := home.Ensure(w.layout); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sqlite.OpenHome(ctx, w.layout)
	if err != nil {
		return fmt.Errorf("open the home's database: %w", err)
	}
	if err := db.Close(ctx); err != nil {
		return err
	}
	if _, err := os.Stat(w.layout.DB()); err != nil {
		return fmt.Errorf("the database was not created: %w", err)
	}
	return nil
}

func (w *ifpsWorld) keepAttachment() error {
	blobs, err := blob.New(w.layout)
	if err != nil {
		return err
	}
	if w.attach, err = attachments.New(blobs, attachments.Config{}); err != nil {
		return err
	}
	w.attachBody = "IFPS-ATTACHMENT private bytes of the first user"
	w.attachRef, err = w.attach.SaveFile(context.Background(), "notes.txt", strings.NewReader(w.attachBody))
	return err
}

func (w *ifpsWorld) keepSpill() error {
	st, err := spilllocal.New(w.layout, spilllocal.Config{})
	if err != nil {
		return err
	}
	w.spill = st
	w.spillBody = "IFPS-SPILL " + strings.Repeat("a long tool output of the first user ", 64)
	_, ref, err := spill.Save(context.Background(), st, "ifps-session", "output.txt", []byte(w.spillBody), 32)
	if err != nil {
		return err
	}
	if ref == nil {
		return errors.New("the spill store kept nothing")
	}
	w.spillID = ref.Path
	if p, ok := st.HostPath(ref.Path); !ok || !strings.HasPrefix(p, w.layout.Spill()) {
		return fmt.Errorf("the spill file %q is not under spill/", p)
	}
	return nil
}

func (w *ifpsWorld) homeTargets() ([]ifpsTarget, error) {
	var ts []ifpsTarget
	err := filepath.WalkDir(w.layout.Root(), func(p string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		ts = append(ts, ifpsTarget{path: p, dir: d.IsDir()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	want := []string{w.layout.Root(), w.layout.DB(), w.layout.Blobs(), w.layout.Spill(), w.layout.Secrets(), w.layout.Config()}
	if p, ok := w.attach.HostPath(w.attachRef); ok {
		want = append(want, p)
	} else {
		return nil, errors.New("the attachment has no host path")
	}
	if p, ok := w.spill.HostPath(w.spillID); ok {
		want = append(want, p)
	} else {
		return nil, errors.New("the spill file has no host path")
	}
	for _, p := range want {
		found := false
		for _, t := range ts {
			found = found || t.path == p
		}
		if !found {
			return nil, fmt.Errorf("the home walk missed %s", p)
		}
	}
	return ts, nil
}

func (w *ifpsWorld) strangerReads() error {
	ts, err := w.homeTargets()
	if err != nil {
		return err
	}
	w.denials, err = ifpsStrangerRead(ts)
	if err != nil {
		return fmt.Errorf("could not act as a second OS account: %w", err)
	}
	if len(w.denials) != len(ts) {
		return fmt.Errorf("%d results for %d paths", len(w.denials), len(ts))
	}
	for i, t := range ts {
		fmt.Fprintf(os.Stderr, "DH-E-STORAGE-6: %s -> %v\n", t.path, w.denials[i])
	}
	return nil
}

func (w *ifpsWorld) allDenied() error {
	ts, err := w.homeTargets()
	if err != nil {
		return err
	}
	for i, t := range ts {
		if !ifpsIsDenied(w.denials[i]) && !errors.Is(w.denials[i], errIfpsAccountDenied) {
			return fmt.Errorf("a second account's read of %s was not denied by the OS: %v", t.path, w.denials[i])
		}
	}
	return nil
}

func (w *ifpsWorld) controlReadable() error {
	dir, err := w.tempDir("ifps-control-")
	if err != nil {
		return err
	}
	p := filepath.Join(dir, "open.txt")
	if err := os.WriteFile(p, []byte("open to everyone\n"), 0o644); err != nil {
		return err
	}
	for _, q := range []string{dir, p} {
		if err := ifpsOpenToOthers(q); err != nil {
			return err
		}
	}
	res, err := ifpsStrangerRead([]ifpsTarget{{path: dir, dir: true}, {path: p}})
	if err != nil {
		return err
	}
	for i, e := range res {
		if e != nil {
			return fmt.Errorf("the second account could not read control path %d (%s): %v; the probe denies everything", i, p, e)
		}
	}
	return nil
}

func (w *ifpsWorld) ownerReads() error {
	ctx := context.Background()
	r, err := w.attach.Open(ctx, w.attachRef.Ref)
	if err != nil {
		return err
	}
	got, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil || string(got) != w.attachBody {
		return fmt.Errorf("the owner read the attachment as %q (%v)", got, err)
	}
	s, err := w.spill.Open(ctx, "ifps-session", w.spillID)
	if err != nil {
		return err
	}
	got, err = io.ReadAll(s)
	_ = s.Close()
	if err != nil || string(got) != w.spillBody {
		return fmt.Errorf("the owner read the spill file as %d bytes (%v)", len(got), err)
	}
	return nil
}

func (w *ifpsWorld) loosen(dir string) error {
	if dir != "spill" {
		return fmt.Errorf("unexpected directory %q", dir)
	}
	return ifpsLoosenDir(w.layout.Spill())
}

func ifpsNotPrivate(err error, name string) error {
	if err == nil {
		return errors.New("nothing was refused")
	}
	if !herr.Has(err, home.ReasonNotPrivate) {
		return fmt.Errorf("refused with %v, want %s", err, home.ReasonNotPrivate)
	}
	if !strings.Contains(err.Error(), name) {
		return fmt.Errorf("the refusal %q does not name %s", err, name)
	}
	return nil
}

func (w *ifpsWorld) spillRefuses(reason, name string) error {
	_, err := spilllocal.New(w.layout, spilllocal.Config{})
	if reason != string(home.ReasonNotPrivate) {
		return fmt.Errorf("unexpected reason %q", reason)
	}
	return ifpsNotPrivate(err, name)
}

func (w *ifpsWorld) homeRefuses(reason, name string) error {
	if reason != string(home.ReasonNotPrivate) {
		return fmt.Errorf("unexpected reason %q", reason)
	}
	return ifpsNotPrivate(home.Check(w.layout), name+"/")
}

// --- evidence --------------------------------------------------------------

var ifpsCommit = regexp.MustCompile("^`?[0-9a-f]{7,40}`?$")

// evidenceHonest: each in-scope lane row of the scenario either reads "not run"
// or "blocked" while the Stage 8.8 blockers stand, or reads "passed" with the
// commit, the environment and the artifacts of a real run (impl.md sec. 7.3).
func (w *ifpsWorld) evidenceHonest(id string) error {
	root, err := s18rRepoRoot()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(root, "docs", "stories", "harness-DSH-M2", "evidence", "DH-S-18.md"))
	if err != nil {
		return err
	}
	doc := string(raw)
	seen := map[string]bool{}
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, "| "+id+" |") {
			continue
		}
		cols := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		for i := range cols {
			cols[i] = strings.TrimSpace(cols[i])
		}
		if len(cols) != 8 {
			continue // a row of another table (the step table) names the id too
		}
		variant, env, commit, result, artifacts := cols[1], cols[4], cols[5], cols[6], cols[7]
		if variant != "Linux (WSL)" && variant != "Windows" {
			continue
		}
		seen[variant] = true
		switch {
		case strings.HasPrefix(result, "passed"):
			if !ifpsCommit.MatchString(commit) || env == "" || artifacts == "" {
				return fmt.Errorf("%s %s reads passed without a commit, environment and artifacts: %s", id, variant, line)
			}
		case strings.HasPrefix(result, "not run"), strings.HasPrefix(result, "blocked"):
			if !strings.Contains(doc, "## Stage 8.8 blockers") {
				return fmt.Errorf("%s %s is not run and the record names no Stage 8.8 blocker", id, variant)
			}
		default:
			return fmt.Errorf("%s %s has result %q", id, variant, result)
		}
	}
	if !seen["Linux (WSL)"] || !seen["Windows"] {
		return fmt.Errorf("the evidence lacks a Linux (WSL) or Windows row of %s (found %v)", id, seen)
	}
	return nil
}

func InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_instruction_files_and_private_storage_on_real_systems(ctx *godog.ScenarioContext) {
	var w *ifpsWorld
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		w = &ifpsWorld{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.close()
		return c, err
	})
	ctx.Step(`^git and an editor script are installed on this machine$`, func() error { return w.toolsInstalled() })
	ctx.Step(`^a real git repository on branch "([^"]*)" whose "([^"]*)" holds the section "([^"]*)"$`, func(b, n, s string) error { return w.repository(b, n, s) })
	ctx.Step(`^the branch "([^"]*)" of the repository adds the section "([^"]*)" to "([^"]*)"$`, func(b, s, n string) error { return w.branchAdds(b, s, n) })
	ctx.Step(`^the repository's "([^"]*)" is a link to an instruction file outside the repository$`, func(p string) error { return w.outsideLink(p) })
	ctx.Step(`^the instructions contributor follows a session working in "([^"]*)" of the repository$`, func(s string) error { return w.followSession(s) })
	ctx.Step(`^the first model request is assembled$`, func() error { return w.assemble() })
	ctx.Step(`^the next model request is assembled$`, func() error { return w.assemble() })
	ctx.Step(`^the request carries an? "([^"]*)" instructions message containing "([^"]*)"$`, func(f, s string) error { return w.carries(f, s) })
	ctx.Step(`^the instructions message contains "([^"]*)"$`, func(s string) error { return w.contains(s) })
	ctx.Step(`^the instructions message does not contain "([^"]*)"$`, func(s string) error { return w.lacks(s) })
	ctx.Step(`^the instructions message does not contain the outside file's text$`, func() error { return w.noOutside() })
	ctx.Step(`^the instructions message refuses "([^"]*)" with "([^"]*)"$`, func(p, r string) error { return w.refuses(p, r) })
	ctx.Step(`^the editor script adds the section "([^"]*)" to "([^"]*)"$`, func(s, n string) error { return w.editorAdds(s, n) })
	ctx.Step(`^the instructions contributor notices the change within (\d+) seconds$`, func(n int) error { return w.noticesWithin(n) })
	ctx.Step(`^the edit is committed and the branch "([^"]*)" is checked out$`, func(b string) error { return w.commitAndSwitch(b) })
	ctx.Step(`^the request carries no instructions message$`, func() error { return w.carriesNone() })
	ctx.Step(`^the DH-S-18 evidence claims no "([^"]*)" lane row it has not run$`, func(id string) error { return w.evidenceHonest(id) })

	ctx.Step(`^a second ordinary OS account on this machine$`, func() error { return w.secondAccount() })
	ctx.Step(`^a harness home created private by the harness in a directory both accounts can traverse$`, func() error { return w.privateHome() })
	ctx.Step(`^the second account can list the shared directory and read a file opened to everyone$`, func() error { return w.accountControl() })
	ctx.Step(`^the second account lists every directory and reads every file of the home$`, func() error { return w.accountReads() })
	ctx.Step(`^no denied read printed a stored name or stored bytes$`, func() error { return w.nothingLeaked() })
	ctx.Step(`^the attachment store keeps one attachment in the home$`, func() error { return w.keepAttachment() })
	ctx.Step(`^the spill store keeps one spill file in the home$`, func() error { return w.keepSpill() })
	ctx.Step(`^a principal that is not the home's owner is checked against every directory and file of the home$`, func() error { return w.strangerReads() })
	ctx.Step(`^the operating system denies every one of those reads$`, func() error { return w.allDenied() })
	ctx.Step(`^the same principal can read a control file the test opens to everyone$`, func() error { return w.controlReadable() })
	ctx.Step(`^the home's owner can still read the attachment and the spill file$`, func() error { return w.ownerReads() })
	ctx.Step(`^"([^"]*)" is opened to other accounts$`, func(d string) error { return w.loosen(d) })
	ctx.Step(`^the spill store refuses to start with "([^"]*)" naming "([^"]*)"$`, func(r, n string) error { return w.spillRefuses(r, n) })
	ctx.Step(`^the harness refuses the home with "([^"]*)" naming "([^"]*)"$`, func(r, n string) error { return w.homeRefuses(r, n) })
}

func TestE2E_dh_s_18_acceptance_on_real_operating_systems_instruction_files_and_private_storage_on_real_systems(t *testing.T) {
	tags := os.Getenv("IFPS_GODOG_TAGS")
	// A Windows run without the runbook's Windows account has DH-E-STORAGE-6 run
	// in WSL, with an account made there, instead of here.
	inWSL := runtime.GOOS == "windows" && tags == "" && os.Getenv("DSH_SCENARIO_STORAGE6_USER") == ""
	if inWSL {
		tags = "~" + ifpsSecondAccountTag
	}
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_instruction_files_and_private_storage_on_real_systems,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_18_acceptance_on_real_operating_systems_instruction_files_and_private_storage_on_real_systems.feature"},
			Tags:     tags,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("DH-S-18 Stage 8.8 scenarios failed")
	}
	if inWSL {
		t.Run("linux-wsl", ifpsWSL)
	}
}
