package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	ok := []string{"counter", "my-app", "a", "app123", "x-y-z"}
	for _, n := range ok {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", n, err)
		}
	}
	bad := []string{"", "-lead", "trail-", "Upper", "has_underscore", "a/b", "a.b", "a b", "../x", strings.Repeat("a", 41)}
	for _, n := range bad {
		if err := ValidateName(n); err == nil {
			t.Errorf("ValidateName(%q) = nil, want error", n)
		}
	}
}

func TestNewTUIWritesLayout(t *testing.T) {
	dir := t.TempDir()
	proj, err := New(dir, "counter", TUI)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, rel := range []string{"go.mod", "plumtree.json", ".gitignore", "README.md", "AGENTS.md", "app/main.go"} {
		if _, err := os.Stat(filepath.Join(proj, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	gomod := read(t, proj, "go.mod")
	if !strings.Contains(gomod, "module counter") || !strings.Contains(gomod, "github.com/Ceinl/plumtree/sdk") {
		t.Errorf("go.mod missing module or sdk require:\n%s", gomod)
	}
	main := read(t, proj, "app/main.go")
	if !strings.Contains(main, `sdk.RunTUI`) || !strings.Contains(main, `Name: "counter"`) {
		t.Errorf("main.go not a TUI app for counter:\n%s", main)
	}
	if gi := read(t, proj, ".gitignore"); !strings.Contains(gi, ".env.plumtree.server.local") {
		t.Errorf(".gitignore missing secret file: %s", gi)
	}
	if mf := read(t, proj, "plumtree.json"); !strings.Contains(mf, `"type": "tui"`) {
		t.Errorf("manifest missing type: %s", mf)
	}
	if readme := read(t, proj, "README.md"); !strings.Contains(readme, "pt dev --headless") {
		t.Errorf("README missing deterministic TUI check: %s", readme)
	}
	agents := read(t, proj, "AGENTS.md")
	if !strings.Contains(agents, "This is a Plumtree tui app named `counter`") ||
		!strings.Contains(agents, "Run the deterministic smoke check") {
		t.Errorf("AGENTS.md missing project guidance:\n%s", agents)
	}
}

func TestNewCLIWritesCLIMain(t *testing.T) {
	proj, err := New(t.TempDir(), "hello", CLI)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if main := read(t, proj, "app/main.go"); !strings.Contains(main, "sdk.CLI") {
		t.Errorf("CLI main.go missing sdk.CLI:\n%s", main)
	}
	if readme := read(t, proj, "README.md"); !strings.Contains(readme, "pt dev Alice") {
		t.Errorf("CLI README missing CLI run command:\n%s", readme)
	}
	if agents := read(t, proj, "AGENTS.md"); !strings.Contains(agents, "write user output through `sdk.Ctx`") {
		t.Errorf("CLI AGENTS.md missing CLI guidance:\n%s", agents)
	}
}

func TestNewRefusesNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if _, err := New(dir, "counter", TUI); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir, "counter", TUI); err == nil {
		t.Error("New should refuse to overwrite an existing non-empty dir")
	}
}

func TestNewRejectsBadName(t *testing.T) {
	if _, err := New(t.TempDir(), "Bad Name", TUI); err == nil {
		t.Error("New should reject an invalid name")
	}
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
