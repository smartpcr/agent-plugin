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

// ackShell runs the --send command; a test points it at a helper process.
var ackShell = []string{"sh", "-c"}

// recordAcks accepts connections on ln until a line holding token is appended to
// logPath and the --send command has exited 0, or ctx ends; it then reads the log
// back and returns the line it holds. Whatever the outcome, it closes ln and every
// accepted connection, kills a send command still running, and joins every
// goroutine it started before the log is closed and it returns.
func recordAcks(ctx context.Context, ln net.Listener, logPath, token, send string, stderr io.Writer) (string, error) {
	logf, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		ln.Close()
		return "", err
	}
	defer logf.Close()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu      sync.Mutex // guards logf writes, conns and closed
		conns   = map[net.Conn]bool{}
		closed  bool
		workers sync.WaitGroup
		once    sync.Once
	)
	arrived := make(chan struct{})
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			if closed {
				mu.Unlock()
				c.Close()
				return
			}
			conns[c] = true
			workers.Add(1)
			mu.Unlock()
			go func() {
				defer workers.Done()
				defer func() { mu.Lock(); delete(conns, c); mu.Unlock(); c.Close() }()
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
	// stop closes the listener and every open connection and joins the workers.
	stop := func() {
		ln.Close()
		mu.Lock()
		closed = true
		for c := range conns {
			c.Close()
		}
		mu.Unlock()
		workers.Wait()
	}

	var sendDone chan error
	if send != "" {
		cmd := exec.CommandContext(runCtx, ackShell[0], append(ackShell[1:], send)...)
		cmd.Stdout, cmd.Stderr = stderr, stderr
		cmd.WaitDelay = 2 * time.Second
		sendDone = make(chan error, 1)
		if err := cmd.Start(); err != nil {
			stop()
			return "", fmt.Errorf("the send command: %w", err)
		}
		go func() { sendDone <- cmd.Wait() }()
	}
	var errs []error
	select {
	case <-arrived:
	case <-ctx.Done():
		errs = append(errs, fmt.Errorf("no acknowledgment holding %q arrived at %s", token, ln.Addr()))
	}
	if sendDone != nil {
		// After the acknowledgment the sender is joined within what is left of
		// the timeout; one still running then is killed and fails the probe.
		var serr error
		select {
		case serr = <-sendDone:
		default:
			select {
			case serr = <-sendDone:
			case <-ctx.Done():
				cancel()
				<-sendDone
				serr = errors.New("still running at the timeout; killed")
			}
		}
		if serr != nil {
			errs = append(errs, fmt.Errorf("the send command: %w", serr))
		}
	}
	stop()
	if len(errs) > 0 {
		return "", errors.Join(errs...)
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
