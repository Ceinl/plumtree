package sdk

import (
	"fmt"
	"io"
	"os"
)

// CLI runs a non-interactive app. The handler receives a Ctx for output and the
// invocation arguments. Returning an error exits non-zero. Like RunTUI, the
// same code runs natively and hosted; hosted, the platform filters control
// characters from the text output before forwarding it.
func CLI(_ Meta, handler func(Ctx, []string) error) {
	ctx := Ctx{out: &Out{w: os.Stdout}}
	if err := handler(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// Ctx is the capability handle passed to a CLI app. For now it exposes text
// output; storage, auth, env, and fetch are added in later phases.
type Ctx struct{ out *Out }

// Out returns the app's text output sink.
func (c Ctx) Out() *Out { return c.out }

// Out is a text output sink. Hosted, the platform sanitizes what is written.
type Out struct{ w io.Writer }

// Printf writes formatted text.
func (o *Out) Printf(format string, a ...any) { fmt.Fprintf(o.w, format, a...) }

// Print writes its operands.
func (o *Out) Print(a ...any) { fmt.Fprint(o.w, a...) }

// Println writes its operands followed by a newline.
func (o *Out) Println(a ...any) { fmt.Fprintln(o.w, a...) }
