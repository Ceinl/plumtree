package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestApplyTailscaleDefaults(t *testing.T) {
	addr, origin, sshAddr := "127.0.0.1:8080", "http://localhost:8080", "127.0.0.1:2222"
	applyTailscaleDefaults("100.93.98.124", &addr, &origin, &sshAddr, networkOverrides{})
	if addr != "100.93.98.124:8080" || origin != "http://100.93.98.124:8080" || sshAddr != "100.93.98.124:2222" {
		t.Fatalf("tailscale defaults = %q %q %q", addr, origin, sshAddr)
	}
}

func TestApplyTailscaleDefaultsPreservesOverrides(t *testing.T) {
	addr, origin, sshAddr := "custom-http", "https://custom.example", "custom-ssh"
	applyTailscaleDefaults("100.93.98.124", &addr, &origin, &sshAddr, networkOverrides{
		addr: true, origin: true, ssh: true,
	})
	if addr != "custom-http" || origin != "https://custom.example" || sshAddr != "custom-ssh" {
		t.Fatalf("overrides changed to %q %q %q", addr, origin, sshAddr)
	}
}

func TestValidatePublicOriginForShoo(t *testing.T) {
	for _, origin := range []string{
		"http://localhost:8080",
		"http://app.localhost:8080",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
		"https://plumtree.example",
	} {
		got, err := validatePublicOrigin(origin, true)
		if err != nil {
			t.Errorf("validatePublicOrigin(%q) error = %v", origin, err)
		}
		if got != origin {
			t.Errorf("validatePublicOrigin(%q) = %q", origin, got)
		}
	}

	for _, origin := range []string{"http://100.93.98.124:8080", "http://plumtree.example"} {
		_, err := validatePublicOrigin(origin, true)
		if err == nil || !strings.Contains(err.Error(), "requires HTTPS") || !strings.Contains(err.Error(), "Tailscale Serve") {
			t.Errorf("validatePublicOrigin(%q) error = %v, want actionable HTTPS diagnostic", origin, err)
		}
	}
}

func TestValidatePublicOriginAllowsTrustedHTTPAutoClaim(t *testing.T) {
	got, err := validatePublicOrigin("http://100.93.98.124:8080/", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://100.93.98.124:8080" {
		t.Fatalf("origin = %q", got)
	}
}

func TestValidatePublicOriginRejectsNonOriginURLs(t *testing.T) {
	for _, origin := range []string{
		"plumtree.example",
		"ftp://plumtree.example",
		"https://user@plumtree.example",
		"https://plumtree.example/dashboard",
		"https://plumtree.example?x=1",
	} {
		if _, err := validatePublicOrigin(origin, false); err == nil {
			t.Errorf("validatePublicOrigin(%q) succeeded", origin)
		}
	}
}

func TestParseTailscaleIPv4(t *testing.T) {
	got, err := parseTailscaleIPv4("100.93.98.124\n")
	if err != nil || got != "100.93.98.124" {
		t.Fatalf("parseTailscaleIPv4 = %q, %v", got, err)
	}
	for _, invalid := range []string{"", "192.168.1.2", "fd7a:115c:a1e0::1"} {
		if _, err := parseTailscaleIPv4(invalid); err == nil {
			t.Fatalf("parseTailscaleIPv4(%q) succeeded", invalid)
		}
	}
}

func TestLoadOrCreateDevTokenPersistsPrivateToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dev-token")
	first, err := loadOrCreateDevTokenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreateDevTokenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || second != first {
		t.Fatalf("tokens = %q and %q", first, second)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("token permissions = %04o, want 0600", info.Mode().Perm())
	}
}

func TestLoadOrCreateDevTokenRejectsPermissiveFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows file modes do not model group/world permissions")
	}
	path := filepath.Join(t.TempDir(), "dev-token")
	if err := os.WriteFile(path, []byte("unsafe-token\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadOrCreateDevTokenAt(path)
	if err == nil || !strings.Contains(err.Error(), "insecure permissions") {
		t.Fatalf("loadOrCreateDevTokenAt error = %v", err)
	}
}
