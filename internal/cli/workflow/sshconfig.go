package workflow

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ceinl/plumtree/internal/fsatomic"
)

const (
	devSSHHostKeyAlias = "plumtree-dev"
	sshConfigBegin     = "# BEGIN PLUMTREE DEV"
	sshConfigEnd       = "# END PLUMTREE DEV"
)

// validateSSHAlias rejects ssh_config alias values that could break the managed
// block or the generated Host line.
func validateSSHAlias(host string) error {
	if host == "" {
		return errors.New("ssh host alias cannot be empty")
	}
	if strings.ContainsAny(host, " \t\r\n") {
		return fmt.Errorf("ssh host alias %q cannot contain whitespace", host)
	}
	return nil
}

// localConnectHost maps a wildcard listen host to the address an SSH client on
// the same machine can actually reach, bracketing IPv6 literals for command
// lines.
func localConnectHost(listenHost string) string {
	switch listenHost {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	}
	if ip := net.ParseIP(listenHost); ip != nil && strings.Contains(listenHost, ":") {
		return "[" + listenHost + "]"
	}
	return listenHost
}

// installDevSSHConfig installs or replaces only the Plumtree-managed block in
// ~/.ssh/config, leaving everything outside the markers untouched. The write is
// atomic so a crash never truncates the user's ssh config.
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

	next := replaceManagedSSHBlock(string(existing), devSSHConfigBlock(host, unbracket(targetHost), port))
	if err := fsatomic.WriteFileAtomic(path, []byte(next), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// unbracket removes the command-line brackets from an IPv6 literal; ssh_config
// HostName takes the bare address.
func unbracket(host string) string {
	return strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
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
