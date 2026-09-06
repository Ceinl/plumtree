package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Ceinl/plumtree/sdk/cli"
	"github.com/Ceinl/plumtree/sdk/identity"
)

// cliSession is the capability state one exec invocation needs.
type cliSession struct {
	uid      string
	name     string
	id       identity.Identity
	cfg      config
	history  []message
	memories []string
}

func loadCLISession(ctx context.Context, lookup lookupFunc) cliSession {
	id, err := identityWhoami(ctx)
	if err != nil {
		id = identity.Identity{User: "anonymous:unknown", Kind: identity.KindAnonymous}
	}
	uid := uidFor(id.User)
	return cliSession{
		uid:      uid,
		name:     loadName(ctx, uid),
		id:       id,
		cfg:      loadConfig(ctx, lookup),
		history:  loadConversation(ctx, uid),
		memories: loadMemories(ctx, uid),
	}
}

// buildCLI assembles the SSH-exec command tree: the same spirit as the TUI,
// one question at a time. `complete` and `lookup` are injected so plumtest
// can run the tree without network or ambient secrets.
func buildCLI(complete completeFunc, lookup lookupFunc) cli.Command {
	return cli.Root("familiar — a terminal spirit (GLM) living in this server",
		cli.New("ask", "ask the familiar one question and get one answer").
			WithArgs(cli.AnyArgs()).
			WithArgument(cli.StringsArg("question", "everything after `ask` is the question")).
			WithFlag(cli.BoolFlag("fresh", "ignore conversation history for this question")).
			WithHandler(func(call cli.Context, _ []string) (cli.Output, error) {
				words, err := call.ArgStrings("question")
				if err != nil {
					return cli.Empty(), err
				}
				question := strings.TrimSpace(strings.Join(words, " "))
				if question == "" {
					return cli.Empty(), cli.ErrUsage
				}
				session := loadCLISession(call, lookup)
				if !session.cfg.HasKey {
					return cli.Empty(), cli.Error{Code: "no_key", Message: session.cfg.Hint, ExitCode: 1}
				}
				fresh, err := call.Bool("fresh")
				if err != nil {
					return cli.Empty(), err
				}
				history := session.history
				if fresh {
					history = nil
				} else {
					if err := saveConversation(call, session.uid, history); err != nil {
						fmt.Fprintf(call.Stderr, "warn: could not save conversation: %v\n", err)
					}
				}
				history = append(history, message{Role: "user", Content: question})
				done := complete(call, session.cfg, apiHistory(history), session.memories)
				if done.Err != "" {
					return cli.Empty(), cli.Error{Code: "completion", Message: done.Err, ExitCode: 1}
				}
				if !fresh {
					history = append(history, message{Role: "assistant", Content: done.Text})
					if err := saveConversation(call, session.uid, history); err != nil {
						fmt.Fprintf(call.Stderr, "warn: could not save conversation: %v\n", err)
					}
				}
				return cli.Present(done.Text, func(writer cli.Writer, text string) {
					writer.Println(text)
				}), nil
			}),
		cli.New("history", "show recent conversation turns").
			WithFlag(cli.IntFlag("count", "turns to show").WithDefault(10)).
			WithHandler(func(call cli.Context, _ []string) (cli.Output, error) {
				session := loadCLISession(call, lookup)
				if len(session.history) == 0 {
					return cli.Present("no conversation yet", func(writer cli.Writer, text string) {
						writer.Println(text)
					}), nil
				}
				count, err := call.Int("count")
				if err != nil || count <= 0 {
					count = 10
				}
				if len(session.history) > count {
					session.history = session.history[len(session.history)-count:]
				}
				var rendered strings.Builder
				for _, msg := range session.history {
					who := "familiar"
					if msg.Role == "user" {
						who = "you"
					}
					rendered.WriteString(who + " › " + truncateRunes(msg.Content, 2000) + "\n")
				}
				text := strings.TrimRight(rendered.String(), "\n")
				return cli.Present(text, func(writer cli.Writer, value string) {
					writer.Println(value)
				}), nil
			}),
		cli.New("clear", "forget the whole conversation").
			WithHandler(func(call cli.Context, _ []string) (cli.Output, error) {
				session := loadCLISession(call, lookup)
				if err := clearConversation(call, session.uid); err != nil {
					return cli.Empty(), cli.Error{Code: "storage", Message: err.Error(), ExitCode: 1}
				}
				return cli.Present("forgotten.", func(writer cli.Writer, text string) {
					writer.Println(text)
				}), nil
			}),
		cli.New("remember", "store a durable note that is sent with every question").
			WithArgs(cli.AnyArgs()).
			WithArgument(cli.StringsArg("fact", "the note to remember")).
			WithHandler(func(call cli.Context, _ []string) (cli.Output, error) {
				words, err := call.ArgStrings("fact")
				if err != nil {
					return cli.Empty(), err
				}
				fact := strings.TrimSpace(strings.Join(words, " "))
				if fact == "" {
					return cli.Empty(), cli.ErrUsage
				}
				session := loadCLISession(call, lookup)
				if len(session.memories) >= memMax {
					return cli.Empty(), cli.Error{Code: "memory_full", Message: "memory is full — forget something first", ExitCode: 1}
				}
				session.memories = append(session.memories, truncateRunes(fact, memMaxRunes))
				if err := saveMemories(call, session.uid, session.memories); err != nil {
					return cli.Empty(), cli.Error{Code: "storage", Message: err.Error(), ExitCode: 1}
				}
				return cli.Present("noted.", func(writer cli.Writer, text string) {
					writer.Println(text)
				}), nil
			}),
		cli.New("forget", "drop one memory by number, or all of them").
			WithArgs(cli.ExactArgs(1)).
			WithArgument(cli.StringArg("target", "memory number or `all`")).
			WithHandler(func(call cli.Context, _ []string) (cli.Output, error) {
				target, err := call.ArgString("target")
				if err != nil {
					return cli.Empty(), err
				}
				session := loadCLISession(call, lookup)
				switch {
				case target == "all":
					session.memories = nil
				default:
					index, convErr := strconv.Atoi(target)
					if convErr != nil || index < 1 || index > len(session.memories) {
						return cli.Empty(), cli.Error{Code: "usage", Message: "choose a memory number 1-" + strconv.Itoa(len(session.memories)) + " or `all`", ExitCode: 1}
					}
					session.memories = append(session.memories[:index-1], session.memories[index:]...)
				}
				if err := saveMemories(call, session.uid, session.memories); err != nil {
					return cli.Empty(), cli.Error{Code: "storage", Message: err.Error(), ExitCode: 1}
				}
				return cli.Present("forgotten.", func(writer cli.Writer, text string) {
					writer.Println(text)
				}), nil
			}),
		cli.New("memories", "list remembered notes").
			WithHandler(func(call cli.Context, _ []string) (cli.Output, error) {
				session := loadCLISession(call, lookup)
				if len(session.memories) == 0 {
					return cli.Present("nothing remembered yet", func(writer cli.Writer, text string) {
						writer.Println(text)
					}), nil
				}
				var rendered strings.Builder
				for index, memory := range session.memories {
					rendered.WriteString(strconv.Itoa(index+1) + ". " + memory + "\n")
				}
				text := strings.TrimRight(rendered.String(), "\n")
				return cli.Present(text, func(writer cli.Writer, value string) {
					writer.Println(value)
				}), nil
			}),
		cli.New("status", "show configuration without leaking secrets").
			WithHandler(func(call cli.Context, _ []string) (cli.Output, error) {
				session := loadCLISession(call, lookup)
				keyState := "missing — `pt secret set GLM_API_KEY=<key>` (dev: GLM_API_KEY in env)"
				if session.cfg.HasKey {
					keyState = "set"
				}
				persona := "default"
				if session.cfg.Persona != defaultPersona() {
					persona = "custom"
				}
				text := strings.Join([]string{
					"model     " + session.cfg.Model,
					"endpoint  " + hostOf(session.cfg.BaseURL),
					"transport " + session.cfg.Transport,
					"api key   " + keyState,
					"persona   " + persona,
					"identity  " + session.id.User,
					"uid       " + session.uid,
				}, "\n")
				return cli.Present(text, func(writer cli.Writer, value string) {
					writer.Println(value)
				}), nil
			}),
	)
}
