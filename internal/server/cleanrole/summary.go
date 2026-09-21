package cleanrole

import (
	"fmt"
	"io"
	"strings"

	serverconfig "github.com/Ceinl/plumtree/internal/server/config"
)

// field is one label/value line of the readiness summary.
type field struct{ key, value string }

// writeReadySummary prints the compact operator readiness summary. Plain text
// is the information contract; NO_COLOR and redirected output read the same.
//
//	plumtree  ready
//
//	mode       development
//	version    dev
//	roles      control, gateway
//	http       disabled
//	gateway    disabled
//	capacity   adaptive
//	ssh        127.0.0.1:2222
//	state      ./plumtree.db
//	config     ./config.json
//	host key   SHA256:...
//
//	hosted apps are unavailable until the runner role is enabled
//	next: plumtree bootstrap -handle HANDLE
func writeReadySummary(out io.Writer, cfg serverconfig.Config, version, configPath string, firstRun bool, sshAddr, fingerprint string) error {
	fields := []field{
		{"mode", summaryMode(cfg)},
		{"version", version},
		{"roles", summaryRoles(cfg)},
		{"http", summaryGate(cfg.Exposure.HTTP)},
		{"gateway", summaryGate(cfg.Exposure.Gateway)},
		{"capacity", summaryCapacity(cfg)},
		{"ssh", sshAddr},
		{"state", cfg.Storage.DatabasePath},
		{"config", configPath},
		{"host key", fingerprint},
	}
	return writeSummary(out, fields, unavailableProducts(cfg), summaryNext(cfg, firstRun))
}

// writeRunnerReadySummary prints the same shape for the runner-only assembly,
// which owns neither the database, the SSH listener, nor the public exposures.
func writeRunnerReadySummary(out io.Writer, cfg serverconfig.Config, version, configPath, runnerEndpoint string) error {
	fields := []field{
		{"mode", summaryMode(cfg)},
		{"version", version},
		{"roles", "runner"},
		{"runner", runnerEndpoint},
		{"capacity", summaryCapacity(cfg)},
		{"config", configPath},
	}
	notes := []string{"hosted apps run on paired devices; pairing and the control API are unavailable here"}
	return writeSummary(out, fields, notes,
		"point a composed server's runner at this endpoint (runtime.runnerEndpoint)")
}

func writeSummary(out io.Writer, fields []field, notes []string, next string) error {
	var body strings.Builder
	_, _ = fmt.Fprintln(&body, "plumtree  ready")
	_, _ = fmt.Fprintln(&body)
	for _, f := range fields {
		_, _ = fmt.Fprintf(&body, "%-9s  %s\n", f.key, f.value)
	}
	_, _ = fmt.Fprintln(&body)
	for _, note := range notes {
		_, _ = fmt.Fprintln(&body, note)
	}
	_, _ = fmt.Fprintf(&body, "next: %s\n", next)
	_, err := fmt.Fprint(out, body.String())
	return err
}

func summaryMode(cfg serverconfig.Config) string {
	if cfg.Runtime.Production {
		return "production"
	}
	return "development"
}

// roleTable lists the three operational roles in the order the summary and
// unavailable-product checks present them.
func roleTable(cfg serverconfig.Config) []struct {
	enabled bool
	name    string
} {
	return []struct {
		enabled bool
		name    string
	}{{cfg.Roles.Control, "control"}, {cfg.Roles.Gateway, "gateway"}, {cfg.Roles.Runner, "runner"}}
}

func summaryRoles(cfg serverconfig.Config) string {
	var roles []string
	for _, role := range roleTable(cfg) {
		if role.enabled {
			roles = append(roles, role.name)
		}
	}
	if len(roles) == 0 {
		return "-"
	}
	return strings.Join(roles, ", ")
}

// summaryGate renders an exposure gate as its address when exposed or a plain
// "disabled", so a disabled product reads as status, not a warning.
func summaryGate(gate serverconfig.ExposureGate) string {
	if !gate.Enabled {
		return "disabled"
	}
	return gate.Address
}

// summaryCapacity reports adaptive capacity as status: normal profile
// selections belong in the summary, not in startup warnings.
func summaryCapacity(cfg serverconfig.Config) string {
	if cfg.Resources.AutoCapacity && cfg.Resources.MemoryLimitBytes == 0 {
		return "adaptive (materialized at startup)"
	}
	return fmt.Sprintf("%d MB limit", cfg.Resources.MemoryLimitBytes/(1<<20))
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
	for _, role := range roleTable(cfg) {
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
