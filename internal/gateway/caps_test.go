package gateway

import "testing"

func TestHostCommandsRequireOperatorOptInAndClaimedApp(t *testing.T) {
	backend := &countingBackend{}

	if caps := (&Server{Backend: backend}).capsFor("app-1", "owner-1"); caps.Exec != nil {
		t.Fatal("host commands available without operator opt-in")
	}

	server := &Server{Backend: backend, AllowHostCommands: true}
	if caps := server.capsFor("app-1", ""); caps.Exec != nil {
		t.Fatal("host commands available to an unclaimed preview app")
	}
	if caps := server.capsFor("app-1", "owner-1"); caps.Exec == nil {
		t.Fatal("host commands unavailable to claimed app after operator opt-in")
	}
}
