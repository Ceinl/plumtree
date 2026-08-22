package cleanrole

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	serverconfig "github.com/Ceinl/plumtree/internal/server/config"
	"github.com/Ceinl/plumtree/internal/state"
)

// executeState exposes the current-format offline state operations through the
// operator binary. The server must be stopped before these commands run.
func executeState(ctx context.Context, args, environment []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: plumtree state inventory|backup|restore|recover")
	}
	command := args[0]
	fs := flag.NewFlagSet("plumtree state "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "typed config file path")
	input := fs.String("input", "", "source backup bundle")
	output := fs.String("output", "", "destination backup bundle")
	yes := fs.Bool("yes", false, "confirm a destructive restore or recovery")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || *configPath == "" {
		return fmt.Errorf("usage: plumtree state %s --config PATH", command)
	}
	resolved, err := ResolveServe([]string{"serve", "--config", *configPath}, environment, 0)
	if err != nil {
		return err
	}
	projection, err := serverconfig.MaterializeRole(resolved.Config, serverconfig.RoleControl)
	if err != nil {
		return fmt.Errorf("clean server: state configuration: %w", err)
	}
	paths := state.Paths{Database: resolved.Config.Storage.DatabasePath, KVRoot: resolved.Config.Storage.KVRoot, SSHIdentity: resolved.Config.Storage.SSHIdentity}
	key := projection.Secret()

	switch command {
	case "inventory":
		if *input != "" || *output != "" || *yes {
			return errors.New("usage: plumtree state inventory --config PATH")
		}
		report, err := state.Inventory(ctx, paths, key)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(report)
	case "backup":
		if *output == "" || *input != "" || *yes {
			return errors.New("usage: plumtree state backup --config PATH --output DIRECTORY")
		}
		if err := state.Backup(ctx, paths, *output, key, state.Options{}); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, *output)
		return err
	case "restore":
		if *input == "" || *output != "" || !*yes {
			return errors.New("usage: plumtree state restore --config PATH --input DIRECTORY --yes")
		}
		return state.Restore(ctx, *input, paths, key, state.Options{})
	case "recover":
		if *input != "" || *output != "" || !*yes {
			return errors.New("usage: plumtree state recover --config PATH --yes")
		}
		return state.Recover(paths)
	default:
		return fmt.Errorf("unknown plumtree state command %q", command)
	}
}

func ensureKVRoot(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}
