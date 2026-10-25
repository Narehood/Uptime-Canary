package origin

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"unsafe"
)

func protectConnectionFile(f *os.File) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return errors.New("Cannot identify the node connection account")
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	// No secret has been written yet. Set a protected DACL and verify the opened
	// file's descriptor before publishing any private bytes into it.
	if err = windows.SetNamedSecurityInfo(f.Name(), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return errors.New("Cannot restrict node connection identity permissions")
	}
	return checkPrivateConnectionFile(f)
}

func checkPrivateConnectionFile(f *os.File) error {
	refused := errors.New("Node connection identity permissions must be private to this account, SYSTEM and Administrators")
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return refused
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return refused
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return refused
	}
	allowed := func(sid *windows.SID) bool {
		return sid != nil && sid.IsValid() && (sid.Equals(user.User.Sid) || sid.Equals(system) || sid.Equals(administrators))
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || sd == nil || !sd.IsValid() {
		return refused
	}
	owner, _, err := sd.Owner()
	if err != nil || !allowed(owner) {
		return refused
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return refused
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(dacl, i, &ace) != nil || ace == nil {
			return refused
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return refused
		}
		const offset = unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.SidStart)
		if uintptr(ace.Header.AceSize) < offset+8 {
			return refused
		}
		raw := unsafe.Slice((*byte)(unsafe.Pointer(&ace.SidStart)), int(ace.Header.AceSize)-int(offset))
		if int(raw[1]) > 15 || 8+4*int(raw[1]) > len(raw) {
			return refused
		}
		if ace.Mask != 0 && !allowed((*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
			return refused
		}
	}
	return nil
}
