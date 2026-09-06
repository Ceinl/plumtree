package ui

import (
	"fmt"
	"testing"
)

// benchTree builds a representative TUI view: bordered column, header text,
// rows of styled text, and buttons.
func benchTree(rows int) Node {
	children := make([]Node, 0, rows+3)
	children = append(children, Text("┌─ header ─┐").Bold())
	for i := 0; i < rows; i++ {
		children = append(children,
			Row(
				Textf("row %d: %s", i, "some rendered content here"),
				Button("ok", struct{}{}),
			).Gap(1),
		)
	}
	children = append(children, Text("footer"))
	return Column(children...).Border(Rounded).Padding(All(1)).Gap(1)
}

func BenchmarkRender(b *testing.B) {
	for _, rows := range []int{4, 24} {
		b.Run(fmt.Sprintf("rows=%d", rows), func(b *testing.B) {
			theme := DefaultTheme()
			tree := benchTree(rows)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = RenderWithTheme(tree, 160, 48, theme)
			}
		})
	}
}

// BenchmarkRenderer measures the runtime's steady-state storage reuse. View
// construction and WASM execution are outside this component benchmark.
func BenchmarkRenderer(b *testing.B) {
	for _, rows := range []int{4, 24} {
		b.Run(fmt.Sprintf("rows=%d", rows), func(b *testing.B) {
			var renderer Renderer
			tree := benchTree(rows)
			theme := DefaultTheme()
			renderer.RenderWithTheme(tree, 160, 48, theme)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				renderer.RenderWithTheme(tree, 160, 48, theme)
			}
		})
	}
}
