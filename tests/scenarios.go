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
		RepoGitHubInitDryRunScenario(),
		LLMChangelogScenario(),
		ChangelogHashTrackingScenario(),
		ChangelogStateTransitionsScenario(),

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
		// Setup Wizard scenarios - Integration tests (explicit only, uses real grove binaries)
		SetupWizardRealBinariesScenario(),

		// Ecosystem Init discovery scenarios
		EcosystemInitAlreadyDiscoverableScenario(),
		EcosystemInitNotDiscoverableScenario(),
		EcosystemInitDeclineAddScenario(),
		EcosystemInitNonInteractiveScenario(),
		EcosystemInitPreservesConfigScenario(),
		EcosystemInitEditsCorrectFileScenario(),
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
