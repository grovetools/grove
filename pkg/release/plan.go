package release

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/grovetools/core/pkg/paths"
)

// ReleasePlan represents the state of an entire release operation.
type ReleasePlan struct {
	CreatedAt            time.Time                   `json:"created_at"`
	Repos                map[string]*RepoReleasePlan `json:"repos"`                  // Keyed by repo name
	ReleaseLevels        [][]string                  `json:"release_levels"`         // Topologically sorted repo names for orchestration
	RootDir              string                      `json:"root_dir"`               // The root of the ecosystem being released
	ParentVersion        string                      `json:"parent_version"`         // The parent ecosystem version
	ParentCurrentVersion string                      `json:"parent_current_version"` // Current parent ecosystem version
	Type                 string                      `json:"type,omitempty"`         // "full" or "rc"
}

// RepoReleasePlan holds the plan for a single repository.
type RepoReleasePlan struct {
	CurrentVersion      string `json:"current_version"`
	SuggestedBump       string `json:"suggested_bump"` // "major", "minor", or "patch"
	SuggestionReasoning string `json:"suggestion_reasoning"`
	SelectedBump        string `json:"selected_bump"`
	NextVersion         string `json:"next_version"`
	ChangelogPath       string `json:"changelog_path"`   // Path to the staged changelog file
	ChangelogCommit     string `json:"changelog_commit"` // Git commit hash when changelog was generated
	Status              string `json:"status"`           // "Pending Review", "Approved", "-"
	Selected            bool   `json:"selected"`         // Whether this repo is selected for release
	
	// Changelog tracking for dirty detection
	ChangelogHash       string `json:"changelog_hash,omitempty"`  // SHA256 hash of generated changelog content
	ChangelogState      string `json:"changelog_state,omitempty"` // "clean", "dirty", or "none"
	ChangelogPushed     bool   `json:"changelog_pushed,omitempty"` // Whether changelog has been committed and pushed
	CIPassed           bool   `json:"ci_passed,omitempty"`        // Whether CI passed after changelog push
	TagPushed          bool   `json:"tag_pushed,omitempty"`       // Whether release tag has been created and pushed
	
	// Release operation tracking
	LastFailedOperation string `json:"last_failed_operation,omitempty"` // Track which operation failed for better recovery
	
	// Git status information
	Branch              string `json:"branch,omitempty"`
	IsDirty             bool   `json:"is_dirty,omitempty"`
	HasUpstream         bool   `json:"has_upstream,omitempty"`
	AheadCount          int    `json:"ahead_count,omitempty"`   // Commits ahead of upstream
	BehindCount         int    `json:"behind_count,omitempty"`  // Commits behind upstream
	ModifiedCount       int    `json:"modified_count,omitempty"`
	StagedCount         int    `json:"staged_count,omitempty"`
	UntrackedCount      int    `json:"untracked_count,omitempty"`
	CommitsSinceLastTag int    `json:"commits_since_last_tag,omitempty"`
}

func getReleaseStateDir() string {
	return filepath.Join(paths.StateDir(), "release")
}

func getPlanPath() (string, error) {
	releaseDir := getReleaseStateDir()
	if err := os.MkdirAll(releaseDir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(releaseDir, "release_plan.json"), nil
}

func getStagingDir() (string, error) {
	releaseDir := getReleaseStateDir()
	stagingDir := filepath.Join(releaseDir, "staging")
	// The staging dir itself may be removed, so ensure it exists when requested.
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		return "", err
	}
	return stagingDir, nil
}

// LoadPlan reads and unmarshals the release plan from disk.
func LoadPlan() (*ReleasePlan, error) {
	planPath, err := getPlanPath()
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(planPath); os.IsNotExist(err) {
		return nil, os.ErrNotExist
	}

	data, err := os.ReadFile(planPath)
	if err != nil {
		return nil, err
	}

	var plan ReleasePlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// SavePlan marshals and writes the plan to disk.
func SavePlan(plan *ReleasePlan) error {
	planPath, err := getPlanPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(planPath, data, 0644)
}

// ClearPlan deletes the plan file and staging directory.
func ClearPlan() error {
	planPath, err := getPlanPath()
	if err == nil {
		os.Remove(planPath)
	}

	// Staging dir is now a subdirectory of the release state dir
	stagingDir := filepath.Join(getReleaseStateDir(), "staging")
	os.RemoveAll(stagingDir)

	return nil
}