//go:build e2e && windows

package e2e

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	//lint:ignore typeassert CreateRestrictedToken takes its SID_AND_ATTRIBUTES array and out-token as uintptr made with unsafe.Pointer
	"unsafe"

	"golang.org/x/sys/windows"
)

var ifpsCreateRestrictedToken = windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")

const ifpsDisableMaxPrivilege = 0x1

// ifpsStrangerToken is an impersonation token for a second local account: this
// process's token with its user SID, SYSTEM and Administrators made deny-only
// and every privilege but bypass-traverse removed. It keeps only what any other
// interactive account holds too (Everyone, Users, Authenticated Users, the
// logon session), so what the OS refuses it, it refuses a second account.
func ifpsStrangerToken() (windows.Token, error) {
	var proc windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_IMPERSONATE, &proc); err != nil {
		return 0, err
	}
	defer proc.Close()
	tu, err := proc.GetTokenUser()
	if err != nil {
		return 0, err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return 0, err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return 0, err
	}
	disable := []windows.SIDAndAttributes{{Sid: tu.User.Sid}, {Sid: system}, {Sid: admins}}
	var restricted windows.Token
	r, _, callErr := ifpsCreateRestrictedToken.Call(uintptr(proc), ifpsDisableMaxPrivilege,
		uintptr(len(disable)), uintptr(unsafe.Pointer(&disable[0])), 0, 0, 0, 0, uintptr(unsafe.Pointer(&restricted)))
	runtime.KeepAlive(disable)
	if r == 0 {
		return 0, fmt.Errorf("CreateRestrictedToken: %w", callErr)
	}
	defer restricted.Close()
	var imp windows.Token
	if err := windows.DuplicateTokenEx(restricted, windows.TOKEN_IMPERSONATE|windows.TOKEN_QUERY, nil,
		windows.SecurityImpersonation, windows.TokenImpersonation, &imp); err != nil {
		return 0, err
	}
	return imp, nil
}

func ifpsReadOne(t ifpsTarget) error {
	f, err := os.Open(t.path)
	if err != nil {
		return err
	}
	defer f.Close()
	if t.dir {
		_, err = f.Readdirnames(-1)
		return err
	}
	_, err = io.ReadAll(f)
	return err
}

// ifpsStrangerRead reads each target on one OS thread impersonating the second
// account, and returns each read's error (nil when the OS allowed it).
func ifpsStrangerRead(ts []ifpsTarget) ([]error, error) {
	tok, err := ifpsStrangerToken()
	if err != nil {
		return nil, err
	}
	defer tok.Close()
	type result struct {
		errs []error
		err  error
	}
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		// The thread stays locked and exits with the goroutine if it cannot revert.
		if err := windows.SetThreadToken(nil, tok); err != nil {
			runtime.UnlockOSThread()
			done <- result{err: fmt.Errorf("SetThreadToken: %w", err)}
			return
		}
		errs := make([]error, len(ts))
		for i, t := range ts {
			errs[i] = ifpsReadOne(t)
		}
		if err := windows.RevertToSelf(); err != nil {
			done <- result{err: fmt.Errorf("RevertToSelf: %w", err)}
			return
		}
		runtime.UnlockOSThread()
		done <- result{errs: errs}
	}()
	r := <-done
	return r.errs, r.err
}

// ifpsIsDenied is an access check the OS failed: ERROR_ACCESS_DENIED, not a
// sharing violation or a missing file.
func ifpsIsDenied(err error) bool {
	return err != nil && errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

// ifpsGrantEveryone adds an Everyone (S-1-1-0) read entry to path's protected DACL.
func ifpsGrantEveryone(path string) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	old, _, err := sd.DACL()
	if err != nil {
		return err
	}
	everyone, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_READ,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(everyone),
		},
	}}, old)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func ifpsOpenToOthers(path string) error { return ifpsGrantEveryone(path) }

func ifpsLoosenDir(path string) error { return ifpsGrantEveryone(path) }
