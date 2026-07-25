package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func isolatePTConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "pt.json")
	t.Setenv("PLUMTREE_PT_CONFIG", path)
	t.Setenv("PLUMTREE_DEV_TOKEN_FILE", filepath.Join(dir, "dev-token"))
	t.Setenv("PLUMTREE_SERVER_URL", "")
	t.Setenv("PLUMTREE_DEV_TOKEN", "")
	return path
}

func TestResolveConnectionPrecedence(t *testing.T) {
	isolatePTConfig(t)
	if _, err := writePTConfig(ptConfig{ServerURL: "https://saved.example", DeployToken: "saved-token"}); err != nil {
		t.Fatal(err)
	}
	server, token, err := resolveConnection()
	if err != nil {
		t.Fatal(err)
	}
	if server != "https://saved.example" || token != "saved-token" {
		t.Fatalf("saved connection = %q %q", server, token)
	}
	t.Setenv("PLUMTREE_SERVER_URL", "https://env.example/")
	t.Setenv("PLUMTREE_DEV_TOKEN", "env-token")
	server, token, err = resolveConnection()
	if err != nil {
		t.Fatal(err)
	}
	if server != "https://env.example" || token != "env-token" {
		t.Fatalf("environment connection = %q %q", server, token)
	}
}

func TestResolveConnectionRejectsInvalidEnvironmentURL(t *testing.T) {
	isolatePTConfig(t)
	t.Setenv("PLUMTREE_SERVER_URL", "ssh://plumtree.example")
	if _, _, err := resolveConnection(); err == nil || !strings.Contains(err.Error(), "scheme must be http or https") {
		t.Fatalf("resolveConnection error = %v", err)
	}
}

func TestResolveConnectionUsesManagedTokenForLocalDefault(t *testing.T) {
	isolatePTConfig(t)
	t.Setenv("PLUMTREE_DEV_TOKEN", "seed")
	os.Unsetenv("PLUMTREE_DEV_TOKEN")
	path, err := localDevTokenPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("managed-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server, token, err := resolveConnection()
	if err != nil {
		t.Fatal(err)
	}
	if server != localServerURL || token != "managed-token" {
		t.Fatalf("connection = %q %q, want %q managed-token", server, token, localServerURL)
	}
}

func TestResolveConnectionDoesNotUseManagedTokenForRemoteServer(t *testing.T) {
	isolatePTConfig(t)
	t.Setenv("PLUMTREE_DEV_TOKEN", "seed")
	os.Unsetenv("PLUMTREE_DEV_TOKEN")
	path, err := localDevTokenPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("local-only-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writePTConfig(ptConfig{ServerURL: "https://remote.example"}); err != nil {
		t.Fatal(err)
	}
	server, token, err := resolveConnection()
	if err != nil {
		t.Fatal(err)
	}
	if server != "https://remote.example" || token != "" {
		t.Fatalf("remote connection = %q %q", server, token)
	}
}

func TestAddServerRejectsPermissiveExistingConfigBeforeWritingToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows file modes do not model group/world permissions")
	}
	path := isolatePTConfig(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	err := cmdAddServer([]string{"https://new.example", "new"}, strings.NewReader("new-secret\n"), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "insecure permissions") {
		t.Fatalf("cmdAddServer error = %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "new-secret") {
		t.Fatalf("permissive config exposed the new deploy token: %q", b)
	}
}

func TestWritePTConfigAtomicallyReplacesPermissiveFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows file modes do not model group/world permissions")
	}
	path := isolatePTConfig(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writePTConfig(ptConfig{DeployToken: "new-secret"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config permissions = %o, want 600", got)
	}
}

func TestReadPTConfigRejectsUnknownField(t *testing.T) {
	path := isolatePTConfig(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]string{"unknown": "value"})
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPTConfig(); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("readPTConfig error = %v", err)
	}
}

func TestValidateServerURL(t *testing.T) {
	for _, raw := range []string{"plumtree.example", "ssh://plumtree.example", "https://plumtree.example/path", "https://user@plumtree.example"} {
		if _, err := validateServerURL(raw); err == nil {
			t.Fatalf("validateServerURL(%q) succeeded", raw)
		}
	}
	if got, err := validateServerURL("http://127.0.0.1:18080/"); err != nil || got != "http://127.0.0.1:18080" {
		t.Fatalf("validateServerURL(localhost) = %q, %v", got, err)
	}
}

func TestAddServersMakesFirstDefaultAndResolvesAlias(t *testing.T) {
	path := isolatePTConfig(t)
	var out bytes.Buffer
	if err := cmdAddServer([]string{"https://main.example/", "primary"}, strings.NewReader("main-token\n"), &out); err != nil {
		t.Fatal(err)
	}
	if err := cmdAddServer([]string{"https://staging.example", "staging"}, strings.NewReader("stage-token\n"), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "main-token") || strings.Contains(out.String(), "stage-token") {
		t.Fatalf("add-server output exposed a token: %q", out.String())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config permissions = %o, want 600", got)
	}
	server, token, err := resolveConnection()
	if err != nil {
		t.Fatal(err)
	}
	if server != "https://main.example" || token != "main-token" {
		t.Fatalf("default connection = %q %q", server, token)
	}
	server, token, err = resolveConnectionForAlias("staging")
	if err != nil {
		t.Fatal(err)
	}
	if server != "https://staging.example" || token != "stage-token" {
		t.Fatalf("staging connection = %q %q", server, token)
	}
}

func TestAddServerRejectsDuplicateAndInvalidAliases(t *testing.T) {
	isolatePTConfig(t)
	if err := cmdAddServer([]string{"https://one.example", "one"}, strings.NewReader("token\n"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := cmdAddServer([]string{"https://two.example", "one"}, strings.NewReader("token\n"), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate alias error = %v", err)
	}
	if err := cmdAddServer([]string{"https://two.example", "not valid"}, strings.NewReader("token\n"), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "invalid server alias") {
		t.Fatalf("invalid alias error = %v", err)
	}
}

func TestResolveConnectionForUnknownAlias(t *testing.T) {
	isolatePTConfig(t)
	if _, _, err := resolveConnectionForAlias("missing"); err == nil || !strings.Contains(err.Error(), `unknown server alias "missing"`) {
		t.Fatalf("unknown alias error = %v", err)
	}
}
