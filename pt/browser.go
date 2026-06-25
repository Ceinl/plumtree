package main

import (
	"errors"
	"os/exec"
	"runtime"
)

func openURLInBrowser(link string) error {
	name, args := browserOpenInvocation(link)
	if name == "" {
		return errors.New("no browser opener for this platform")
	}
	return exec.Command(name, args...).Start()
}

func browserOpenInvocation(link string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{link}
	case "windows":
		return "cmd", []string{"/c", "start", "", link}
	default:
		return "xdg-open", []string{link}
	}
}
