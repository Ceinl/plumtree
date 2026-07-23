package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

func cmdPing(args []string, out io.Writer) error {
	list := false
	switch len(args) {
	case 0:
	case 1:
		if args[0] != "list" {
			return errors.New("usage: pt ping [list]")
		}
		list = true
	default:
		return errors.New("usage: pt ping [list]")
	}

	server, devToken, err := resolveConnection()
	if err != nil {
		return err
	}
	if devToken == "" {
		return fmt.Errorf("server %s: missing deploy token; run `pt configure --token` or set PLUMTREE_DEV_TOKEN", server)
	}
	result, err := getPing(context.Background(), server, devToken)
	if err != nil {
		return explainPingError(server, err)
	}

	fmt.Fprintf(out, "Server: %s\n", terminalSafeText(server))
	fmt.Fprintln(out, "Status: reachable (authenticated)")
	if !list {
		return nil
	}
	if len(result.Apps) == 0 {
		fmt.Fprintln(out, "No deployed apps.")
		return nil
	}
	fmt.Fprintln(out, "Apps:")
	for _, app := range result.Apps {
		fmt.Fprintf(out, "  %s  %s\n", terminalSafeText(app.Handle), terminalSafeText(app.ActiveDeployID))
	}
	return nil
}

func explainPingError(server string, err error) error {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Errorf("cannot reach %s: DNS lookup failed for %q; check the server address and network connection", server, dnsErr.Name)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("cannot reach %s: network connection failed: %v; check the server address and network connection", server, urlErr.Err)
	}
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		detail := strings.TrimSpace(statusErr.Body)
		switch statusErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("authentication failed for %s (%s): %s; check the token with `pt configure`", server, statusErr.Status, detail)
		default:
			if statusErr.StatusCode >= 500 {
				return fmt.Errorf("server %s returned %s: %s; try again or check the server logs", server, statusErr.Status, detail)
			}
			return fmt.Errorf("server %s returned %s: %s; check that this is a compatible Plumtree server", server, statusErr.Status, detail)
		}
	}
	return fmt.Errorf("server %s check failed: %w", server, err)
}
