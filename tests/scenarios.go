package tests

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattsolo1/grove-tend/pkg/harness"
	"gopkg.in/yaml.v3"
)

// AllScenarios returns all test scenarios for grove-meta
func AllScenarios() []*harness.Scenario {
	return []*harness.Scenario{
		ConventionalCommitsScenario(),
		AddRepoDryRunScenario(),
		// AddRepoWithGitHubScenario(), // Commented out - GitHub integration test
		AddRepoSkipGitHubScenario(),
		WorkspaceBootstrappingScenario(), // Add the new scenario here
		PolyglotProjectTypesScenario(),
		PolyglotDependencyGraphScenario(),
		PolyglotAddRepoScenario(),
		// PolyglotReleaseScenario(),
		LLMChangelogScenario(),
		// ReleaseTUIScenario(),
		// ReleaseTUISelectionScenario(),
		// ReleaseTUIChangelogWorkflowScenario(),
		// ReleaseTUIChangelogDirtyStateScenario(),
		ChangelogHashTrackingScenario(),
		ChangelogStateTransitionsScenario(),
		// SyncDepsReleaseScenario(),
		WorkspaceDetectionScenario(),
		WorkspaceBinaryDelegationScenario(),
		// New streamline-release refactor scenarios
		// StreamlinedFullReleaseScenario(),
		// StreamlinedRCReleaseScenario(),
		// StreamlinedFailureScenario(),

		// Setup Wizard scenarios - CLI tests
		SetupWizardCLIDefaultsScenario(),
		SetupWizardCLIDryRunScenario(),
		SetupWizardCLIOnlyScenario(),
		SetupWizardEcosystemFilesScenario(),
		SetupWizardNotebookConfigScenario(),
		SetupWizardConfigPreservationScenario(),
		SetupWizardTmuxIdempotentScenario(),
		// Setup Wizard scenarios - TUI tests (local only)
		SetupWizardTUIComponentSelectionScenario(),
		SetupWizardTUINavigationScenario(),
		SetupWizardTUIFullWorkflowScenario(),
		SetupWizardTUIDeselectAllScenario(),
		SetupWizardTUIQuitScenario(),
	}
}

// setupGlobalGroveConfig creates a global grove.yml in the sandboxed home directory
// to make the discovery service aware of the test's ecosystem directory.
func setupGlobalGroveConfig(ctx *harness.Context, searchPath string) error {
	globalConfigDir := filepath.Join(ctx.ConfigDir(), "grove")
	if err := os.MkdirAll(globalConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create global config dir: %w", err)
	}

	config := map[string]interface{}{
		"search_paths": map[string]interface{}{
			"work": map[string]interface{}{
				"path":    searchPath,
				"enabled": true,
			},
		},
	}
	yamlData, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal global config: %w", err)
	}

	if err := os.WriteFile(filepath.Join(globalConfigDir, "grove.yml"), yamlData, 0644); err != nil {
		return fmt.Errorf("failed to write global grove.yml: %w", err)
	}
	return nil
}
