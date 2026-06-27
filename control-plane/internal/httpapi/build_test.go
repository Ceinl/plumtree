package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	buildworker "github.com/Ceinl/plumtree/build-worker"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

// fakeBuilder stands in for the sandboxed build worker so control-plane tests
// stay fast and hermetic. It echoes a fixed result for the given source.
type fakeBuilder struct {
	result buildworker.Result
	err    error
	gotReq buildworker.Request
}

func (f *fakeBuilder) Build(_ context.Context, req buildworker.Request) (buildworker.Result, error) {
	f.gotReq = req
	return f.result, f.err
}

func TestDevDeployBuildsUploadedSource(t *testing.T) {
	wasm := []byte("\x00asm\x01\x00\x00\x00fake")
	sum := sha256.Sum256(wasm)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	builder := &fakeBuilder{result: buildworker.Result{
		Success:         true,
		WASM:            wasm,
		Digest:          digest,
		SizeBytes:       int64(len(wasm)),
		CompilerVersion: "go version test",
		DurationMillis:  12,
	}}
	server := NewWithConfig(Config{Store: control.NewStore(), DevToken: "secret", Build: builder})

	body := map[string]any{
		"appName":    "counter",
		"appType":    "tui",
		"abiVersion": 0,
		"source":     []byte("fake-archive"),
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatal(err)
	}
	rec := serveDevRequest(t, server, http.MethodPost, "/api/dev/deploy", &buf, "")

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(builder.gotReq.Source, []byte("fake-archive")) {
		t.Errorf("builder did not receive the uploaded source, got %q", builder.gotReq.Source)
	}
}

func TestDevDeploySurfacesBuildFailure(t *testing.T) {
	builder := &fakeBuilder{result: buildworker.Result{
		Success: false,
		Failure: &buildworker.Failure{Stage: buildworker.StageCompile, Message: "go build failed", Log: "main.go:1: syntax error"},
	}}
	server := NewWithConfig(Config{Store: control.NewStore(), DevToken: "secret", Build: builder})

	body := map[string]any{
		"appName": "counter",
		"appType": "tui",
		"source":  []byte("fake-archive"),
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatal(err)
	}
	rec := serveDevRequest(t, server, http.MethodPost, "/api/dev/deploy", &buf, "")

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Stage   string `json:"stage"`
		Message string `json:"message"`
		Log     string `json:"log"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Stage != string(buildworker.StageCompile) || resp.Message == "" || resp.Log == "" {
		t.Errorf("unexpected failure payload: %+v", resp)
	}
}

func TestDevDeploySourceWithoutBackendErrors(t *testing.T) {
	server := NewWithConfig(Config{Store: control.NewStore(), DevToken: "secret"})
	body := map[string]any{"appName": "counter", "appType": "tui", "source": []byte("x")}
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(body)
	rec := serveDevRequest(t, server, http.MethodPost, "/api/dev/deploy", &buf, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}
