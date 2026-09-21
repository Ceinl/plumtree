package gateway

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDevHostKeyPersistsIdentity(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("HOME", configHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("AppData", configHome)

	first, err := devHostKey()
	if err != nil {
		t.Fatalf("create host key: %v", err)
	}
	second, err := devHostKey()
	if err != nil {
		t.Fatalf("reload host key: %v", err)
	}
	if !bytes.Equal(first.PublicKey().Marshal(), second.PublicKey().Marshal()) {
		t.Fatal("reloaded host key changed identity")
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("user config directory: %v", err)
	}
	keyPath := filepath.Join(configDir, "plumtree", "dev_host_ed25519")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat persisted host key: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("host key mode = %o, want no group or other permissions", info.Mode().Perm())
	}
}

func TestDevHostKeyRefusesCorruptFileAndLeavesItUntouched(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("HOME", configHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("AppData", configHome)

	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("user config directory: %v", err)
	}
	path := filepath.Join(configDir, "plumtree", "dev_host_ed25519")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte("previously a fine host key, now garbage\n")
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = devHostKey()
	if err == nil {
		t.Fatal("dev host key loading unexpectedly regenerated a corrupt file")
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(got, corrupt) {
		t.Fatalf("corrupt file was modified: %q, %v", got, readErr)
	}
}
