package main

import "testing"

func TestSSHConfigInstallationIsOptIn(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		disabled bool
		want     bool
	}{
		{name: "default empty host", want: false},
		{name: "whitespace host", host: "  ", want: false},
		{name: "explicit alias", host: "plumtree-local", want: true},
		{name: "explicitly disabled", host: "plumtree-local", disabled: true, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldInstallDevSSHConfig(test.host, test.disabled); got != test.want {
				t.Fatalf("shouldInstallDevSSHConfig(%q, %t) = %t, want %t", test.host, test.disabled, got, test.want)
			}
		})
	}
}
