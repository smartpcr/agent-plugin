package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// runAckProbe is "harness-gate ack-probe --listen addr --token t --log file
// [--timeout d] [--send cmd]": the service:ack-recorder preflight of an s18
// power-cut job (architecture.md provided services; runbook-power-cut.md step 2).
// It listens on addr, appends every line a connection sends to the log, runs the
// --send shell command (the throw-away guest sending its test acknowledgment), and
// exits 0 once a line holding the token has been recorded and read back from the
// log; 1 when none arrives within the timeout or the log does not hold it; 2 on a
// usage error or when addr cannot be listened on.
func runAckProbe(args []string, stdout, stderr io.Writer) int {
	var listen, token, logPath, send string
	var timeout time.Duration
	if _, ok := subcommandFlags("ack-probe", args, stderr, func(f *flag.FlagSet) {
		f.StringVar(&listen, "listen", "", "the address acknowledgments arrive at (DSH_SCENARIO_NODEFS_LISTEN)")
		f.StringVar(&token, "token", "", "the text the test acknowledgment carries")
		f.StringVar(&logPath, "log", "", "the append-only file each received line is written to")
		f.StringVar(&send, "send", "", "a shell command that makes the guest send the acknowledgment")
		f.DurationVar(&timeout, "timeout", 2*time.Minute, "how long to wait for the acknowledgment")
	}); !ok {
		return 2
	}
	if listen == "" || token == "" || logPath == "" {
		fmt.Fprintln(stderr, "harness-gate ack-probe: --listen, --token and --log are required")
		return 2
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate ack-probe: %v\n", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	got, err := recordAcks(ctx, ln, logPath, token, send, stderr)
	if err != nil {
		fmt.Fprintf(stdout, "harness-gate ack-probe: FAILED: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "harness-gate ack-probe: passed: recorded and read back %q from %s\n", got, logPath)
	return 0
}

// recordAcks accepts connections on ln until a line holding token is appended to
// logPath or ctx ends, then reads the log back and returns the line it holds. ln is
// closed on return.
func recordAcks(ctx context.Context, ln net.Listener, logPath, token, send string, stderr io.Writer) (string, error) {
	logf, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		ln.Close()
		return "", err
	}
	defer logf.Close()
	var mu sync.Mutex
	arrived := make(chan struct{})
	var once sync.Once
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					line := sc.Text()
					mu.Lock()
					_, werr := fmt.Fprintln(logf, line)
					if werr == nil {
						werr = logf.Sync()
					}
					mu.Unlock()
					if werr == nil && strings.Contains(line, token) {
						once.Do(func() { close(arrived) })
					}
				}
			}()
		}
	}()
	sendDone := make(chan error, 1)
	if send != "" {
		cmd := exec.CommandContext(ctx, "sh", "-c", send)
		cmd.Stdout, cmd.Stderr = stderr, stderr
		go func() { sendDone <- cmd.Run() }()
	}
	var waitErr error
	select {
	case <-arrived:
	case <-ctx.Done():
		waitErr = fmt.Errorf("no acknowledgment holding %q arrived at %s", token, ln.Addr())
	}
	ln.Close()
	if send != "" && waitErr != nil {
		if err := <-sendDone; err != nil {
			waitErr = errors.Join(waitErr, fmt.Errorf("the send command: %w", err))
		}
	}
	if waitErr != nil {
		return "", waitErr
	}
	mu.Lock()
	data, err := os.ReadFile(logPath)
	mu.Unlock()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, token) {
			return line, nil
		}
	}
	return "", fmt.Errorf("%s does not hold the acknowledgment holding %q", logPath, token)
}
