package workflow

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Ceinl/plumtree/internal/cli/scaffold"
	"github.com/Ceinl/plumtree/internal/cli/sshdev"
	"github.com/Ceinl/plumtree/internal/runner"
	plumterminal "github.com/Ceinl/plumtree/internal/terminal"
	"github.com/Ceinl/plumtree/internal/terminal/keyboard"
	xterm "golang.org/x/term"
)

func (r Runner) devProject(args []string) error {
	fs := flag.NewFlagSet("pt dev", flag.ContinueOnError)
	reset := fs.Bool("reset", false, "reset the persistent local profile")
	headless := fs.Bool("headless", false, "run a scripted session without a terminal")
	script := fs.String("script", "up,up,down,q", "headless input tokens")
	width := fs.Int("w", 40, "headless frame width")
	height := fs.Int("h", 12, "headless frame height")
	memoryPages := fs.Uint("mem-pages", uint(runner.DefaultLimits.MemoryPages), "linear-memory cap in 64 KiB pages")
	frameTimeout := fs.Duration("frame-timeout", runner.DefaultLimits.FrameTimeout, "per-frame wall-clock deadline")
	maxFPS := fs.Int("max-fps", 60, "terminal and SSH repaint cap")
	sshMode := fs.Bool("ssh", false, "serve the app over SSH on loopback")
	sshAddr := fs.String("addr", "127.0.0.1:2222", "SSH listen address (loopback only)")
	allowNonloopback := fs.Bool("allow-nonloopback-ssh", false, "permit a non-loopback SSH listen address (with --ssh)")
	sshHost := fs.String("host", "plumtree.dev", "SSH config alias to install for the dev server (with --ssh)")
	noSSHConfig := fs.Bool("no-ssh-config", false, "print the raw ssh command instead of installing the ~/.ssh/config alias (with --ssh)")
	jsonOut := fs.Bool("json", false, "emit stable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateDevOptions(*memoryPages, *frameTimeout, *maxFPS, *width, *height); err != nil {
		return err
	}
	if *sshMode && *headless {
		return errors.New("choose one development mode: --ssh or --headless")
	}
	if *sshMode {
		if fs.NArg() != 0 {
			return errors.New("pt dev --ssh does not accept app arguments; pass them with ssh exec")
		}
		if err := sshdev.CheckListenAddress(*sshAddr, *allowNonloopback); err != nil {
			return err
		}
		if !*noSSHConfig {
			if err := validateSSHAlias(*sshHost); err != nil {
				return err
			}
		}
	}

	ctx, stop := signal.NotifyContext(r.context(), os.Interrupt)
	defer stop()
	root, manifest, err := r.project()
	if err != nil {
		return err
	}
	if manifest.Type == string(scaffold.TUI) && !*sshMode && !*headless && fs.NArg() != 0 {
		return errors.New("TUI development does not accept app arguments")
	}
	result, err := Build(ctx, root, r.Workspace)
	if err != nil {
		return err
	}
	caps, cleanup, err := OpenProfile(Profile{Root: root, Reset: *reset})
	if err != nil {
		return err
	}
	defer cleanup()
	_, out, errOut := r.streams()
	limits := runner.DefaultLimits
	limits.MemoryPages = uint32(*memoryPages)
	limits.FrameTimeout = *frameTimeout
	limits.MaxFramesPerSec = *maxFPS

	if *sshMode {
		return r.runSSH(ctx, result.Artifact.WASM, limits, caps, manifest, *sshAddr, *allowNonloopback, *maxFPS, *sshHost, *noSSHConfig, out, errOut)
	}
	if manifest.Type == string(scaffold.CLI) {
		err = runner.RunCLIWithStreams(ctx, result.Artifact.WASM, limits, caps, fs.Args(), runner.CLIStreams{Stdin: r.In, Stdout: out, Stderr: errOut})
		writeDevFinish(out, errOut, caps, nil)
	} else if *headless {
		_, _ = fmt.Fprintf(out, "pt dev (headless) · %dx%d · mem-cap %d pages · frame-deadline %s\n", *width, *height, *memoryPages, *frameTimeout)
		serializedOut := &serializedWriter{writer: out}
		source := runner.NewScriptSource(*width, *height, splitTokens(*script))
		source.Echo = serializedOut
		logs := runner.NewLogBuffer()
		err = runner.Run(ctx, result.Artifact.WASM, limits, caps, source, runner.TextSink{W: serializedOut}, logs)
		writeDevFinish(out, errOut, caps, []byte(logs.String()))
	} else {
		err = r.runTTY(ctx, result.Artifact.WASM, limits, caps, *maxFPS)
	}
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeStable(out, map[string]any{"name": manifest.Name, "profile": filepath.Join(root, ".plumtree", "dev"), "reset": *reset})
	}
	_, _ = fmt.Fprintf(out, "Dev profile ready for %s\n", manifest.Name)
	return nil
}

type serializedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *serializedWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(value)
}

func validateDevOptions(memoryPages uint, frameTimeout time.Duration, maxFPS, width, height int) error {
	if memoryPages == 0 || uint64(memoryPages) > uint64(runner.MaxMemoryPages) {
		return fmt.Errorf("mem-pages must be between 1 and %d", runner.MaxMemoryPages)
	}
	if frameTimeout < 0 {
		return errors.New("frame-timeout must not be negative")
	}
	if maxFPS < 0 {
		return errors.New("max-fps must not be negative")
	}
	if width < plumterminal.MinWidth || width > plumterminal.MaxWidth || height < plumterminal.MinHeight || height > plumterminal.MaxHeight || width > plumterminal.MaxCells/height {
		return fmt.Errorf("headless size must be within %dx%d and %dx%d with at most %d cells", plumterminal.MinWidth, plumterminal.MinHeight, plumterminal.MaxWidth, plumterminal.MaxHeight, plumterminal.MaxCells)
	}
	return nil
}

func (r Runner) runSSH(ctx context.Context, wasm []byte, limits runner.Limits, caps runner.Capabilities, manifest Manifest, address string, allowNonloopback bool, maxFPS int, alias string, noSSHConfig bool, out, errOut io.Writer) error {
	engine := runner.New()
	defer engine.Close(context.Background())
	server := &sshdev.Server{Wasm: wasm, Runner: engine, Limits: limits, Caps: caps, AppType: manifest.Type, AppName: manifest.Name, MaxFPS: maxFPS, AllowNonloopback: allowNonloopback,
		Logf: func(format string, values ...any) {
			message := fmt.Sprintf(format, values...)
			_, _ = fmt.Fprintln(errOut, runner.SanitizeTerminalText(message))
		},
	}
	return server.ListenAndServe(ctx, address, func(resolved net.Addr) {
		host, port, err := net.SplitHostPort(resolved.String())
		if err != nil {
			_, _ = fmt.Fprintf(out, "pt dev --ssh · %s (%s) · %s\n", manifest.Name, manifest.Type, resolved)
			return
		}
		connectHost := localConnectHost(host)
		if noSSHConfig {
			_, _ = fmt.Fprintf(out, "pt dev --ssh · %s (%s) · %s\nConnect: ssh -p %s -o StrictHostKeyChecking=accept-new %s@%s\n", manifest.Name, manifest.Type, resolved, port, manifest.Name, connectHost)
			return
		}
		configPath, installErr := installDevSSHConfig(alias, connectHost, port)
		if installErr != nil {
			_, _ = fmt.Fprintf(errOut, "could not install the %q ssh config alias (%v); use the raw command\n", alias, installErr)
			_, _ = fmt.Fprintf(out, "pt dev --ssh · %s (%s) · %s\nConnect: ssh -p %s -o StrictHostKeyChecking=accept-new %s@%s\n", manifest.Name, manifest.Type, resolved, port, manifest.Name, connectHost)
			return
		}
		_, _ = fmt.Fprintf(out, "pt dev --ssh · %s (%s) · %s\nConnect: ssh %s (alias installed in %s)\n", manifest.Name, manifest.Type, resolved, alias, configPath)
	})
}

func (r Runner) runTTY(ctx context.Context, wasm []byte, limits runner.Limits, caps runner.Capabilities, maxFPS int) error {
	in, out, errOut := r.streams()
	input, ok := in.(*os.File)
	if !ok || !xterm.IsTerminal(int(input.Fd())) {
		return errors.New("stdin is not a terminal; use pt dev --headless or pt dev --ssh")
	}
	fd := int(input.Fd())
	state, err := xterm.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("enter terminal raw mode: %w", err)
	}
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		_, _ = io.WriteString(out, plumterminal.DISABLE_MOUSE+plumterminal.SHOW_CURSOR+plumterminal.CLOSE_ALT)
		_ = xterm.Restore(fd, state)
	}
	defer restore()
	_, _ = io.WriteString(out, plumterminal.HIDE_CURSOR+plumterminal.OPEN_ALT+plumterminal.ENABLE_MOUSE+plumterminal.CLEAR_SCREEN+plumterminal.MOVE_CURSOR)

	size := func() (int, int) {
		width, height, sizeErr := xterm.GetSize(fd)
		if sizeErr != nil || width < 1 || height < 1 {
			return plumterminal.DefaultWidth, plumterminal.DefaultHeight
		}
		return width, height
	}
	width, height := size()
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, resizeSignal())
	defer signal.Stop(winch)
	source := &runner.TTYSource{Keys: keyboard.ListenReader(ctx, in), Winch: winch, Refresh: runner.DefaultRefresh, Size: size}
	sink := runner.NewTTYSinkWriter(width, height, maxFPS, out)
	logs := runner.NewLogBuffer()
	err = runner.Run(ctx, wasm, limits, caps, source, sink, logs)
	sink.Close()
	restore()
	writeDevFinish(out, errOut, caps, []byte(logs.String()))
	return err
}

func writeDevFinish(out, errOut io.Writer, caps runner.Capabilities, logs []byte) {
	if len(logs) > 0 {
		_, _ = fmt.Fprintf(errOut, "\n[app logs]\n%s\n", runner.SanitizeTerminalText(string(logs)))
	}
	if caps.Goodbye != nil && *caps.Goodbye != "" {
		_, _ = fmt.Fprintf(out, "\n%s\n", runner.SanitizeTerminalText(*caps.Goodbye))
	}
}

func splitTokens(value string) []string {
	var tokens []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			tokens = append(tokens, item)
		}
	}
	return tokens
}
