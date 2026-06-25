package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	devSSHHostKeyAlias = "plumtree-dev"
	sshConfigBegin     = "# BEGIN PLUMTREE DEV"
	sshConfigEnd       = "# END PLUMTREE DEV"
)

func validateSSHHost(host string) error {
	if host == "" {
		return errors.New("ssh host alias cannot be empty")
	}
	if strings.ContainsAny(host, " \t\r\n") {
		return fmt.Errorf("ssh host alias %q cannot contain whitespace", host)
	}
	return nil
}

func localConnectHost(listenHost string) string {
	switch listenHost {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	default:
		return listenHost
	}
}

func installDevSSHConfig(host, targetHost, port string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ssh")
	path := filepath.Join(dir, "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	var existing []byte
	if b, err := os.ReadFile(path); err == nil {
		existing = b
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	next := replaceManagedSSHBlock(string(existing), devSSHConfigBlock(host, targetHost, port))
	if err := os.WriteFile(path, []byte(next), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func devSSHConfigBlock(host, targetHost, port string) string {
	return fmt.Sprintf(`%s
Host %s
    HostName %s
    Port %s
    HostKeyAlias %s
    StrictHostKeyChecking accept-new
%s
`, sshConfigBegin, host, targetHost, port, devSSHHostKeyAlias, sshConfigEnd)
}

func replaceManagedSSHBlock(existing, block string) string {
	existing = strings.TrimRight(existing, "\n")
	start := strings.Index(existing, sshConfigBegin)
	end := strings.Index(existing, sshConfigEnd)
	if start >= 0 && end >= start {
		end += len(sshConfigEnd)
		next := strings.TrimRight(existing[:start], "\n")
		tail := strings.TrimLeft(existing[end:], "\n")
		var parts []string
		if next != "" {
			parts = append(parts, next)
		}
		parts = append(parts, strings.TrimRight(block, "\n"))
		if tail != "" {
			parts = append(parts, tail)
		}
		return strings.Join(parts, "\n\n") + "\n"
	}
	if existing == "" {
		return block
	}
	return strings.TrimRight(block, "\n") + "\n\n" + existing + "\n"
}
