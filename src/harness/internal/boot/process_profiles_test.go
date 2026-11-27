package boot

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// The profile helper is this test binary, re-exec'd through subprocess.Start (the
// one spawn path) with these variables.
const (
	profileHelperEnv = "DSH_BOOT_PROFILE_HELPER"
	profileHomeEnv   = "DSH_BOOT_PROFILE_HOME"
	// readyMarker is the line the helper writes on standard error once the App is
	// ready: standard output is the sdk profile's protocol stream.
	readyMarker = "boot-test: ready"
	// processWait bounds each wait on the helper.
	processWait = 60 * time.Second
)

// TestHelperProfileProcess is the helper process; it is skipped in a normal run.
// It launches the profile named by its environment through boot.Process on the
// given home with the process's own standard streams, writes readyMarker once
// ready, and exits with Process's status. The sdk profile stops when the client
// closes its input (wire.stdio owns the streams); every other profile stops,
// Completed with status 0, when its standard input ends.
func TestHelperProfileProcess(t *testing.T) {
	name := os.Getenv(profileHelperEnv)
	if name == "" {
		t.Skip("the helper process of the profile process test")
	}
	var def profile.Definition
	for _, d := range profile.Definitions() {
		if d.Name == name {
			def = d
		}
	}
	var tree []loader.EntryOptions
	opts := launchOptionsAt(t, def, os.Getenv(profileHomeEnv), &tree, turnsOf(name)...)
	app := launch.NewApp()
	opts.App, opts.Stderr = app, os.Stderr
	go func() {
		<-app.Ready()
		fmt.Fprintln(os.Stderr, readyMarker)
	}()
	if name != profile.SDK {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			app.Stop(launch.Completed, output.OK)
		}()
	}
	os.Exit(Process(context.Background(), opts))
}

// DH-D-BUNDLE-3 (C42), the os-process proof of Stage 6.1: each of the five
// profiles runs as its own OS process through boot.Process on a temp home with the
// keyless fake route, reaches ready and exits 0 when its client ends. While web
// runs, its runtime record names that process and an origin that accepts a
// connection; while any other profile runs the home holds no runtime record. The
// sdk client drives the process's real standard streams. After each exit the home
// holds no runtime record.
func TestFiveProfilesRunAsProcessesAndExitCleanly(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range profile.Definitions() {
		t.Run(def.Name, func(t *testing.T) {
			homeDir := shortHome(t)
			inR, inW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			outR, outW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = inW.Close(); _ = outR.Close() })
			env := subprocess.Env{profileHelperEnv + "=" + def.Name, profileHomeEnv + "=" + homeDir}
			for _, v := range []string{"SYSTEMROOT", "TEMP", "TMP", "TMPDIR"} {
				if val := os.Getenv(v); val != "" {
					env = append(env, v+"="+val)
				}
			}
			if dir := os.Getenv(subprocesstest.CoverDirEnv); dir != "" {
				env = append(env, subprocesstest.CoverDirEnv+"="+dir)
			}
			stderr := &testkit.Buffer{}
			p, err := subprocess.Start(context.Background(), subprocess.Spec{
				Argv:   []string{exe, "-test.run=^TestHelperProfileProcess$", "-test.count=1"},
				Env:    env,
				Stdin:  inR,
				Stdout: outW,
				Stderr: stderr,
				Grace:  time.Second,
				Launch: sandbox.Trusted("boot test: the test binary re-exec'd as one profile's process"),
			})
			if err != nil {
				t.Fatalf("start %s: %v", def.Name, err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), processWait)
				defer cancel()
				_ = p.StopTree(ctx, 0)
				_ = p.Wait()
			})
			_ = inR.Close()
			_ = outW.Close()

			deadline := time.After(processWait)
			for !strings.Contains(stderr.String(), readyMarker) {
				select {
				case <-p.Exited():
					t.Fatalf("%s exited %d before ready:\n%s", def.Name, p.ExitCode(), stderr.String())
				case <-deadline:
					t.Fatalf("%s never reached ready:\n%s", def.Name, stderr.String())
				case <-time.After(10 * time.Millisecond):
				}
			}

			if _, err := os.Stat(home.New(homeDir).DB()); err != nil {
				t.Errorf("%s is ready without the session store: %v", def.Name, err)
			}
			if def.Name == profile.Web {
				if rec := listening(t, homeDir); rec.PID != p.Pid() || rec.Profile != profile.Web {
					t.Errorf("runtime record names pid %d, profile %q; want the process %d, web", rec.PID, rec.Profile, p.Pid())
				}
			} else {
				noRuntimeRecord(t, homeDir)
			}
			if def.Name == profile.SDK {
				driveSDK(t, inW, outR)
				noRuntimeRecord(t, homeDir)
			}

			if err := inW.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-p.Exited():
			case <-time.After(processWait):
				t.Fatalf("%s did not exit once its client ended:\n%s", def.Name, stderr.String())
			}
			_ = p.Wait()
			if status := p.ExitCode(); status != output.OK {
				t.Fatalf("%s exited %d, want %d:\n%s", def.Name, status, output.OK, stderr.String())
			}
			if strings.Contains(stderr.String(), "dsh: fatal:") {
				t.Errorf("%s printed a fatal message:\n%s", def.Name, stderr.String())
			}
			noRuntimeRecord(t, homeDir)
		})
	}
}
