package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/prune"
	"github.com/spf13/cobra"

	envtui "github.com/grovetools/grove/pkg/tui/env"
)

// newEnvPruneCmd wires the `grove env prune` subcommand. Dry-run by
// default: lists orphans without touching anything. --yes applies local
// deletions; --include=cloud additionally applies cloud deletions (only
// honored together with --yes). --worktree scopes to a single slug.
func newEnvPruneCmd() *cobra.Command {
	var (
		yes          bool
		includeCloud bool
		worktreeOpt  string
		jsonOutput   bool
	)
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Detect and optionally delete orphaned grove environment resources",
		Long: `Detect orphaned per-worktree resources — docker images/volumes,
.grove-worktrees/ and .grove/volumes/ directories, and cloud resources
(Cloud Run, GCE, Artifact Registry, GCS state) — by cross-referencing
against the ecosystem's active worktree list.

Dry-run by default: nothing is deleted without --yes. Cloud resources
additionally require --include=cloud.`,
		Example: `  grove env prune                       # dry-run list
  grove env prune --yes                 # delete local orphans
  grove env prune --yes --include=cloud # delete local + cloud orphans
  grove env prune --worktree tier1-tf-rerun --yes --include=cloud`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEnvPrune(yes, includeCloud, worktreeOpt, jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Apply local deletions (required to modify anything)")
	cmd.Flags().BoolVar(&includeCloud, "include-cloud", false, "Include cloud resources in detection and (with --yes) deletion")
	cmd.Flags().StringVar(&worktreeOpt, "worktree", "", "Scope prune to a single worktree slug")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func runEnvPrune(yes, includeCloud bool, worktreeOpt string, jsonOutput bool) error {
	root, err := resolveEcosystemRoot()
	if err != nil {
		return err
	}
	cfg, err := config.LoadFrom(root.Path)
	if err != nil {
		cfg = &config.Config{}
	}

	states, err := envtui.EnumerateWorktreeStates(root)
	if err != nil {
		return fmt.Errorf("enumerate worktrees: %w", err)
	}

	active, inactive := collectSlugs(root.Path, states)
	cloud := buildCloudConfig(cfg, root.Name)

	in := prune.Inputs{
		GitRoot:      root.Path,
		Active:       active,
		Inactive:     inactive,
		Cloud:        cloud,
		DockerRunner: prune.ExecRunner{},
		GcloudRunner: prune.ExecRunner{},
	}
	opts := prune.Options{
		DryRun:       !yes,
		IncludeCloud: includeCloud,
		Worktree:     worktreeOpt,
	}

	result, err := prune.Run(in, opts)
	if err != nil {
		return err
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	return writePruneHuman(os.Stdout, result, includeCloud)
}

// collectSlugs returns (active, inactive) slug lists. Active comes from
// the workspace discovery. Inactive comes from `.grove-worktrees/` dirs
// that aren't in the active set — same heuristic used by the TUI's
// Orphans page, reused here so a slug that exists only as a stale
// directory still registers in the ExtractSlug dictionary.
func collectSlugs(ecosystemRoot string, states []envtui.WorktreeState) (active, inactive []string) {
	seen := make(map[string]struct{})
	for _, s := range states {
		if s.Workspace == nil {
			continue
		}
		name := s.Workspace.Name
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		active = append(active, name)
	}
	// Any directory under .grove-worktrees/ that isn't already active
	// counts as a known-inactive slug for dictionary purposes.
	entries, _ := os.ReadDir(filepath.Join(ecosystemRoot, ".grove-worktrees"))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, ok := seen[e.Name()]; ok {
			continue
		}
		inactive = append(inactive, e.Name())
	}
	sort.Strings(active)
	sort.Strings(inactive)
	return active, inactive
}

// buildCloudConfig derives the CloudConfig from grove.toml. It walks
// every defined environment profile and harvests:
//   - project_id (vars.project_id) — first one wins
//   - state_bucket (config.state_bucket) — first one wins
//   - image registry AR repo roots — deduped
//
// Region defaults to us-central1 (matches kitchen-env conventions and
// the current Cloud Run deployments); can be made configurable later.
func buildCloudConfig(cfg *config.Config, ecosystem string) prune.CloudConfig {
	cc := prune.CloudConfig{Ecosystem: ecosystem, Region: "us-central1"}
	seenRepo := make(map[string]struct{})

	ingest := func(env *config.EnvironmentConfig) {
		if env == nil || env.Config == nil {
			return
		}
		if cc.StateBucket == "" {
			if b, _ := env.Config["state_bucket"].(string); b != "" {
				cc.StateBucket = b
			}
		}
		if cc.Project == "" {
			if vars, ok := env.Config["vars"].(map[string]interface{}); ok {
				if p, _ := vars["project_id"].(string); p != "" {
					cc.Project = p
				}
			}
		}
		images, _ := env.Config["images"].(map[string]interface{})
		for _, v := range images {
			m, ok := v.(map[string]interface{})
			if !ok {
				continue
			}
			reg, _ := m["registry"].(string)
			if reg == "" {
				continue
			}
			// Drop the trailing "/<image-name>" so we have the
			// repo path ("us-central1-docker.pkg.dev/proj/repo").
			repo := reg
			if i := strings.LastIndex(repo, "/"); i > 0 {
				repo = repo[:i]
			}
			if _, ok := seenRepo[repo]; !ok {
				seenRepo[repo] = struct{}{}
				cc.ARRepos = append(cc.ARRepos, repo)
			}
		}
	}
	ingest(cfg.Environment)
	names := make([]string, 0, len(cfg.Environments))
	for n := range cfg.Environments {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		ingest(cfg.Environments[n])
	}
	sort.Strings(cc.ARRepos)
	return cc
}

// writePruneHuman prints a grouped, per-category summary of orphans
// plus a deletion summary if Run actually deleted anything.
func writePruneHuman(w *os.File, r *prune.PruneResult, includeCloud bool) error {
	if r.DryRun {
		fmt.Fprintln(w, "DRY RUN — pass --yes to apply local deletions, add --include-cloud for cloud.")
	}
	if r.ScopedTo != "" {
		fmt.Fprintf(w, "Scope: worktree=%s\n", r.ScopedTo)
	}

	localByCat := make(map[prune.Category][]prune.Orphan)
	cloudByCat := make(map[prune.Category][]prune.Orphan)
	for _, o := range r.Orphans {
		if o.Category.IsCloud() {
			cloudByCat[o.Category] = append(cloudByCat[o.Category], o)
		} else {
			localByCat[o.Category] = append(localByCat[o.Category], o)
		}
	}

	fmt.Fprintf(w, "\nLocal orphans (%d):\n", countOrphans(localByCat))
	printCategories(w, []prune.Category{prune.CatDockerImage, prune.CatDockerVolume, prune.CatHostWorktree, prune.CatHostVolume}, localByCat)

	if includeCloud {
		fmt.Fprintf(w, "\nCloud orphans (%d):\n", countOrphans(cloudByCat))
		printCategories(w, []prune.Category{prune.CatCloudRun, prune.CatCloudGCE, prune.CatCloudAR, prune.CatCloudGCS}, cloudByCat)
	} else if countOrphans(cloudByCat) > 0 || true {
		fmt.Fprintln(w, "\nCloud orphans: [NOT SHOWN — pass --include-cloud to scan cloud resources]")
	}

	if !r.DryRun {
		fmt.Fprintf(w, "\nDeleted: %d  Failed: %d\n", len(r.Deleted), len(r.Failed))
		for _, f := range r.Failed {
			fmt.Fprintf(w, "  FAIL %s %s: %s\n", f.Orphan.Category, f.Orphan.Name, f.Error)
		}
	}
	return nil
}

func countOrphans(m map[prune.Category][]prune.Orphan) int {
	n := 0
	for _, v := range m {
		n += len(v)
	}
	return n
}

func printCategories(w *os.File, order []prune.Category, m map[prune.Category][]prune.Orphan) {
	for _, cat := range order {
		items := m[cat]
		if len(items) == 0 {
			continue
		}
		names := make([]string, 0, len(items))
		for _, o := range items {
			names = append(names, o.Name)
		}
		sort.Strings(names)
		fmt.Fprintf(w, "  %s (%d):\n", cat, len(items))
		for _, n := range names {
			fmt.Fprintf(w, "    %s\n", n)
		}
	}
}
