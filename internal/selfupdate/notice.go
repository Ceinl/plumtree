package selfupdate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ceinl/plumtree/internal/fsatomic"
)

// The passive update notice reads a cached daily check result; the cache
// path is overridable via PLUMTREE_UPDATE_CACHE_PATH (tests, CI) and the
// release endpoint via PLUMTREE_UPDATE_API_BASE (tests, air-gapped use).
// The opt-out is PLUMTREE_DISABLE_UPDATE_NOTICE.
const (
	CachePathEnv = "PLUMTREE_UPDATE_CACHE_PATH"
	APIBaseEnv   = "PLUMTREE_UPDATE_API_BASE"
	OptOutEnv    = "PLUMTREE_DISABLE_UPDATE_NOTICE"
)

// noticeRecord is one passive-update-notice cache entry: the check
// timestamp plus the latest stable tag the check saw.
type noticeRecord struct {
	Checked string // RFC3339
	Stable  string
}

// NoticeCachePath resolves the per-user cache file.
func NoticeCachePath(userCacheDir func() (string, error)) string {
	if path := strings.TrimSpace(os.Getenv(CachePathEnv)); path != "" {
		return path
	}
	dir, err := userCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "plumtree", "update-check.json")
}

// readNotice loads the cached check, tolerating absence and corruption.
func readNotice(path string) noticeRecord {
	record := noticeRecord{}
	data, err := os.ReadFile(path)
	if err != nil {
		return record
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return noticeRecord{}
	}
	return record
}

func writeNotice(path string, record noticeRecord) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(record)
	if err != nil {
		return
	}
	_ = fsatomic.WriteFileAtomic(path, data, 0o644)
}

const noticeStalenessLimit = 24 * time.Hour

// PassiveHint returns the one-line hint when a cached check (refreshed at
// most once per day) found a newer stable release. Silent without a usable
// cache; opt-out and non-release builds produce no hint.
func PassiveHint(userCacheDir func() (string, error), current string, now time.Time) string {
	if os.Getenv(OptOutEnv) != "" || !IsRelease(current) {
		return ""
	}
	record := readNotice(NoticeCachePath(userCacheDir))
	checkedAt, err := time.Parse(time.RFC3339, record.Checked)
	if err != nil || now.Sub(checkedAt) > noticeStalenessLimit {
		return ""
	}
	if CompareVersions(current, record.Stable) >= 0 {
		return ""
	}
	return "a newer stable release is available: " + record.Stable + " (run update to install it)"
}

// RefreshNoticeCache re-resolves the latest release opportunistically when
// the cache is older than one day; the refresh is abandoned after a short
// budget, so the user command never waits for the network. A failing or
// rate-limited check stays silent.
func RefreshNoticeCache(userCacheDir func() (string, error), current string, now time.Time) {
	path, stale := staleCheck(userCacheDir, current, now, noticeStalenessLimit)
	if path == "" || !stale {
		return
	}
	go refresh(path)
}

// RefreshNoticeCacheSync is RefreshNoticeCache with the network fetch inline,
// still bounded by the refresh budget. Short-lived callers (a bare `pt`) use
// it so the process does not die before the goroutine lands.
func RefreshNoticeCacheSync(userCacheDir func() (string, error), current string) {
	path, _ := staleCheck(userCacheDir, current, time.Now(), noticeStalenessLimit)
	if path == "" {
		return
	}
	refresh(path)
}

// staleCheck resolves the cache path and reports whether the daily cache is
// stale; opt-outs and non-release builds suppress the refresh entirely.
func staleCheck(userCacheDir func() (string, error), current string, now time.Time, limit time.Duration) (string, bool) {
	if os.Getenv(OptOutEnv) != "" || !IsRelease(current) {
		return "", false
	}
	path := NoticeCachePath(userCacheDir)
	if path == "" {
		return "", false
	}
	if record := readNotice(path); record.Checked != "" {
		if checkedAt, err := time.Parse(time.RFC3339, record.Checked); err == nil && now.Sub(checkedAt) < limit {
			return path, false
		}
	}
	return path, true
}

func refresh(path string) {
	ctx, cancel := context.WithTimeout(context.Background(), noticeRefreshTimeout)
	defer cancel()
	source := NewGitHubSource(DefaultRepo)
	if base := strings.TrimSpace(os.Getenv(APIBaseEnv)); base != "" {
		source.APIBase = base
	}
	release, err := source.Latest(ctx)
	if err != nil || release.Tag == "" {
		return
	}
	writeNotice(path, noticeRecord{Checked: time.Now().UTC().Format(time.RFC3339), Stable: release.Tag})
}

const noticeRefreshTimeout = 2 * time.Second
