package main

import "github.com/Ceinl/plumtree/sdk"

func main() {
	sdk.RunTUIWithActions(&boardModel{}, sdk.Meta{Name: "agentboard", Type: "tui"}, appActions())
}
