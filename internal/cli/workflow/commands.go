package workflow

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	plumbuild "github.com/Ceinl/plumtree/internal/build"
	"github.com/Ceinl/plumtree/internal/cli/paired"
	"github.com/Ceinl/plumtree/internal/cli/scaffold"
	"github.com/Ceinl/plumtree/sdk/abi"
	xterm "golang.org/x/term"
)

type OpenFunc func(context.Context, paired.ServerRecord) (*API, error)
type ConfirmFunc func(string) bool

// Runner is the explicit clean command surface. It receives all I/O and
// network opening through fields, so tests and JSON/CI callers do not inherit
// global argv, environment, or stdio mutations.
type Runner struct {
	Context   context.Context
	In        io.Reader
	Out       io.Writer
	Err       io.Writer
	StorePath string
	KeyDir    string
	Workspace string
	Open      OpenFunc
	Confirm   ConfirmFunc
}

func (r Runner) context() context.Context {
	if r.Context != nil {
		return r.Context
	}
	return context.Background()
}

func (r Runner) streams() (io.Reader, io.Writer, io.Writer) {
	in, out, errOut := r.In, r.Out, r.Err
	if in == nil {
		in = strings.NewReader("")
	}
	if out == nil {
		out = io.Discard
	}
	if errOut == nil {
		errOut = io.Discard
	}
	return in, out, errOut
}

func (r Runner) Run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: pt <new|dev|build|deploy|status|app|logs|secret|egress|access|audit|ssh>")
	}
	if isHelp(args[0]) {
		return r.writeHelp("")
	}
	if args[0] == "help" {
		if len(args) > 2 {
			return errors.New("usage: pt help [command]")
		}
		command := ""
		if len(args) == 2 {
			command = args[1]
		}
		return r.writeHelp(command)
	}
	if len(args) == 2 && isHelp(args[1]) {
		return r.writeHelp(args[0])
	}
	switch args[0] {
	case "new":
		return r.newProject(args[1:])
	case "build":
		return r.buildProject(args[1:])
	case "dev":
		return r.devProject(args[1:])
	case "deploy":
		return r.deployProject(args[1:])
	case "status":
		return r.status(args[1:])
	case "app":
		return r.app(args[1:])
	case "logs":
		return r.logs(args[1:])
	case "secret":
		return r.secret(args[1:])
	case "egress":
		return r.egress(args[1:])
	case "access":
		return r.access(args[1:])
	case "audit":
		return r.audit(args[1:])
	case "ssh":
		return r.ssh(args[1:])
	default:
		return fmt.Errorf("unknown clean pt command %q", args[0])
	}
}

func (r Runner) newProject(args []string) error {
	fs := flag.NewFlagSet("pt new", flag.ContinueOnError)
	tui, cli := fs.Bool("tui", false, "scaffold an interactive TUI app"), fs.Bool("cli", false, "scaffold a finite CLI app")
	access := fs.String("access", "", "required: public or restricted")
	ordered, err := flagsBeforePositionals(args, map[string]bool{"--access": true, "-access": true})
	if err != nil {
		return err
	}
	if err := fs.Parse(ordered); err != nil {
		return err
	}
	if fs.NArg() != 1 || (*tui == *cli) || *access == "" {
		return errors.New("usage: pt new NAME --tui|--cli --access public|restricted")
	}
	kind := scaffold.TUI
	if *cli {
		kind = scaffold.CLI
	}
	project, err := NewScaffold(".", fs.Arg(0), kind, *access)
	if err != nil {
		return err
	}
	_, out, _ := r.streams()
	return writeStable(out, map[string]any{"project": filepath.Base(project), "type": kind, "access": *access})
}

func (r Runner) project() (string, Manifest, error) {
	root, err := findProjectRoot()
	if err != nil {
		return "", Manifest{}, err
	}
	manifest, err := ReadManifest(root)
	return root, manifest, err
}

func (r Runner) buildProject(args []string) error {
	fs := flag.NewFlagSet("pt build", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit stable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: pt build [--json]")
	}
	root, manifest, err := r.project()
	if err != nil {
		return err
	}
	result, err := Build(context.Background(), root, r.Workspace)
	if err != nil {
		return err
	}
	_, out, _ := r.streams()
	if *jsonOut {
		return writeStable(out, map[string]any{"name": manifest.Name, "type": manifest.Type, "access": manifest.Access, "bytes": len(result.Artifact.WASM)})
	}
	_, _ = fmt.Fprintf(out, "Built %s (%s, %s, %d bytes)\n", manifest.Name, manifest.Type, manifest.Access, len(result.Artifact.WASM))
	return nil
}

func (r Runner) openTarget(ctx context.Context, name string) (*API, paired.ServerRecord, error) {
	store, err := paired.Load(r.StorePath)
	if err != nil {
		return nil, paired.ServerRecord{}, err
	}
	target, err := store.ResolveTarget(name, nil)
	if err != nil {
		return nil, paired.ServerRecord{}, fmt.Errorf("%w: %v", ErrTarget, err)
	}
	record, err := store.Get(target.Name)
	if err != nil {
		return nil, paired.ServerRecord{}, err
	}
	if r.Open == nil {
		return nil, paired.ServerRecord{}, errors.New("clean pt API opener is not configured")
	}
	api, err := r.Open(ctx, record)
	return api, record, err
}

func (r Runner) deployProject(args []string) error {
	fs := flag.NewFlagSet("pt deploy", flag.ContinueOnError)
	server := fs.String("server", "", "explicit saved server name")
	jsonOut, yes := fs.Bool("json", false, "emit stable JSON"), fs.Bool("yes", false, "confirm deployment")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: pt deploy [--server NAME] [--yes] [--json]")
	}
	if !*yes && !r.confirm("Deploy this artifact to the selected server?") {
		return confirmationRequired()
	}
	root, manifest, err := r.project()
	if err != nil {
		return err
	}
	result, err := Build(context.Background(), root, r.Workspace)
	if err != nil {
		return err
	}
	api, _, err := r.openTarget(context.Background(), *server)
	if err != nil {
		return err
	}
	version, err := api.Version(context.Background())
	if err != nil {
		return err
	}
	if version.Version == "" {
		return errors.New("server version preflight returned no product version")
	}
	source, err := plumbuild.PackSource(root)
	if err != nil {
		return fmt.Errorf("pack source: %w", err)
	}
	deployed, err := api.Deploy(context.Background(), ArtifactRequest{Name: manifest.Name, Type: manifest.Type, Access: manifest.Access, ABIVersion: abi.Version, SourceDigest: plumbuild.SourceDigest(source), BuildMetadata: map[string]string{"client": "pt", "productVersion": version.Version}, WASM: result.Artifact.WASM}, "")
	if err != nil {
		return err
	}
	_, out, _ := r.streams()
	if *jsonOut {
		return writeStable(out, deployed)
	}
	_, _ = fmt.Fprintf(out, "Deployed %s (%s)\n", manifest.Name, manifest.Access)
	return nil
}

func (r Runner) status(args []string) error {
	fs := flag.NewFlagSet("pt status", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit stable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: pt status [--json]")
	}
	api, record, err := r.openTarget(context.Background(), "")
	if err != nil {
		return err
	}
	version, err := api.Version(context.Background())
	if err != nil {
		return err
	}
	apps, err := api.Apps(context.Background())
	if err != nil {
		return err
	}
	_, out, _ := r.streams()
	value := map[string]any{"server": record.Name, "endpoint": record.Endpoint().String(), "version": version, "apps": apps.Apps}
	if *jsonOut {
		return writeStable(out, value)
	}
	_, _ = fmt.Fprintf(out, "Server: %s (%s)\nApps: %d\n", record.Name, version.Version, len(apps.Apps))
	return nil
}

func (r Runner) app(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: pt app list|show ID")
	}
	api, _, err := r.openTarget(context.Background(), "")
	if err != nil {
		return err
	}
	_, out, _ := r.streams()
	switch args[0] {
	case "list":
		value, err := api.Apps(context.Background())
		if err != nil {
			return err
		}
		return writeStable(out, value)
	case "show":
		if len(args) != 2 {
			return errors.New("usage: pt app show ID")
		}
		value, err := api.App(context.Background(), args[1])
		if err != nil {
			return err
		}
		return writeStable(out, value)
	default:
		return fmt.Errorf("unknown app command %q", args[0])
	}
}

func (r Runner) logs(args []string) error {
	fs := flag.NewFlagSet("pt logs", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "follow session updates")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: pt logs APP_ID [--follow]")
	}
	api, _, err := r.openTarget(context.Background(), "")
	if err != nil {
		return err
	}
	_, out, _ := r.streams()
	if !*follow {
		value, err := api.Sessions(context.Background(), fs.Arg(0))
		if err != nil {
			return err
		}
		return writeStable(out, value)
	}
	return api.FollowSessions(context.Background(), fs.Arg(0), time.Second, func(value SessionsResult) error { return writeStable(out, value) })
}

func (r Runner) secret(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: pt secret set|list|rm")
	}
	if args[0] == "rm" {
		positional, yes, err := confirmationArgs(args[1:])
		if err != nil || len(positional) != 2 {
			return errors.New("usage: pt secret rm APP_ID KEY [--yes]")
		}
		if !yes && !r.confirm("Remove this secret?") {
			return confirmationRequired()
		}
		api, _, err := r.openTarget(context.Background(), "")
		if err != nil {
			return err
		}
		return api.DeleteSecret(context.Background(), positional[0], positional[1])
	}
	api, _, err := r.openTarget(context.Background(), "")
	if err != nil {
		return err
	}
	_, out, _ := r.streams()
	switch args[0] {
	case "list":
		if len(args) != 2 {
			return errors.New("usage: pt secret list APP_ID")
		}
		value, err := api.Secrets(context.Background(), args[1])
		if err != nil {
			return err
		}
		return writeStable(out, value)
	case "set":
		if len(args) < 3 || len(args) > 4 {
			return errors.New("usage: pt secret set APP_ID KEY [VALUE]")
		}
		value := ""
		if len(args) == 4 {
			value = args[3]
		} else {
			value, err = readSecret(r.In)
			if err != nil {
				return err
			}
		}
		return api.SetSecret(context.Background(), args[1], args[2], value)
	default:
		return fmt.Errorf("unknown secret command %q", args[0])
	}
}

func (r Runner) egress(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: pt egress list|add|rm APP_ID [HOST]")
	}
	if args[0] == "rm" {
		positional, yes, err := confirmationArgs(args[1:])
		if err != nil || len(positional) != 2 {
			return errors.New("usage: pt egress rm APP_ID HOST [--yes]")
		}
		if !yes && !r.confirm("Remove this egress permission?") {
			return confirmationRequired()
		}
		api, _, err := r.openTarget(context.Background(), "")
		if err != nil {
			return err
		}
		return api.SetEgress(context.Background(), positional[0], positional[1], false)
	}
	api, _, err := r.openTarget(context.Background(), "")
	if err != nil {
		return err
	}
	_, out, _ := r.streams()
	switch args[0] {
	case "list":
		value, err := api.Egress(context.Background(), args[1])
		if err != nil {
			return err
		}
		return writeStable(out, value)
	case "add":
		if len(args) != 3 {
			return errors.New("usage: pt egress add APP_ID HOST")
		}
		return api.SetEgress(context.Background(), args[1], args[2], true)
	default:
		return fmt.Errorf("unknown egress command %q", args[0])
	}
}

func (r Runner) access(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: pt access list|add|rm APP_ID [KEY]")
	}
	if args[0] == "rm" {
		positional, yes, err := confirmationArgs(args[1:])
		if err != nil || len(positional) != 2 {
			return errors.New("usage: pt access rm APP_ID KEY_ID [--yes]")
		}
		if !yes && !r.confirm("Remove this access key?") {
			return confirmationRequired()
		}
		api, _, err := r.openTarget(context.Background(), "")
		if err != nil {
			return err
		}
		return api.RemoveAccess(context.Background(), positional[0], positional[1])
	}
	api, _, err := r.openTarget(context.Background(), "")
	if err != nil {
		return err
	}
	_, out, _ := r.streams()
	switch args[0] {
	case "list":
		value, err := api.Access(context.Background(), args[1])
		if err != nil {
			return err
		}
		return writeStable(out, value)
	case "add":
		if len(args) != 5 {
			return errors.New("usage: pt access add APP_ID NAME PUBLIC_KEY FINGERPRINT")
		}
		return api.AddAccess(context.Background(), args[1], args[2], args[3], args[4])
	default:
		return fmt.Errorf("unknown access command %q", args[0])
	}
}

func (r Runner) audit(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: pt audit")
	}
	api, _, err := r.openTarget(context.Background(), "")
	if err != nil {
		return err
	}
	value, err := api.Audit(context.Background())
	if err != nil {
		return err
	}
	_, out, _ := r.streams()
	return writeStable(out, value)
}

func (r Runner) ssh(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: pt ssh APP_HANDLE")
	}
	store, err := paired.Load(r.StorePath)
	if err != nil {
		return err
	}
	record, err := store.CurrentRecord()
	if err != nil {
		return err
	}
	command, err := SSHInstruction(record, args[0])
	if err != nil {
		return err
	}
	_, out, _ := r.streams()
	_, err = fmt.Fprintln(out, command)
	return err
}

func (r Runner) confirm(prompt string) bool {
	if r.Confirm != nil {
		return r.Confirm(prompt)
	}
	return false
}

func confirmationRequired() error {
	return fmt.Errorf("%w: rerun with --yes or use an interactive terminal", ErrConfirm)
}

// confirmationArgs accepts --yes before or after the destructive target. The
// remaining values are the command's positional arguments.
func confirmationArgs(args []string) ([]string, bool, error) {
	positional := make([]string, 0, len(args))
	yes := false
	for _, arg := range args {
		switch arg {
		case "--yes":
			yes = true
		case "--":
			// A boundary is optional because --yes is the only supported flag.
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, false, fmt.Errorf("unknown flag %q", arg)
			}
			positional = append(positional, arg)
		}
	}
	return positional, yes, nil
}

// flagsBeforePositionals lets the standard flag parser accept documented
// git-style forms such as `pt new NAME --tui --access public`.
func flagsBeforePositionals(args []string, valueFlags map[string]bool) ([]string, error) {
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}
		flags = append(flags, arg)
		name, _, hasValue := strings.Cut(arg, "=")
		if valueFlags[name] && !hasValue {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag needs an argument: %s", name)
			}
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positionals...), nil
}
func readSecret(in io.Reader) (string, error) {
	if in == nil {
		return "", errors.New("secret input is required")
	}
	if file, ok := in.(*os.File); ok && xterm.IsTerminal(int(file.Fd())) {
		value, err := xterm.ReadPassword(int(file.Fd()))
		if err != nil {
			return "", fmt.Errorf("read secret: %w", err)
		}
		return string(value), nil
	}
	line, err := bufio.NewReader(io.LimitReader(in, 128<<10)).ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
func writeStable(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "plumtree.json")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no strict plumtree.json found")
		}
		dir = parent
	}
}
