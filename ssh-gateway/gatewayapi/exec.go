package gatewayapi

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/Ceinl/plumtree/sdk/abi"
)

var actionNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// ParseExecCommand converts an SSH exec payload to guest arguments without
// invoking a shell. The action form preserves everything after the name as one
// JSON argument; ordinary CLI commands use bounded whitespace-separated args.
func ParseExecCommand(command string) ([]string, error) {
	if len(command) == 0 {
		return nil, nil
	}
	if len(command) > abi.ActionMaxCommand {
		return nil, errors.New("exec command exceeds size limit")
	}
	if command == "action" || strings.HasPrefix(command, "action ") || strings.HasPrefix(command, "action\t") {
		rest := strings.TrimLeft(command[len("action"):], " \t")
		name, raw, ok := strings.Cut(rest, " ")
		if !ok {
			name, raw, ok = strings.Cut(rest, "\t")
		}
		raw = strings.TrimLeft(raw, " \t")
		if !ok || !actionNameRE.MatchString(name) || len(name) > abi.ActionMaxName || len(raw) == 0 || len(raw) > abi.ActionMaxJSON || !json.Valid([]byte(raw)) {
			return nil, errors.New("invalid action name or JSON arguments")
		}
		return []string{abi.ActionArgPrefix, name, raw}, nil
	}
	args := strings.Fields(command)
	if len(args) > 64 {
		return nil, errors.New("too many CLI arguments")
	}
	for _, arg := range args {
		if len(arg) > 4096 {
			return nil, errors.New("CLI argument exceeds size limit")
		}
	}
	return args, nil
}
