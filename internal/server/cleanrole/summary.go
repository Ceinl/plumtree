package cleanrole

import (
	"fmt"
	"io"
	"strings"

	serverconfig "github.com/Ceinl/plumtree/internal/server/config"
)

// writeReadySummary prints the compact operator readiness summary. Plain text
// is the information contract; NO_COLOR and redirected output read the same.
//
//	plumtree  ready
//
//	mode       development
//	version    dev
//	roles      control, gateway
//	ssh        127.0.0.1:2222
//	state      ./plumtree.db
//	config     ./config.json
//	host key   SHA256:...
//
//	hosted apps are unavailable until the runner role is enabled
//	next: plumtree bootstrap -handle HANDLE
func writeReadySummary(out io.Writer, cfg serverconfig.Config, version, configPath string, firstRun bool, sshAddr, fingerprint string) error {
	roles := summaryRoles(cfg)
	_, err := fmt.Fprintf(out, `plumtree  ready

mode       %s
version    %s
roles      %s
ssh        %s
state      %s
config     %s
host key   %s

%s
next: %s
`,
		summaryMode(cfg), version, roles, sshAddr, cfg.Storage.DatabasePath, configPath,
		fingerprint, strings.Join(unavailableProducts(cfg), "\n"), summaryNext(cfg, firstRun))
	return err
}

// writeRunnerReadySummary prints the same shape for the runner-only assembly,
// which owns neither the database nor the SSH listener.
func writeRunnerReadySummary(out io.Writer, cfg serverconfig.Config, version, configPath, runnerEndpoint string) error {
	cfg.Roles.Control = false
	cfg.Roles.Gateway = false
	cfg.Roles.Runner = true
	_, err := fmt.Fprintf(out, `plumtree  ready

mode       %s
version    %s
roles      %s
runner     %s

%s
next: %s
`,
		summaryMode(cfg), version, summaryRoles(cfg), runnerEndpoint,
		strings.Join(unavailableProducts(cfg), "\n"), summaryNext(cfg, false))
	return err
}

func summaryMode(cfg serverconfig.Config) string {
	if cfg.Runtime.Production {
		return "production"
	}
	return "development"
}

func summaryRoles(cfg serverconfig.Config) string {
	var roles []string
	for _, role := range []struct {
		enabled bool
		name    string
	}{{cfg.Roles.Control, "control"}, {cfg.Roles.Gateway, "gateway"}, {cfg.Roles.Runner, "runner"}} {
		if role.enabled {
			roles = append(roles, role.name)
		}
	}
	if len(roles) == 0 {
		return "-"
	}
	return strings.Join(roles, ", ")
}

func summaryNext(cfg serverconfig.Config, firstRun bool) string {
	if firstRun {
		return "plumtree bootstrap -handle HANDLE"
	}
	host := "127.0.0.1"
	port := "2222"
	if address := cfg.Exposure.SSH.Address; address != "" {
		if splitHost, splitPort, ok := splitAddress(address); ok {
			host, port = splitHost, splitPort
		}
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("ssh -p %s <owner>/<app>@%s to run an app", port, host)
}

func splitAddress(address string) (string, string, bool) {
	host, port, ok := strings.Cut(address, ":")
	if !ok || port == "" {
		return "", "", false
	}
	return strings.Trim(host, "[]"), port, true
}

// unavailableProducts lists product functions the selected assembly does not
// deliver, with the operator action that restores them. Normal profile
// selections belong here as status, not as startup warnings.
func unavailableProducts(cfg serverconfig.Config) []string {
	var lines []string
	if !cfg.Roles.Control && !cfg.Roles.Gateway && cfg.Runtime.RunnerEndpoint == "" {
		lines = append(lines, "hosted apps are unavailable until a control or gateway role is enabled")
		return lines
	}
	missing := missingRoleNames(cfg)
	switch {
	case !cfg.Roles.Control && cfg.Roles.Gateway && cfg.Roles.Runner:
		lines = append(lines, "device pairing and the control API are unavailable until the control role is enabled")
	case len(missing) > 0:
		plural := ""
		if len(missing) > 1 {
			plural = "s"
		}
		lines = append(lines, fmt.Sprintf("hosted apps are unavailable until the %s role%s %s enabled",
			strings.Join(missing, " and "), plural, isAre(missing)))
	}
	return lines
}

func missingRoleNames(cfg serverconfig.Config) []string {
	var missing []string
	for _, role := range []struct {
		enabled bool
		name    string
	}{{cfg.Roles.Control, "control"}, {cfg.Roles.Gateway, "gateway"}, {cfg.Roles.Runner, "runner"}} {
		if !role.enabled {
			missing = append(missing, role.name)
		}
	}
	return missing
}

func isAre(roles []string) string {
	if len(roles) == 1 {
		return "is"
	}
	return "are"
}
