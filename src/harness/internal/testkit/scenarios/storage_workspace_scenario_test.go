//go:build scenario

package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/session until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/session/event until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/session/store/sqlite until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/storage/home until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/storage/workspace until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/workspace"
)

var (
	storageWorkspaceLine = regexp.MustCompile(`(?m)^workspace=(\S+)\s*$`)
	// storageActivityLine is workspace.ActiveError: the session and its first
	// activity's provider and detail.
	storageActivityLine = regexp.MustCompile(regexp.QuoteMeta(string(workspace.ReasonActive)) + `: session (\S+) is active \(([^:;()\s]+): ([^;\n]+)(?:;[^\n]*)?\)`)
	storageRequestLine  = regexp.MustCompile(`(?m)^request_at=(\d+)\s*$`)
)

// storageListWithin bounds how soon a second process sees a workspace another
// added; storageRefresh is the activity refresh after which a stopped session
// sends no model request.
const (
	storageListWithin = 2 * time.Second
	storageRefresh    = 2 * time.Second
)

// storageFlags reads session id's archive flags straight from the home's
// database.
func storageFlags(t *testing.T, root, id string) workspace.Flags {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), storeStepTimeout)
	defer cancel()
	s, err := sqlite.OpenHome(ctx, home.New(root), sqlite.WithKind("scenario"))
	if err != nil {
		t.Fatalf("opening the home's database: %v", err)
	}
	defer func() { _ = s.Close(ctx) }()
	reg, err := workspace.Open(ctx, s)
	if err != nil {
		t.Fatalf("opening the workspace registry: %v", err)
	}
	f, err := reg.Flags(ctx, session.ID(id))
	if err != nil {
		t.Fatalf("flags of %s: %v", id, err)
	}
	return f
}

// TestWorkspaceSharedAndArchiveStopsWork: two processes on one home share the
// workspace registry, an archive is refused while another process works the
// session unless asked to stop it, and an archived session resumes once
// unarchived (areas/storage.md DH-E-STORAGE-1; DH-D-STORAGE-1, DH-D-STORAGE-7).
// Runs in DH-S-19; authored in DH-S-8.
//
// Given two processes on one home, A a web host and B a CLI run with the replay
// model provider, when (1) a workspace is added through A's HTTP API, (2) B
// lists workspaces, (3) the session B is running a long turn in is archived
// without stop, (4) it is archived with stop, and (5) it is unarchived and sent a
// new prompt, then (2) lists the workspace within 2 seconds; (3) is refused and
// the refusal names B's activity, and the session is not hidden; (4) B's turn
// ends cancelled, no model request is sent after the archive plus one refresh,
// and the session is hidden; (5) the prompt is accepted and the session's log is
// contiguous. Each process step has a 60 second timeout.
//
// Inputs are shell command lines over {home}, {path} and {session}:
// DSH_SCENARIO_STORAGE1_WEB starts host A on {home} in the foreground;
// DSH_SCENARIO_STORAGE1_ADD adds {path} as a workspace through A's HTTP API and
// prints "workspace=<id>"; DSH_SCENARIO_STORAGE1_LIST lists the workspaces from
// a CLI process (B) and prints one "workspace=<id>" line each;
// DSH_SCENARIO_STORAGE1_CREATE creates a session in {path} attached to its
// workspace and prints "session=<id>"; DSH_SCENARIO_STORAGE1_RUN runs B in the
// foreground on {session} with a long replayed turn;
// DSH_SCENARIO_STORAGE1_ARCHIVE archives {session} without stop through A (and
// exits non-zero with the refusal when refused);
// DSH_SCENARIO_STORAGE1_ARCHIVE_STOP archives {session} with stop through A;
// DSH_SCENARIO_STORAGE1_REQUESTS prints one "request_at=<unix ms>" line per
// model request the replay provider received for {session};
// DSH_SCENARIO_STORAGE1_UNARCHIVE unarchives {session};
// DSH_SCENARIO_STORAGE1_PROMPT sends {session} a new prompt and returns once it
// is accepted; DSH_SCENARIO_STORAGE1_STOP stops host A.
//
//dsh:scenario DH-E-STORAGE-1
func TestWorkspaceSharedAndArchiveStopsWork(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	project := filepath.Join(t.TempDir(), "project")
	webT := need(t, "DSH_SCENARIO_STORAGE1_WEB", "command starting web host A on {home}")
	addT := need(t, "DSH_SCENARIO_STORAGE1_ADD", "command adding {path} as a workspace through A's HTTP API")
	listT := need(t, "DSH_SCENARIO_STORAGE1_LIST", "command listing workspaces from CLI process B")
	createT := need(t, "DSH_SCENARIO_STORAGE1_CREATE", "command creating a session in {path}")
	runT := need(t, "DSH_SCENARIO_STORAGE1_RUN", "command running B on {session} with a long turn")
	archiveT := need(t, "DSH_SCENARIO_STORAGE1_ARCHIVE", "command archiving {session} without stop")
	archiveStopT := need(t, "DSH_SCENARIO_STORAGE1_ARCHIVE_STOP", "command archiving {session} with stop")
	requestsT := need(t, "DSH_SCENARIO_STORAGE1_REQUESTS", "command printing request_at=<unix ms> per model request of {session}")
	unarchiveT := need(t, "DSH_SCENARIO_STORAGE1_UNARCHIVE", "command unarchiving {session}")
	promptT := need(t, "DSH_SCENARIO_STORAGE1_PROMPT", "command sending {session} a new prompt")
	stopT := need(t, "DSH_SCENARIO_STORAGE1_STOP", "command stopping host A")
	with := func(id string) map[string]string {
		return map[string]string{"home": root, "path": project, "session": id}
	}
	step := func(what, tmpl, id string) string {
		out, err := storeStep(t, what, expand(tmpl, with(id)))
		if err != nil {
			t.Fatalf("%s: %v\n%s", what, err, out)
		}
		return out
	}
	if err := home.Ensure(home.New(root)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	host := startShell(t, expand(webT, with("")))
	defer func() { _, _ = storeStep(t, "stop host A", expand(stopT, with(""))); _ = host }()

	// (1) and (2)
	m := storageWorkspaceLine.FindStringSubmatch(step("add the workspace through A", addT, ""))
	if m == nil {
		t.Fatal("adding the workspace printed no workspace=<id>")
	}
	added := m[1]
	for deadline := time.Now().Add(storageListWithin); ; {
		listed := false
		for _, l := range storageWorkspaceLine.FindAllStringSubmatch(step("list from B", listT, ""), -1) {
			listed = listed || l[1] == added
		}
		if listed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("B did not list workspace %s within %s", added, storageListWithin)
		}
		<-time.After(100 * time.Millisecond)
	}

	// (3)
	sm := storeSessionLine.FindStringSubmatch(step("create the session", createT, ""))
	if sm == nil {
		t.Fatal("creating the session printed no session=<id>")
	}
	id := sm[1]
	b := startShell(t, expand(runT, with(id)))
	for deadline := time.Now().Add(storeStepTimeout); len(storageRequestLine.FindAllString(step("model requests", requestsT, id), -1)) == 0; {
		if time.Now().After(deadline) {
			t.Fatal("B sent no model request")
		}
		<-time.After(100 * time.Millisecond)
	}
	out, err := storeStep(t, "archive without stop", expand(archiveT, with(id)))
	if err == nil {
		t.Fatalf("archiving a session B is working was not refused:\n%s", out)
	}
	// B's activity is whatever a provider in A can see of another process -- its
	// lease, an agent -- so the refusal names some provider's activity of this
	// session, not a particular provider.
	if a := storageActivityLine.FindStringSubmatch(out); a == nil || a[1] != id || strings.TrimSpace(a[3]) == "" {
		t.Fatalf("the refusal does not name B's activity on %s:\n%s", id, out)
	}
	if f := storageFlags(t, root, id); f.Archive.Hidden() {
		t.Fatalf("a refused archive left the session hidden: %+v", f)
	}

	// (4)
	archivedAt := time.Now()
	step("archive with stop", archiveStopT, id)
	for deadline := time.Now().Add(storeStepTimeout); !b.exited(); {
		if time.Now().After(deadline) {
			t.Fatal("B's turn did not end after an archive with stop")
		}
		<-time.After(50 * time.Millisecond)
	}
	if f := storageFlags(t, root, id); f.Archive != workspace.ArchiveArchived {
		t.Fatalf("the session is not hidden after an archive with stop: %+v", f)
	}
	recs, _ := storeLog(t, root, id)
	ended := false
	for _, rec := range recs {
		if e, err := event.TurnEndType.As(event.Of(rec)); err == nil {
			ended = e.Reason.Kind == event.ReasonAborted || e.Reason.Kind == event.ReasonInterrupted
		}
	}
	if !ended {
		t.Fatal("B's last turn did not end cancelled")
	}
	<-time.After(storageRefresh)
	cutoff := archivedAt.Add(storageRefresh).UnixMilli()
	for _, l := range storageRequestLine.FindAllStringSubmatch(step("model requests after the archive", requestsT, id), -1) {
		if at, _ := strconv.ParseInt(l[1], 10, 64); at > cutoff {
			t.Fatalf("a model request at %d followed the archive at %d plus one refresh", at, archivedAt.UnixMilli())
		}
	}

	// (5)
	step("unarchive", unarchiveT, id)
	if f := storageFlags(t, root, id); f.Archive.Hidden() {
		t.Fatalf("the session is hidden after unarchive: %+v", f)
	}
	before := len(recs)
	step("a new prompt", promptT, id)
	after, _ := storeLog(t, root, id) // fails on a gap or a duplicate
	if len(after) <= before {
		t.Fatalf("the new prompt added nothing to the log: %d records, then %d", before, len(after))
	}
}
