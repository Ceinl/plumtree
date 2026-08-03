package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/Ceinl/plumtree/internal/state"
)

func cmdState(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: pt state backup|restore|inventory|rekey")
	}
	switch args[0] {
	case "backup":
		return cmdStateBackup(args[1:])
	case "restore":
		return cmdStateRestore(args[1:])
	case "inventory":
		return cmdStateInventory(args[1:])
	case "rekey":
		return cmdStateRekey(args[1:])
	default:
		return fmt.Errorf("unknown state command %q", args[0])
	}
}

type stateFlags struct{ database, kv, ssh, key string }

func (f *stateFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&f.database, "database", "", "current SQLite database path")
	fs.StringVar(&f.kv, "kv-dir", "", "current KV root directory")
	fs.StringVar(&f.ssh, "ssh-identity", "", "current SSH host identity path")
	fs.StringVar(&f.key, "key-file", "", "private raw 32-byte database key file")
}
func (f stateFlags) paths() (state.Paths, error) {
	if f.database == "" || f.kv == "" || f.ssh == "" {
		return state.Paths{}, errors.New("--database, --kv-dir, and --ssh-identity are required")
	}
	return state.Paths{Database: f.database, KVRoot: f.kv, SSHIdentity: f.ssh}, nil
}
func readStateKey(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, errors.New("read key file: unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("read key file: unsafe file")
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("read key file: insecure permissions")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read key file: unavailable")
	}
	if len(b) != 32 {
		return nil, errors.New("read key file: key must be exactly 32 bytes")
	}
	return b, nil
}

func cmdStateBackup(args []string) error {
	fs := flag.NewFlagSet("state backup", flag.ContinueOnError)
	var f stateFlags
	f.bind(fs)
	var output string
	fs.StringVar(&output, "output", "", "destination bundle directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	paths, err := f.paths()
	if err != nil {
		return err
	}
	if output == "" {
		return errors.New("--output is required")
	}
	key, err := readStateKey(f.key)
	if err != nil {
		return err
	}
	if err := state.Backup(nilContext(), paths, output, key, state.Options{}); err != nil {
		return err
	}
	fmt.Println(output)
	return nil
}
func cmdStateRestore(args []string) error {
	fs := flag.NewFlagSet("state restore", flag.ContinueOnError)
	var f stateFlags
	f.bind(fs)
	var input string
	fs.StringVar(&input, "input", "", "source bundle directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	paths, err := f.paths()
	if err != nil {
		return err
	}
	if input == "" {
		return errors.New("--input is required")
	}
	key, err := readStateKey(f.key)
	if err != nil {
		return err
	}
	return state.Restore(nilContext(), input, paths, key, state.Options{})
}
func cmdStateInventory(args []string) error {
	fs := flag.NewFlagSet("state inventory", flag.ContinueOnError)
	var f stateFlags
	f.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	paths, err := f.paths()
	if err != nil {
		return err
	}
	key, err := readStateKey(f.key)
	if err != nil {
		return err
	}
	report, err := state.Inventory(nilContext(), paths, key)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}
func cmdStateRekey(args []string) error {
	fs := flag.NewFlagSet("state rekey", flag.ContinueOnError)
	var database, oldFile, newFile string
	fs.StringVar(&database, "database", "", "current SQLite database path")
	fs.StringVar(&oldFile, "old-key-file", "", "old private raw 32-byte key file")
	fs.StringVar(&newFile, "new-key-file", "", "new private raw 32-byte key file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if database == "" {
		return errors.New("--database is required")
	}
	oldKey, err := readStateKey(oldFile)
	if err != nil {
		return err
	}
	newKey, err := readStateKey(newFile)
	if err != nil {
		return err
	}
	return state.RekeyDatabase(nilContext(), database, oldKey, newKey)
}

// Kept local to avoid threading CLI cancellation through the existing command
// runner until the root command lifecycle is selected in the later cutover.
func nilContext() context.Context { return context.Background() }
