package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolatePTConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nested", "pt.json")
	t.Setenv("PLUMTREE_PT_CONFIG", path)
	t.Setenv("PLUMTREE_SERVER_URL", "")
	t.Setenv("PLUMTREE_DEV_TOKEN", "")
	return path
}

func TestConfigureWritesSecureConfig(t *testing.T) {
	path := isolatePTConfig(t)
	var out bytes.Buffer
	if err := cmdConfigure([]string{
		"--addr", "https://plumtree.example/",
		"--token", "deploy-secret",
	}, &out); err != nil {
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
	if err := cmdConfigure([]string{"--addr", "https://new.example"}, &bytes.Buffer{}); err != nil {
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

func TestConfigureClearsToken(t *testing.T) {
	isolatePTConfig(t)
	if _, err := writePTConfig(ptConfig{ServerURL: "https://plumtree.example", DeployToken: "remove-me"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdConfigure([]string{"--clear-token"}, &bytes.Buffer{}); err != nil {
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
