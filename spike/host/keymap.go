package main

import (
	"strings"

	"github.com/Ceinl/plumtree/tui-runtime/keyboard"
	"github.com/Ceinl/plumtree/spike/abi"
)

// mapKey translates a host keyboard event into the ABI-stable event the guest
// understands. Mouse, paste, and unknown events are dropped for the spike
// (ok=false). This is the host->guest input boundary.
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
	switch ev.Type {
	case keyboard.KeyRune:
		e.Key, e.Ch = abi.KeyRune, ev.Ch
	case keyboard.KeyEnter:
		e.Key = abi.KeyEnter
	case keyboard.KeyBackspace:
		e.Key = abi.KeyBackspace
	case keyboard.KeyTab:
		e.Key = abi.KeyTab
	case keyboard.KeyEscape:
		e.Key = abi.KeyEscape
	case keyboard.KeyCtrlC:
		e.Key = abi.KeyCtrlC
	case keyboard.KeyArrowUp:
		e.Key = abi.KeyArrowUp
	case keyboard.KeyArrowDown:
		e.Key = abi.KeyArrowDown
	case keyboard.KeyArrowLeft:
		e.Key = abi.KeyArrowLeft
	case keyboard.KeyArrowRight:
		e.Key = abi.KeyArrowRight
	case keyboard.KeyHome:
		e.Key = abi.KeyHome
	case keyboard.KeyEnd:
		e.Key = abi.KeyEnd
	case keyboard.KeyPageUp:
		e.Key = abi.KeyPageUp
	case keyboard.KeyPageDown:
		e.Key = abi.KeyPageDown
	case keyboard.KeyDelete:
		e.Key = abi.KeyDelete
	default:
		return abi.Event{}, false
	}
	return e, true
}

// parseScriptToken maps a headless-script token to an event. Named tokens cover
// special keys; "none"/"tick" is a repaint; any other single rune is a rune key.
func parseScriptToken(tok string) (abi.Event, bool) {
	switch strings.ToLower(strings.TrimSpace(tok)) {
	case "", "none", "tick":
		return abi.Event{Kind: abi.KindNone}, true
	case "up":
		return abi.Event{Kind: abi.KindKey, Key: abi.KeyArrowUp}, true
	case "down":
		return abi.Event{Kind: abi.KindKey, Key: abi.KeyArrowDown}, true
	case "left":
		return abi.Event{Kind: abi.KindKey, Key: abi.KeyArrowLeft}, true
	case "right":
		return abi.Event{Kind: abi.KindKey, Key: abi.KeyArrowRight}, true
	case "enter":
		return abi.Event{Kind: abi.KindKey, Key: abi.KeyEnter}, true
	case "tab":
		return abi.Event{Kind: abi.KindKey, Key: abi.KeyTab}, true
	case "esc":
		return abi.Event{Kind: abi.KindKey, Key: abi.KeyEscape}, true
	case "ctrl-c":
		return abi.Event{Kind: abi.KindKey, Key: abi.KeyCtrlC, Mods: abi.ModCtrl}, true
	}
	// Single rune (e.g. "q", "+", "b").
	if r := []rune(tok); len(r) == 1 {
		return abi.Event{Kind: abi.KindKey, Key: abi.KeyRune, Ch: r[0]}, true
	}
	return abi.Event{}, false
}
