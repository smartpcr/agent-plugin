package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAckProbeRecordsAndReadsBack: service:ack-recorder's preflight passes only
// once the test acknowledgment is in the log and read back; other lines are
// recorded too, and no acknowledgment within the timeout fails.
func TestAckProbeRecordsAndReadsBack(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	log := filepath.Join(t.TempDir(), "ack.log")
	go func() {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			return
		}
		defer c.Close()
		fmt.Fprint(c, "noise\nguest probe-1 seq 1 probe-1-ack\n")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got, err := recordAcks(ctx, ln, log, "probe-1-ack", "", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if got != "guest probe-1 seq 1 probe-1-ack" {
		t.Errorf("read back %q", got)
	}
	data, err := os.ReadFile(log)
	if err != nil || !strings.HasPrefix(string(data), "noise\n") {
		t.Errorf("log = %q, %v; want every line recorded", data, err)
	}

	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln2.Close() })
	short, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	if _, err := recordAcks(short, ln2, filepath.Join(t.TempDir(), "none.log"), "never", "", io.Discard); err == nil {
		t.Error("no acknowledgment arrived, yet the probe passed")
	}
}

// TestAckProbeHelperProcess is the --send command of the tests below, run as
// ackShell: "send ADDR LINE" sends LINE to ADDR, "fail" exits 3, "hang ADDR
// LINE" sends LINE and then keeps running.
func TestAckProbeHelperProcess(t *testing.T) {
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		return
	}
	f := strings.Fields(args[1])
	switch f[0] {
	case "fail":
		os.Exit(3)
	case "send", "hang":
		c, err := net.Dial("tcp", f[1])
		if err != nil {
			os.Exit(4)
		}
		fmt.Fprintln(c, f[2])
		c.Close()
		if f[0] == "hang" {
			// Block on a signal that never comes (no one dials this listener),
			// so the process runs until the probe kills it.
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				os.Exit(4)
			}
			t.Cleanup(func() { l.Close() })
			l.Accept()
		}
		os.Exit(0)
	}
}

func helperShell(t *testing.T) {
	t.Helper()
	old := ackShell
	ackShell = []string{os.Args[0], "-test.run=^TestAckProbeHelperProcess$", "--"}
	t.Cleanup(func() { ackShell = old })
}

// TestAckProbeReleasesAnOpenConnection: a client that keeps its socket open at
// the timeout does not hold the probe; it fails promptly with every worker joined.
func TestAckProbeReleasesAnOpenConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprint(c, "noise\n")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := recordAcks(ctx, ln, filepath.Join(t.TempDir(), "ack.log"), "never", "", io.Discard); err == nil {
		t.Fatal("no acknowledgment arrived, yet the probe passed")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("recordAcks returned after %v with a connection open", d)
	}
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Error("the probe left the client's connection open")
	}
}

// TestAckProbeJoinsTheSender: the --send command is joined: an acknowledgment
// whose sender exits 0 passes, a sender failing fails the probe, and one still
// running after its acknowledgment is killed at the timeout and fails it.
func TestAckProbeJoinsTheSender(t *testing.T) {
	helperShell(t)
	for _, tc := range []struct {
		name, send string
		pass       bool
		why        string
	}{
		{"sends and exits", "send {addr} tok-1", true, ""},
		{"fails", "fail", false, "the send command"},
		{"still running", "hang {addr} tok-1", false, "still running at the timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { ln.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			start := time.Now()
			send := strings.ReplaceAll(tc.send, "{addr}", ln.Addr().String())
			got, err := recordAcks(ctx, ln, filepath.Join(t.TempDir(), "ack.log"), "tok-1", send, io.Discard)
			if tc.pass && (err != nil || got != "tok-1") {
				t.Errorf("got %q, %v; want the acknowledgment read back", got, err)
			}
			if !tc.pass && (err == nil || !strings.Contains(err.Error(), tc.why)) {
				t.Errorf("err = %v, want the send command's failure", err)
			}
			if d := time.Since(start); d > 15*time.Second {
				t.Errorf("recordAcks took %v; the sender was not killed", d)
			}
		})
	}
}

func TestAckProbeUsage(t *testing.T) {
	if code := runAckProbe([]string{"--listen", "127.0.0.1:0"}, io.Discard, io.Discard); code != 2 {
		t.Errorf("missing --token and --log: exit %d, want 2", code)
	}
}
