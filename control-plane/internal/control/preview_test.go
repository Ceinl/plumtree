package control

import (
	"errors"
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
