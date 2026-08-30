package sshdev

import (
	"net"
	"strings"
	"testing"
)

func TestCheckListenAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		allow   bool
		wantErr bool // refused addresses must name the override flag
	}{
		{name: "loopback ipv4", address: "127.0.0.1:2222"},
		{name: "loopback ipv6", address: "[::1]:2222"},
		{name: "localhost", address: "localhost:2222"},
		{name: "wildcard ipv4", address: "0.0.0.0:2222", wantErr: true},
		{name: "empty host", address: ":2222", wantErr: true},
		{name: "wildcard ipv6", address: "[::]:2222", wantErr: true},
		{name: "lan address", address: "192.168.1.10:2222", wantErr: true},
		{name: "override permits anything", address: "0.0.0.0:2222", allow: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := CheckListenAddress(test.address, test.allow)
			if test.wantErr {
				if err == nil {
					t.Fatalf("address %q was accepted", test.address)
				}
				if !strings.Contains(err.Error(), "--allow-nonloopback-ssh") {
					t.Fatalf("refusal must name the override flag, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("address %q with allow=%v: %v", test.address, test.allow, err)
			}
		})
	}
}

// A hostname is allowed only when it resolves exclusively to loopback.
func TestCheckListenAddressRejectsNonLoopbackHostname(t *testing.T) {
	ips, err := net.LookupHost("example.com")
	if err != nil || len(ips) == 0 || net.ParseIP(ips[0]) == nil || net.ParseIP(ips[0]).IsLoopback() {
		t.Skip("environment does not resolve example.com to a non-loopback address")
	}
	err = CheckListenAddress("example.com:2222", false)
	if err == nil || !strings.Contains(err.Error(), "--allow-nonloopback-ssh") {
		t.Fatalf("non-loopback hostname error = %v", err)
	}
}
