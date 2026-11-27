//go:build scenario

package scenarios

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/session/store until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/session/store/sqlite until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	//lint:ignore scenarioimport written before testkit/scenario; its body is kept (architecture.md I-2), so it uses internal/storage/home until it moves to the helper (DH-ADR-TOOLCHAIN-8)
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
)

var storeImageLine = regexp.MustCompile(`(?m)^image=(.+?)\s*$`)

// storeCheck opens the home's database and runs the store check on it.
func storeCheck(t *testing.T, root string) (sqlite.CheckReport, func(id string) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), storeStepTimeout)
	defer cancel()
	s, err := sqlite.OpenHome(ctx, home.New(root), sqlite.WithKind("scenario"))
	if err != nil {
		t.Fatalf("opening the home's database: %v", err)
	}
	defer func() { _ = s.Close(ctx) }()
	rep, err := s.Check(ctx)
	if err != nil {
		t.Fatalf("store check: %v", err)
	}
	listed := map[string]bool{}
	for q := (store.Query{Limit: 500}); ; {
		page, err := s.List(ctx, q)
		if err != nil {
			t.Fatalf("listing the home's sessions: %v", err)
		}
		for _, sum := range page.Summaries {
			listed[sum.Header.ID] = true
		}
		if page.Next == "" {
			break
		}
		q.Cursor = page.Next
	}
	return rep, func(id string) bool { return listed[id] }
}

// storeFiles lists the regular files under dir, relative and slash-separated; a
// missing dir lists nothing.
func storeFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.Type().IsRegular() {
			rel, _ := filepath.Rel(dir, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("listing %s: %v", dir, err)
	}
	return files
}

// storeSpillDir is the directory holding session id's spill files.
func storeSpillDir(root, id string) string {
	return filepath.Join(home.New(root).Spill(), sqlite.SpillName(id))
}

// storeTree logs the scratch home's files as cleanup evidence; the guarded runner
// copies "evidence:" lines into its record before the home is removed.
func storeTree(t *testing.T, root, when string) {
	t.Helper()
	files := storeFiles(t, root)
	var shown []string
	for i, f := range files {
		if i == 40 {
			shown = append(shown, fmt.Sprintf("... %d more", len(files)-40))
			break
		}
		size := int64(-1)
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err == nil {
			size = info.Size()
		}
		shown = append(shown, fmt.Sprintf("%s (%d)", f, size))
	}
	t.Logf("evidence: home %s, %s: %d files: %s", root, when, len(files), strings.Join(shown, ", "))
}

// storeSeq reads id's log from the database (storeLog fails on a gap) and logs
// its seq range as evidence.
func storeSeq(t *testing.T, root, id string) []store.Record {
	t.Helper()
	recs, _ := storeLog(t, root, id)
	t.Logf("evidence: log of %s read from the database: seq 0 to %d (%d records), no gap", id, len(recs)-1, len(recs))
	return recs
}

// storeGone requires that nothing of the deleted session id remains in root: no
// row (not listed, not tombstoned), no spill directory and none of blobs, and
// logs it as evidence.
func storeGone(t *testing.T, root, id string, blobs []string) {
	t.Helper()
	rep, listed := storeCheck(t, root)
	if listed(id) || slices.Contains(rep.Deleting, id) {
		t.Fatalf("a row of the deleted session %s remains (deleting: %v)", id, rep.Deleting)
	}
	spill := storeSpillDir(root, id)
	if _, err := os.Stat(spill); !os.IsNotExist(err) {
		t.Fatalf("the deleted session %s's spill directory remains: %s (%v): %v", id, spill, err, storeFiles(t, spill))
	}
	for _, p := range blobs {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("a blob of the deleted session %s remains: %s (%v)", id, p, err)
		}
	}
	t.Logf("evidence: session %s gone: no row (not listed, not deleting), spill directory %s absent, %d blobs absent: %s",
		id, filepath.Base(spill), len(blobs), strings.Join(blobs, ", "))
}

// TestDeletedSessionLeavesNothingBehind: deleting a session removes every row of
// it, an image still referenced by a fork survives while an unreferenced one is
// collected, and a deletion interrupted by a kill finishes after a restart
// (areas/session-store.md DH-E-STORE-6; DH-D-STORE-6). Guarded; deletion removes
// data, so it runs in DH-S-16 (step 16.8) on scratch homes only; authored in
// DH-S-7. Until it passes the SQLite store is used on scratch homes only.
//
// Given the built binary started as a headless host on an empty home with the
// replay model provider, and two sessions, one with attached images and spill
// files and one forked from it that shares one of the images, when (1) the first
// session is deleted, (2) a deletion of a large session (10,000 events, with
// spill files) is started and the host is killed after its first chunk and
// started again and (3) the store check runs, then after (1) no row or spill file
// of the first session remains, the fork and the image it shares survive and an
// image only the first session referenced is collected; after (2) the tombstoned
// session is invisible at once and, once the store is opened again (the
// restarted host, or this check, finishes it), no row, image or spill file of it
// remains; (3) reports no damage. The home's files, both sessions' absence and the
// fork's log seq range are logged as "evidence:" lines. Each process step has a
// 60 second timeout.
//
// Inputs are shell command lines over {home} and {session}:
// DSH_SCENARIO_STORE6_START starts the headless host on {home} in the foreground;
// DSH_SCENARIO_STORE6_CREATE creates a session with attached images and at least
// one spilled tool result and prints "session=<id>"; DSH_SCENARIO_STORE6_FORK
// forks {session}, keeping its first image, and prints "session=<id>";
// DSH_SCENARIO_STORE6_IMAGES prints one "image=<path>" line per image file
// {session} references; DSH_SCENARIO_STORE6_LARGE creates a session of 10,000
// events with at least one spilled tool result and prints "session=<id>";
// DSH_SCENARIO_STORE6_DELETE deletes {session} and returns when
// it is done; DSH_SCENARIO_STORE6_DELETE_BEGIN starts deleting {session} and
// returns once its first chunk is gone; DSH_SCENARIO_STORE6_KILL kills the host
// without a clean stop.
//
//dsh:scenario DH-E-STORE-6
func TestDeletedSessionLeavesNothingBehind(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	startT := need(t, "DSH_SCENARIO_STORE6_START", "command starting the headless host on {home}")
	createT := need(t, "DSH_SCENARIO_STORE6_CREATE", "command creating a session with attached images")
	forkT := need(t, "DSH_SCENARIO_STORE6_FORK", "command forking {session} keeping its first image")
	imagesT := need(t, "DSH_SCENARIO_STORE6_IMAGES", "command printing image=<path> for each image of {session}")
	largeT := need(t, "DSH_SCENARIO_STORE6_LARGE", "command creating a session of 10,000 events")
	deleteT := need(t, "DSH_SCENARIO_STORE6_DELETE", "command deleting {session}")
	beginT := need(t, "DSH_SCENARIO_STORE6_DELETE_BEGIN", "command starting a deletion of {session}")
	killT := need(t, "DSH_SCENARIO_STORE6_KILL", "command killing the host without a clean stop")
	with := func(id string) map[string]string { return map[string]string{"home": root, "session": id} }
	newSession := func(what, line string) string {
		out, err := storeStep(t, what, line)
		m := storeSessionLine.FindStringSubmatch(out)
		if err != nil || m == nil {
			t.Fatalf("%s: %v\n%s", what, err, out)
		}
		return m[1]
	}
	imagesOf := func(id string) []string {
		out, err := storeStep(t, "images of "+id, expand(imagesT, with(id)))
		if err != nil {
			t.Fatalf("listing the images of %s: %v\n%s", id, err, out)
		}
		var paths []string
		for _, m := range storeImageLine.FindAllStringSubmatch(out, -1) {
			paths = append(paths, m[1])
		}
		return paths
	}
	if err := home.Ensure(home.New(root)); err != nil {
		t.Fatal(err)
	}
	keep := false
	t.Cleanup(func() {
		if !keep && !t.Failed() {
			_ = os.RemoveAll(root)
		} else {
			t.Logf("kept the home at %s", root)
		}
	})

	host := startShell(t, expand(startT, with("")))
	first := newSession("create the first session", expand(createT, with("")))
	fork := newSession("fork it", expand(forkT, with(first)))
	large := newSession("create the large session", expand(largeT, with("")))
	firstImages, forkImages, largeImages := imagesOf(first), imagesOf(fork), imagesOf(large)
	if len(forkImages) == 0 || len(firstImages) <= len(forkImages) {
		t.Fatalf("the fixture needs a shared image and one only the first session holds: first %v, fork %v", firstImages, forkImages)
	}
	for _, id := range []string{first, large} {
		if spills := storeFiles(t, storeSpillDir(root, id)); len(spills) == 0 {
			t.Fatalf("the fixture needs spill files of %s; %s has none", id, storeSpillDir(root, id))
		} else {
			t.Logf("evidence: session %s spill files before its deletion: %s", id, strings.Join(spills, ", "))
		}
	}
	storeTree(t, root, "before the deletions")
	forkRecs := storeSeq(t, root, fork)

	// (1)
	if out, err := storeStep(t, "delete the first session", expand(deleteT, with(first))); err != nil {
		t.Fatalf("deleting %s: %v\n%s", first, err, out)
	}
	var unshared []string
	for _, p := range firstImages {
		if !slices.Contains(forkImages, p) {
			unshared = append(unshared, p)
		}
	}
	storeGone(t, root, first, unshared)
	if after := storeSeq(t, root, fork); len(after) != len(forkRecs) {
		keep = true
		t.Fatalf("the fork changed with its parent's deletion: %d records, then %d", len(forkRecs), len(after))
	}
	for _, p := range forkImages {
		if _, err := os.Stat(p); err != nil {
			keep = true
			t.Fatalf("the image the fork shares is gone: %s: %v", p, err)
		}
	}

	// (2)
	if out, err := storeStep(t, "begin deleting the large session", expand(beginT, with(large))); err != nil {
		t.Fatalf("starting the deletion of %s: %v\n%s", large, err, out)
	}
	if out, err := storeStep(t, "kill the host", expand(killT, with(""))); err != nil {
		t.Fatalf("killing the host: %v\n%s", err, out)
	}
	deadline := time.Now().Add(storeStepTimeout)
	for !host.exited() {
		if time.Now().After(deadline) {
			t.Fatal("the host did not exit after the kill")
		}
		<-time.After(50 * time.Millisecond)
	}
	host = startShell(t, expand(startT, with("")))
	deadline = time.Now().Add(storeStepTimeout)
	var (
		rep    sqlite.CheckReport
		listed func(string) bool
	)
	for {
		rep, listed = storeCheck(t, root)
		if listed(large) {
			keep = true
			t.Fatalf("the tombstoned session %s is still listed", large)
		}
		if !slices.Contains(rep.Deleting, large) {
			break
		}
		if time.Now().After(deadline) {
			keep = true
			t.Fatalf("the interrupted deletion of %s did not finish within %s", large, storeStepTimeout)
		}
		<-time.After(250 * time.Millisecond)
	}
	storeGone(t, root, large, largeImages)
	storeTree(t, root, "after the deletions")

	// (3)
	if !rep.Clean() {
		keep = true
		t.Fatalf("the store check reports damage: integrity %v, problems %v", rep.Integrity, rep.Problems)
	}
	_, _ = storeStep(t, "stop the host", expand(killT, with("")))
	_ = host
}
