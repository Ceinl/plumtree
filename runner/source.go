package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Ceinl/plumtree/tui-runtime/keyboard"
	"github.com/Ceinl/plumtree/sdk/abi"
)

// DefaultRefresh is a reasonable repaint cadence for apps that poll shared state
// (e.g. KV-backed apps); it is the interval the SSH/TTY hosts pass to TTYSource.
const DefaultRefresh = 750 * time.Millisecond

// ScriptSource feeds a fixed sequence of events for headless runs: an initial
// resize, then one event per script token. When Echo is set it prints a label
// before each event so output interleaves as "label, frame, label, frame…".
type ScriptSource struct {
	Echo  io.Writer
	steps []step
	i     int
}

type step struct {
	label string
	ev    abi.Event
}

// NewScriptSource builds a source sized w x h that replays the given tokens
// (see ParseToken). Unrecognized tokens are dropped.
func NewScriptSource(w, h int, tokens []string) *ScriptSource {
	steps := []step{{label: "(initial)", ev: abi.Event{Kind: abi.KindResize, W: w, H: h}}}
	for _, t := range tokens {
		if ev, ok := ParseToken(t); ok {
			steps = append(steps, step{label: t, ev: ev})
		}
	}
	return &ScriptSource{steps: steps}
}

func (s *ScriptSource) Next(context.Context) (abi.Event, bool) {
	if s.i >= len(s.steps) {
		return abi.Event{}, false
	}
	st := s.steps[s.i]
	s.i++
	if s.Echo != nil {
		fmt.Fprintf(s.Echo, "\n── %s ──\n", st.label)
	}
	return st.ev, true
}

// TTYSource feeds live terminal input: an initial resize, then key events and
// resize notifications until the keyboard channel closes or ctx is cancelled.
type TTYSource struct {
	Keys  <-chan keyboard.Event
	Winch <-chan os.Signal
	Size  func() (w, h int)
	// Refresh, when > 0, emits a periodic KindNone repaint event so apps that
	// poll shared state (e.g. KV) redraw without local input. The host's frame
	// rate cap drops repaints that produce no change, so an idle app is cheap.
	Refresh time.Duration

	// bus, when set, delivers pub/sub messages (KindMessage events) the session
	// has subscribed to; Next selects on it so a published message wakes an
	// otherwise idle session immediately. Set by the runner via BindBus.
	bus <-chan abi.Event

	sentInitial bool
	ticker      *time.Ticker
}

// BindBus wires the session's subscription channel into the input select. It
// satisfies runner.BusBinder.
func (s *TTYSource) BindBus(events <-chan abi.Event) { s.bus = events }

func (s *TTYSource) Next(ctx context.Context) (abi.Event, bool) {
	if !s.sentInitial {
		s.sentInitial = true
		if s.Refresh > 0 {
			s.ticker = time.NewTicker(s.Refresh)
		}
		w, h := s.Size()
		return abi.Event{Kind: abi.KindResize, W: w, H: h}, true
	}
	var tick <-chan time.Time
	if s.ticker != nil {
		tick = s.ticker.C
	}
	for {
		select {
		case <-ctx.Done():
			s.stop()
			return abi.Event{}, false
		case ev := <-s.bus:
			return ev, true
		case <-tick:
			return abi.Event{Kind: abi.KindNone}, true
		case <-s.Winch:
			w, h := s.Size()
			return abi.Event{Kind: abi.KindResize, W: w, H: h}, true
		case ev, ok := <-s.Keys:
			if !ok {
				s.stop()
				return abi.Event{}, false
			}
			if ae, ok := mapKey(ev); ok {
				return ae, true
			}
			// Unmapped (mouse/paste/unknown): keep waiting.
		}
	}
}

func (s *TTYSource) stop() {
	if s.ticker != nil {
		s.ticker.Stop()
		s.ticker = nil
	}
}

// mapKey translates a runtime keyboard event to an ABI event.
func mapKey(ev keyboard.Event) (abi.Event, bool) {
	var m abi.Mods
	if ev.Shift {
		m |= abi.ModShift
	}
	if ev.Ctrl {
		m |= abi.ModCtrl
	}
	if ev.Alt {
		m |= abi.ModAlt
	}
	if ev.Cmd {
		m |= abi.ModCmd
	}
	e := abi.Event{Kind: abi.KindKey, Mods: m}
	if ev.Type == keyboard.KeyRune {
		e.Key, e.Ch = abi.KeyRune, ev.Ch
		return e, true
	}
	key, ok := keyMap[ev.Type]
	if !ok {
		return abi.Event{}, false
	}
	e.Key = key
	return e, true
}

var keyMap = map[keyboard.EventType]abi.KeyType{
	keyboard.KeyEnter:      abi.KeyEnter,
	keyboard.KeyBackspace:  abi.KeyBackspace,
	keyboard.KeyTab:        abi.KeyTab,
	keyboard.KeyEscape:     abi.KeyEscape,
	keyboard.KeyCtrlC:      abi.KeyCtrlC,
	keyboard.KeyArrowUp:    abi.KeyArrowUp,
	keyboard.KeyArrowDown:  abi.KeyArrowDown,
	keyboard.KeyArrowLeft:  abi.KeyArrowLeft,
	keyboard.KeyArrowRight: abi.KeyArrowRight,
	keyboard.KeyHome:       abi.KeyHome,
	keyboard.KeyEnd:        abi.KeyEnd,
	keyboard.KeyPageUp:     abi.KeyPageUp,
	keyboard.KeyPageDown:   abi.KeyPageDown,
	keyboard.KeyDelete:     abi.KeyDelete,
}

// ParseToken maps a headless-script token to an event. Named tokens cover
// special keys; "none"/"tick" is a repaint; any other single rune is a rune key.
func ParseToken(tok string) (abi.Event, bool) {
	tok = strings.ToLower(strings.TrimSpace(tok))
	if ev, ok := tokenMap[tok]; ok {
		return ev, true
	}
	if r := []rune(tok); len(r) == 1 {
		return abi.Event{Kind: abi.KindKey, Key: abi.KeyRune, Ch: r[0]}, true
	}
	return abi.Event{}, false
}

var tokenMap = map[string]abi.Event{
	"":       {Kind: abi.KindNone},
	"none":   {Kind: abi.KindNone},
	"tick":   {Kind: abi.KindNone},
	"up":     {Kind: abi.KindKey, Key: abi.KeyArrowUp},
	"down":   {Kind: abi.KindKey, Key: abi.KeyArrowDown},
	"left":   {Kind: abi.KindKey, Key: abi.KeyArrowLeft},
	"right":  {Kind: abi.KindKey, Key: abi.KeyArrowRight},
	"enter":  {Kind: abi.KindKey, Key: abi.KeyEnter},
	"tab":    {Kind: abi.KindKey, Key: abi.KeyTab},
	"esc":    {Kind: abi.KindKey, Key: abi.KeyEscape},
	"ctrl-c": {Kind: abi.KindKey, Key: abi.KeyCtrlC, Mods: abi.ModCtrl},
}
