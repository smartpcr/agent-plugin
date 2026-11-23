//go:build windows

package local_test

import (
	"os"
	"path/filepath"
	"testing"
	//lint:ignore typeassert reading an ACE's SID (GetAce) needs unsafe.Pointer at SidStart
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
)

// Scenario: Spill files are private (DH-D-STORAGE-5, TC-08), Windows: the saved
// file and its directories carry a protected DACL whose only ACEs are for the
// owner, SYSTEM and Administrators, read back with GetNamedSecurityInfo.
func TestSpillFilesArePrivate(t *testing.T) {
	l := newHome(t)
	sp := newStore(t, l)
	_, ref, err := spill.Save(bg, sp, "s1", "out.txt", []byte("0123456789"), 2)
	if err != nil {
		t.Fatal(err)
	}
	file, ok := sp.HostPath(ref.Path)
	if !ok {
		t.Fatal("no host path")
	}

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	system, _ := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	admins, _ := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	allowed := []*windows.SID{user.User.Sid, system, admins}

	for _, path := range []string{file, filepath.Dir(file), l.Spill()} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
			windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		control, _, err := sd.Control()
		if err != nil {
			t.Fatal(err)
		}
		if control&windows.SE_DACL_PROTECTED == 0 {
			t.Fatalf("%s: the DACL is not protected (control %#x)", path, control)
		}
		dacl, _, err := sd.DACL()
		if err != nil || dacl == nil {
			t.Fatalf("%s: no DACL: %v", path, err)
		}
		if dacl.AceCount == 0 {
			t.Fatalf("%s: empty DACL", path)
		}
		ownerHasAce := false
		for i := uint32(0); i < uint32(dacl.AceCount); i++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(dacl, i, &ace); err != nil {
				t.Fatal(err)
			}
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			known := false
			for _, a := range allowed {
				known = known || windows.EqualSid(sid, a)
			}
			if !known {
				t.Fatalf("%s: ACE %d is for %s, not the owner, SYSTEM or Administrators", path, i, sid)
			}
			ownerHasAce = ownerHasAce || windows.EqualSid(sid, user.User.Sid)
		}
		if !ownerHasAce {
			t.Fatalf("%s: no ACE for the owner", path)
		}
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
}
