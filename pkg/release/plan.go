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
	ChangelogHash     string `json:"changelog_hash,omitempty"`      // SHA256 hash of generated changelog content
	ChangelogState    string `json:"changelog_state,omitempty"`     // "clean", "dirty", or "none"
	ChangelogPushed   bool   `json:"changelog_pushed,omitempty"`    // Whether changelog has been committed and pushed
	ChangelogGenError string `json:"changelog_gen_error,omitempty"` // Non-fatal changelog generation failure, surfaced in plan state for review
	CIPassed          bool   `json:"ci_passed,omitempty"`           // Whether CI passed after changelog push
	TagPushed         bool   `json:"tag_pushed,omitempty"`          // Whether release tag has been created and pushed

	// Docs + changelog generation tracking (grove release gen — cached-LLM
	// fan-out). Populated by `grove release gen`; consumed by the TUI (Phase 5)
	// and apply (Phase 4).
	DocsGenerated    bool      `json:"docs_generated,omitempty"`     // docgen sections were generated + staged into the notebook
	DocsGeneratedAt  time.Time `json:"docs_generated_at,omitempty"`  // when gen last generated docs for this repo
	DocsSections     []string  `json:"docs_sections,omitempty"`      // section scope generated (empty ⇒ all sections)
	ChangelogStaged  bool      `json:"changelog_staged,omitempty"`   // a fresh changelog was staged for review by gen
	CacheWriteTokens int64     `json:"cache_write_tokens,omitempty"` // total cache_creation tokens across the repo's gen wave (docs + changelog)
	CacheReadTokens  int64     `json:"cache_read_tokens,omitempty"`  // total cache_read tokens across the repo's gen wave
	GenEstCostUSD    float64   `json:"gen_est_cost_usd,omitempty"`   // estimated USD cost of the repo's gen wave
	GenError         string    `json:"gen_error,omitempty"`          // non-empty when gen failed this repo (context freeze-verify, docs, or changelog)
	GenAttempts      int       `json:"gen_attempts,omitempty"`       // attempts the last gen run made for this repo (>1 ⇒ transient failures were retried)
	CheckCommand     string    `json:"check_command,omitempty"`      // optional per-repo local check command (opening only — no test wiring in this effort)
	CheckStatus      string    `json:"check_status,omitempty"`       // "skipped" when no check command; placeholder for future check integration

	// Release operation tracking
	LastFailedOperation string `json:"last_failed_operation,omitempty"` // Track which operation failed for better recovery

	// Git status information
	Branch              string `json:"branch,omitempty"`
	IsDirty             bool   `json:"is_dirty,omitempty"`
	HasUpstream         bool   `json:"has_upstream,omitempty"`
	AheadCount          int    `json:"ahead_count,omitempty"`  // Commits ahead of upstream
	BehindCount         int    `json:"behind_count,omitempty"` // Commits behind upstream
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
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(releaseDir, "release_plan.json"), nil
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

// SavePlan marshals and writes the plan to disk. The write is atomic (temp
// file in the same directory + rename) so a crash mid-write can never leave a
// truncated plan behind — the plan is the release's checkpoint state, and both
// gen (after every repo) and apply (after every stage) rewrite it frequently.
func SavePlan(plan *ReleasePlan) error {
	planPath, err := getPlanPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(planPath), ".release_plan-*.json.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, planPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// proposalDirName is the per-repo staging subtree ("staging/<repo>/proposal/")
// that holds docs-proposal run history from 'grove release propose'. It is
// drafting-loop history awaiting human review, not plan state, so ClearPlan
// preserves it unless explicitly told otherwise.
const proposalDirName = "proposal"

// ClearPlan deletes the plan file and clears the staging directory. By default
// each repo's staged content (changelogs etc.) is removed but its proposal/
// subtree — docs-proposal run history awaiting review — is PRESERVED; pass
// includeProposals to wipe the whole staging tree.
func ClearPlan(includeProposals bool) error {
	planPath, err := getPlanPath()
	if err == nil {
		os.Remove(planPath)
	}

	// Staging dir is now a subdirectory of the release state dir
	stagingDir := filepath.Join(getReleaseStateDir(), "staging")
	return clearStagingDir(stagingDir, includeProposals)
}

// clearStagingDir clears a release staging tree. With includeProposals it is a
// full wipe; otherwise everything under each repo's staging dir EXCEPT the
// proposal/ subtree is removed (and a repo dir with no proposal history is
// removed entirely). Factored on a caller-supplied path so it is unit-testable
// against a temp dir.
func clearStagingDir(stagingDir string, includeProposals bool) error {
	if includeProposals {
		return os.RemoveAll(stagingDir)
	}

	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		repoDir := filepath.Join(stagingDir, e.Name())
		if !e.IsDir() {
			// Stray top-level files are plan-scoped staging output.
			if rerr := os.Remove(repoDir); rerr != nil {
				return rerr
			}
			continue
		}
		// A repo dir with no proposal history clears entirely; one with history
		// keeps exactly the proposal/ subtree.
		if info, serr := os.Stat(filepath.Join(repoDir, proposalDirName)); serr != nil || !info.IsDir() {
			if rerr := os.RemoveAll(repoDir); rerr != nil {
				return rerr
			}
			continue
		}
		subEntries, serr := os.ReadDir(repoDir)
		if serr != nil {
			return serr
		}
		for _, se := range subEntries {
			if se.IsDir() && se.Name() == proposalDirName {
				continue
			}
			if rerr := os.RemoveAll(filepath.Join(repoDir, se.Name())); rerr != nil {
				return rerr
			}
		}
	}
	return nil
}
