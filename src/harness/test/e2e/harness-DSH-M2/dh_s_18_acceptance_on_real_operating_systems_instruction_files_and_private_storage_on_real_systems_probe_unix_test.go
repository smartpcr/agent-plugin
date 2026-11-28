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
