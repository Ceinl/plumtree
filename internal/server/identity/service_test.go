package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	serverconfig "github.com/Ceinl/plumtree/internal/server/config"
	"github.com/Ceinl/plumtree/internal/sqlite"
)

func newTestService(t *testing.T) (*Service, *sqlite.Repository) {
	t.Helper()
	repo, err := sqlite.OpenRepository(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	cfg := serverconfig.Default()
	cfg.Roles.Control = true
	cfg.Exposure.HTTP = serverconfig.ExposureGate{Enabled: true, Address: "127.0.0.1:8080"}
	cfg.Exposure.SSH = serverconfig.ExposureGate{Enabled: true, Address: "127.0.0.1:2222"}
	cfg.Exposure.Gateway = serverconfig.ExposureGate{Enabled: true, Address: "127.0.0.1:9090"}
	counter := 0
	service, err := New(repo, cfg, WithIDFactory(func(prefix string) string {
		counter++
		return prefix + "_test_" + strconv.Itoa(counter)
	}))
	if err != nil {
		t.Fatal(err)
	}
	return service, repo
}

func TestPairedRegistrationEnrollmentRecoveryAndRevocation(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()
	secret := []byte(strings.Repeat("r", 32))
	registration, err := service.RegisterAuthor(ctx, RegisterInput{Handle: "alice", DeviceName: "laptop", PublicKey: "public-key-1", Fingerprint: "fingerprint-1", RecoverySecret: secret})
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := service.BeginDeviceAddition(ctx, registration.Author.ID, registration.Device.ID, "phone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteDeviceAddition(ctx, challenge.ID, []byte(strings.Repeat("x", 32)), "public-key-2", "fingerprint-2"); !errors.Is(err, sqlite.ErrConflict) {
		t.Fatalf("wrong token proof=%v", err)
	}
	second, err := service.CompleteDeviceAddition(ctx, challenge.ID, challenge.Secret, "public-key-2", "fingerprint-2")
	if err != nil {
		t.Fatal(err)
	}
	challenge2, err := service.BeginDeviceAddition(ctx, registration.Author.ID, second.ID, "tablet")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteDeviceAddition(ctx, challenge2.ID, challenge.Secret, "public-key-3", "fingerprint-3"); !errors.Is(err, sqlite.ErrConflict) {
		t.Fatalf("substituted token proof=%v", err)
	}
	third, err := service.CompleteDeviceAddition(ctx, challenge2.ID, challenge2.Secret, "public-key-3", "fingerprint-3")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RotateRecovery(ctx, registration.Author.ID, secret, []byte(strings.Repeat("n", 32))); err != nil {
		t.Fatalf("rotate recovery: %v", err)
	}
	if err := service.RotateRecovery(ctx, registration.Author.ID, secret, []byte(strings.Repeat("z", 32))); !errors.Is(err, sqlite.ErrConflict) {
		t.Fatalf("old recovery accepted: %v", err)
	}
	if err := service.RevokeDevice(ctx, registration.Author.ID, registration.Device.ID, second.ID); err != nil {
		t.Fatalf("revoke device: %v", err)
	}
	if err := service.RevokeDevice(ctx, registration.Author.ID, second.ID, third.ID); !errors.Is(err, sqlite.ErrNotFound) {
		t.Fatalf("revoked actor accepted: %v", err)
	}
}

func TestAuthorAppsAccessDeployAuditAndTombstone(t *testing.T) {
	service, repo := newTestService(t)
	ctx := context.Background()
	registration, err := service.RegisterAuthorLocal(ctx, RegisterInput{Handle: "alice", DeviceName: "laptop", PublicKey: "public-key", Fingerprint: "fingerprint", RecoverySecret: []byte(strings.Repeat("r", 32))})
	if err != nil {
		t.Fatal(err)
	}
	app, err := service.CreateApp(ctx, registration.Author.ID, registration.Device.ID, "app-1", "demo", "tui", "public")
	if err != nil {
		t.Fatal(err)
	}
	key, err := service.AddAccessKey(ctx, sqlite.AccessKeyInput{ID: "key-1", AppID: app.ID, Name: "ci", PublicKey: "access-key", Fingerprint: "access-fingerprint", AddedByDeviceID: registration.Device.ID})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := service.ListAccessKeys(ctx, registration.Author.ID, app.ID)
	if err != nil || len(keys) != 1 || keys[0].ID != key.ID {
		t.Fatalf("keys=%+v err=%v", keys, err)
	}
	wasm := []byte("wasm")
	sum := sha256.Sum256(wasm)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	artifact, err := repo.PutArtifact(ctx, sqlite.ArtifactInput{ID: "artifact-1", Digest: digest, WASM: wasm, ABIVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := service.Deploy(ctx, sqlite.DeploymentInput{ID: "deploy-1", AppID: app.ID, ArtifactID: artifact.ID, DeployedByDeviceID: registration.Device.ID})
	if err != nil {
		t.Fatal(err)
	}
	if deployment.ID != "deploy-1" {
		t.Fatalf("deployment=%+v", deployment)
	}
	if err := service.RemoveAccessKey(ctx, registration.Author.ID, app.ID, key.ID, registration.Device.ID); err != nil {
		t.Fatal(err)
	}
	if events, err := service.ListAudit(ctx, sqlite.AuditFilter{ScopeAuthorID: registration.Author.ID, Action: "app.access.add", Limit: 10}); err != nil || len(events) != 1 {
		t.Fatalf("audit events=%+v err=%v", events, err)
	}
	var output bytes.Buffer
	if err := service.RunCommand([]string{"audit", "list", "--author-id", registration.Author.ID}, &output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte("author.register")) || bytes.Contains(output.Bytes(), []byte("wasm")) {
		t.Fatalf("audit output=%s", output.Bytes())
	}
	if removed, err := service.PruneAudit(ctx, time.Now().Add(time.Hour)); err != nil || removed == 0 {
		t.Fatalf("prune removed=%d err=%v", removed, err)
	}
	if err := service.RetireAuthorLocal(ctx, registration.Author.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterAuthorLocal(ctx, RegisterInput{Handle: "alice", DeviceName: "new", PublicKey: "new-key", Fingerprint: "new-fingerprint", RecoverySecret: []byte(strings.Repeat("s", 32))}); !errors.Is(err, sqlite.ErrConflict) {
		t.Fatalf("tombstone bypassed: %v", err)
	}
}

func TestIndependentExposureGates(t *testing.T) {
	repo, err := sqlite.OpenRepository(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	cfg := serverconfig.Default()
	cfg.Roles.Control = true
	service, err := New(repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.RegisterAuthor(context.Background(), RegisterInput{Handle: "alice", DeviceName: "laptop", PublicKey: "key", Fingerprint: "fp", RecoverySecret: []byte(strings.Repeat("r", 32))})
	if !errors.Is(err, ErrGateDisabled) {
		t.Fatalf("registration gate error=%v", err)
	}
}
