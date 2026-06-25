// Command host is the Plumtree feasibility-spike runner: it loads a guest
// counter.wasm, drives it one frame at a time over the abi ptr/len contract,
// and renders the structured frames it returns.
//
// Two modes:
//
//	host -headless -script "up,up,down,q"   deterministic, no PTY (default here)
//	host                                    interactive, needs a real terminal
//
// The guest runs in wazero with no filesystem, env, args, or network, a
// linear-memory cap, and a per-frame wall-clock deadline.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Ceinl/plumtree/tui-runtime/keyboard"
	"github.com/Ceinl/plumtree/tui-runtime/terminal"
	"github.com/Ceinl/plumtree/spike/abi"
)

func main() {
	var (
		wasmPath     = flag.String("wasm", "dist/counter.wasm", "path to the guest .wasm")
		headless     = flag.Bool("headless", false, "run a scripted session without a PTY")
		script       = flag.String("script", "up,up,up,down,q", "headless: comma-separated input tokens")
		cols         = flag.Int("w", 36, "headless: frame width")
		rows         = flag.Int("h", 9, "headless: frame height")
		memPages     = flag.Uint("mem-pages", 512, "linear-memory cap in 64KiB pages")
		frameTimeout = flag.Duration("frame-timeout", 2*time.Second, "per-frame wall-clock deadline")
		maxFPS       = flag.Int("max-fps", 60, "tty: max repaints per second (output rate cap)")
	)
	flag.Parse()

	wasm, err := os.ReadFile(*wasmPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read wasm: %v\n(build it with ./build.sh)\n", err)
		os.Exit(1)
	}

	lim := Limits{MemoryPages: uint32(*memPages), FrameTimeout: *frameTimeout}

	if *headless {
		if err := runHeadless(wasm, lim, *script, *cols, *rows); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if err := runTTY(wasm, lim, *maxFPS); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// runHeadless steps the guest through a fixed input script, printing each frame
// as plain text. Deterministic and TTY-free, so it works in CI and here.
func runHeadless(wasm []byte, lim Limits, script string, w, h int) error {
	ctx := context.Background()
	var logs bytes.Buffer

	sess, err := NewSession(ctx, wasm, lim, &logs)
	if err != nil {
		return err
	}
	defer sess.Close(ctx)

	fmt.Printf("guest loaded · %dx%d · mem-cap %d pages · frame-deadline %s\n",
		w, h, lim.MemoryPages, lim.FrameTimeout)

	// Build the step list: an initial repaint, then one step per script token.
	steps := []string{"(initial)"}
	steps = append(steps, splitTokens(script)...)

	for i, tok := range steps {
		var event []byte
		if i > 0 {
			ev, ok := parseScriptToken(tok)
			if !ok {
				fmt.Printf("\n? skipping unknown token %q\n", tok)
				continue
			}
			if ev.Kind != abi.KindNone {
				event = abi.EncodeEvent(ev)
			}
		}

		frame, err := sess.Frame(ctx, w, h, event)
		if errors.Is(err, ErrFrameDeadline) {
			fmt.Printf("\n── %s ──\n", tok)
			fmt.Printf("✗ %v (host cancelled the runaway guest)\n", err)
			break
		}
		if err != nil {
			return fmt.Errorf("frame %q: %w", tok, err)
		}

		fmt.Printf("\n── %s ──\n", tok)
		renderText(os.Stdout, frame)
		if frame.Quit {
			fmt.Println("guest requested quit")
			break
		}
	}

	if logs.Len() > 0 {
		fmt.Printf("\n[guest logs]\n%s\n", logs.String())
	}
	return nil
}

func splitTokens(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// runTTY runs the interactive loop against a real terminal: raw mode, live key
// input, SIGWINCH resize, and host-rendered frames. Requires a PTY.
func runTTY(wasm []byte, lim Limits, maxFPS int) error {
	fd := int(os.Stdin.Fd())
	term := terminal.New(fd)
	if err := term.Enter(); err != nil {
		if errors.Is(err, terminal.ErrNotTerminal) {
			return errors.New("not a terminal — use: host -headless -script \"up,up,down,q\"")
		}
		return err
	}
	defer term.Exit()

	// Guest logs would corrupt the alt screen; stash them and print on exit.
	var logs bytes.Buffer
	defer func() {
		if logs.Len() > 0 {
			fmt.Fprintf(os.Stderr, "\n[guest logs]\n%s\n", logs.String())
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sess, err := NewSession(ctx, wasm, lim, &logs)
	if err != nil {
		return err
	}
	defer sess.Close(ctx)

	rend := newTTYRenderer(term.W, term.H)
	thr := newThrottle(maxFPS)

	draw := func(event []byte) (quit bool, err error) {
		f, err := sess.Frame(ctx, term.W, term.H, event)
		if err != nil {
			return false, err
		}
		if thr.allow(time.Now()) {
			rend.draw(f)
		}
		return f.Quit, nil
	}

	if _, err := draw(nil); err != nil {
		return err
	}

	keys := keyboard.Listen(ctx)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-winch:
			if err := term.RefreshSize(); err == nil {
				if _, err := draw(nil); err != nil {
					return err
				}
			}
		case ev, ok := <-keys:
			if !ok {
				return nil
			}
			abiEv, ok := mapKey(ev)
			if !ok {
				continue
			}
			quit, err := draw(abi.EncodeEvent(abiEv))
			if err != nil {
				return err
			}
			if quit {
				return nil
			}
		}
	}
}
