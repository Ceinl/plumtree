package cleanrole

import (
	"bytes"
	"testing"

	serverconfig "github.com/Ceinl/plumtree/internal/server/config"
)

func summaryConfig() serverconfig.Config {
	c := serverconfig.Default()
	c.Storage.DatabasePath = "plumtree.db"
	return c
}

func TestReadySummaryControlOnlyAssembly(t *testing.T) {
	cfg := serverconfig.Default()
	cfg.Roles.Gateway = false
	cfg.Roles.Runner = false
	out := &bytes.Buffer{}
	if err := writeReadySummary(out, cfg, "1.2.3", "config.json", true, "127.0.0.1:2222", "SHA256:fp"); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"plumtree  ready", "mode       development", "version    1.2.3",
		"roles      control", "ssh        127.0.0.1:2222", "state      plumtree.db",
		"config     config.json", "host key   SHA256:fp",
		"hosted apps are unavailable until the gateway and runner roles are enabled",
		"next: plumtree bootstrap -handle HANDLE",
	} {
		if !containsLine(text, want) {
			t.Fatalf("summary missing %q:\n%s", want, text)
		}
	}
}

func TestReadySummaryExistingServerRunsApps(t *testing.T) {
	cfg := summaryConfig()
	out := &bytes.Buffer{}
	if err := writeReadySummary(out, cfg, "dev", "config.json", false, "127.0.0.1:2222", "SHA256:fp"); err != nil {
		t.Fatal(err)
	}
	if containsLine(out.String(), "hosted apps are unavailable") {
		t.Fatalf("fully composed assembly should not claim broken functions:\n%s", out.String())
	}
	if !containsLine(out.String(), "next: ssh -p 2222 <owner>/<app>@127.0.0.1 to run an app") {
		t.Fatalf("next action missing:\n%s", out.String())
	}
}

func TestReadySummaryProductionModeAndNoSecrets(t *testing.T) {
	cfg := summaryConfig()
	cfg.Runtime.Production = true
	cfg.Secrets.DatabaseKeyFile = "/srv/secrets/database.key"
	out := &bytes.Buffer{}
	if err := writeReadySummary(out, cfg, "1.0.0", "config.json", false, "0.0.0.0:443", "SHA256:fp"); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !containsLine(text, "mode       production") {
		t.Fatalf("mode missing:\n%s", text)
	}
	if containsLine(text, "database.key") || containsLine(text, "/srv/secrets") {
		t.Fatalf("summary leaked secret material:\n%s", text)
	}
}

func TestReadySummaryRunnerOnlyAssembly(t *testing.T) {
	cfg := summaryConfig()
	cfg.Roles.Control = false
	cfg.Roles.Gateway = false
	cfg.Roles.Runner = true
	cfg.Runtime.RunnerEndpoint = "unix:///run/plumtree/runner.sock"
	out := &bytes.Buffer{}
	if err := writeRunnerReadySummary(out, cfg, "dev", "config.json", cfg.Runtime.RunnerEndpoint); err != nil {
		t.Fatal(err)
	}
	if !containsLine(out.String(), "runner     unix:///run/plumtree/runner.sock") {
		t.Fatalf("runner endpoint missing:\n%s", out.String())
	}
}

func containsLine(text, line string) bool {
	for _, actual := range bytes.Split([]byte(text), []byte("\n")) {
		if string(actual) == line {
			return true
		}
	}
	return false
}
