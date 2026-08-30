package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/internal/runner"
)

func TestHostCommandsRequireOperatorOptInAndClaimedApp(t *testing.T) {
	backend := &countingBackend{}

	if caps := (&Server{Backend: backend}).capsFor("app-1", "owner-1"); caps.Exec != nil {
		t.Fatal("host commands available without operator opt-in")
	}

	server := &Server{Backend: backend, EnableHostCommands: true, HostCommandAllowlist: []string{"echo"}}
	if caps := server.capsFor("app-1", ""); caps.Exec != nil {
		t.Fatal("host commands available to an unclaimed preview app")
	}
	if caps := server.capsFor("app-1", "owner-1"); caps.Exec == nil {
		t.Fatal("host commands unavailable to claimed app after operator opt-in")
	}
	cmd, ok := server.capsFor("app-1", "owner-1").Exec.(runner.LocalCommander)
	if !ok {
		t.Fatalf("exec capability = %T, want runner.LocalCommander", server.capsFor("app-1", "owner-1").Exec)
	}
	if len(cmd.Allowlist) != 1 || cmd.Allowlist[0] != "echo" {
		t.Fatalf("allowlist = %q", cmd.Allowlist)
	}
}

// Enabling host commands without an allowlist must refuse startup entirely.
func TestStartRefusesHostCommandsWithEmptyAllowlist(t *testing.T) {
	s := &Server{Backend: &stubBackend{}, EnableHostCommands: true}
	if err := s.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "empty allowlist") {
		t.Fatalf("start err = %v, want empty-allowlist refusal", err)
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
}

func (b *capsBackend) SecretsForApp(string) (map[string]string, error) {
	return b.secrets, b.secretsErr
}
func (b *capsBackend) EgressAllowlist(string) ([]string, error) {
	return b.allow, b.egressErr
}

func withLog(t *testing.T) (*Server, *strings.Builder) {
	t.Helper()
	var log strings.Builder
	s := &Server{Backend: &capsBackend{}, Logf: func(format string, args ...any) {
		log.WriteString(fmt.Sprintf(format, args...) + "\n")
	}}
	return s, &log
}

// A control-plane failure must be indistinguishable from intentional config in
// effect (capability absent) but loud in the operator's log.
func TestCapsForFailsClosedWhenControlPlaneErrors(t *testing.T) {
	backend := &capsBackend{
		secrets:    map[string]string{"TOKEN": "t"},
		secretsErr: errors.New("control plane down"),
	}
	var log strings.Builder
	s := &Server{Backend: backend, Logf: func(format string, args ...any) {
		log.WriteString(fmt.Sprintf(format, args...) + "\n")
	}}

	caps := s.capsFor("app-1", "owner-1")
	if caps.Env != nil {
		t.Fatal("env granted despite a secrets lookup failure")
	}
	for _, want := range []string{"ERROR", "runs without env", "control plane down"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log missing %q:\n%s", want, log.String())
		}
	}

	backend.secretsErr = nil
	backend.egressErr = ErrCapsUnavailable
	log.Reset()
	caps = s.capsFor("app-1", "owner-1")
	if caps.Fetch != nil {
		t.Fatal("fetcher granted despite an egress lookup failure")
	}
	for _, want := range []string{"ERROR", "default-deny"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log missing %q:\n%s", want, log.String())
		}
	}
}

func TestCapsForNilSecretsYieldsEmptyEnv(t *testing.T) {
	s, _ := withLog(t)
	if caps := s.capsFor("app-1", "owner-1"); caps.Env != nil {
		t.Fatalf("env = %v, want nil for a claimed app with no secrets", caps.Env)
	}
}

// A configured allowlist that fails validation is rejected loudly and egress
// stays default-deny; a valid one wires the validated fetcher.
func TestCapsForValidatesEgressAllowlist(t *testing.T) {
	s, log := withLog(t)
	s.Backend.(*capsBackend).allow = []string{"api.example.com", "bad host.com"}
	if caps := s.capsFor("app-1", "owner-1"); caps.Fetch != nil {
		t.Fatal("fetcher built from an invalid allowlist")
	}
	for _, want := range []string{"ERROR", "rejected", "bad host.com"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log missing %q:\n%s", want, log.String())
		}
	}

	s.Backend.(*capsBackend).allow = []string{"api.example.com"}
	log.Reset()
	caps := s.capsFor("app-1", "owner-1")
	if caps.Fetch == nil {
		t.Fatal("valid allowlist did not wire a fetcher")
	}
	af, ok := caps.Fetch.(*runner.AllowlistFetcher)
	if !ok {
		t.Fatalf("unexpected fetcher %T", caps.Fetch)
	}
	if _, err := runner.NewValidatedAllowlistFetcher(s.Backend.(*capsBackend).allow); err != nil {
		t.Fatalf("allowlist should have validated: %v", err)
	}
	if len(af.Allow) != 1 || af.Allow[0] != "api.example.com" {
		t.Fatalf("fetcher allow = %q", af.Allow)
	}
}
