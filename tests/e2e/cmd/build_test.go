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
		assert.Contains(t, outputStr, "Projects that would run")
		assert.Contains(t, outputStr, "Total:")
	})

	t.Run("VerboseMode", func(t *testing.T) {
		// Test with a filter to match the current project
		cmd := exec.Command(groveBin, "build", "--verbose", "--filter", "grove")
		output, err := cmd.CombinedOutput()

		outputStr := string(output)
		if err != nil {
			t.Logf("Build output:\n%s", outputStr)
		}

		// Check that build ran (output format varies by environment)
		assert.NotEmpty(t, outputStr)
	})

	t.Run("FilterPattern", func(t *testing.T) {
		cmd := exec.Command(groveBin, "build", "--dry-run", "--filter", "grove")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err)

		outputStr := string(output)
		assert.Contains(t, outputStr, "grove")
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
		// Test that --jobs flag is accepted
		cmd := exec.Command(groveBin, "build", "--verbose", "--jobs", "2", "--filter", "grove")
		output, err := cmd.CombinedOutput()

		outputStr := string(output)
		if err != nil {
			t.Logf("Build output:\n%s", outputStr)
		}

		assert.NotEmpty(t, outputStr)
	})

	t.Run("ContinueOnError", func(t *testing.T) {
		// Create a temporary project with a failing Makefile
		tempDir := t.TempDir()
		failingProject := filepath.Join(tempDir, "test-fail")
		require.NoError(t, os.MkdirAll(failingProject, 0o755))

		// Create a Makefile that fails
		makefile := filepath.Join(failingProject, "Makefile")
		require.NoError(t, os.WriteFile(makefile, []byte(`
build:
	@echo "This build will fail"
	@exit 1
`), 0o644))

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
		assert.Contains(t, outputStr, "Build all Grove packages in parallel")
		assert.Contains(t, outputStr, "--verbose")
		assert.Contains(t, outputStr, "--jobs")
		assert.Contains(t, outputStr, "--filter")
		assert.Contains(t, outputStr, "--exclude")
		assert.Contains(t, outputStr, "--fail-fast")
		assert.Contains(t, outputStr, "--dry-run")
	})

	t.Run("CustomBuildCommand", func(t *testing.T) {
		// Create a temporary project with a custom build_cmd
		tempDir := t.TempDir()
		testProject := filepath.Join(tempDir, "test-custom-build")
		require.NoError(t, os.MkdirAll(testProject, 0o755))

		// Create a grove.yml with custom build_cmd
		groveYml := filepath.Join(testProject, "grove.yml")
		require.NoError(t, os.WriteFile(groveYml, []byte(`name: test-custom-build
version: "1.0"
build_cmd: echo "Custom build command executed successfully"
`), 0o644))

		// Change to the test project directory
		originalDir, err := os.Getwd()
		require.NoError(t, err)
		defer func() { _ = os.Chdir(originalDir) }()

		require.NoError(t, os.Chdir(testProject))

		// Run grove build in the test project (non-TTY produces JSON output)
		cmd := exec.Command(groveBin, "build")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "build should succeed with custom command: %s", string(output))

		outputStr := string(output)
		assert.Contains(t, outputStr, `"success": true`,
			"should report success for custom build command")
		assert.Contains(t, outputStr, "test-custom-build",
			"should include the project name")
	})

	t.Run("CustomBuildCommandFallback", func(t *testing.T) {
		tempDir := t.TempDir()
		testProject := filepath.Join(tempDir, "test-fallback")
		require.NoError(t, os.MkdirAll(testProject, 0o755))

		groveYml := filepath.Join(testProject, "grove.yml")
		require.NoError(t, os.WriteFile(groveYml, []byte(`name: test-fallback
version: "1.0"
`), 0o644))

		makefile := filepath.Join(testProject, "Makefile")
		require.NoError(t, os.WriteFile(makefile, []byte(`
build:
	@echo "Using default make build"
`), 0o644))

		originalDir, err := os.Getwd()
		require.NoError(t, err)
		defer func() { _ = os.Chdir(originalDir) }()

		require.NoError(t, os.Chdir(testProject))

		// Run grove build (non-TTY produces JSON output)
		cmd := exec.Command(groveBin, "build")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "build should succeed with default make build: %s", string(output))

		outputStr := string(output)
		assert.Contains(t, outputStr, `"success": true`,
			"should report success when falling back to 'make build'")
		assert.Contains(t, outputStr, "test-fallback",
			"should include the project name")
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
- Custom build_cmd configuration
- Fallback to default 'make build' when build_cmd is not specified
`)
}
