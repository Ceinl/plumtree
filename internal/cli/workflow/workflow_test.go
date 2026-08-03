package workflow

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/internal/cli/paired"
	"github.com/Ceinl/plumtree/internal/cli/scaffold"
)

func TestStrictManifestAndExplicitScaffolds(t *testing.T) {
	root := t.TempDir()
	if _, err := NewScaffold(root, "demo", scaffold.TUI, "restricted"); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(filepath.Join(root, "demo"))
	if err != nil || manifest.Access != "restricted" || manifest.Type != "tui" {
		t.Fatalf("manifest=%+v err=%v", manifest, err)
	}
	if _, err := NewScaffold(root, "bad", scaffold.TUI, ""); err == nil {
		t.Fatal("missing access policy accepted")
	}
	if err := os.WriteFile(filepath.Join(root, "demo", "plumtree.json"), []byte(`{"name":"demo","type":"tui","access":"public","future":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(filepath.Join(root, "demo")); err == nil {
		t.Fatal("unknown manifest field accepted")
	}
}

func TestPersistentProfileReset(t *testing.T) {
	root := t.TempDir()
	caps, cleanup, err := OpenProfile(Profile{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := caps.KV.Set("persist", []byte("value")); err != nil {
		t.Fatal(err)
	}
	cleanup()
	caps, cleanup, err = OpenProfile(Profile{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	value, found, err := caps.KV.Get("persist")
	cleanup()
	if err != nil || !found || string(value) != "value" {
		t.Fatalf("value=%q found=%t err=%v", value, found, err)
	}
	_, cleanup, err = OpenProfile(Profile{Root: root, Reset: true})
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	caps, cleanup, err = OpenProfile(Profile{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	_, found, err = caps.KV.Get("persist")
	cleanup()
	if err != nil || found {
		t.Fatalf("reset did not remove value: found=%t err=%v", found, err)
	}
}

func TestAPIUsesStableProblemAndMultipartDeployment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/version" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"product":"plumtree","version":"v1","apiVersion":1,"abiVersion":4}`)
			return
		}
		if r.URL.Path == "/api/v1/apps" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"code":"forbidden","detail":"bad\u001b[31m"}`)
			return
		}
		if r.URL.Path == "/api/v1/deployments" {
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
				t.Error("deployment is not multipart")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"apiVersion":1,"app":{"id":"app"}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	baseURL, _ := url.Parse(server.URL)
	transport := server.Client().Transport
	api := &API{HTTP: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		request.URL.Scheme, request.URL.Host = baseURL.Scheme, baseURL.Host
		return transport.RoundTrip(request)
	})}}
	version, err := api.Version(context.Background())
	if err != nil || version.Version != "v1" {
		t.Fatalf("version=%+v err=%v", version, err)
	}
	_, err = api.Apps(context.Background())
	if err == nil || !strings.Contains(err.Error(), "forbidden:") || strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("problem err=%v", err)
	}
	result, err := api.Deploy(context.Background(), ArtifactRequest{Name: "demo", Type: "tui", Access: "public", ABIVersion: 4, WASM: []byte("wasm")}, "")
	if err != nil || result.API != 1 {
		t.Fatalf("deploy=%+v err=%v", result, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestSSHInstructionRejectsTerminalInjection(t *testing.T) {
	if _, err := SSHInstruction(testServerRecord(), "owner/app\nmalicious"); err == nil {
		t.Fatal("unsafe handle accepted")
	}
}

func testServerRecord() paired.ServerRecord {
	return paired.ServerRecord{Name: "main", ServerID: "server", Host: "example.test", Port: 2222,
		HostKeyAlgorithm: "ssh-ed25519", HostKeyFingerprint: "SHA256:host", ProductVersion: "v1", KeyRef: "key.ed25519"}
}
