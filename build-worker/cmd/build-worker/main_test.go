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

func TestValidateProductionLimits(t *testing.T) {
	if err := validateProductionLimits(true, false, time.Minute, 8<<20, 2<<30, 2); err != nil {
		t.Fatalf("bounded config refused: %v", err)
	}
	err := validateProductionLimits(true, false, time.Minute, 8<<20, -1, 2)
	if err == nil || !strings.Contains(err.Error(), "max-memory-bytes") {
		t.Fatalf("error = %v, want memory-limit refusal", err)
	}
	if err := validateProductionLimits(true, true, 0, 0, -1, 0); err != nil {
		t.Fatalf("acknowledged config refused: %v", err)
	}
}
