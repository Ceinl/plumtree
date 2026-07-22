// Command pt is the Plumtree author CLI. It implements the local authoring loop
// (`pt new` scaffolds an app; `pt dev` compiles it to WASM and runs it in a local
// wazero sandbox over the same ABI the platform uses), the deploy + claim loop
// (`pt deploy`, `pt claim`), and per-app capability config (`pt secret`,
// `pt egress`).
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ceinl/plumtree/pt/internal/scaffold"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "new":
		err = cmdNew(os.Args[2:])
	case "dev":
		err = cmdDev(os.Args[2:])
	case "deploy":
		err = cmdDeploy(os.Args[2:])
	case "claim":
		err = cmdClaim(os.Args[2:])
	case "inspect":
		err = cmdInspect(os.Args[2:])
	case "logs":
		err = cmdLogs(os.Args[2:])
	case "secret":
		err = cmdSecret(os.Args[2:])
	case "egress":
		err = cmdEgress(os.Args[2:])
	case "whoami":
		err = cmdWhoami(os.Args[2:])
	case "configure", "config":
		err = cmdConfigure(os.Args[2:], os.Stdin, os.Stdout)
	case "--addr", "--token", "--token-stdin", "--clear-addr", "--clear-token":
		// Convenient git-style shorthand: `pt --addr URL --token`.
		err = cmdConfigure(os.Args[1:], os.Stdin, os.Stdout)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "pt: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pt:", terminalSafeText(err.Error()))
		os.Exit(1)
	}
}

// devRoot is the local plumtree repo path, embedded at build time via
// -ldflags "-X main.devRoot=...". It lets `pt dev` resolve the unpublished
// local SDK/runtime for apps anywhere on disk, even without an env var.
var devRoot string

func usage() {
	fmt.Fprint(os.Stderr, `pt — Plumtree author CLI

Usage:
  pt new <name> [--tui|--cli]   scaffold a new app (default --tui)
  pt dev [flags]                build the app to WASM and run it locally
  pt deploy [flags]             upload app source; the control plane builds it server-side
  pt claim                      open this app's deploy claim URL
  pt inspect [deploy-id]         inspect this app's claimed deploy metadata
  pt logs [deploy-id]            show basic session metadata for this app
  pt secret set|list|rm          manage this app's server-side secrets (claimed apps)
  pt egress add|list|rm          manage this app's outbound HTTP allowlist (claimed apps)
  pt whoami                     show the claimed app namespace for this project
  pt configure [flags]          save or show server address and deploy token
  pt --addr URL --token         shorthand with hidden interactive token input

pt dev flags:
  --ssh                serve over SSH; connect with: ssh <app>@plumtree.dev
  --addr               ssh listen address (default 127.0.0.1:2222)
  --host               local SSH host alias (default plumtree.dev)
  --no-ssh-config      do not update ~/.ssh/config; print a raw ssh command instead
  --headless           run a scripted session without a terminal
  --script "up,up,q"   headless input tokens (up/down/left/right/enter/tab/esc/ctrl-c or a rune)
  -w, -h               headless frame size (default 40x12)
  --frame-timeout      per-frame wall-clock deadline (default 2s)
  --mem-pages          linear-memory cap in 64KiB pages (default 512)
  --max-fps            tty/ssh repaint cap (default 60)

Environment (deploy/inspect/logs/whoami/secret/egress):
  PLUMTREE_SERVER_URL  temporary control-plane URL override
  PLUMTREE_DEV_TOKEN   temporary deploy-token override
  PLUMTREE_PT_CONFIG   alternate pt configuration file
`)
}

// --- pt new ---------------------------------------------------------------

func cmdNew(args []string) error {
	// Accept the name in any position so both `pt new counter --tui` and
	// `pt new --tui counter` work (flag.Parse otherwise stops at the name).
	var name string
	var flags []string
	for _, a := range args {
		if name == "" && !strings.HasPrefix(a, "-") {
			name = a
			continue
		}
		flags = append(flags, a)
	}

	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	tui := fs.Bool("tui", false, "scaffold a TUI app (default)")
	cli := fs.Bool("cli", false, "scaffold a non-interactive CLI app")
	if err := fs.Parse(flags); err != nil {
		return err
	}
	if name == "" || fs.NArg() != 0 {
		return errors.New("usage: pt new <name> [--tui|--cli]")
	}
	if *tui && *cli {
		return errors.New("choose one of --tui or --cli")
	}
	kind := scaffold.TUI
	if *cli {
		kind = scaffold.CLI
	}

	cwd, _ := os.Getwd()
	dir, err := scaffold.New(cwd, name, kind)
	if err != nil {
		return err
	}
	rel, relErr := filepath.Rel(cwd, dir)
	if relErr != nil {
		rel = dir
	}
	fmt.Printf("Created %s/ (%s app)\n\nNext:\n  cd %s\n  pt dev\n", rel, kind, rel)
	return nil
}
