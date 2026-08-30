package workflow

import (
	"fmt"
	"strings"
)

const rootHelp = `pt — Plumtree author CLI

Usage:
  pt new NAME --tui|--cli --access public|restricted
  pt dev [flags] [--] [args...]
  pt build [--json]
  pt deploy [--server NAME] [--yes] [--json]
  pt status | app | logs | secret | egress | access | audit | ssh

Run "pt help COMMAND" for command details.
`

const devHelp = `Usage:
  pt dev [flags] [--] [args...]

TUI apps use the current terminal by default. CLI arguments follow the flags.
Use -- before an app argument that starts with a dash.

Flags:
  --headless                    run a scripted TUI session
  --script TOKENS               comma-separated headless input
  -w N, -h N                    headless terminal size
  --ssh                         serve the app through local SSH
  --addr HOST:PORT              SSH listen address (default 127.0.0.1:2222)
  --allow-nonloopback-ssh       permit a non-loopback SSH listen address
  --host ALIAS                  ssh config alias to install (with --ssh,
                                default plumtree.dev)
  --no-ssh-config               print the raw ssh command instead of writing
                                the ~/.ssh/config alias (with --ssh)
  --mem-pages N                 WebAssembly memory limit
  --frame-timeout D             per-frame deadline
  --max-fps N                   terminal and SSH repaint limit
  --reset                       reset the persistent local profile
  --json                        emit stable JSON
`

const newHelp = `Usage:
  pt new NAME --tui|--cli --access public|restricted
  pt new --tui|--cli --access public|restricted NAME
`

func isHelp(value string) bool {
	return value == "-h" || value == "--help"
}

func (r Runner) writeHelp(command string) error {
	help := rootHelp
	switch strings.TrimSpace(command) {
	case "":
	case "new":
		help = newHelp
	case "dev":
		help = devHelp
	case "build":
		help = `Usage:
  pt build [--json]

Compiles the current project locally to a typed WASM artifact using the
author's own toolchain; nothing is uploaded. --json emits stable JSON.
`
	case "deploy":
		help = `Usage:
  pt deploy [--server NAME] [--yes] [--json]

Builds the current project locally and deploys it to the selected paired
server. Asks for confirmation unless --yes is given.
`
	case "secret":
		help = `Usage:
  pt secret set APP_ID KEY [VALUE]
  pt secret list APP_ID
  pt secret rm APP_ID KEY [--yes]

With no VALUE, set reads the secret from stdin (hidden prompt in a terminal).
rm asks for confirmation unless --yes is given.
`
	case "egress":
		help = `Usage:
  pt egress add APP_ID HOST
  pt egress list APP_ID
  pt egress rm APP_ID HOST [--yes]

Egress is default-deny; add allows one host. rm asks for confirmation unless
--yes is given.
`
	case "access":
		help = `Usage:
  pt access add APP_ID NAME PUBLIC_KEY FINGERPRINT
  pt access list APP_ID
  pt access rm APP_ID KEY_ID [--yes]

Access keys let non-owner SSH identities reach a restricted app. rm asks for
confirmation unless --yes is given.
`
	case "status":
		help = `Usage:
  pt status [--json]

Shows the current paired server, its product version, and the author's apps.
`
	case "app":
		help = `Usage:
  pt app list
  pt app show ID

Both subcommands print stable JSON.
`
	case "logs":
		help = `Usage:
  pt logs APP_ID [--follow]

Prints session records as stable JSON. --follow keeps polling once per second.
`
	case "audit":
		help = `Usage:
  pt audit

Prints this author's audit records as stable JSON.
`
	case "ssh":
		help = `Usage:
  pt ssh APP_HANDLE

Prints the direct leaf ssh command for an app on the current paired server.
APP_HANDLE is owner/app or app — the same handle used to connect.
`
	case "pair":
		help = `Usage:
  pt pair [--bootstrap ID|--token ID] [--name NAME] [--port N] [--device NAME]
          [--secret PHRASE] [--next-recovery-secret PHRASE] [--yes] HOST

Pairs this device with a server. Exactly one of --bootstrap ID (first author)
or --token ID (additional device) is required. The pairing phrase is read from
stdin when --secret is absent. The displayed SSH host key must be confirmed,
or --yes accepts it. HOST may include an explicit port (HOST:PORT).
`
	case "recover":
		help = `Usage:
  pt recover --author HANDLE [--secret PHRASE] [--next-recovery-secret PHRASE]
             [--yes] HOST

Recovers access with the offline recovery phrase (read from stdin when
--secret is absent), rotates the phrase, and revokes lost devices. Prints the
new recovery phrase once.
`
	case "server":
		help = `Usage:
  pt server list|current
  pt server use NAME
  pt server rename OLD NEW
  pt server unpair [NAME] [--yes]
  pt server forget [NAME] [--yes]

list and current print stable JSON. unpair also revokes the device on the
server and requires confirmation unless --yes is given. forget removes only
the local record.
`
	case "device":
		help = `Usage:
  pt device list
  pt device invite NAME
  pt device revoke DEVICE_ID [--yes]

invite prints a one-use invitation for pairing a second device; all
subcommands print stable JSON. revoke asks for confirmation unless --yes is
given.
`
	default:
		return fmt.Errorf("unknown pt help command %q", command)
	}
	_, out, _ := r.streams()
	_, err := fmt.Fprint(out, help)
	return err
}
