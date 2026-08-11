package doctorchecks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/pkg/workspace"
)

func TestUndeclaredDiscoveredEcosystemsUsesFilesystemIdentity(t *testing.T) {
	parent := t.TempDir()
	actual := filepath.Join(parent, "Code", "grovetools")
	if err := os.MkdirAll(actual, 0o755); err != nil {
		t.Fatal(err)
	}

	// On a case-insensitive filesystem this is a different spelling of the
	// same directory. Keep the fixture self-contained and skip only where the
	// filesystem cannot represent the macOS condition under test.
	recorded := filepath.Join(parent, "code", "grovetools")
	if _, err := os.Stat(recorded); err != nil {
		t.Skip("filesystem is case-sensitive; case-folded inode identity is not available")
	}
	if canonical(recorded) == canonical(actual) {
		// EvalSymlinks may itself normalize case on some platforms. The outcome
		// is still correct, but force proof that both spellings are one inode.
		recordedInfo, err := os.Stat(recorded)
		if err != nil {
			t.Fatal(err)
		}
		actualInfo, err := os.Stat(actual)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(recordedInfo, actualInfo) {
			t.Fatal("case variants unexpectedly identify different directories")
		}
	}

	got := undeclaredDiscoveredEcosystems(
		[]string{recorded},
		[]workspace.Ecosystem{{Name: "grovetools", Path: actual}},
	)
	if len(got) != 0 {
		t.Fatalf("same-inode case variant reported undeclared: %v", got)
	}
}

func TestUndeclaredDiscoveredEcosystemsStillReportsDifferentDirectory(t *testing.T) {
	parent := t.TempDir()
	declared := filepath.Join(parent, "declared")
	discovered := filepath.Join(parent, "discovered")
	for _, path := range []string{declared, discovered} {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	got := undeclaredDiscoveredEcosystems(
		[]string{declared},
		[]workspace.Ecosystem{{Name: "other", Path: discovered}},
	)
	if len(got) != 1 || !strings.Contains(got[0], discovered) {
		t.Fatalf("different inode was not reported undeclared: %v", got)
	}
}
