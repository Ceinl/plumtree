// Package cleanrole is the selected root server assembly. It loads one set of
// typed configuration values and gives role constructors immutable projections.
package cleanrole

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Ceinl/plumtree/internal/httpapi/v1"
	serverconfig "github.com/Ceinl/plumtree/internal/server/config"
	"github.com/Ceinl/plumtree/internal/sqlite"
	"github.com/Ceinl/plumtree/internal/transport"
	"golang.org/x/crypto/ssh"
)

const defaultProductVersion = "dev"

// ResolvedServe is the immutable selected input for server role constructors.
// Gateway and runner components can consume the same Config after their data
// plane is selected.
type ResolvedServe struct {
	serverconfig.Loaded
	ConfigPath     string
	ProductVersion string
	ServerID       string
}

// Run is the process adapter used by cmd/plumtree.
func Run(args []string) error {
	return Execute(context.Background(), args, os.Environ(), os.Stdout, os.Stderr)
}

// Execute runs a local config command or the selected control role.
func Execute(ctx context.Context, args, environment []string, out, errOut io.Writer) error {
	if len(args) > 0 && args[0] == "config" {
		return executeConfig(args[1:], environment, out)
	}
	resolved, err := ResolveServe(args, environment, 0)
	if err != nil {
		return err
	}
	for _, diagnostic := range serverconfig.Diagnostics(resolved.Config) {
		_, _ = fmt.Fprintf(errOut, "warning: %s: %s\n", diagnostic.Code, diagnostic.Message)
	}
	projection, err := serverconfig.MaterializeRole(resolved.Config, serverconfig.RoleControl)
	if err != nil {
		return fmt.Errorf("clean server: control configuration: %w", err)
	}
	component := &controlComponent{resolved: resolved, projection: projection, out: out}
	lifecycle := serverconfig.NewLifecycle(component)
	shutdownTimeout, _ := time.ParseDuration(resolved.Config.Runtime.ShutdownTimeout)
	err = lifecycle.RunWithSignals(ctx, shutdownTimeout)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// ResolveServe bootstraps and resolves the exact startup configuration. Precedence
// is flag, environment, persisted configuration, then typed default.
func ResolveServe(args, environment []string, hostMemory int64) (ResolvedServe, error) {
	if len(args) > 0 && args[0] == "serve" {
		args = args[1:]
	}
	env := environmentMap(environment)
	configPath := configPathFromArgs(args, env["PLUMTREE_CONFIG"])
	if configPath == "" {
		var err error
		configPath, err = DefaultConfigPath()
		if err != nil {
			return ResolvedServe{}, err
		}
	}
	if _, _, err := serverconfig.Bootstrap(configPath); err != nil {
		return ResolvedServe{}, fmt.Errorf("clean server: bootstrap config: %w", err)
	}

	fs := flag.NewFlagSet("plumtree serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	parsedConfigPath := configPath
	fs.StringVar(&parsedConfigPath, "config", configPath, "typed config file path")
	productVersion := firstNonEmpty(env["PLUMTREE_PRODUCT_VERSION"], defaultProductVersion)
	serverID := env["PLUMTREE_SERVER_ID"]
	fs.StringVar(&productVersion, "product-version", productVersion, "exact Plumtree product version")
	fs.StringVar(&serverID, "server-id", serverID, "stable server identity")
	overrides := make(map[string]string)
	for _, field := range serverconfig.FieldNames() {
		field := field
		fs.Func(serverconfig.FlagName(field), "one-run override for "+field, func(value string) error {
			overrides[field] = value
			return nil
		})
	}
	if err := fs.Parse(args); err != nil {
		return ResolvedServe{}, err
	}
	if fs.NArg() != 0 {
		return ResolvedServe{}, fmt.Errorf("clean server: unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if parsedConfigPath != configPath {
		return ResolvedServe{}, errors.New("clean server: --config must select the file before startup")
	}
	if strings.TrimSpace(productVersion) == "" {
		return ResolvedServe{}, errors.New("clean server: product version is required")
	}
	if hostMemory <= 0 {
		hostMemory = serverconfig.HostMemory()
	}
	loaded, err := serverconfig.Load(serverconfig.LoadOptions{
		Path: configPath, Environment: env, Flags: overrides,
		ReadFile: os.ReadFile, HostMemory: hostMemory,
	})
	if err != nil {
		return ResolvedServe{}, fmt.Errorf("clean server: load config: %w", err)
	}
	loaded.Config = serverconfig.ResolvePaths(loaded.Config, configPath)
	if err := loaded.Config.ValidateProduction(); err != nil {
		return ResolvedServe{}, fmt.Errorf("clean server: production validation: %w", err)
	}
	if _, err := serverconfig.NewControlRole(loaded.Config); err != nil {
		return ResolvedServe{}, fmt.Errorf("clean server: control role: %w", err)
	}
	return ResolvedServe{Loaded: loaded, ConfigPath: configPath, ProductVersion: productVersion, ServerID: serverID}, nil
}

func executeConfig(args, environment []string, out io.Writer) error {
	env := environmentMap(environment)
	path := configPathFromArgs(args, env["PLUMTREE_CONFIG"])
	if path == "" {
		var err error
		path, err = DefaultConfigPath()
		if err != nil {
			return err
		}
	}
	args = removeConfigArgs(args)
	if _, _, err := serverconfig.Bootstrap(path); err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New("usage: plumtree config show|set|unset")
	}
	switch args[0] {
	case "show":
		if len(args) != 1 {
			return errors.New("usage: plumtree config show [--config path]")
		}
		return serverconfig.RunConfigCommand([]string{"show", "--path", path}, out)
	case "set":
		if len(args) != 3 {
			return errors.New("usage: plumtree config set [--config path] <field> <value>")
		}
		return serverconfig.RunConfigCommand([]string{"set", "--path", path, "--field", args[1], "--value", args[2]}, out)
	case "unset":
		if len(args) != 2 {
			return errors.New("usage: plumtree config unset [--config path] <field>")
		}
		return serverconfig.RunConfigCommand([]string{"unset", "--path", path, "--field", args[1]}, out)
	default:
		return fmt.Errorf("usage: plumtree config show|set|unset: unknown command %q", args[0])
	}
}

// DefaultConfigPath returns the only implicit server configuration location.
func DefaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "plumtree", "config.json"), nil
}

func configPathFromArgs(args []string, fallback string) string {
	for i, arg := range args {
		if arg == "--config" || arg == "-config" {
			if i+1 < len(args) {
				return args[i+1]
			}
		}
		if value, ok := strings.CutPrefix(arg, "--config="); ok {
			return value
		}
		if value, ok := strings.CutPrefix(arg, "-config="); ok {
			return value
		}
	}
	return fallback
}

func removeConfigArgs(args []string) []string {
	result := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" || args[i] == "-config" {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "--config=") || strings.HasPrefix(args[i], "-config=") {
			continue
		}
		result = append(result, args[i])
	}
	return result
}

func environmentMap(values []string) map[string]string {
	result := make(map[string]string, len(values))
	for _, value := range values {
		key, item, ok := strings.Cut(value, "=")
		if ok {
			result[key] = item
		}
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type controlComponent struct {
	resolved      ResolvedServe
	projection    serverconfig.RoleProjection
	out           io.Writer
	repo          *sqlite.Repository
	listener      net.Listener
	sshConfig     *ssh.ServerConfig
	api           *v1.Server
	identity      sqlite.ServerIdentity
	errors        chan error
	wg            sync.WaitGroup
	connectionsMu sync.Mutex
	connections   map[net.Conn]struct{}
	admission     *connectionAdmission
}

func (c *controlComponent) Start(ctx context.Context) error {
	if !c.resolved.Config.Exposure.SSH.Enabled {
		return errors.New("clean server: selected control role requires exposure.ssh.enabled")
	}
	repo, err := openRepository(c.projection)
	if err != nil {
		return fmt.Errorf("clean server: open repository: %w", err)
	}
	c.repo = repo
	signer, fingerprint, err := loadOrCreateHostKey(c.resolved.Config.Storage.SSHIdentity)
	if err != nil {
		_ = repo.Close()
		return fmt.Errorf("clean server: host key: %w", err)
	}
	c.identity, err = ensureIdentity(ctx, repo, c.resolved.ServerID, signer, fingerprint)
	if err != nil {
		_ = repo.Close()
		return fmt.Errorf("clean server: identity: %w", err)
	}
	c.api, err = v1.New(v1.Config{Repository: repo, ProductVersion: c.resolved.ProductVersion})
	if err != nil {
		_ = repo.Close()
		return fmt.Errorf("clean server: API: %w", err)
	}
	c.listener, err = net.Listen("tcp", c.resolved.Config.Exposure.SSH.Address)
	if err != nil {
		_ = repo.Close()
		return fmt.Errorf("clean server: SSH listen: %w", err)
	}
	c.sshConfig = authenticatedSSHConfig(signer)
	c.errors = make(chan error, 1)
	c.connections = make(map[net.Conn]struct{})
	c.admission = newConnectionAdmission(c.resolved.Config.Limits.MaxConnections, c.resolved.Config.Limits.MaxConnectionsPerIP)
	c.wg.Add(1)
	go c.accept()
	return nil
}

func (c *controlComponent) Ready(context.Context) error {
	if c.repo == nil || c.listener == nil {
		return errors.New("clean server: control role is not ready")
	}
	_, _ = fmt.Fprintf(c.out, "plumtree server %s ready on %s\n", c.identity.ID, c.listener.Addr())
	return nil
}

func (c *controlComponent) Stop(ctx context.Context) error {
	if c.listener != nil {
		_ = c.listener.Close()
	}
	done := make(chan struct{})
	go func() { c.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		c.closeConnections()
		<-done
		if c.repo != nil {
			_ = c.repo.Close()
		}
		return ctx.Err()
	}
	if c.repo != nil {
		return c.repo.Close()
	}
	return nil
}

func (c *controlComponent) Errors() <-chan error { return c.errors }

func (c *controlComponent) accept() {
	defer c.wg.Done()
	for {
		conn, err := c.listener.Accept()
		if err != nil {
			select {
			case c.errors <- fmt.Errorf("clean server: accept SSH connection: %w", err):
			default:
			}
			return
		}
		clientIP := connectionIP(conn.RemoteAddr())
		if !c.admission.acquire(clientIP) {
			_ = conn.Close()
			continue
		}
		c.connectionsMu.Lock()
		c.connections[conn] = struct{}{}
		c.connectionsMu.Unlock()
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			defer c.admission.release(clientIP)
			defer func() {
				c.connectionsMu.Lock()
				delete(c.connections, conn)
				c.connectionsMu.Unlock()
			}()
			serveConnection(conn, c.sshConfig, c.repo, c.api, c.identity, c.resolved.ProductVersion, c.resolved.Config)
		}()
	}
}

func (c *controlComponent) closeConnections() {
	c.connectionsMu.Lock()
	defer c.connectionsMu.Unlock()
	for connection := range c.connections {
		_ = connection.Close()
	}
}

func openRepository(projection serverconfig.RoleProjection) (*sqlite.Repository, error) {
	return sqlite.OpenRepository(projection.Config().Storage.DatabasePath, projection.Secret())
}

func authenticatedSSHConfig(signer ssh.Signer) *ssh.ServerConfig {
	configuration := &ssh.ServerConfig{
		NoClientAuth: true,
		PublicKeyCallback: func(_ ssh.ConnMetadata, _ ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
		VerifiedPublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey, permissions *ssh.Permissions, _ string) (*ssh.Permissions, error) {
			if permissions == nil {
				return nil, errors.New("clean server: missing SSH permissions")
			}
			if permissions.Extensions == nil {
				permissions.Extensions = make(map[string]string)
			}
			permissions.Extensions["plumtree-fingerprint"] = ssh.FingerprintSHA256(key)
			return permissions, nil
		},
	}
	configuration.AddHostKey(signer)
	return configuration
}

func serveConnection(raw net.Conn, configuration *ssh.ServerConfig, repo *sqlite.Repository, api *v1.Server, identity sqlite.ServerIdentity, productVersion string, cfg serverconfig.Config) {
	defer raw.Close()
	idleTimeout, _ := time.ParseDuration(cfg.Limits.IdleTimeout)
	connection := newActivityConn(raw, idleTimeout)
	if timeout, _ := time.ParseDuration(cfg.Limits.HandshakeTimeout); timeout > 0 {
		_ = raw.SetDeadline(time.Now().Add(timeout))
	}
	serverConn, channels, requests, err := ssh.NewServerConn(connection, configuration)
	if err != nil {
		return
	}
	connection.enableIdleDeadline()
	defer serverConn.Close()
	go ssh.DiscardRequests(requests)
	fingerprint := ""
	if serverConn.Permissions != nil && serverConn.Permissions.Extensions != nil {
		fingerprint = serverConn.Permissions.Extensions["plumtree-fingerprint"]
	}
	var principal v1.Principal
	if fingerprint != "" {
		device, lookupErr := repo.DeviceByFingerprint(context.Background(), fingerprint)
		if lookupErr != nil {
			return
		}
		principal = v1.Principal{ServerID: identity.ID, AuthorID: device.AuthorID, DeviceID: device.ID, Fingerprint: device.Fingerprint}
	}
	for request := range channels {
		if request.ChannelType() != "session" {
			_ = request.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}
		channel, channelRequests, err := request.Accept()
		if err != nil {
			continue
		}
		go serveSession(channel, channelRequests, api, principal, productVersion)
	}
}

func serveSession(channel ssh.Channel, requests <-chan *ssh.Request, api *v1.Server, principal v1.Principal, productVersion string) {
	defer channel.Close()
	for request := range requests {
		if request.Type != "subsystem" {
			_ = request.Reply(false, nil)
			continue
		}
		var subsystem string
		if err := ssh.Unmarshal(request.Payload, &subsystem); err != nil || subsystem != transport.ControlSubsystem {
			_ = request.Reply(false, nil)
			continue
		}
		_ = request.Reply(true, nil)
		if principal.DeviceID == "" {
			return
		}
		handler := httpHandlerWithPrincipal(api.Handler(), principal)
		_ = transport.ServeHTTPStream(channel, handler, productVersion)
		return
	}
}

func httpHandlerWithPrincipal(handler http.Handler, principal v1.Principal) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		handler.ServeHTTP(writer, request.WithContext(v1.WithPrincipal(request.Context(), principal)))
	})
}

func ensureIdentity(ctx context.Context, repo *sqlite.Repository, requestedID string, signer ssh.Signer, fingerprint string) (sqlite.ServerIdentity, error) {
	identity, err := repo.ServerIdentity(ctx)
	if err == nil {
		if identity.SSHHostKeyAlgorithm != signer.PublicKey().Type() || identity.SSHHostKeyFingerprint != fingerprint {
			return sqlite.ServerIdentity{}, errors.New("persisted server identity does not match host key")
		}
		return identity, nil
	}
	if !errors.Is(err, sqlite.ErrNotFound) {
		return sqlite.ServerIdentity{}, err
	}
	if requestedID == "" {
		requestedID, err = randomIdentityID()
		if err != nil {
			return sqlite.ServerIdentity{}, err
		}
	}
	identity = sqlite.ServerIdentity{ID: requestedID, SSHHostKeyAlgorithm: signer.PublicKey().Type(), SSHHostKeyFingerprint: fingerprint}
	if err := repo.SetServerIdentity(ctx, identity); err != nil {
		return sqlite.ServerIdentity{}, err
	}
	return identity, nil
}

func randomIdentityID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "server_" + fmt.Sprintf("%x", raw[:]), nil
}

func loadOrCreateHostKey(path string) (ssh.Signer, string, error) {
	if data, err := os.ReadFile(path); err == nil {
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return nil, "", err
		}
		return signer, ssh.FingerprintSHA256(signer.PublicKey()), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	block, err := ssh.MarshalPrivateKey(privateKey, "plumtree server host key")
	if err != nil {
		return nil, "", err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, "", err
		}
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
		return nil, "", err
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, "", err
	}
	return signer, ssh.FingerprintSHA256(signer.PublicKey()), nil
}
