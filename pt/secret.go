package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

// cmdSecret manages an app's server-side secrets. Secrets are claimed-only and
// authorized by the saved deploy claim token (the same proof `pt deploy` uses),
// so a project must be deployed and claimed first.
//
//	pt secret set KEY [VALUE]   set a secret (VALUE read from stdin if omitted)
//	pt secret list              list secret names and versions (never values)
//	pt secret rm KEY            remove a secret
func cmdSecret(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: pt secret <set|list|rm> ...")
	}
	switch args[0] {
	case "set":
		return cmdSecretSet(args[1:])
	case "list", "ls":
		return cmdSecretList(args[1:])
	case "rm", "delete":
		return cmdSecretRm(args[1:])
	default:
		return fmt.Errorf("pt secret: unknown subcommand %q", args[0])
	}
}

func secretFlags(name string) (*flag.FlagSet, *string, *string) {
	fs := flag.NewFlagSet("secret "+name, flag.ContinueOnError)
	serverURL := fs.String("server", env("PLUMTREE_SERVER_URL", ""), "control-plane URL")
	devToken := fs.String("dev-token", os.Getenv("PLUMTREE_DEV_TOKEN"), "local dev deploy token")
	return fs, serverURL, devToken
}

func cmdSecretSet(args []string) error {
	fs, serverURL, devToken := secretFlags("set")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return errors.New("usage: pt secret set KEY [VALUE]")
	}
	key := fs.Arg(0)
	var value string
	if fs.NArg() == 2 {
		value = fs.Arg(1)
	} else {
		// Read the value from stdin so it never lands in shell history.
		fmt.Fprintf(os.Stderr, "Enter value for %s (end with newline): ", key)
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return fmt.Errorf("reading secret value: %w", err)
		}
		value = strings.TrimRight(line, "\r\n")
	}

	meta, _, server, token, err := deployReadOptions(*serverURL, *devToken, "")
	if err != nil {
		return err
	}
	res, err := setSecret(context.Background(), server, token, meta.DeployID, meta.ClaimToken, key, value)
	if err != nil {
		return err
	}
	fmt.Printf("Set secret %s (version %d)\n", res.Key, res.Version)
	return nil
}

func cmdSecretList(args []string) error {
	fs, serverURL, devToken := secretFlags("list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	meta, _, server, token, err := deployReadOptions(*serverURL, *devToken, "")
	if err != nil {
		return err
	}
	res, err := listSecrets(context.Background(), server, token, meta.DeployID, meta.ClaimToken)
	if err != nil {
		return err
	}
	if len(res.Secrets) == 0 {
		fmt.Println("No secrets set.")
		return nil
	}
	for _, s := range res.Secrets {
		fmt.Printf("%-32s v%d\n", s.Key, s.Version)
	}
	return nil
}

func cmdSecretRm(args []string) error {
	fs, serverURL, devToken := secretFlags("rm")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: pt secret rm KEY")
	}
	meta, _, server, token, err := deployReadOptions(*serverURL, *devToken, "")
	if err != nil {
		return err
	}
	if err := deleteSecret(context.Background(), server, token, meta.DeployID, meta.ClaimToken, fs.Arg(0)); err != nil {
		return err
	}
	fmt.Printf("Removed secret %s\n", fs.Arg(0))
	return nil
}
