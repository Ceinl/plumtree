package execprotocol

import (
	"errors"

	"github.com/Ceinl/plumtree/sdk/cli"
)

const maxCommand = 64 * 1024

// ParseExecCommand converts an SSH exec payload to bounded guest arguments
// without invoking a shell. Quoting and escaping follow the clean CLI lexer.
func ParseExecCommand(command string) ([]string, error) {
	if len(command) == 0 {
		return nil, nil
	}
	if len(command) > maxCommand {
		return nil, errors.New("exec command exceeds size limit")
	}
	args, err := cli.Lex(command)
	if err != nil {
		return nil, err
	}
	return args, nil
}
