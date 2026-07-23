package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCmdPingSucceedsWithResolvedServerAndToken(t *testing.T) {
	var gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Plumtree-Dev-Token")
		if r.URL.Path != "/api/dev/ping" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "apps": []any{}})
	}))
	defer server.Close()
	isolatePTConfig(t)
	t.Setenv("PLUMTREE_SERVER_URL", server.URL)
	t.Setenv("PLUMTREE_DEV_TOKEN", "env-token")

	var out bytes.Buffer
	if err := cmdPing(nil, &out); err != nil {
		t.Fatal(err)
	}
	if gotToken != "env-token" {
		t.Fatalf("token = %q", gotToken)
	}
	if !strings.Contains(out.String(), "Server: "+server.URL) || !strings.Contains(out.String(), "Status: reachable (authenticated)") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestCmdPingListPrintsAppsAndEmptyState(t *testing.T) {
	apps := []map[string]string{{"handle": "alice/counter", "activeDeployId": "dep_000001"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "apps": apps})
	}))
	defer server.Close()
	isolatePTConfig(t)
	t.Setenv("PLUMTREE_SERVER_URL", server.URL)
	t.Setenv("PLUMTREE_DEV_TOKEN", "token")

	var out bytes.Buffer
	if err := cmdPing([]string{"list"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "alice/counter  dep_000001") {
		t.Fatalf("list output = %q", out.String())
	}

	apps = nil
	out.Reset()
	if err := cmdPing([]string{"list"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No deployed apps.") {
		t.Fatalf("empty output = %q", out.String())
	}
}

func TestCmdPingReportsAuthenticationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid dev token"}`, http.StatusUnauthorized)
	}))
	defer server.Close()
	isolatePTConfig(t)
	t.Setenv("PLUMTREE_SERVER_URL", server.URL)
	t.Setenv("PLUMTREE_DEV_TOKEN", "wrong")

	err := cmdPing(nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "authentication failed") || !strings.Contains(err.Error(), server.URL) || !strings.Contains(err.Error(), "pt configure") {
		t.Fatalf("error = %v", err)
	}
}

func TestCmdPingReportsMissingTokenWithServer(t *testing.T) {
	isolatePTConfig(t)
	t.Setenv("PLUMTREE_SERVER_URL", "https://plumtree.example")
	err := cmdPing(nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "https://plumtree.example") || !strings.Contains(err.Error(), "missing deploy token") {
		t.Fatalf("error = %v", err)
	}
}

func TestExplainPingErrorDistinguishesDNSFailure(t *testing.T) {
	err := explainPingError("https://missing.example", &net.DNSError{Name: "missing.example", Err: "no such host"})
	if err == nil || !strings.Contains(err.Error(), "DNS lookup failed") || !strings.Contains(err.Error(), "check the server address") {
		t.Fatalf("error = %v", err)
	}
}
