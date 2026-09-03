package cleanrole

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func executeHelp(t *testing.T, args []string) (string, error) {
	t.Helper()
	out := &bytes.Buffer{}
	err := Execute(context.Background(), args, nil, out, &bytes.Buffer{})
	return out.String(), err
}

func TestEveryDispatchableCommandHasHelpTopic(t *testing.T) {
	commands := []string{"serve", "bootstrap", "config", "state", "suspend", "unsuspend", "quota"}
	for _, command := range commands {
		if err := writeHelp(&bytes.Buffer{}, command); err != nil {
			t.Errorf("plumtree help %s: %v", command, err)
		}
		if _, err := executeHelp(t, []string{"help", command}); err != nil {
			t.Errorf("plumtree help %s via Execute: %v", command, err)
		}
		if _, err := executeHelp(t, []string{command, "--help"}); err != nil {
			t.Errorf("plumtree %s --help via Execute: %v", command, err)
		}
	}
	if err := writeHelp(&bytes.Buffer{}, ""); err != nil {
		t.Fatalf("root help: %v", err)
	}
	if err := writeHelp(&bytes.Buffer{}, "not-a-command"); err == nil {
		t.Fatal("unknown help topic unexpectedly resolved")
	}
}

func TestRootHelpListsEveryCommand(t *testing.T) {
	out, err := executeHelp(t, []string{"--help"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bootstrap", "config show|set|unset", "state inventory|backup|restore", "suspend deploy", "unsuspend deploy", "quota set", "plumtree help COMMAND"} {
		if !strings.Contains(out, want) {
			t.Errorf("root help missing %q:\n%s", want, out)
		}
	}
}

func TestHelpEntryPoints(t *testing.T) {
	root, err := executeHelp(t, []string{"--help"})
	if err != nil || !strings.Contains(root, "plumtree \u2014") {
		t.Fatalf("--help = %q, %v", root, err)
	}
	for _, args := range [][]string{{"-h"}, {"help"}, {"help", "--help"}} {
		out, err := executeHelp(t, args)
		if err != nil || out != root {
			t.Errorf("%v = %q, %v, want root help", args, out, err)
		}
	}
	serve, err := executeHelp(t, []string{"help", "serve"})
	if err != nil || !strings.Contains(serve, "plumtree [serve]") {
		t.Fatalf("help serve = %q, %v", serve, err)
	}
	for _, args := range [][]string{{"serve", "--help"}, {"--config", "/tmp/plumtree.json", "--help"}} {
		out, err := executeHelp(t, args)
		if err != nil || out != serve {
			t.Errorf("%v = %q, %v, want serve help", args, out, err)
		}
	}
	bootstrap, err := executeHelp(t, []string{"help", "bootstrap"})
	if err != nil || !strings.Contains(bootstrap, "-handle HANDLE") {
		t.Fatalf("help bootstrap = %q, %v", bootstrap, err)
	}
	for _, args := range [][]string{{"help", "author"}, {"help", "author", "bootstrap"}, {"author", "bootstrap", "--help"}, {"author", "--help"}} {
		out, err := executeHelp(t, args)
		if err != nil || out != bootstrap {
			t.Errorf("%v = %q, %v, want bootstrap help", args, out, err)
		}
	}
}

func TestHelpRejectsUnknownCommands(t *testing.T) {
	if _, err := executeHelp(t, []string{"bogus"}); err == nil || !strings.Contains(err.Error(), `unknown plumtree command "bogus"`) {
		t.Fatalf("bogus command error = %v", err)
	}
	if _, err := executeHelp(t, []string{"help", "bogus"}); err == nil || !strings.Contains(err.Error(), `unknown plumtree help command "bogus"`) {
		t.Fatalf("help bogus error = %v", err)
	}
	if _, err := executeHelp(t, []string{"help", "serve", "extra"}); err == nil || !strings.Contains(err.Error(), "usage: plumtree help [command]") {
		t.Fatalf("help serve extra error = %v", err)
	}
}
