package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/control-plane/internal/control"
	"github.com/Ceinl/plumtree/ssh-gateway/gateway"
	"github.com/Ceinl/plumtree/ssh-gateway/httpbackend"
)

const gwToken = "gw-secret"

// newGatewayBackend stands up a control-plane HTTP server with the gateway API
// enabled and returns an httpbackend.Client pointed at it, plus the store so
// tests can mutate platform state.
func newGatewayBackend(t *testing.T) (gateway.Backend, *control.Store) {
	t.Helper()
	store := control.NewStore()
	handler := NewWithConfig(Config{
		Store:        store,
		Verifier:     fakeVerifier{},
		AppOrigin:    "http://localhost:8080",
		GatewayToken: gwToken,
	}).Handler()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return httpbackend.New(srv.URL, gwToken), store
}

// seedRunnable creates an owned, active deploy and returns its handle + app ID.
func seedRunnable(t *testing.T, store *control.Store, wasm []byte) (handle, appID string) {
	t.Helper()
	owner, err := store.EnsureOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.EnsureApp(control.AppInput{OwnerID: owner.ID, Name: "counter"})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(wasm)
	artifact, err := store.CreateArtifact(control.ArtifactInput{
		Digest:        "sha256:" + hex.EncodeToString(sum[:]),
		SizeBytes:     int64(len(wasm)),
		BuildMetadata: map[string]string{"app_type": "cli"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutArtifactBytes(artifact.ID, wasm); err != nil {
		t.Fatal(err)
	}
	deploy, err := store.CreateDeploy(control.DeployInput{
		AppID:            app.ID,
		ArtifactID:       artifact.ID,
		SourceDigest:     "sha256:" + strings.Repeat("b", 64),
		CreatedByOwnerID: owner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateDeploy(app.ID, deploy.ID); err != nil {
		t.Fatal(err)
	}
	return owner.Handle + "/counter", app.ID
}

func TestGatewayBackendResolveAndSessionRoundTrip(t *testing.T) {
	backend, store := newGatewayBackend(t)
	wasm := []byte("\x00asm-fake-module")
	handle, appID := seedRunnable(t, store, wasm)

	run, err := backend.ResolveRunnable(handle)
	if err != nil {
		t.Fatalf("ResolveRunnable: %v", err)
	}
	if run.AppID != appID || run.AppName != "counter" || run.OwnerID == "" {
		t.Fatalf("unexpected runnable: %+v", run)
	}
	if run.AppType != "cli" {
		t.Fatalf("app type = %q, want cli", run.AppType)
	}
	if string(run.WASM) != string(wasm) {
		t.Fatalf("wasm round-trip mismatch: %q", run.WASM)
	}

	sessionID, err := backend.StartSession(run.AppID, run.DeployID)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if sessionID == "" {
		t.Fatal("empty session id")
	}
	if err := backend.RecordSessionLog(sessionID, "hello output", true); err != nil {
		t.Fatalf("RecordSessionLog: %v", err)
	}
	if err := backend.EndSession(sessionID); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	// The log and end were persisted server-side.
	sessions, err := store.ListSessionsForApp(appID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	got := sessions[0]
	if got.Log != "hello output" || !got.LogTruncated || got.EndedAt == nil {
		t.Fatalf("session not recorded as expected: %+v", got)
	}
}

func TestGatewayBackendSecretsAndEgress(t *testing.T) {
	backend, store := newGatewayBackend(t)
	_, appID := seedRunnable(t, store, []byte("wasm"))

	if _, err := store.UpsertSecret(control.SecretInput{AppID: appID, Key: "API_KEY", Value: []byte("v1")}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddEgressHost(appID, "api.example.com"); err != nil {
		t.Fatal(err)
	}

	if got := backend.SecretsForApp(appID); got["API_KEY"] != "v1" {
		t.Fatalf("secrets = %v, want API_KEY=v1", got)
	}
	allow := backend.EgressAllowlist(appID)
	if len(allow) != 1 || allow[0] != "api.example.com" {
		t.Fatalf("egress = %v, want [api.example.com]", allow)
	}
}

func TestGatewayBackendSuspendedAndUnknown(t *testing.T) {
	backend, store := newGatewayBackend(t)
	handle, appID := seedRunnable(t, store, []byte("wasm"))

	if _, err := store.SetAppSuspended(appID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ResolveRunnable(handle); !errors.Is(err, gateway.ErrSuspended) {
		t.Fatalf("suspended resolve err = %v, want ErrSuspended", err)
	}

	if _, err := backend.ResolveRunnable("alice/nope"); err == nil || errors.Is(err, gateway.ErrSuspended) {
		t.Fatalf("unknown resolve err = %v, want a plain not-found error", err)
	}
}

func TestGatewayAPIDisabledWithoutToken(t *testing.T) {
	store := control.NewStore()
	handler := NewWithConfig(Config{
		Store:     store,
		Verifier:  fakeVerifier{},
		AppOrigin: "http://localhost:8080",
		// GatewayToken intentionally empty.
	}).Handler()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	backend := httpbackend.New(srv.URL, "anything")
	if _, err := backend.ResolveRunnable("alice/counter"); err == nil {
		t.Fatal("expected error when gateway API is disabled")
	}
}
