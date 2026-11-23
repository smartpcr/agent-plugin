//go:build windows

package home

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	//lint:ignore typeassert SECURITY_ATTRIBUTES.Length needs unsafe.Sizeof, and reading an ACE's SID (GetAce) needs unsafe.Pointer at SidStart
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

// privateACL is a DACL granting full control to the current user, SYSTEM and
// Administrators only, with the given inheritance.
func privateACL(inheritance uint32) (*windows.ACL, error) {
	sids, err := trusted()
	if err != nil {
		return nil, err
	}
	entries := make([]windows.EXPLICIT_ACCESS, len(sids))
	for i, sid := range sids {
		entries[i] = windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.SET_ACCESS,
			Inheritance:       inheritance,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		}
	}
	return windows.ACLFromEntries(entries, nil)
}

// makePrivate replaces the directory's DACL with a protected one granting full
// control to the current user, SYSTEM and Administrators only, inherited by what
// is created inside.
func makePrivate(path string) error {
	acl, err := privateACL(windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

// createPrivate creates path with CREATE_NEW, which fails on any existing name,
// a symbolic link or junction included, and gives the file a protected DACL for
// the current user, SYSTEM and Administrators in the same call, so no other
// access list is ever in force. Delete is shared, so a staged file can be linked
// or renamed while open.
func createPrivate(path string) (*os.File, error) {
	acl, err := privateACL(windows.NO_INHERITANCE)
	if err != nil {
		return nil, &os.PathError{Op: "create", Path: path, Err: err}
	}
	sd, err := windows.NewSecurityDescriptor()
	if err != nil {
		return nil, &os.PathError{Op: "create", Path: path, Err: err}
	}
	if err := sd.SetDACL(acl, true, false); err != nil {
		return nil, &os.PathError{Op: "create", Path: path, Err: err}
	}
	if err := sd.SetControl(windows.SE_DACL_PROTECTED, windows.SE_DACL_PROTECTED); err != nil {
		return nil, &os.PathError{Op: "create", Path: path, Err: err}
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "create", Path: path, Err: err}
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE, sa, windows.CREATE_NEW,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			err = fs.ErrExist
		}
		return nil, &os.PathError{Op: "create", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// checkPrivate requires, for a directory, a protected DACL, and for any object a
// DACL whose every entry names the current user, SYSTEM or Administrators, and an
// owner among them.
func checkPrivate(path string, info fs.FileInfo) string {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "its access list cannot be read: " + err.Error()
	}
	return checkDescriptor(sd, info)
}

// checkPrivateFile checks the security descriptor of the open handle.
func checkPrivateFile(f *os.File, info fs.FileInfo) string {
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "its access list cannot be read: " + err.Error()
	}
	return checkDescriptor(sd, info)
}

func checkDescriptor(sd *windows.SECURITY_DESCRIPTOR, info fs.FileInfo) string {
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
