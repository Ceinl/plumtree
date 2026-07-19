package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/control-plane/internal/control"
	"github.com/Ceinl/plumtree/runner"
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

func TestSuspensionFansOutAndWaitsForEveryGateway(t *testing.T) {
	store := control.NewStore()
	handler := NewWithConfig(Config{Store: store, GatewayToken: gwToken}).Handler()
	srv := httptest.NewServer(handler)
	defer srv.Close()
	_, appID := seedRunnable(t, store, []byte("wasm"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clients := []*httpbackend.Client{
		httpbackend.New(srv.URL, gwToken),
		httpbackend.New(srv.URL, gwToken),
	}
	received := []chan gateway.Suspension{make(chan gateway.Suspension, 1), make(chan gateway.Suspension, 1)}
	release := []chan struct{}{make(chan struct{}), make(chan struct{})}
	for i, client := range clients {
		i := i
		if err := client.StartSuspensionWatcher(ctx, func(ctx context.Context, event gateway.Suspension) error {
			received[i] <- event
			select {
			case <-release[i]:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}); err != nil {
			t.Fatal(err)
		}
	}

	done := make(chan error, 1)
	go func() {
		_, err := store.SetAppSuspended(appID, true)
		done <- err
	}()
	for i := range received {
		select {
		case event := <-received[i]:
			if event.Scope != gateway.KillApp || event.ID != appID {
				t.Fatalf("gateway %d event = %+v", i, event)
			}
		case <-time.After(time.Second):
			t.Fatalf("gateway %d did not receive suspension", i)
		}
	}
	close(release[0])
	select {
	case err := <-done:
		t.Fatalf("suspension returned before every gateway acknowledged: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release[1])
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("suspension did not return after all acknowledgements")
	}
}

func TestSuspensionFanoutIncludesOwnerAppAndDeploy(t *testing.T) {
	store := control.NewStore()
	handler := NewWithConfig(Config{Store: store, GatewayToken: gwToken}).Handler()
	srv := httptest.NewServer(handler)
	defer srv.Close()
	_, appID := seedRunnable(t, store, []byte("wasm"))
	app, deploy, _, err := store.ResolveActiveDeploy("alice", "counter")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	received := []chan gateway.Suspension{make(chan gateway.Suspension, 3), make(chan gateway.Suspension, 3)}
	for i := range received {
		i := i
		if err := httpbackend.New(srv.URL, gwToken).StartSuspensionWatcher(ctx, func(_ context.Context, event gateway.Suspension) error {
			received[i] <- event
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	changes := []struct {
		want    gateway.Suspension
		suspend func() error
	}{
		{gateway.Suspension{Scope: gateway.KillOwner, ID: app.OwnerID}, func() error {
			_, err := store.SetOwnerSuspended(app.OwnerID, true)
			return err
		}},
		{gateway.Suspension{Scope: gateway.KillApp, ID: appID}, func() error {
			_, err := store.SetAppSuspended(appID, true)
			return err
		}},
		{gateway.Suspension{Scope: gateway.KillDeploy, ID: deploy.ID}, func() error {
			return store.SetDeploySuspended(deploy.ID, true)
		}},
	}
	for _, change := range changes {
		if err := change.suspend(); err != nil {
			t.Fatal(err)
		}
		for i := range received {
			select {
			case got := <-received[i]:
				if got != change.want {
					t.Fatalf("gateway %d suspension = %+v, want %+v", i, got, change.want)
				}
			case <-time.After(time.Second):
				t.Fatalf("gateway %d did not receive %+v", i, change.want)
			}
		}
	}
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

func TestGatewayBackendResolvesOnlyRegisteredKeyAsAuthenticated(t *testing.T) {
	backend, store := newGatewayBackend(t)
	unknown, err := backend.ResolveIdentity("SHA256:unknown")
	if err != nil {
		t.Fatal(err)
	}
	if unknown.User != "SHA256:unknown" || unknown.Authenticated || unknown.Kind != runner.IdentitySSHKey || unknown.OwnerID != "" {
		t.Fatalf("unknown key identity = %+v", unknown)
	}

	owner, err := store.CreateOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterSSHKey(control.SSHKeyInput{
		OwnerID: owner.ID, Name: "laptop", PublicKey: "ssh-ed25519 AAAATEST", Fingerprint: "SHA256:registered",
	}); err != nil {
		t.Fatal(err)
	}
	registered, err := backend.ResolveIdentity("SHA256:registered")
	if err != nil {
		t.Fatal(err)
	}
	if registered.User != "SHA256:registered" || !registered.Authenticated || registered.Kind != runner.IdentitySSHKey || registered.OwnerID != owner.ID {
		t.Fatalf("registered key identity = %+v", registered)
	}
}

func TestGatewayBackendResolveMultiMegabyteArtifact(t *testing.T) {
	backend, store := newGatewayBackend(t)
	wasm := bytes.Repeat([]byte("plumtree-wasm"), (3<<20)/len("plumtree-wasm")+1)
	wasm = wasm[:3<<20]
	handle, _ := seedRunnable(t, store, wasm)

	run, err := backend.ResolveRunnable(handle)
	if err != nil {
		t.Fatalf("ResolveRunnable with %d-byte artifact: %v", len(wasm), err)
	}
	if !bytes.Equal(run.WASM, wasm) {
		t.Fatalf("artifact round-trip mismatch: got %d bytes, want %d", len(run.WASM), len(wasm))
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
	run, err := backend.ResolveRunnable(handle)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.SetAppSuspended(appID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ResolveRunnable(handle); !errors.Is(err, gateway.ErrSuspended) {
		t.Fatalf("suspended resolve err = %v, want ErrSuspended", err)
	}
	if _, err := backend.StartSession(run.AppID, run.DeployID); !errors.Is(err, gateway.ErrSuspended) {
		t.Fatalf("suspended start err = %v, want ErrSuspended", err)
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
