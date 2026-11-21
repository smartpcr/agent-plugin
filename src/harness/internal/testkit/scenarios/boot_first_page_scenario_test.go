//go:build scenario

package scenarios

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
)

// boot1Ready finds the first page's address with its launch token in the output.
var boot1Ready = regexp.MustCompile(`https?://[^\s"']+[?&#]token=[^\s"']+`)

// TestFailedLaunchFailsLoudlyAndServingOneServes: a launch that cannot serve fails
// loudly, and one that can serve does, ending with the UI's first page requested
// with the launch token (areas/boot.md DH-E-BOOT-1; DH-D-BOOT-5, DH-D-BOOT-3).
// Executed in DH-S-22 (step 22.3) on Windows 11 and on Linux; authored in DH-S-6.
//
// Given the binary built from this checkout, an empty temporary home, profile web,
// a second program holding the HTTP port the profile uses and an overlay that
// gives one optional tool entry an invalid config value, when (1) dsh launches
// with the overlay while the port is held, (2) the port is released and it
// launches again, (3) the UI's first page is requested with the launch token and
// (4) the termination signal is sent, then in (1) the process exits 78 within the
// start-up bound, standard error names the HTTP entry as required with the bind
// error, names the optional entry with the overlay's file, line and column and
// gives the diagnostic file's path, standard output is empty and no child process
// remains; in (2) it reaches readiness with one warning naming the optional entry;
// (3) succeeds; after (4) it exits 0 and the port is free. Cleanup: the port holder
// is stopped and the home deleted; a surviving process is killed with its output
// logged.
//
// Inputs: DSH_SCENARIO_BOOT1_LAYER is the overlay text over {port}, binding the web
// entry to {port} and giving one optional tool entry an invalid value;
// DSH_SCENARIO_BOOT1_HTTP and DSH_SCENARIO_BOOT1_OPTIONAL name the HTTP entry and
// that optional entry; DSH_SCENARIO_BOOT1_BOUND is the start-up bound (a Go
// duration); DSH_SCENARIO_BOOT1_CHILDREN is a shell command line over {pid} that
// prints the pids of {pid}'s live descendants, nothing when there are none.
//
//dsh:scenario DH-E-BOOT-1
func TestFailedLaunchFailsLoudlyAndServingOneServes(t *testing.T) {
	layerT := need(t, "DSH_SCENARIO_BOOT1_LAYER", "overlay over {port} with one invalid optional tool entry")
	httpEntry := need(t, "DSH_SCENARIO_BOOT1_HTTP", "name of the profile's HTTP entry")
	optional := need(t, "DSH_SCENARIO_BOOT1_OPTIONAL", "name of the optional entry the overlay breaks")
	bound, err := time.ParseDuration(need(t, "DSH_SCENARIO_BOOT1_BOUND", "start-up bound, a Go duration"))
	if err != nil {
		t.Fatalf("DSH_SCENARIO_BOOT1_BOUND: %v", err)
	}
	children := need(t, "DSH_SCENARIO_BOOT1_CHILDREN", "command printing {pid}'s live descendants")

	bin := dshBinary(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "home")
	holder, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	port := strconv.Itoa(holder.Addr().(*net.TCPAddr).Port)
	overlay := filepath.Join(dir, "overlay.yml")
	if err := os.WriteFile(overlay, []byte(expand(layerT, map[string]string{"port": port})), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--home", root, "--layer", overlay, "web"}

	// (1) The port is held.
	cmd := exec.CommandContext(t.Context(), bin, args...)
	var stdout, stderr testkit.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-time.After(bound):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("the held-port launch did not exit within %s:\n%s", bound, stderr.String())
	}
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 78 {
		t.Errorf("the held-port launch ended with %v after %s, want exit status 78", err, time.Since(start))
	}
	text := stderr.String()
	for _, want := range []string{httpEntry, "required", port, optional, overlay, root} {
		if !strings.Contains(text, want) {
			t.Errorf("standard error does not name %q:\n%s", want, text)
		}
	}
	if !regexp.MustCompile(regexp.QuoteMeta(overlay) + `:\d+:\d+`).MatchString(text) {
		t.Errorf("standard error gives no line and column in %s:\n%s", overlay, text)
	}
	if stdout.String() != "" {
		t.Errorf("standard output is not empty:\n%s", stdout.String())
	}
	if out, _ := shell(t, expand(children, map[string]string{"pid": strconv.Itoa(pid)})); strings.TrimSpace(out) != "" {
		t.Errorf("child processes remain after the failed launch: %s", out)
	}

	// (2) The port is released.
	if err := holder.Close(); err != nil {
		t.Fatal(err)
	}
	cmd = exec.CommandContext(t.Context(), bin, args...)
	stdout.Reset()
	stderr.Reset()
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done = make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			t.Logf("killing the surviving host; stderr:\n%s", stderr.String())
			_ = cmd.Process.Kill()
			<-done
		}
	})
	var page string
	deadline := time.Now().Add(bound)
	for page == "" {
		page = boot1Ready.FindString(stdout.String() + stderr.String())
		if page != "" {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("the launch ended with %v before readiness:\n%s", err, stderr.String())
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("no readiness with a launch token within %s:\n%s%s", bound, stdout.String(), stderr.String())
		}
	}
	warnings := 0
	for _, line := range strings.Split(stderr.String(), "\n") {
		if strings.Contains(strings.ToLower(line), "warn") && strings.Contains(line, optional) {
			warnings++
		}
	}
	if warnings != 1 {
		t.Errorf("%d warnings name %s, want 1:\n%s", warnings, optional, stderr.String())
	}

	// (3) The first page.
	resp, err := http.Get(page)
	if err != nil {
		t.Fatalf("GET the first page: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(body) == 0 {
		t.Errorf("GET the first page: %s with %d bytes", resp.Status, len(body))
	}

	// (4) The termination signal.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("the termination signal: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("after the termination signal the host ended with %v, want exit 0", err)
		}
	case <-time.After(bound):
		t.Fatalf("the host did not exit within %s of the termination signal", bound)
	}
	free, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%s", port))
	if err != nil {
		t.Errorf("port %s is not free after exit: %v", port, err)
	} else {
		t.Cleanup(func() { _ = free.Close() })
	}
}
