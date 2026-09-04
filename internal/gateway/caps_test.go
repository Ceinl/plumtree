package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/internal/runner"
)

// assembleWith wires a server's operator options and capability sources into
// the hosted assembler so tests drive the same path session setup uses.
func assembleWith(s *Server, app Runnable, identity runner.Identity) (runner.Capabilities, error) {
	return AssembleHostCapabilities(context.Background(), app, identity, s.hostCapabilityOptions(), s.hostCapabilitySources())
}

func TestHostCommandsRequireOperatorOptInAndClaimedApp(t *testing.T) {
	backend := &capsBackend{}
	app := Runnable{AppID: "app-1", OwnerID: "owner-1"}
	identity := runner.Identity{User: "key", Kind: runner.IdentitySSHKey, Authenticated: true}

	plain := mustNewServer(t, Config{Backend: backend})
	if caps, _ := assembleWith(plain, app, identity); caps.Exec != nil {
		t.Fatal("host commands available without operator opt-in")
	}

	server := mustNewServer(t, Config{Backend: backend, EnableHostCommands: true, HostCommandAllowlist: []string{"echo"}})
	if caps, _ := assembleWith(server, Runnable{AppID: "app-1"}, identity); caps.Exec != nil {
		t.Fatal("host commands available to an unclaimed preview app")
	}
	caps, err := assembleWith(server, app, identity)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if caps.Exec == nil {
		t.Fatal("host commands unavailable to claimed app after operator opt-in")
	}
	cmd, ok := caps.Exec.(runner.LocalCommander)
	if !ok {
		t.Fatalf("exec capability = %T, want runner.LocalCommander", caps.Exec)
	}
	if len(cmd.Allowlist) != 1 || cmd.Allowlist[0] != "echo" {
		t.Fatalf("allowlist = %q", cmd.Allowlist)
	}
}

// Enabling host commands without an allowlist fails during construction.
func TestNewRefusesHostCommandsWithEmptyAllowlist(t *testing.T) {
	backend := &stubBackend{}
	if _, err := New(Config{Backend: backend, Suspensions: backend, EnableHostCommands: true}); err == nil || !strings.Contains(err.Error(), "empty allowlist") {
		t.Fatalf("New err = %v, want empty-allowlist refusal", err)
	}
}

// capsBackend drives the capability sources independently of session
// accounting; it embeds stubBackend for everything else.
type capsBackend struct {
	stubBackend
	secrets    map[string]string
	secretsErr error
	allow      []string
	egressErr  error
	kvErr      error
}

func (b *capsBackend) KVStore(context.Context, string) (runner.Store, error) {
	if b.kvErr != nil {
		return nil, b.kvErr
	}
	return runner.NewMemStore(0, 0), nil
}

func (b *capsBackend) StartSuspensionWatcher(context.Context, func(context.Context, Suspension) error) error {
	return nil
}

func (b *capsBackend) SecretsForApp(context.Context, string) (map[string]string, error) {
	return b.secrets, b.secretsErr
}
func (b *capsBackend) EgressAllowlist(context.Context, string) ([]string, error) {
	return b.allow, b.egressErr
}

// A capability-source failure fails closed (capability absent) and is reported
// as an error, never hidden as intentional absence.
func TestAssembleHostCapabilitiesFailsClosedWhenControlPlaneErrors(t *testing.T) {
	backend := &capsBackend{
		secrets:    map[string]string{"TOKEN": "t"},
		secretsErr: errors.New("control plane down"),
	}
	s := mustNewServer(t, Config{Backend: backend})
	app := Runnable{AppID: "app-1", OwnerID: "owner-1"}

	caps, err := assembleWith(s, app, runner.Identity{})
	if caps.Env != nil {
		t.Fatal("env granted despite a secrets lookup failure")
	}
	if err == nil {
		t.Fatal("secrets source failure returned no error")
	}
	for _, want := range []string{"runs without env", "control plane down"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}

	backend.secretsErr = nil
	backend.egressErr = ErrCapsUnavailable
	caps, err = assembleWith(s, app, runner.Identity{})
	if caps.Fetch != nil {
		t.Fatal("fetcher granted despite an egress lookup failure")
	}
	if err == nil {
		t.Fatal("egress source failure returned no error")
	}
	if !errors.Is(err, ErrCapsUnavailable) {
		t.Fatalf("error = %v, want ErrCapsUnavailable", err)
	}
	if !strings.Contains(err.Error(), "default-deny") {
		t.Errorf("error missing default-deny: %v", err)
	}
}

func TestAssembleHostCapabilitiesNilSecretsYieldsNoEnv(t *testing.T) {
	s := mustNewServer(t, Config{Backend: &capsBackend{}})
	caps, err := assembleWith(s, Runnable{AppID: "app-1", OwnerID: "owner-1"}, runner.Identity{})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if caps.Env != nil {
		t.Fatalf("env = %v, want nil for a paired app with no secrets", caps.Env)
	}
}

// A configured allowlist that fails validation is rejected and egress stays
// default-deny; a valid one wires the validated fetcher.
func TestAssembleHostCapabilitiesValidatesEgressAllowlist(t *testing.T) {
	backend := &capsBackend{allow: []string{"api.example.com", "bad host.com"}}
	s := mustNewServer(t, Config{Backend: backend})
	app := Runnable{AppID: "app-1", OwnerID: "owner-1"}

	caps, err := assembleWith(s, app, runner.Identity{})
	if caps.Fetch != nil {
		t.Fatal("fetcher built from an invalid allowlist")
	}
	if err == nil {
		t.Fatal("invalid allowlist returned no error")
	}
	for _, want := range []string{"rejected", "bad host.com"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}

	backend.allow = []string{"api.example.com"}
	caps, err = assembleWith(s, app, runner.Identity{})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if caps.Fetch == nil {
		t.Fatal("valid allowlist did not wire a fetcher")
	}
	af, ok := caps.Fetch.(*runner.AllowlistFetcher)
	if !ok {
		t.Fatalf("unexpected fetcher %T", caps.Fetch)
	}
	if len(af.Allow) != 1 || af.Allow[0] != "api.example.com" {
		t.Fatalf("fetcher allow = %q", af.Allow)
	}
}

// Claimed-only policy: unclaimed apps get no Env, Fetch, or Exec even when the
// sources are configured, and that absence is intentional (no error). Claimed
// apps with the same sources get all three.
func TestAssembleHostCapabilitiesClaimedOnly(t *testing.T) {
	backend := &capsBackend{
		secrets: map[string]string{"TOKEN": "t"},
		allow:   []string{"api.example.com"},
	}
	s := mustNewServer(t, Config{Backend: backend, EnableHostCommands: true, HostCommandAllowlist: []string{"echo"}})
	identity := runner.Identity{User: "anon", Kind: runner.IdentityAnonymous}

	unclaimed, err := assembleWith(s, Runnable{AppID: "app-1"}, identity)
	if err != nil {
		t.Fatalf("unclaimed assemble: %v", err)
	}
	if unclaimed.Env != nil || unclaimed.Fetch != nil || unclaimed.Exec != nil {
		t.Fatalf("unclaimed app got claimed capabilities: env=%v fetch=%v exec=%v",
			unclaimed.Env, unclaimed.Fetch, unclaimed.Exec)
	}

	claimed, err := assembleWith(s, Runnable{AppID: "app-1", OwnerID: "owner-1"}, identity)
	if err != nil {
		t.Fatalf("claimed assemble: %v", err)
	}
	if claimed.Env == nil {
		t.Fatal("claimed app missing env")
	}
	if claimed.Fetch == nil {
		t.Fatal("claimed app missing fetch")
	}
	if claimed.Exec == nil {
		t.Fatal("claimed app missing exec")
	}
}

// Default-deny egress: an empty allowlist wires no Fetcher without an error.
func TestAssembleHostCapabilitiesDefaultDenyWithoutAllowlist(t *testing.T) {
	s := mustNewServer(t, Config{Backend: &capsBackend{}})
	caps, err := assembleWith(s, Runnable{AppID: "app-1", OwnerID: "owner-1"}, runner.Identity{})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if caps.Fetch != nil {
		t.Fatalf("fetch = %v, want nil with no allowlist", caps.Fetch)
	}
}

// Every session gets its identity and a fresh goodbye string from the single
// assembly call; gateway code must not assign Auth afterwards.
func TestAssembleHostCapabilitiesSetsAuthAndGoodbye(t *testing.T) {
	s := mustNewServer(t, Config{Backend: &capsBackend{}})
	identity := runner.Identity{User: "SHA256:abc", Kind: runner.IdentitySSHKey, Authenticated: true, OwnsApp: true}
	app := Runnable{AppID: "app-1", OwnerID: "owner-1"}

	first, err := assembleWith(s, app, identity)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	auth, ok := first.Auth.(runner.StaticAuth)
	if !ok {
		t.Fatalf("auth = %T, want runner.StaticAuth", first.Auth)
	}
	if auth.Identity != identity {
		t.Fatalf("identity = %+v, want %+v", auth.Identity, identity)
	}
	if first.Goodbye == nil {
		t.Fatal("goodbye is nil")
	}

	second, err := assembleWith(s, app, identity)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if second.Goodbye == nil || second.Goodbye == first.Goodbye {
		t.Fatal("goodbye strings must be freshly allocated per session")
	}
}

// Bus reuse: sessions of the same app share one bus instance through the
// assembler.
func TestAssembleHostCapabilitiesReusesBus(t *testing.T) {
	s := mustNewServer(t, Config{Backend: &capsBackend{}})
	app := Runnable{AppID: "app-1", OwnerID: "owner-1"}

	first, err := assembleWith(s, app, runner.Identity{})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	second, err := assembleWith(s, app, runner.Identity{})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if first.Bus == nil || second.Bus == nil {
		t.Fatal("bus is nil")
	}
	if first.Bus != second.Bus {
		t.Fatal("same app got different bus instances")
	}
}

func TestAssembleHostCapabilitiesRequiresAppID(t *testing.T) {
	s := mustNewServer(t, Config{Backend: &capsBackend{}})
	identity := runner.Identity{User: "local"}
	caps, err := assembleWith(s, Runnable{}, identity)
	if err == nil {
		t.Fatal("empty app ID returned no error")
	}
	auth, ok := caps.Auth.(runner.StaticAuth)
	if !ok || auth.Identity != identity {
		t.Fatalf("auth = %+v, want identity %+v", caps.Auth, identity)
	}
	if caps.Goodbye == nil {
		t.Fatal("goodbye is nil")
	}
}

// A KV source failure fails closed with an error; tested here through the
// assembler without a repository.
func TestAssembleHostCapabilitiesKVFailureFailsClosed(t *testing.T) {
	s := mustNewServer(t, Config{Backend: &capsBackend{}})
	src := s.hostCapabilitySources()
	src.KV = func(context.Context, string) (runner.Store, error) { return nil, errors.New("disk gone") }

	caps, err := AssembleHostCapabilities(
		context.Background(),
		Runnable{AppID: "app-1", OwnerID: "owner-1"}, runner.Identity{},
		s.hostCapabilityOptions(), src)
	if caps.KV != nil {
		t.Fatalf("KV = %T, want nil on source failure", caps.KV)
	}
	if err == nil || !strings.Contains(err.Error(), "without kv") {
		t.Fatalf("error = %v, want kv failure", err)
	}
}

func TestAssembleHostCapabilitiesReportsMissingSources(t *testing.T) {
	caps, err := AssembleHostCapabilities(
		context.Background(),
		Runnable{AppID: "app-1", OwnerID: "owner-1"},
		runner.Identity{},
		HostCapabilityOptions{},
		HostCapabilitySources{},
	)
	if caps.KV != nil || caps.Bus != nil || caps.Env != nil || caps.Fetch != nil {
		t.Fatalf("capabilities unexpectedly present with missing sources: %+v", caps)
	}
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("error = %v, want ErrNotConfigured", err)
	}
	for _, want := range []string{"bus", "KV", "secrets", "egress"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}
