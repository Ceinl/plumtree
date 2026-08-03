package sqlite

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPerformanceCorpusProfilesAndGate(t *testing.T) {
	var statements atomic.Int64
	var blobReads atomic.Int64
	cfg, err := ProfileConfig(t.TempDir()+"/perf.db", nil, ProfilePR, func(event TraceEvent) {
		if event.Kind == "statement" {
			statements.Add(1)
		}
		if strings.Contains(strings.ToLower(event.Statement), "select") && strings.Contains(strings.ToLower(event.Statement), "wasm") {
			blobReads.Add(1)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err := OpenWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	measurement, err := RunCorpus(context.Background(), db, ProfilePR)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	settings, err := Settings(ProfilePR)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	gate := Evaluate(measurement, settings.Budget)
	raw, _ := json.Marshal(struct {
		Measurement Measurement `json:"measurement"`
		Gate        GateResult  `json:"gate"`
	}{measurement, gate})
	t.Logf("sqlite-performance %s", raw)
	if !gate.Passed {
		_ = db.Close()
		t.Fatalf("performance gate failed: %+v measurement=%+v", gate, measurement)
	}
	if measurement.Operations != settings.Operations || measurement.MetadataBlobRead || blobReads.Load() != 0 {
		_ = db.Close()
		t.Fatalf("corpus metadata/blob proof: %+v blobReads=%d", measurement, blobReads.Load())
	}
	if statements.Load() == 0 {
		_ = db.Close()
		t.Fatal("query tracer recorded no statements")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestScheduledPerformanceCorpus(t *testing.T) {
	if os.Getenv("PLUMTREE_SCHEDULED_STRESS") != "1" {
		t.Skip("scheduled stress profile runs in the scheduled qualification workflow")
	}
	settings, err := Settings(ProfileScheduled)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ProfileConfig(t.TempDir()+"/scheduled.db", nil, ProfileScheduled, nil)
	if err != nil {
		t.Fatal(err)
	}
	db, err := OpenWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	measurement, err := RunCorpus(context.Background(), db, ProfileScheduled)
	if err != nil {
		t.Fatal(err)
	}
	gate := Evaluate(measurement, settings.Budget)
	raw, _ := json.Marshal(struct {
		Measurement Measurement `json:"measurement"`
		Gate        GateResult  `json:"gate"`
	}{measurement, gate})
	t.Logf("sqlite-performance %s", raw)
	if !gate.Passed {
		t.Fatalf("scheduled performance gate failed: %+v", gate)
	}
}

func TestPerformanceProfilesAreDeterministicAndBounded(t *testing.T) {
	for _, profile := range []Profile{ProfileTiny, ProfilePR, ProfileScheduled} {
		settings, err := Settings(profile)
		if err != nil {
			t.Fatal(err)
		}
		if settings.Operations <= 0 || settings.MaxOpenConns <= 0 || settings.BusyTimeout <= 0 || settings.WALAutoCheckpointPages <= 0 {
			t.Fatalf("invalid settings for %s: %+v", profile, settings)
		}
		if settings.Budget.MaxElapsed <= 0 || settings.Budget.MaxHeapBytes <= 0 || settings.Budget.MaxRSSBytes <= 0 || settings.Budget.MaxCPUTime <= 0 || settings.Budget.MaxWALBytes <= 0 || settings.Budget.MaxDiskWriteBytes <= 0 {
			t.Fatalf("incomplete budget for %s: %+v", profile, settings.Budget)
		}
	}
	if _, err := Settings(Profile("unknown")); err == nil {
		t.Fatal("unknown profile accepted")
	}
	if got := DefaultBusyTimeout; got != 5*time.Second {
		t.Fatalf("default busy timeout = %s", got)
	}
}
