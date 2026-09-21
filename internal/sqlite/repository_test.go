package sqlite

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
)

func newTestRepository(t *testing.T, options ...RepositoryOption) *Repository {
	t.Helper()
	r, err := OpenRepository(":memory:", nil, options...)
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func registerTestAuthor(t *testing.T, r *Repository) (Author, Device) {
	t.Helper()
	a, d, err := r.RegisterAuthor(context.Background(), RegistrationInput{
		AuthorID: "author-1", Handle: "alice", DeviceID: "device-1", DeviceName: "laptop",
		PublicKey: "ssh-ed25519-key", Fingerprint: "fingerprint-1", RecoverySalt: []byte("salt"), RecoveryVerifier: []byte("verifier"),
	})
	if err != nil {
		t.Fatalf("register author: %v", err)
	}
	return a, d
}

func TestRepositorySchemaAndAtomicJourney(t *testing.T) {
	r := newTestRepository(t)
	a, d := registerTestAuthor(t, r)
	if got, err := r.ServerIdentity(context.Background()); !errors.Is(err, ErrNotFound) || got.ID != "" {
		t.Fatalf("empty identity = %#v, %v", got, err)
	}
	if err := r.SetServerIdentity(context.Background(), ServerIdentity{ID: "server-1", SSHHostKeyAlgorithm: "ssh-ed25519", SSHHostKeyFingerprint: "SHA256:host"}); err != nil {
		t.Fatalf("identity: %v", err)
	}

	wasm := []byte("wasm-bytes")
	hash := sha256.Sum256(wasm)
	digest := "sha256:" + hexForTest(hash[:])
	artifact, err := r.PutArtifact(context.Background(), ArtifactInput{ID: "artifact-1", Digest: digest, WASM: wasm, ABIVersion: 1})
	if err != nil {
		t.Fatalf("artifact: %v", err)
	}
	if got, err := r.ListArtifactMetadata(context.Background(), 10); err != nil || len(got) != 1 || got[0].WASMBytesForTest() != nil {
		t.Fatalf("metadata separation: %#v, %v", got, err)
	}
	if _, err := r.PutArtifact(context.Background(), ArtifactInput{ID: "artifact-2", Digest: digest, WASM: []byte("different"), ABIVersion: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid digest = %v", err)
	}

	app, err := r.CreateApp(context.Background(), AppInput{ID: "app-1", AuthorID: a.ID, Name: "demo", Kind: "tui", AccessMode: "public"})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	dep, err := r.CreateDeployment(context.Background(), DeploymentInput{ID: "deployment-1", AppID: app.ID, ArtifactID: artifact.ID, DeployedByDeviceID: d.ID})
	if err != nil {
		t.Fatalf("deployment: %v", err)
	}
	if err := r.ActivateDeployment(context.Background(), app.ID, dep.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	runnable, err := r.ResolveRunnable(context.Background(), "author-1", "demo")
	if err != nil || string(runnable.WASM) != string(wasm) || runnable.App.UpdatedAt.IsZero() || runnable.Artifact.CreatedAt.IsZero() {
		t.Fatalf("resolve runnable: %#v, %v", runnable, err)
	}
	if err := r.SetDeploymentSuspended(context.Background(), dep.ID, true); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if _, err := r.ResolveRunnable(context.Background(), "author-1", "demo"); !errors.Is(err, ErrSuspended) {
		t.Fatalf("suspended resolve = %v", err)
	}
	if err := r.SetDeploymentSuspended(context.Background(), dep.ID, false); err != nil {
		t.Fatalf("resume: %v", err)
	}

	if err := r.SetCapabilityConfig(context.Background(), app.ID, "kv", "mode", "strict"); err != nil {
		t.Fatalf("cap config: %v", err)
	}
	if err := r.SetCapabilityValue(context.Background(), app.ID, "kv", "counter", []byte("1")); err != nil {
		t.Fatalf("cap value: %v", err)
	}
	if values, err := r.CapabilityValues(context.Background(), app.ID); err != nil || len(values) != 1 || string(values[0].Value) != "1" {
		t.Fatalf("cap values: %#v, %v", values, err)
	}
	if secret, err := r.SetSecret(context.Background(), app.ID, "token", []byte("secret")); err != nil || secret.Version != 1 {
		t.Fatalf("secret set: %#v, %v", secret, err)
	}
	if _, value, err := r.Secret(context.Background(), app.ID, "token"); err != nil || string(value) != "secret" {
		t.Fatalf("secret read: %q, %v", value, err)
	}

	if err := r.StartSession(context.Background(), Session{ID: "session-1", AppID: app.ID, DeploymentID: dep.ID, ArtifactDigest: digest, LeafIdentitySummary: "device-1"}); err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := r.RecordSessionLog(context.Background(), "session-1", "hello", false); err != nil {
		t.Fatalf("session log: %v", err)
	}
	if _, err := r.EndSession(context.Background(), "session-1"); err != nil {
		t.Fatalf("end session: %v", err)
	}
	ended, err := r.EndSession(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("replayed end session: %v", err)
	}
	sessions, err := r.ListSessions(context.Background(), app.ID, 10)
	if err != nil || len(sessions) != 1 || sessions[0].EndedAt == nil || sessions[0].Log != "hello" {
		t.Fatalf("sessions: %#v, %v", sessions, err)
	}
	if !sessions[0].EndedAt.Equal(*ended) {
		t.Fatalf("replayed end changed the end time: %v vs %v", sessions[0].EndedAt, ended)
	}
}

func TestRepositoryFaultsRollbackAndNotifyAfterCommit(t *testing.T) {
	var events []CommitEvent
	var r *Repository
	r = newTestRepository(t, WithCommitListener(func(event CommitEvent) error {
		events = append(events, event)
		var count int
		if err := r.DB().QueryRow(`SELECT COUNT(*) FROM authors`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("listener saw count %d before commit", count)
		}
		return nil
	}))
	_, _, err := r.RegisterAuthor(context.Background(), RegistrationInput{AuthorID: "author-1", Handle: "alice", DeviceID: "device-1", DeviceName: "laptop", PublicKey: "key", Fingerprint: "fp", RecoverySalt: []byte("s"), RecoveryVerifier: []byte("v")})
	if err != nil || len(events) != 1 {
		t.Fatalf("successful registration: %v events=%d", err, len(events))
	}

	statementRepo := newTestRepository(t, WithRepositoryFaults(Faults{Statement: func(string) error { return errors.New("boom") }}))
	_, _, err = statementRepo.RegisterAuthor(context.Background(), RegistrationInput{AuthorID: "author-2", Handle: "bob", DeviceID: "device-2", DeviceName: "laptop", PublicKey: "key", Fingerprint: "fp2", RecoverySalt: []byte("s"), RecoveryVerifier: []byte("v")})
	if !errors.Is(err, ErrInjectedStatement) {
		t.Fatalf("statement fault = %v", err)
	}
	assertCount(t, statementRepo, "authors", 0)

	commitRepo := newTestRepository(t, WithRepositoryFaults(Faults{Commit: func(string) error { return errors.New("boom") }}))
	_, _, err = commitRepo.RegisterAuthor(context.Background(), RegistrationInput{AuthorID: "author-3", Handle: "carol", DeviceID: "device-3", DeviceName: "laptop", PublicKey: "key", Fingerprint: "fp3", RecoverySalt: []byte("s"), RecoveryVerifier: []byte("v")})
	if !errors.Is(err, ErrInjectedCommit) {
		t.Fatalf("commit fault = %v", err)
	}
	assertCount(t, commitRepo, "authors", 0)
}

func assertCount(t *testing.T, r *Repository, table string, want int) {
	t.Helper()
	var got int
	if err := r.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s count=%d want %d", table, got, want)
	}
}
func hexForTest(value []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, len(value)*2)
	for i, v := range value {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&15]
	}
	return string(out)
}

// WASMBytesForTest is intentionally always empty: metadata DTOs cannot carry
// the blob. It keeps the separation assertion readable without exposing data.
func (a ArtifactMetadata) WASMBytesForTest() []byte { return nil }

// ResolveLeafRunnable must gate restricted apps on a registered access key or
// the owner's own device, while public apps stay open to everyone.
func TestResolveLeafRunnableEnforcesAccessMode(t *testing.T) {
	r := newTestRepository(t)
	a, d := registerTestAuthor(t, r)
	wasm := []byte("wasm-bytes")
	hash := sha256.Sum256(wasm)
	digest := "sha256:" + hexForTest(hash[:])
	artifact, err := r.PutArtifact(context.Background(), ArtifactInput{ID: "artifact-1", Digest: digest, WASM: wasm, ABIVersion: 1})
	if err != nil {
		t.Fatalf("artifact: %v", err)
	}
	for _, app := range []struct{ name, access string }{{"open", "public"}, {"closed", "restricted"}} {
		created, err := r.CreateApp(context.Background(), AppInput{ID: "app-" + app.name, AuthorID: a.ID, Name: app.name, Kind: "cli", AccessMode: app.access})
		if err != nil {
			t.Fatalf("app %s: %v", app.name, err)
		}
		deployment, err := r.CreateDeployment(context.Background(), DeploymentInput{ID: "deployment-" + app.name, AppID: created.ID, ArtifactID: artifact.ID, DeployedByDeviceID: d.ID})
		if err != nil {
			t.Fatalf("deployment %s: %v", app.name, err)
		}
		if err := r.ActivateDeployment(context.Background(), created.ID, deployment.ID); err != nil {
			t.Fatalf("activate %s: %v", app.name, err)
		}
	}
	if _, err := r.AddAccessKey(context.Background(), AccessKeyInput{ID: "access-1", AppID: "app-closed", Name: "guest", PublicKey: "ssh-ed25519-guest", Fingerprint: "SHA256:guest", AddedByDeviceID: d.ID}); err != nil {
		t.Fatalf("access key: %v", err)
	}

	tests := []struct {
		name             string
		handle           string
		fingerprint      string
		identityAuthorID string
		allowed          bool
	}{
		{"public anonymous", "alice/open", "", "", true},
		{"public unknown fingerprint", "alice/open", "SHA256:stranger", "", true},
		{"restricted anonymous denied", "alice/closed", "", "", false},
		{"restricted unknown fingerprint denied", "alice/closed", "SHA256:stranger", "", false},
		{"restricted access key fingerprint allowed", "alice/closed", "SHA256:guest", "", true},
		{"restricted owner device allowed", "alice/closed", d.Fingerprint, "", true},
		{"restricted owner identity allowed", "alice/closed", "", a.ID, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			author, app, _ := strings.Cut(test.handle, "/")
			runnable, err := r.ResolveLeafRunnable(context.Background(), author, app, test.fingerprint, test.identityAuthorID)
			if test.allowed && err != nil {
				t.Fatalf("allowed resolve failed: %v", err)
			}
			if !test.allowed && !errors.Is(err, ErrNotFound) {
				t.Fatalf("denied resolve = %v, want ErrNotFound", err)
			}
			if test.allowed && string(runnable.WASM) != string(wasm) {
				t.Fatalf("runnable WASM = %q", runnable.WASM)
			}
		})
	}
}
