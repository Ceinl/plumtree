package config

import "os"

// LoadOptions contains process inputs for one immutable configuration load.
// Callers provide environment and flag maps so packages do not read or mutate
// process-global state.
type LoadOptions struct {
	Path        string
	Environment map[string]string
	Flags       map[string]string
	ReadFile    func(string) ([]byte, error)
	HostMemory  int64
}

// Loaded is the concrete startup configuration and the source selected for
// each setting. The configuration is materialized once and is not reloaded.
type Loaded struct {
	Config  Config
	Sources map[string]Provenance
}

// Load reads, overrides, validates, and materializes one startup configuration.
func Load(options LoadOptions) (Loaded, error) {
	c, err := Read(options.Path)
	if err != nil {
		return Loaded{}, err
	}
	c, sources, err := ApplyOverrides(c, options.Environment, options.Flags)
	if err != nil {
		return Loaded{}, err
	}
	read := options.ReadFile
	if read == nil {
		read = os.ReadFile
	}
	c, err = MaterializeCapacity(c, read, options.HostMemory)
	if err != nil {
		return Loaded{}, err
	}
	return Loaded{Config: c, Sources: sources}, nil
}
