package tests

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/grovetools/tend/pkg/harness"
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

		// Notebook TOML / grove init scenarios (discovery tests are in core)
		GroveInitNotebookScenario(),

		// Dev Commands scenarios
		DevCwdWorkflow(),
		DevLinkAndUseWorkflow(),
		DevPointWorkflow(),
		DevListWorkflow(),

		// Keys/Keybind orchestrator scenarios
		KeysTraceScenario(),
		KeysAvailableScenario(),
		KeysConflictsScenario(),
		KeysMatrixScenario(),
		KeysGenerateScenario(),
		KeysSyncScenario(),
		KeysPopupsScenario(),
		KeysCheckScenario(),
		KeysDumpScenario(),
		KeysValidateScenario(),
		KeysHelpScenario(),
		KeysIntegrationScenario(),

		// Satellite simulation scenarios (local SSH sim by default; a real
		// VM under TEND_SATELLITE_REAL=1 — see tests/satellite_endpoint.go)
		SatelliteReposPushScenario(),
		SatelliteReposInterlockScenario(),
		SatelliteWorktreeScenario(),
		SatelliteVintageGuardScenario(),
		SatelliteConfigPushScenario(),
		SatelliteRegistryMergeScenario(),
		SatelliteHostKeyPinScenario(),
		SatelliteUpgradeScenario(),
		SatelliteReposFlagMatrixScenario(),

		// Satellite lifecycle acceptance scenarios: boot a REAL tart/docker
		// machine with `up`, run the suite above against it in real mode,
		// `down` it, and assert zero residue. Opt-in via
		// TEND_SATELLITE_LIFECYCLE=1 (cheap pass-with-NOTICE otherwise) —
		// see tests/scenarios_satellite_lifecycle.go.
		SatelliteTartLifecycleScenario(),
		SatelliteTartFullLifecycleScenario(),
		SatelliteDockerLifecycleScenario(),
	}
}

// setupGlobalGroveConfig creates a global grove.yml in the sandboxed home directory
// to make the discovery service aware of the test's ecosystem directory.
func setupGlobalGroveConfig(ctx *harness.Context, searchPath string) error {
	globalConfigDir := filepath.Join(ctx.ConfigDir(), "grove")
	if err := os.MkdirAll(globalConfigDir, 0o755); err != nil {
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

	if err := os.WriteFile(filepath.Join(globalConfigDir, "grove.yml"), yamlData, 0o600); err != nil {
		return fmt.Errorf("failed to write global grove.yml: %w", err)
	}
	return nil
}
