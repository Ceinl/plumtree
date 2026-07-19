package main

import "testing"

func TestValidateBrowserURL(t *testing.T) {
	for _, good := range []string{
		"https://plumtree.example/claim/dep/token",
		"http://localhost:8080/claim/dep/token?x=1",
	} {
		if err := validateBrowserURL(good); err != nil {
			t.Errorf("validateBrowserURL(%q): %v", good, err)
		}
	}
	for _, bad := range []string{
		"", "javascript:alert(1)", "file:///etc/passwd",
		"https://user:pass@example.com/", "https://example.com/\nnext",
	} {
		if err := validateBrowserURL(bad); err == nil {
			t.Errorf("validateBrowserURL(%q) succeeded", bad)
		}
	}
}
