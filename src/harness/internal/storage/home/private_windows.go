//go:build windows

package home

import (
	"fmt"
	"io/fs"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows ACE types that grant or deny access; any other type in a home DACL fails
// the check.
const (
	aceAllowed = 0 // ACCESS_ALLOWED_ACE_TYPE
	aceDenied  = 1 // ACCESS_DENIED_ACE_TYPE
)

// trusted are the SIDs a private object's DACL may name: the current user, SYSTEM
// and Administrators.
func trusted() ([]*windows.SID, error) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, err
	}
	return []*windows.SID{tu.User.Sid, system, admins}, nil
}

// makePrivate replaces the directory's DACL with a protected one granting full
// control to the current user, SYSTEM and Administrators only, inherited by what
// is created inside.
func makePrivate(path string) error {
	sids, err := trusted()
	if err != nil {
		return err
	}
	entries := make([]windows.EXPLICIT_ACCESS, len(sids))
	for i, sid := range sids {
		entries[i] = windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.SET_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		}
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

// checkPrivate requires, for a directory, a protected DACL, and for any object a
// DACL whose every entry names the current user, SYSTEM or Administrators, and an
// owner among them.
func checkPrivate(path string, info fs.FileInfo) string {
	sids, err := trusted()
	if err != nil {
		return "the current user is unknown: " + err.Error()
	}
	isTrusted := func(sid *windows.SID) bool {
		for _, t := range sids {
			if sid.Equals(t) {
				return true
			}
		}
		return false
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "its access list cannot be read: " + err.Error()
	}
	if owner, _, err := sd.Owner(); err != nil || owner == nil {
		return "it has no owner"
	} else if !isTrusted(owner) {
		return "owned by " + owner.String() + ", not the current user"
	}
	if info.IsDir() {
		control, _, err := sd.Control()
		if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
			return "its access list inherits from the parent directory"
		}
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return "it has no access list, so everyone has access"
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return "its access list cannot be read: " + err.Error()
		}
		if t := ace.Header.AceType; t != aceAllowed && t != aceDenied {
			return fmt.Sprintf("its access list holds an entry of type %d", t)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !isTrusted(sid) {
			return "its access list has an entry for " + sid.String()
		}
	}
	return ""
}
