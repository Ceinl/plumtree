//go:build windows

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsConfigACLRestrictsAccessToCurrentUser(t *testing.T) {
	path := isolatePTConfig(t)
	if _, err := writePTConfig(ptConfig{ServerURL: "https://saved.example", DeployToken: "saved-token"}); err != nil {
		t.Fatal(err)
	}
	assertWindowsPTConfigOwnerOnly(t, path)
}

func TestWindowsDefaultConfigACLRestrictsAccessToCurrentUser(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PLUMTREE_PT_CONFIG", "")
	t.Setenv("APPDATA", dir)
	t.Setenv("PLUMTREE_SERVER_URL", "")
	t.Setenv("PLUMTREE_DEV_TOKEN", "")

	path, err := ptConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writePTConfig(ptConfig{ServerURL: "https://saved.example", DeployToken: "saved-token"}); err != nil {
		t.Fatal(err)
	}
	assertWindowsPTConfigOwnerOnly(t, path)
}

func TestWindowsExistingConfigIsRepairedBeforeTokenUse(t *testing.T) {
	path := isolatePTConfig(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"serverUrl":"https://saved.example","deployToken":"saved-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := makeWindowsPTConfigWorldReadable(path); err != nil {
		t.Fatal(err)
	}

	cfg, err := readPTConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeployToken != "saved-token" {
		t.Fatalf("deploy token = %q, want saved-token", cfg.DeployToken)
	}
	assertWindowsPTConfigOwnerOnly(t, path)
}

func TestWindowsVerifyRejectsPermissiveACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pt.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := makeWindowsPTConfigWorldReadable(path); err != nil {
		t.Fatal(err)
	}
	userSID, err := currentWindowsUserSID()
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyWindowsPTConfigSecurity(path, userSID); err == nil {
		t.Fatal("verifyWindowsPTConfigSecurity accepted a world-readable config")
	}
}

func assertWindowsPTConfigOwnerOnly(t *testing.T, path string) {
	t.Helper()
	userSID, err := currentWindowsUserSID()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		t.Fatal(err)
	}
	ownerAllowed, err := isWindowsPTConfigOwnerAllowed(owner, userSID)
	if err != nil {
		t.Fatal(err)
	}
	if !ownerAllowed {
		t.Fatalf("config owner = %v, want current user or an enabled group", owner)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("config ACL still inherits permissions")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil || dacl.AceCount != 1 {
		if dacl == nil {
			t.Fatal("config ACL is empty")
		}
		t.Fatalf("config ACL has %d entries, want one current-user entry", dacl.AceCount)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	aceType := ace.Header.AceType
	aceFlags := ace.Header.AceFlags
	aceMask := ace.Mask
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	aceSIDValid := aceSID.IsValid()
	aceSIDMatches := aceSID.Equals(userSID)
	runtime.KeepAlive(descriptor)
	if aceType != windows.ACCESS_ALLOWED_ACE_TYPE || aceFlags != 0 || aceMask != windowsPTConfigAccess || !aceSIDValid || !aceSIDMatches {
		t.Fatalf("config ACL grants unexpected access: %s", descriptor.String())
	}
}

func makeWindowsPTConfigWorldReadable(path string) error {
	userSID, err := currentWindowsUserSID()
	if err != nil {
		return err
	}
	worldSID, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windowsPTConfigAccess,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(userSID),
			},
		},
		{
			AccessPermissions: windows.FILE_GENERIC_READ,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(worldSID),
			},
		},
	}, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}
