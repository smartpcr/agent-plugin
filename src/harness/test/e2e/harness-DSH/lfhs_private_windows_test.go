//go:build e2e && windows

package e2e

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// lfhsLoosen adds a DACL entry granting Everyone (S-1-1-0) read through
// SetNamedSecurityInfo, as TC-06b does on Windows, keeping the list protected.
func lfhsLoosen(path string) error {
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

// lfhsAssertPrivate independently inspects path: its DACL must be protected and
// its only ACEs must be for the owner SID, SYSTEM and Administrators.
func lfhsAssertPrivate(path string) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return fmt.Errorf("%s has no owner", path)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	allowed := []*windows.SID{owner, tu.User.Sid, system, admins}

	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("%s: DACL is not protected", path)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("%s: no DACL", path)
	}
	if dacl.AceCount == 0 {
		return fmt.Errorf("%s: DACL is empty", path)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return err
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		ok := false
		for _, a := range allowed {
			if sid.Equals(a) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("%s: DACL has an entry for %s", path, sid.String())
		}
	}
	return nil
}
