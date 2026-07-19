package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Ceinl/plumtree/pt/internal/sshdev"
	"github.com/Ceinl/plumtree/runner"
	"github.com/Ceinl/plumtree/tui-runtime/keyboard"
	"github.com/Ceinl/plumtree/tui-runtime/terminal"
)

func cmdDev(args []string) error {
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	headless := fs.Bool("headless", false, "run without a terminal, scripted")
	script := fs.String("script", "up,up,down,q", "headless input tokens (comma-separated)")
	cols := fs.Int("w", 40, "headless frame width")
	rows := fs.Int("h", 12, "headless frame height")
	memPages := fs.Uint("mem-pages", uint(runner.DefaultLimits.MemoryPages), "linear-memory cap in 64KiB pages")
	frameTimeout := fs.Duration("frame-timeout", runner.DefaultLimits.FrameTimeout, "per-frame wall-clock deadline")
	maxFPS := fs.Int("max-fps", 60, "tty/ssh repaint cap (frames/sec)")
	sshMode := fs.Bool("ssh", false, "serve the app over SSH (connect with: ssh <app>@plumtree.dev)")
	addr := fs.String("addr", "127.0.0.1:2222", "ssh: listen address")
	sshHost := fs.String("host", "plumtree.dev", "ssh: local host alias")
	noSSHConfig := fs.Bool("no-ssh-config", false, "ssh: do not update ~/.ssh/config")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *memPages == 0 || uint64(*memPages) > uint64(runner.MaxMemoryPages) {
		return fmt.Errorf("mem-pages must be between 1 and %d", runner.MaxMemoryPages)
	}

	proj, err := findProject()
	if err != nil {
		return err
	}
	man, err := readManifest(proj)
	if err != nil {
		return err
	}

	wasm, cleanup, err := buildWASM(proj)
	if err != nil {
		return err
	}
	defer cleanup()

	lim := runner.Limits{MemoryPages: uint32(*memPages), FrameTimeout: *frameTimeout}
	ctx := context.Background()

	caps, err := devCapabilities(proj)
	if err != nil {
		return err
	}

	if *sshMode {
		return runSSH(ctx, wasm, lim, caps, man, *addr, *sshHost, !*noSSHConfig, *maxFPS)
	}

	switch man.Type {
	case "cli":
		return runner.RunCLI(ctx, wasm, lim, caps, fs.Args(), os.Stdout)
	default:
		if *headless {
			return runHeadless(ctx, wasm, lim, caps, *script, *cols, *rows)
		}
		return runTTY(ctx, wasm, lim, caps, *maxFPS)
	}
}

func devCapabilities(proj string) (runner.Capabilities, error) {
	kv, err := runner.NewFileStore(
		filepath.Join(proj, ".plumtree", "kv.json"),
		runner.DefaultMaxKeys, runner.DefaultMaxBytes,
	)
	if err != nil {
		return runner.Capabilities{}, fmt.Errorf("open kv store: %w", err)
	}
	// One bus shared across this process's sessions, so `pt dev --ssh`
	// connections to the same app exchange live pub/sub messages. Auth reports a
	// fixed local identity in dev (there is no platform identity locally).
	return runner.Capabilities{
		KV:   kv,
		Bus:  runner.NewMemBus(),
		Auth: runner.StaticAuth{Identity: runner.Identity{User: "local"}},
	}, nil
}

func runSSH(ctx context.Context, wasm []byte, lim runner.Limits, caps runner.Capabilities, man manifest, addr, host string, installConfig bool, maxFPS int) error {
	if err := validateSSHHost(host); err != nil {
		return err
	}
	srv := &sshdev.Server{
		Wasm:    wasm,
		Limits:  lim,
		Caps:    caps,
		AppType: man.Type,
		AppName: man.Name,
		MaxFPS:  maxFPS,
		Logf:    func(f string, a ...any) { fmt.Fprintf(os.Stderr, "  "+f+"\n", a...) },
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	ready := func(a net.Addr) {
		listenHost, port, _ := net.SplitHostPort(a.String())
		connectHost := localConnectHost(listenHost)
		name := man.Name
		if name == "" {
			name = "app"
		}
		fmt.Printf("pt dev — serving %q (%s) over SSH on port %s\n\n", man.Name, man.Type, port)
		if installConfig {
			path, err := installDevSSHConfig(host, connectHost, port)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  ssh config update failed: %v\n", err)
				fmt.Printf("Connect:\n  ssh -p %s -o HostKeyAlias=%s -o StrictHostKeyChecking=accept-new %s@%s\n\n", port, devSSHHostKeyAlias, name, connectHost)
			} else {
				fmt.Printf("SSH config: %s maps %s -> %s:%s\n\n", path, host, connectHost, port)
				fmt.Printf("Connect:\n  ssh %s@%s\n\n", name, host)
			}
		} else {
			fmt.Printf("Connect:\n  ssh -p %s -o HostKeyAlias=%s -o StrictHostKeyChecking=accept-new %s@%s\n\n", port, devSSHHostKeyAlias, name, connectHost)
		}
		fmt.Println("Ctrl-C to stop.")
	}
	return srv.ListenAndServe(ctx, addr, ready)
}

func runHeadless(ctx context.Context, wasm []byte, lim runner.Limits, caps runner.Capabilities, script string, w, h int) error {
	fmt.Printf("pt dev (headless) · %dx%d · mem-cap %d pages · frame-deadline %s\n",
		w, h, lim.MemoryPages, lim.FrameTimeout)

	src := runner.NewScriptSource(w, h, splitTokens(script))
	src.Echo = os.Stdout

	var logs bytes.Buffer
	err := runner.Run(ctx, wasm, lim, caps, src, runner.TextSink{W: os.Stdout}, &logs)
	if errors.Is(err, runner.ErrFrameDeadline) {
		fmt.Printf("\n✗ %v\n", err)
		err = nil
	}
	if logs.Len() > 0 {
		fmt.Printf("\n[app logs]\n%s\n", logs.String())
	}
	return err
}

func runTTY(ctx context.Context, wasm []byte, lim runner.Limits, caps runner.Capabilities, maxFPS int) error {
	fd := int(os.Stdin.Fd())
	term := terminal.New(fd)
	if err := term.Enter(); err != nil {
		if errors.Is(err, terminal.ErrNotTerminal) {
			return errors.New("not a terminal — use: pt dev --headless")
		}
		return err
	}
	defer term.Exit()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	keys := keyboard.Listen(ctx)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, resizeSignal())
	defer signal.Stop(winch)

	src := &runner.TTYSource{
		Keys:    keys,
		Winch:   winch,
		Refresh: runner.DefaultRefresh,
		Size: func() (int, int) {
			_ = term.RefreshSize()
			return term.W, term.H
		},
	}
	sink := runner.NewTTYSink(term.W, term.H, maxFPS)

	var logs bytes.Buffer
	err := runner.Run(ctx, wasm, lim, caps, src, sink, &logs)
	_ = term.Exit()
	if logs.Len() > 0 {
		fmt.Fprintf(os.Stderr, "\n[app logs]\n%s\n", logs.String())
	}
	return err
}
