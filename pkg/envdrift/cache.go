package envdrift

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// driftCacheBasename is the filename written under a worktree's state
// directory (usually .grove/env) to cache the most recent drift result.
const driftCacheBasename = "drift.json"

// defaultCacheTTL is how long a cached drift summary is considered
// fresh before `grove env drift` will re-run Terraform. Callers can
// override with IsStaleWithTTL.
const defaultCacheTTL = 24 * time.Hour

// DriftCache is the on-disk shape of <stateDir>/drift.json. We wrap
// DriftSummary in a struct with CheckedAt so callers can decide whether
// the cached result is still fresh.
type DriftCache struct {
	Summary   *DriftSummary `json:"summary"`
	CheckedAt time.Time     `json:"checked_at"`
}

// LoadCache reads the cached drift summary for the given state directory.
// Returns (nil, zero-time, nil) if the file is missing so callers can
// treat "no cache" and "fresh start" as the same case. Corrupt JSON is
// returned as an error.
func LoadCache(stateDir string) (*DriftSummary, time.Time, error) {
	path := cachePath(stateDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, time.Time{}, nil
		}
		return nil, time.Time{}, fmt.Errorf("failed to read drift cache: %w", err)
	}
	var cache DriftCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to parse drift cache: %w", err)
	}
	return cache.Summary, cache.CheckedAt, nil
}

// SaveCache writes the summary to <stateDir>/drift.json, stamping
// CheckedAt with time.Now(). Creates the state directory if it does not
// yet exist.
func SaveCache(stateDir string, summary *DriftSummary) error {
	if summary == nil {
		return errors.New("drift cache: refusing to persist nil summary")
	}
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}
	cache := DriftCache{Summary: summary, CheckedAt: time.Now().UTC()}
	data, err := json.MarshalIndent(&cache, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal drift cache: %w", err)
	}
	if err := os.WriteFile(cachePath(stateDir), data, 0644); err != nil {
		return fmt.Errorf("failed to write drift cache: %w", err)
	}
	return nil
}

// IsStale returns true when the cached timestamp is older than the
// default 24h TTL. A zero-value time (missing cache) is always stale.
func IsStale(checkedAt time.Time) bool {
	return IsStaleWithTTL(checkedAt, defaultCacheTTL)
}

// IsStaleWithTTL is a test-friendly variant of IsStale.
func IsStaleWithTTL(checkedAt time.Time, ttl time.Duration) bool {
	if checkedAt.IsZero() {
		return true
	}
	return time.Since(checkedAt) > ttl
}

func cachePath(stateDir string) string {
	return filepath.Join(stateDir, driftCacheBasename)
}
