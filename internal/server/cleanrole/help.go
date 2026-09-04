package cleanrole

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

const rootHelp = `plumtree — Plumtree server and operator CLI

Usage:
  plumtree [serve] [--config PATH] [--field-name VALUE]...
  plumtree bootstrap [-config PATH | -database PATH] -handle HANDLE [-device NAME] [-ttl 10m]
  plumtree config show|set|unset ...
  plumtree state inventory|backup|restore ...
  plumtree suspend deploy <id> [-config PATH | -database PATH]
  plumtree unsuspend deploy <id> [-config PATH | -database PATH]
  plumtree quota set <authorID> <maxApps> <maxDeploymentsPerApp> <maxSecretsPerApp> <maxSessions> [-config PATH | -database PATH]

Run "plumtree help COMMAND" for command details.
`

const serveHelp = `Usage:
  plumtree [serve] [--config PATH] [--field-name VALUE]...

Starts the server from the resolved configuration. The serve keyword is
optional; bare flags also start the server.

Flags:
  --config PATH               typed config file path (or PLUMTREE_CONFIG)
  --product-version VERSION   exact product version (or PLUMTREE_PRODUCT_VERSION)
  --server-id ID              stable server identity (or PLUMTREE_SERVER_ID)
  --host-key PATH             alias for storage.sshIdentity (or PLUMTREE_HOST_KEY)
  --host-command-allowlist V  alias for runtime.hostCommandAllowlist
  --<field-name> VALUE        one-run override for any plumtree config field,
                              e.g. --limits-max-sessions 200

Every config field also has a PLUMTREE_* environment form. Precedence is flag,
environment, persisted configuration, then typed default. The first serve
creates the strict configuration; config changes take effect after restart.
`

const bootstrapHelp = `Usage:
  plumtree bootstrap [-config PATH | -database PATH] -handle HANDLE [-device NAME] [-ttl 10m]

Creates a one-use, time-bounded first-author authority and prints a human
summary with the ID, the secret, and the next pairing command. The secret is
shown once; only its verifier is stored. Pass --json for stable output.

Flags:
  --config PATH    typed config file path (uses its database and key)
  --database PATH  SQLite database path (default plumtree.db, without --config)
  --handle HANDLE  author handle bound to this authority (required)
  --device NAME    first device name (default device)
  --ttl DURATION   authority lifetime between 1m and 1h (default 10m)

The legacy spelling "plumtree author bootstrap" is accepted with the same flags.
`

const configHelp = `Usage:
  plumtree config show [--config PATH]
  plumtree config set [--config PATH] <field> <value>
  plumtree config unset [--config PATH] <field>

Inspects and edits the persisted typed configuration. Changes take effect
after restart. --config selects the file (or PLUMTREE_CONFIG); otherwise the
platform config path is used. Run "plumtree config show" to list fields.
`

const stateHelp = `Usage:
  plumtree state inventory [-config PATH]
  plumtree state backup [-config PATH] -output PATH
  plumtree state restore [-config PATH] -input PATH -yes

Inventory reports the database, KV root, and host key paths; backup writes a
bundle containing all three; restore replaces them from a bundle. Restore is
destructive and requires -yes. --config selects the file (or PLUMTREE_CONFIG).
`

const suspendHelp = `Usage:
  plumtree suspend deploy <id> [-config PATH | -database PATH]

Suspends one deployment. The gateway suspension watcher applies the change
immediately. Uses --config (or PLUMTREE_CONFIG) or a direct --database path,
but not both.
`

const unsuspendHelp = `Usage:
  plumtree unsuspend deploy <id> [-config PATH | -database PATH]

Lifts the suspension on one deployment. The gateway suspension watcher applies
the change immediately. Uses --config (or PLUMTREE_CONFIG) or a direct
--database path, but not both.
`

const quotaHelp = `Usage:
  plumtree quota set <authorID> <maxApps> <maxDeploymentsPerApp> <maxSecretsPerApp> <maxSessions> [-config PATH | -database PATH]

Sets one author's resource quotas in a single write. All four values must be
non-negative integers. Uses --config (or PLUMTREE_CONFIG) or a direct
--database path, but not both.
`

var commandHelp = map[string]string{
	"serve":     serveHelp,
	"bootstrap": bootstrapHelp,
	"config":    configHelp,
	"state":     stateHelp,
	"suspend":   suspendHelp,
	"unsuspend": unsuspendHelp,
	"quota":     quotaHelp,
}

func isHelp(value string) bool { return value == "-h" || value == "--help" }

func isKnownTopCommand(value string) bool {
	_, known := commandHelp[value]
	return known || value == "author"
}

func normalizeHelpTopic(command string) string {
	command = strings.TrimSpace(command)
	switch command {
	case "", "help":
		return ""
	case "author":
		return "bootstrap"
	default:
		return command
	}
}

func writeHelp(out io.Writer, command string) error {
	topic := normalizeHelpTopic(command)
	help := rootHelp
	if topic != "" {
		var known bool
		help, known = commandHelp[topic]
		if !known {
			return fmt.Errorf("unknown plumtree help command %q", command)
		}
	}
	_, err := fmt.Fprint(out, help)
	return err
}

// routeCommandHelp handles help forms and rejects commands that would
// otherwise fall through to serve argument parsing.
func routeCommandHelp(args []string, out io.Writer) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	if isHelp(args[0]) {
		return true, writeHelp(out, "")
	}
	if args[0] == "help" {
		switch {
		case len(args) == 1, len(args) == 2 && isHelp(args[1]):
			return true, writeHelp(out, "")
		case len(args) == 2:
			return true, writeHelp(out, args[1])
		case len(args) == 3 && args[1] == "author" && args[2] == "bootstrap":
			return true, writeHelp(out, "bootstrap")
		default:
			return true, errors.New("usage: plumtree help [command]")
		}
	}
	if args[0] == "author" {
		if len(args) == 2 && isHelp(args[1]) {
			return true, writeHelp(out, "bootstrap")
		}
		if len(args) < 2 {
			return true, errors.New("usage: plumtree author bootstrap [flags]")
		}
		if args[1] != "bootstrap" {
			return true, fmt.Errorf("unknown plumtree author command %q", args[1])
		}
		for _, arg := range args[2:] {
			if isHelp(arg) {
				return true, writeHelp(out, "bootstrap")
			}
		}
		return false, nil
	}
	if isKnownTopCommand(args[0]) {
		for _, arg := range args[1:] {
			if isHelp(arg) {
				return true, writeHelp(out, args[0])
			}
		}
		return false, nil
	}
	if strings.HasPrefix(args[0], "-") {
		for _, arg := range args {
			if isHelp(arg) {
				return true, writeHelp(out, "serve")
			}
		}
		return false, nil
	}
	return true, fmt.Errorf("unknown plumtree command %q", args[0])
}
