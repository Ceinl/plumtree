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

func TestConfigureWritesSecureConfig(t *testing.T) {
	path := isolatePTConfig(t)
	var out bytes.Buffer
	if err := cmdConfigure([]string{
		"--addr", "https://plumtree.example/",
		"--token-stdin",
	}, strings.NewReader("deploy-secret\n"), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "deploy-secret") {
		t.Fatalf("configure output exposed token: %q", out.String())
	}
	if !strings.Contains(out.String(), "Address: https://plumtree.example") || !strings.Contains(out.String(), "Token:   configured") {
		t.Fatalf("unexpected configure output: %q", out.String())
	}
	cfg, err := readPTConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerURL != "https://plumtree.example" || cfg.DeployToken != "deploy-secret" {
		t.Fatalf("config = %+v", cfg)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config permissions = %o, want 600", got)
	}
}

func TestConfigureUpdatesOnlySpecifiedValue(t *testing.T) {
	isolatePTConfig(t)
	if _, err := writePTConfig(ptConfig{ServerURL: "https://old.example", DeployToken: "keep-me"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdConfigure([]string{"--addr", "https://new.example"}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	cfg, err := readPTConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerURL != "https://new.example" || cfg.DeployToken != "keep-me" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestConfigureTokenAliasReadsStdin(t *testing.T) {
	isolatePTConfig(t)
	if err := cmdConfigure([]string{"--token"}, strings.NewReader("deploy-secret\n"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	cfg, err := readPTConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeployToken != "deploy-secret" {
		t.Fatalf("deploy token = %q", cfg.DeployToken)
	}
}

func TestConfigureClearsToken(t *testing.T) {
	isolatePTConfig(t)
	if _, err := writePTConfig(ptConfig{ServerURL: "https://plumtree.example", DeployToken: "remove-me"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdConfigure([]string{"--clear-token"}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	cfg, err := readPTConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeployToken != "" || cfg.ServerURL != "https://plumtree.example" {
		t.Fatalf("config = %+v", cfg)
	}
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

func TestConfigureShowsManagedLocalToken(t *testing.T) {
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
	var out bytes.Buffer
	if err := cmdConfigure(nil, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Token:   automatic local token") {
		t.Fatalf("configure output = %q", out.String())
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

func TestConfigureRejectsPermissiveExistingConfigBeforeWritingToken(t *testing.T) {
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
	err := cmdConfigure([]string{"--token-stdin"}, strings.NewReader("new-secret\n"), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "insecure permissions") {
		t.Fatalf("cmdConfigure error = %v", err)
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
