package tests

import "github.com/mattsolo1/grove-tend/pkg/harness"

// AllScenarios returns all test scenarios for grove-meta
func AllScenarios() []*harness.Scenario {
	return []*harness.Scenario{
		ConventionalCommitsScenario(),
		AddRepoDryRunScenario(),
		AddRepoWithGitHubScenario(),
		AddRepoSkipGitHubScenario(),
		PolyglotProjectTypesScenario(),
		PolyglotDependencyGraphScenario(),
		PolyglotAddRepoScenario(),
		PolyglotReleaseScenario(),
		LLMChangelogScenario(),
		ReleaseTUIScenario(),
	}
}

