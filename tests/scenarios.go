package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/grovetools/tend/pkg/harness"
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

		// Explicit real-binary V2/V3 configuration cutover acceptance gate.
		ConfigCutoverV2V3Scenario(),

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

		// The unified-identity full loop: join → subscribe → materialize →
		// presence note on a second origin → satellite config seed → satellite
		// round trip, against a real grove-syncd and two real groveds
		// (tests/scenarios_identity_loop.go).
		IdentityUnifiedLoopScenario(),
	}
}

// setupGlobalGroveConfig records a scan root and a complete notebook binding
// in the sandboxed canonical routing files.
func setupGlobalGroveConfig(ctx *harness.Context, searchPath string) error {
	globalConfigDir := filepath.Join(ctx.ConfigDir(), "grove")
	if err := os.MkdirAll(globalConfigDir, 0o755); err != nil {
		return fmt.Errorf("failed to create global config dir: %w", err)
	}

	roots := fmt.Sprintf("[roots.work]\npath = %s\nscan = true\n", strconv.Quote(searchPath))
	if err := os.WriteFile(filepath.Join(globalConfigDir, "roots.toml"), []byte(roots), 0o600); err != nil {
		return fmt.Errorf("failed to write roots.toml: %w", err)
	}
	notebookRoot := filepath.Join(ctx.RootDir, "notebooks", "nb")
	notebooks := fmt.Sprintf("default = \"nb\"\n[notebooks.nb]\nroot = %s\n", strconv.Quote(notebookRoot))
	if err := os.WriteFile(filepath.Join(globalConfigDir, "notebooks.toml"), []byte(notebooks), 0o600); err != nil {
		return fmt.Errorf("failed to write notebooks.toml: %w", err)
	}
	return nil
}
