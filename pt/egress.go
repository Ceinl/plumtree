package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
)

// cmdEgress manages an app's outbound-HTTP allowlist. Egress is default-deny and
// claimed-only, authorized by the saved deploy claim token.
//
//	pt egress add HOST    allow outbound requests to HOST (and its subdomains)
//	pt egress list        show the current allowlist
//	pt egress rm HOST     remove HOST from the allowlist
func cmdEgress(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: pt egress <add|list|rm> ...")
	}
	switch args[0] {
	case "add":
		return cmdEgressMutate(args[1:], "add", addEgressHost)
	case "rm", "remove":
		return cmdEgressMutate(args[1:], "rm", removeEgressHost)
	case "list", "ls":
		return cmdEgressList(args[1:])
	default:
		return fmt.Errorf("pt egress: unknown subcommand %q", args[0])
	}
}

func egressFlags(name string) (*flag.FlagSet, *string, *string) {
	fs := flag.NewFlagSet("egress "+name, flag.ContinueOnError)
	serverURL := fs.String("server", env("PLUMTREE_SERVER_URL", ""), "control-plane URL")
	devToken := fs.String("dev-token", os.Getenv("PLUMTREE_DEV_TOKEN"), "local dev deploy token")
	return fs, serverURL, devToken
}

func cmdEgressMutate(args []string, verb string, call func(ctx context.Context, server, devToken, deployID, claimToken, host string) ([]string, error)) error {
	fs, serverURL, devToken := egressFlags(verb)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: pt egress %s HOST", verb)
	}
	meta, _, server, token, err := deployReadOptions(*serverURL, *devToken, "")
	if err != nil {
		return err
	}
	hosts, err := call(context.Background(), server, token, meta.DeployID, meta.ClaimToken, fs.Arg(0))
	if err != nil {
		return err
	}
	printEgress(hosts)
	return nil
}

func cmdEgressList(args []string) error {
	fs, serverURL, devToken := egressFlags("list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	meta, _, server, token, err := deployReadOptions(*serverURL, *devToken, "")
	if err != nil {
		return err
	}
	hosts, err := listEgressHosts(context.Background(), server, token, meta.DeployID, meta.ClaimToken)
	if err != nil {
		return err
	}
	printEgress(hosts)
	return nil
}

func printEgress(hosts []string) {
	if len(hosts) == 0 {
		fmt.Println("Egress allowlist is empty (default-deny).")
		return
	}
	fmt.Println("Allowed hosts:")
	for _, h := range hosts {
		fmt.Printf("  %s\n", h)
	}
}
