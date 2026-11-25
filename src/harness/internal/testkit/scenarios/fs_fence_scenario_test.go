//go:build scenario

package scenarios

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// fs1Calls is the number of write, edit and apply_patch calls per filesystem
// (areas/filesystem.md DH-E-FS-1): whole cycles of three, at least 10,000.
const fs1Calls = 10_000

// fs1Batch is the number of calls one prompt's turn carries; the layer's step
// budget must allow it.
const fs1Batch = 99

// TestFenceHoldsUnderALinkSwap: the fence holds on real filesystems while a path
// is being swapped (areas/filesystem.md DH-E-FS-1; DH-D-FS-4; implementation-plan
// Stage 8.x, e2e-scenarios.md TC-21s3). Authored in DH-S-12 (implementation-plan
// Stage 2.8); executed in DH-S-18 on Linux ext4, Windows NTFS and an SMB share
// (macOS out of scope by decision platforms-windows-wsl). It needs the headless
// SDK launcher (DH-S-16).
//
// Given, for each directory of DSH_SCENARIO_FS1_DIRS (one per filesystem), the
// built dsh with a fresh home, a session in workspace-write whose working
// directory is a project W (with a sub-directory W/sub) and one extra write root
// X, an outside directory O seeded with files and listed by hash, and a helper
// process that swaps W/sub for a link to O in a tight loop (a symbolic link, or a
// junction where Windows refuses one, recorded in the log); when a direct caller
// issues 10,000 write, edit and apply_patch calls into W/sub through the tool
// pipeline, then repeats one write under read-only and, in a new
// workspace-write session whose client answers allowed-once, one plain write to
// a directory E outside every root followed by its escalated retry (the same
// arguments plus sandbox_permissions danger-full-access and a justification,
// policy.Resolver.Escalate A17); then the hash listing of O is unchanged, every
// refused call returns fs/sandbox-denied and no staged file is left in W, X or
// O, the read-only write is refused, the plain write to E is refused
// fs/sandbox-denied with no approval asked, and the retry asks exactly one
// approval (naming its call ID, its reason showing the justification), succeeds
// and is logged with that approval.
//
// The direct caller is the scripted model endpoint (scripted_model_test.go):
// each scripted call goes through dsh's whole pipeline as a model's would.
// Operator decision fs-scripted-model-vs-replay: it switches to the replay
// provider when DH-S-16 lands. Operator decision fs1-transient-refusal-codes:
// while the link is being swapped a call may also find W/sub missing or not a
// directory for an instant, so fs/not-found and fs/not-directory are accepted as
// refusals beside fs/sandbox-denied; any other refusal fails, except a later call
// of a cycle whose earlier call was refused (its file is then not in the state
// the call expects), which is counted and logged. At least one call must have
// been refused fs/sandbox-denied: a run in which the swap never raced the seam
// proved nothing.
//
// Cleanup: the helper is stopped and every temporary directory removed; when O
// changed, the stage stops (M2-R-03), both listings and the session log are kept
// and their paths logged.
//
// Inputs: DSH_SCENARIO_FS1_DIRS the ';'-separated base directories, one per
// local filesystem under test (ext4 on the Linux leg, NTFS on the Windows leg);
// DSH_SCENARIO_FS1_SHARE a base directory on the SMB share; DSH_SCENARIO_FS1_LAYER a configuration layer, a template
// over {endpoint} (the scripted endpoint's root) and {extra} (X), that registers
// route DSH_SCENARIO_FS1_ROUTE on {endpoint} with a scenario-local key, enables
// the six file tools, adds {extra} as a write root and allows fs1Batch+1 steps a
// turn, with no title route; DSH_SCENARIO_FS1_ROUTE and DSH_SCENARIO_FS1_MODEL the
// route and model a session names.
//
//dsh:scenario DH-E-FS-1
func TestFenceHoldsUnderALinkSwap(t *testing.T) {
	if os.Getenv("DSH_SCENARIO_FS1_ROLE") == "swapper" {
		fs1Swapper(t)
		return
	}
	dirs := append(needList(t, "DSH_SCENARIO_FS1_DIRS", "one base directory on each local filesystem under test (ext4, NTFS)"),
		need(t, "DSH_SCENARIO_FS1_SHARE", "a base directory on the SMB share"))
	layerT := need(t, "DSH_SCENARIO_FS1_LAYER", "a layer over {endpoint} and {extra} with the scripted route, the file tools and one extra root")
	route := &protocol.ModelRoute{
		Provider: need(t, "DSH_SCENARIO_FS1_ROUTE", "the route the layer registers on the scripted endpoint"),
		Model:    need(t, "DSH_SCENARIO_FS1_MODEL", "a model of that route"),
	}
	bin := dshBinary(t)
	for i, base := range dirs {
		t.Run(fmt.Sprintf("fs%d", i+1), func(t *testing.T) { fs1On(t, bin, base, layerT, route) })
	}
}

// fs1On is DH-E-FS-1 on the filesystem of base.
func fs1On(t *testing.T, bin, base, layerT string, route *protocol.ModelRoute) {
	t.Logf("filesystem under test: %s", base)
	scratch, err := os.MkdirTemp(base, "dsh-fs1-")
	if err != nil {
		t.Fatal(err)
	}
	ws, extra, outside, escalate := filepath.Join(scratch, "W"), filepath.Join(scratch, "X"), filepath.Join(scratch, "O"), filepath.Join(scratch, "E")
	sub := filepath.Join(ws, "sub")
	for _, d := range []string{filepath.Join(ws, ".git"), sub, extra, outside, escalate} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 8 {
		if err := os.WriteFile(filepath.Join(outside, fmt.Sprintf("keep%d.txt", i)), []byte(fmt.Sprintf("outside %d\n", i)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := hashListing(t, outside)
	keepListings(t, "fs1-outside", before, func() map[string]string { return hashListing(t, outside) })
	t.Cleanup(func() {
		if t.Failed() && len(listingDiff(before, hashListing(t, outside))) > 0 {
			t.Logf("an outside file changed: the stage stops (M2-R-03); %s is kept", scratch)
			return
		}
		_ = os.RemoveAll(scratch)
	})

	m := startScriptedModel(t)
	layer := writeLayer(t, expand(layerT, map[string]string{"endpoint": m.URL, "extra": filepath.ToSlash(extra)}))
	h := startSDK(t, bin, nil, []string{"--layer", layer})
	session := h.createIn(t, route, ws, "workspace-write")
	h.keepExport(t, session)

	stop := fs1StartSwapper(t, sub, outside)

	cycles := (fs1Calls + 2) / 3
	var outcomes []*toolOutcome
	for first := 0; first < cycles; first += fs1Batch / 3 {
		for c := first; c < min(first+fs1Batch/3, cycles); c++ {
			m.script(fs1Cycle(sub, c)...)
		}
		outcomes = append(outcomes, toolOutcomes(t, h.runPrompt(t, session, "Run the scripted calls.", 10*time.Minute))...)
		if n := m.pending(); n != 0 {
			t.Fatalf("the turn ended with %d scripted calls not made", n)
		}
	}
	swaps, kind := stop()
	t.Logf("the helper swapped W/sub %d times using a %s", swaps, kind)

	if len(outcomes) != cycles*3 {
		t.Fatalf("%d tool calls in the log, %d scripted", len(outcomes), cycles*3)
	}
	var denied, transient, dependent int
	refusedIn := map[string]bool{} // cycle tag -> an earlier call of it was refused
	for _, o := range outcomes {
		c, ok := m.call(o.ID)
		if !ok {
			t.Fatalf("call %s (%s) was not scripted", o.ID, o.Name)
		}
		cycle, _, _ := strings.Cut(c.Tag, "/")
		if !o.IsError {
			continue
		}
		switch {
		case o.has(codeSandboxDenied):
			denied++
		case o.has(codeNotFound), o.has(codeNotDirectory):
			transient++
		case refusedIn[cycle]:
			dependent++
			t.Logf("call %s (%s) of a cycle with an earlier refusal failed: %s", c.Tag, o.Name, o.Raw)
		default:
			t.Fatalf("call %s (%s) was refused with neither %s nor a transient code: %s", c.Tag, o.Name, codeSandboxDenied, o.Raw)
		}
		refusedIn[cycle] = true
	}
	t.Logf("%d calls: %d refused %s, %d refused transiently, %d dependent failures", len(outcomes), denied, codeSandboxDenied, transient, dependent)
	if diff := listingDiff(before, hashListing(t, outside)); len(diff) > 0 {
		t.Fatalf("files outside every root changed: %v", diff)
	}
	if left := stagedFiles(t, ws, extra, outside); len(left) > 0 {
		t.Fatalf("staged files left behind: %v", left)
	}
	if denied == 0 {
		t.Fatalf("no call was refused %s in %d swaps: the swap never raced the seam", codeSandboxDenied, swaps)
	}

	// read-only: one write inside W is refused.
	ro := h.createIn(t, route, ws, "read-only")
	h.keepExport(t, ro)
	roPath := filepath.Join(ws, "read-only.txt")
	m.script(scriptedCall{Name: "write", Args: map[string]any{"file_path": roPath, "content": "read-only\n"}, Tag: "read-only"})
	got := toolOutcomes(t, h.runPrompt(t, ro, "Run the scripted call.", 5*time.Minute))
	if len(got) != 1 || !got[0].IsError || !got[0].has(codeSandboxDenied) {
		t.Fatalf("the read-only write answered %+v, want one call refused %s", got, codeSandboxDenied)
	}
	if _, err := os.Lstat(roPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists after a refused read-only write (%v)", roPath, err)
	}

	fs1Escalation(t, bin, layer, route, m, ws, escalate)
}

// fs1Cycle is one cycle of three calls on its own file of sub: a write, an edit
// and a patch, each expecting the content the previous one left.
func fs1Cycle(sub string, c int) []scriptedCall {
	name := fmt.Sprintf("c%05d.txt", c)
	path := filepath.Join(sub, name)
	tag := strconv.Itoa(c)
	patch := "*** Begin Patch\n*** Update File: " + filepath.ToSlash(path) + "\n@@\n-line-b\n+line-c\n*** End Patch\n"
	return []scriptedCall{
		{Name: "write", Args: map[string]any{"file_path": path, "content": "line-a\n"}, Tag: tag + "/write"},
		{Name: "edit", Args: map[string]any{"file_path": path, "old_string": "line-a", "new_string": "line-b"}, Tag: tag + "/edit"},
		{Name: "apply_patch", Args: map[string]any{"input": patch}, Tag: tag + "/apply_patch"},
	}
}

// fs1Justification is the justification the escalated retry carries; its
// approval's reason must show it.
const fs1Justification = "DH-E-FS-1 escalation leg: write one file outside every root"

// fs1Escalation is the escalated leg (policy.Resolver.Escalate, A17): in a
// workspace-write session whose client answers every approval allowed-once, a
// plain write to E, outside every root, is refused fs/sandbox-denied with no
// approval asked; the retry with the same arguments plus sandbox_permissions
// danger-full-access and a justification asks exactly one approval, naming the
// retry's call ID and showing the justification, then succeeds, and the log
// records that approval.
func fs1Escalation(t *testing.T, bin, layer string, route *protocol.ModelRoute, m *scriptedModel, ws, escalate string) {
	t.Helper()
	var mu sync.Mutex
	var asked []protocol.ApprovalRequest
	allow := sdkOptions{Approve: func(r protocol.ApprovalRequest) protocol.ApprovalResult {
		mu.Lock()
		asked = append(asked, r)
		mu.Unlock()
		return protocol.ApprovalResult{Outcome: "allowed-once"}
	}}
	h := startSDKWith(t, bin, nil, []string{"--layer", layer}, allow)
	session := h.createIn(t, route, ws, "workspace-write")
	h.keepExport(t, session)
	path := filepath.Join(escalate, "escalated.txt")
	m.script(
		scriptedCall{Name: "write", Args: map[string]any{"file_path": path, "content": "escalated\n"}, Tag: "escalation/denied"},
		scriptedCall{Name: "write", Args: map[string]any{
			"file_path":           path,
			"content":             "escalated\n",
			"sandbox_permissions": "danger-full-access",
			"justification":       fs1Justification,
		}, Tag: "escalation/retry"},
	)
	evs := h.runPrompt(t, session, "Run the scripted calls.", 5*time.Minute)
	if n := m.pending(); n != 0 {
		t.Fatalf("the escalation turn ended with %d scripted calls not made", n)
	}
	got := toolOutcomes(t, evs)
	if len(got) != 2 {
		t.Fatalf("the escalation leg answered %+v, want the denied write and its retry", got)
	}
	denied, retry := got[0], got[1]
	if !denied.IsError || !denied.has(codeSandboxDenied) {
		t.Fatalf("the plain write to E answered %+v, want it refused %s", denied, codeSandboxDenied)
	}
	if retry.IsError {
		t.Fatalf("the escalated retry answered %+v, want it to succeed", retry)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "escalated\n" {
		t.Fatalf("%s is %q (%v) after the escalated retry", path, data, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(asked) != 1 || asked[0].CallID != retry.ID {
		t.Fatalf("approvals asked %+v, want exactly one, for the escalated retry %s (none for the denied call %s)", asked, retry.ID, denied.ID)
	}
	if !strings.Contains(asked[0].Reason, fs1Justification) {
		t.Fatalf("the approval of %s has reason %q, want it to show the justification %q", retry.ID, asked[0].Reason, fs1Justification)
	}
	t.Logf("escalation reason: %q", asked[0].Reason)
	logged := false
	for _, e := range evs {
		if strings.HasPrefix(e.Type, "approval/") && strings.Contains(string(e.Data), retry.ID) {
			logged = true
		}
	}
	if !logged {
		t.Fatalf("no approval/ event of the run names the escalated retry %s", retry.ID)
	}
}

// fs1StartSwapper starts the helper process that swaps sub for a link to target
// until the returned stop is called; stop reports the swaps and the link kind.
func fs1StartSwapper(t *testing.T, sub, target string) (stop func() (int, string)) {
	t.Helper()
	stopFile := filepath.Join(t.TempDir(), "stop")
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestFenceHoldsUnderALinkSwap$", "-test.v")
	cmd.Env = append(os.Environ(),
		"DSH_SCENARIO_FS1_ROLE=swapper", "DSH_SCENARIO_FS1_SUB="+sub,
		"DSH_SCENARIO_FS1_TARGET="+target, "DSH_SCENARIO_FS1_STOP="+stopFile)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var (
		once  sync.Once
		swaps int
		kind  = "unknown link"
		lines []string
	)
	read := make(chan struct{})
	go func() {
		defer close(read)
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			line := sc.Text()
			lines = append(lines, line)
			if v, ok := strings.CutPrefix(line, "fs1-swapper kind: "); ok {
				kind = v
			}
			if v, ok := strings.CutPrefix(line, "fs1-swapper swaps: "); ok {
				swaps, _ = strconv.Atoi(v)
			}
		}
	}()
	stop = func() (int, string) {
		once.Do(func() {
			_ = os.WriteFile(stopFile, nil, 0o600)
			<-read
			if err := cmd.Wait(); err != nil {
				t.Errorf("the swap helper: %v\n%s", err, strings.Join(lines, "\n"))
			}
		})
		return swaps, kind
	}
	t.Cleanup(func() {
		stop()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
	})
	return stop
}

// fs1Swapper is the helper role: until the stop file exists, move sub aside, put a
// link to target in its place, then remove the link and move sub back.
func fs1Swapper(t *testing.T) {
	sub := need(t, "DSH_SCENARIO_FS1_SUB", "the directory to swap")
	target := need(t, "DSH_SCENARIO_FS1_TARGET", "the outside directory the link points at")
	stopFile := need(t, "DSH_SCENARIO_FS1_STOP", "the file whose existence stops the helper")
	aside := sub + ".real"
	link, kind := fs1Linker(t, sub, target, aside)
	fmt.Printf("fs1-swapper kind: %s\n", kind)
	swaps := 0
	for {
		if _, err := os.Stat(stopFile); err == nil {
			break
		}
		// A step that fails (a handle the seam holds open on Windows) is retried
		// on the next round; the directory is always restored before stopping.
		if err := os.Rename(sub, aside); err != nil {
			continue
		}
		if err := link(); err == nil {
			swaps++
			_ = os.Remove(sub)
		}
		for os.Rename(aside, sub) != nil {
			// The seam creates a missing parent, so sub may now be a real
			// directory; a link (never followed here) is only unlinked.
			if fi, err := os.Lstat(sub); err == nil && fi.Mode().IsDir() {
				_ = os.RemoveAll(sub)
			} else {
				_ = os.Remove(sub)
			}
		}
	}
	fmt.Printf("fs1-swapper swaps: %d\n", swaps)
}

// fs1Linker returns the step that puts a link to target at sub: a symbolic link,
// or on Windows a junction when the user may not create one.
func fs1Linker(t *testing.T, sub, target, aside string) (func() error, string) {
	t.Helper()
	if err := os.Rename(sub, aside); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(sub)
		if err := os.Rename(aside, sub); err != nil {
			t.Fatal(err)
		}
	}()
	if err := os.Symlink(target, sub); err == nil {
		return func() error { return os.Symlink(target, sub) }, "symbolic link"
	} else if runtime.GOOS != "windows" {
		t.Fatalf("symbolic link: %v", err)
	}
	junction := func() error { return exec.Command("cmd", "/C", "mklink", "/J", sub, target).Run() }
	if err := junction(); err != nil {
		t.Fatalf("neither a symbolic link nor a junction can be made at %s: %v", sub, err)
	}
	return junction, "junction"
}
