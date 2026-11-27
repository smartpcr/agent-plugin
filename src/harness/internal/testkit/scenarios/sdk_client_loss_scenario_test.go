//go:build scenario

package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/scenario"
	"github.com/smartpcr/agent-plugin/src/sdk/sdk"
)

// TestClientLossLeavesNoHost is the DH-E-SDK-2 scenario
// (docs/harness/areas/sdk-acp-typert.md; DH-D-SDK-3, DH-D-SDK-4; tech-spec sec.
// 4.5: authored in DH-S-16, run in DH-S-39): a client that dies or stops reading
// never leaves a host behind.
//
// Given the built binary on Linux, macOS or Windows, a scratch home whose sdk
// profile layer (DSH_SCENARIO_SDK2_LAYER) selects the keyless replay provider
// with a recording whose tool call runs a long-lived child command that writes
// its pid to {pidfile}, and a driver program on the Go client (this test binary
// re-executed, sdk2DriverEnv),
// When three runs each start a turn and, mid-turn, (a) the driver is killed
// without closing, (b) the driver closes the host's stdin and waits, (c) the
// driver stops reading stdout while the agent keeps producing events,
// Then in (a) and (b) the host exits within DSH_SCENARIO_SDK2_SHUTDOWN, the child
// command is gone, and a new host resumes the session with its turn closed; in
// (c) the host exits with the stalled-client status (125) after the write stall,
// and history read by a new host holds every committed event once.
// Cleanup: a process left is killed by its recorded pid at the test's end; the
// homes are removed by scenario.Run.
//
// Inputs: DSH_SCENARIO_SDK2_LAYER, a layer template file ({pidfile} expanded);
// DSH_SCENARIO_SDK2_SHUTDOWN and DSH_SCENARIO_SDK2_WRITE_STALL, the layer's
// ShutdownTimeout and writeStall as Go durations.
//
//dsh:scenario DH-E-SDK-2
func TestClientLossLeavesNoHost(t *testing.T) {
	scenario.Run(t, "DH-E-SDK-2", scenario.Needs{
		Bin: true, Deadline: 20 * time.Minute,
		Env: []string{"DSH_SCENARIO_SDK2_LAYER", "DSH_SCENARIO_SDK2_SHUTDOWN", "DSH_SCENARIO_SDK2_WRITE_STALL"},
	}, func(s *scenario.S) {
		shutdown := sdk2Duration(t, "DSH_SCENARIO_SDK2_SHUTDOWN")
		stall := sdk2Duration(t, "DSH_SCENARIO_SDK2_WRITE_STALL")
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"kill", "close", "stall"} {
			home, dir := s.TempDir(), s.TempDir()
			status, pidfile := filepath.Join(dir, "status.json"), filepath.Join(dir, "child.pid")
			profileLayer(t, home, "sdk", layerInput(t, "DSH_SCENARIO_SDK2_LAYER", "a replay layer whose tool runs a long-lived child", map[string]string{"pidfile": pidfile}))
			driver := launcher.Spawn(t, launcher.Program{Argv: []string{self}, Dir: dir, Env: []string{
				sdk2DriverEnv + "=" + mode, "DSH_SCENARIO_SDK2_STATUS=" + status, "DSH_SCENARIO_SDK2_HOME=" + home, scenario.BinEnv + "=" + s.Bin,
			}})
			st := sdk2Status(t, status, driver)
			child := readPid(t, pidfile, 2*time.Minute)
			t.Cleanup(func() { sdk2Kill(st.Host, child) })
			bound := shutdown + 10*time.Second
			switch mode {
			case "kill":
				if p, err := os.FindProcess(driver.Process.Pid()); err == nil {
					_ = p.Kill()
				}
			case "stall":
				bound += stall
			}
			goneWithin(t, st.Host, mode+": the host", bound)
			goneWithin(t, child, mode+": the tool's child command", 10*time.Second)
			if mode == "stall" {
				if code := sdk2Exit(t, status, 10*time.Second); code != 125 {
					t.Errorf("stall: the host exited %d, want the stalled-client status 125", code)
				}
			}
			sdk2Resumed(t, s, mode, home, st.Session)
		}
	})
}

// sdk2DriverEnv names the driver's mode in a re-executed test binary.
const sdk2DriverEnv = "DSH_SCENARIO_SDK2_DRIVER"

func init() {
	if mode := os.Getenv(sdk2DriverEnv); mode != "" {
		os.Exit(sdk2Drive(mode, os.Getenv("DSH_SCENARIO_SDK2_STATUS"), os.Getenv(scenario.BinEnv), os.Getenv("DSH_SCENARIO_SDK2_HOME")))
	}
}

// sdk2State is what the driver reports once its turn has started.
type sdk2State struct {
	Session string `json:"session"`
	Host    int    `json:"host"`
}

// sdk2Drive is the driver: it starts dsh --profile sdk on home, connects the Go
// client, creates a session, starts a turn, reports to status and then, by
// mode, waits to be killed, closes the host's stdin, or stops reading. The
// host's exit status goes to status+".exit".
func sdk2Drive(mode, status, bin, home string) int {
	ctx := context.Background()
	cmd := exec.Command(bin, "--home", home, "--profile", "sdk")
	cmd.Dir, cmd.Env, cmd.Stderr = filepath.Dir(status), append(os.Environ(), "DSH_HOME="+home), os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return sdk2Fail(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return sdk2Fail(err)
	}
	if err := cmd.Start(); err != nil {
		return sdk2Fail(err)
	}
	go func() {
		_ = cmd.Wait()
		_ = os.WriteFile(status+".exit", []byte(strconv.Itoa(cmd.ProcessState.ExitCode())), 0o600)
	}()
	in := &stallReader{r: stdout}
	h, err := sdk.Connect(ctx, sdk.Transport{In: in, Out: stdin}, sdk.Options{})
	if err != nil {
		return sdk2Fail(err)
	}
	sess, err := h.Create(ctx, sdk.CreateOptions{Cwd: filepath.Dir(status)})
	if err != nil {
		return sdk2Fail(err)
	}
	if _, err := sess.Prompt(ctx, sdk.Input{Text: "run the recorded task"}); err != nil {
		return sdk2Fail(err)
	}
	// The receipt only says the turn is queued: the loss is triggered once the
	// tool's child command runs, which it shows by writing its pid.
	if err := sdk2ChildRunning(filepath.Join(filepath.Dir(status), "child.pid"), 2*time.Minute); err != nil {
		return sdk2Fail(err)
	}
	data, _ := json.Marshal(sdk2State{Session: sess.ID, Host: cmd.Process.Pid})
	if err := os.WriteFile(status, data, 0o600); err != nil {
		return sdk2Fail(err)
	}
	switch mode {
	case "close":
		_ = stdin.Close()
		for {
			if _, err := os.Stat(status + ".exit"); err == nil {
				return 0
			}
			time.Sleep(100 * time.Millisecond)
		}
	case "stall":
		in.stopped.Store(true)
	}
	select {}
}

func sdk2Fail(err error) int {
	fmt.Fprintln(os.Stderr, "sdk2 driver:", err)
	return 2
}

// sdk2ChildRunning waits up to within for pidfile to hold a positive pid: the
// tool's child command writes it once it is running mid-turn.
func sdk2ChildRunning(pidfile string, within time.Duration) error {
	for end := time.Now().Add(within); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		if b, err := os.ReadFile(pidfile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				return nil
			}
		}
	}
	return fmt.Errorf("the tool's child command wrote no pid to %s within %s", pidfile, within)
}

// stallReader reads r until stopped, then blocks every read for good.
type stallReader struct {
	r       io.Reader
	stopped atomic.Bool
}

func (s *stallReader) Read(b []byte) (int, error) {
	if s.stopped.Load() {
		select {}
	}
	return s.r.Read(b)
}

// sdk2Status waits for the driver's report.
func sdk2Status(t *testing.T, path string, driver *launcher.Host) sdk2State {
	t.Helper()
	for end := time.Now().Add(5 * time.Minute); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		var st sdk2State
		if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &st) == nil && st.Host > 0 {
			return st
		}
		select {
		case <-driver.Process.Exited():
			t.Fatalf("the driver exited %d before its turn started:\n%s", driver.Process.ExitCode(), driver.Stderr.String())
		default:
		}
	}
	t.Fatalf("no driver report within 5m:\n%s", driver.Stderr.String())
	return sdk2State{}
}

// sdk2Exit is the host's exit status the driver recorded.
func sdk2Exit(t *testing.T, status string, within time.Duration) int {
	t.Helper()
	for end := time.Now().Add(within); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		if data, err := os.ReadFile(status + ".exit"); err == nil {
			code, _ := strconv.Atoi(strings.TrimSpace(string(data)))
			return code
		}
	}
	t.Fatalf("the driver recorded no host exit within %s", within)
	return 0
}

// sdk2Resumed resumes session on a new host on home and checks its turn closed
// and its history holds every seq once.
func sdk2Resumed(t *testing.T, s *scenario.S, mode, home, session string) {
	t.Helper()
	ctx := s.Context()
	h, err := sdk.Start(ctx, sdk.Options{Bin: s.Bin, Home: home, Dir: s.Workspace})
	if err != nil {
		t.Fatalf("%s: a new host on %s: %v", mode, home, err)
	}
	if _, err := h.Resume(ctx, session, sdk.ResumeOptions{}); err != nil {
		t.Errorf("%s: a new host cannot resume %s: %v", mode, session, err)
	}
	if err := h.Close(context.WithoutCancel(ctx)); err != nil {
		t.Errorf("%s: close the resuming host: %v", mode, err)
	}
	evs := storedHistory(t, s, home, session)
	seen := map[int64]bool{}
	for _, e := range evs {
		if seen[e.Seq] {
			t.Errorf("%s: seq %d appears twice in history", mode, e.Seq)
		}
		seen[e.Seq] = true
	}
	ends := ofType(evs, "turn/end")
	if len(ends) == 0 {
		t.Fatalf("%s: the interrupted turn was never closed", mode)
	}
	var end wireTurnEnd
	decode(t, ends[len(ends)-1], &end)
	if k := end.Reason.Kind; k != "aborted" && k != "interrupted" {
		t.Errorf("%s: the turn closed %q, want aborted (or interrupted, closed on resume)", mode, k)
	}
}

// sdk2Kill kills, by recorded pid, whatever a run left.
func sdk2Kill(pids ...int) {
	for _, pid := range pids {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	}
}

func sdk2Duration(t *testing.T, name string) time.Duration {
	t.Helper()
	d, err := time.ParseDuration(need(t, name, "a Go duration"))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return d
}
