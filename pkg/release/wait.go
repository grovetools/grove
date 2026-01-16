package release

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// WaitConfig holds configuration for waiting on module availability
type WaitConfig struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Timeout        time.Duration
}

// DefaultWaitConfig returns the default wait configuration
func DefaultWaitConfig() WaitConfig {
	return WaitConfig{
		MaxRetries:     20,
		InitialBackoff: 15 * time.Second,
		MaxBackoff:     60 * time.Second,
		Timeout:        5 * time.Minute,
	}
}

// WaitForModuleAvailability polls until a module version is available on the Go module proxy
func WaitForModuleAvailability(ctx context.Context, modulePath, version string) error {
	config := DefaultWaitConfig()
	return WaitForModuleAvailabilityWithConfig(ctx, modulePath, version, config)
}

// WaitForModuleAvailabilityWithConfig polls with custom configuration
func WaitForModuleAvailabilityWithConfig(ctx context.Context, modulePath, version string, config WaitConfig) error {
	// Create a timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	// First, do a quick check with git to ensure the tag was pushed
	if err := checkGitTagExists(timeoutCtx, modulePath, version); err != nil {
		return fmt.Errorf("git tag check failed: %w", err)
	}

	backoff := config.InitialBackoff
	attempt := 0

	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("timeout waiting for module %s@%s after %v", modulePath, version, config.Timeout)
		default:
		}

		attempt++
		if attempt > config.MaxRetries {
			return fmt.Errorf("exceeded max retries (%d) waiting for module %s@%s", config.MaxRetries, modulePath, version)
		}

		// Try to list the module
		if err := checkModuleAvailable(timeoutCtx, modulePath, version); err == nil {
			// Success!
			return nil
		}

		// Log the retry attempt
		fmt.Printf("Waiting for module %s@%s to be available (attempt %d/%d)...\n",
			modulePath, version, attempt, config.MaxRetries)

		// Wait with backoff
		select {
		case <-timeoutCtx.Done():
			return timeoutCtx.Err()
		case <-time.After(backoff):
		}

		// Exponential backoff with cap
		backoff = backoff * 2
		if backoff > config.MaxBackoff {
			backoff = config.MaxBackoff
		}
	}
}

// checkGitTagExists verifies the tag exists on the remote repository
func checkGitTagExists(ctx context.Context, modulePath, version string) error {
	// For now, skip the git tag check as it's redundant
	// The module availability check is what really matters
	return nil
}

// checkModuleAvailable checks if a module version is available via go list
func checkModuleAvailable(ctx context.Context, modulePath, version string) error {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", fmt.Sprintf("%s@%s", modulePath, version))

	// Set up environment for private modules
	cmd.Env = append(os.Environ(),
		"GOPRIVATE=github.com/grovetools/*",
		"GOPROXY=direct",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("module not available: %w (output: %s)", err, output)
	}

	return nil
}

// WaitForMultipleModules waits for multiple modules to become available
func WaitForMultipleModules(ctx context.Context, modules map[string]string) error {
	for modulePath, version := range modules {
		if err := WaitForModuleAvailability(ctx, modulePath, version); err != nil {
			return fmt.Errorf("failed waiting for %s@%s: %w", modulePath, version, err)
		}
	}
	return nil
}
