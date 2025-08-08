package tests

import "github.com/mattsolo1/grove-tend/internal/harness"

// AllScenarios returns all test scenarios for grove-meta
func AllScenarios() []*harness.Scenario {
	return []*harness.Scenario{
		ConventionalCommitsScenario(),
	}
}