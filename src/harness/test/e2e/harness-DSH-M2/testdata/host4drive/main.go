// Command host4drive runs DH-E-HOST-4's killed-host sequence (TC-21s3) on the
// operating system it runs on, for the e2e step definitions of
// dh_s_18_acceptance_on_real_operating_systems_crash_report_on_each_system.feature.
// It is built for each variant and run there (natively, or across WSL), so the kill
// is the variant's own (SIGKILL on Linux, TerminateProcess on Windows) and the
// owner-only check is the variant's file system's.
//
// On a fresh short home it starts "dsh web --no-open --port 0", waits until
// "dsh status" names that process live, kills it with no grace, starts dsh again on
// the same home and waits for the unclean-exit line, checks the report that line
// names, runs "dsh crash", then starts "dsh --safe web" and waits until its listener
// answers. Kill names the forced kill only when the killed host's wait status and
// leftover runtime record prove it. It prints one JSON object (host4Result) on standard output; the step
// definitions assert on it. It exits 0 unless its own arguments are wrong.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
)

const (
	readyWithin = time.Minute
	exitWithin  = time.Minute
	lineWithin  = time.Minute
)

var unclean = regexp.MustCompile(`did not stop cleanly; report: (\S+);.*--safe`)

type host4Result struct {
	GOOS       string `json:"goos"`
	Kill       string `json:"kill"`
	Home       string `json:"home"`
	ReportsDir string `json:"reportsDir"`

	FirstPID    int    `json:"firstPid"`
	FirstReady  string `json:"firstReady"`
	FirstStderr string `json:"firstStderr"`
	KilledExit  bool   `json:"killedExit"`
	// What the kill left behind, as observed: the wait status of the killed host
	// and how many runtime records (run/web-*.json, removed by a clean stop) remain.
	KilledCode     int    `json:"killedCode"`
	KilledSignaled bool   `json:"killedSignaled"`
	KilledSignal   string `json:"killedSignal"`
	KilledRecords  int    `json:"killedRecords"`

	NextStderr  string `json:"nextStderr"`
	UncleanLine string `json:"uncleanLine"`
	Report      string `json:"report"`
	ReportUnder bool   `json:"reportUnder"`
	ReportErr   string `json:"reportErr"`
	PrivateErr  string `json:"privateErr"`
	ReportText  string `json:"reportText"`
	CrashCode   int    `json:"crashCode"`
	CrashOut    string `json:"crashOut"`

	SafePID    int    `json:"safePid"`
	SafeReady  string `json:"safeReady"`
	SafeStderr string `json:"safeStderr"`

	Fatal string `json:"fatal"`
}

// buffer is a goroutine-safe output sink.
type buffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type host struct {
	cmd    *exec.Cmd
	stderr *buffer
	done   chan struct{}
}

func (h *host) exited(within time.Duration) bool {
	select {
	case <-h.done:
		return true
	case <-time.After(within):
		return false
	}
}

// kill is the forced rung alone: Process.Kill is SIGKILL on POSIX and
// TerminateProcess on Windows, so the host cannot remove its runtime record.
func (h *host) kill() { _ = h.cmd.Process.Kill() }

// killedBy names the forced kill the observed evidence shows, or describes the
// evidence when it shows none: on POSIX the host must have died of SIGKILL; on
// Windows it must have exited with TerminateProcess's code 1 (the code
// Process.Kill passes) and left its runtime record, so its clean stop never ran.
func killedBy(r *host4Result) string {
	switch {
	case runtime.GOOS != "windows" && r.KilledSignaled && r.KilledSignal == syscall.SIGKILL.String():
		return "SIGKILL"
	case runtime.GOOS == "windows" && !r.KilledSignaled && r.KilledCode == 1 && r.KilledRecords > 0:
		return "TerminateProcess"
	}
	return fmt.Sprintf("unproven (exit %d, signaled %t, signal %q, runtime records left %d)",
		r.KilledCode, r.KilledSignaled, r.KilledSignal, r.KilledRecords)
}

// observeKill records the killed host's wait status and the runtime records it
// left, then sets r.Kill from that evidence.
func (d *driver) observeKill(h *host, r *host4Result) {
	st := h.cmd.ProcessState
	r.KilledCode = st.ExitCode()
	if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		r.KilledSignaled, r.KilledSignal = true, ws.Signal().String()
	}
	recs, _ := filepath.Glob(filepath.Join(home.New(d.root).Run(), "web-*.json"))
	r.KilledRecords = len(recs)
	r.Kill = killedBy(r)
}

type driver struct {
	bin, root string
	env       []string
	hosts     []*host
}

func (d *driver) start(args ...string) (*host, error) {
	h := &host{stderr: &buffer{}, done: make(chan struct{})}
	h.cmd = exec.Command(d.bin, append([]string{"--home", d.root}, args...)...)
	h.cmd.Env, h.cmd.Dir, h.cmd.Stdout, h.cmd.Stderr = d.env, filepath.Dir(d.root), h.stderr, h.stderr
	if err := h.cmd.Start(); err != nil {
		return nil, err
	}
	go func() { defer close(h.done); _ = h.cmd.Wait() }()
	d.hosts = append(d.hosts, h)
	return h, nil
}

func (d *driver) run(args ...string) (int, string, string) {
	cmd := exec.Command(d.bin, append([]string{"--home", d.root}, args...)...)
	var out, errb bytes.Buffer
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = d.env, filepath.Dir(d.root), &out, &errb
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, out.String(), errb.String()
	case errors.As(err, &ee):
		return ee.ExitCode(), out.String(), errb.String()
	default:
		return -1, out.String(), err.Error()
	}
}

// answers waits until "dsh status" exits 0 naming h's process live, and returns
// that status line.
func (d *driver) answers(h *host) (string, error) {
	pid := "process " + strconv.Itoa(h.cmd.Process.Pid)
	deadline := time.Now().Add(readyWithin)
	var last string
	for time.Now().Before(deadline) {
		select {
		case <-h.done:
			return "", fmt.Errorf("the host exited %d before its listener answered:\n%s", h.cmd.ProcessState.ExitCode(), h.stderr)
		default:
		}
		code, out, errs := d.run("status")
		last = fmt.Sprintf("exit %d\n%s%s", code, out, errs)
		if code == 0 {
			for _, line := range strings.Split(out, "\n") {
				if strings.Contains(line, pid+"\t") && strings.Contains(line, "\tlive") {
					return strings.TrimSpace(line), nil
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", fmt.Errorf("dsh status did not name %s live within %s; last:\n%s\nhost stderr:\n%s", pid, readyWithin, last, h.stderr)
}

// waitLine waits for h's unclean-exit line naming process pid and returns the
// line and the report it names.
func (d *driver) waitLine(h *host, pid int) (string, string, error) {
	want := "(process " + strconv.Itoa(pid) + ")"
	find := func() (string, string, bool) {
		for _, line := range strings.Split(h.stderr.String(), "\n") {
			if m := unclean.FindStringSubmatch(line); m != nil && strings.Contains(line, want) {
				return strings.TrimSpace(line), m[1], true
			}
		}
		return "", "", false
	}
	deadline := time.After(lineWithin)
	for {
		if line, report, ok := find(); ok {
			return line, report, nil
		}
		select {
		case <-h.done:
			if line, report, ok := find(); ok {
				return line, report, nil
			}
			return "", "", fmt.Errorf("dsh exited %d with no unclean-exit line naming %s:\n%s", h.cmd.ProcessState.ExitCode(), want, h.stderr)
		case <-deadline:
			return "", "", fmt.Errorf("no unclean-exit line naming %s within %s:\n%s", want, lineWithin, h.stderr)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (d *driver) sequence(r *host4Result) error {
	first, err := d.start("web", "--no-open", "--port", "0")
	if err != nil {
		return fmt.Errorf("starting the first host: %w", err)
	}
	r.FirstPID = first.cmd.Process.Pid
	r.FirstReady, err = d.answers(first)
	r.FirstStderr = first.stderr.String()
	if err != nil {
		return err
	}
	first.kill()
	if r.KilledExit = first.exited(exitWithin); !r.KilledExit {
		return fmt.Errorf("the killed host %d did not exit within %s", r.FirstPID, exitWithin)
	}
	d.observeKill(first, r)

	next, err := d.start("web", "--no-open", "--port", "0")
	if err != nil {
		return fmt.Errorf("starting dsh again: %w", err)
	}
	r.UncleanLine, r.Report, err = d.waitLine(next, r.FirstPID)
	r.NextStderr = next.stderr.String()
	if err != nil {
		return err
	}
	r.ReportUnder = inside(r.ReportsDir, r.Report)
	if f, err := os.Open(r.Report); err != nil {
		r.ReportErr = err.Error()
	} else {
		if err := home.CheckPrivateFile(f); err != nil {
			r.PrivateErr = err.Error()
		}
		_ = f.Close()
		if b, err := os.ReadFile(r.Report); err != nil {
			r.ReportErr = err.Error()
		} else {
			r.ReportText = string(b)
		}
	}
	var crashErr string
	r.CrashCode, r.CrashOut, crashErr = d.run("crash")
	if r.CrashCode != 0 {
		r.CrashOut += crashErr
	}
	if _, err := d.answers(next); err != nil {
		return fmt.Errorf("the restarted host: %w", err)
	}
	next.kill()
	if !next.exited(exitWithin) {
		return fmt.Errorf("the restarted host did not exit within %s of its kill", exitWithin)
	}

	safe, err := d.start("--safe", "web", "--no-open", "--port", "0")
	if err != nil {
		return fmt.Errorf("starting dsh --safe web: %w", err)
	}
	r.SafePID = safe.cmd.Process.Pid
	r.SafeReady, err = d.answers(safe)
	r.SafeStderr = safe.stderr.String()
	return err
}

func removeAll(dir string) {
	deadline := time.Now().Add(10 * time.Second)
	for os.RemoveAll(dir) != nil && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
}

func main() {
	bin := flag.String("bin", "", "the dsh binary under test")
	flag.Parse()
	if *bin == "" {
		fmt.Fprintln(os.Stderr, "host4drive: -bin is required")
		os.Exit(2)
	}
	r := &host4Result{GOOS: runtime.GOOS, Kill: "not observed"}
	// A short home: the web host's control socket lives in <home>/run and a Unix
	// socket path is bounded (about 108 bytes).
	dir, err := os.MkdirTemp("", "dsh-h4e-")
	if err != nil {
		r.Fatal = err.Error()
	} else {
		root := filepath.Join(dir, "home")
		r.Home, r.ReportsDir = root, home.New(root).Reports()
		env := []string{"DSH_HOME=" + root}
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(strings.ToUpper(kv), "DSH_HOME=") {
				env = append(env, kv)
			}
		}
		d := &driver{bin: *bin, root: root, env: env}
		if err := d.sequence(r); err != nil {
			r.Fatal = err.Error()
		}
		for _, h := range d.hosts {
			h.kill()
			h.exited(exitWithin)
		}
		removeAll(dir)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(r)
}
