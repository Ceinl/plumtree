package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func homeForSSHConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestInstallDevSSHConfigCreatesReplacesAndPreservesForeignContent(t *testing.T) {
	home := homeForSSHConfig(t)
	foreign := "Host work\n    HostName work.example.net\n"
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}

	path, err := installDevSSHConfig("plumtree.dev", "127.0.0.1", "2222")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(home, ".ssh", "config") {
		t.Fatalf("installed at %q", path)
	}

	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(first)
	if strings.Count(content, sshConfigBegin) != 1 || strings.Count(content, sshConfigEnd) != 1 {
		t.Fatalf("marker discipline violated: %q", content)
	}
	before, after, found := strings.Cut(content, sshConfigEnd)
	if !found || strings.Contains(before, "work.example.net") || !strings.Contains(after, "Host work") {
		t.Fatalf("foreign config must stay outside the managed block: %q", content)
	}
	if !strings.Contains(content, "Host plumtree.dev") || !strings.Contains(content, "Port 2222") {
		t.Fatalf("managed block incomplete: %q", content)
	}

	// Reinstalling with a different port replaces only the managed block.
	if _, err := installDevSSHConfig("plumtree.dev", "127.0.0.1", "3333"); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content = string(second)
	if strings.Count(content, sshConfigBegin) != 1 || strings.Count(content, sshConfigEnd) != 1 {
		t.Fatalf("replacement duplicated markers: %q", content)
	}
	if strings.Contains(content, "Port 2222") || !strings.Contains(content, "Port 3333") {
		t.Fatalf("replacement did not update the managed block: %q", content)
	}
	if !strings.Contains(content, foreign) {
		t.Fatalf("foreign config was altered: %q", content)
	}
}

func TestValidateSSHAlias(t *testing.T) {
	tests := []struct {
		alias   string
		wantErr bool
	}{
		{alias: "plumtree.dev"},
		{alias: "dev_1"},
		{alias: "", wantErr: true},
		{alias: "a b", wantErr: true},
		{alias: "a\tb", wantErr: true},
		{alias: "a\nb", wantErr: true},
	}
	for _, test := range tests {
		err := validateSSHAlias(test.alias)
		if (err != nil) != test.wantErr {
			t.Fatalf("validateSSHAlias(%q) = %v, want error=%v", test.alias, err, test.wantErr)
		}
	}
}

func TestLocalConnectHostMapsWildcardListeners(t *testing.T) {
	tests := map[string]string{
		"":          "127.0.0.1",
		"0.0.0.0":   "127.0.0.1",
		"::":        "127.0.0.1",
		"::1":       "[::1]",
		"127.0.0.1": "127.0.0.1",
		"localhost": "localhost",
	}
	for listen, want := range tests {
		if got := localConnectHost(listen); got != want {
			t.Fatalf("localConnectHost(%q) = %q, want %q", listen, got, want)
		}
	}
}
