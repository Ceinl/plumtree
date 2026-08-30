package cleanrole

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/internal/sqlite"
)

// seedOperatorApp creates one author with a deployed app so the suspend and
// quota commands have something real to act on. It avoids guest builds: the
// artifact bytes never run here.
func seedOperatorApp(t *testing.T, database string) {
	t.Helper()
	repo, err := sqlite.OpenRepository(database, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if _, _, err := repo.RegisterAuthor(context.Background(), sqlite.RegistrationInput{
		AuthorID: "author-1", Handle: "alice", DeviceID: "device-1", DeviceName: "laptop",
		PublicKey: "ssh-ed25519-key", Fingerprint: "fingerprint-1", RecoverySalt: []byte("salt"), RecoveryVerifier: []byte("verifier"),
	}); err != nil {
		t.Fatal(err)
	}
	wasm := []byte("wasm-bytes")
	digestBytes := sha256.Sum256(wasm)
	artifact, err := repo.PutArtifact(context.Background(), sqlite.ArtifactInput{ID: "artifact-1", Digest: "sha256:" + hex.EncodeToString(digestBytes[:]), WASM: wasm, ABIVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	app, err := repo.CreateApp(context.Background(), sqlite.AppInput{ID: "app-1", AuthorID: "author-1", Name: "tool", Kind: "cli", AccessMode: "public"})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.CreateDeployment(context.Background(), sqlite.DeploymentInput{ID: "deployment-1", AppID: app.ID, ArtifactID: artifact.ID, DeployedByDeviceID: "device-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ActivateDeployment(context.Background(), app.ID, deployment.ID); err != nil {
		t.Fatal(err)
	}
}

func openOperatorRepository(t *testing.T, database string) *sqlite.Repository {
	t.Helper()
	repo, err := sqlite.OpenRepository(database, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

func TestSuspendAndUnsuspendDeployCommand(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "plumtree.db")
	seedOperatorApp(t, database)

	out := &bytes.Buffer{}
	if err := Execute(context.Background(), []string{"suspend", "deploy", "deployment-1", "--database", database}, nil, out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"suspended":true`) {
		t.Fatalf("suspend output = %q", out.String())
	}
	repo := openOperatorRepository(t, database)
	if _, err := repo.ResolveRunnable(context.Background(), "author-1", "tool"); !errors.Is(err, sqlite.ErrSuspended) {
		t.Fatalf("suspended resolve = %v", err)
	}

	out.Reset()
	if err := Execute(context.Background(), []string{"unsuspend", "deploy", "deployment-1", "--database", database}, nil, out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"suspended":false`) {
		t.Fatalf("unsuspend output = %q", out.String())
	}
	if _, err := repo.ResolveRunnable(context.Background(), "author-1", "tool"); err != nil {
		t.Fatalf("unsuspended resolve = %v", err)
	}
}

func TestQuotaSetCommandEnforcesTheNewLimit(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "plumtree.db")
	seedOperatorApp(t, database)

	out := &bytes.Buffer{}
	args := []string{"quota", "set", "author-1", "4", "4", "4", "1", "--database", database}
	if err := Execute(context.Background(), args, nil, out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"maxSessions":1`) {
		t.Fatalf("quota output = %q", out.String())
	}

	repo := openOperatorRepository(t, database)
	session := sqlite.Session{ID: "session-1", AppID: "app-1", DeploymentID: "deployment-1",
		ArtifactDigest: "sha256:" + hex.EncodeToString(func() []byte { sum := sha256.Sum256([]byte("wasm-bytes")); return sum[:] }())}
	if err := repo.StartSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	session.ID = "session-2"
	if err := repo.StartSession(context.Background(), session); !errors.Is(err, sqlite.ErrQuota) {
		t.Fatalf("second session over quota = %v", err)
	}
}

func TestOperatorCommandsRejectBadInvocations(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "plumtree.db")
	seedOperatorApp(t, database)

	for _, test := range []struct {
		name string
		args []string
	}{
		{"missing deploy keyword", []string{"suspend", "deployment-1"}},
		{"wrong arity", []string{"unsuspend", "deploy"}},
		{"unknown deployment", []string{"suspend", "deploy", "deployment-404"}},
		{"quota wrong arity", []string{"quota", "set", "author-1", "4", "4"}},
		{"negative quota value", []string{"quota", "set", "author-1", "-1", "4", "4", "4"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := Execute(context.Background(), append(test.args, "--database", database), nil, &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil {
				t.Fatalf("%v succeeded", test.args)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "usage") && !errors.Is(err, sqlite.ErrNotFound) && !errors.Is(err, sqlite.ErrInvalid) {
				t.Fatalf("error = %v, want usage guidance or a sentinel", err)
			}
		})
	}
}
