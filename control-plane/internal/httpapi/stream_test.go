package httpapi

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/control-plane/internal/auth/shoo"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

// nextStreamFrame reads SSE lines until it sees a "data:" event and decodes its
// app list, failing if none arrives before the deadline.
func nextStreamFrame(t *testing.T, r *bufio.Reader) []struct {
	Handle           string `json:"handle"`
	ConnectionsToday int    `json:"connectionsToday"`
} {
	t.Helper()
	type frame = struct {
		Handle           string `json:"handle"`
		ConnectionsToday int    `json:"connectionsToday"`
	}
	lines := make(chan string, 1)
	go func() {
		line, err := r.ReadString('\n')
		if err != nil {
			close(lines)
			return
		}
		lines <- line
	}()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("stream closed before a data frame arrived")
			}
			if data, found := strings.CutPrefix(line, "data: "); found {
				var body struct {
					Apps []frame `json:"apps"`
				}
				if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &body); err != nil {
					t.Fatalf("decode frame: %v", err)
				}
				return body.Apps
			}
			// keepalive/comment or blank separator line: read the next one.
			go func() {
				line, err := r.ReadString('\n')
				if err != nil {
					close(lines)
					return
				}
				lines <- line
			}()
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for stream frame")
		}
	}
}

func TestAppsStreamPushesOnSessionStart(t *testing.T) {
	store := control.NewStore()
	owner, _, err := store.EnsureOwnerForIdentity(control.IdentityInput{
		Provider: control.ProviderShoo,
		Subject:  "ps_test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimOwnerHandle(owner.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(control.AppInput{OwnerID: owner.ID, Name: "counter"})
	if err != nil {
		t.Fatal(err)
	}
	wasm := []byte("\x00asm-fake-module")
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

	server := New(store, fakeVerifier{claims: shoo.Claims{PairwiseSub: "ps_test"}}, "http://localhost:8080")
	srv := httptest.NewServer(server.Handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/apps/stream?access_token=test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	reader := bufio.NewReader(resp.Body)

	initial := nextStreamFrame(t, reader)
	if len(initial) != 1 || initial[0].ConnectionsToday != 0 {
		t.Fatalf("initial frame = %+v, want one app with 0 connections", initial)
	}

	if _, err := store.StartSession(app.ID, deploy.ID); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	updated := nextStreamFrame(t, reader)
	if len(updated) != 1 || updated[0].ConnectionsToday != 1 {
		t.Fatalf("pushed frame = %+v, want one app with 1 connection", updated)
	}
}
