package tests

import "github.com/mattsolo1/grove-tend/pkg/harness"

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
		PolyglotReleaseScenario(),
		LLMChangelogScenario(),
		ReleaseTUIScenario(),
		ReleaseTUISelectionScenario(),
		ReleaseTUIChangelogWorkflowScenario(),
		ReleaseTUIChangelogDirtyStateScenario(),
		ChangelogHashTrackingScenario(),
		ChangelogStateTransitionsScenario(),
		SyncDepsReleaseScenario(),
		WorkspaceDetectionScenario(),
		WorkspaceBinaryDelegationScenario(),
		// TODO: Uncomment once release refactor commands are implemented
		// StreamlinedFullReleaseScenario(),
		// StreamlinedRCReleaseScenario(),
		// StreamlinedFailureScenario(),
	}
}

