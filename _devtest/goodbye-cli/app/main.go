// Command goodbye-cli is a Plumtree CLI app.
package main

import "github.com/Ceinl/plumtree/sdk"

func main() {
	sdk.CLI(sdk.Meta{Name: "goodbye-cli", Type: "cli"},
		func(ctx sdk.Ctx, args []string) error {
			sdk.SetGoodbye("Goodbye from goodbye-cli!")
			name := "world"
			if len(args) > 0 {
				name = args[0]
			}
			ctx.Out().Printf("Hello %s\n", name)
			return nil
		})
}
