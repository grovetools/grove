package tests

import "github.com/grovepm/grove-tend/internal/harness"

// AllScenarios returns all test scenarios for grove-meta
func AllScenarios() []*harness.Scenario {
	return []*harness.Scenario{
		ConventionalCommitsScenario(),
	}
}