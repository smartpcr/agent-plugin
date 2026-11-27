// Package diag is the host's fatal path (DH-D-HOST-7): a host that cannot continue
// prints one message on standard error and writes one report file under
// <home>/reports/. Only the first fatal error of a process is reported; the report
// holds the facts, the error chain, every goroutine's stack and the tail of the log,
// with secrets redacted.
package diag

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

const (
	// MaxReports is how many report files a home keeps; older ones are pruned.
	MaxReports = 10
	// RingSize is how much of the log tail a report carries.
	RingSize = 64 << 10
	// reportPrefix and reportExt frame every report file name.
	reportPrefix = "report-"
	reportExt    = ".txt"
	// minSecret is the shortest secret value redacted; shorter values would mangle
	// the report without protecting anything.
	minSecret = 4
)

// Key is the service under which the boot tree provides the fatal path.
var Key = cordis.NewServiceKey[*Path]("host.diag")

// Facts are the launch facts a report starts with.
type Facts struct {
	Version string
	OS      string
	Profile string
}

// Config configures a fatal path.
type Config struct {
	// Reports is the directory reports are written to: <home>/reports.
	Reports string
	// Stderr receives the one fatal message.
	Stderr io.Writer
	Facts  Facts
	// Ring is the log tail a report carries; nil carries none.
	Ring *Ring
	// Secrets are values redacted from the message and the report: the fallback
	// that holds with no secret store.
	Secrets []string
	// Masker, when set, also masks the message and the report: the process's
	// secret store (*secret.Store), which masks every value it has released.
	// SetMasker attaches one later, once the store exists.
	Masker Masker
	// Now is the clock of the report; nil is time.Now.
	Now func() time.Time
	// Release runs after the report is written, bounded by ReleaseTimeout; nil runs
	// nothing. It lets the host release what it holds (FatalReleaseTimeout).
	Release        func(ctx context.Context)
	ReleaseTimeout time.Duration
}

// Masker masks secret values in a text; *secret.Store is one. It is an interface
// so that this package imports nothing of internal/human.
type Masker interface {
	MaskString(text string) string
}

// maskFailed replaces a text whose masker panicked: nothing unmasked is written.
const maskFailed = "[redacted: the secret store could not mask this text]"

// Path is a fatal path. Fatal reports once; later calls are not reported.
type Path struct {
	c      Config
	masker atomic.Pointer[Masker]
	mu     sync.Mutex
	said   bool
	report string
}

// New returns a fatal path.
func New(c Config) *Path {
	if c.Stderr == nil {
		c.Stderr = io.Discard
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	c.Secrets = slices.DeleteFunc(slices.Clone(c.Secrets), func(s string) bool { return len(s) < minSecret })
	// longest first, so a secret that contains another is redacted whole
	slices.SortFunc(c.Secrets, func(a, b string) int { return len(b) - len(a) })
	p := &Path{c: c}
	p.SetMasker(c.Masker)
	return p
}

// SetMasker makes m mask every later message and report, in addition to
// Config.Secrets; nil removes it. It is safe to call while a fatal error is
// reported.
func (p *Path) SetMasker(m Masker) {
	if m == nil {
		p.masker.Store(nil)
		return
	}
	p.masker.Store(&m)
}

// Said reports whether a fatal error was reported, and the report it wrote ("" when
// the write failed).
func (p *Path) Said() (bool, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.said, p.report
}

// Fatal reports err as the fatal error of the process, raised by source (an entry
// id, "boot", "panic"): it writes the report, prints one message naming it, then
// runs the release hook bounded by the release timeout. It returns the report path
// ("" when it could not be written) and whether this call was the one reported; a
// second call prints and writes nothing. Fatal never exits the process.
func (p *Path) Fatal(source string, err error) (report string, first bool) {
	p.mu.Lock()
	if p.said {
		p.mu.Unlock()
		return "", false
	}
	p.said = true
	if err == nil {
		err = errors.New("fatal error")
	}
	text := p.redact(err.Error())
	report, werr := p.write(source, err, text)
	p.report = report
	p.mu.Unlock()

	head, rest, _ := strings.Cut(strings.TrimSpace(text), "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "dsh: fatal: %s: %s", source, head)
	if werr != nil {
		fmt.Fprintf(&b, " (no report: %s)\n", p.redact(werr.Error()))
	} else {
		fmt.Fprintf(&b, " (report: %s)\n", report)
	}
	for _, line := range strings.Split(rest, "\n") {
		if strings.TrimSpace(line) != "" {
			b.WriteString("  " + line + "\n")
		}
	}
	_, _ = io.WriteString(p.c.Stderr, b.String())
	p.release()
	return report, true
}

func (p *Path) release() {
	if p.c.Release == nil {
		return
	}
	timeout := p.c.ReleaseTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		p.c.Release(ctx)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// write writes the report file and prunes the directory to MaxReports.
func (p *Path) write(source string, err error, text string) (string, error) {
	if p.c.Reports == "" {
		return "", errors.New("no report directory")
	}
	now := p.c.Now().UTC()
	path := filepath.Join(p.c.Reports, reportName(now, os.Getpid()))
	var b bytes.Buffer
	fmt.Fprintf(&b, "dsh fatal report\n\n")
	fmt.Fprintf(&b, "time:    %s\n", now.Format(time.RFC3339Nano))
	fmt.Fprintf(&b, "version: %s\n", p.c.Facts.Version)
	fmt.Fprintf(&b, "os:      %s/%s (%s)\n", runtime.GOOS, runtime.GOARCH, p.c.Facts.OS)
	fmt.Fprintf(&b, "profile: %s\n", p.c.Facts.Profile)
	fmt.Fprintf(&b, "source:  %s\n", source)
	fmt.Fprintf(&b, "\n== error\n%s\n", text)
	b.WriteString("\n== chain\n")
	for i, e := range chain(err) {
		reason := ""
		if r, ok := herr.ReasonOf(e); ok {
			reason = " [" + string(r) + "]"
		}
		fmt.Fprintf(&b, "%d. %T%s: %s\n", i, e, reason, p.redact(e.Error()))
	}
	b.WriteString("\n== goroutines\n")
	b.WriteString(p.redact(stacks()))
	b.WriteString("\n== log tail\n")
	if p.c.Ring != nil {
		b.WriteString(p.redact(string(p.c.Ring.Bytes())))
	}
	if err := fsx.CreateIfAbsent(path, fsx.Bytes(b.Bytes()), fsx.Options{Perm: 0o600}); err != nil {
		return "", err
	}
	prune(p.c.Reports)
	return path, nil
}

// redact replaces every secret value in s: the configured values, then what the
// masker masks. A masker that panics fails closed: the whole text is replaced.
func (p *Path) redact(s string) (out string) {
	for _, secret := range p.c.Secrets {
		s = strings.ReplaceAll(s, secret, herr.Redacted)
	}
	m := p.masker.Load()
	if m == nil || *m == nil {
		return s
	}
	defer func() {
		if recover() != nil {
			out = maskFailed
		}
	}()
	return (*m).MaskString(s)
}

// chain is err and its causes, depth first.
func chain(err error) []error {
	var out []error
	var walk func(error)
	walk = func(e error) {
		if e == nil || len(out) >= 32 {
			return
		}
		out = append(out, e)
		switch u := e.(type) {
		case interface{ Unwrap() []error }:
			for _, c := range u.Unwrap() {
				walk(c)
			}
		case interface{ Unwrap() error }:
			walk(u.Unwrap())
		}
	}
	walk(err)
	return out
}

// stacks returns every goroutine's stack.
func stacks() string {
	buf := make([]byte, 64<<10)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) || len(buf) >= 8<<20 {
			return string(buf[:n])
		}
		buf = make([]byte, 2*len(buf))
	}
}

// Reports lists the report files of dir, oldest first.
func Reports(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if n := e.Name(); e.Type().IsRegular() && strings.HasPrefix(n, reportPrefix) && strings.HasSuffix(n, reportExt) {
			names = append(names, n)
		}
	}
	slices.Sort(names) // the UTC timestamp sorts by name
	return names, nil
}

// reportTime is the time layout of a report file name.
const reportTime = "20060102T150405.000000000Z"

func reportName(at time.Time, pid int) string {
	return fmt.Sprintf("%s%s-%d%s", reportPrefix, at.UTC().Format(reportTime), pid, reportExt)
}

// ReportOf returns the path of the newest report under dir that process pid wrote
// at or after since, or "" when it wrote none.
func ReportOf(dir string, pid int, since time.Time) string {
	names, err := Reports(dir)
	if err != nil {
		return ""
	}
	suffix := fmt.Sprintf("-%d%s", pid, reportExt)
	for _, n := range slices.Backward(names) {
		stamp, ok := strings.CutSuffix(strings.TrimPrefix(n, reportPrefix), suffix)
		if !ok {
			continue
		}
		if at, err := time.Parse(reportTime, stamp); err == nil && !at.Before(since.UTC().Truncate(time.Second)) {
			return filepath.Join(dir, n)
		}
	}
	return ""
}

// Unclean describes a host that ended without a clean stop: its runtime record was
// left behind with a free lock.
type Unclean struct {
	Origin    string
	PID       int
	Profile   string
	Version   string
	StartedAt time.Time
	// CrashFile is the runtime's crash output of the home when it holds bytes.
	CrashFile string
}

// UncleanReport returns the report of the dead host u: the one it wrote itself
// (Fatal, at or after its start), else the one an earlier start wrote for it, else
// a new one written now under dir naming what is known. It is written once per dead
// host, so every later start names the same file.
func UncleanReport(dir string, u Unclean, now time.Time) (string, error) {
	if path := ReportOf(dir, u.PID, u.StartedAt); path != "" {
		return path, nil
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "dsh unclean exit report\n\n")
	fmt.Fprintf(&b, "time:    %s\n", now.UTC().Format(time.RFC3339Nano))
	fmt.Fprintf(&b, "version: %s\n", u.Version)
	fmt.Fprintf(&b, "profile: %s\n", u.Profile)
	fmt.Fprintf(&b, "origin:  %s\n", u.Origin)
	fmt.Fprintf(&b, "pid:     %d\n", u.PID)
	fmt.Fprintf(&b, "started: %s\n", u.StartedAt.UTC().Format(time.RFC3339Nano))
	b.WriteString("\n== error\nThe host ended without a clean stop: it was killed, lost its machine, or\n" +
		"crashed before it could write a report. Its runtime record was left behind.\n")
	if u.CrashFile != "" {
		fmt.Fprintf(&b, "\n== crash output\n%s\n", u.CrashFile)
	}
	path := filepath.Join(dir, reportName(now, u.PID))
	if err := fsx.CreateIfAbsent(path, fsx.Bytes(b.Bytes()), fsx.Options{Perm: 0o600}); err != nil {
		return "", err
	}
	prune(dir)
	return path, nil
}

// prune removes the oldest reports beyond MaxReports.
func prune(dir string) {
	names, err := Reports(dir)
	if err != nil || len(names) <= MaxReports {
		return
	}
	for _, n := range names[:len(names)-MaxReports] {
		_ = os.Remove(filepath.Join(dir, n))
	}
}
