package env

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
//
// Orphan entries — state.json files that no longer map to a discovered
// worktree — have Workspace == nil and OrphanStatePath set to the absolute
// path of the state.json. The Deployments page renders these inline so the
// main matrix always reflects every state.json on disk.
type WorktreeState struct {
	Workspace       *workspace.WorkspaceNode
	EnvState        *coreenv.EnvStateFile
	Drift           *envdrift.DriftSummary
	DriftCheckedAt  time.Time
	OrphanStatePath string
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
		if node == nil {
			continue
		}
		// Whitelist the two node kinds that represent a deployable ecosystem
		// slice: the ecosystem root itself (so the main checkout shows up as
		// its own row) and per-worktree ecosystem checkouts. Subprojects and
		// their worktrees are not independent deployment targets.
		if node.Kind != workspace.KindEcosystemRoot && node.Kind != workspace.KindEcosystemWorktree {
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
	// Append any orphan state.json files under this ecosystem's
	// .grove-worktrees/ directory so the Deployments matrix sees them
	// inline rather than hiding them on a separate tab.
	orphans := DetectLocalOrphans(root.Path, states)
	states = append(states, orphans...)
	return states, nil
}

// belongsToEcosystem returns true when node shares an ecosystem with root.
// We accept either a matching ParentEcosystemPath or RootEcosystemPath so
// deep hierarchies (ecosystem-worktree → sub-project-worktree) all surface
// under the same Deployments table.
//
// Path comparisons are case-insensitive because macOS's case-insensitive
// filesystem lets workspace.GetProjects and workspace.GetProjectByPath
// return different casings for the same directory — e.g. "/Users/.../Code"
// vs "/users/.../code" — and a strict string match would drop every
// worktree on the floor.
func belongsToEcosystem(node, root *workspace.WorkspaceNode) bool {
	if root.Path == "" {
		return false
	}
	if pathsEqual(node.Path, root.Path) {
		// The ecosystem worktree itself counts when it owns its own state.
		return true
	}
	if node.ParentEcosystemPath != "" && pathsEqual(node.ParentEcosystemPath, root.Path) {
		return true
	}
	if node.RootEcosystemPath != "" && pathsEqual(node.RootEcosystemPath, root.Path) {
		return true
	}
	// When root itself is a worktree of a higher ecosystem, RootEcosystemPath
	// is the top-level root — match by the shared root too.
	if root.RootEcosystemPath != "" && pathsEqual(node.RootEcosystemPath, root.RootEcosystemPath) {
		return true
	}
	return false
}

// pathsEqual compares two filesystem paths for equality after normalising
// case — macOS-safe, and a no-op for paths that already match literally.
func pathsEqual(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
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

// FormatWorktreeStateSummary produces the short "● running" / "☁ applied" /
// "◯ inactive" label shown in the CLI tables and the (shrunk) TUI grid.
// Exported so `grove env ecosystem` keeps the same terminology as the TUI.
func FormatWorktreeStateSummary(state WorktreeState) string {
	if state.EnvState == nil {
		return "inactive"
	}
	running := len(state.EnvState.Services) > 0 || len(state.EnvState.Endpoints) > 0
	cloud := state.EnvState.Provider == "terraform"
	switch {
	case running && cloud:
		return "● running · ☁ applied"
	case running:
		return "● running"
	case cloud:
		return "☁ applied"
	default:
		return "inactive"
	}
}

// DetectLocalOrphans returns a WorktreeState for every .grove/env/state.json
// under the ecosystem's .grove-worktrees/ directory that isn't owned by a
// known worktree. Each returned state has Workspace == nil and
// OrphanStatePath set to the state.json path, so the Deployments page can
// render an inline orphan row. EnvState + Drift are parsed best-effort from
// disk, matching how real worktree states are loaded.
//
// Exported so the CLI (`grove env ecosystem`) can reuse the same heuristic
// as the TUI's Orphans page without importing rendering code.
func DetectLocalOrphans(ecosystemRoot string, worktrees []WorktreeState) []WorktreeState {
	orphans := []WorktreeState{}
	pattern := filepath.Join(ecosystemRoot, ".grove-worktrees", "*", ".grove", "env", "state.json")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return orphans
	}
	// Lowercase both sides of the comparison: ecosystemRoot is normalized via
	// pathutil.NormalizeForLookup (darwin lowercases), but w.Workspace.Path
	// retains original casing. Without normalizing the `known` map, active
	// worktrees get re-matched as orphans on case-insensitive filesystems.
	known := make(map[string]struct{}, len(worktrees))
	for _, w := range worktrees {
		if w.Workspace != nil {
			known[strings.ToLower(filepath.Clean(w.Workspace.Path))] = struct{}{}
		}
	}
	sort.Strings(matches)
	for _, m := range matches {
		// m == <root>/.grove-worktrees/<wt-name>/.grove/env/state.json
		// → worktree path = <root>/.grove-worktrees/<wt-name>
		wtDir := filepath.Dir(filepath.Dir(filepath.Dir(m)))
		if _, ok := known[strings.ToLower(filepath.Clean(wtDir))]; ok {
			continue
		}
		orphan := WorktreeState{OrphanStatePath: m}
		if ef := readStateFile(filepath.Dir(m)); ef != nil {
			orphan.EnvState = ef
		}
		if drift, checkedAt, err := envdrift.LoadCache(filepath.Dir(m)); err == nil && drift != nil {
			orphan.Drift = drift
			orphan.DriftCheckedAt = checkedAt
		}
		orphans = append(orphans, orphan)
	}
	return orphans
}
