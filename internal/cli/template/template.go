// Package template exposes the embedded `pt new` project template.
package template

import "embed"

// Files contains the scaffold source tree. `base` is always applied, then the
// app-kind directory (`tui`, `cli` or `vm`) overlays kind-specific files.
//
//go:embed all:base all:tui all:cli all:vm
var Files embed.FS
