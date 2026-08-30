// Package config owns Plumtree's typed, restart-only server configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const FormatVersion = 1

var (
	ErrInvalid       = errors.New("config: invalid configuration")
	ErrUnsafePath    = errors.New("config: unsafe path")
	ErrAlreadyExists = errors.New("config: already initialized")
)

// Config is concern-oriented JSON. Unknown fields are rejected by Read.
type Config struct {
	Version   int       `json:"version"`
	Storage   Storage   `json:"storage"`
	Exposure  Exposure  `json:"exposure"`
	Limits    Limits    `json:"limits"`
	Resources Resources `json:"resources"`
	Roles     Roles     `json:"roles"`
	Secrets   Secrets   `json:"secrets"`
	Runtime   Runtime   `json:"runtime"`
}

type Storage struct {
	DatabasePath string `json:"databasePath"`
	KVRoot       string `json:"kvRoot"`
	SSHIdentity  string `json:"sshIdentity"`
}

type ExposureGate struct {
	Enabled bool   `json:"enabled"`
	Address string `json:"address"`
}

type Exposure struct {
	HTTP    ExposureGate `json:"http"`
	SSH     ExposureGate `json:"ssh"`
	Gateway ExposureGate `json:"gateway"`
}

type Limits struct {
	MaxSessions          int    `json:"maxSessions"`
	MaxConnections       int    `json:"maxConnections"`
	MaxConnectionsPerIP  int    `json:"maxConnectionsPerIP"`
	MaxFPS               int    `json:"maxFPS"`
	MaxApps              int    `json:"maxApps"`
	MaxDeployments       int    `json:"maxDeployments"`
	MaxSessionsPerAppDay int    `json:"maxSessionsPerAppDay"`
	MaxDeploysPerHour    int    `json:"maxDeploysPerHour"`
	MaxConcurrentBuilds  int    `json:"maxConcurrentBuilds"`
	MaxQueuedBuilds      int    `json:"maxQueuedBuilds"`
	RateLimitPerSec      int    `json:"rateLimitPerSec"`
	RateBurst            int    `json:"rateBurst"`
	MaxEventsPerSec      int    `json:"maxEventsPerSec"`
	MaxFramesPerSec      int    `json:"maxFramesPerSec"`
	MemoryPages          int    `json:"memoryPages"`
	SessionTimeout       string `json:"sessionTimeout"`
	HandshakeTimeout     string `json:"handshakeTimeout"`
	IdleTimeout          string `json:"idleTimeout"`
	FrameTimeout         string `json:"frameTimeout"`
}

type Resources struct {
	MemoryLimitBytes int64    `json:"memoryLimitBytes"`
	AutoCapacity     bool     `json:"autoCapacity"`
	Capacity         Capacity `json:"capacity"`
}

type Capacity struct {
	MaxSessions int `json:"maxSessions"`
	MaxWorkers  int `json:"maxWorkers"`
	MaxBuilds   int `json:"maxBuilds"`
}

type Roles struct {
	Control bool `json:"control"`
	Gateway bool `json:"gateway"`
	Runner  bool `json:"runner"`
}

type Runtime struct {
	Production                 bool   `json:"production"`
	AcknowledgeUnlimitedLimits bool   `json:"acknowledgeUnlimitedLimits"`
	ShutdownTimeout            string `json:"shutdownTimeout"`
	RunnerEndpoint             string `json:"runnerEndpoint,omitempty"`
	RunnerWorker               string `json:"runnerWorker,omitempty"`
	RunnerScratchRoot          string `json:"runnerScratchRoot,omitempty"`
	WorkerUIDBase              int    `json:"workerUIDBase,omitempty"`
	// HostCommandAllowlist is a comma-separated list of executables hosted
	// apps may run as the server OS user. Empty (the default) keeps host
	// commands disabled; shell interpreters are always refused.
	HostCommandAllowlist string `json:"hostCommandAllowlist,omitempty"`
}

// Secret references are paths only. Secret bytes are read by a role only when
// that role explicitly needs the corresponding reference.
type Secrets struct {
	DatabaseKeyFile  string `json:"databaseKeyFile,omitempty"`
	GatewayTokenFile string `json:"gatewayTokenFile,omitempty"`
	RunnerTokenFile  string `json:"runnerTokenFile,omitempty"`
}

func Default() Config {
	return Config{Version: FormatVersion,
		Storage:  Storage{DatabasePath: "plumtree.db", KVRoot: "plumtree-data", SSHIdentity: "plumtree_host_key"},
		Exposure: Exposure{SSH: ExposureGate{Enabled: true, Address: ":2222"}},
		Roles:    Roles{Control: true, Gateway: true},
		Runtime:  Runtime{ShutdownTimeout: "30s"},
		Limits: Limits{
			MaxSessions: 64, MaxConnections: 1024, MaxConnectionsPerIP: 32, MaxFPS: 60,
			MaxApps: 25, MaxDeployments: 100, MaxSessionsPerAppDay: 50,
			MaxDeploysPerHour: 100, MaxConcurrentBuilds: 2, MaxQueuedBuilds: 8,
			RateLimitPerSec: 20, RateBurst: 40, MaxEventsPerSec: 200,
			MaxFramesPerSec: 120, MemoryPages: 512, SessionTimeout: "30m",
			HandshakeTimeout: "10s", IdleTimeout: "5m", FrameTimeout: "2s",
		}, Resources: Resources{AutoCapacity: true}}
}

func (c Config) Validate() error {
	if c.Version != FormatVersion {
		return fmt.Errorf("%w: version %d", ErrInvalid, c.Version)
	}
	for name, value := range map[string]string{"databasePath": c.Storage.DatabasePath, "kvRoot": c.Storage.KVRoot, "sshIdentity": c.Storage.SSHIdentity} {
		if value != "" && filepath.Clean(value) == "." {
			return fmt.Errorf("%w: %s", ErrInvalid, name)
		}
	}
	for name, gate := range map[string]ExposureGate{"http": c.Exposure.HTTP, "ssh": c.Exposure.SSH, "gateway": c.Exposure.Gateway} {
		if gate.Enabled && strings.TrimSpace(gate.Address) == "" {
			return fmt.Errorf("%w: enabled %s exposure needs an address", ErrInvalid, name)
		}
	}
	for name, value := range map[string]int{
		"maxSessions": c.Limits.MaxSessions, "maxConnections": c.Limits.MaxConnections,
		"maxConnectionsPerIP": c.Limits.MaxConnectionsPerIP, "maxFPS": c.Limits.MaxFPS,
		"maxApps": c.Limits.MaxApps, "maxDeployments": c.Limits.MaxDeployments,
		"maxSessionsPerAppDay": c.Limits.MaxSessionsPerAppDay, "maxDeploysPerHour": c.Limits.MaxDeploysPerHour,
		"maxConcurrentBuilds": c.Limits.MaxConcurrentBuilds, "maxQueuedBuilds": c.Limits.MaxQueuedBuilds,
		"rateLimitPerSec": c.Limits.RateLimitPerSec, "rateBurst": c.Limits.RateBurst,
		"maxEventsPerSec": c.Limits.MaxEventsPerSec, "maxFramesPerSec": c.Limits.MaxFramesPerSec,
		"memoryPages": c.Limits.MemoryPages,
	} {
		if value < 0 {
			return fmt.Errorf("%w: %s cannot be negative", ErrInvalid, name)
		}
	}
	if strings.TrimSpace(c.Runtime.ShutdownTimeout) == "" {
		return fmt.Errorf("%w: shutdownTimeout must be a positive duration", ErrInvalid)
	}
	for name, value := range map[string]string{
		"sessionTimeout": c.Limits.SessionTimeout, "handshakeTimeout": c.Limits.HandshakeTimeout,
		"idleTimeout": c.Limits.IdleTimeout, "frameTimeout": c.Limits.FrameTimeout,
		"shutdownTimeout": c.Runtime.ShutdownTimeout,
	} {
		if value != "" {
			d, err := time.ParseDuration(value)
			if err != nil || d <= 0 {
				return fmt.Errorf("%w: %s must be a positive duration", ErrInvalid, name)
			}
		}
	}
	if c.Resources.MemoryLimitBytes < 0 {
		return fmt.Errorf("%w: memoryLimitBytes", ErrInvalid)
	}
	if c.Runtime.WorkerUIDBase < 0 || uint64(c.Runtime.WorkerUIDBase) > math.MaxUint32 {
		return fmt.Errorf("%w: workerUIDBase", ErrInvalid)
	}
	if c.Runtime.WorkerUIDBase > 0 && (c.Limits.MaxSessions <= 0 || uint64(c.Runtime.WorkerUIDBase)+uint64(c.Limits.MaxSessions-1) > math.MaxUint32) {
		return fmt.Errorf("%w: workerUIDBase range", ErrInvalid)
	}
	if c.Resources.Capacity.MaxSessions < 0 || c.Resources.Capacity.MaxWorkers < 0 || c.Resources.Capacity.MaxBuilds < 0 {
		return fmt.Errorf("%w: capacity", ErrInvalid)
	}
	if c.Secrets.DatabaseKeyFile != "" && filepath.IsAbs(c.Secrets.DatabaseKeyFile) == false && strings.Contains(c.Secrets.DatabaseKeyFile, "..") {
		return fmt.Errorf("%w: database key reference", ErrUnsafePath)
	}
	for name, value := range map[string]string{
		"gateway token reference": c.Secrets.GatewayTokenFile,
		"runner token reference":  c.Secrets.RunnerTokenFile,
	} {
		if value != "" && filepath.IsAbs(value) == false && strings.Contains(value, "..") {
			return fmt.Errorf("%w: %s", ErrUnsafePath, name)
		}
	}
	return nil
}

// ValidateProduction enforces the settings whose absence would remove a
// security or resource boundary. The unlimited-limits acknowledgement is an
// explicit operator policy; it never permits an unencrypted production store.
func (c Config) ValidateProduction() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if !c.Runtime.Production {
		return nil
	}
	if c.Roles.Gateway && strings.TrimSpace(c.Runtime.RunnerEndpoint) == "" {
		return fmt.Errorf("%w: production gateway requires runtime.runnerEndpoint", ErrInvalid)
	}
	if c.Roles.Gateway {
		endpoint := c.Runtime.RunnerEndpoint
		switch network, address, ok := strings.Cut(endpoint, "://"); {
		case network == "tcp":
			return fmt.Errorf("%w: production gateway runner endpoint %q uses plain tcp://, which ships the broker token and session traffic unencrypted; use unix:// or tls://", ErrInvalid, endpoint)
		case !ok || address == "" || (network != "unix" && network != "tls"):
			return fmt.Errorf("%w: production gateway runner endpoint must be unix:// or tls:// with an address", ErrInvalid)
		default:
		}
	}
	if c.Roles.Gateway && strings.TrimSpace(c.Secrets.GatewayTokenFile) == "" {
		return fmt.Errorf("%w: production gateway requires secrets.gatewayTokenFile", ErrInvalid)
	}
	if c.Roles.Runner {
		if c.Roles.Control || c.Roles.Gateway {
			return fmt.Errorf("%w: production runner must use an isolated role", ErrInvalid)
		}
		endpoint := c.Runtime.RunnerEndpoint
		if strings.HasPrefix(endpoint, "tcp://") {
			return fmt.Errorf("%w: production runner endpoint %q uses plain tcp://, which ships the broker token and session traffic unencrypted; use unix:// or tls://", ErrInvalid, endpoint)
		}
		if strings.TrimSpace(endpoint) == "" || strings.TrimSpace(c.Runtime.RunnerWorker) == "" || strings.TrimSpace(c.Secrets.RunnerTokenFile) == "" {
			return fmt.Errorf("%w: production runner requires endpoint, worker, and token", ErrInvalid)
		}
		network, address, ok := strings.Cut(endpoint, "://")
		if !ok || address == "" || (network != "unix" && network != "tls") {
			return fmt.Errorf("%w: production runner endpoint must be unix:// or tls:// with an address", ErrInvalid)
		}
	}
	if c.Roles.Control && strings.TrimSpace(c.Secrets.DatabaseKeyFile) == "" {
		return fmt.Errorf("%w: production requires secrets.databaseKeyFile", ErrInvalid)
	}
	if c.Runtime.AcknowledgeUnlimitedLimits {
		return nil
	}
	unlimited := make([]string, 0)
	for name, value := range map[string]int{
		"limits.maxSessions":          c.Limits.MaxSessions,
		"limits.maxConnections":       c.Limits.MaxConnections,
		"limits.maxConnectionsPerIP":  c.Limits.MaxConnectionsPerIP,
		"limits.maxApps":              c.Limits.MaxApps,
		"limits.maxDeployments":       c.Limits.MaxDeployments,
		"limits.maxSessionsPerAppDay": c.Limits.MaxSessionsPerAppDay,
		"limits.maxDeploysPerHour":    c.Limits.MaxDeploysPerHour,
		"limits.maxConcurrentBuilds":  c.Limits.MaxConcurrentBuilds,
		"limits.maxQueuedBuilds":      c.Limits.MaxQueuedBuilds,
		"limits.rateLimitPerSec":      c.Limits.RateLimitPerSec,
		"limits.rateBurst":            c.Limits.RateBurst,
		"limits.maxFPS":               c.Limits.MaxFPS,
		"limits.maxEventsPerSec":      c.Limits.MaxEventsPerSec,
		"limits.maxFramesPerSec":      c.Limits.MaxFramesPerSec,
		"limits.memoryPages":          c.Limits.MemoryPages,
	} {
		if value == 0 {
			unlimited = append(unlimited, name)
		}
	}
	for name, value := range map[string]string{
		"limits.sessionTimeout":   c.Limits.SessionTimeout,
		"limits.handshakeTimeout": c.Limits.HandshakeTimeout,
		"limits.idleTimeout":      c.Limits.IdleTimeout,
		"limits.frameTimeout":     c.Limits.FrameTimeout,
	} {
		if value == "" {
			unlimited = append(unlimited, name)
		}
	}
	if len(unlimited) > 0 {
		slices.Sort(unlimited)
		return fmt.Errorf("%w: production refuses unlimited critical settings: %s", ErrInvalid, strings.Join(unlimited, ", "))
	}
	return nil
}

func decode(data []byte) (Config, error) {
	var c Config
	if err := rejectDuplicateFields(data); err != nil {
		return c, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return c, fmt.Errorf("%w: multiple JSON documents", ErrInvalid)
		}
		return c, fmt.Errorf("%w: trailing JSON: %v", ErrInvalid, err)
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

func rejectDuplicateFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("object key is not a string")
				}
				if _, exists := seen[key]; exists {
					return fmt.Errorf("duplicate field %q", key)
				}
				seen[key] = struct{}{}
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return fmt.Errorf("unexpected delimiter %q", delimiter)
		}
	}
	return walk()
}

func Read(path string) (Config, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Config{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return Config{}, fmt.Errorf("%w: configuration file", ErrUnsafePath)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return decode(b)
}

var pathLocks sync.Map

func lockFor(path string) *sync.Mutex {
	value, _ := pathLocks.LoadOrStore(path, &sync.Mutex{})
	return value.(*sync.Mutex)
}

// Bootstrap is create-only: a valid existing file is loaded and never
// overwritten, while a missing path is created atomically with mode 0600.
func Bootstrap(path string) (Config, bool, error) {
	if strings.TrimSpace(path) == "" {
		return Config{}, false, fmt.Errorf("%w: path is required", ErrInvalid)
	}
	lock := lockFor(path)
	lock.Lock()
	defer lock.Unlock()
	if _, err := os.Lstat(path); err == nil {
		c, readErr := Read(path)
		return c, false, readErr
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, false, err
	}
	c := Default()
	if err := c.Validate(); err != nil {
		return Config{}, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return Config{}, false, err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return Config{}, false, err
	}
	b = append(b, '\n')
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			loaded, loadErr := Read(path)
			return loaded, false, loadErr
		}
		return Config{}, false, err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return Config{}, false, err
	}
	if closeErr != nil {
		return Config{}, false, closeErr
	}
	return c, true, nil
}

func Write(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: path is required", ErrInvalid)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".plumtree-config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func Update(path string, update func(*Config) error) error {
	lock := lockFor(path)
	lock.Lock()
	defer lock.Unlock()
	fileLock, err := acquireFileLock(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = unix.Flock(int(fileLock.Fd()), unix.LOCK_UN)
		_ = fileLock.Close()
	}()
	c, err := Read(path)
	if err != nil {
		return err
	}
	if err := update(&c); err != nil {
		return err
	}
	return Write(path, c)
}

// acquireFileLock extends the in-process mutex to separate config-editing
// processes. The lock file is private and intentionally persistent; its
// advisory lock is released automatically when the process exits.
func acquireFileLock(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lockPath := path + ".lock"
	if info, err := os.Lstat(lockPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, fmt.Errorf("%w: edit lock", ErrUnsafePath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

type Provenance string

const (
	SourceDefault     Provenance = "default"
	SourceConfig      Provenance = "config"
	SourceEnvironment Provenance = "environment"
	SourceFlag        Provenance = "flag"
)

func ResolveString(defaultValue, configValue, environmentValue, flagValue string) (string, Provenance) {
	if flagValue != "" {
		return flagValue, SourceFlag
	}
	if environmentValue != "" {
		return environmentValue, SourceEnvironment
	}
	if configValue != "" {
		return configValue, SourceConfig
	}
	return defaultValue, SourceDefault
}
func ResolveInt(defaultValue, configValue, environmentValue, flagValue int, configSet, environmentSet, flagSet bool) (int, Provenance) {
	if flagSet {
		return flagValue, SourceFlag
	}
	if environmentSet {
		return environmentValue, SourceEnvironment
	}
	if configSet {
		return configValue, SourceConfig
	}
	return defaultValue, SourceDefault
}

// Redacted returns a copy suitable for operator output. Configuration stores
// only secret-file references, but even those paths can disclose deployment
// layout, so show output never emits them.
func (c Config) Redacted() Config {
	if c.Secrets.DatabaseKeyFile != "" {
		c.Secrets.DatabaseKeyFile = "<redacted>"
	}
	if c.Secrets.GatewayTokenFile != "" {
		c.Secrets.GatewayTokenFile = "<redacted>"
	}
	if c.Secrets.RunnerTokenFile != "" {
		c.Secrets.RunnerTokenFile = "<redacted>"
	}
	return c
}

func (c Config) Set(field, value string) (Config, error) {
	next := c
	switch field {
	case "storage.databasePath":
		next.Storage.DatabasePath = value
	case "storage.kvRoot":
		next.Storage.KVRoot = value
	case "storage.sshIdentity":
		next.Storage.SSHIdentity = value
	case "limits.maxSessions":
		v, e := strconv.Atoi(value)
		if e != nil || v < 0 {
			return c, fmt.Errorf("%w: maxSessions", ErrInvalid)
		}
		next.Limits.MaxSessions = v
	case "limits.maxConnections":
		v, e := strconv.Atoi(value)
		if e != nil || v < 0 {
			return c, fmt.Errorf("%w: maxConnections", ErrInvalid)
		}
		next.Limits.MaxConnections = v
	case "limits.maxConnectionsPerIP", "limits.maxFPS", "limits.maxApps", "limits.maxDeployments", "limits.maxSessionsPerAppDay", "limits.maxDeploysPerHour", "limits.maxConcurrentBuilds", "limits.maxQueuedBuilds", "limits.rateLimitPerSec", "limits.rateBurst", "limits.maxEventsPerSec", "limits.maxFramesPerSec", "limits.memoryPages":
		v, e := strconv.Atoi(value)
		if e != nil || v < 0 {
			return c, fmt.Errorf("%w: %s", ErrInvalid, field)
		}
		switch field {
		case "limits.maxConnectionsPerIP":
			next.Limits.MaxConnectionsPerIP = v
		case "limits.maxFPS":
			next.Limits.MaxFPS = v
		case "limits.maxApps":
			next.Limits.MaxApps = v
		case "limits.maxDeployments":
			next.Limits.MaxDeployments = v
		case "limits.maxSessionsPerAppDay":
			next.Limits.MaxSessionsPerAppDay = v
		case "limits.maxDeploysPerHour":
			next.Limits.MaxDeploysPerHour = v
		case "limits.maxConcurrentBuilds":
			next.Limits.MaxConcurrentBuilds = v
		case "limits.maxQueuedBuilds":
			next.Limits.MaxQueuedBuilds = v
		case "limits.rateLimitPerSec":
			next.Limits.RateLimitPerSec = v
		case "limits.rateBurst":
			next.Limits.RateBurst = v
		case "limits.maxEventsPerSec":
			next.Limits.MaxEventsPerSec = v
		case "limits.maxFramesPerSec":
			next.Limits.MaxFramesPerSec = v
		case "limits.memoryPages":
			next.Limits.MemoryPages = v
		}
	case "limits.sessionTimeout":
		next.Limits.SessionTimeout = value
	case "limits.handshakeTimeout":
		next.Limits.HandshakeTimeout = value
	case "limits.idleTimeout":
		next.Limits.IdleTimeout = value
	case "limits.frameTimeout":
		next.Limits.FrameTimeout = value
	case "exposure.gateway.enabled":
		v, e := strconv.ParseBool(value)
		if e != nil {
			return c, fmt.Errorf("%w: gateway enabled", ErrInvalid)
		}
		next.Exposure.Gateway.Enabled = v
	case "exposure.gateway.address":
		next.Exposure.Gateway.Address = value
	case "resources.autoCapacity":
		v, e := strconv.ParseBool(value)
		if e != nil {
			return c, fmt.Errorf("%w: auto capacity", ErrInvalid)
		}
		next.Resources.AutoCapacity = v
	case "roles.control", "roles.gateway", "roles.runner":
		v, e := strconv.ParseBool(value)
		if e != nil {
			return c, fmt.Errorf("%w: %s", ErrInvalid, field)
		}
		switch field {
		case "roles.control":
			next.Roles.Control = v
		case "roles.gateway":
			next.Roles.Gateway = v
		case "roles.runner":
			next.Roles.Runner = v
		}
	case "secrets.databaseKeyFile":
		next.Secrets.DatabaseKeyFile = value
	case "secrets.gatewayTokenFile":
		next.Secrets.GatewayTokenFile = value
	case "secrets.runnerTokenFile":
		next.Secrets.RunnerTokenFile = value
	case "runtime.production":
		v, e := strconv.ParseBool(value)
		if e != nil {
			return c, fmt.Errorf("%w: production", ErrInvalid)
		}
		next.Runtime.Production = v
	case "runtime.acknowledgeUnlimitedLimits":
		v, e := strconv.ParseBool(value)
		if e != nil {
			return c, fmt.Errorf("%w: acknowledge unlimited limits", ErrInvalid)
		}
		next.Runtime.AcknowledgeUnlimitedLimits = v
	case "runtime.shutdownTimeout":
		next.Runtime.ShutdownTimeout = value
	case "runtime.runnerEndpoint":
		next.Runtime.RunnerEndpoint = value
	case "runtime.runnerWorker":
		next.Runtime.RunnerWorker = value
	case "runtime.runnerScratchRoot":
		next.Runtime.RunnerScratchRoot = value
	case "runtime.workerUIDBase":
		v, e := strconv.Atoi(value)
		if e != nil || v < 0 {
			return c, fmt.Errorf("%w: workerUIDBase", ErrInvalid)
		}
		next.Runtime.WorkerUIDBase = v
	case "runtime.hostCommandAllowlist":
		next.Runtime.HostCommandAllowlist = value
	case "exposure.http.enabled":
		v, e := strconv.ParseBool(value)
		if e != nil {
			return c, fmt.Errorf("%w: http enabled", ErrInvalid)
		}
		next.Exposure.HTTP.Enabled = v
	case "exposure.http.address":
		next.Exposure.HTTP.Address = value
	case "exposure.ssh.enabled":
		v, e := strconv.ParseBool(value)
		if e != nil {
			return c, fmt.Errorf("%w: ssh enabled", ErrInvalid)
		}
		next.Exposure.SSH.Enabled = v
	case "exposure.ssh.address":
		next.Exposure.SSH.Address = value
	default:
		return c, fmt.Errorf("%w: unknown setting %q", ErrInvalid, field)
	}
	if err := next.Validate(); err != nil {
		return c, err
	}
	return next, nil
}
func (c Config) Unset(field string) (Config, error) {
	d := Default()
	switch field {
	case "storage.databasePath":
		c.Storage.DatabasePath = d.Storage.DatabasePath
	case "storage.kvRoot":
		c.Storage.KVRoot = d.Storage.KVRoot
	case "storage.sshIdentity":
		c.Storage.SSHIdentity = d.Storage.SSHIdentity
	case "limits.maxSessions":
		c.Limits.MaxSessions = d.Limits.MaxSessions
	case "limits.maxConnections":
		c.Limits.MaxConnections = d.Limits.MaxConnections
	case "limits.maxConnectionsPerIP":
		c.Limits.MaxConnectionsPerIP = d.Limits.MaxConnectionsPerIP
	case "limits.maxFPS":
		c.Limits.MaxFPS = d.Limits.MaxFPS
	case "limits.maxApps":
		c.Limits.MaxApps = d.Limits.MaxApps
	case "limits.maxDeployments":
		c.Limits.MaxDeployments = d.Limits.MaxDeployments
	case "limits.maxSessionsPerAppDay":
		c.Limits.MaxSessionsPerAppDay = d.Limits.MaxSessionsPerAppDay
	case "limits.maxDeploysPerHour":
		c.Limits.MaxDeploysPerHour = d.Limits.MaxDeploysPerHour
	case "limits.maxConcurrentBuilds":
		c.Limits.MaxConcurrentBuilds = d.Limits.MaxConcurrentBuilds
	case "limits.maxQueuedBuilds":
		c.Limits.MaxQueuedBuilds = d.Limits.MaxQueuedBuilds
	case "limits.rateLimitPerSec":
		c.Limits.RateLimitPerSec = d.Limits.RateLimitPerSec
	case "limits.rateBurst":
		c.Limits.RateBurst = d.Limits.RateBurst
	case "limits.maxEventsPerSec":
		c.Limits.MaxEventsPerSec = d.Limits.MaxEventsPerSec
	case "limits.maxFramesPerSec":
		c.Limits.MaxFramesPerSec = d.Limits.MaxFramesPerSec
	case "limits.memoryPages":
		c.Limits.MemoryPages = d.Limits.MemoryPages
	case "limits.sessionTimeout":
		c.Limits.SessionTimeout = d.Limits.SessionTimeout
	case "limits.handshakeTimeout":
		c.Limits.HandshakeTimeout = d.Limits.HandshakeTimeout
	case "limits.idleTimeout":
		c.Limits.IdleTimeout = d.Limits.IdleTimeout
	case "limits.frameTimeout":
		c.Limits.FrameTimeout = d.Limits.FrameTimeout
	case "exposure.http.enabled":
		c.Exposure.HTTP.Enabled = d.Exposure.HTTP.Enabled
	case "exposure.http.address":
		c.Exposure.HTTP.Address = d.Exposure.HTTP.Address
	case "exposure.ssh.enabled":
		c.Exposure.SSH.Enabled = d.Exposure.SSH.Enabled
	case "exposure.ssh.address":
		c.Exposure.SSH.Address = d.Exposure.SSH.Address
	case "exposure.gateway.enabled":
		c.Exposure.Gateway.Enabled = d.Exposure.Gateway.Enabled
	case "exposure.gateway.address":
		c.Exposure.Gateway.Address = d.Exposure.Gateway.Address
	case "resources.autoCapacity":
		c.Resources.AutoCapacity = d.Resources.AutoCapacity
	case "roles.control":
		c.Roles.Control = d.Roles.Control
	case "roles.gateway":
		c.Roles.Gateway = d.Roles.Gateway
	case "roles.runner":
		c.Roles.Runner = d.Roles.Runner
	case "secrets.databaseKeyFile":
		c.Secrets.DatabaseKeyFile = d.Secrets.DatabaseKeyFile
	case "secrets.gatewayTokenFile":
		c.Secrets.GatewayTokenFile = d.Secrets.GatewayTokenFile
	case "secrets.runnerTokenFile":
		c.Secrets.RunnerTokenFile = d.Secrets.RunnerTokenFile
	case "runtime.production":
		c.Runtime.Production = d.Runtime.Production
	case "runtime.acknowledgeUnlimitedLimits":
		c.Runtime.AcknowledgeUnlimitedLimits = d.Runtime.AcknowledgeUnlimitedLimits
	case "runtime.shutdownTimeout":
		c.Runtime.ShutdownTimeout = d.Runtime.ShutdownTimeout
	case "runtime.runnerEndpoint":
		c.Runtime.RunnerEndpoint = d.Runtime.RunnerEndpoint
	case "runtime.runnerWorker":
		c.Runtime.RunnerWorker = d.Runtime.RunnerWorker
	case "runtime.runnerScratchRoot":
		c.Runtime.RunnerScratchRoot = d.Runtime.RunnerScratchRoot
	case "runtime.workerUIDBase":
		c.Runtime.WorkerUIDBase = d.Runtime.WorkerUIDBase
	case "runtime.hostCommandAllowlist":
		c.Runtime.HostCommandAllowlist = d.Runtime.HostCommandAllowlist
	default:
		return c, fmt.Errorf("%w: unknown setting %q", ErrInvalid, field)
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}
