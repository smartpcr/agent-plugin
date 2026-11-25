//go:build scenario

package scenarios

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// The helpers the DH-S-12 filesystem scenarios (DH-E-FS-1, -2, -3, DH-E-VIEW-5)
// share. Like the rest of the package they read the product only through the
// committed events the SDK returns.

// The codes of areas/filesystem.md the scenarios read from tool results.
const (
	codeSandboxDenied = "fs/sandbox-denied"
	codeStaleVersion  = "fs/stale-version"
	codeNotFound      = "fs/not-found"
	codeNotDirectory  = "fs/not-directory"
	codeInvalidRegex  = "search/invalid-pattern"
	codeWorkdirGone   = "workdir/unavailable"
)

// stagePrefix names a file the seam stages before it publishes by rename
// (util/fsx StagePrefix; areas/filesystem.md DH-D-FS-1). A refused call leaves none.
const stagePrefix = ".dsh-stage-"

// hashListing is a recursive listing of dir: each entry's slash path relative to
// dir, mapped to "dir", "link:<target>" or "sha256:<hex>" of its bytes. Links are
// listed, not followed, so a listing never leaves dir.
func hashListing(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			out[rel] = "link:" + target
		case d.IsDir():
			out[rel] = "dir"
		default:
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			out[rel] = "sha256:" + hex.EncodeToString(sum[:])
		}
		return nil
	})
	if err != nil {
		t.Fatalf("listing %s: %v", dir, err)
	}
	return out
}

// listingDiff names every path whose entry differs between two listings, sorted.
func listingDiff(before, after map[string]string) []string {
	var diff []string
	for p, v := range before {
		if w, ok := after[p]; !ok {
			diff = append(diff, "removed "+p)
		} else if w != v {
			diff = append(diff, "changed "+p)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			diff = append(diff, "created "+p)
		}
	}
	slices.Sort(diff)
	return diff
}

// keepListings writes both listings beside the test's temporary directories
// when the scenario fails, and logs where (the scenarios' "keep both listings").
func keepListings(t *testing.T, name string, before map[string]string, after func() map[string]string) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		f, err := os.CreateTemp("", "dsh-scenario-listings-"+name+"-*.json")
		if err != nil {
			t.Logf("keeping the listings of %s: %v", name, err)
			return
		}
		defer f.Close()
		_ = json.NewEncoder(f).Encode(map[string]any{"before": before, "after": after()})
		t.Logf("kept the before and after listings of %s at %s", name, f.Name())
	})
}

// stagedFiles names every staged file left below dirs.
func stagedFiles(t *testing.T, dirs ...string) []string {
	t.Helper()
	var left []string
	for _, dir := range dirs {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && strings.HasPrefix(d.Name(), stagePrefix) {
				left = append(left, p)
			}
			return nil
		})
	}
	return left
}

// toolOutcome is one tool call of the log: its tool/call and its tool/result.
type toolOutcome struct {
	ID       string
	Name     string
	Args     json.RawMessage
	Call     int64
	Result   int64
	CalledAt string
	DoneAt   string
	IsError  bool
	Raw      json.RawMessage
	calls    int
	results  int
}

// has reports whether the call's result names code (the result's text carries
// the herr code of a refusal).
func (o *toolOutcome) has(code string) bool { return bytes.Contains(o.Raw, []byte(code)) }

// toolOutcomes reads the tool calls of evs, in the order they were called, and
// fails the scenario unless each has exactly one tool/call before one tool/result.
func toolOutcomes(t *testing.T, evs []protocol.Event) []*toolOutcome {
	t.Helper()
	byID := map[string]*toolOutcome{}
	var order []*toolOutcome
	for _, e := range ofType(evs, "tool/call", "tool/result") {
		var p struct {
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			IsError   bool   `json:"is_error"`
		}
		decode(t, e, &p)
		o := byID[p.CallID]
		if o == nil {
			o = &toolOutcome{ID: p.CallID}
			byID[p.CallID] = o
			order = append(order, o)
		}
		if e.Type == "tool/call" {
			o.Name, o.Args, o.Call, o.CalledAt = p.Name, json.RawMessage(p.Arguments), e.Seq, e.Time
			o.calls++
		} else {
			o.Result, o.DoneAt, o.IsError, o.Raw = e.Seq, e.Time, p.IsError, e.Data
			o.results++
		}
	}
	for _, o := range order {
		if o.calls != 1 || o.results != 1 || o.Call >= o.Result {
			t.Fatalf("call %s (%s) has %d tool/call and %d tool/result (seqs %d, %d); want one each, call first", o.ID, o.Name, o.calls, o.results, o.Call, o.Result)
		}
	}
	return order
}

// runPrompt sends text to session, waits for its run and returns the run's events.
func (h *sdkHost) runPrompt(t *testing.T, session, text string, within time.Duration) []protocol.Event {
	t.Helper()
	run := h.awaitRun(t, session, h.prompt(t, session, text), within)
	return inRun(h.history(t, session), run.FirstSeq, run.LastSeq)
}

// createIn opens a session on model in cwd under the permission preset mode.
func (h *sdkHost) createIn(t *testing.T, model *protocol.ModelRoute, cwd, mode string) string {
	t.Helper()
	var r protocol.SessionCreateResult
	h.call(t, "session/create", protocol.SessionCreateParams{Model: model, Cwd: cwd, PermissionMode: mode}, &r)
	if r.SessionID == "" {
		t.Fatal("session/create named no session")
	}
	return r.SessionID
}
