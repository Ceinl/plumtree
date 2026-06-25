package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	buildworker "github.com/Ceinl/plumtree/build-worker"
)

func cmdDeploy(args []string) error {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	serverURL := fs.String("server", env("PLUMTREE_SERVER_URL", "http://localhost:18080"), "control-plane URL")
	devToken := fs.String("dev-token", os.Getenv("PLUMTREE_DEV_TOKEN"), "local dev deploy token")
	visibility := fs.String("visibility", "public", "public or private")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: pt deploy [--server URL] --dev-token TOKEN")
	}
	if *devToken == "" {
		return errors.New("missing --dev-token; start control-plane with -dev-token TOKEN and pass the same token here")
	}
	if *visibility != "public" && *visibility != "private" {
		return errors.New("--visibility must be public or private")
	}

	proj, err := findProject()
	if err != nil {
		return err
	}
	man, err := readManifest(proj)
	if err != nil {
		return err
	}
	if man.Name == "" {
		return errors.New("plumtree.json missing app name")
	}
	if man.Type == "" {
		man.Type = "tui"
	}

	source, err := buildworker.PackSource(proj)
	if err != nil {
		return fmt.Errorf("packing app source: %w", err)
	}

	req := deployRequest{
		AppName:      man.Name,
		AppType:      man.Type,
		Visibility:   *visibility,
		ABIVersion:   0,
		SourceDigest: buildworker.SourceDigest(source),
		BuildMetadata: map[string]string{
			"app_type": man.Type,
			"client":   "pt",
		},
		Source: source,
	}

	server := normalizedServerURL(*serverURL)
	meta, err := readDeployMetadata(proj)
	if err != nil {
		return err
	}

	var res deployResponse
	usedExistingClaim := false
	if usableDeployMetadata(meta, server) {
		res, err = putDeploy(context.Background(), server, *devToken, meta.DeployID, meta.ClaimToken, req)
		if err == nil {
			usedExistingClaim = true
		} else if !isHTTPStatus(err, http.StatusNotFound) {
			return err
		}
	}
	if !usedExistingClaim {
		res, err = postDeploy(context.Background(), server, *devToken, req)
	}
	if err != nil {
		return err
	}

	claimURL := responseClaimURL(res)
	claimToken := ""
	if claimURL != "" {
		claimToken = claimTokenFromURL(claimURL, res.Deploy.ID)
	}
	if claimToken == "" && usedExistingClaim && meta != nil {
		claimToken = meta.ClaimToken
		claimURL = deployClaimURL(meta)
	}
	if claimToken == "" {
		return errors.New("control plane did not return a usable deploy claim URL")
	}
	nextMeta := deployMetadata{
		ServerURL:      server,
		DevToken:       *devToken,
		DeployID:       res.Deploy.ID,
		ClaimToken:     claimToken,
		ClaimURL:       claimURL,
		ClaimExpiresAt: res.Deploy.ClaimExpiresAt,
		AppHandle:      res.App.Handle,
		UpdatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeDeployMetadata(proj, nextMeta); err != nil {
		return err
	}

	if res.App.Handle != "" {
		fmt.Printf("Deployed %s\n", res.App.Handle)
	} else {
		fmt.Printf("Created deploy claim for %s\n", man.Name)
		fmt.Println("Claim: pt claim")
		if res.Deploy.ClaimExpiresAt != "" {
			fmt.Printf("Claim expires: %s\n", res.Deploy.ClaimExpiresAt)
		}
	}
	fmt.Printf("Deploy: %s\n", res.Deploy.ID)
	fmt.Printf("Dashboard: %s/dashboard\n", server)
	return nil
}

func cmdClaim(args []string) error {
	fs := flag.NewFlagSet("claim", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: pt claim")
	}
	proj, err := findProject()
	if err != nil {
		return err
	}
	meta, err := readDeployMetadata(proj)
	if err != nil {
		return err
	}
	if meta == nil {
		return errors.New("no deploy claim metadata found; run pt deploy first")
	}
	link := deployClaimURL(meta)
	if link == "" {
		return errors.New("deploy claim metadata is missing a claim URL")
	}
	fmt.Printf("Claim URL: %s\n", link)
	if err := openURLInBrowser(link); err != nil {
		fmt.Fprintf(os.Stderr, "Could not open browser: %v\n", err)
		return nil
	}
	fmt.Println("Opened claim URL in browser.")
	return nil
}

func cmdInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	serverURL := fs.String("server", env("PLUMTREE_SERVER_URL", ""), "control-plane URL")
	devToken := fs.String("dev-token", os.Getenv("PLUMTREE_DEV_TOKEN"), "local dev deploy token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("usage: pt inspect [deploy-id] [--server URL] [--dev-token TOKEN]")
	}
	meta, deployID, server, token, err := deployReadOptions(*serverURL, *devToken, fs.Arg(0))
	if err != nil {
		return err
	}
	res, err := getDeployInspect(context.Background(), server, token, deployID, meta.ClaimToken)
	if err != nil {
		return err
	}
	if res.App.Handle != "" && meta.AppHandle != res.App.Handle {
		meta.AppHandle = res.App.Handle
		_ = updateCurrentDeployMetadata(*meta)
	}
	printInspect(res)
	return nil
}

func cmdLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	serverURL := fs.String("server", env("PLUMTREE_SERVER_URL", ""), "control-plane URL")
	devToken := fs.String("dev-token", os.Getenv("PLUMTREE_DEV_TOKEN"), "local dev deploy token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("usage: pt logs [deploy-id] [--server URL] [--dev-token TOKEN]")
	}
	meta, deployID, server, token, err := deployReadOptions(*serverURL, *devToken, fs.Arg(0))
	if err != nil {
		return err
	}
	res, err := getDeployLogs(context.Background(), server, token, deployID, meta.ClaimToken)
	if err != nil {
		return err
	}
	if len(res.Sessions) == 0 {
		fmt.Println("No sessions recorded yet.")
		return nil
	}
	for _, session := range res.Sessions {
		ended := session.EndedAt
		if ended == "" {
			ended = "active"
		}
		fmt.Printf("%s  deploy=%s  started=%s  ended=%s\n", session.ID, session.DeployID, session.StartedAt, ended)
		if session.Log != "" {
			for _, line := range strings.Split(strings.TrimRight(session.Log, "\n"), "\n") {
				fmt.Printf("    %s\n", line)
			}
			if session.LogTruncated {
				fmt.Println("    (log truncated)")
			}
		}
	}
	return nil
}

func cmdWhoami(args []string) error {
	fs := flag.NewFlagSet("whoami", flag.ContinueOnError)
	serverURL := fs.String("server", env("PLUMTREE_SERVER_URL", ""), "control-plane URL")
	devToken := fs.String("dev-token", os.Getenv("PLUMTREE_DEV_TOKEN"), "local dev deploy token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: pt whoami [--server URL] [--dev-token TOKEN]")
	}
	meta, deployID, server, token, err := deployReadOptions(*serverURL, *devToken, "")
	if err != nil {
		return err
	}
	res, err := getDeployInspect(context.Background(), server, token, deployID, meta.ClaimToken)
	if err != nil {
		return err
	}
	if res.App.Handle == "" {
		fmt.Printf("Deploy %s is not claimed yet.\n", deployID)
		return nil
	}
	if meta.AppHandle != res.App.Handle {
		meta.AppHandle = res.App.Handle
		_ = updateCurrentDeployMetadata(*meta)
	}
	fmt.Println(res.App.Handle)
	return nil
}

func printInspect(res inspectResponse) {
	handle := res.App.Handle
	if handle == "" {
		handle = "(unclaimed)"
	}
	fmt.Printf("App:      %s\n", handle)
	fmt.Printf("Deploy:   %s\n", res.Deploy.ID)
	fmt.Printf("Type:     %s\n", firstNonEmpty(res.Deploy.AppType, "tui"))
	fmt.Printf("Visible:  %s\n", firstNonEmpty(res.App.Visibility, "private"))
	fmt.Printf("Active:   %t\n", res.App.ActiveDeployID == res.Deploy.ID && res.App.ActiveDeployID != "")
	fmt.Printf("Artifact: %s (%d bytes)\n", res.Artifact.Digest, res.Artifact.SizeBytes)
	if res.Deploy.SourceDigest != "" {
		fmt.Printf("Source:   %s\n", res.Deploy.SourceDigest)
	}
	if m := res.Artifact.BuildMetadata; m != nil {
		if m["builder"] != "" {
			fmt.Printf("Built by: %s\n", m["builder"])
		}
		if m["compiler"] != "" {
			fmt.Printf("Compiler: %s\n", m["compiler"])
		}
		if m["build_duration_ms"] != "" {
			fmt.Printf("Build:    %s ms\n", m["build_duration_ms"])
		}
	}
}
