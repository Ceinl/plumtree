// Package cleanrole is the selected root server assembly. It loads one set of
// typed configuration values and gives role constructors immutable projections.
package cleanrole

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ceinl/plumtree/internal/gateway"
	"github.com/Ceinl/plumtree/internal/hostkey"
	"github.com/Ceinl/plumtree/internal/httpapi/v1"
	"github.com/Ceinl/plumtree/internal/runner"
	serverconfig "github.com/Ceinl/plumtree/internal/server/config"
	identityservice "github.com/Ceinl/plumtree/internal/server/identity"
	pairingserver "github.com/Ceinl/plumtree/internal/server/pairing"
	"github.com/Ceinl/plumtree/internal/sqlite"
	statebundle "github.com/Ceinl/plumtree/internal/state"
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

// Execute runs a local config or bootstrap command, an operator command, or
// the selected control role.
func Execute(ctx context.Context, args, environment []string, out, errOut io.Writer) error {
	if len(args) > 0 && isHelp(args[0]) {
		return writeHelp(out, "")
	}
	if len(args) > 0 && args[0] == "help" {
		if len(args) == 1 || (len(args) == 2 && isHelp(args[1])) {
			return writeHelp(out, "")
		}
		if len(args) == 2 {
			return writeHelp(out, args[1])
		}
		if len(args) == 3 && args[1] == "author" && args[2] == "bootstrap" {
			return writeHelp(out, "bootstrap")
		}
		return errors.New("usage: plumtree help [command]")
	}
	if len(args) > 1 && isKnownTopCommand(args[0]) {
		for _, arg := range args[1:] {
			if isHelp(arg) {
				topic := args[0]
				if topic == "author" {
					topic = "bootstrap"
				}
				return writeHelp(out, topic)
			}
		}
	}
	if len(args) > 0 && strings.HasPrefix(args[0], "-") {
		for _, arg := range args {
			if isHelp(arg) {
				return writeHelp(out, "serve")
			}
		}
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") && !isKnownTopCommand(args[0]) {
		return fmt.Errorf("unknown plumtree command %q", args[0])
	}
	if len(args) > 0 && args[0] == "config" {
		return executeConfig(args[1:], environment, out)
	}
	if len(args) > 0 && args[0] == "bootstrap" {
		return runBootstrap(ctx, args[1:], environment, out)
	}
	if len(args) > 1 && args[0] == "author" && args[1] == "bootstrap" {
		return runBootstrap(ctx, args[2:], environment, out)
	}
	if len(args) > 0 && args[0] == "state" {
		return runState(ctx, args[1:], environment, out)
	}
	if len(args) > 0 {
		switch args[0] {
		case "suspend", "unsuspend":
			return executeSuspension(ctx, args[0], args[1:], environment, out)
		case "quota":
			return executeQuota(ctx, args[1:], environment, out)
		}
	}
	resolved, err := ResolveServe(args, environment, 0)
	if err != nil {
		return err
	}
	for _, diagnostic := range serverconfig.Diagnostics(resolved.Config) {
		_, _ = fmt.Fprintf(errOut, "warning: %s: %s\n", diagnostic.Code, diagnostic.Message)
	}
	projection, err := serverconfig.MaterializeRole(resolved.Config, serverconfig.RoleControl)
	if resolved.Config.Roles.Runner && !resolved.Config.Roles.Control && !resolved.Config.Roles.Gateway {
		projection, err = serverconfig.MaterializeRole(resolved.Config, serverconfig.RoleRunner)
		if err != nil {
			return fmt.Errorf("clean server: runner configuration: %w", err)
		}
		component := &runnerComponent{projection: projection, out: out, environ: environment}
		return runLifecycle(ctx, resolved.Config, component)
	}
	if err != nil {
		return fmt.Errorf("clean server: control configuration: %w", err)
	}
	var gatewayToken []byte
	if resolved.Config.Roles.Gateway {
		gatewayProjection, gatewayErr := serverconfig.MaterializeRole(resolved.Config, serverconfig.RoleGateway)
		if gatewayErr != nil {
			return fmt.Errorf("clean server: gateway configuration: %w", gatewayErr)
		}
		gatewayToken = gatewayProjection.Secret()
	}
	component := &controlComponent{resolved: resolved, projection: projection, gatewayToken: gatewayToken, out: out}
	return runLifecycle(ctx, resolved.Config, component)
}

func runLifecycle(ctx context.Context, cfg serverconfig.Config, component serverconfig.Component) error {
	lifecycle := serverconfig.NewLifecycle(component)
	shutdownTimeout, parseErr := time.ParseDuration(cfg.Runtime.ShutdownTimeout)
	if parseErr != nil || shutdownTimeout <= 0 {
		shutdownTimeout, _ = time.ParseDuration(serverconfig.Default().Runtime.ShutdownTimeout)
	}
	err := lifecycle.RunWithSignals(ctx, shutdownTimeout)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func runBootstrap(ctx context.Context, args, environment []string, out io.Writer) error {
	fs := flag.NewFlagSet("plumtree bootstrap", flag.ContinueOnError)
	database := fs.String("database", "", "path to the Plumtree SQLite database")
	configPath := fs.String("config", "", "typed config file path")
	handle := fs.String("handle", "", "author handle bound to this authority")
	device := fs.String("device", "device", "first device name")
	ttl := fs.Duration("ttl", 10*time.Minute, "one-use authority lifetime")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *handle == "" || (*database != "" && *configPath != "") {
		return errors.New("usage: plumtree bootstrap [-config PATH | -database PATH] -handle HANDLE [-device NAME] [-ttl 10m]")
	}
	databasePath := *database
	var databaseKey []byte
	if *configPath != "" {
		projection, err := loadControlProjection(*configPath, environment)
		if err != nil {
			return err
		}
		if err := ensurePrivateDirectory(projection.Config().Storage.KVRoot); err != nil {
			return fmt.Errorf("clean server: prepare KV root: %w", err)
		}
		databasePath = projection.Config().Storage.DatabasePath
		databaseKey = projection.Secret()
	}
	if databasePath == "" {
		databasePath = "plumtree.db"
	}
	result, err := Bootstrap(ctx, BootstrapConfig{Database: databasePath, DatabaseKey: databaseKey, Handle: *handle, DeviceName: *device, TTL: *ttl})
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(map[string]any{"bootstrapID": result.ID, "handle": result.Handle, "deviceName": result.DeviceName, "secret": string(result.Secret), "expiresAt": result.ExpiresAt})
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("directory must be private and must not be a symlink")
	}
	return nil
}

func loadControlProjection(configPath string, environment []string) (serverconfig.RoleProjection, error) {
	loaded, err := serverconfig.Load(serverconfig.LoadOptions{
		Path: configPath, Environment: environmentMap(environment), ReadFile: os.ReadFile, HostMemory: serverconfig.HostMemory(),
	})
	if err != nil {
		return serverconfig.RoleProjection{}, fmt.Errorf("clean server: load config: %w", err)
	}
	loaded.Config = serverconfig.ResolvePaths(loaded.Config, configPath)
	if err := loaded.Config.ValidateProduction(); err != nil {
		return serverconfig.RoleProjection{}, fmt.Errorf("clean server: production validation: %w", err)
	}
	if !loaded.Config.Roles.Control {
		return serverconfig.RoleProjection{}, errors.New("clean server: control role is required")
	}
	projection, err := serverconfig.MaterializeRole(loaded.Config, serverconfig.RoleControl)
	if err != nil {
		return serverconfig.RoleProjection{}, fmt.Errorf("clean server: control configuration: %w", err)
	}
	return projection, nil
}

func runState(ctx context.Context, args, environment []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: plumtree state inventory|backup|restore")
	}
	command := args[0]
	fs := flag.NewFlagSet("plumtree state "+command, flag.ContinueOnError)
	configPath := fs.String("config", "", "typed config file path")
	output := fs.String("output", "", "backup bundle path")
	input := fs.String("input", "", "backup bundle path")
	yes := fs.Bool("yes", false, "confirm a destructive restore")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: plumtree state inventory|backup|restore")
	}
	if *configPath == "" {
		*configPath = environmentMap(environment)["PLUMTREE_CONFIG"]
	}
	if *configPath == "" {
		var err error
		*configPath, err = DefaultConfigPath()
		if err != nil {
			return err
		}
	}
	projection, err := loadControlProjection(*configPath, environment)
	if err != nil {
		return err
	}
	cfg := projection.Config()
	paths := statebundle.Paths{Database: cfg.Storage.DatabasePath, KVRoot: cfg.Storage.KVRoot, SSHIdentity: cfg.Storage.SSHIdentity}
	key := projection.Secret()

	switch command {
	case "inventory":
		if *input != "" || *output != "" || *yes {
			return errors.New("usage: plumtree state inventory [-config PATH]")
		}
		report, err := statebundle.Inventory(ctx, paths, key)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(report)
	case "backup":
		if *output == "" || *input != "" || *yes {
			return errors.New("usage: plumtree state backup [-config PATH] -output PATH")
		}
		if err := statebundle.Backup(ctx, paths, *output, key, statebundle.Options{}); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"status": "backed-up", "output": *output})
	case "restore":
		if *input == "" || *output != "" || !*yes {
			return errors.New("usage: plumtree state restore [-config PATH] -input PATH -yes")
		}
		if err := statebundle.Restore(ctx, *input, paths, key, statebundle.Options{}); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"status": "restored", "input": *input})
	default:
		return fmt.Errorf("usage: plumtree state inventory|backup|restore: unknown command %q", command)
	}
}

// splitOperatorStorageFlags extracts operator storage flags so positional
// arguments can appear before or after them.
func splitOperatorStorageFlags(args []string) (database, configPath string, databaseSet bool, rest []string, err error) {
	database = "plumtree.db"
	for i := 0; i < len(args); i++ {
		if value, ok := strings.CutPrefix(args[i], "--database="); ok {
			database = value
			databaseSet = true
			continue
		}
		if value, ok := strings.CutPrefix(args[i], "-database="); ok {
			database = value
			databaseSet = true
			continue
		}
		if value, ok := strings.CutPrefix(args[i], "--config="); ok {
			configPath = value
			continue
		}
		if value, ok := strings.CutPrefix(args[i], "-config="); ok {
			configPath = value
			continue
		}
		if args[i] == "--database" || args[i] == "-database" || args[i] == "--config" || args[i] == "-config" {
			if i+1 >= len(args) {
				return "", "", false, nil, errors.New("operator storage flag requires a path")
			}
			if strings.Contains(args[i], "database") {
				database = args[i+1]
				databaseSet = true
			} else {
				configPath = args[i+1]
			}
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	if databaseSet && configPath != "" {
		return "", "", false, nil, errors.New("operator command accepts either -config or -database, not both")
	}
	return database, configPath, databaseSet, rest, nil
}

func resolveOperatorStorage(database, configPath string, databaseSet bool, environment []string) (string, []byte, error) {
	if configPath == "" && !databaseSet {
		configPath = environmentMap(environment)["PLUMTREE_CONFIG"]
	}
	if configPath == "" {
		return database, nil, nil
	}
	projection, err := loadControlProjection(configPath, environment)
	if err != nil {
		return "", nil, err
	}
	return projection.Config().Storage.DatabasePath, projection.Secret(), nil
}

// executeSuspension applies the operator kill switch to one deployment. The
// gateway picks the change up live through its suspension watcher.
func executeSuspension(ctx context.Context, command string, args, environment []string, out io.Writer) error {
	database, configPath, databaseSet, positionals, parseErr := splitOperatorStorageFlags(args)
	usage := fmt.Sprintf("usage: plumtree %s deploy <id> [-config PATH | -database PATH]", command)
	if parseErr != nil {
		return fmt.Errorf("%s: %w", usage, parseErr)
	}
	if len(positionals) != 2 || positionals[0] != "deploy" {
		return errors.New(usage)
	}
	database, key, err := resolveOperatorStorage(database, configPath, databaseSet, environment)
	if err != nil {
		return err
	}
	repository, err := sqlite.OpenRepository(database, key)
	if err != nil {
		return err
	}
	defer repository.Close()
	suspended := command == "suspend"
	if err := repository.SetDeploymentSuspended(ctx, positionals[1], suspended); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(map[string]any{"deployment": positionals[1], "suspended": suspended})
}

// executeQuota sets one author's typed resource quotas in a single write.
func executeQuota(ctx context.Context, args, environment []string, out io.Writer) error {
	database, configPath, databaseSet, positionals, parseErr := splitOperatorStorageFlags(args)
	const usage = "usage: plumtree quota set <authorID> <maxApps> <maxDeploymentsPerApp> <maxSecretsPerApp> <maxSessions> [-config PATH | -database PATH]"
	if parseErr != nil {
		return fmt.Errorf("%s: %w", usage, parseErr)
	}
	if len(positionals) != 6 || positionals[0] != "set" {
		return errors.New(usage)
	}
	fields := []string{"maxApps", "maxDeploymentsPerApp", "maxSecretsPerApp", "maxSessions"}
	values := make([]int, len(fields))
	for i, raw := range positionals[2:] {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return fmt.Errorf("%w: %s must be a non-negative integer", sqlite.ErrInvalid, fields[i])
		}
		values[i] = value
	}
	database, key, err := resolveOperatorStorage(database, configPath, databaseSet, environment)
	if err != nil {
		return err
	}
	repository, err := sqlite.OpenRepository(database, key)
	if err != nil {
		return err
	}
	defer repository.Close()
	quota := sqlite.Quota{AuthorID: positionals[1], MaxApps: values[0], MaxDeploymentsPerApp: values[1], MaxSecretsPerApp: values[2], MaxSessions: values[3]}
	if err := repository.SetQuota(ctx, quota); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(map[string]any{"author": quota.AuthorID, "maxApps": quota.MaxApps,
		"maxDeploymentsPerApp": quota.MaxDeploymentsPerApp, "maxSecretsPerApp": quota.MaxSecretsPerApp, "maxSessions": quota.MaxSessions})
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
	if value := hostKeyFlagValue(args); value != "" {
		overrides["storage.sshIdentity"] = value
	} else if value := env["PLUMTREE_HOST_KEY"]; value != "" {
		overrides["storage.sshIdentity"] = value
	}
	if value := hostCommandAllowlistFlagValue(args); value != "" {
		overrides["runtime.hostCommandAllowlist"] = value
	} else if value := env["PLUMTREE_HOST_COMMAND_ALLOWLIST"]; value != "" {
		overrides["runtime.hostCommandAllowlist"] = value
	}
	if err := fs.Parse(stripHostKeyArgs(stripHostCommandArgs(args))); err != nil {
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
	if !loaded.Config.Roles.Control && !loaded.Config.Roles.Gateway && !loaded.Config.Roles.Runner {
		return ResolvedServe{}, errors.New("clean server: at least one role is required")
	}
	for _, role := range []struct {
		name    serverconfig.RoleName
		enabled bool
	}{{serverconfig.RoleControl, loaded.Config.Roles.Control}, {serverconfig.RoleGateway, loaded.Config.Roles.Gateway}, {serverconfig.RoleRunner, loaded.Config.Roles.Runner}} {
		if role.enabled {
			if _, err := serverconfig.NewRole(loaded.Config, role.name); err != nil {
				return ResolvedServe{}, fmt.Errorf("clean server: %s role: %w", role.name, err)
			}
		}
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

// hostKeyFlagValue extracts the -host-key operator alias for storage.sshIdentity.
// It is parsed manually because the mechanical flag set only carries schema fields.
func hostKeyFlagValue(args []string) string {
	for i, arg := range args {
		if arg == "--host-key" || arg == "-host-key" {
			if i+1 < len(args) {
				return args[i+1]
			}
		}
		if value, ok := strings.CutPrefix(arg, "--host-key="); ok {
			return value
		}
		if value, ok := strings.CutPrefix(arg, "-host-key="); ok {
			return value
		}
	}
	return ""
}

// stripHostKeyArgs removes the manual -host-key alias so the mechanical flag
// set never sees an unknown flag.
func stripHostKeyArgs(args []string) []string {
	result := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--host-key" || args[i] == "-host-key" {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "--host-key=") || strings.HasPrefix(args[i], "-host-key=") {
			continue
		}
		result = append(result, args[i])
	}
	return result
}

// hostCommandAllowlistFlagValue extracts the manual -host-command-allowlist
// operator alias for runtime.hostCommandAllowlist. Like -host-key it is parsed
// by hand because the mechanical flag set only carries schema spellings.
func hostCommandAllowlistFlagValue(args []string) string {
	for i, arg := range args {
		if arg == "--host-command-allowlist" || arg == "-host-command-allowlist" {
			if i+1 < len(args) {
				return args[i+1]
			}
		}
		if value, ok := strings.CutPrefix(arg, "--host-command-allowlist="); ok {
			return value
		}
		if value, ok := strings.CutPrefix(arg, "-host-command-allowlist="); ok {
			return value
		}
	}
	return ""
}

// stripHostCommandArgs removes the manual -host-command-allowlist alias so the
// mechanical flag set never sees an unknown flag.
func stripHostCommandArgs(args []string) []string {
	result := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--host-command-allowlist" || args[i] == "-host-command-allowlist" {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "--host-command-allowlist=") || strings.HasPrefix(args[i], "-host-command-allowlist=") {
			continue
		}
		result = append(result, args[i])
	}
	return result
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
	gatewayToken  []byte
	out           io.Writer
	repo          *sqlite.Repository
	listener      net.Listener
	sshConfig     *ssh.ServerConfig
	api           *v1.Server
	identities    *identityservice.Service
	leaf          *gateway.Server
	identity      sqlite.ServerIdentity
	errors        chan error
	wg            sync.WaitGroup
	connectionsMu sync.Mutex
	connections   map[net.Conn]struct{}
	admission     *connectionAdmission
	ready         func(string)
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
	c.identities, err = newIdentityService(repo, c.resolved.Config.Exposure.SSH.Address)
	if err != nil {
		_ = repo.Close()
		return fmt.Errorf("clean server: identity service: %w", err)
	}
	c.api, err = v1.New(v1.Config{Repository: repo, Identity: c.identities, ProductVersion: c.resolved.ProductVersion})
	if err != nil {
		_ = repo.Close()
		return fmt.Errorf("clean server: API: %w", err)
	}
	c.leaf = newLeafServer(repo, c.resolved.Config, c.gatewayToken)
	if err := c.leaf.Start(ctx); err != nil {
		_ = repo.Close()
		return fmt.Errorf("clean server: leaf gateway: %w", err)
	}
	var listenConfig net.ListenConfig
	c.listener, err = listenConfig.Listen(ctx, "tcp", c.resolved.Config.Exposure.SSH.Address)
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
	if c.ready != nil {
		c.ready(c.listener.Addr().String())
	}
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
			serveConnection(conn, c.sshConfig, c.repo, c.identities, c.api, c.leaf, c.identity, c.resolved.ProductVersion, c.resolved.Config)
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
			permissions.Extensions["plumtree-public-key"] = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
			return permissions, nil
		},
		KeyboardInteractiveCallback: func(_ ssh.ConnMetadata, _ ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			return &ssh.Permissions{Extensions: map[string]string{"plumtree-auth-kind": "anonymous"}}, nil
		},
	}
	configuration.AddHostKey(signer)
	return configuration
}

func serveConnection(raw net.Conn, configuration *ssh.ServerConfig, repo *sqlite.Repository, identities *identityservice.Service, api *v1.Server, leaf *gateway.Server, identity sqlite.ServerIdentity, productVersion string, cfg serverconfig.Config) {
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
	publicKey := ""
	if serverConn.Permissions != nil && serverConn.Permissions.Extensions != nil {
		fingerprint = serverConn.Permissions.Extensions["plumtree-fingerprint"]
		publicKey = serverConn.Permissions.Extensions["plumtree-public-key"]
	}
	var principal v1.Principal
	leafIdentity := runner.Identity{User: "anonymous:" + base64.RawStdEncoding.EncodeToString(serverConn.SessionID()), Kind: runner.IdentityAnonymous}
	if fingerprint != "" {
		leafIdentity = runner.Identity{User: fingerprint, Kind: runner.IdentitySSHKey}
		device, lookupErr := repo.DeviceByFingerprint(context.Background(), fingerprint)
		if lookupErr != nil && !errors.Is(lookupErr, sqlite.ErrNotFound) {
			return
		}
		if lookupErr == nil {
			principal = v1.Principal{ServerID: identity.ID, AuthorID: device.AuthorID, DeviceID: device.ID, Fingerprint: device.Fingerprint}
			leafIdentity.Authenticated = true
			leafIdentity.OwnerID = device.AuthorID
		}
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
		go serveSession(channel, channelRequests, serverConn.User(), leafIdentity, identities, api, leaf, principal, identity, productVersion, base64.RawStdEncoding.EncodeToString(serverConn.SessionID()), publicKey, fingerprint)
	}
}

func serveSession(channel ssh.Channel, requests <-chan *ssh.Request, handle string, leafIdentity runner.Identity, identities *identityservice.Service, api *v1.Server, leaf *gateway.Server, principal v1.Principal, identity sqlite.ServerIdentity, productVersion, sessionID, publicKey, fingerprint string) {
	defer channel.Close()
	for request := range requests {
		if request.Type != "subsystem" {
			prefixed := make(chan *ssh.Request)
			go func(first *ssh.Request) {
				defer close(prefixed)
				prefixed <- first
				for next := range requests {
					prefixed <- next
				}
			}(request)
			leaf.HandleSession(context.Background(), channel, prefixed, handle, leafIdentity)
			return
		}
		var subsystemRequest struct{ Name string }
		if err := ssh.Unmarshal(request.Payload, &subsystemRequest); err != nil {
			_ = request.Reply(false, nil)
			continue
		}
		switch subsystemRequest.Name {
		case transport.ControlSubsystem:
			if principal.DeviceID == "" {
				_ = request.Reply(false, nil)
				return
			}
			_ = request.Reply(true, nil)
			handler := httpHandlerWithPrincipal(api.Handler(), principal)
			_ = transport.ServeHTTPStream(channel, handler, productVersion)
			return
		case transport.PairSubsystem:
			if principal.DeviceID != "" || publicKey == "" || fingerprint == "" {
				_ = request.Reply(false, nil)
				return
			}
			_ = request.Reply(true, nil)
			handler := pairingserver.Handler{Identity: identities, ServerID: identity.ID, HostKeyAlgorithm: identity.SSHHostKeyAlgorithm,
				HostKeyFingerprint: identity.SSHHostKeyFingerprint, ProductVersion: productVersion, SessionID: sessionID,
				CandidatePublicKey: publicKey, CandidateFingerprint: fingerprint}
			_ = handler.Serve(channel)
			return
		default:
			_ = request.Reply(false, nil)
			return
		}
	}
}

func newLeafServer(repo *sqlite.Repository, cfg serverconfig.Config, runnerToken []byte) *gateway.Server {
	limits := runner.Limits{
		MemoryPages:     uint32(cfg.Limits.MemoryPages),
		MaxEventsPerSec: cfg.Limits.MaxEventsPerSec,
		MaxFramesPerSec: cfg.Limits.MaxFramesPerSec,
	}
	limits.SessionTimeout, _ = time.ParseDuration(cfg.Limits.SessionTimeout)
	limits.FrameTimeout, _ = time.ParseDuration(cfg.Limits.FrameTimeout)
	allowlist := parseHostCommandAllowlist(cfg.Runtime.HostCommandAllowlist)
	return &gateway.Server{
		Backend: gateway.NewSQLiteBackend(repo), Runner: runner.New(), Limits: limits,
		MaxFPS: cfg.Limits.MaxFPS, MaxConcurrentSessions: cfg.Limits.MaxSessions,
		RunnerEndpoint: cfg.Runtime.RunnerEndpoint, RunnerToken: strings.TrimSpace(string(runnerToken)),
		EnableHostCommands: len(allowlist) > 0, HostCommandAllowlist: allowlist,
	}
}

// parseHostCommandAllowlist splits the operator's CSV allowlist, trimming
// whitespace and dropping empty entries so "a,, b" and "" behave sanely.
func parseHostCommandAllowlist(raw string) []string {
	var out []string
	for _, entry := range strings.Split(raw, ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			out = append(out, entry)
		}
	}
	return out
}

func newIdentityService(repo *sqlite.Repository, sshAddress string) (*identityservice.Service, error) {
	cfg := serverconfig.Default()
	cfg.Roles.Control = true
	cfg.Exposure.SSH = serverconfig.ExposureGate{Enabled: true, Address: sshAddress}
	return identityservice.New(repo, cfg)
}

func httpHandlerWithPrincipal(handler http.Handler, principal v1.Principal) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		handler.ServeHTTP(writer, request.WithContext(v1.WithPrincipal(request.Context(), principal)))
	})
}

type ServeConfig struct {
	Database, SSHAddress, HostKeyPath, ServerID, ProductVersion string
	Ready                                                       func(string)
}

// Serve keeps the programmatic native server seam used by qualification tests.
// Normal process startup goes through Execute and the typed configuration file.
func Serve(ctx context.Context, cfg ServeConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	configuration := serverconfig.Default()
	configuration.Roles.Control = true
	configuration.Storage.DatabasePath = cfg.Database
	configuration.Storage.SSHIdentity = cfg.HostKeyPath
	configuration.Exposure.SSH = serverconfig.ExposureGate{Enabled: true, Address: cfg.SSHAddress}
	projection, err := serverconfig.MaterializeRole(configuration, serverconfig.RoleControl)
	if err != nil {
		return err
	}
	component := &controlComponent{
		resolved:   ResolvedServe{Loaded: serverconfig.Loaded{Config: configuration}, ProductVersion: cfg.ProductVersion, ServerID: cfg.ServerID},
		projection: projection,
		out:        io.Discard,
		ready:      cfg.Ready,
	}
	if err := component.Start(ctx); err != nil {
		return err
	}
	if err := component.Ready(ctx); err != nil {
		_ = component.Stop(context.Background())
		return err
	}
	select {
	case <-ctx.Done():
	case err := <-component.Errors():
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
	}
	component.closeConnections()
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return component.Stop(stopCtx)
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
	signer, err := hostkey.LoadOrCreate(path, "plumtree server host key")
	if err != nil {
		return nil, "", err
	}
	return signer, ssh.FingerprintSHA256(signer.PublicKey()), nil
}
