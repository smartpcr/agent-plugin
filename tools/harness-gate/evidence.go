package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"
)

// An evidence record (--evidence FILE; M1-G-TOOL-12, M1-G-TOOL-18) is one gate run
// a reviewer can check without a CI run ID:
//
//	harness-gate evidence record
//	commit: <git rev-parse HEAD>
//	tree: clean (git status --porcelain --untracked-files=all is empty)
//	... the system, toolchain and compiler (evidenceHeader) ...
//	started: <UTC>
//	--- output ---
//	<the full gate output, also written to stdout>
//	--- end of output ---
//	ended: <UTC>
//	exit: <status>
//	sha256: <hex>
//
// The final sha256 is the SHA-256 of every byte of the file before that line, so
// "head -n -1 FILE | sha256sum" reproduces it. A tree that is not clean is refused
// (exit 2, naming the dirty paths) before any file is written or gate is started.

// DirtyTreeError is the refusal of an evidence run on a tree with changes.
type DirtyTreeError struct {
	Root  string
	Paths []string // as git status --porcelain prints them, relative to the repository top level
}

func (e *DirtyTreeError) Error() string {
	return fmt.Sprintf("the tree at %s is not clean; commit or remove every change before recording evidence:\n  %s",
		e.Root, strings.Join(e.Paths, "\n  "))
}

// CleanCommit returns the commit checked out at root, or a *DirtyTreeError when
// "git status --porcelain" lists any change, untracked files included.
func CleanCommit(ctx context.Context, root string) (string, error) {
	out, err := runCommand(ctx, root, os.Environ(), "git", "-C", root, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD in %s: %w\n%s", root, err, out)
	}
	commit := strings.TrimSpace(out)
	out, err = runCommand(ctx, root, os.Environ(), "git", "-C", root, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", fmt.Errorf("git status --porcelain in %s: %w\n%s", root, err, out)
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) > 3 {
			paths = append(paths, line[3:])
		}
	}
	if len(paths) > 0 {
		return commit, &DirtyTreeError{Root: root, Paths: paths}
	}
	return commit, nil
}

// evidenceHeader is the header of an evidence record of commit, started at start.
// A fact that cannot be read is recorded as such; it does not stop the run.
func (c *Config) evidenceHeader(ctx context.Context, commit string, start time.Time, args []string) string {
	var b strings.Builder
	line := func(key, value string) { fmt.Fprintf(&b, "%s: %s\n", key, value) }
	read := func(name string, args ...string) string {
		out, err := c.command(ctx, c.Root, c.env(), name, args...)
		if err != nil {
			return fmt.Sprintf("unavailable (%s %s: %v)", name, strings.Join(args, " "), err)
		}
		return firstLine(out)
	}
	b.WriteString("harness-gate evidence record\n")
	line("commit", commit)
	line("tree", CleanTreeField)
	line("command", "harness-gate "+strings.Join(args, " "))
	line("goos/goarch", read(c.Go, "env", "GOOS")+"/"+read(c.Go, "env", "GOARCH"))
	line("go version", c.Toolchain.Line)
	line("go floor", fmt.Sprintf("%s (%s); children run with GOTOOLCHAIN=%s", c.Toolchain.Name(), c.Toolchain.Source, c.Toolchain.Name()))
	line("goroot", c.Toolchain.GOROOT)
	cc, err := c.command(ctx, c.Root, c.env("CGO_ENABLED=1"), c.Go, "env", "CC")
	cc = strings.TrimSpace(cc)
	switch fields := strings.Fields(cc); {
	case err != nil:
		line("cc", fmt.Sprintf("unavailable (go env CC: %v)", err))
	case len(fields) == 0:
		line("cc", "none (go env CC is empty)")
	default:
		line("cc", cc)
		line("cc --version", read(fields[0], "--version"))
	}
	line("host os", hostOS(read))
	if runtime.GOOS == "linux" {
		line("wsl distro", orNone(os.Getenv("WSL_DISTRO_NAME")))
		line("os-release", osRelease("/etc/os-release"))
		line("kernel", read("uname", "-r"))
	}
	line("children", "CI=1")
	line("started", start.UTC().Format(time.RFC3339))
	return b.String()
}

// hostOS is the build of the host operating system.
func hostOS(read func(string, ...string) string) string {
	if runtime.GOOS == "windows" {
		return read("cmd", "/c", "ver")
	}
	return read("uname", "-srvm")
}

// osRelease is the PRETTY_NAME of an os-release file.
func osRelease(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Sprintf("unavailable (%v)", err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if v, ok := strings.CutPrefix(s.Text(), "PRETTY_NAME="); ok {
			return strings.Trim(v, `"'`)
		}
	}
	return "unavailable (no PRETTY_NAME in " + path + ")"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// RunEvidence runs gates as one evidence record written to path and teed to
// stdout, and returns the exit status: the gates' status, or 2 when the record
// cannot be written. Every child runs with CI=1, so a golden refuses -update on a
// local lane as it does in CI. The caller has checked the tree with CleanCommit.
func (c *Config) RunEvidence(ctx context.Context, path, commit string, args []string, gates func(*Config) []Gate, stdout, stderr io.Writer) int {
	c.Environ = setEnv(c.Environ, "CI=1")
	start := time.Now()
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate: --evidence: %v\n", err)
		return 2
	}
	h := sha256.New()
	rec := &errWriter{w: io.MultiWriter(f, h)}
	out := io.MultiWriter(rec, stdout)
	io.WriteString(out, c.evidenceHeader(ctx, commit, start, args))
	io.WriteString(out, "--- output ---\n")
	printToolchain(out, c)
	code := ExitCode(Run(ctx, out, gates(c)))
	fmt.Fprintf(out, "--- end of output ---\nended: %s\nexit: %d\n", time.Now().UTC().Format(time.RFC3339), code)
	sum := "sha256: " + hex.EncodeToString(h.Sum(nil)) + "\n"
	_, werr := io.WriteString(f, sum)
	io.WriteString(stdout, sum)
	if err := errors.Join(rec.err, werr, f.Close()); err != nil {
		fmt.Fprintf(stderr, "harness-gate: --evidence %s: %v\n", path, err)
		return 2
	}
	fmt.Fprintf(stdout, "harness-gate: evidence written to %s\n", path)
	return code
}

// errWriter keeps the first write error, so a failed record is reported once.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return len(p), nil
	}
	if _, err := e.w.Write(p); err != nil {
		e.err = err
	}
	return len(p), nil
}
