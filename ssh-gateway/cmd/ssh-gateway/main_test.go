package main

import (
	"strings"
	"testing"
	"time"
)

func TestValidateProductionLimits(t *testing.T) {
	cfg := config{production: true, maxSessions: 64, maxConnections: 1024,
		maxConnectionsPerIP: 32, handshakeTimeout: 10 * time.Second, idleTimeout: time.Minute}
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
