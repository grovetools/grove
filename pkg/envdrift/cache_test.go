package envdrift

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAndLoadCache_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	summary := &DriftSummary{
		Profile:  "terraform-infra",
		Provider: "terraform",
		HasDrift: true,
		Add:      2,
		Change:   1,
		Destroy:  0,
		Resources: []DriftResource{
			{Address: "google_storage_bucket.state", Action: "create"},
			{Address: "google_project_iam.binding", Action: "update"},
		},
	}

	if err := SaveCache(dir, summary); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "drift.json")); err != nil {
		t.Fatalf("expected drift.json to exist: %v", err)
	}

	got, checkedAt, err := LoadCache(dir)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil summary")
	}
	if got.Profile != summary.Profile || got.Add != 2 || got.Change != 1 || got.Destroy != 0 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if len(got.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(got.Resources))
	}
	if got.Resources[0].Address != "google_storage_bucket.state" {
		t.Errorf("unexpected first resource: %+v", got.Resources[0])
	}
	if time.Since(checkedAt) > time.Minute {
		t.Errorf("checkedAt should be recent, got %v", checkedAt)
	}
}

func TestLoadCache_MissingFile(t *testing.T) {
	dir := t.TempDir()
	summary, checkedAt, err := LoadCache(dir)
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if summary != nil {
		t.Errorf("expected nil summary for missing file, got %+v", summary)
	}
	if !checkedAt.IsZero() {
		t.Errorf("expected zero time, got %v", checkedAt)
	}
}

func TestLoadCache_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "drift.json"), []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadCache(dir)
	if err == nil {
		t.Fatal("expected error for corrupt JSON")
	}
}

func TestSaveCache_RejectsNilSummary(t *testing.T) {
	dir := t.TempDir()
	if err := SaveCache(dir, nil); err == nil {
		t.Fatal("expected error when persisting nil summary")
	}
}

func TestSaveCache_CreatesStateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "env")
	summary := &DriftSummary{Profile: "p", Provider: "terraform"}
	if err := SaveCache(dir, summary); err != nil {
		t.Fatalf("SaveCache should create nested dirs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "drift.json")); err != nil {
		t.Errorf("drift.json not created: %v", err)
	}
}

func TestIsStale_ZeroTimeIsStale(t *testing.T) {
	if !IsStale(time.Time{}) {
		t.Error("zero time should be stale")
	}
}

func TestIsStale_WithinTTLIsFresh(t *testing.T) {
	if IsStale(time.Now().Add(-1 * time.Hour)) {
		t.Error("1h-old cache should be fresh under 24h TTL")
	}
}

func TestIsStale_PastTTLIsStale(t *testing.T) {
	if !IsStale(time.Now().Add(-25 * time.Hour)) {
		t.Error("25h-old cache should be stale under 24h TTL")
	}
}

func TestIsStaleWithTTL_BoundaryCases(t *testing.T) {
	ttl := time.Hour
	if IsStaleWithTTL(time.Now().Add(-30*time.Minute), ttl) {
		t.Error("30m-old cache should be fresh under 1h TTL")
	}
	if !IsStaleWithTTL(time.Now().Add(-90*time.Minute), ttl) {
		t.Error("90m-old cache should be stale under 1h TTL")
	}
}
