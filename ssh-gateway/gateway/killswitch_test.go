package gateway

import (
	"context"
	"testing"
)

// track registers a cancellable session and reports whether it was cancelled.
func track(r *sessionRegistry, id, owner, app, deploy string) func() bool {
	ctx, cancel := context.WithCancel(context.Background())
	r.add(id, sessionEntry{ownerID: owner, appID: app, deployID: deploy, cancel: cancel})
	return func() bool { return ctx.Err() != nil }
}

func TestSessionRegistryKillsByScope(t *testing.T) {
	r := newSessionRegistry()
	// Two apps under owner o1, one under o2.
	a1 := track(r, "s1", "o1", "app1", "dep1")
	a2 := track(r, "s2", "o1", "app1", "dep2") // same app, different deploy
	b1 := track(r, "s3", "o1", "app2", "dep3")
	c1 := track(r, "s4", "o2", "app3", "dep4")

	if n := r.kill(KillDeploy, "dep1"); n != 1 {
		t.Fatalf("kill deploy dep1 cancelled %d, want 1", n)
	}
	if !a1() {
		t.Error("s1 (dep1) should be cancelled")
	}
	if a2() || b1() || c1() {
		t.Error("only dep1's session should be cancelled")
	}

	// app1 owns both s1 and s2 (different deploys). kill is idempotent, so it
	// matches and cancels both; s1 was already cancelled above.
	if n := r.kill(KillApp, "app1"); n != 2 {
		t.Fatalf("kill app app1 cancelled %d, want 2", n)
	}
	if !a2() {
		t.Error("s2 should be cancelled by the app-level kill")
	}
}

func TestSessionRegistryKillOwnerAndAll(t *testing.T) {
	r := newSessionRegistry()
	a := track(r, "s1", "o1", "app1", "dep1")
	b := track(r, "s2", "o1", "app2", "dep2")
	c := track(r, "s3", "o2", "app3", "dep3")

	if n := r.kill(KillOwner, "o1"); n != 2 {
		t.Fatalf("kill owner o1 cancelled %d, want 2", n)
	}
	if !a() || !b() {
		t.Error("both o1 sessions should be cancelled")
	}
	if c() {
		t.Error("o2 session should be untouched")
	}

	if n := r.killAll(); n != 3 {
		t.Fatalf("killAll cancelled %d, want 3", n)
	}
	if !c() {
		t.Error("killAll should cancel the o2 session too")
	}
}

func TestSessionRegistryRemoveStopsTracking(t *testing.T) {
	r := newSessionRegistry()
	done := track(r, "s1", "o1", "app1", "dep1")
	r.remove("s1")
	if n := r.kill(KillApp, "app1"); n != 0 {
		t.Fatalf("kill after remove cancelled %d, want 0", n)
	}
	if done() {
		t.Error("removed session should not have been cancelled")
	}
}
