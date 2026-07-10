package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	buildworker "github.com/Ceinl/plumtree/build-worker"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

// fakeBuilder stands in for the sandboxed build worker so control-plane tests
// stay fast and hermetic. It echoes a fixed result for the given source.
type fakeBuilder struct {
	result buildworker.Result
	err    error
	gotReq buildworker.Request
	calls  atomic.Int32
}

func (f *fakeBuilder) Build(_ context.Context, req buildworker.Request) (buildworker.Result, error) {
	f.calls.Add(1)
	f.gotReq = req
	return f.result, f.err
}

func successfulFakeBuilder() *fakeBuilder {
	wasm := []byte("\x00asm\x01\x00\x00\x00fake")
	return &fakeBuilder{result: buildworker.Result{Success: true, WASM: wasm, Digest: testDigest(wasm), SizeBytes: int64(len(wasm))}}
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

func TestDevDeployRejectsInvalidFieldsBeforeBuild(t *testing.T) {
	builder := successfulFakeBuilder()
	server := NewWithConfig(Config{Store: control.NewStore(), DevToken: "secret", Build: builder})
	body := map[string]any{"appName": "INVALID", "appType": "tui", "source": []byte("archive")}
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(body)
	rec := serveDevRequest(t, server, http.MethodPost, "/api/dev/deploy", &buf, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := builder.calls.Load(); got != 0 {
		t.Fatalf("builder calls = %d, want 0", got)
	}
}

func TestDevDeployUpdateAuthenticatesClaimBeforeBuild(t *testing.T) {
	builder := successfulFakeBuilder()
	server := NewWithConfig(Config{Store: control.NewStore(), DevToken: "secret", Build: builder})
	created := createDevDeploy(t, server, []byte("old wasm"))
	body := map[string]any{"appName": "counter", "appType": "tui", "source": []byte("archive")}
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(body)
	rec := serveDevRequest(t, server, http.MethodPut, "/api/dev/deploy/"+url.PathEscape(created.Deploy.ID), &buf, "wrong-token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := builder.calls.Load(); got != 0 {
		t.Fatalf("builder calls = %d, want 0", got)
	}
}

func TestDevDeployReservesQuotaBeforeBuild(t *testing.T) {
	store := control.NewStore(control.WithMaxDeployClaimsPerHour(1))
	builder := successfulFakeBuilder()
	server := NewWithConfig(Config{Store: store, DevToken: "secret", Build: builder})
	createDevDeploy(t, server, []byte("old wasm"))
	body := map[string]any{"appName": "another", "appType": "tui", "source": []byte("archive")}
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(body)
	rec := serveDevRequest(t, server, http.MethodPost, "/api/dev/deploy", &buf, "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := builder.calls.Load(); got != 0 {
		t.Fatalf("builder calls = %d, want 0", got)
	}
}

type blockingBuilder struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingBuilder) Build(ctx context.Context, _ buildworker.Request) (buildworker.Result, error) {
	b.entered <- struct{}{}
	select {
	case <-b.release:
	case <-ctx.Done():
		return buildworker.Result{}, ctx.Err()
	}
	wasm := []byte("\x00asm\x01\x00\x00\x00")
	return buildworker.Result{Success: true, WASM: wasm, Digest: testDigest(wasm), SizeBytes: int64(len(wasm))}, nil
}

func TestDevDeployBuildConcurrencyLimit(t *testing.T) {
	builder := &blockingBuilder{entered: make(chan struct{}, 2), release: make(chan struct{}, 2)}
	server := NewWithConfig(Config{Store: control.NewStore(), DevToken: "secret", Build: builder, MaxConcurrentBuilds: 1, MaxQueuedBuilds: 1})
	request := func(name string) *httptest.ResponseRecorder {
		body := `{"appName":"` + name + `","appType":"tui","source":"YXJjaGl2ZQ=="}`
		req := httptest.NewRequest(http.MethodPost, "/api/dev/deploy", strings.NewReader(body))
		req.Header.Set("X-Plumtree-Dev-Token", "secret")
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)
		return rec
	}
	done := make(chan *httptest.ResponseRecorder, 2)
	go func() { done <- request("one") }()
	<-builder.entered
	go func() { done <- request("two") }()
	select {
	case <-builder.entered:
		t.Fatal("second build entered while first held the only slot")
	case <-time.After(100 * time.Millisecond):
	}
	builder.release <- struct{}{}
	select {
	case <-builder.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("second build did not enter after slot release")
	}
	builder.release <- struct{}{}
	for range 2 {
		if rec := <-done; rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	}
}

func TestDevDeployBuildQueueLimit(t *testing.T) {
	builder := &blockingBuilder{entered: make(chan struct{}, 2), release: make(chan struct{}, 2)}
	server := NewWithConfig(Config{Store: control.NewStore(), DevToken: "secret", Build: builder, MaxConcurrentBuilds: 1, MaxQueuedBuilds: 1})
	request := func(name string) *httptest.ResponseRecorder {
		body := `{"appName":"` + name + `","appType":"tui","source":"YXJjaGl2ZQ=="}`
		req := httptest.NewRequest(http.MethodPost, "/api/dev/deploy", strings.NewReader(body))
		req.Header.Set("X-Plumtree-Dev-Token", "secret")
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)
		return rec
	}
	done := make(chan *httptest.ResponseRecorder, 2)
	go func() { done <- request("one") }()
	<-builder.entered
	go func() { done <- request("two") }()
	deadline := time.Now().Add(time.Second)
	for len(server.buildQueue) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(server.buildQueue) != 1 {
		t.Fatal("second build did not enter the queue")
	}
	if rec := request("three"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("overflow status = %d, body = %s", rec.Code, rec.Body.String())
	}
	builder.release <- struct{}{}
	<-builder.entered
	builder.release <- struct{}{}
	for range 2 {
		if rec := <-done; rec.Code != http.StatusCreated {
			t.Fatalf("admitted status = %d, body = %s", rec.Code, rec.Body.String())
		}
	}
}
