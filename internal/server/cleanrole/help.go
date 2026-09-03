package cleanrole

import (
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

Creates a one-use, time-bounded first-author authority and prints its ID and
secret as JSON. The secret is shown once; only its verifier is stored.

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

Suspends one deployment. The gateway picks the change up live through its
suspension watcher. Uses --config (or PLUMTREE_CONFIG) or a direct --database
path, but not both.
`

const unsuspendHelp = `Usage:
  plumtree unsuspend deploy <id> [-config PATH | -database PATH]

Lifts the suspension on one deployment. The gateway picks the change up live
through its suspension watcher. Uses --config (or PLUMTREE_CONFIG) or a direct
--database path, but not both.
`

const quotaHelp = `Usage:
  plumtree quota set <authorID> <maxApps> <maxDeploymentsPerApp> <maxSecretsPerApp> <maxSessions> [-config PATH | -database PATH]

Sets one author's resource quotas in a single write. All four values must be
non-negative integers. Uses --config (or PLUMTREE_CONFIG) or a direct
--database path, but not both.
`

func isHelp(value string) bool { return value == "-h" || value == "--help" }

func isKnownTopCommand(value string) bool {
	switch value {
	case "serve", "config", "bootstrap", "author", "state", "suspend", "unsuspend", "quota":
		return true
	}
	return false
}

func normalizeHelpTopic(command string) string {
	switch strings.TrimSpace(command) {
	case "", "help":
		return ""
	case "author":
		return "bootstrap"
	default:
		return strings.TrimSpace(command)
	}
}

func writeHelp(out io.Writer, command string) error {
	help := rootHelp
	switch normalizeHelpTopic(command) {
	case "":
	case "serve":
		help = serveHelp
	case "bootstrap":
		help = bootstrapHelp
	case "config":
		help = configHelp
	case "state":
		help = stateHelp
	case "suspend":
		help = suspendHelp
	case "unsuspend":
		help = unsuspendHelp
	case "quota":
		help = quotaHelp
	default:
		return fmt.Errorf("unknown plumtree help command %q", command)
	}
	_, err := fmt.Fprint(out, help)
	return err
}
