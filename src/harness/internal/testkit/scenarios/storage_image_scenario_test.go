//go:build scenario

package scenarios

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
)

var storageSeenLine = regexp.MustCompile(`(?m)^image_seen=true\s*$`)

// TestImageSurvivesCrashRestartAndCollection: an attached image stays readable
// through a crash mid-upload, a restart, a fork whose parent is deleted, and the
// maintenance that collects unreferenced files (areas/storage.md
// DH-E-STORAGE-2; DH-D-STORAGE-3, DH-D-STORAGE-5). Runs in DH-S-23; authored in
// DH-S-8.
//
// Given the built binary started as a headless host on an empty home with the
// replay model provider, when (1) a 20 megapixel JPEG is attached and the model
// is asked about it, (2) the host is killed in the middle of a second upload,
// (3) the host is started again and a follow-up is asked, (4) the session is
// forked, the parent deleted, the clock advanced past the grace period and
// maintenance waited for, and (5) the fork is asked about the image, then after
// (1) the request carried the image; after (3) the follow-up is answered with
// the image, every image the session references exists, nothing of the
// interrupted upload is referenced, and the store check reports no damage; after
// (4) every image the fork references still exists; after (5) the fork's request
// carried the image. Each process step has a 60 second timeout.
//
// Inputs are shell command lines over {home} and {session}:
// DSH_SCENARIO_STORAGE2_START starts the headless host on {home} in the
// foreground; DSH_SCENARIO_STORAGE2_ATTACH creates a session, attaches a 20MP
// JPEG, asks about it, and prints "session=<id>" and, when the model request
// carried the image, "image_seen=true"; DSH_SCENARIO_STORAGE2_UPLOAD_BEGIN
// starts a second upload to {session} and returns while it is in flight;
// DSH_SCENARIO_STORAGE2_KILL kills the host without a clean stop;
// DSH_SCENARIO_STORAGE2_ASK asks {session} a follow-up about the image and
// prints "image_seen=true" when the model request carried it;
// DSH_SCENARIO_STORAGE2_IMAGES prints one "image=<path>" line per image file
// {session} references; DSH_SCENARIO_STORAGE2_FORK forks {session} and prints
// "session=<id>"; DSH_SCENARIO_STORAGE2_DELETE deletes {session};
// DSH_SCENARIO_STORAGE2_MAINTAIN advances the host's clock past the grace
// period and returns once a maintenance pass has finished.
//
//dsh:scenario DH-E-STORAGE-2
func TestImageSurvivesCrashRestartAndCollection(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	startT := need(t, "DSH_SCENARIO_STORAGE2_START", "command starting the headless host on {home}")
	attachT := need(t, "DSH_SCENARIO_STORAGE2_ATTACH", "command attaching a 20MP JPEG to a new session and asking about it")
	uploadT := need(t, "DSH_SCENARIO_STORAGE2_UPLOAD_BEGIN", "command starting a second upload to {session}")
	killT := need(t, "DSH_SCENARIO_STORAGE2_KILL", "command killing the host without a clean stop")
	askT := need(t, "DSH_SCENARIO_STORAGE2_ASK", "command asking {session} about the image")
	imagesT := need(t, "DSH_SCENARIO_STORAGE2_IMAGES", "command printing image=<path> for each image of {session}")
	forkT := need(t, "DSH_SCENARIO_STORAGE2_FORK", "command forking {session}")
	deleteT := need(t, "DSH_SCENARIO_STORAGE2_DELETE", "command deleting {session}")
	maintainT := need(t, "DSH_SCENARIO_STORAGE2_MAINTAIN", "command advancing past the grace period and awaiting maintenance")
	with := func(id string) map[string]string { return map[string]string{"home": root, "session": id} }
	step := func(what, tmpl, id string) string {
		out, err := storeStep(t, what, expand(tmpl, with(id)))
		if err != nil {
			t.Fatalf("%s: %v\n%s", what, err, out)
		}
		return out
	}
	sessionOf := func(what, out string) string {
		m := storeSessionLine.FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("%s printed no session=<id>:\n%s", what, out)
		}
		return m[1]
	}
	imagesExist := func(id string) []string {
		var paths []string
		for _, m := range storeImageLine.FindAllStringSubmatch(step("images of "+id, imagesT, id), -1) {
			if _, err := os.Stat(m[1]); err != nil {
				t.Fatalf("an image %s references is gone: %v", id, err)
			}
			paths = append(paths, m[1])
		}
		if len(paths) == 0 {
			t.Fatalf("%s references no image", id)
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

	// (1)
	host := startShell(t, expand(startT, with("")))
	out := step("attach the image and ask", attachT, "")
	id := sessionOf("attaching the image", out)
	if !storageSeenLine.MatchString(out) {
		t.Fatalf("the first request did not carry the image:\n%s", out)
	}
	first := imagesExist(id)

	// (2)
	step("begin the second upload", uploadT, id)
	step("kill the host", killT, "")
	for deadline := time.Now().Add(storeStepTimeout); !host.exited(); {
		if time.Now().After(deadline) {
			t.Fatal("the host did not exit after the kill")
		}
		<-time.After(50 * time.Millisecond)
	}

	// (3)
	host = startShell(t, expand(startT, with("")))
	if out := step("ask a follow-up", askT, id); !storageSeenLine.MatchString(out) {
		keep = true
		t.Fatalf("the follow-up after the restart did not carry the image:\n%s", out)
	}
	if after := imagesExist(id); !slices.Equal(after, first) {
		keep = true
		t.Fatalf("the session references %v after the interrupted upload, want %v", after, first)
	}
	storeLog(t, root, id) // fails on a gap or a duplicate
	if rep, _ := storeCheck(t, root); !rep.Clean() {
		keep = true
		t.Fatalf("the store check reports damage after the crash: integrity %v, problems %v", rep.Integrity, rep.Problems)
	}

	// (4)
	fork := sessionOf("forking", step("fork the session", forkT, id))
	step("delete the parent", deleteT, id)
	step("advance past the grace period and await maintenance", maintainT, "")
	imagesExist(fork)

	// (5)
	if out := step("ask the fork", askT, fork); !storageSeenLine.MatchString(out) {
		keep = true
		t.Fatalf("the fork's request did not carry the image:\n%s", out)
	}
	step("stop the host", killT, "")
	_ = host
}
