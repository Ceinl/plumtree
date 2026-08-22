package config

import (
	"fmt"
	"strings"
	"unicode"
)

var persistableFields = []string{
	"storage.databasePath", "storage.kvRoot", "storage.sshIdentity",
	"limits.maxSessions", "limits.maxConnections", "limits.maxConnectionsPerIP", "limits.maxFPS",
	"limits.maxApps", "limits.maxDeployments", "limits.maxSessionsPerAppDay", "limits.maxDeploysPerHour",
	"limits.maxConcurrentBuilds", "limits.maxQueuedBuilds", "limits.rateLimitPerSec", "limits.rateBurst",
	"limits.maxEventsPerSec", "limits.maxFramesPerSec", "limits.memoryPages", "limits.sessionTimeout",
	"limits.handshakeTimeout", "limits.idleTimeout", "limits.frameTimeout",
	"exposure.http.enabled", "exposure.http.address", "exposure.ssh.enabled", "exposure.ssh.address",
	"exposure.gateway.enabled", "exposure.gateway.address", "resources.autoCapacity",
	"roles.control", "roles.gateway", "roles.runner", "secrets.databaseKeyFile",
	"secrets.gatewayTokenFile", "secrets.runnerTokenFile", "runtime.production",
	"runtime.acknowledgeUnlimitedLimits", "runtime.shutdownTimeout", "runtime.runnerEndpoint",
	"runtime.runnerWorker", "runtime.runnerScratchRoot", "runtime.workerUIDBase",
}

// FieldNames returns the fixed schema paths accepted by set, unset, flags, and
// environment overrides.
func FieldNames() []string { return append([]string(nil), persistableFields...) }

// FlagName and EnvironmentName expose the mechanical operator spelling for a
// persistent schema path.
func FlagName(field string) string        { return flagKey(field) }
func EnvironmentName(field string) string { return envKey(field) }

// ApplyOverrides applies the same typed settings from caller-provided flag
// and environment maps. The maps make this mechanical layer testable and keep
// the package from mutating process-global argv or environment state.
func ApplyOverrides(c Config, environment, flags map[string]string) (Config, map[string]Provenance, error) {
	provenance := make(map[string]Provenance, len(persistableFields))
	for _, field := range persistableFields {
		value, source, ok := overrideValue(field, environment, flags)
		if !ok {
			provenance[field] = SourceConfig
			continue
		}
		next, err := c.Set(field, value)
		if err != nil {
			return c, nil, fmt.Errorf("%s override: %w", field, err)
		}
		c = next
		provenance[field] = source
	}
	return c, provenance, nil
}

func overrideValue(field string, environment, flags map[string]string) (string, Provenance, bool) {
	if value, ok := lookup(flags, field, flagKey(field)); ok {
		return value, SourceFlag, true
	}
	if value, ok := lookup(environment, field, envKey(field)); ok {
		return value, SourceEnvironment, true
	}
	return "", "", false
}

func lookup(values map[string]string, field, alias string) (string, bool) {
	if values == nil {
		return "", false
	}
	if value, ok := values[field]; ok {
		return value, true
	}
	value, ok := values[alias]
	return value, ok
}

func flagKey(field string) string {
	return camelToKebab(strings.ReplaceAll(field, ".", "-"))
}

func envKey(field string) string {
	parts := strings.Split(field, ".")
	for i, part := range parts {
		parts[i] = camelToSnake(part)
	}
	return "PLUMTREE_" + strings.ToUpper(strings.Join(parts, "_"))
}

func camelToSnake(value string) string {
	var out []rune
	for i, r := range value {
		if unicode.IsUpper(r) && i > 0 {
			out = append(out, '_')
		}
		out = append(out, unicode.ToLower(r))
	}
	return string(out)
}

func camelToKebab(value string) string {
	return strings.ReplaceAll(camelToSnake(value), "_", "-")
}
