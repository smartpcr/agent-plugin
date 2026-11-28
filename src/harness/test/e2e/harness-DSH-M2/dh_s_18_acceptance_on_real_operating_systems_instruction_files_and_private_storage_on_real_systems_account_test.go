//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ifpsAccount is a real second OS account: every list and read is a process run
// as it.
type ifpsAccount struct {
	name string
	how  string
	// base is a directory both accounts can traverse; "" is the temp directory.
	base string
	// as lists the directory or prints the file path as the account; it fails
	// (non-zero exit) when the OS denies the access.
	as    func(dir bool, path string) (string, error)
	close func()
}

var (
	errIfpsAccountDenied = errors.New("the second account's access was refused")
	errIfpsAllowed       = errors.New("the second account's access succeeded")
)

// ifpsNoAccount names what a machine without a second account lacks; the
// required scenario fails there rather than skip (runbook-two-account.md, M2-EN-5).
const ifpsNoAccount = "no second OS account (second-account preflight failed): set DSH_SCENARIO_STORAGE6_USER, _PUBLIC, _LIST and _READ " +
	"per docs/stories/harness-DSH-M2/evidence/runbook-two-account.md, or, on POSIX, run as root or with passwordless sudo"

// ifpsNotRunner: name is an account other than the one this run is.
func ifpsNotRunner(name string) error {
	me, err := user.Current()
	if err != nil {
		return err
	}
	other, err := user.Lookup(name)
	if err != nil {
		return fmt.Errorf("the second account %s is not an account of this machine: %w", name, err)
	}
	if other.Uid == me.Uid {
		return fmt.Errorf("the second account %s is the account this run is (%s)", name, me.Username)
	}
	return nil
}

// ifpsDenialText is how the OS's refusal reads: EACCES on POSIX
// ("Permission denied"), ERROR_ACCESS_DENIED on Windows ("Access is denied").
var ifpsDenialText = regexp.MustCompile(`(?i)permission denied|access is denied`)

// ifpsOSDenied: a failed access is the OS's refusal only when the command ran to
// its end and printed the refusal; a timeout, a missing tool, a wrapper that
// could not switch accounts or any other failure is not.
func ifpsOSDenied(out string, err error) error {
	if err == nil {
		return errIfpsAllowed
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("timed out, not a denial: %w", err)
	}
	if !ifpsDenialText.MatchString(out) {
		return fmt.Errorf("failed without an OS denial (%v): %s", err, strings.TrimSpace(out))
	}
	return fmt.Errorf("%w: %v", errIfpsAccountDenied, err)
}

func ifpsExpand(template string, vars map[string]string) string {
	for k, v := range vars {
		template = strings.ReplaceAll(template, "{"+k+"}", v)
	}
	return template
}

func ifpsExec(cmd *exec.Cmd) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	c.Dir, c.Env, c.SysProcAttr = cmd.Dir, cmd.Env, cmd.SysProcAttr
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, &out
	err := c.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("%w: %v", ctx.Err(), err)
	}
	return out.String(), err
}

// ifpsRunbookAccount is the account the runbook supplies, the inputs of the
// s18 two-account jobs (TestAnotherLocalUserCannotReadStoredData's contract).
func ifpsRunbookAccount() (*ifpsAccount, error) {
	user, public := os.Getenv("DSH_SCENARIO_STORAGE6_USER"), os.Getenv("DSH_SCENARIO_STORAGE6_PUBLIC")
	listT, readT := os.Getenv("DSH_SCENARIO_STORAGE6_LIST"), os.Getenv("DSH_SCENARIO_STORAGE6_READ")
	if user == "" && public == "" && listT == "" && readT == "" {
		return nil, nil
	}
	for name, v := range map[string]string{"DSH_SCENARIO_STORAGE6_USER": user, "DSH_SCENARIO_STORAGE6_PUBLIC": public, "DSH_SCENARIO_STORAGE6_LIST": listT, "DSH_SCENARIO_STORAGE6_READ": readT} {
		if v == "" {
			return nil, fmt.Errorf("%s is not set while other DSH_SCENARIO_STORAGE6_* inputs are", name)
		}
	}
	for name, line := range map[string]string{"DSH_SCENARIO_STORAGE6_LIST": listT, "DSH_SCENARIO_STORAGE6_READ": readT} {
		if !strings.Contains(line, "{path}") {
			return nil, fmt.Errorf("%s does not name {path}: %s", name, line)
		}
	}
	if err := ifpsNotRunner(user); err != nil {
		return nil, err
	}
	return &ifpsAccount{
		name: user, how: "the runbook's account", base: public,
		as: func(dir bool, path string) (string, error) {
			t := readT
			if dir {
				t = listT
			}
			return ifpsExec(ifpsShell(ifpsExpand(t, map[string]string{"path": path, "user": user})))
		},
		close: func() {},
	}, nil
}

// ifpsSecondAccount returns the runbook's account, else one made for this
// scenario where the run may make accounts, else fails: a required scenario
// never skips.
func ifpsSecondAccount() (*ifpsAccount, error) {
	if a, err := ifpsRunbookAccount(); a != nil || err != nil {
		return a, err
	}
	if a, err := ifpsEphemeralAccount(); a != nil || err != nil {
		return a, err
	}
	return nil, errors.New(ifpsNoAccount)
}

func ifpsRandName() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "dshs18e2e" + hex.EncodeToString(b)
}

func (w *ifpsWorld) secondAccount() error {
	a, err := ifpsSecondAccount()
	if err != nil {
		fmt.Fprintf(os.Stderr, "DH-E-STORAGE-6: %v\n", err)
		return err
	}
	w.cleanup = append(w.cleanup, a.close)
	w.account = a
	fmt.Fprintf(os.Stderr, "DH-E-STORAGE-6: second account %s (%s)\n", a.name, a.how)
	return nil
}

// sharedDir makes a directory in the account's base that both accounts can
// traverse and list.
func (w *ifpsWorld) sharedDir(pattern string) (string, error) {
	base := ""
	if w.account != nil {
		base = w.account.base
	}
	d, err := os.MkdirTemp(base, pattern)
	if err != nil {
		return "", err
	}
	w.cleanup = append(w.cleanup, func() { _ = os.RemoveAll(d) })
	if d, err = filepath.EvalSymlinks(d); err != nil {
		return "", err
	}
	return d, ifpsOpenToOthers(d)
}

func (w *ifpsWorld) accountControl() error {
	dir, err := w.sharedDir("ifps-control-")
	if err != nil {
		return err
	}
	p := filepath.Join(dir, "open.txt")
	if err := os.WriteFile(p, []byte("IFPS-CONTROL open to everyone\n"), 0o644); err != nil {
		return err
	}
	if err := ifpsOpenToOthers(p); err != nil {
		return err
	}
	if out, err := w.account.as(true, dir); err != nil || !strings.Contains(out, "open.txt") {
		return fmt.Errorf("%s cannot list the shared directory %s, so a denial would prove nothing: %v\n%s", w.account.name, dir, err, out)
	}
	if out, err := w.account.as(false, p); err != nil || !strings.Contains(out, "IFPS-CONTROL") {
		return fmt.Errorf("%s cannot read %s opened to everyone, so a denial would prove nothing: %v\n%s", w.account.name, p, err, out)
	}
	return nil
}

func (w *ifpsWorld) accountReads() error {
	ts, err := w.homeTargets()
	if err != nil {
		return err
	}
	w.denials, w.outputs = make([]error, len(ts)), make([]string, len(ts))
	for i, t := range ts {
		out, err := w.account.as(t.dir, t.path)
		err = ifpsOSDenied(out, err)
		// A refusal names the path asked for; only what else it printed can leak.
		w.denials[i], w.outputs[i] = err, strings.ReplaceAll(out, t.path, "{path}")
		fmt.Fprintf(os.Stderr, "DH-E-STORAGE-6: %s as %s -> %v: %s\n", t.path, w.account.name, err, strings.TrimSpace(out))
	}
	return nil
}

func (w *ifpsWorld) nothingLeaked() error {
	if len(w.outputs) == 0 {
		return fmt.Errorf("no read of the second account was recorded")
	}
	stored := []string{"notes.txt", "output.txt", "IFPS-ATTACHMENT", "IFPS-SPILL"}
	if p, ok := w.attach.HostPath(w.attachRef); ok {
		stored = append(stored, filepath.Base(p))
	}
	for i, out := range w.outputs {
		for _, s := range stored {
			if s != "" && strings.Contains(out, s) {
				return fmt.Errorf("denied read %d printed %q: %s", i, s, out)
			}
		}
	}
	return nil
}

// ifpsSecondAccountTag marks the scenario that needs a second OS account.
const ifpsSecondAccountTag = "@second-account"

// ifpsWSL runs DH-E-STORAGE-6 in this Windows machine's WSL as root, where the
// scenario makes and deletes its own second account, when the Windows run has
// no second account of its own: the test binary is cross-built from this
// checkout and runs on the same tree. No WSL, or no root in it, fails the run.
func ifpsWSL(t *testing.T) {
	wsl, err := exec.LookPath("wsl.exe")
	if err != nil {
		t.Fatalf("DH-E-STORAGE-6 needs a second OS account: %s; and there is no WSL to make one in: %v", ifpsNoAccount, err)
	}
	if out, err := ifpsExec(exec.Command(wsl, "-u", "root", "-e", "id", "-u")); err != nil || strings.TrimSpace(out) != "0" {
		t.Fatalf("DH-E-STORAGE-6 needs a second OS account: %s; and WSL cannot run as root to make one: %v %s", ifpsNoAccount, err, out)
	}
	pkg, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "ifps-linux.test")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "test", "-c", "-tags", "e2e", "-o", bin, ".")
	build.Dir = pkg
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	if out, err := ifpsExec(build); err != nil {
		t.Fatalf("cross-building the Linux test binary: %v\n%s", err, out)
	}
	lin := func(p string) string {
		out, err := ifpsExec(exec.Command(wsl, "-e", "wslpath", "-a", p))
		if err != nil {
			t.Fatalf("wslpath %s: %v %s", p, err, out)
		}
		return strings.TrimSpace(out)
	}
	run := exec.Command(wsl, "-u", "root", "--cd", lin(pkg), "-e", "env", "IFPS_GODOG_TAGS="+ifpsSecondAccountTag, "HOME=/root",
		lin(bin), "-test.run", "^TestE2E_dh_s_18_acceptance_on_real_operating_systems_instruction_files_and_private_storage_on_real_systems$", "-test.v")
	out, err := ifpsExec(run)
	t.Log(out)
	if err != nil {
		t.Fatalf("DH-E-STORAGE-6 failed in WSL: %v", err)
	}
	out = regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(out, "")
	if !strings.Contains(out, "1 scenarios (1 passed)") || !strings.Contains(out, "(made for this scenario)") {
		t.Fatalf("DH-E-STORAGE-6 did not pass with a real second account in WSL")
	}
}

// A runbook command that fails for any reason but the OS's refusal must not pass
// as a denial.
func TestIfpsFailureWithoutOSRefusalIsNotADenial(t *testing.T) {
	refused := ifpsShell("echo Permission denied && exit 1")
	if runtime.GOOS == "windows" {
		refused = ifpsShell("echo Access is denied.& exit /b 5")
	}
	out, err := ifpsExec(refused)
	if got := ifpsOSDenied(out, err); !errors.Is(got, errIfpsAccountDenied) {
		t.Errorf("a printed OS refusal was not a denial: %v", got)
	}
	for name, line := range map[string]string{"wrapper failed": "echo no such user& exit 3", "tool missing": "ifps-no-such-tool"} {
		out, err := ifpsExec(ifpsShell(line))
		if got := ifpsOSDenied(out, err); errors.Is(got, errIfpsAccountDenied) {
			t.Errorf("%s counted as an OS denial: %q", name, out)
		}
	}
	timedOut := fmt.Errorf("%w: signal: killed", context.DeadlineExceeded)
	if got := ifpsOSDenied("Permission denied", timedOut); errors.Is(got, errIfpsAccountDenied) {
		t.Errorf("a timeout counted as an OS denial")
	}
	if got := ifpsOSDenied("", nil); !errors.Is(got, errIfpsAllowed) {
		t.Errorf("a successful read was not reported as allowed: %v", got)
	}
}
