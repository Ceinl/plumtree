// Package app is the TUI runtime loop. It wires the terminal, keyboard input
// and screen buffer together and drives a layout.Component tree: every frame it
// lays the root out to the terminal size, renders it into the diffing screen
// buffer and flushes only the changed cells.
//
// The loop is deliberately generic — it knows nothing about any particular
// application. Callers supply behaviour through the OnKey/OnResize/OnTick hooks
// and mutate their own component tree (reachable via Root) between frames.
package app

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Ceinl/plumtree/tui-runtime/keyboard"
	"github.com/Ceinl/plumtree/tui-runtime/layout"
	"github.com/Ceinl/plumtree/tui-runtime/screen"
	"github.com/Ceinl/plumtree/tui-runtime/terminal"
)

// App owns the terminal, input stream and screen buffer for one running TUI.
type App struct {
	root layout.Component
	fd   int

	// OnKey is invoked for every keyboard event before the frame is repainted.
	// Return true to quit the loop. If nil, Ctrl+C quits and every other key is
	// ignored.
	OnKey func(ev keyboard.Event) (quit bool)

	// OnResize, if set, runs after the terminal is resized and before the frame
	// is repainted, e.g. to re-flow content to the new width/height.
	OnResize func(w, h int)

	// TickInterval, when > 0, repaints the screen on a fixed cadence in addition
	// to input-driven frames. Use it for animation or streaming output whose
	// rate is decoupled from keystrokes. OnTick (if set) runs before each tick
	// frame; return false to skip the repaint for that tick.
	TickInterval time.Duration
	OnTick       func() (render bool)

	// Wake lets an external event source wake an otherwise idle loop. OnWake
	// runs on the loop goroutine before rendering, keeping model mutation
	// serialized with keyboard, resize, and tick handlers.
	Wake   <-chan struct{}
	OnWake func() (render bool)
}

// New returns an App that renders root, reading input from stdin.
func New(root layout.Component) *App {
	return &App{root: root, fd: int(os.Stdin.Fd())}
}

// Run enters raw mode, paints the first frame and then loops until a quit is
// requested (via OnKey, Ctrl+C, closed input) or ctx is cancelled. The terminal
// is always restored before Run returns.
func (a *App) Run(ctx context.Context) error {
	term := terminal.New(a.fd)
	if err := term.Enter(); err != nil {
		return err
	}
	defer term.Exit()

	tmux := terminal.EnableTmuxExtendedKeys()
	defer tmux.Restore()

	scr := screen.NewScreen(term.W, term.H)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	keys := keyboard.Listen(ctx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	// SIGINT/SIGTERM/SIGHUP terminate the process by default, so deferred
	// cleanup never runs and the terminal is left in raw mode + alt screen with
	// a hidden cursor. Catch them here, restore the terminal and tmux state,
	// then re-raise the signal with the default handler so the process exits
	// with the conventional status. term.Exit and tmux.Restore are idempotent,
	// so the deferred calls above remain safe.
	fatalCh := make(chan os.Signal, 1)
	signal.Notify(fatalCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(fatalCh)
	go func() {
		sig, ok := <-fatalCh
		if !ok {
			return
		}
		_ = term.Exit()
		tmux.Restore()
		signal.Stop(fatalCh)
		// Re-raise with the default disposition so we exit as if uncaught.
		if p, err := os.FindProcess(os.Getpid()); err == nil {
			signal.Reset(sig.(syscall.Signal))
			_ = p.Signal(sig)
		}
	}()

	var tick <-chan time.Time
	if a.TickInterval > 0 {
		ticker := time.NewTicker(a.TickInterval)
		defer ticker.Stop()
		tick = ticker.C
	}

	a.render(scr)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-keys:
			if !ok {
				return nil
			}
			if a.handleKey(ev) {
				return nil
			}
			a.render(scr)
		case <-sigCh:
			if err := term.RefreshSize(); err == nil {
				scr.Resize(term.W, term.H)
				if a.OnResize != nil {
					a.OnResize(term.W, term.H)
				}
			}
			a.render(scr)
		case <-tick:
			if a.OnTick != nil && !a.OnTick() {
				continue
			}
			a.render(scr)
		case <-a.Wake:
			if a.OnWake != nil && !a.OnWake() {
				continue
			}
			a.render(scr)
		}
	}
}

func (a *App) handleKey(ev keyboard.Event) (quit bool) {
	if a.OnKey != nil {
		return a.OnKey(ev)
	}
	return ev.Type == keyboard.KeyCtrlC
}

func (a *App) render(scr *screen.Screen) {
	if a.root == nil {
		return
	}
	a.root.Layout(0, 0, scr.Width(), scr.Height())
	a.root.Render(scr)
	scr.Flush()
}
