package release

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
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

// WaitForModuleAvailability polls until the release tag is visible on the
// module's git remote. repoDir is the module's working tree; the check is a
// `git ls-remote --tags origin <version>` against its configured origin — the
// authoritative "the tag has landed on the remote" signal under GOPROXY=direct
// (where the module proxy is bypassed and go resolves straight from git). When
// repoDir is empty it falls back to a `go list -m` probe (with GOWORK=off so it
// never resolves a workspace-local sibling instead of the published version).
func WaitForModuleAvailability(ctx context.Context, modulePath, version, repoDir string) error {
	config := DefaultWaitConfig()
	return WaitForModuleAvailabilityWithConfig(ctx, modulePath, version, repoDir, config)
}

// WaitForModuleAvailabilityWithConfig polls with custom configuration
func WaitForModuleAvailabilityWithConfig(ctx context.Context, modulePath, version, repoDir string, config WaitConfig) error {
	// Create a timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	backoff := config.InitialBackoff
	attempt := 0

	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("timeout waiting for %s@%s to be visible after %v", modulePath, version, config.Timeout)
		default:
		}

		attempt++
		if attempt > config.MaxRetries {
			return fmt.Errorf("exceeded max retries (%d) waiting for %s@%s", config.MaxRetries, modulePath, version)
		}

		// Check whether the tag is visible on the remote.
		if err := checkTagVisible(timeoutCtx, modulePath, version, repoDir); err == nil {
			// Success!
			return nil
		}

		// Log the retry attempt
		fmt.Printf("Waiting for %s@%s to be visible on origin (attempt %d/%d)...\n",
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

// checkTagVisible reports whether the release tag is visible on the module's
// remote. Preferred path (repoDir set): `git ls-remote --tags origin <version>`
// — a direct, proxy-free check that the tag landed on the origin the release
// pushed to (real GitHub in production, a file:// bare repo in tests). Fallback
// (repoDir empty): a `go list -m` probe with GOWORK=off so it consults the
// published module rather than a workspace-local sibling.
func checkTagVisible(ctx context.Context, modulePath, version, repoDir string) error {
	if repoDir != "" {
		return gitTagVisibleOnRemote(ctx, repoDir, version)
	}
	return checkModuleAvailable(ctx, modulePath, version)
}

// gitTagVisibleOnRemote returns nil once `git ls-remote --tags origin` lists the
// exact tag ref, else a non-nil error. It matches the fully-qualified ref so a
// tag whose name is a prefix of another cannot yield a false positive.
func gitTagVisibleOnRemote(ctx context.Context, repoDir, version string) error {
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--tags", "origin", "refs/tags/"+version) //nolint:gosec // G204: version is a computed semver tag, not user input
	cmd.Dir = repoDir
	// GOWORK=off is harmless for git but keeps every go-adjacent release
	// invocation uniformly workspace-free.
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git ls-remote failed: %w (output: %s)", err, output)
	}
	if strings.TrimSpace(string(output)) == "" {
		return fmt.Errorf("tag %s not yet visible on origin", version)
	}
	return nil
}

// checkModuleAvailable checks if a module version is available via go list. It
// sets GOWORK=off so the probe never resolves a workspace-local sibling module
// (which would spuriously succeed for an unpublished version).
func checkModuleAvailable(ctx context.Context, modulePath, version string) error {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", fmt.Sprintf("%s@%s", modulePath, version)) //nolint:gosec // G204: args are not user-controlled

	// Set up environment for private modules
	cmd.Env = append(os.Environ(),
		"GOPRIVATE=github.com/grovetools/*",
		"GOPROXY=direct",
		"GOWORK=off",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("module not available: %w (output: %s)", err, output)
	}

	return nil
}

// WaitForMultipleModules waits for multiple modules to become available. The
// map is module path → version; remote visibility is probed via `go list -m`
// (repoDir unknown here) with GOWORK=off.
func WaitForMultipleModules(ctx context.Context, modules map[string]string) error {
	for modulePath, version := range modules {
		if err := WaitForModuleAvailability(ctx, modulePath, version, ""); err != nil {
			return fmt.Errorf("failed waiting for %s@%s: %w", modulePath, version, err)
		}
	}
	return nil
}
