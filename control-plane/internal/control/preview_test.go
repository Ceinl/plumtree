package control

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// makePreviewDeploy creates an unclaimed deploy with artifact bytes and returns
// its id.
func makePreviewDeploy(t *testing.T, store *Store, wasm []byte) string {
	t.Helper()
	art, err := store.CreateArtifact(ArtifactInput{Digest: digestBytes(wasm), SizeBytes: int64(len(wasm))})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutArtifactBytes(art.ID, wasm); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployClaim(DeployClaimInput{
		AppName: "counter", AppType: "tui", ArtifactID: art.ID,
		SourceDigest:   digestBytes([]byte("src")),
		ClaimTokenHash: digestBytes([]byte("tok")),
	})
	if err != nil {
		t.Fatal(err)
	}
	return dep.ID
}

func TestAnonymousPreviewSessionSurvivesSnapshotReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-plane-state.json")
	store, err := OpenStore(path, WithAnonymousPreview(true))
	if err != nil {
		t.Fatal(err)
	}
	depID := makePreviewDeploy(t, store, []byte("\x00asm-reload"))
	app, deploy, _, _, err := store.ResolveRunnable("preview-" + depID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.StartSession(app.ID, deploy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EndSession(session.ID); err != nil {
		t.Fatal(err)
	}

	reloaded, err := OpenStore(path, WithAnonymousPreview(true))
	if err != nil {
		t.Fatalf("reload after preview session: %v", err)
	}
	got, ok := reloaded.sessions[session.ID]
	if !ok || got.AppID != app.ID || got.DeployID != deploy.ID || got.EndedAt == nil {
		t.Fatalf("reloaded preview session = %+v, ok=%v", got, ok)
	}
}

func TestAnonymousPreviewGated(t *testing.T) {
	wasm := []byte("\x00asm-preview")

	// Disabled (default): preview handles do not resolve.
	off := NewStore()
	depID := makePreviewDeploy(t, off, wasm)
	if _, _, _, _, err := off.ResolveRunnable("preview-" + depID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("preview disabled: err = %v, want ErrNotFound", err)
	}

	// Enabled: an unclaimed deploy runs by id in an ownerless sandbox.
	on := NewStore(WithAnonymousPreview(true))
	depID = makePreviewDeploy(t, on, wasm)
	app, _, _, gotWASM, err := on.ResolveRunnable("preview-" + depID)
	if err != nil {
		t.Fatalf("preview enabled: %v", err)
	}
	if string(gotWASM) != string(wasm) {
		t.Fatalf("preview wasm = %q", gotWASM)
	}
	if app.OwnerID != "" {
		t.Fatalf("preview app should be ownerless, got OwnerID %q", app.OwnerID)
	}
	if !strings.HasPrefix(app.ID, "preview-") {
		t.Fatalf("preview app ID = %q, want preview- prefix", app.ID)
	}

	// A missing deploy still 404s.
	if _, _, _, _, err := on.ResolveRunnable("preview-nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing preview: err = %v, want ErrNotFound", err)
	}
}

func TestAnonymousPreviewStartsSession(t *testing.T) {
	store := NewStore(WithAnonymousPreview(true))
	depID := makePreviewDeploy(t, store, []byte("\x00asm-sess"))

	// Resolve mints the synthetic preview app id the gateway hands to StartSession.
	app, deploy, _, _, err := store.ResolveRunnable("preview-" + depID)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.StartSession(app.ID, deploy.ID)
	if err != nil {
		t.Fatalf("StartSession(preview): %v", err)
	}
	if sess.AppID != app.ID || sess.DeployID != depID {
		t.Fatalf("session = %+v, want AppID %q DeployID %q", sess, app.ID, depID)
	}
	if _, err := store.EndSession(sess.ID); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	// Gating still holds at accounting time: with preview off, the synthetic id
	// has no persisted app and must not start.
	off := NewStore()
	if _, err := off.StartSession(app.ID, deploy.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("preview disabled StartSession: err = %v, want ErrNotFound", err)
	}

	// A suspended deploy cannot start a preview session either.
	if err := store.SetDeploySuspended(depID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartSession(app.ID, deploy.ID); !errors.Is(err, ErrSuspended) {
		t.Fatalf("suspended StartSession: err = %v, want ErrSuspended", err)
	}
}

func TestAnonymousPreviewBlocksSuspended(t *testing.T) {
	store := NewStore(WithAnonymousPreview(true))
	depID := makePreviewDeploy(t, store, []byte("\x00asm-x"))
	if err := store.SetDeploySuspended(depID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := store.ResolveRunnable("preview-" + depID); !errors.Is(err, ErrSuspended) {
		t.Fatalf("suspended preview: err = %v, want ErrSuspended", err)
	}
}
