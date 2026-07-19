package main

import (
	"strings"
	"testing"
	"time"
)

func TestValidateProductionLimits(t *testing.T) {
	cfg := config{production: true, maxSessions: 64, maxConnections: 1024,
		maxConnectionsPerIP: 32, handshakeTimeout: 10 * time.Second, idleTimeout: time.Minute,
		runnerEndpoint: "unix:///run/plumtree/runner.sock", runnerToken: "secret"}
	if err := validateProductionLimits(cfg); err != nil {
		t.Fatalf("bounded config refused: %v", err)
	}
	cfg.maxSessions = 0
	if err := validateProductionLimits(cfg); err == nil || !strings.Contains(err.Error(), "max-sessions") {
		t.Fatalf("error = %v, want session-limit refusal", err)
	}
	cfg.ackUnlimited = true
	if err := validateProductionLimits(cfg); err != nil {
		t.Fatalf("acknowledged config refused: %v", err)
	}
}

func TestProductionRequiresRemoteRunnerBoundary(t *testing.T) {
	cfg := config{production: true, maxSessions: 64, maxConnections: 1024,
		maxConnectionsPerIP: 32, handshakeTimeout: 10 * time.Second, idleTimeout: time.Minute,
		runnerWorker: "/usr/local/bin/plumtree-runner-worker"}
	if err := validateProductionLimits(cfg); err == nil || !strings.Contains(err.Error(), "remote runner-endpoint") {
		t.Fatalf("error = %v, want remote runner refusal", err)
	}
	cfg.runnerWorker = ""
	cfg.runnerEndpoint = "unix:///run/plumtree/runner.sock"
	if err := validateProductionLimits(cfg); err == nil || !strings.Contains(err.Error(), "runner-token") {
		t.Fatalf("error = %v, want missing token refusal", err)
	}
}
