//go:build e2e && windows

package e2e

import (
	"fmt"
	//lint:ignore typeassert reading an ACE's SID (GetAce) needs unsafe.Pointer at SidStart
	"unsafe"

	"golang.org/x/sys/windows"
)

// ssaAssertOwnerOnly reads path's security descriptor with GetNamedSecurityInfo
// and requires a protected DACL whose ACEs are only for the descriptor's owner
// SID, SYSTEM and Administrators, with at least one ACE for the owner.
func ssaAssertOwnerOnly(path string) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("%s: GetNamedSecurityInfo: %w", path, err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return fmt.Errorf("%s: no owner SID", path)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	allowed := []*windows.SID{owner, system, admins}

	control, _, err := sd.Control()
	if err != nil {
		return err
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("%s: DACL is not protected (control %#x)", path, control)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("%s: no DACL", path)
	}
	ownerHasAce := false
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return err
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		known := false
		for _, a := range allowed {
			known = known || sid.Equals(a)
		}
		if !known {
			return fmt.Errorf("%s: ACE %d is for %s, not the owner %s, SYSTEM or Administrators", path, i, sid, owner)
		}
		ownerHasAce = ownerHasAce || sid.Equals(owner)
	}
	if !ownerHasAce {
		return fmt.Errorf("%s: no ACE for the owner %s", path, owner)
	}
	return nil
}
