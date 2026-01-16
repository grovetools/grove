package build_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grovetools/grove/pkg/build"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun(t *testing.T) {
	t.Run("BasicExecution", func(t *testing.T) {
		// Create a temporary directory with test projects
		tempDir := t.TempDir()
		projects := createTestProjects(t, tempDir, 3, true)

		jobs := make([]build.BuildJob, len(projects))
		for i, p := range projects {
			jobs[i] = build.BuildJob{
				Name: filepath.Base(p),
				Path: p,
			}
		}

		ctx := context.Background()
		results := build.Run(ctx, jobs, 2, true)

		var count int
		for result := range results {
			count++
			assert.NotNil(t, result.Job)
			assert.NotEmpty(t, result.Job.Name)
			// Should succeed since we created valid Makefiles
			assert.NoError(t, result.Err)
			assert.Greater(t, result.Duration, time.Duration(0))
		}

		assert.Equal(t, len(jobs), count)
	})

	t.Run("FailureHandling", func(t *testing.T) {
		tempDir := t.TempDir()
		// Create one successful and one failing project
		successProject := createSuccessProject(t, tempDir, "success")
		failProject := createFailProject(t, tempDir, "fail")

		jobs := []build.BuildJob{
			{Name: "success", Path: successProject},
			{Name: "fail", Path: failProject},
		}

		ctx := context.Background()
		results := build.Run(ctx, jobs, 2, true) // continue on error

		var successCount, failCount int
		for result := range results {
			if result.Err != nil {
				failCount++
			} else {
				successCount++
			}
		}

		assert.Equal(t, 1, successCount)
		assert.Equal(t, 1, failCount)
	})

	t.Run("StopOnError", func(t *testing.T) {
		tempDir := t.TempDir()
		// Create multiple projects with one failing
		projects := []string{
			createSuccessProject(t, tempDir, "p1"),
			createFailProject(t, tempDir, "p2"),
			createSuccessProject(t, tempDir, "p3"),
		}

		jobs := make([]build.BuildJob, len(projects))
		for i, p := range projects {
			jobs[i] = build.BuildJob{
				Name: filepath.Base(p),
				Path: p,
			}
		}

		ctx := context.Background()
		results := build.Run(ctx, jobs, 1, false) // stop on error with 1 worker

		var count int
		var foundError bool
		for result := range results {
			count++
			if result.Err != nil {
				foundError = true
			}
		}

		assert.True(t, foundError)
		// With stop on error, we might not process all jobs
		assert.LessOrEqual(t, count, len(jobs))
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		tempDir := t.TempDir()
		projects := createTestProjects(t, tempDir, 5, true)

		jobs := make([]build.BuildJob, len(projects))
		for i, p := range projects {
			jobs[i] = build.BuildJob{
				Name: filepath.Base(p),
				Path: p,
			}
		}

		ctx, cancel := context.WithCancel(context.Background())
		results := build.Run(ctx, jobs, 2, true)

		// Cancel after receiving first result
		var count int
		for result := range results {
			count++
			if count == 1 {
				cancel()
			}
			// After cancellation, remaining jobs should have an error (either context canceled or killed)
			if count > 1 && result.Err != nil {
				errStr := result.Err.Error()
				assert.True(t, strings.Contains(errStr, "context canceled") ||
					strings.Contains(errStr, "killed") ||
					strings.Contains(errStr, "signal"),
					"Expected context-related error, got: %s", errStr)
			}
		}
	})

	t.Run("Parallelism", func(t *testing.T) {
		tempDir := t.TempDir()
		numProjects := 10
		projects := createTestProjects(t, tempDir, numProjects, true)

		jobs := make([]build.BuildJob, len(projects))
		for i, p := range projects {
			jobs[i] = build.BuildJob{
				Name: filepath.Base(p),
				Path: p,
			}
		}

		// Test with different worker counts
		for _, numWorkers := range []int{1, 2, 5, 10} {
			t.Run(fmt.Sprintf("%d_workers", numWorkers), func(t *testing.T) {
				ctx := context.Background()
				start := time.Now()
				results := build.Run(ctx, jobs, numWorkers, true)

				var count int
				for range results {
					count++
				}
				duration := time.Since(start)

				assert.Equal(t, numProjects, count)
				t.Logf("Completed %d builds with %d workers in %v", count, numWorkers, duration)
			})
		}
	})
}

func TestRunWithEvents(t *testing.T) {
	t.Run("EventSequence", func(t *testing.T) {
		tempDir := t.TempDir()
		projects := createTestProjects(t, tempDir, 3, true)

		jobs := make([]build.BuildJob, len(projects))
		for i, p := range projects {
			jobs[i] = build.BuildJob{
				Name: filepath.Base(p),
				Path: p,
			}
		}

		ctx := context.Background()
		events := build.RunWithEvents(ctx, jobs, 2, true)

		startEvents := make(map[string]bool)
		finishEvents := make(map[string]bool)

		for event := range events {
			switch event.Type {
			case "start":
				assert.False(t, startEvents[event.Job.Name], "Duplicate start event for %s", event.Job.Name)
				startEvents[event.Job.Name] = true
				assert.Nil(t, event.Result)
			case "finish":
				assert.True(t, startEvents[event.Job.Name], "Finish before start for %s", event.Job.Name)
				assert.False(t, finishEvents[event.Job.Name], "Duplicate finish event for %s", event.Job.Name)
				finishEvents[event.Job.Name] = true
				assert.NotNil(t, event.Result)
			default:
				t.Fatalf("Unknown event type: %s", event.Type)
			}
		}

		// All jobs should have both start and finish events
		assert.Equal(t, len(jobs), len(startEvents))
		assert.Equal(t, len(jobs), len(finishEvents))
	})

	t.Run("ConcurrentEvents", func(t *testing.T) {
		tempDir := t.TempDir()
		numProjects := 10
		projects := createTestProjects(t, tempDir, numProjects, true)

		jobs := make([]build.BuildJob, len(projects))
		for i, p := range projects {
			jobs[i] = build.BuildJob{
				Name: fmt.Sprintf("project-%d", i),
				Path: p,
			}
		}

		ctx := context.Background()
		events := build.RunWithEvents(ctx, jobs, 5, true)

		var mu sync.Mutex
		runningCount := 0
		maxRunning := 0

		for event := range events {
			mu.Lock()
			if event.Type == "start" {
				runningCount++
				if runningCount > maxRunning {
					maxRunning = runningCount
				}
			} else if event.Type == "finish" {
				runningCount--
			}
			mu.Unlock()
		}

		assert.Equal(t, 0, runningCount, "All started jobs should finish")
		assert.Greater(t, maxRunning, 1, "Should have concurrent builds")
		assert.LessOrEqual(t, maxRunning, 5, "Should respect worker limit")
	})
}

// Helper functions to create test projects

func createTestProjects(t *testing.T, baseDir string, count int, success bool) []string {
	var projects []string
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("project-%d", i)
		var path string
		if success {
			path = createSuccessProject(t, baseDir, name)
		} else {
			path = createFailProject(t, baseDir, name)
		}
		projects = append(projects, path)
	}
	return projects
}

func createSuccessProject(t *testing.T, baseDir, name string) string {
	projectDir := filepath.Join(baseDir, name)
	require.NoError(t, os.MkdirAll(projectDir, 0755))

	makefile := filepath.Join(projectDir, "Makefile")
	content := `build:
	@echo "Building ` + name + `"
	@sleep 0.01
	@echo "Build complete"
`
	require.NoError(t, os.WriteFile(makefile, []byte(content), 0644))
	return projectDir
}

func createFailProject(t *testing.T, baseDir, name string) string {
	projectDir := filepath.Join(baseDir, name)
	require.NoError(t, os.MkdirAll(projectDir, 0755))

	makefile := filepath.Join(projectDir, "Makefile")
	content := `build:
	@echo "Building ` + name + `"
	@echo "Error: Build failed!" >&2
	@exit 1
`
	require.NoError(t, os.WriteFile(makefile, []byte(content), 0644))
	return projectDir
}