package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHTTPServerTimeoutsAreBounded(t *testing.T) {
	srv := newHTTPServer(":0", http.NotFoundHandler())
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.WriteTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Fatalf("HTTP server has an unbounded timeout: %+v", srv)
	}
	if httpShutdownTimeout <= 0 {
		t.Fatalf("shutdown timeout = %s, want positive", httpShutdownTimeout)
	}
}

func TestValidateProductionLimitsRefusesUnlimited(t *testing.T) {
	limits := productionLimits{
		maxSessions: 64, maxSessionsPerAppDay: 50, maxDeploysPerHour: 100,
		maxAppsPerOwner: 5, maxConcurrentBuilds: 2, rateLimit: 20,
		maxConnections: 1024, maxConnectionsPerIP: 32,
		sshHandshakeTimeout: 10 * time.Second, sshIdleTimeout: 5 * time.Minute,
		runnerSessionTimeout: 30 * time.Minute,
	}
	limits.maxDeploysPerHour = 0
	err := validateProductionLimits(true, false, false, limits)
	if err == nil || !strings.Contains(err.Error(), "max-deploys-per-hour") {
		t.Fatalf("error = %v, want deploy limit refusal", err)
	}
	if err := validateProductionLimits(true, true, false, limits); err != nil {
		t.Fatalf("explicit acknowledgement should allow startup: %v", err)
	}
}

func TestValidateProductionLimitsChecksEmbeddedSSHOnlyWhenEnabled(t *testing.T) {
	limits := productionLimits{
		maxSessionsPerAppDay: 50, maxDeploysPerHour: 100, maxAppsPerOwner: 5,
		maxConcurrentBuilds: 2, rateLimit: 20,
	}
	if err := validateProductionLimits(true, false, false, limits); err != nil {
		t.Fatalf("disabled embedded SSH should not require SSH limits: %v", err)
	}
	if err := validateProductionLimits(true, false, true, limits); err == nil {
		t.Fatal("enabled embedded SSH should require a remote runner")
	}
	limits.runnerEndpoint = "unix:///run/plumtree/runner.sock"
	limits.runnerToken = "secret"
	if err := validateProductionLimits(true, false, true, limits); err == nil || !strings.Contains(err.Error(), "max-sessions") {
		t.Fatalf("error = %v, want session limit after runner validation", err)
	}
}
