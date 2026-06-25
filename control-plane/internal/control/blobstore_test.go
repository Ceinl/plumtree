package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeArtifact creates an app and an artifact whose bytes are wasm, returning the
// artifact ID.
func makeArtifact(t *testing.T, store *Store, wasm []byte) string {
	t.Helper()
	owner, err := store.CreateOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(AppInput{OwnerID: owner.ID, Name: "counter"}); err != nil {
		t.Fatal(err)
	}
	art, err := store.CreateArtifact(ArtifactInput{Digest: digestBytes(wasm), SizeBytes: int64(len(wasm))})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutArtifactBytes(art.ID, wasm); err != nil {
		t.Fatal(err)
	}
	return art.ID
}

func TestFSBlobStoreKeepsArtifactsOnDisk(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	blobDir := filepath.Join(dir, "artifacts")
	wasm := []byte("\x00asm-pretend-binary-bytes")

	store, err := OpenStore(stateFile, WithBlobDir(blobDir))
	if err != nil {
		t.Fatal(err)
	}
	artID := makeArtifact(t, store, wasm)

	// The bytes live on disk under blobDir, not in the JSON state file.
	if _, err := os.Stat(filepath.Join(blobDir, artID+".wasm")); err != nil {
		t.Fatalf("artifact file missing: %v", err)
	}
	state, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state), "pretend-binary") {
		t.Fatal("state file embedded the artifact bytes; durable storage should keep them on disk")
	}

	// Reopening the store reads the artifact back from disk.
	reopened, err := OpenStore(stateFile, WithBlobDir(blobDir))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reopened.blobs.Get(artID); !ok || string(got) != string(wasm) {
		t.Fatalf("artifact not durable across reopen: %q ok=%v", got, ok)
	}
}

func TestMemBlobStoreEmbedsInSnapshot(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	wasm := []byte("\x00asm-inline-bytes")

	store, err := OpenStore(stateFile) // default: in-memory blob store
	if err != nil {
		t.Fatal(err)
	}
	artID := makeArtifact(t, store, wasm)

	// Without a blob dir, the bytes are embedded (base64) in the state file...
	state, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), "blobs") {
		t.Fatal("in-memory blob store should embed artifacts in the state file")
	}

	// ...and survive a reopen from that file alone.
	reopened, err := OpenStore(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reopened.blobs.Get(artID); !ok || string(got) != string(wasm) {
		t.Fatalf("inline artifact not restored: %q ok=%v", got, ok)
	}
}
