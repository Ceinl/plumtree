// Package sdk is the author-facing surface for Plumtree apps. An app is an
// ordinary Go program that builds against this package; `pt` compiles it to
// WebAssembly and the platform runs it sandboxed, streaming it over SSH.
//
// A TUI app implements Model and calls RunTUI:
//
//	type app struct{ n int }
//	func (a *app) Update(ev sdk.Event) {
//	    if k, ok := ev.(sdk.KeyMsg); ok && k.Key == sdk.KeyUp { a.n++ }
//	}
//	func (a *app) View() tui.Component { ... }
//	func main() { sdk.RunTUI(&app{}, sdk.Meta{Name: "counter", Type: "tui"}) }
//
// RunTUI has two implementations selected at build time: a native terminal loop
// (for `go run` and tests) and a WASM-guest loop that speaks the host ABI. App
// code is identical for both — the low-level ABI is hidden here.
package sdk

import "github.com/Ceinl/plumtree/sdk/tui"

// Meta describes an app to the platform.
type Meta struct {
	Name string // app handle; [a-z0-9-]
	Type string // "tui" or "cli"
}

// Model is a TUI app: it mutates state in response to events and rebuilds its
// view each frame. The runtime lays out and renders the returned tree.
type Model interface {
	// Update applies one input event to the model's state.
	Update(Event)
	// View builds the component tree for the current state.
	View() tui.Component
}

// quitRequested is set by Quit and read by both RunTUI loops after Update.
var quitRequested bool

// Quit asks the runtime to end the session after the current frame.
func Quit() { quitRequested = true }
