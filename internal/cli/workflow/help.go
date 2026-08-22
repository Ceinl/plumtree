package workflow

import (
	"fmt"
	"strings"
)

const rootHelp = `pt — Plumtree author CLI

Usage:
  pt pair [--bootstrap ID|--token ID] [--secret PHRASE] [--yes] HOST
  pt recover --author HANDLE [--secret PHRASE] [--yes] HOST
  pt server list|current|use|rename|unpair|forget
  pt device list|invite|revoke
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
  --headless           run a scripted TUI session
  --script TOKENS      comma-separated headless input
  -w N, -h N           headless terminal size
  --ssh                serve the app through local SSH
  --addr HOST:PORT     SSH loopback address (default 127.0.0.1:2222)
  --mem-pages N        WebAssembly memory limit
  --frame-timeout D    per-frame deadline
  --max-fps N          terminal and SSH repaint limit
  --reset              reset the persistent local profile
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
	case "pair":
		help = "Usage:\n  pt pair [--bootstrap ID|--token ID] [--secret PHRASE] [--yes] HOST\n"
	case "recover":
		help = "Usage:\n  pt recover --author HANDLE [--secret PHRASE] [--yes] HOST\n"
	case "server":
		help = "Usage:\n  pt server list|current|use|rename|unpair|forget\n"
	case "device":
		help = "Usage:\n  pt device list|invite|revoke\n"
	case "new":
		help = newHelp
	case "dev":
		help = devHelp
	case "deploy":
		help = "Usage:\n  pt deploy [--server NAME] [--yes] [--json]\n"
	case "secret":
		help = "Usage:\n  pt secret set|list|rm APP_ID [KEY] [VALUE] [--yes]\n"
	case "egress":
		help = "Usage:\n  pt egress list|add|rm APP_ID [HOST] [--yes]\n"
	case "access":
		help = "Usage:\n  pt access list|add|rm APP_ID [KEY] [--yes]\n"
	default:
		return fmt.Errorf("unknown pt help command %q", command)
	}
	_, out, _ := r.streams()
	_, err := fmt.Fprint(out, help)
	return err
}
