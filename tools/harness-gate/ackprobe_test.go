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
	short, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	if _, err := recordAcks(short, ln2, filepath.Join(t.TempDir(), "none.log"), "never", "", io.Discard); err == nil {
		t.Error("no acknowledgment arrived, yet the probe passed")
	}
}

func TestAckProbeUsage(t *testing.T) {
	if code := runAckProbe([]string{"--listen", "127.0.0.1:0"}, io.Discard, io.Discard); code != 2 {
		t.Errorf("missing --token and --log: exit %d, want 2", code)
	}
}
