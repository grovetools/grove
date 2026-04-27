package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDashboardPortFilePath returns a path inside the user's state dir.
func TestDashboardPortFilePath(t *testing.T) {
	p := dashboardPortFilePath()
	if p == "" {
		t.Fatal("path is empty")
	}
	if filepath.Base(p) != "dashboard.port" {
		t.Errorf("basename = %q", filepath.Base(p))
	}
}

// TestReadPortFile happy + error paths. The happy path uses a temp-home
// override via HOME; the error path asserts that a missing file returns a
// non-nil error.
func TestReadPortFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if _, err := readPortFile(); err == nil {
		t.Error("expected error when port file missing")
	}

	dir := filepath.Join(tmp, ".local", "state", "grove")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dashboard.port"), []byte("54321\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	port, err := readPortFile()
	if err != nil {
		t.Fatal(err)
	}
	if port != 54321 {
		t.Errorf("port = %d, want 54321", port)
	}
}
