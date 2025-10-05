package aggregator

import (
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
)

// CollectorFunc is a function that collects data for a single workspace
type CollectorFunc[T any] func(workspacePath string, workspaceName string) (T, error)

// RendererFunc is a function that renders the aggregated results
type RendererFunc[T any] func(results map[string]T) error

// WorkspaceResult holds the result from a single workspace
type WorkspaceResult[T any] struct {
	Name  string
	Path  string
	Data  T
	Error error
}

// Run executes a collector function across all workspaces and renders the results
func Run[T any](collector CollectorFunc[T], renderer RendererFunc[T], workspaces []string) error {
	if len(workspaces) == 0 {
		return fmt.Errorf("no workspaces found")
	}

	// Use goroutines for concurrent collection
	results := make(chan WorkspaceResult[T], len(workspaces))
	var wg sync.WaitGroup

	for _, wsPath := range workspaces {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()

			// Use base name for workspace name since rootDir is not available
			name := path
			data, err := collector(path, name)
			results <- WorkspaceResult[T]{
				Name:  name,
				Path:  path,
				Data:  data,
				Error: err,
			}
		}(wsPath)
	}

	// Close results channel when all collectors are done
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect all results
	aggregated := make(map[string]T)
	for result := range results {
		if result.Error != nil {
			logrus.Debugf("Error collecting data for %s: %v", result.Name, result.Error)
			continue
		}
		aggregated[result.Name] = result.Data
	}

	// Render the results
	return renderer(aggregated)
}

// RunWithErrors is like Run but includes workspaces with errors in the results
func RunWithErrors[T any](collector CollectorFunc[T], renderer func(map[string]WorkspaceResult[T]) error, rootDir string, workspaces []string) error {
	if len(workspaces) == 0 {
		return fmt.Errorf("no workspaces found")
	}

	// Use goroutines for concurrent collection
	results := make(chan WorkspaceResult[T], len(workspaces))
	var wg sync.WaitGroup

	for _, wsPath := range workspaces {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()

			// Use path as name for now - will be refined by caller if needed
			name := path
			data, err := collector(path, name)
			results <- WorkspaceResult[T]{
				Name:  name,
				Path:  path,
				Data:  data,
				Error: err,
			}
		}(wsPath)
	}

	// Close results channel when all collectors are done
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect all results (including errors)
	aggregated := make(map[string]WorkspaceResult[T])
	for result := range results {
		aggregated[result.Name] = result
	}

	// Render the results
	return renderer(aggregated)
}
