//go:build e2e && !windows

package e2e

import (
	"bytes"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ifpsNobody is the second account a root run reads as: nobody.
const ifpsNobody = 65534

// errIfpsKernelDenies is a read the kernel's permission check refuses every uid
// but the owner's (and root's): judged from owner and mode, the check itself,
// when the run cannot become another uid.
var errIfpsKernelDenies = fmt.Errorf("the kernel denies a non-owner uid: %w", iofs.ErrPermission)

// errIfpsDenied is a read a process of another uid ran and the kernel refused.
var errIfpsDenied = fmt.Errorf("a process of uid %d was refused: %w", ifpsNobody, iofs.ErrPermission)

// ifpsStrangerRead reads each target as a second account: as uid 65534 in a
// child process when this run is root, else by the owner and mode bits the
// kernel checks for any uid that is not the owner.
func ifpsStrangerRead(ts []ifpsTarget) ([]error, error) {
	errs := make([]error, len(ts))
	if os.Geteuid() == 0 {
		fmt.Fprintf(os.Stderr, "DH-E-STORAGE-6: reading as uid %d\n", ifpsNobody)
		for i, t := range ts {
			errs[i] = ifpsReadAsNobody(t)
		}
		return errs, nil
	}
	fmt.Fprintf(os.Stderr, "DH-E-STORAGE-6: not root; judging by owner and mode for any uid but %d\n", os.Getuid())
	for i, t := range ts {
		denied, err := ifpsJudge(t)
		if err != nil {
			return nil, err
		}
		if denied {
			errs[i] = errIfpsKernelDenies
		}
	}
	return errs, nil
}

func ifpsReadAsNobody(t ifpsTarget) error {
	name, args := "cat", []string{"--", t.path}
	if t.dir {
		name, args = "ls", []string{"-a", "--", t.path}
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: ifpsNobody, Gid: ifpsNobody}}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if strings.Contains(out.String(), "Permission denied") {
			return errIfpsDenied
		}
		return fmt.Errorf("%s as uid %d: %v: %s", name, ifpsNobody, err, out.String())
	}
	return nil
}

func ifpsOwnedBy(info iofs.FileInfo, uid int) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == uid
}

// ifpsJudge applies the kernel's check for a uid that is neither the owner nor
// root, nor in the owner's group: an ancestor directory we own without group or
// other search refuses the lookup; the target we own without group or other read
// refuses the read.
func ifpsJudge(t ifpsTarget) (bool, error) {
	uid := os.Getuid()
	info, err := os.Lstat(t.path)
	if err != nil {
		return false, err
	}
	if ifpsOwnedBy(info, uid) && info.Mode().Perm()&0o044 == 0 {
		return true, nil
	}
	for dir := filepath.Dir(t.path); ; dir = filepath.Dir(dir) {
		di, err := os.Stat(dir)
		if err != nil {
			return false, err
		}
		if ifpsOwnedBy(di, uid) && di.Mode().Perm()&0o011 == 0 {
			return true, nil
		}
		if filepath.Dir(dir) == dir {
			return false, nil
		}
	}
}

func ifpsIsDenied(err error) bool {
	return errors.Is(err, errIfpsDenied) || errors.Is(err, errIfpsKernelDenies)
}

func ifpsOpenToOthers(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.Chmod(path, 0o755)
	}
	return os.Chmod(path, 0o644)
}

// ifpsLoosenDir makes dir group-writable, as DH-E-STORAGE-6's POSIX step 2 does.
func ifpsLoosenDir(dir string) error { return os.Chmod(dir, 0o770) }

// ifpsShell runs line through sh -c.
func ifpsShell(line string) *exec.Cmd { return exec.Command("sh", "-c", line) }

func ifpsTool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, d := range []string{"/usr/sbin", "/sbin"} {
		if _, err := os.Stat(filepath.Join(d, name)); err == nil {
			return filepath.Join(d, name)
		}
	}
	return ""
}

func ifpsIDs(args ...string) ([]int, error) {
	out, err := ifpsExec(exec.Command("id", args...))
	if err != nil {
		return nil, fmt.Errorf("id %s: %v %s", strings.Join(args, " "), err, out)
	}
	var ids []int
	for _, f := range strings.Fields(out) {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil, fmt.Errorf("id %s printed %q", strings.Join(args, " "), out)
		}
		ids = append(ids, n)
	}
	return ids, nil
}

// ifpsEphemeralAccount makes an ordinary account with a group of its own when
// the run is root or has passwordless sudo, and deletes it when the scenario
// ends; else there is none.
func ifpsEphemeralAccount() (*ifpsAccount, error) {
	var pre []string
	if os.Geteuid() != 0 {
		if _, err := exec.LookPath("sudo"); err != nil {
			return nil, nil
		}
		if _, err := ifpsExec(exec.Command("sudo", "-n", "true")); err != nil {
			return nil, nil
		}
		pre = []string{"sudo", "-n"}
	}
	useradd, userdel := ifpsTool("useradd"), ifpsTool("userdel")
	if useradd == "" || userdel == "" {
		return nil, nil
	}
	priv := func(args ...string) *exec.Cmd {
		a := append(append([]string{}, pre...), args...)
		c := exec.Command(a[0], a[1:]...)
		c.Dir = "/"
		return c
	}
	shell := "/usr/sbin/nologin"
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/false"
	}
	name := ifpsRandName()
	if out, err := ifpsExec(priv(useradd, "--no-create-home", "--user-group", "--shell", shell, name)); err != nil {
		return nil, fmt.Errorf("useradd %s: %v %s", name, err, out)
	}
	a := &ifpsAccount{name: name, how: "made for this scenario", close: func() {
		if out, err := ifpsExec(priv(userdel, name)); err != nil {
			fmt.Fprintf(os.Stderr, "DH-E-STORAGE-6: userdel %s: %v %s\n", name, err, out)
		}
	}}
	fail := func(err error) (*ifpsAccount, error) { a.close(); return nil, err }
	uid, err := ifpsIDs("-u", name)
	if err != nil {
		return fail(err)
	}
	gids, err := ifpsIDs("-G", name)
	if err != nil {
		return fail(err)
	}
	if len(uid) != 1 || uid[0] == os.Getuid() || uid[0] == 0 {
		return fail(fmt.Errorf("the account %s has uid %v, the runner's is %d", name, uid, os.Getuid()))
	}
	mine, err := os.Getgroups()
	if err != nil {
		return fail(err)
	}
	for _, g := range gids {
		for _, m := range append(mine, os.Getgid()) {
			if g == m {
				return fail(fmt.Errorf("the account %s shares group %d with the runner", name, g))
			}
		}
	}
	a.as = func(dir bool, path string) (string, error) {
		args := []string{"cat", "--", path}
		if dir {
			args = []string{"ls", "-a", "--", path}
		}
		if pre != nil {
			return ifpsExec(priv(append([]string{"-u", name, "--"}, args...)...))
		}
		c := exec.Command(args[0], args[1:]...)
		c.Dir = "/"
		c.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid[0]), Gid: uint32(gids[0]), Groups: []uint32{}}}
		return ifpsExec(c)
	}
	a.denied = func(out string) bool { return strings.Contains(out, "Permission denied") }
	return a, nil
}
