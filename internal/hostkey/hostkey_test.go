package hostkey

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestLoadRejectsMissingAndGarbageKeys(t *testing.T) {
	dir := t.TempDir()

	if _, err := Load(filepath.Join(dir, "absent")); err == nil {
		t.Fatal("loading a missing host key unexpectedly succeeded")
	}

	path := filepath.Join(dir, "garbage")
	if err := os.WriteFile(path, []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("loading a garbage host key unexpectedly succeeded")
	}
	if !bytes.Contains([]byte(err.Error()), []byte(path)) || !bytes.Contains([]byte(err.Error()), []byte("remove the file")) {
		t.Fatalf("error must name the path and the remedy, got: %v", err)
	}
}

func TestLoadOrCreatePersistsIdentityAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host_ed25519")

	first, err := LoadOrCreate(path, "test host key")
	if err != nil {
		t.Fatalf("create host key: %v", err)
	}
	second, err := LoadOrCreate(path, "test host key")
	if err != nil {
		t.Fatalf("reload host key: %v", err)
	}
	if !bytes.Equal(first.PublicKey().Marshal(), second.PublicKey().Marshal()) {
		t.Fatal("reloaded host key changed identity")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("host key mode = %o, want no group or other permissions", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory entries = %v, want only the host key", entries)
	}
}

func TestLoadOrCreateRefusesCorruptFileAndLeavesItUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host_ed25519")
	corrupt := []byte("-----BEGIN OPENSSH PRIVATE KEY-----\ngarbage\n-----END OPENSSH PRIVATE KEY-----\n")
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadOrCreate(path, "test host key"); err == nil {
		t.Fatal("loading a corrupt host key unexpectedly succeeded")
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, corrupt) {
		t.Fatalf("corrupt file was modified: %q, %v", got, err)
	}
}

func TestGenerateProducesParsablePEM(t *testing.T) {
	signer, pemBytes, err := Generate("generated for test")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		t.Fatalf("generated PEM does not parse: %v", err)
	}
	if !bytes.Equal(signer.PublicKey().Marshal(), parsed.PublicKey().Marshal()) {
		t.Fatal("parsed public key differs from the generated signer")
	}
}
