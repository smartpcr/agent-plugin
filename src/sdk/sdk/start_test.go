package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// fakeHostEnv makes the test binary a fake host: "serve" answers initialize and
// shutdown and exits 0 at the end of its input; "ignore" also ignores the end
// of its input and blocks; "ignore-term" ignores SIGTERM besides.
const fakeHostEnv = "SDK_TEST_FAKE_HOST"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeHostEnv); mode != "" {
		fakeHost(mode)
	}
	os.Exit(m.Run())
}

func fakeHost(mode string) {
	if mode == "ignore-term" {
		signal.Ignore(syscall.SIGTERM)
	}
	args := strings.Join(os.Args[1:], " ")
	conn := jsonrpc.Serve(context.Background(), os.Stdin, os.Stdout, jsonrpc.Options{Methods: map[string]jsonrpc.Handler{
		protocol.InitializeMethod: func(context.Context, json.RawMessage) (any, error) {
			return protocol.InitializeResult{ProtocolVersion: protocol.Version, ServerInfo: protocol.Implementation{Name: "fake", Version: args}}, nil
		},
		"shutdown": func(context.Context, json.RawMessage) (any, error) { return protocol.Empty{}, nil },
	}})
	<-conn.Done()
	if mode != "serve" {
		// A pending timer keeps the runtime from reporting a deadlock.
		<-time.After(time.Hour)
	}
	os.Exit(0)
}

// fakeOptions start the test binary as a fake host in mode. A host that exits
// politely gets a long grace (a -race child is slow to exit); one that ignores
// shutdown a short one, so the ladder test is quick.
func fakeOptions(t *testing.T, mode string) Options {
	grace := 30 * time.Second
	if mode != "serve" {
		grace = 500 * time.Millisecond
	}
	return Options{Bin: os.Args[0], Home: t.TempDir(), Env: []string{fakeHostEnv + "=" + mode}, ShutdownGrace: grace}
}

// poll checks fact on a ticker, as testkit/procwait does, for up to ten seconds.
func poll(t *testing.T, what string, fact func() bool) {
	t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(10 * time.Second)
	for !fact() {
		select {
		case <-tick.C:
		case <-deadline:
			t.Fatalf("timed out waiting: %s", what)
		}
	}
}

// waitPidGone waits until pid names no process.
func waitPidGone(t *testing.T, pid int) {
	t.Helper()
	poll(t, fmt.Sprintf("pid %d gone", pid), func() bool { return pidGone(pid) })
}

// TC-15b logic: Start refuses Options without a home before it starts anything:
// the binary does not exist, so a Start that got as far as exec would fail with
// the start error instead.
//
//dsh:test DH-T-SDK-17
func TestStartRefusesAMissingHome(t *testing.T) {
	o := fakeOptions(t, "serve")
	o.Home = ""
	o.Bin = filepath.Join(t.TempDir(), "no-such-dsh")
	h, err := Start(t.Context(), o)
	if err == nil || h != nil || !strings.Contains(err.Error(), "Options.Home is required") {
		t.Fatalf("Start = %v, %v; want the missing-home error", h, err)
	}
}

// Start refuses Options without a binary when DSH_BIN is unset too.
//
//dsh:test DH-T-SDK-17
func TestStartNeedsABinary(t *testing.T) {
	t.Setenv(BinEnv, "")
	o := fakeOptions(t, "serve")
	o.Bin = ""
	if _, err := Start(t.Context(), o); err == nil || !strings.Contains(err.Error(), BinEnv) {
		t.Fatalf("Start = %v, want the no-binary error", err)
	}
}

// Start takes DSH_BIN when Options.Bin is empty and runs
// `<bin> --home <dir> --profile sdk`; Close ends it at the shutdown rung.
//
//dsh:test DH-T-SDK-16
func TestStartRunsTheSDKProfileAndClosesAtShutdown(t *testing.T) {
	o := fakeOptions(t, "serve")
	t.Setenv(BinEnv, o.Bin)
	o.Bin = ""
	h, err := Start(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := h.Info().ServerInfo.Version, "--home "+o.Home+" --profile sdk"; got != want {
		t.Fatalf("child args %q, want %q", got, want)
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if e := h.Ending(); e != (Ending{Rung: RungShutdown, ExitCode: 0}) {
		t.Fatalf("ending %+v, want shutdown with 0", e)
	}
	waitPidGone(t, h.Pid())
}

// TC-15b: a child that ignores shutdown is terminated and, failing that,
// killed; the pid is gone either way.
//
//dsh:test DH-T-SDK-16
func TestCloseLadderEscalates(t *testing.T) {
	cases := []struct {
		mode string
		want Rung
	}{{"ignore-term", RungKill}}
	if runtime.GOOS != "windows" {
		cases = append(cases, struct {
			mode string
			want Rung
		}{"ignore", RungTerminate})
	}
	for _, c := range cases {
		t.Run(c.mode, func(t *testing.T) {
			h, err := Start(t.Context(), fakeOptions(t, c.mode))
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			if err := h.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := h.Ending().Rung; got != c.want {
				t.Fatalf("rung %q, want %q", got, c.want)
			}
			if took := time.Since(start); took > 10*time.Second {
				t.Fatalf("Close took %v", took)
			}
			waitPidGone(t, h.Pid())
		})
	}
}
