package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const maxPTConfigBytes = 64 << 10

type ptConfig struct {
	ServerURL   string `json:"serverUrl,omitempty"`
	DeployToken string `json:"deployToken,omitempty"`
}

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

func readPTConfig() (ptConfig, error) {
	path, err := ptConfigPath()
	if err != nil {
		return ptConfig{}, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ptConfig{}, nil
	}
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
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", fmt.Errorf("write pt config %q: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("secure pt config %q: %w", path, err)
	}
	return path, nil
}

func resolveConnection() (serverURL, deployToken string, err error) {
	cfg, err := readPTConfig()
	if err != nil {
		return "", "", err
	}
	serverURL = normalizedServerURL(firstNonEmpty(
		os.Getenv("PLUMTREE_SERVER_URL"),
		cfg.ServerURL,
		defaultServerURL,
		localServerURL,
	))
	deployToken = firstNonEmpty(
		os.Getenv("PLUMTREE_DEV_TOKEN"),
		cfg.DeployToken,
		defaultDevToken,
	)
	return serverURL, deployToken, nil
}

func cmdConfigure(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("configure", flag.ContinueOnError)
	fs.SetOutput(out)
	addr := fs.String("addr", "", "control-plane URL, including http:// or https://")
	token := fs.String("token", "", "deploy token")
	clearAddr := fs.Bool("clear-addr", false, "remove the saved control-plane URL")
	clearToken := fs.Bool("clear-token", false, "remove the saved deploy token")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: pt configure [--addr URL] [--token TOKEN] [--clear-addr] [--clear-token]")
	}

	setAddr, setToken := false, false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "addr":
			setAddr = true
		case "token":
			setToken = true
		}
	})
	if setAddr && *clearAddr {
		return errors.New("choose --addr or --clear-addr, not both")
	}
	if setToken && *clearToken {
		return errors.New("choose --token or --clear-token, not both")
	}

	cfg, err := readPTConfig()
	if err != nil {
		return err
	}
	changed := setAddr || setToken || *clearAddr || *clearToken
	if setAddr {
		cfg.ServerURL, err = validateServerURL(*addr)
		if err != nil {
			return err
		}
	}
	if setToken {
		cfg.DeployToken = strings.TrimSpace(*token)
		if cfg.DeployToken == "" {
			return errors.New("deploy token cannot be empty; use --clear-token to remove it")
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
