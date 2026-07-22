package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const maxDevTokenBytes = 4 << 10

var tailscaleIPv4Range = netip.MustParsePrefix("100.64.0.0/10")

type networkOverrides struct {
	addr   bool
	origin bool
	ssh    bool
}

func visitedFlagNames() map[string]bool {
	visited := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	return visited
}

func envIsSet(name string) bool {
	_, ok := os.LookupEnv(name)
	return ok
}

func applyTailscaleDefaults(ip string, addr, origin, sshAddr *string, overrides networkOverrides) {
	if !overrides.addr {
		*addr = net.JoinHostPort(ip, "8080")
	}
	if !overrides.origin {
		*origin = "http://" + net.JoinHostPort(ip, "8080")
	}
	if !overrides.ssh {
		*sshAddr = net.JoinHostPort(ip, "2222")
	}
}

func detectTailscaleIPv4(parent context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tailscale", "ip", "-4").Output()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", errors.New("detect Tailscale address: tailscale CLI timed out")
	}
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", errors.New("detect Tailscale address: tailscale CLI is not installed")
		}
		return "", fmt.Errorf("detect Tailscale address: run `tailscale ip -4`: %w", err)
	}
	return parseTailscaleIPv4(string(out))
}

func parseTailscaleIPv4(output string) (string, error) {
	for _, field := range strings.Fields(output) {
		ip, err := netip.ParseAddr(field)
		if err == nil && ip.Is4() && tailscaleIPv4Range.Contains(ip) {
			return ip.String(), nil
		}
	}
	return "", errors.New("detect Tailscale address: `tailscale ip -4` returned no Tailscale IPv4 address")
}

func devTokenFilePath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("PLUMTREE_DEV_TOKEN_FILE")); path != "" {
		return filepath.Abs(path)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(dir, "plumtree", "dev-token"), nil
}

func loadOrCreateDevToken() (token, path string, err error) {
	path, err = devTokenFilePath()
	if err != nil {
		return "", "", err
	}
	token, err = loadOrCreateDevTokenAt(path)
	return token, path, err
}

func loadOrCreateDevTokenAt(path string) (string, error) {
	if token, err := readDevTokenFile(path); err == nil {
		return token, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create dev token directory: %w", err)
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate dev token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return readDevTokenFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("create dev token %q: %w", path, err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if _, err := f.WriteString(token + "\n"); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write dev token %q: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("sync dev token %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close dev token %q: %w", path, err)
	}
	complete = true
	return token, nil
}

func readDevTokenFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("dev token %q must be a regular file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("dev token %q has insecure permissions %04o; run chmod 600 %q", path, info.Mode().Perm(), path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read dev token %q: %w", path, err)
	}
	if len(b) > maxDevTokenBytes {
		return "", fmt.Errorf("dev token %q exceeds %d bytes", path, maxDevTokenBytes)
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", fmt.Errorf("dev token %q is empty", path)
	}
	return token, nil
}
