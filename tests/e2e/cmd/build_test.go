package cmd_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCommand(t *testing.T) {
	// Build the grove binary if needed
	groveBin := filepath.Join(repoRoot(), "bin", "grove")
	if _, err := os.Stat(groveBin); os.IsNotExist(err) {
		t.Skip("Grove binary not found. Run 'make build' first.")
	}

	t.Run("DryRun", func(t *testing.T) {
		cmd := exec.Command(groveBin, "build", "--dry-run")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "dry-run should not fail")

		outputStr := string(output)
		assert.Contains(t, outputStr, "Projects that would be built:")
		assert.Contains(t, outputStr, "Total:")
	})

	t.Run("VerboseMode", func(t *testing.T) {
		// Test with a filter to make it faster
		cmd := exec.Command(groveBin, "build", "--verbose", "--filter", "grove-meta")
		output, err := cmd.CombinedOutput()

		outputStr := string(output)
		if err != nil {
			t.Logf("Build output:\n%s", outputStr)
		}

		// Check for verbose mode indicators
		assert.Contains(t, outputStr, "Starting parallel build")
		assert.Contains(t, outputStr, "[1/1]") // Progress indicator
		assert.Contains(t, outputStr, "Building grove-meta...")
		assert.Contains(t, outputStr, "Build finished")
	})

	t.Run("FilterPattern", func(t *testing.T) {
		cmd := exec.Command(groveBin, "build", "--dry-run", "--filter", "grove-core")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err)

		outputStr := string(output)
		assert.Contains(t, outputStr, "grove-core")
		assert.NotContains(t, outputStr, "grove-meta")
		assert.NotContains(t, outputStr, "grove-proxy")
	})

	t.Run("ExcludePattern", func(t *testing.T) {
		cmd := exec.Command(groveBin, "build", "--dry-run", "--exclude", "grove-core,grove-proxy")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err)

		outputStr := string(output)
		assert.NotContains(t, outputStr, "grove-core")
		assert.NotContains(t, outputStr, "grove-proxy")
		// Should still have other projects
		assert.Contains(t, outputStr, "grove-")
	})

	t.Run("ParallelExecution", func(t *testing.T) {
		// Test that --jobs flag is respected
		cmd := exec.Command(groveBin, "build", "--verbose", "--jobs", "2", "--filter", "grove-meta")
		output, err := cmd.CombinedOutput()

		outputStr := string(output)
		if err != nil {
			t.Logf("Build output:\n%s", outputStr)
		}

		assert.Contains(t, outputStr, "using 2 workers")
	})

	t.Run("ContinueOnError", func(t *testing.T) {
		// Create a temporary project with a failing Makefile
		tempDir := t.TempDir()
		failingProject := filepath.Join(tempDir, "test-fail")
		require.NoError(t, os.MkdirAll(failingProject, 0755))

		// Create a Makefile that fails
		makefile := filepath.Join(failingProject, "Makefile")
		require.NoError(t, os.WriteFile(makefile, []byte(`
build:
	@echo "This build will fail"
	@exit 1
`), 0644))

		// This test would need modification to the discovery logic
		// to include the temp directory, so we'll skip the actual execution
		t.Skip("Requires modification to discovery logic for testing")
	})

	t.Run("EmptyFilter", func(t *testing.T) {
		cmd := exec.Command(groveBin, "build", "--filter", "nonexistent-project")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err)

		outputStr := string(output)
		assert.Contains(t, outputStr, "No projects to build after filtering")
	})

	t.Run("HelpFlag", func(t *testing.T) {
		cmd := exec.Command(groveBin, "build", "--help")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err)

		outputStr := string(output)
		assert.Contains(t, outputStr, "Builds all discovered Grove packages in parallel")
		assert.Contains(t, outputStr, "--verbose")
		assert.Contains(t, outputStr, "--jobs")
		assert.Contains(t, outputStr, "--filter")
		assert.Contains(t, outputStr, "--exclude")
		assert.Contains(t, outputStr, "--fail-fast")
		assert.Contains(t, outputStr, "--dry-run")
	})
}

func TestBuildCommandTUI(t *testing.T) {
	// TUI mode is harder to test in a non-interactive environment
	// This test ensures the TUI doesn't panic
	t.Run("TUIDoesNotPanic", func(t *testing.T) {
		groveBin := filepath.Join(repoRoot(), "bin", "grove")
		if _, err := os.Stat(groveBin); os.IsNotExist(err) {
			t.Skip("Grove binary not found. Run 'make build' first.")
		}

		// Create a context with timeout to kill the TUI after a short time
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		cmd := exec.CommandContext(ctx, groveBin, "build", "--filter", "nonexistent")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		// We expect it to either succeed (no projects) or be killed by context
		if err != nil && !strings.Contains(err.Error(), "killed") && !strings.Contains(err.Error(), "signal") {
			t.Logf("stdout: %s", stdout.String())
			t.Logf("stderr: %s", stderr.String())
			t.Fatalf("Unexpected error: %v", err)
		}
	})
}

func TestBuildRunner(t *testing.T) {
	// These would be unit tests for the build runner package
	// They would go in pkg/build/runner_test.go
	t.Skip("Unit tests for build runner should be in pkg/build/runner_test.go")
}

func repoRoot() string {
	// Find the repository root by looking for go.mod
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// TestSummary provides a summary of what was tested
func TestSummary(t *testing.T) {
	fmt.Print(`Build Command E2E Tests Summary:
- Dry-run mode functionality
- Verbose mode with progress indicators
- Filter patterns for project selection
- Exclude patterns for project exclusion
- Parallel execution with job control
- Empty filter handling
- Help flag output
- TUI mode basic stability
`)
}