package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/grove/pkg/envdrift"
	envtui "github.com/grovetools/grove/pkg/tui/env"
	"github.com/spf13/cobra"
)

// EcosystemJSON is the stable JSON surface emitted by `grove env ecosystem
// --json`. Agents and scripts depend on this shape; keep all keys
// lower_snake_case and keep nested DriftSummary tags in sync with
// envdrift.DriftSummary.
type ecosystemJSON struct {
	Ecosystem   string           `json:"ecosystem"`
	Worktrees   []worktreeJSON   `json:"worktrees"`
	SharedInfra *sharedInfraJSON `json:"shared_infra,omitempty"`
	Orphans     []string         `json:"orphans"`
}

type worktreeJSON struct {
	Name      string     `json:"name"`
	Path      string     `json:"path"`
	Profile   string     `json:"profile,omitempty"`
	Provider  string     `json:"provider,omitempty"`
	State     string     `json:"state"`
	Endpoints []string   `json:"endpoints,omitempty"`
	Drift     *driftJSON `json:"drift,omitempty"`
}

type sharedInfraJSON struct {
	Profile  string     `json:"profile"`
	Provider string     `json:"provider,omitempty"`
	Drift    *driftJSON `json:"drift,omitempty"`
}

// driftJSON wraps envdrift.DriftSummary with the cache timestamp + staleness
// flag that only matter at the ecosystem-overview level (the drift CLI
// surfaces freshness separately via stderr).
type driftJSON struct {
	*envdrift.DriftSummary
	CheckedAt time.Time `json:"checked_at"`
	Stale     bool      `json:"stale"`
}

func newEnvEcosystemCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "ecosystem",
		Short: "Cross-worktree ecosystem snapshot (CLI companion to `grove env tui`)",
		Long: `Print a cross-worktree snapshot of every deployment under the current
ecosystem: profile, state, endpoints, and cached drift summary. Mirrors the
Deployments matrix in ` + "`grove env tui`" + ` so scripts and agents can consume
the same view headlessly.

Must be run from the ecosystem root or any worktree inside it — the command
auto-resolves to the containing ecosystem. Running it from a non-ecosystem
directory returns a clear error.

The JSON schema is lower_snake_case and keeps nested drift objects identical
to ` + "`grove env drift --json`" + ` so downstream jq filters stay consistent.`,
		Example: `  # Text table of every worktree under this ecosystem
  grove env ecosystem

  # All profiles in use, one per line
  grove env ecosystem --json | jq '.worktrees[].profile'

  # Drifting worktrees only
  grove env ecosystem --json | jq '.worktrees[] | select(.drift.has_drift)'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEnvEcosystem(jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

// runEnvEcosystem resolves the ecosystem root, enumerates worktree state,
// and prints either a tab-aligned text table or a JSON document. Kept
// separate from the cobra.Command so tests can drive it directly.
func runEnvEcosystem(jsonOutput bool) error {
	root, err := resolveEcosystemRoot()
	if err != nil {
		return err
	}

	cfg, err := config.LoadFrom(root.Path)
	if err != nil {
		// Fall back to no config — we can still enumerate worktrees and
		// render drift, just without the shared-infra block.
		cfg = &config.Config{}
	}

	states, err := envtui.EnumerateWorktreeStates(root)
	if err != nil {
		return fmt.Errorf("enumerate worktrees: %w", err)
	}

	payload := buildEcosystemPayload(root, cfg, states)

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}

	return writeEcosystemTable(os.Stdout, payload)
}

// resolveEcosystemRoot finds the workspace node for cwd, then walks to the
// ecosystem root if the user is inside a worktree. Returns a helpful error
// when cwd isn't part of any ecosystem.
func resolveEcosystemRoot() (*workspace.WorkspaceNode, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get cwd: %w", err)
	}
	node, err := workspace.GetProjectByPath(cwd)
	if err != nil || node == nil {
		return nil, fmt.Errorf("current directory is not an ecosystem root or ecosystem worktree")
	}

	// Already at the ecosystem root (or an ecosystem worktree, which
	// satisfies IsEcosystem()) — good enough for enumeration.
	if node.IsEcosystem() {
		return node, nil
	}

	// Inside a worktree but not the ecosystem itself — re-resolve to the
	// top-level root so EnumerateWorktreeStates sees every sibling.
	if node.RootEcosystemPath != "" {
		rootNode, err := workspace.GetProjectByPath(node.RootEcosystemPath)
		if err == nil && rootNode != nil && rootNode.IsEcosystem() {
			return rootNode, nil
		}
	}

	return nil, fmt.Errorf("current directory is not an ecosystem root or ecosystem worktree")
}

// buildEcosystemPayload assembles the serializable view. Split from
// runEnvEcosystem so the text writer and JSON marshaler share one
// construction path — the JSON schema is the source of truth.
func buildEcosystemPayload(root *workspace.WorkspaceNode, cfg *config.Config, states []envtui.WorktreeState) ecosystemJSON {
	payload := ecosystemJSON{
		Ecosystem: root.Name,
		Worktrees: make([]worktreeJSON, 0, len(states)),
		Orphans:   envtui.DetectLocalOrphans(root.Path, states),
	}

	for _, s := range states {
		if s.Workspace == nil {
			continue
		}
		row := worktreeJSON{
			Name:  s.Workspace.Name,
			Path:  s.Workspace.Path,
			State: envtui.FormatWorktreeStateSummary(s),
		}
		if s.EnvState != nil {
			row.Profile = s.EnvState.Environment
			row.Provider = s.EnvState.Provider
			row.Endpoints = s.EnvState.Endpoints
		}
		if s.Drift != nil {
			row.Drift = &driftJSON{
				DriftSummary: s.Drift,
				CheckedAt:    s.DriftCheckedAt,
				Stale:        envdrift.IsStale(s.DriftCheckedAt),
			}
		}
		payload.Worktrees = append(payload.Worktrees, row)
	}

	shared := sharedProfileName(cfg)
	if shared != "" {
		infra := &sharedInfraJSON{Profile: shared}
		if ec := lookupEnvironment(cfg, shared); ec != nil {
			infra.Provider = ec.Provider
		}
		// Shared-infra drift cache lives at the ecosystem root's
		// .grove/env/drift.json (same convention as the Shared Infra page).
		if summary, checkedAt, err := envdrift.LoadCache(root.Path + "/.grove/env"); err == nil && summary != nil {
			infra.Drift = &driftJSON{
				DriftSummary: summary,
				CheckedAt:    checkedAt,
				Stale:        envdrift.IsStale(checkedAt),
			}
		}
		payload.SharedInfra = infra
	}

	return payload
}

// writeEcosystemTable prints a tab-aligned table matching the TUI
// Deployments columns: WORKTREE, PROFILE, STATE, ENDPOINTS, DRIFT. Kept
// terse — this is a CLI companion, not a full dashboard.
func writeEcosystemTable(w *os.File, p ecosystemJSON) error {
	fmt.Fprintf(w, "ecosystem: %s  (%d worktree(s))\n\n", p.Ecosystem, len(p.Worktrees))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "WORKTREE\tPROFILE\tSTATE\tENDPOINTS\tDRIFT")
	for _, row := range p.Worktrees {
		profile := row.Profile
		if profile == "" {
			profile = "-"
		}
		endpoints := "(local only)"
		if len(row.Endpoints) == 1 {
			endpoints = row.Endpoints[0]
		} else if len(row.Endpoints) > 1 {
			endpoints = fmt.Sprintf("%s, +%d", row.Endpoints[0], len(row.Endpoints)-1)
		}
		drift := formatDriftCellText(row.Drift, row.Provider)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			row.Name, profile, row.State, endpoints, drift)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if p.SharedInfra != nil {
		fmt.Fprintf(w, "\nshared infra: %s (%s)\n", p.SharedInfra.Profile, p.SharedInfra.Provider)
		if p.SharedInfra.Drift != nil {
			fmt.Fprintf(w, "  drift: %s\n",
				formatDriftCellText(p.SharedInfra.Drift, p.SharedInfra.Provider))
		}
	}

	if len(p.Orphans) > 0 {
		fmt.Fprintf(w, "\norphans (%d):\n", len(p.Orphans))
		for _, o := range p.Orphans {
			fmt.Fprintf(w, "  %s\n", o)
		}
	}
	return nil
}

// formatDriftCellText mirrors the TUI's drift column rendering. "n/a" for
// non-terraform profiles, "—" for missing cache, and "clean" / "+A ~B -C"
// with an age suffix otherwise.
func formatDriftCellText(d *driftJSON, provider string) string {
	if d == nil || d.DriftSummary == nil {
		if provider != "" && provider != "terraform" {
			return "n/a"
		}
		return "-"
	}
	age := humanDuration(time.Since(d.CheckedAt))
	if !d.HasDrift {
		return "clean · " + age
	}
	return fmt.Sprintf("+%d ~%d -%d · %s", d.Add, d.Change, d.Destroy, age)
}

// humanDuration mirrors deployments_page.humanAge but lives here so the CLI
// doesn't have to import TUI internals for a two-line formatter.
func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// sharedProfileName returns the single shared profile name, or "" when
// none is defined. Delegates to config.IsSharedProfile so CLI and TUI
// stay in lockstep.
func sharedProfileName(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	for name := range cfg.Environments {
		if config.IsSharedProfile(cfg, name) {
			return name
		}
	}
	return ""
}

// lookupEnvironment resolves a profile name to its raw EnvironmentConfig.
// "default" maps to cfg.Environment; anything else to cfg.Environments[name].
func lookupEnvironment(cfg *config.Config, name string) *config.EnvironmentConfig {
	if cfg == nil || name == "" {
		return nil
	}
	if name == "default" {
		return cfg.Environment
	}
	if cfg.Environments != nil {
		return cfg.Environments[name]
	}
	return nil
}
