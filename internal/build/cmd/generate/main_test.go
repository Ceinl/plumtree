package main

import "testing"

func TestIncludeModuleFile(t *testing.T) {
	for _, path := range []string{"go.mod", "go.sum", "sdk.go", "internal/tui/app.go"} {
		if !includeModuleFile(path) {
			t.Errorf("includeModuleFile(%q) = false", path)
		}
	}
	for _, path := range []string{"README.md", "buschat", "sdk_test.go", "internal/tui/app_test.go"} {
		if includeModuleFile(path) {
			t.Errorf("includeModuleFile(%q) = true", path)
		}
	}
}

func TestExcludedModuleDir(t *testing.T) {
	for _, name := range []string{".git", "examples", "plums"} {
		if !excludedModuleDir(name) {
			t.Errorf("excludedModuleDir(%q) = false", name)
		}
	}
	if excludedModuleDir("internal") {
		t.Fatal("internal source directory was excluded")
	}
}
