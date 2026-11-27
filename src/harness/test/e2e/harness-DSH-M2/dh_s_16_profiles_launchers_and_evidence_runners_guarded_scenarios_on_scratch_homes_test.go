//go:build e2e

package e2e

// Stage 6.12 (impl.md DH-S-16 step 16.8): the guarded scenarios, each on a
// scratch home of its own.
//
//   - DH-E-STORE-1 and DH-E-STORE-2 run the built dsh headless (testhook build,
//     keyless llm.replay route and the long fake tool testkit_sleep) as real OS
//     processes on one home, kill them without a clean stop and read the
//     embedded SQLite store back row by row.
//   - DH-E-STORE-6 runs the store's deletions in re-executed child processes
//     (service:os-process), one killed between two chunks.
//   - DH-E-FS-3 runs two re-executed edit processes over the real fs.local
//     provider with one shared lock directory, each edit a read and a
//     ReplaceIfVersion write (the edit tool's guard), with an editor save process
//     beside them. dsh's own fs.ops cannot boot in the binary yet
//     (internal/boot/builtin: "Not yet bootable"), so the edits do not go through
//     dsh's tool pipeline. DSH_SCENARIO_FS3_EDITOR, when set, is the lane's editor
//     save script ("<editor> <file> <log> <stop>", logging "<start> <end>
//     <sha256>" per save); otherwise the suite's own editor role saves the same
//     way.
//   - DH-E-STORE-2 plays the cut turn on the replay route, not the signed-in
//     Claude Code route. It also checks the guarded runner's lane plan, which
//     binds the live route and bwrap to the WSL lane.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

const (
	gshRoleEnv = "DSH_E2E_GSH_ROLE"
	gshArgsEnv = "DSH_E2E_GSH_ARGS" // tab-separated role arguments
	gshWithin  = 2 * time.Minute
	// gshEditsWithin is the budget of one process's 1,000 guarded edits: a lock,
	// a full hash and a rename each, slow on Windows (the impl's FS-3 allows 10m).
	gshEditsWithin = 10 * time.Minute
	gshChunk       = 100
	gshEdits       = 1000
	gshAnchor      = "ANCHOR"
	gshSleepLine   = "testkit_sleep: running"
	gshUnknown     = "is unknown: the turn was closed while it ran"
)

var (
	gshOuterT *testing.T
	gshBG     = context.Background()
	gshRunRe  = regexp.MustCompile(`dsh headless: run (ses_[0-9a-f]+) on route`)
)

// ---- re-executed children ----

func init() {
	role := os.Getenv(gshRoleEnv)
	if role == "" {
		return
	}
	if err := gshChild(role, strings.Split(os.Getenv(gshArgsEnv), "\t")); err != nil {
		fmt.Printf("child error %v\n", err)
		os.Exit(2)
	}
	os.Exit(0)
}

func gshChild(role string, a []string) error {
	switch role {
	case "delete": // home id stopAt (0: the whole deletion)
		stopAt, _ := strconv.ParseInt(a[2], 10, 64)
		s, err := sqlite.OpenHome(gshBG, home.New(a[0]), sqlite.WithKind("e2e-deleter"), sqlite.WithDeleteChunk(gshChunk))
		if err != nil {
			return err
		}
		if stopAt > 0 {
			sqlite.E2ESetAfterDeleteChunk(func(_ string, deleted int64) {
				if deleted >= stopAt {
					fmt.Printf("chunk deleted=%d\n", deleted)
					select {} // killed here, between two chunks
				}
			})
		}
		if err := s.Delete(gshBG, a[1], store.DeleteOptions{}); err != nil {
			return err
		}
		fmt.Println("deleted")
		return s.Close(gshBG)
	case "open": // home
		s, err := sqlite.OpenHome(gshBG, home.New(a[0]), sqlite.WithKind("e2e-reopen"))
		if err != nil {
			return err
		}
		fmt.Println("opened")
		return s.Close(gshBG)
	case "edit": // file locks versions name
		return gshEditChild(a[0], a[1], a[2], a[3])
	case "editor": // file log stop
		return gshEditorChild(a[0], a[1], a[2])
	}
	return fmt.Errorf("unknown role %q", role)
}

// gshEditChild is one process's edits: each reads the file and writes it back
// with its marker before the anchor, guarded by the version it read. It reports
// "ack <marker> <start> <end> <sha>" or "stale <marker>"; each acknowledged
// version is kept under versions by its SHA-256.
func gshEditChild(file, locks, versions, name string) error {
	l := local.NewWithOptions(local.Options{LockDir: locks})
	p := sandboxtest.Policy(sandbox.WorkspaceWrite, filepath.Dir(file))
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	for i := range gshEdits {
		marker := fmt.Sprintf("%s%04d", name, i)
		start := time.Now()
		var data []byte
		var info fs.Info
		var err error
		for try := 0; try < 200; try++ { // a read racing the editor's rename is retried; it changes nothing
			if data, info, err = gshRead(l, file); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if err != nil {
			return fmt.Errorf("read for %s: %v", marker, err)
		}
		next := strings.Replace(string(data), gshAnchor, marker+"\n"+gshAnchor, 1)
		w, err := l.Write(gshBG, fs.Ref{Path: file}, []byte(next), fs.ReplaceIfVersion{Version: info.Version}, p)
		end := time.Now()
		switch {
		case err == nil:
			sum := hex.EncodeToString(w.Version.Digest[:])
			if sum != gshSum([]byte(next)) {
				return fmt.Errorf("edit %s returned version %s, not its content's", marker, sum)
			}
			if err := os.WriteFile(filepath.Join(versions, sum), []byte(next), 0o600); err != nil {
				return err
			}
			fmt.Fprintf(out, "ack %s %d %d %s\n", marker, start.UnixNano(), end.UnixNano(), sum)
		case herr.Kind(err) == fs.CodeStaleVersion:
			fmt.Fprintf(out, "stale %s %d %d\n", marker, start.UnixNano(), end.UnixNano())
		default:
			fmt.Fprintf(out, "fail %s %v\n", marker, err)
		}
	}
	fmt.Fprintln(out, "done")
	return nil
}

func gshRead(l *local.FS, file string) ([]byte, fs.Info, error) {
	rc, info, err := l.Open(gshBG, fs.Ref{Path: file})
	if err != nil {
		return nil, info, err
	}
	defer rc.Close()
	var b strings.Builder
	_, err = bufio.NewReader(rc).WriteTo(&b)
	return []byte(b.String()), info, err
}

// gshEditorChild saves file as an editor does until stop exists: read it whole,
// write a temporary file beside it, rename it over the file.
func gshEditorChild(file, logPath, stop string) error {
	log, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer log.Close()
	for n := 0; ; n++ {
		if _, err := os.Stat(stop); err == nil {
			return nil
		}
		start := time.Now()
		data, err := os.ReadFile(file)
		if err != nil {
			time.Sleep(time.Millisecond)
			continue
		}
		tmp := fmt.Sprintf("%s.~%d", file, n)
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return err
		}
		for try := 0; ; try++ {
			if err = os.Rename(tmp, file); err == nil || try == 500 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(log, "%s %s %s\n", start.Format(time.RFC3339Nano), time.Now().Format(time.RFC3339Nano), gshSum(data))
		time.Sleep(2 * time.Millisecond)
	}
}

func gshSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// gshProc is a re-executed child reporting lines on its standard output.
type gshProc struct {
	cmd   *exec.Cmd
	lines chan string
	log   []string
}

func gshSpawn(role string, args ...string) (*gshProc, error) {
	cmd := exec.Command(os.Args[0], "-test.run=^$", "-test.count=1")
	cmd.Env = append(os.Environ(), gshRoleEnv+"="+role, gshArgsEnv+"="+strings.Join(args, "\t"))
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &gshProc{cmd: cmd, lines: make(chan string, 4096)}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			p.lines <- sc.Text()
		}
		close(p.lines)
	}()
	gshOuterT.Cleanup(p.kill)
	return p, nil
}

// expect is the child's next line starting with prefix.
func (p *gshProc) expect(prefix string) (string, error) { return p.expectWithin(prefix, gshWithin) }

func (p *gshProc) expectWithin(prefix string, within time.Duration) (string, error) {
	deadline := time.After(within)
	for {
		select {
		case line, ok := <-p.lines:
			if !ok {
				return "", fmt.Errorf("the child ended before %q:\n%s", prefix, strings.Join(p.log, "\n"))
			}
			p.log = append(p.log, line)
			if strings.HasPrefix(line, "child error") {
				return "", fmt.Errorf("the child failed: %s", line)
			}
			if strings.HasPrefix(line, prefix) {
				return line, nil
			}
		case <-deadline:
			return "", fmt.Errorf("no %q from the child within %s", prefix, within)
		}
	}
}

func (p *gshProc) kill() {
	if p.cmd.ProcessState == nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	}
}

// ---- the scenario world ----

type gshEvent struct {
	seq       int64
	typ, data string
}

type gshAck struct {
	what, sum, marker string
	start, end        int64
}

type gshWorld struct {
	root    string
	layout  home.Layout
	ws      string
	session string

	a, b    *launcher.Host
	aPid    int
	aEpoch  uint64
	aInst   string
	aEvents int

	before   []gshEvent
	requests string

	file, locks string
	acks, saves []gshAck
	versions    map[string]map[string]bool
	stale       []string
}

func gshBuilt() error {
	if shkBin == "" {
		mod, err := shkModuleDir()
		if err != nil {
			return err
		}
		shkBin = launcher.Build(gshOuterT, mod, "./cmd/dsh", "testhook")
	}
	return nil
}

func (w *gshWorld) scratchHome() error {
	w.layout = temphome.New(gshOuterT)
	w.root = w.layout.Root()
	w.ws = gshOuterT.TempDir()
	return nil
}

// overlay writes a launcher layer inserting llm.replay over a script of steps
// (each one assistant message block) and the long fake tool, and returns its path.
func gshOverlay(requests string, blocks ...map[string]any) (string, error) {
	dir := gshOuterT.TempDir()
	lines := []string{`{"dsh_export":1}`}
	for i, b := range blocks {
		data, err := json.Marshal(map[string]any{
			"message":        map[string]any{"id": fmt.Sprintf("gsh-msg-%s-%d", filepath.Base(dir), i), "role": "assistant", "blocks": []any{b}},
			"request_digest": "authored", "usage": map[string]int{"input": 1, "output": 1},
		})
		if err != nil {
			return "", err
		}
		line, err := json.Marshal(map[string]any{"seq": i, "type": "assistant/message", "pv": 4, "data": string(data)})
		if err != nil {
			return "", err
		}
		lines = append(lines, string(line))
	}
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return "", err
	}
	if requests == "" {
		requests = gshOuterT.TempDir()
	}
	layer := filepath.Join(gshOuterT.TempDir(), "overlay.yml")
	text := "- insert:\n    - id: replay\n      name: llm.replay\n      config:\n        scenario: " + strconv.Quote(dir) +
		"\n        mode: replay\n        route: gsh\n        model: replay\n        requests: " + strconv.Quote(requests) +
		"\n    - id: sleep\n      name: tools.testkit-sleep\n"
	return layer, os.WriteFile(layer, []byte(text), 0o600)
}

func gshSleepCall(id string) map[string]any {
	return map[string]any{"kind": "tool_use", "tool_use": map[string]any{"id": id, "name": "testkit_sleep", "args": map[string]any{}}}
}

func gshText(s string) map[string]any {
	return map[string]any{"kind": "text", "text": map[string]any{"text": s}}
}

func (w *gshWorld) argv(layer string, words ...string) []string {
	return append([]string{shkBin, "--home", w.root, "--layer", layer, "headless", "--json"}, words...)
}

// startLong starts a headless run whose turn runs testkit_sleep and returns once
// the tool body runs and its call is stored.
func (w *gshWorld) startLong(id string, words ...string) (*launcher.Host, error) {
	layer, err := gshOverlay("", gshSleepCall(id))
	if err != nil {
		return nil, err
	}
	h := launcher.Spawn(gshOuterT, launcher.Program{Argv: w.argv(layer, words...), Dir: w.ws, Env: shkKeylessEnv(os.Environ())})
	deadline := time.Now().Add(gshWithin)
	for !strings.Contains(h.Stderr.String(), gshSleepLine) {
		if time.Now().After(deadline) || h.Await(0) {
			return nil, fmt.Errorf("the long tool never ran (exit %d):\n%s", h.Process.ExitCode(), h.Stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if m := gshRunRe.FindStringSubmatch(h.Stderr.String()); m != nil && w.session == "" {
		w.session = m[1]
	}
	for {
		evs, err := w.events()
		if err == nil && slices.ContainsFunc(evs, func(e gshEvent) bool { return e.typ == "tool/call" && strings.Contains(e.data, id) }) {
			return h, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the tool call %s was never stored (%v)", id, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// refused runs a resume that must be refused session/owned naming pid.
func (w *gshWorld) refused(pid int) error {
	layer, err := gshOverlay("")
	if err != nil {
		return err
	}
	before, err := w.events()
	if err != nil {
		return err
	}
	h, err := launcher.Run(gshOuterT, gshWithin, launcher.Program{Argv: w.argv(layer, "--session", w.session, "Take over."), Dir: w.ws, Env: shkKeylessEnv(os.Environ())})
	if err != nil {
		return err
	}
	msg := h.Stderr.String()
	if code := h.Process.ExitCode(); code != 6 || !strings.Contains(msg, "session/owned") || !strings.Contains(msg, fmt.Sprintf("(pid %d,", pid)) {
		return fmt.Errorf("the resume exited %d; want 6 (busy) with session/owned naming pid %d:\n%s", code, pid, msg)
	}
	after, err := w.events()
	if err != nil {
		return err
	}
	if !slices.Equal(before, after) {
		return fmt.Errorf("the refused resume changed the log: %d events before, %d after", len(before), len(after))
	}
	return nil
}

func (w *gshWorld) events() ([]gshEvent, error) {
	db, err := sql.Open("sqlite", w.layout.DB())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rs, err := db.Query(`SELECT e.seq, e.type, e.data FROM events e JOIN sessions s ON s.uid = e.session WHERE s.id = ? ORDER BY e.seq`, w.session)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []gshEvent
	for rs.Next() {
		var e gshEvent
		if err := rs.Scan(&e.seq, &e.typ, &e.data); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rs.Err()
}

func (w *gshWorld) count(q string, args ...any) (int64, error) {
	db, err := sql.Open("sqlite", w.layout.DB())
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var n int64
	return n, db.QueryRow(q, args...).Scan(&n)
}

func (w *gshWorld) stat() (store.Summary, error) {
	s, err := sqlite.OpenHome(gshBG, w.layout, sqlite.WithKind("e2e-probe"))
	if err != nil {
		return store.Summary{}, err
	}
	sum, err := s.Stat(gshBG, w.session)
	return sum, errors.Join(err, s.Close(gshBG))
}

// ---- DH-E-STORE-1 ----

func (w *gshWorld) aCreates() (err error) {
	if w.a, err = w.startLong("gsh-a", "Run the long tool."); err != nil {
		return err
	}
	if w.session == "" {
		return errors.New("A named no session")
	}
	w.aPid = w.a.Process.Pid()
	sum, err := w.stat()
	if err != nil {
		return err
	}
	if sum.Holder == nil || sum.Holder.PID != w.aPid {
		return fmt.Errorf("the session's holder is %+v; want A (pid %d)", sum.Holder, w.aPid)
	}
	w.aEpoch, w.aInst = sum.LeaseEpoch, sum.Holder.Instance
	evs, err := w.events()
	w.aEvents = len(evs)
	return err
}

func (w *gshWorld) bResumesLive() error { return nil }

func (w *gshWorld) refusedNamingA(code string) error {
	if code != "session/owned" {
		return fmt.Errorf("unexpected refusal %q", code)
	}
	return w.refused(w.aPid)
}

func (w *gshWorld) logUnchanged() error {
	evs, err := w.events()
	if err == nil && len(evs) != w.aEvents {
		err = fmt.Errorf("%d events after the refusal; want A's %d", len(evs), w.aEvents)
	}
	return err
}

func (w *gshWorld) killAResumeB() (err error) {
	if err := w.a.Stop(); err != nil {
		return err
	}
	w.b, err = w.startLong("gsh-b", "--session", w.session, "Run the long tool again.")
	return err
}

func (w *gshWorld) bHoldsNextEpoch() error {
	sum, err := w.stat()
	if err != nil {
		return err
	}
	if sum.Holder == nil || sum.Holder.PID != w.b.Process.Pid() || sum.LeaseEpoch != w.aEpoch+1 {
		return fmt.Errorf("holder %+v at epoch %d; want B (pid %d) at epoch %d", sum.Holder, sum.LeaseEpoch, w.b.Process.Pid(), w.aEpoch+1)
	}
	return nil
}

func (w *gshWorld) cutTurnEnded(reason string) error {
	evs, err := w.events()
	if err != nil {
		return err
	}
	want := fmt.Sprintf(`"kind":%q`, reason)
	if !slices.ContainsFunc(evs, func(e gshEvent) bool { return e.typ == "turn/end" && strings.Contains(e.data, want) }) {
		return fmt.Errorf("no turn/end %s in the session's %d events", reason, len(evs))
	}
	return nil
}

func (w *gshWorld) aGone() error {
	if _, err := os.Stat(filepath.Join(w.layout.Run(), w.aInst+".lock")); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("A's lock file run/%s.lock: %v; want it removed", w.aInst, err)
	}
	if n, err := w.count(`SELECT count(*) FROM hosts WHERE instance = ?`, w.aInst); err != nil || n != 0 {
		return fmt.Errorf("%d hosts rows of A (%v); want none", n, err)
	}
	return nil
}

func (w *gshWorld) newARefused(code string) error {
	if code != "session/owned" {
		return fmt.Errorf("unexpected refusal %q", code)
	}
	return w.refused(w.b.Process.Pid())
}

func (w *gshWorld) lastResumes() error {
	if err := w.b.Stop(); err != nil {
		return err
	}
	layer, err := gshOverlay("", gshText("Recovered."))
	if err != nil {
		return err
	}
	h, err := launcher.Run(gshOuterT, gshWithin, launcher.Program{Argv: w.argv(layer, "--session", w.session, "Finish."), Dir: w.ws, Env: shkKeylessEnv(os.Environ())})
	if err != nil {
		return err
	}
	return gshCompleted(h, w.session)
}

func gshCompleted(h *launcher.Host, session string) error {
	out := strings.TrimSpace(h.Stdout.String())
	lines := strings.Split(out, "\n")
	var r struct{ Type, Session, Reason string }
	_ = json.Unmarshal([]byte(lines[len(lines)-1]), &r)
	if h.Process.ExitCode() != 0 || r.Type != "run/result" || r.Session != session || r.Reason != "completed" {
		return fmt.Errorf("run exited %d with %q; want a completed run/result on %s\nstderr:\n%s", h.Process.ExitCode(), lines[len(lines)-1], session, h.Stderr.String())
	}
	return nil
}

func (w *gshWorld) lastCompleted() error { return nil }

func (w *gshWorld) seqNoGap() error {
	evs, err := w.events()
	if err != nil {
		return err
	}
	for i, e := range evs {
		if e.seq != int64(i) {
			return fmt.Errorf("event %d has seq %d: the log has a gap", i, e.seq)
		}
	}
	if len(evs) == 0 {
		return errors.New("the session has no events")
	}
	return nil
}

// ---- DH-E-STORE-6 ----

func gshCall(seq store.Seq) (store.Record, error) {
	d, err := event.ToolCall.Draft(event.ToolCallPayload{CallID: fmt.Sprintf("c%d", seq), Name: "grep", Arguments: `{}`})
	if err != nil {
		return store.Record{}, err
	}
	return d.Stamp(seq, int64(seq)+1)
}

func gshSpilled(seq store.Seq, path string) (store.Record, error) {
	msg, err := vocab.RestoreMessage(fmt.Sprintf("r%d", seq), vocab.RoleTool, nil, vocab.ToolResultBlock(fmt.Sprintf("c%d", seq-1), false, vocab.TextPart("head")))
	if err != nil {
		return store.Record{}, err
	}
	d, err := event.ToolResult.Draft(event.Append(), event.ToolResultPayload{CallID: fmt.Sprintf("c%d", seq-1), Message: msg, Spill: &event.Spill{Path: path, Bytes: 5}})
	if err != nil {
		return store.Record{}, err
	}
	return d.Stamp(seq, int64(seq)+1)
}

func (w *gshWorld) spill(id string, names ...string) string {
	return filepath.Join(append([]string{w.layout.Spill(), sqlite.SpillName(id)}, names...)...)
}

func (w *gshWorld) seedSAndF() error {
	if err := w.scratchHome(); err != nil {
		return err
	}
	var recs []store.Record
	for i, name := range []string{"image-1.png", "image-2.png"} {
		c, err := gshCall(store.Seq(2 * i))
		if err != nil {
			return err
		}
		r, err := gshSpilled(store.Seq(2*i+1), sqlite.SpillName("S")+"/"+name)
		if err != nil {
			return err
		}
		recs = append(recs, c, r)
		temphome.Write(gshOuterT, w.spill("S", name), "blob "+name)
	}
	s, err := sqlite.OpenHome(gshBG, w.layout)
	if err != nil {
		return err
	}
	err = s.Seed(gshBG, store.Header{ID: "S"}, recs)
	if err == nil {
		var f store.Writer
		if f, err = s.Create(gshBG, store.Header{ID: "F"}, store.CreateOptions{ForkOf: &store.ForkOf{ID: "S", Through: 1}}); err == nil {
			err = f.Close(gshBG)
		}
	}
	return errors.Join(err, s.Close(gshBG))
}

func (w *gshWorld) deleteIn(id string) error {
	p, err := gshSpawn("delete", w.root, id, "0")
	if err != nil {
		return err
	}
	_, err = p.expect("deleted")
	return err
}

func (w *gshWorld) deleteS() error { return w.deleteIn("S") }
func (w *gshWorld) deleteF() error { return w.deleteIn("F") }

func (w *gshWorld) noRowsOf(ids ...string) error {
	for _, id := range ids {
		for _, q := range []string{
			`SELECT count(*) FROM sessions WHERE id = ?`,
			`SELECT count(*) FROM events WHERE session IN (SELECT uid FROM sessions WHERE id = ?)`,
			`SELECT count(*) FROM blob_refs WHERE session IN (SELECT uid FROM sessions WHERE id = ?)`,
			`SELECT count(*) FROM checkpoints WHERE session IN (SELECT uid FROM sessions WHERE id = ?)`,
			`SELECT count(*) FROM requests WHERE session = ?`,
		} {
			if n, err := w.count(q, id); err != nil || n != 0 {
				return fmt.Errorf("%q left %d rows (%v): %s", id, n, err, q)
			}
		}
	}
	return nil
}

func gshExists(path string) bool { _, err := os.Lstat(path); return err == nil }

func (w *gshWorld) sGone() error {
	if err := w.noRowsOf("S"); err != nil {
		return err
	}
	if gshExists(w.spill("S", "image-2.png")) {
		return errors.New("S's unshared blob image-2.png survived S's deletion")
	}
	return nil
}

func (w *gshWorld) fSurvives() error {
	if n, err := w.count(`SELECT count(*) FROM sessions WHERE id = 'F' AND state = 'active'`); err != nil || n != 1 {
		return fmt.Errorf("fork F: %d active rows (%v); want 1", n, err)
	}
	ref := "spill:" + sqlite.SpillName("S") + "/image-1.png"
	if n, err := w.count(`SELECT count(*) FROM blob_refs WHERE ref = ? AND session IN (SELECT uid FROM sessions WHERE id = 'F')`, ref); err != nil || n != 1 {
		return fmt.Errorf("F cites %s %d times (%v); want once", ref, n, err)
	}
	if !gshExists(w.spill("S", "image-1.png")) {
		return errors.New("the blob F shares with S was removed with S")
	}
	return nil
}

func (w *gshWorld) seedL(n int) error {
	recs := make([]store.Record, n)
	for i := range recs {
		var err error
		if recs[i], err = gshCall(store.Seq(i)); err != nil {
			return err
		}
	}
	s, err := sqlite.OpenHome(gshBG, w.layout)
	if err != nil {
		return err
	}
	err = s.Seed(gshBG, store.Header{ID: "L"}, recs)
	if err = errors.Join(err, s.Close(gshBG)); err != nil {
		return err
	}
	temphome.Write(gshOuterT, w.spill("L", "0001-out.txt"), "spilled")
	w.aEvents = n
	return nil
}

func (w *gshWorld) killedAfterChunk(chunk int) error {
	if chunk != gshChunk {
		return fmt.Errorf("the deleter deletes in chunks of %d", gshChunk)
	}
	p, err := gshSpawn("delete", w.root, "L", strconv.Itoa(chunk))
	if err != nil {
		return err
	}
	if _, err := p.expect("chunk deleted=" + strconv.Itoa(chunk)); err != nil {
		return err
	}
	p.kill()
	return nil
}

func (w *gshWorld) lTombstoned() error {
	if n, err := w.count(`SELECT count(*) FROM sessions WHERE id = 'L' AND state = 'deleting'`); err != nil || n != 1 {
		return fmt.Errorf("%d tombstones of L (%v); want 1", n, err)
	}
	left, err := w.count(`SELECT count(*) FROM events WHERE session IN (SELECT uid FROM sessions WHERE id = 'L')`)
	if err != nil || left != int64(w.aEvents-gshChunk) {
		return fmt.Errorf("%d events of L left after the kill (%v); want %d", left, err, w.aEvents-gshChunk)
	}
	return nil
}

func (w *gshWorld) reopen() error {
	p, err := gshSpawn("open", w.root)
	if err != nil {
		return err
	}
	_, err = p.expect("opened")
	return err
}

func (w *gshWorld) nothingOfSL() error {
	if err := w.noRowsOf("S", "L"); err != nil {
		return err
	}
	if gshExists(w.spill("L")) {
		return errors.New("L's spill directory survived its deletion")
	}
	if gshExists(w.spill("S", "image-2.png")) {
		return errors.New("S's unshared blob is back")
	}
	return nil
}

func (w *gshWorld) homeEmpty() error {
	for _, table := range []string{"sessions", "events", "blob_refs", "checkpoints", "requests"} {
		if n, err := w.count(`SELECT count(*) FROM ` + table); err != nil || n != 0 {
			return fmt.Errorf("%d %s rows left (%v)", n, table, err)
		}
	}
	entries, err := os.ReadDir(w.layout.Spill())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("spill files left: %v", entries)
	}
	return nil
}

func (w *gshWorld) checkClean() error {
	s, err := sqlite.OpenHome(gshBG, w.layout)
	if err != nil {
		return err
	}
	r, err := s.Check(gshBG)
	if err = errors.Join(err, s.Close(gshBG)); err != nil {
		return err
	}
	if !r.Clean() || len(r.Deleting) != 0 {
		return fmt.Errorf("Check = %+v; want clean, nothing being deleted", r)
	}
	return nil
}

// ---- DH-E-FS-3 ----

func (w *gshWorld) workspaceFile() error {
	w.ws, w.locks = gshOuterT.TempDir(), gshOuterT.TempDir()
	w.file = filepath.Join(w.ws, "shared.txt")
	return os.WriteFile(w.file, []byte(gshAnchor+"\n"), 0o600)
}

func (w *gshWorld) editorReady() error { return nil }

func (w *gshWorld) editsRun(n int) error {
	if n != gshEdits {
		return fmt.Errorf("each edit process issues %d edits", gshEdits)
	}
	versions := gshOuterT.TempDir()
	logPath, stop := filepath.Join(gshOuterT.TempDir(), "saves.log"), filepath.Join(gshOuterT.TempDir(), "stop")
	var editor *exec.Cmd
	if script := os.Getenv("DSH_SCENARIO_FS3_EDITOR"); script != "" {
		editor = exec.Command(script, w.file, logPath, stop)
	} else {
		editor = exec.Command(os.Args[0], "-test.run=^$", "-test.count=1")
		editor.Env = append(os.Environ(), gshRoleEnv+"=editor", gshArgsEnv+"="+strings.Join([]string{w.file, logPath, stop}, "\t"))
	}
	editor.Stdout, editor.Stderr = os.Stderr, os.Stderr
	if err := editor.Start(); err != nil {
		return fmt.Errorf("the editor save script (service:git-and-editor): %v", err)
	}
	editorDone := make(chan error, 1)
	go func() { editorDone <- editor.Wait() }()
	defer func() { _ = editor.Process.Kill() }()

	type result struct {
		lines []string
		err   error
	}
	results := make(chan result, 2)
	for _, name := range []string{"a", "b"} {
		p, err := gshSpawn("edit", w.file, w.locks, versions, name)
		if err != nil {
			return err
		}
		go func() {
			_, err := p.expectWithin("done", gshEditsWithin)
			results <- result{p.log, err}
		}()
	}
	var errs []error
	for range 2 {
		r := <-results
		errs = append(errs, r.err)
		for _, line := range r.lines {
			f := strings.Fields(line)
			switch f[0] {
			case "ack":
				s, _ := strconv.ParseInt(f[2], 10, 64)
				e, _ := strconv.ParseInt(f[3], 10, 64)
				w.acks = append(w.acks, gshAck{what: "edit " + f[1], marker: f[1], start: s, end: e, sum: f[4]})
			case "stale":
				w.stale = append(w.stale, f[1])
			case "fail":
				errs = append(errs, fmt.Errorf("an edit returned neither success nor %s: %s", fs.CodeStaleVersion, line))
			}
		}
	}
	if err := os.WriteFile(stop, nil, 0o600); err != nil {
		return err
	}
	select {
	case err := <-editorDone:
		errs = append(errs, err)
	case <-time.After(gshWithin):
		errs = append(errs, errors.New("the editor did not stop"))
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		s, err1 := time.Parse(time.RFC3339Nano, f[0])
		e, err2 := time.Parse(time.RFC3339Nano, f[1])
		if err := errors.Join(err1, err2); err != nil {
			return fmt.Errorf("editor log line %q: %v", line, err)
		}
		w.saves = append(w.saves, gshAck{what: "save", start: s.UnixNano(), end: e.UnixNano(), sum: f[2]})
	}
	w.requests = versions
	return nil
}

func (w *gshWorld) everyEditAnswered(code string) error {
	if got := len(w.acks) + len(w.stale); got != 2*gshEdits {
		return fmt.Errorf("%d edits answered %s or success; want %d", got, code, 2*gshEdits)
	}
	return nil
}

func (w *gshWorld) someStale(code string) error {
	gshOuterT.Logf("DH-E-FS-3: %d edits applied, %d refused %s, %d editor saves", len(w.acks), len(w.stale), code, len(w.saves))
	if len(w.stale) == 0 {
		return fmt.Errorf("no edit was refused %s: the processes never contended", code)
	}
	return nil
}

func (w *gshWorld) finalLines() ([]string, error) {
	data, err := os.ReadFile(w.file)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"), nil
}

func (w *gshWorld) finalWellFormed() error {
	lines, err := w.finalLines()
	if err != nil {
		return err
	}
	if lines[len(lines)-1] != gshAnchor {
		return fmt.Errorf("the final file does not end with the anchor: %q", lines[len(lines)-1])
	}
	seen := map[string]bool{}
	for _, l := range lines[:len(lines)-1] {
		if seen[l] || len(l) != 5 {
			return fmt.Errorf("line %q of the final file is not a unique marker", l)
		}
		seen[l] = true
	}
	for _, m := range w.stale {
		if seen[m] {
			return fmt.Errorf("edit %s was refused but its marker is in the file", m)
		}
	}
	return nil
}

// version is the set of marker lines of the acknowledged version sum (or the
// initial file); ok is false for a version no process acknowledged.
func (w *gshWorld) version(sum string) (lines map[string]bool, ok bool) {
	if v, ok := w.versions[sum]; ok {
		return v, v != nil
	}
	text, err := gshAnchor+"\n", error(nil)
	if sum != gshSum([]byte(text)) {
		var b []byte
		b, err = os.ReadFile(filepath.Join(w.requests, sum))
		text = string(b)
	}
	if err == nil {
		lines = map[string]bool{}
		for _, l := range strings.Split(text, "\n") {
			lines[l] = true
		}
	}
	if w.versions == nil {
		w.versions = map[string]map[string]bool{}
	}
	w.versions[sum] = lines
	return lines, lines != nil
}

func (w *gshWorld) savesKnown() error {
	for _, s := range w.saves {
		if _, ok := w.version(s.sum); !ok {
			return fmt.Errorf("an editor save wrote %s, a version no process acknowledged", s.sum)
		}
	}
	return nil
}

func (w *gshWorld) noSilentLoss() error {
	lines, err := w.finalLines()
	if err != nil {
		return err
	}
	for _, a := range w.acks {
		if slices.Contains(lines, a.marker) {
			continue
		}
		explained := slices.ContainsFunc(w.saves, func(s gshAck) bool {
			if s.start >= a.end || s.end <= a.start {
				return false
			}
			v, _ := w.version(s.sum)
			return !v[a.marker]
		})
		if !explained {
			return fmt.Errorf("edit %s was acknowledged and is missing, and no overlapping editor save wrote a version without it", a.marker)
		}
	}
	return nil
}

func (w *gshWorld) finalIsLast() error {
	data, err := os.ReadFile(w.file)
	if err != nil {
		return err
	}
	final := gshSum(data)
	all := append(slices.Clone(w.acks), w.saves...)
	lastStart := int64(0)
	for _, o := range all {
		lastStart = max(lastStart, o.start)
	}
	for _, c := range all {
		if c.sum == final && lastStart <= c.end {
			gshOuterT.Logf("DH-E-FS-3 evidence: final file %s, last written by %s", final, c.what)
			return nil
		}
	}
	return fmt.Errorf("the final file %s is no last acknowledged version", final)
}

// ---- DH-E-STORE-2 ----

func (w *gshWorld) killedMidTool() (err error) {
	if w.a, err = w.startLong("gsh-cut", "Run the sandboxed sleep, then write."); err != nil {
		return err
	}
	if w.session == "" {
		return errors.New("the run named no session")
	}
	if w.before, err = w.events(); err != nil {
		return err
	}
	return w.a.Stop()
}

func (w *gshWorld) resumedAndPrompted() error {
	w.requests = gshOuterT.TempDir()
	layer, err := gshOverlay(w.requests, gshText("Recovered."))
	if err != nil {
		return err
	}
	h, err := launcher.Run(gshOuterT, gshWithin, launcher.Program{Argv: w.argv(layer, "--session", w.session, "What happened?"), Dir: w.ws, Env: shkKeylessEnv(os.Environ())})
	if err != nil {
		return err
	}
	return gshCompleted(h, w.session)
}

func (w *gshWorld) prefixKept() error {
	evs, err := w.events()
	if err != nil {
		return err
	}
	if len(evs) <= len(w.before) || !slices.Equal(evs[:len(w.before)], w.before) {
		return fmt.Errorf("the %d events stored before the kill are not an unchanged prefix of the %d after", len(w.before), len(evs))
	}
	return w.seqNoGap()
}

func (w *gshWorld) outcomeUnknown(code string) error {
	evs, err := w.events()
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(evs[len(w.before):], func(e gshEvent) bool {
		return e.typ == "tool/result" && strings.Contains(e.data, code) && strings.Contains(e.data, gshUnknown)
	}) {
		return fmt.Errorf("no tool/result %s after the kill", code)
	}
	return nil
}

func (w *gshWorld) firstRequestCarries() error {
	b, err := os.ReadFile(filepath.Join(w.requests, "000.json"))
	if err != nil {
		return fmt.Errorf("the replay recorded no first request: %v", err)
	}
	if !strings.Contains(string(b), gshUnknown) {
		return fmt.Errorf("the first request after the resume does not carry the unknown outcome:\n%s", b)
	}
	return nil
}

func (w *gshWorld) deleteSession() error {
	s, err := sqlite.OpenHome(gshBG, w.layout)
	if err != nil {
		return err
	}
	return errors.Join(s.Delete(gshBG, w.session, store.DeleteOptions{}), s.Close(gshBG))
}

func (w *gshWorld) sessionGone() error {
	if err := w.noRowsOf(w.session); err != nil {
		return err
	}
	if gshExists(w.spill(w.session)) {
		return errors.New("the deleted session's spill directory remains")
	}
	return nil
}

const gshProbe = `package probe

import (
	"fmt"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/guarded"
)

func TestProbe(t *testing.T) {
	ids := []string{"DH-E-STORE-1", "DH-E-STORE-6", "DH-E-FS-3", "DH-E-STORE-2"}
	for _, l := range []guarded.Lane{guarded.WSL, guarded.Windows} {
		run, out := guarded.Select(l, ids)
		fmt.Printf("GSH\t%s run %s\n", l, strings.Join(run, ","))
		for _, e := range out {
			fmt.Printf("GSH\t%s out %s %s\n", l, e.ID, e.Result())
		}
	}
	fmt.Printf("GSH\tsignin %s\n", guarded.SignIn["DH-E-STORE-2"])
}
`

func (w *gshWorld) lanePlan(row string) error {
	mod, err := shkModuleDir()
	if err != nil {
		return err
	}
	gate := filepath.Join(mod, "..", "..", "tools", "harness-gate")
	tmp := gshOuterT.TempDir()
	gomod := fmt.Sprintf("module dsh.e2e/gshprobe\n\ngo 1.27.4\n\nrequire github.com/smartpcr/agent-plugin/tools/harness-gate v0.0.0\n\nreplace github.com/smartpcr/agent-plugin/tools/harness-gate => %s\n",
		strconv.Quote(filepath.ToSlash(gate)))
	sum, err := os.ReadFile(filepath.Join(gate, "go.sum"))
	if err != nil {
		return err
	}
	for name, b := range map[string][]byte{"go.mod": []byte(gomod), "go.sum": sum, "probe_test.go": []byte(gshProbe)} {
		if err := os.WriteFile(filepath.Join(tmp, name), b, 0o644); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(gshBG, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-run", "^TestProbe$", ".")
	cmd.Dir = tmp
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("the guarded lane plan probe: %v\n%s", err, out)
	}
	var got []string
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "GSH\t"); ok {
			got = append(got, rest)
		}
	}
	want := []string{
		"wsl run DH-E-STORE-1,DH-E-STORE-6,DH-E-FS-3,DH-E-STORE-2",
		"windows run DH-E-STORE-1,DH-E-STORE-6,DH-E-FS-3",
		"windows out DH-E-STORE-2 " + row,
		"signin claude-session",
	}
	if !slices.Equal(got, want) {
		return fmt.Errorf("the guarded lane plan is\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	return nil
}

func InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_guarded_scenarios_on_scratch_homes(ctx *godog.ScenarioContext) {
	w := &gshWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = gshWorld{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		for _, h := range []*launcher.Host{w.a, w.b} {
			if h != nil {
				_ = h.Stop()
			}
		}
		return c, nil
	})

	ctx.Step(`^dsh built from this checkout with the keyless replay route$`, gshBuilt)
	ctx.Step(`^a scratch home with its embedded SQLite store$`, w.scratchHome)

	ctx.Step(`^headless host A creates a session and runs a long turn$`, w.aCreates)
	ctx.Step(`^headless host B resumes A's live session$`, w.bResumesLive)
	ctx.Step(`^B is refused "([^"]*)" naming A's pid$`, w.refusedNamingA)
	ctx.Step(`^the refused resume appended nothing to the session log$`, w.logUnchanged)
	ctx.Step(`^A is killed without a clean stop and B resumes the session with a long turn$`, w.killAResumeB)
	ctx.Step(`^B holds the session with lease epoch one past A's$`, w.bHoldsNextEpoch)
	ctx.Step(`^the turn cut by the kill ended "([^"]*)"$`, w.cutTurnEnded)
	ctx.Step(`^A's lock file and hosts row are gone$`, w.aGone)
	ctx.Step(`^a new copy of A resumes the session$`, func() error { return nil })
	ctx.Step(`^it is refused "([^"]*)" naming B's pid$`, w.newARefused)
	ctx.Step(`^B is killed and a last headless host resumes and prompts the session$`, w.lastResumes)
	ctx.Step(`^the last run completes on the session$`, w.lastCompleted)
	ctx.Step(`^the session's seq runs 0\.\.n with no gap$`, w.seqNoGap)

	ctx.Step(`^a scratch home holding session S whose tool results spilled two blobs, and a fork F of S that cites the first$`, w.seedSAndF)
	ctx.Step(`^a separate process deletes S$`, w.deleteS)
	ctx.Step(`^S has no row and its unshared blob is gone$`, w.sGone)
	ctx.Step(`^F and the blob it shares with S survive$`, w.fSurvives)
	ctx.Step(`^a (\d+)-event session L with a spill file in the scratch home$`, w.seedL)
	ctx.Step(`^a process deleting L in chunks of 100 events is killed after its first chunk$`, func() error { return w.killedAfterChunk(gshChunk) })
	ctx.Step(`^L is tombstoned with its remaining events still stored$`, w.lTombstoned)
	ctx.Step(`^a new process opens the store$`, w.reopen)
	ctx.Step(`^no row, blob reference or spill file of S or L remains$`, w.nothingOfSL)
	ctx.Step(`^a separate process deletes F$`, w.deleteF)
	ctx.Step(`^the scratch home holds no session, event, blob reference or spill file$`, w.homeEmpty)
	ctx.Step(`^the store's integrity check reports clean$`, w.checkClean)

	ctx.Step(`^a scratch workspace file ending in the anchor line and one lock directory both processes share$`, w.workspaceFile)
	ctx.Step(`^the editor save script \(DSH_SCENARIO_FS3_EDITOR, else the suite's own editor save process\)$`, w.editorReady)
	ctx.Step(`^two processes each drive (\d+) version-guarded edits on the file while the editor saves it$`, w.editsRun)
	ctx.Step(`^every edit either took effect or returned "([^"]*)"$`, w.everyEditAnswered)
	ctx.Step(`^at least one conflicting edit was refused "([^"]*)"$`, w.someStale)
	ctx.Step(`^the final file is unique marker lines then the anchor, holding no refused edit's marker$`, w.finalWellFormed)
	ctx.Step(`^every editor save wrote back a version some process acknowledged$`, w.savesKnown)
	ctx.Step(`^every acknowledged edit missing from the final file was overwritten by an overlapping editor save of a version without it$`, w.noSilentLoss)
	ctx.Step(`^the final file equals the last acknowledged version$`, w.finalIsLast)

	ctx.Step(`^a headless host's turn is running a tool call and the host and its children are killed$`, w.killedMidTool)
	ctx.Step(`^a new headless host resumes the session and sends a new prompt$`, w.resumedAndPrompted)
	ctx.Step(`^every event persisted before the kill is unchanged and seq continues with no gap$`, w.prefixKept)
	ctx.Step(`^the cut tool call's result is "([^"]*)"$`, w.outcomeUnknown)
	ctx.Step(`^the first model request after the resume, read from the replay's request log, carries that result$`, w.firstRequestCarries)
	ctx.Step(`^the session is deleted with no host running$`, w.deleteSession)
	ctx.Step(`^no row or spill file of the session remains in the scratch home$`, w.sessionGone)
	ctx.Step(`^the guarded runner plans DH-E-STORE-2 on the WSL lane only, behind the Claude Code sign-in, and records the Windows row "([^"]*)"$`, w.lanePlan)
}

func TestE2E_dh_s_16_profiles_launchers_and_evidence_runners_guarded_scenarios_on_scratch_homes(t *testing.T) {
	if os.Getenv(gshRoleEnv) != "" || os.Getenv(shkChildEnv) != "" {
		return
	}
	gshOuterT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_guarded_scenarios_on_scratch_homes,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_16_profiles_launchers_and_evidence_runners_guarded_scenarios_on_scratch_homes.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
