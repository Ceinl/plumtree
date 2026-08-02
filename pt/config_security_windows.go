//go:build windows

package main

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const windowsPTConfigAccess = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.DELETE

func validatePTConfigSecurity(path string, _ os.FileInfo) error {
	return securePTConfigFile(path)
}

func securePTConfigFile(path string) error {
	userSID, err := currentWindowsUserSID()
	if err != nil {
		return fmt.Errorf("resolve current Windows user: %w", err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("inspect Windows ACL: %w", err)
	}
	if descriptor == nil {
		return fmt.Errorf("inspect Windows ACL: empty security descriptor")
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("inspect Windows ACL owner: %w", err)
	}
	ownerAllowed, err := isWindowsPTConfigOwnerAllowed(owner, userSID)
	if err != nil {
		return fmt.Errorf("check Windows config owner %q: %w", path, err)
	}
	if !ownerAllowed {
		return fmt.Errorf("Windows config %q is not owned by the current Windows user or an enabled group", path)
	}

	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windowsPTConfigAccess,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(userSID),
		},
	}}, nil)
	if err != nil {
		return fmt.Errorf("build Windows ACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		return fmt.Errorf("apply Windows ACL: %w", err)
	}
	if err := verifyWindowsPTConfigSecurity(path, userSID); err != nil {
		return err
	}
	return nil
}

func currentWindowsUserSID() (*windows.SID, error) {
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	if tokenUser.User.Sid == nil || !tokenUser.User.Sid.IsValid() {
		return nil, fmt.Errorf("current Windows user has no valid SID")
	}
	return tokenUser.User.Sid, nil
}

func verifyWindowsPTConfigSecurity(path string, userSID *windows.SID) error {
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("verify Windows ACL: %w", err)
	}
	if descriptor == nil {
		return fmt.Errorf("verify Windows ACL: empty security descriptor")
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("verify Windows ACL owner %q: %w", path, err)
	}
	ownerAllowed, err := isWindowsPTConfigOwnerAllowed(owner, userSID)
	if err != nil {
		return fmt.Errorf("verify Windows ACL owner %q: %w", path, err)
	}
	if !ownerAllowed {
		return fmt.Errorf("verify Windows ACL %q: file is not owned by the current Windows user or an enabled group", path)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return fmt.Errorf("verify Windows ACL control: %w", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("verify Windows ACL: inherited permissions are not disabled")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("verify Windows ACL DACL: %w", err)
	}
	if dacl == nil || dacl.AceCount != 1 {
		return fmt.Errorf("verify Windows ACL: expected exactly one access entry")
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		return fmt.Errorf("verify Windows ACL entry: %w", err)
	}
	aceType := ace.Header.AceType
	aceFlags := ace.Header.AceFlags
	aceMask := ace.Mask
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	aceSIDValid := aceSID.IsValid()
	aceSIDMatches := aceSID.Equals(userSID)
	runtime.KeepAlive(descriptor)
	if aceType != windows.ACCESS_ALLOWED_ACE_TYPE || aceFlags != 0 || aceMask != windowsPTConfigAccess {
		return fmt.Errorf("verify Windows ACL: access entry is not owner-only")
	}
	if !aceSIDValid || !aceSIDMatches {
		return fmt.Errorf("verify Windows ACL: access entry is not for the current Windows user")
	}
	return nil
}

func isWindowsPTConfigOwnerAllowed(owner, userSID *windows.SID) (bool, error) {
	if owner == nil {
		return false, nil
	}
	if owner.Equals(userSID) {
		return true, nil
	}
	return windows.GetCurrentProcessToken().IsMember(owner)
}
