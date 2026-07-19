package main

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
)

func openURLInBrowser(link string) error {
	if err := validateBrowserURL(link); err != nil {
		return err
	}
	name, args := browserOpenInvocation(link)
	if name == "" {
		return errors.New("no browser opener for this platform")
	}
	return exec.Command(name, args...).Start()
}

func validateBrowserURL(link string) error {
	if len(link) == 0 || len(link) > 4096 {
		return errors.New("browser URL has invalid length")
	}
	u, err := url.ParseRequestURI(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return fmt.Errorf("refusing to open invalid HTTP(S) URL")
	}
	return nil
}

func browserOpenInvocation(link string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{"--", link}
	case "windows":
		// Avoid cmd.exe /c start: cmd interprets metacharacters in a URL and can
		// turn a malicious control-plane response into local command execution.
		return "rundll32.exe", []string{"url.dll,FileProtocolHandler", link}
	default:
		return "xdg-open", []string{link}
	}
}
