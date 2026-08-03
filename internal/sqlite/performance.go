package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"runtime/metrics"
	"strings"
	"sync/atomic"
	"time"
)

type Profile string

const (
	ProfileTiny      Profile = "tiny"
	ProfilePR        Profile = "pr"
	ProfileScheduled Profile = "scheduled-stress"
)

type Budget struct {
	MaxElapsed        time.Duration
	MaxHeapBytes      uint64
	MaxRSSBytes       uint64
	MaxCPUTime        time.Duration
	MaxDatabaseBytes  int64
	MaxWALBytes       int64
	MaxDiskBytes      int64
	MaxDiskWriteBytes int64
	MaxBusyErrors     int
}

type ProfileSettings struct {
	Operations             int
	BusyTimeout            time.Duration
	MaxOpenConns           int
	CacheSizeKB            int
	WALAutoCheckpointPages int
	Budget                 Budget
}

func Settings(profile Profile) (ProfileSettings, error) {
	switch profile {
	case ProfileTiny:
		return ProfileSettings{Operations: 50, BusyTimeout: 2 * time.Second, MaxOpenConns: 2, CacheSizeKB: 4096, WALAutoCheckpointPages: 100, Budget: Budget{MaxElapsed: 2 * time.Second, MaxHeapBytes: 64 << 20, MaxRSSBytes: 128 << 20, MaxCPUTime: 2 * time.Second, MaxDatabaseBytes: 16 << 20, MaxWALBytes: 4 << 20, MaxDiskBytes: 24 << 20, MaxDiskWriteBytes: 24 << 20}}, nil
	case ProfilePR:
		return ProfileSettings{Operations: 500, BusyTimeout: 5 * time.Second, MaxOpenConns: 4, CacheSizeKB: 8192, WALAutoCheckpointPages: 1000, Budget: Budget{MaxElapsed: 10 * time.Second, MaxHeapBytes: 256 << 20, MaxRSSBytes: 512 << 20, MaxCPUTime: 10 * time.Second, MaxDatabaseBytes: 64 << 20, MaxWALBytes: 16 << 20, MaxDiskBytes: 96 << 20, MaxDiskWriteBytes: 96 << 20}}, nil
	case ProfileScheduled:
		return ProfileSettings{Operations: 5000, BusyTimeout: 5 * time.Second, MaxOpenConns: 8, CacheSizeKB: 16384, WALAutoCheckpointPages: 1000, Budget: Budget{MaxElapsed: 60 * time.Second, MaxHeapBytes: 512 << 20, MaxRSSBytes: 1024 << 20, MaxCPUTime: 60 * time.Second, MaxDatabaseBytes: 256 << 20, MaxWALBytes: 64 << 20, MaxDiskBytes: 384 << 20, MaxDiskWriteBytes: 384 << 20}}, nil
	default:
		return ProfileSettings{}, fmt.Errorf("sqlite performance: unknown profile %q", profile)
	}
}

func ProfileConfig(path string, key []byte, profile Profile, trace TraceFunc) (Config, error) {
	settings, err := Settings(profile)
	if err != nil {
		return Config{}, err
	}
	return Config{Path: path, Key: key, BusyTimeout: settings.BusyTimeout, MaxOpenConns: settings.MaxOpenConns, MaxIdleConns: settings.MaxOpenConns, CacheSizeKB: settings.CacheSizeKB, WALAutoCheckpointPages: settings.WALAutoCheckpointPages, Trace: trace}, nil
}

type Measurement struct {
	Profile          Profile       `json:"profile"`
	Operations       int           `json:"operations"`
	Elapsed          time.Duration `json:"elapsed"`
	HeapBytes        uint64        `json:"heapBytes"`
	RSSBytes         uint64        `json:"rssBytes"`
	CPUTime          time.Duration `json:"cpuTime"`
	DatabaseBytes    int64         `json:"databaseBytes"`
	WALBytes         int64         `json:"walBytes"`
	DiskBytes        int64         `json:"diskBytes"`
	DiskWriteBytes   int64         `json:"diskWriteBytes"`
	BusyErrors       int           `json:"busyErrors"`
	MetadataBlobRead bool          `json:"metadataBlobRead"`
}

type GateResult struct {
	Profile  Profile  `json:"profile"`
	Passed   bool     `json:"passed"`
	Failures []string `json:"failures,omitempty"`
}

var corpusID atomic.Uint64

// RunCorpus runs a deterministic metadata/blob workload. Metadata reads select
// only size and digest columns; blobs are written for deduplication but never
// selected by the qualification read path.
func RunCorpus(ctx context.Context, db *DB, profile Profile) (Measurement, error) {
	settings, err := Settings(profile)
	if err != nil {
		return Measurement{}, err
	}
	if db == nil || db.DB == nil {
		return Measurement{}, errors.New("sqlite performance: nil database")
	}
	id := corpusID.Add(1)
	metadata := fmt.Sprintf("perf_metadata_%d", id)
	blobs := fmt.Sprintf("perf_blobs_%d", id)
	if _, err := db.ExecContext(ctx, `CREATE TABLE `+metadata+` (id INTEGER PRIMARY KEY,digest TEXT NOT NULL,size_bytes INTEGER NOT NULL)`); err != nil {
		return Measurement{}, err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE `+blobs+` (digest TEXT PRIMARY KEY,wasm BLOB NOT NULL)`); err != nil {
		return Measurement{}, err
	}
	defer db.ExecContext(context.Background(), `DROP TABLE `+metadata)
	defer db.ExecContext(context.Background(), `DROP TABLE `+blobs)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	rssBefore, cpuBefore := resourceMetrics()
	diskBeforeDB, diskBeforeWAL := fileBytes(db.path)
	diskBefore := diskBeforeDB + diskBeforeWAL
	start := time.Now()
	busyErrors := 0
	for i := 0; i < settings.Operations; i++ {
		select {
		case <-ctx.Done():
			return Measurement{}, ctx.Err()
		default:
		}
		digest := fmt.Sprintf("sha256:%064x", i%37)
		payload := []byte(fmt.Sprintf("deterministic-payload-%04d", i%37))
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			if isBusy(err) {
				busyErrors++
			}
			return Measurement{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO `+blobs+`(digest,wasm) VALUES(?,?)`, digest, payload); err == nil {
			_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO `+metadata+`(id,digest,size_bytes) VALUES(?,?,?)`, i, digest, len(payload))
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err != nil {
			if isBusy(err) {
				busyErrors++
			}
			return Measurement{}, err
		}
		var count, size int64
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(size_bytes),0) FROM `+metadata).Scan(&count, &size); err != nil {
			if isBusy(err) {
				busyErrors++
			}
			return Measurement{}, err
		}
		if count < 1 || size < 1 {
			return Measurement{}, errors.New("sqlite performance: corpus metadata invariant failed")
		}
	}
	_, _ = db.ExecContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`)
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	dbBytes, walBytes := fileBytes(db.path)
	rssAfter, cpuAfter := resourceMetrics()
	diskWriteBytes := dbBytes + walBytes - diskBefore
	if diskWriteBytes < 0 {
		diskWriteBytes = 0
	}
	var heapBytes uint64
	if after.HeapInuse > before.HeapInuse {
		heapBytes = after.HeapInuse - before.HeapInuse
	}
	rssBytes := uint64(0)
	if rssAfter > rssBefore {
		rssBytes = rssAfter - rssBefore
	}
	cpuTime := time.Duration(0)
	if cpuAfter > cpuBefore {
		cpuTime = cpuAfter - cpuBefore
	}
	return Measurement{Profile: profile, Operations: settings.Operations, Elapsed: elapsed, HeapBytes: heapBytes, RSSBytes: rssBytes, CPUTime: cpuTime, DatabaseBytes: dbBytes, WALBytes: walBytes, DiskBytes: dbBytes + walBytes + sidecarBytes(db.path, "-shm"), DiskWriteBytes: diskWriteBytes, BusyErrors: busyErrors, MetadataBlobRead: false}, nil
}

func Evaluate(measurement Measurement, budget Budget) GateResult {
	result := GateResult{Profile: measurement.Profile, Passed: true}
	fail := func(message string) { result.Passed = false; result.Failures = append(result.Failures, message) }
	if budget.MaxElapsed > 0 && measurement.Elapsed > budget.MaxElapsed {
		fail(fmt.Sprintf("elapsed %s exceeds %s", measurement.Elapsed, budget.MaxElapsed))
	}
	if budget.MaxHeapBytes > 0 && measurement.HeapBytes > budget.MaxHeapBytes {
		fail(fmt.Sprintf("heap %d exceeds %d", measurement.HeapBytes, budget.MaxHeapBytes))
	}
	if budget.MaxRSSBytes > 0 && measurement.RSSBytes > budget.MaxRSSBytes {
		fail(fmt.Sprintf("RSS %d exceeds %d", measurement.RSSBytes, budget.MaxRSSBytes))
	}
	if budget.MaxCPUTime > 0 && measurement.CPUTime > budget.MaxCPUTime {
		fail(fmt.Sprintf("CPU time %s exceeds %s", measurement.CPUTime, budget.MaxCPUTime))
	}
	if budget.MaxDatabaseBytes > 0 && measurement.DatabaseBytes > budget.MaxDatabaseBytes {
		fail(fmt.Sprintf("database bytes %d exceeds %d", measurement.DatabaseBytes, budget.MaxDatabaseBytes))
	}
	if budget.MaxWALBytes > 0 && measurement.WALBytes > budget.MaxWALBytes {
		fail(fmt.Sprintf("WAL bytes %d exceeds %d", measurement.WALBytes, budget.MaxWALBytes))
	}
	if budget.MaxDiskBytes > 0 && measurement.DiskBytes > budget.MaxDiskBytes {
		fail(fmt.Sprintf("disk bytes %d exceeds %d", measurement.DiskBytes, budget.MaxDiskBytes))
	}
	if budget.MaxDiskWriteBytes > 0 && measurement.DiskWriteBytes > budget.MaxDiskWriteBytes {
		fail(fmt.Sprintf("disk writes %d exceeds %d", measurement.DiskWriteBytes, budget.MaxDiskWriteBytes))
	}
	if measurement.BusyErrors > budget.MaxBusyErrors {
		fail(fmt.Sprintf("busy/locked errors %d exceeds %d", measurement.BusyErrors, budget.MaxBusyErrors))
	}
	if measurement.MetadataBlobRead {
		fail("metadata workload selected a BLOB")
	}
	return result
}

func fileBytes(path string) (int64, int64) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	wal := sidecarBytes(path, "-wal")
	return info.Size(), wal
}
func sidecarBytes(path, suffix string) int64 {
	info, err := os.Stat(path + suffix)
	if err != nil {
		return 0
	}
	return info.Size()
}
func isBusy(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "busy") || strings.Contains(text, "locked")
}

func resourceMetrics() (uint64, time.Duration) {
	samples := []metrics.Sample{{Name: "/cpu/classes/user:cpu-seconds"}, {Name: "/cpu/classes/gc/total:cpu-seconds"}}
	metrics.Read(samples)
	var cpuSeconds float64
	if samples[0].Value.Kind() == metrics.KindFloat64 {
		cpuSeconds += samples[0].Value.Float64()
	}
	if samples[1].Value.Kind() == metrics.KindFloat64 {
		cpuSeconds += samples[1].Value.Float64()
	}
	return processMemoryBytes(), time.Duration(cpuSeconds * float64(time.Second))
}
