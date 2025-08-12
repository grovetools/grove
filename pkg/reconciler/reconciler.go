package reconciler

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattsolo1/grove-meta/pkg/devlinks"
	"github.com/mattsolo1/grove-meta/pkg/logger"
	"github.com/mattsolo1/grove-meta/pkg/sdk"
)

// Reconciler manages the intelligent layering of dev links over released versions
type Reconciler struct {
	devConfig    *devlinks.Config
	toolVersions *sdk.ToolVersions
	groveHome    string
}

// New creates a new reconciler instance (DEPRECATED - use NewWithToolVersions)
func New(activeVersion string) (*Reconciler, error) {
	// For backward compatibility, create empty tool versions
	tv := &sdk.ToolVersions{
		Versions: make(map[string]string),
	}
	return NewWithToolVersions(tv)
}

// NewWithToolVersions creates a new reconciler with per-tool versions
func NewWithToolVersions(toolVersions *sdk.ToolVersions) (*Reconciler, error) {
	devConfig, err := devlinks.LoadConfig()
	if err != nil {
		// If no dev config exists, create an empty one
		devConfig = &devlinks.Config{
			Binaries: make(map[string]*devlinks.BinaryLinks),
		}
	}

	groveHome := filepath.Join(os.Getenv("HOME"), ".grove")

	return &Reconciler{
		devConfig:    devConfig,
		toolVersions: toolVersions,
		groveHome:    groveHome,
	}, nil
}

// ReconcileAll reconciles all tool symlinks based on the layered approach:
// - If a dev link is active for a tool, it takes precedence
// - Otherwise, the tool falls back to the released version
func (r *Reconciler) ReconcileAll(tools []string) error {
	for _, toolName := range tools {
		if err := r.Reconcile(toolName); err != nil {
			logger.Error("Failed to reconcile %s: %v", toolName, err)
			// Continue with other tools even if one fails
		}
	}

	return nil
}

// Reconcile reconciles the symlink for a specific tool
func (r *Reconciler) Reconcile(toolName string) error {
	binDir := filepath.Join(r.groveHome, "bin")
	symlinkPath := filepath.Join(binDir, toolName)

	// Check if a dev override is active
	if binLinks, exists := r.devConfig.Binaries[toolName]; exists && binLinks.Current != "" {
		// Dev override is active
		if linkInfo, ok := binLinks.Links[binLinks.Current]; ok {
			logger.Info("'%s' is using dev link '%s' (%s)", toolName, binLinks.Current, linkInfo.Path)
			return createOrUpdateSymlink(symlinkPath, linkInfo.Path)
		}
	}

	// No dev override, fall back to released version
	toolVersion := r.toolVersions.GetToolVersion(toolName)
	if toolVersion == "" {
		logger.Debug("No active version for %s and no dev override", toolName)
		// Remove the symlink if it exists
		os.Remove(symlinkPath)
		return nil
	}

	// Check if the tool exists in the active version
	releasedBinPath := filepath.Join(r.groveHome, "versions", toolVersion, "bin", toolName)
	if _, err := os.Stat(releasedBinPath); err == nil {
		logger.Info("'%s' is using released version '%s'", toolName, toolVersion)
		return createOrUpdateSymlink(symlinkPath, releasedBinPath)
	}

	// Tool doesn't exist in the active version
	logger.Debug("%s not found in version %s", toolName, toolVersion)
	os.Remove(symlinkPath)
	return nil
}

// GetEffectiveSource returns the effective source (dev or release) for a tool
func (r *Reconciler) GetEffectiveSource(toolName string) (source string, version string, path string) {
	// Check dev links first
	if binLinks, exists := r.devConfig.Binaries[toolName]; exists && binLinks.Current != "" {
		if linkInfo, ok := binLinks.Links[binLinks.Current]; ok {
			return "dev", binLinks.Current, linkInfo.Path
		}
	}

	// Check released version
	toolVersion := r.toolVersions.GetToolVersion(toolName)
	if toolVersion != "" {
		releasedBinPath := filepath.Join(r.groveHome, "versions", toolVersion, "bin", toolName)
		if _, err := os.Stat(releasedBinPath); err == nil {
			return "release", toolVersion, releasedBinPath
		}
	}

	return "none", "", ""
}

// createOrUpdateSymlink creates or updates a symlink
func createOrUpdateSymlink(symlinkPath, targetPath string) error {
	// Ensure the directory exists
	if err := os.MkdirAll(filepath.Dir(symlinkPath), 0755); err != nil {
		return fmt.Errorf("failed to create bin directory: %w", err)
	}

	// Remove existing symlink if it exists
	os.Remove(symlinkPath)

	// Create new symlink
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		return fmt.Errorf("failed to create symlink: %w", err)
	}

	return nil
}
