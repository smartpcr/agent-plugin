package diag

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

func TestFatalPrintsOneMessageAndWritesOneReport(t *testing.T) {
	dir := t.TempDir()
	var stderr bytes.Buffer
	ring := NewRing(0)
	_, _ = ring.Write([]byte("an earlier log line token=hunter2-secret\n"))
	p := New(Config{
		Reports: dir, Stderr: &stderr, Ring: ring,
		Facts:   Facts{Version: "v1.2.3", OS: "testos", Profile: "web"},
		Secrets: []string{"hunter2-secret", "abc"},
	})
	cause := herr.New("x/broken", "the cause carries hunter2-secret")
	report, first := p.Fatal("probe", fmt.Errorf("probe failed\ndetail line: %w", cause))
	if !first || report == "" {
		t.Fatalf("Fatal = %q, %v; want a report and the first call", report, first)
	}
	if n := strings.Count(stderr.String(), "dsh: fatal:"); n != 1 {
		t.Fatalf("stderr has %d fatal messages, want 1:\n%s", n, stderr.String())
	}
	if !strings.HasPrefix(stderr.String(), "dsh: fatal: probe: probe failed (report: "+report+")\n") {
		t.Errorf("stderr = %q", stderr.String())
	}
	names, err := Reports(dir)
	if err != nil || len(names) != 1 || filepath.Join(dir, names[0]) != report {
		t.Fatalf("reports = %q, %v; want just %s", names, err, report)
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"version: v1.2.3", "profile: web", "source:  probe", "[x/broken]", "== goroutines", "goroutine ", "an earlier log line", herr.Redacted} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if strings.Contains(text, "hunter2-secret") || strings.Contains(stderr.String(), "hunter2-secret") {
		t.Error("a secret value was not redacted")
	}

	// a second fatal error is not reported
	stderr.Reset()
	if r, again := p.Fatal("other", errors.New("second")); again || r != "" || stderr.Len() != 0 {
		t.Errorf("second Fatal = %q, %v and printed %q; want nothing", r, again, stderr.String())
	}
	if names, _ := Reports(dir); len(names) != 1 {
		t.Errorf("second Fatal wrote a report: %q", names)
	}
}

func TestFatalWithoutWritableReportsStillPrints(t *testing.T) {
	var stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "no", "such", "dir")
	p := New(Config{Reports: missing, Stderr: &stderr})
	report, first := p.Fatal("boot", errors.New("host preparation failed"))
	if !first || report != "" {
		t.Fatalf("Fatal = %q, %v; want no report, first call", report, first)
	}
	got := stderr.String()
	if strings.Count(got, "dsh: fatal:") != 1 || !strings.Contains(got, "host preparation failed (no report: ") {
		t.Errorf("stderr = %q", got)
	}
}

func TestFatalPrunesToTheNewestReports(t *testing.T) {
	dir := t.TempDir()
	for i := range MaxReports + 3 {
		name := fmt.Sprintf("%s20200101T0000%02d.000000000Z-1%s", reportPrefix, i, reportExt)
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(other, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	report, _ := New(Config{Reports: dir, Now: func() time.Time { return now }}).Fatal("boot", errors.New("x"))
	names, err := Reports(dir)
	if err != nil || len(names) != MaxReports || filepath.Join(dir, names[len(names)-1]) != report {
		t.Fatalf("after prune: %q, %v; want %d ending with %s", names, err, MaxReports, report)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("prune removed a file that is not a report: %v", err)
	}
}

func TestFatalReleaseIsBounded(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	p := New(Config{ReleaseTimeout: 50 * time.Millisecond, Release: func(context.Context) { <-block }})
	start := time.Now()
	p.Fatal("boot", errors.New("x"))
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Fatal waited %s for a stuck release", d)
	}
}

func TestRingKeepsTheTail(t *testing.T) {
	r := NewRing(8)
	_, _ = r.Write([]byte("0123456789"))
	_, _ = r.Write([]byte("ab"))
	if got := string(r.Bytes()); got != "456789ab" {
		t.Errorf("ring = %q", got)
	}
}

func TestInstallRemovesAnEmptyCrashFile(t *testing.T) {
	dir := t.TempDir()
	uninstall, err := Install(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, CrashFile)); err != nil {
		t.Fatalf("crash file not created: %v", err)
	}
	uninstall()
	if _, err := os.Stat(filepath.Join(dir, CrashFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("empty crash file left behind: %v", err)
	}
}
