package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

const maxPTConfigBytes = 64 << 10

type ptConfig struct {
	Servers []ptServer `json:"servers,omitempty"`
	// ServerURL and DeployToken are retained so existing single-server config
	// files can be read and migrated the next time a server is added.
	ServerURL   string `json:"serverUrl,omitempty"`
	DeployToken string `json:"deployToken,omitempty"`
}

type ptServer struct {
	Alias       string `json:"alias"`
	ServerURL   string `json:"serverUrl"`
	DeployToken string `json:"deployToken"`
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
	if err := validatePTConfigSecurity(path, info); err != nil {
		return ptConfig{}, fmt.Errorf("pt config %q: %w", path, err)
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
	aliases := make(map[string]struct{}, len(cfg.Servers))
	for i := range cfg.Servers {
		cfg.Servers[i].ServerURL, err = validateServerURL(cfg.Servers[i].ServerURL)
		if err != nil {
			return ptConfig{}, fmt.Errorf("pt config %q: server %q: %w", path, cfg.Servers[i].Alias, err)
		}
		if err := validateServerAlias(cfg.Servers[i].Alias); err != nil {
			return ptConfig{}, fmt.Errorf("pt config %q: %w", path, err)
		}
		if _, exists := aliases[cfg.Servers[i].Alias]; exists {
			return ptConfig{}, fmt.Errorf("pt config %q: duplicate server alias %q", path, cfg.Servers[i].Alias)
		}
		aliases[cfg.Servers[i].Alias] = struct{}{}
		if _, err := requireDeployToken(cfg.Servers[i].DeployToken); err != nil {
			return ptConfig{}, fmt.Errorf("pt config %q: server %q: %w", path, cfg.Servers[i].Alias, err)
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
	aliases := make(map[string]struct{}, len(cfg.Servers))
	for i := range cfg.Servers {
		if err := validateServerAlias(cfg.Servers[i].Alias); err != nil {
			return "", err
		}
		if _, exists := aliases[cfg.Servers[i].Alias]; exists {
			return "", fmt.Errorf("duplicate server alias %q", cfg.Servers[i].Alias)
		}
		aliases[cfg.Servers[i].Alias] = struct{}{}
		cfg.Servers[i].ServerURL, err = validateServerURL(cfg.Servers[i].ServerURL)
		if err != nil {
			return "", fmt.Errorf("server %q: %w", cfg.Servers[i].Alias, err)
		}
		cfg.Servers[i].DeployToken, err = requireDeployToken(cfg.Servers[i].DeployToken)
		if err != nil {
			return "", fmt.Errorf("server %q: %w", cfg.Servers[i].Alias, err)
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
	if err := securePTConfigFile(tmpPath); err != nil {
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
	if err := securePTConfigFile(path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil {
			return "", fmt.Errorf("secure pt config %q: %w (remove failed: %v)", path, err, removeErr)
		}
		return "", fmt.Errorf("secure pt config %q: %w", path, err)
	}
	removeTemp = false
	return path, nil
}

// resolveConnection applies environment, saved, baked, and local defaults in order.
func resolveConnection() (serverURL, deployToken string, err error) {
	return resolveConnectionForAlias("")
}

// resolveConnectionForAlias resolves a named saved server. An empty alias uses
// environment overrides, then the first saved server, then legacy defaults.
func resolveConnectionForAlias(alias string) (serverURL, deployToken string, err error) {
	cfg, err := readPTConfig()
	if err != nil {
		return "", "", err
	}
	if alias != "" {
		for _, server := range cfg.Servers {
			if server.Alias == alias {
				return server.ServerURL, server.DeployToken, nil
			}
		}
		return "", "", fmt.Errorf("unknown server alias %q; add it with `pt --add-server <addr> %s`", alias, alias)
	}
	savedURL, savedToken := cfg.ServerURL, cfg.DeployToken
	if len(cfg.Servers) != 0 {
		savedURL, savedToken = cfg.Servers[0].ServerURL, cfg.Servers[0].DeployToken
	}
	usingLocalDefault := firstNonEmpty(
		os.Getenv("PLUMTREE_SERVER_URL"),
		savedURL,
		defaultServerURL,
	) == ""
	rawServerURL := firstNonEmpty(
		os.Getenv("PLUMTREE_SERVER_URL"),
		savedURL,
		defaultServerURL,
		localServerURL,
	)
	serverURL, err = validateServerURL(rawServerURL)
	if err != nil {
		return "", "", err
	}
	deployToken = firstNonEmpty(
		os.Getenv("PLUMTREE_DEV_TOKEN"),
		savedToken,
		defaultDevToken,
	)
	if deployToken == "" && usingLocalDefault {
		if _, explicitlySet := os.LookupEnv("PLUMTREE_DEV_TOKEN"); !explicitlySet {
			deployToken, err = readLocalDevToken()
			if err != nil {
				return "", "", err
			}
		}
	}
	return serverURL, deployToken, nil
}

// resolveConnectionForServerURL finds credentials for the server recorded in
// per-project deploy metadata. This keeps follow-up commands working after a
// deploy to a non-default server.
func resolveConnectionForServerURL(target string) (serverURL, deployToken string, err error) {
	if target == "" || strings.TrimSpace(os.Getenv("PLUMTREE_SERVER_URL")) != "" {
		return resolveConnection()
	}
	resolvedURL, resolvedToken, err := resolveConnection()
	if err != nil {
		return "", "", err
	}
	if normalizedServerURL(resolvedURL) == normalizedServerURL(target) {
		return resolvedURL, resolvedToken, nil
	}
	cfg, err := readPTConfig()
	if err != nil {
		return "", "", err
	}
	for _, server := range cfg.Servers {
		if normalizedServerURL(server.ServerURL) == normalizedServerURL(target) {
			return server.ServerURL, server.DeployToken, nil
		}
	}
	if normalizedServerURL(cfg.ServerURL) == normalizedServerURL(target) {
		return cfg.ServerURL, cfg.DeployToken, nil
	}
	return "", "", fmt.Errorf("no saved credentials for deploy server %q", target)
}

// cmdAddServer appends a named server. Config order is meaningful: the first
// entry is the default used when -s is omitted.
func cmdAddServer(args []string, in io.Reader, out io.Writer) error {
	if len(args) != 2 {
		return errors.New("usage: pt --add-server <addr> <server_alias>")
	}
	serverURL, err := validateServerURL(args[0])
	if err != nil {
		return err
	}
	alias := strings.TrimSpace(args[1])
	if err := validateServerAlias(alias); err != nil {
		return err
	}
	cfg, err := readPTConfig()
	if err != nil {
		return err
	}
	token, err := readDeployToken(in, out)
	if err != nil {
		return err
	}
	// Promote a legacy single-server config into the ordered list.
	if len(cfg.Servers) == 0 && cfg.ServerURL != "" && cfg.DeployToken != "" {
		legacyAlias := "main"
		if alias == legacyAlias {
			legacyAlias = "legacy"
		}
		cfg.Servers = append(cfg.Servers, ptServer{Alias: legacyAlias, ServerURL: cfg.ServerURL, DeployToken: cfg.DeployToken})
	}
	for _, server := range cfg.Servers {
		if server.Alias == alias {
			return fmt.Errorf("server alias %q already exists", alias)
		}
	}
	cfg.Servers = append(cfg.Servers, ptServer{Alias: alias, ServerURL: serverURL, DeployToken: token})
	cfg.ServerURL, cfg.DeployToken = "", ""
	path, err := writePTConfig(cfg)
	if err != nil {
		return err
	}
	role := ""
	if len(cfg.Servers) == 1 {
		role = " (main)"
	}
	fmt.Fprintf(out, "Added server %s%s at %s\n", alias, role, serverURL)
	fmt.Fprintf(out, "Saved pt configuration to %s\n", path)
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

func validateServerAlias(alias string) error {
	if alias == "" {
		return errors.New("server alias cannot be empty")
	}
	for i, r := range alias {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || (i > 0 && (r == '-' || r == '_' || r == '.')) {
			continue
		}
		return fmt.Errorf("invalid server alias %q: use a letter or number followed by letters, numbers, '.', '-', or '_'", alias)
	}
	return nil
}

// requireDeployToken trims and rejects an empty token value.
func requireDeployToken(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("deploy token cannot be empty")
	}
	return value, nil
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
