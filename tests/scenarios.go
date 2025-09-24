package tests

import "github.com/mattsolo1/grove-tend/pkg/harness"

// AllScenarios returns all test scenarios for grove-meta
func AllScenarios() []*harness.Scenario {
	return []*harness.Scenario{
		ConventionalCommitsScenario(),
		AddRepoDryRunScenario(),
		// AddRepoWithGitHubScenario(), // Commented out - GitHub integration test
		AddRepoSkipGitHubScenario(),
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
	}
}

