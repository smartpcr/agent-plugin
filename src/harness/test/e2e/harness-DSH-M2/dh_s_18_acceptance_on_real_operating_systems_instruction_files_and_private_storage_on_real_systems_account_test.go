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

	"github.com/cucumber/godog"
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
	as func(dir bool, path string) (string, error)
	// denied, when set, tells a refusal by the OS from a command that failed.
	denied func(out string) bool
	close  func()
}

var (
	errIfpsAccountDenied = errors.New("the second account's access was refused")
	errIfpsAllowed       = errors.New("the second account's access succeeded")
)

// ifpsSkipLab names the inputs a machine without a second account lacks: the
// two-account machine is lab-only (runbook-two-account.md, M2-EN-5).
const ifpsSkipLab = "not run (second-account preflight failed): set DSH_SCENARIO_STORAGE6_USER, _PUBLIC, _LIST and _READ " +
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
// scenario where the run may make accounts, else skips the lab-only scenario.
func ifpsSecondAccount() (*ifpsAccount, error) {
	if a, err := ifpsRunbookAccount(); a != nil || err != nil {
		return a, err
	}
	if a, err := ifpsEphemeralAccount(); a != nil || err != nil {
		return a, err
	}
	return nil, fmt.Errorf("%w: %s", godog.ErrSkip, ifpsSkipLab)
}

func ifpsRandName() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "dshs18e2e" + hex.EncodeToString(b)
}

func (w *ifpsWorld) secondAccount() error {
	a, err := ifpsSecondAccount()
	if err != nil {
		if runtime.GOOS == "windows" {
			err = fmt.Errorf("%w; the Linux variant runs in WSL as the subtest linux-wsl", err)
		}
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
		switch {
		case err == nil:
			err = fmt.Errorf("%w: %s read %s", errIfpsAllowed, w.account.name, t.path)
		case w.account.denied != nil && !w.account.denied(out):
			err = fmt.Errorf("%s failed on %s without an OS denial: %v", w.account.name, t.path, err)
		default:
			err = fmt.Errorf("%w: %v", errIfpsAccountDenied, err)
		}
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

// ifpsWSL runs the Linux variant of DH-E-STORAGE-6 in this Windows machine's WSL
// as root, where the scenario makes and deletes its own second account: the
// test binary is cross-built from this checkout and runs on the same tree.
func ifpsWSL(t *testing.T) {
	if os.Getenv("DSH_SCENARIO_STORAGE6_USER") != "" {
		t.Skip("the runbook's Windows account runs DH-E-STORAGE-6 on this lane")
	}
	wsl, err := exec.LookPath("wsl.exe")
	if err != nil {
		t.Skip("no WSL on this machine: " + err.Error())
	}
	if out, err := ifpsExec(exec.Command(wsl, "-u", "root", "-e", "id", "-u")); err != nil || strings.TrimSpace(out) != "0" {
		t.Skipf("WSL cannot run as root here: %v %s", err, out)
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
	run := exec.Command(wsl, "-u", "root", "--cd", lin(pkg), "-e", "env", "IFPS_GODOG_TAGS=@lab-two-account", "HOME=/root",
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
