package config

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
)

// RunConfigCommand implements the additive config show/set/unset surface. The
// caller supplies the path explicitly, so it never consults global flags or
// process environment implicitly.
func RunConfigCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: config show|set|unset")
	}
	switch args[0] {
	case "show":
		return configShow(args[1:], out)
	case "set":
		return configSet(args[1:])
	case "unset":
		return configUnset(args[1:])
	default:
		return fmt.Errorf("%w: unknown config command %q", ErrInvalid, args[0])
	}
}
func configShow(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("config show", flag.ContinueOnError)
	var path string
	fs.StringVar(&path, "path", "", "typed config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("%w: --path is required", ErrInvalid)
	}
	c, err := Read(path)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(c.Redacted(), "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(b))
	return err
}
func configSet(args []string) error {
	fs := flag.NewFlagSet("config set", flag.ContinueOnError)
	var path, field, value string
	fs.StringVar(&path, "path", "", "typed config file path")
	fs.StringVar(&field, "field", "", "setting name")
	fs.StringVar(&value, "value", "", "setting value")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if path == "" || field == "" {
		return fmt.Errorf("%w: --path, --field, and --value are required", ErrInvalid)
	}
	return Update(path, func(c *Config) error {
		next, err := c.Set(field, value)
		if err == nil {
			*c = next
		}
		return err
	})
}
func configUnset(args []string) error {
	fs := flag.NewFlagSet("config unset", flag.ContinueOnError)
	var path, field string
	fs.StringVar(&path, "path", "", "typed config file path")
	fs.StringVar(&field, "field", "", "setting name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if path == "" || field == "" {
		return fmt.Errorf("%w: --path and --field are required", ErrInvalid)
	}
	return Update(path, func(c *Config) error {
		next, err := c.Unset(field)
		if err == nil {
			*c = next
		}
		return err
	})
}
