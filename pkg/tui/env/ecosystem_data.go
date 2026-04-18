package env

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	coreenv "github.com/grovetools/core/pkg/env"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/grove/pkg/envdrift"
	"github.com/sirupsen/logrus"
)

// WorktreeState is the per-worktree slice of data the ecosystem TUI renders
// on the Deployments / Shared Infra / Orphans pages (5e will consume this).
// Any field can be nil / zero — missing state.json, missing drift cache, and
// permission errors are all treated as "no data yet" rather than hard errors.
type WorktreeState struct {
	Workspace      *workspace.WorkspaceNode
	EnvState       *coreenv.EnvStateFile
	Drift          *envdrift.DriftSummary
	DriftCheckedAt time.Time
}

// EnumerateWorktreeStates walks every discovered workspace under root and
// returns a WorktreeState for each node that is (a) a worktree and (b) shares
// root's ecosystem. For each match it reads .grove/env/state.json and the
// drift cache side-by-side, silently skipping files that don't exist yet.
//
// root is expected to be an ecosystem node (KindEcosystemRoot or
// KindEcosystemWorktree). Passing anything else returns an empty slice.
func EnumerateWorktreeStates(root *workspace.WorkspaceNode) ([]WorktreeState, error) {
	if root == nil || !root.IsEcosystem() {
		return nil, nil
	}

	// Discovery is the only API that enumerates every workspace grove knows
	// about; filtering here is cheaper than a bespoke walker. We discard
	// logger output because this is an interactive TUI path and any
	// "couldn't load grove config" warnings would just be noise.
	logger := logrus.New()
	logger.SetOutput(os.Stderr)
	logger.SetLevel(logrus.ErrorLevel)

	nodes, err := workspace.GetProjects(logger)
	if err != nil {
		return nil, err
	}

	states := make([]WorktreeState, 0, len(nodes))
	for _, node := range nodes {
		if node == nil || !node.IsWorktree() {
			continue
		}
		if !belongsToEcosystem(node, root) {
			continue
		}

		state := WorktreeState{Workspace: node}
		stateDir := filepath.Join(node.Path, ".grove", "env")

		if ef := readStateFile(stateDir); ef != nil {
			state.EnvState = ef
		}
		if drift, checkedAt, err := envdrift.LoadCache(stateDir); err == nil && drift != nil {
			state.Drift = drift
			state.DriftCheckedAt = checkedAt
		}
		states = append(states, state)
	}

	sort.Slice(states, func(i, j int) bool {
		return states[i].Workspace.Name < states[j].Workspace.Name
	})
	return states, nil
}

// belongsToEcosystem returns true when node shares an ecosystem with root.
// We accept either a matching ParentEcosystemPath or RootEcosystemPath so
// deep hierarchies (ecosystem-worktree → sub-project-worktree) all surface
// under the same Deployments table.
func belongsToEcosystem(node, root *workspace.WorkspaceNode) bool {
	if root.Path == "" {
		return false
	}
	if node.Path == root.Path {
		// The ecosystem worktree itself counts when it owns its own state.
		return true
	}
	if node.ParentEcosystemPath != "" && node.ParentEcosystemPath == root.Path {
		return true
	}
	if node.RootEcosystemPath != "" && node.RootEcosystemPath == root.Path {
		return true
	}
	// When root itself is a worktree of a higher ecosystem, RootEcosystemPath
	// is the top-level root — match by the shared root too.
	if root.RootEcosystemPath != "" && node.RootEcosystemPath == root.RootEcosystemPath {
		return true
	}
	return false
}

// readStateFile loads .grove/env/state.json from a worktree. Missing or
// unreadable files return nil so the ecosystem TUI can render a blank row
// rather than error out on one bad worktree.
func readStateFile(stateDir string) *coreenv.EnvStateFile {
	data, err := os.ReadFile(filepath.Join(stateDir, "state.json"))
	if err != nil {
		return nil
	}
	var sf coreenv.EnvStateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil
	}
	return &sf
}
