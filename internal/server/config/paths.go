package config

import "path/filepath"

// ResolvePaths resolves relative file references from the directory that owns
// the configuration file. Plumtree does not search the working directory.
func ResolvePaths(c Config, configPath string) Config {
	base := filepath.Dir(configPath)
	resolve := func(path string) string {
		if path == "" || filepath.IsAbs(path) {
			return path
		}
		return filepath.Clean(filepath.Join(base, path))
	}
	c.Storage.DatabasePath = resolve(c.Storage.DatabasePath)
	c.Storage.KVRoot = resolve(c.Storage.KVRoot)
	c.Storage.SSHIdentity = resolve(c.Storage.SSHIdentity)
	c.Secrets.DatabaseKeyFile = resolve(c.Secrets.DatabaseKeyFile)
	c.Secrets.GatewayTokenFile = resolve(c.Secrets.GatewayTokenFile)
	c.Secrets.RunnerTokenFile = resolve(c.Secrets.RunnerTokenFile)
	return c
}
