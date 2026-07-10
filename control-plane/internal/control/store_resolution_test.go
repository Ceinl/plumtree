package control

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestKillSwitchSuspendsResolution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-plane-state.json")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := store.EnsureOwnerForIdentity(IdentityInput{Provider: ProviderShoo, Subject: "ps_kill"})
	if err != nil {
		t.Fatal(err)
	}
	if owner, err = store.ClaimOwnerHandle(owner.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	app, err := store.EnsureApp(AppInput{OwnerID: owner.ID, Name: "counter"})
	if err != nil {
		t.Fatal(err)
	}
	wasm := []byte("wasm bytes")
	artifact, err := store.CreateArtifact(ArtifactInput{Digest: digestBytes(wasm), SizeBytes: int64(len(wasm))})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutArtifactBytes(artifact.ID, wasm); err != nil {
		t.Fatal(err)
	}
	deploy, err := store.CreateDeploy(DeployInput{
		AppID:            app.ID,
		ArtifactID:       artifact.ID,
		SourceDigest:     sourceDigest,
		CreatedByOwnerID: owner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateDeploy(app.ID, deploy.ID); err != nil {
		t.Fatal(err)
	}

	handle := owner.Handle + "/counter"
	events := make(chan SuspensionEvent, 3)
	unregister := store.RegisterSuspensionListener(func(event SuspensionEvent) error {
		events <- event
		return nil
	})
	defer unregister()
	wantEvent := func(scope SuspensionScope, id string) {
		t.Helper()
		if event := <-events; event.Scope != scope || event.ID != id {
			t.Fatalf("suspension event = %+v, want scope=%q id=%q", event, scope, id)
		}
	}
	mustResolve := func(when string) {
		if _, _, _, _, err := store.ResolveRunnable(handle); err != nil {
			t.Fatalf("%s: ResolveRunnable err = %v, want nil", when, err)
		}
	}
	mustSuspended := func(when string) {
		if _, _, _, _, err := store.ResolveRunnable(handle); !errors.Is(err, ErrSuspended) {
			t.Fatalf("%s: ResolveRunnable err = %v, want ErrSuspended", when, err)
		}
	}

	mustResolve("initial")
	if _, err := store.SetAppSuspended(app.ID, true); err != nil {
		t.Fatal(err)
	}
	wantEvent(SuspensionApp, app.ID)
	mustSuspended("app suspended")
	if _, err := store.SetAppSuspended(app.ID, false); err != nil {
		t.Fatal(err)
	}
	mustResolve("app unsuspended")

	if _, err := store.SetOwnerSuspended(owner.ID, true); err != nil {
		t.Fatal(err)
	}
	wantEvent(SuspensionOwner, owner.ID)
	mustSuspended("owner suspended")
	if _, err := store.SetOwnerSuspended(owner.ID, false); err != nil {
		t.Fatal(err)
	}
	mustResolve("owner unsuspended")

	if err := store.SetDeploySuspended(deploy.ID, true); err != nil {
		t.Fatal(err)
	}
	wantEvent(SuspensionDeploy, deploy.ID)
	mustSuspended("deploy suspended")

	reloaded, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := reloaded.ResolveRunnable(handle); !errors.Is(err, ErrSuspended) {
		t.Fatalf("after reload: ResolveRunnable err = %v, want ErrSuspended", err)
	}
	if err := reloaded.SetDeploySuspended(deploy.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := reloaded.ResolveRunnable(handle); err != nil {
		t.Fatalf("after deploy unsuspended: ResolveRunnable err = %v, want nil", err)
	}

	if _, err := store.SetAppSuspended("app_nope", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetAppSuspended unknown err = %v, want ErrNotFound", err)
	}
	if err := store.SetDeploySuspended("dep_nope", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetDeploySuspended unknown err = %v, want ErrNotFound", err)
	}
}

func TestResolveRunnableReturnsArtifactBytes(t *testing.T) {
	store := NewStore()
	owner, err := store.EnsureOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.EnsureApp(AppInput{OwnerID: owner.ID, Name: "counter"})
	if err != nil {
		t.Fatal(err)
	}
	wasm := []byte("wasm bytes")
	artifact, err := store.CreateArtifact(ArtifactInput{
		Digest:     digestBytes(wasm),
		SizeBytes:  int64(len(wasm)),
		ABIVersion: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutArtifactBytes(artifact.ID, wasm); err != nil {
		t.Fatal(err)
	}
	deploy, err := store.CreateDeploy(DeployInput{
		AppID:            app.ID,
		ArtifactID:       artifact.ID,
		SourceDigest:     sourceDigest,
		CreatedByOwnerID: owner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateDeploy(app.ID, deploy.ID); err != nil {
		t.Fatal(err)
	}

	_, gotDeploy, _, gotWASM, err := store.ResolveRunnable("counter")
	if err != nil {
		t.Fatal(err)
	}
	if gotDeploy.ID != deploy.ID || string(gotWASM) != string(wasm) {
		t.Fatalf("deploy=%+v wasm=%q", gotDeploy, gotWASM)
	}
	gotWASM[0] = 'x'
	_, _, _, again, err := store.ResolveRunnable("alice/counter")
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(wasm) {
		t.Fatalf("wasm bytes leaked through return: %q", again)
	}
}
