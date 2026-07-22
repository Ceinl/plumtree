package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/term"
)

const maxPTConfigBytes = 64 << 10

type ptConfig struct {
	ServerURL   string `json:"serverUrl,omitempty"`
	DeployToken string `json:"deployToken,omitempty"`
}

// ptConfigPath returns the configured override path or the OS-native default.
func ptConfigPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("PLUMTREE_PT_CONFIG")); path != "" {
		return filepath.Abs(path)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(dir, "plumtree", "pt.json"), nil
}

// readPTConfig loads configuration only from a private regular file.
func readPTConfig() (ptConfig, error) {
	path, err := ptConfigPath()
	if err != nil {
		return ptConfig{}, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ptConfig{}, nil
	}
	if err != nil {
		return ptConfig{}, fmt.Errorf("inspect pt config %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return ptConfig{}, fmt.Errorf("pt config %q must be a regular file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return ptConfig{}, fmt.Errorf("pt config %q has insecure permissions %04o; run chmod 600 %q", path, info.Mode().Perm(), path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ptConfig{}, fmt.Errorf("read pt config %q: %w", path, err)
	}
	if len(b) > maxPTConfigBytes {
		return ptConfig{}, fmt.Errorf("pt config %q exceeds %d bytes", path, maxPTConfigBytes)
	}
	var cfg ptConfig
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return ptConfig{}, fmt.Errorf("parse pt config %q: %w", path, err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return ptConfig{}, fmt.Errorf("parse pt config %q: %w", path, err)
	}
	if cfg.ServerURL != "" {
		cfg.ServerURL, err = validateServerURL(cfg.ServerURL)
		if err != nil {
			return ptConfig{}, fmt.Errorf("pt config %q: %w", path, err)
		}
	}
	return cfg, nil
}

// writePTConfig atomically replaces the configuration with a private file.
func writePTConfig(cfg ptConfig) (string, error) {
	path, err := ptConfigPath()
	if err != nil {
		return "", err
	}
	if cfg.ServerURL != "" {
		cfg.ServerURL, err = validateServerURL(cfg.ServerURL)
		if err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create pt config directory: %w", err)
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pt-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temporary pt config: %w", err)
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		_ = tmp.Close()
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return "", fmt.Errorf("secure temporary pt config: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		return "", fmt.Errorf("write temporary pt config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("sync temporary pt config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temporary pt config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("replace pt config %q: %w", path, err)
	}
	removeTemp = false
	return path, nil
}

// resolveConnection applies environment, saved, baked, and local defaults in order.
func resolveConnection() (serverURL, deployToken string, err error) {
	cfg, err := readPTConfig()
	if err != nil {
		return "", "", err
	}
	rawServerURL := firstNonEmpty(
		os.Getenv("PLUMTREE_SERVER_URL"),
		cfg.ServerURL,
		defaultServerURL,
		localServerURL,
	)
	serverURL, err = validateServerURL(rawServerURL)
	if err != nil {
		return "", "", err
	}
	deployToken = firstNonEmpty(
		os.Getenv("PLUMTREE_DEV_TOKEN"),
		cfg.DeployToken,
		defaultDevToken,
	)
	return serverURL, deployToken, nil
}

// cmdConfigure shows or updates the persistent pt connection configuration.
func cmdConfigure(args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("configure", flag.ContinueOnError)
	fs.SetOutput(out)
	addr := fs.String("addr", "", "control-plane URL, including http:// or https://")
	tokenPrompt := fs.Bool("token", false, "prompt for the deploy token (or read one line from stdin)")
	tokenStdin := fs.Bool("token-stdin", false, "alias for --token")
	clearAddr := fs.Bool("clear-addr", false, "remove the saved control-plane URL")
	clearToken := fs.Bool("clear-token", false, "remove the saved deploy token")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: pt configure [--addr URL] [--token | --token-stdin] [--clear-addr] [--clear-token]")
	}

	setAddr := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "addr" {
			setAddr = true
		}
	})
	if setAddr && *clearAddr {
		return errors.New("choose --addr or --clear-addr, not both")
	}
	if *tokenPrompt && *tokenStdin {
		return errors.New("choose --token or --token-stdin, not both")
	}
	if (*tokenPrompt || *tokenStdin) && *clearToken {
		return errors.New("choose --token/--token-stdin or --clear-token, not both")
	}

	cfg, err := readPTConfig()
	if err != nil {
		return err
	}
	changed := setAddr || *tokenPrompt || *tokenStdin || *clearAddr || *clearToken
	if setAddr {
		cfg.ServerURL, err = validateServerURL(*addr)
		if err != nil {
			return err
		}
	}
	if *tokenPrompt || *tokenStdin {
		cfg.DeployToken, err = readDeployToken(in, out)
		if err != nil {
			return err
		}
	}
	if *clearAddr {
		cfg.ServerURL = ""
	}
	if *clearToken {
		cfg.DeployToken = ""
	}
	if changed {
		path, err := writePTConfig(cfg)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Saved pt configuration to %s\n", path)
	}
	printPTConfig(out, cfg)
	return nil
}

// readDeployToken reads one token line and disables echo for interactive terminals.
func readDeployToken(in io.Reader, out io.Writer) (string, error) {
	if terminal, ok := in.(*os.File); ok && term.IsTerminal(int(terminal.Fd())) {
		fmt.Fprint(out, "Deploy token: ")
		value, err := term.ReadPassword(int(terminal.Fd()))
		fmt.Fprintln(out)
		if err != nil {
			return "", fmt.Errorf("read deploy token: %w", err)
		}
		return requireDeployToken(string(value))
	}
	value, err := bufio.NewReader(io.LimitReader(in, maxPTConfigBytes+1)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read deploy token: %w", err)
	}
	if len(value) > maxPTConfigBytes {
		return "", errors.New("deploy token is too long")
	}
	return requireDeployToken(strings.TrimSuffix(strings.TrimSuffix(value, "\n"), "\r"))
}

// requireDeployToken trims and rejects an empty token value.
func requireDeployToken(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("deploy token cannot be empty")
	}
	return value, nil
}

// printPTConfig reports configuration state without disclosing the token.
func printPTConfig(out io.Writer, cfg ptConfig) {
	addr := cfg.ServerURL
	if addr == "" {
		addr = "(default: " + localServerURL + ")"
	}
	token := "not configured"
	if cfg.DeployToken != "" {
		token = "configured"
	}
	fmt.Fprintf(out, "Address: %s\n", addr)
	fmt.Fprintf(out, "Token:   %s\n", token)
	if os.Getenv("PLUMTREE_SERVER_URL") != "" || os.Getenv("PLUMTREE_DEV_TOKEN") != "" {
		fmt.Fprintln(out, "Environment variables currently override saved values.")
	}
}

// validateServerURL accepts a path-free absolute HTTP or HTTPS server URL.
func validateServerURL(raw string) (string, error) {
	raw = normalizedServerURL(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid address %q: include http:// or https://", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("invalid address %q: scheme must be http or https", raw)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid address %q: credentials, query, and fragment are not allowed", raw)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("invalid address %q: path is not allowed", raw)
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

// ensureJSONEOF rejects trailing JSON values after the configuration object.
func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
