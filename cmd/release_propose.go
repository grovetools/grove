package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/grovetools/core/util/delegation"
	"github.com/grovetools/grove-anthropic/pkg/anthropic"
	"github.com/grovetools/grove/pkg/release"
)

// propose flag vars (scoped to `grove release propose`).
var (
	proposeRepo     string
	proposeModel    string
	proposeCacheTTL string
	proposeDryRun   bool
)

// newReleaseProposeCmd creates the 'grove release propose' subcommand: a thin
// wrapper that warms the SAME cached cx-context prefix `grove release gen` uses
// and asks docgen for an updated docs outline (sections + prompts), staging a
// reviewable bundle under the release staging dir. It works standalone — no
// release plan is required — so a docs outline can be proposed before planning.
func newReleaseProposeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "propose",
		Short: "Propose an updated docs outline for a repo (staged for review; warms the gen cache)",
		Long: `Run a docs-regeneration "turn 0" for one repo: warm the same cached cx-context
prefix 'grove release gen' fans out over, send ONE request that proposes an
updated documentation outline, and stage a reviewable bundle under the release
staging dir.

The proposal rides the byte-identical shared prefix a later 'grove release gen'
warms, so after you review and edit the proposed prompts + config, a gen run for
the same repo and claude model (within the cache TTL) cache-READs the prefix this
proposal already warmed. Because review takes time, the default cache TTL is 1h.

This shells 'docgen propose' with the same model / cache-ttl conventions as
'grove release gen', so the prefix and model match a later gen run. The bundle is
written to:
  <state>/release/staging/<repo>/proposal/
    PROPOSAL.md                  rationale + proposed outline
    proposed.docgen.config.yml   a complete, valid config (current settings kept)
    prompts/<nn>-<name>.md       one draft prompt per prose section

No release plan is required. Nothing is committed or pushed, and the live
notebook config/prompts are never overwritten.

Examples:
  grove release propose --repo flow --model claude-haiku-4-5
  grove release propose --repo flow --model claude-haiku-4-5 --cache-ttl 1h
  grove release propose --repo flow --dry-run   # assemble the request, no API spend`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReleasePropose(context.Background())
		},
	}

	cmd.Flags().StringVar(&proposeRepo, "repo", "", "Repository to propose a docs outline for (required)")
	cmd.Flags().StringVar(&proposeModel, "model", "", "Claude model whose cache the proposal warms (must match a later gen); a claude-* model is required for the shared cache")
	cmd.Flags().StringVar(&proposeCacheTTL, "cache-ttl", "", "Shared-prefix cache TTL: 5m or 1h (docgen defaults propose to 1h — review outlasts 5m)")
	cmd.Flags().BoolVar(&proposeDryRun, "dry-run", false, "Assemble the request suffix without any API spend")

	_ = cmd.MarkFlagRequired("repo")

	return cmd
}

// runReleasePropose resolves the repo, freeze-verifies its cx context (the same
// pre-spend guard gen pays), computes the staging output dir, and shells
// `docgen propose` to warm the prefix + write the bundle.
func runReleasePropose(ctx context.Context) error {
	displayPhase("Proposing Docs Outline")

	if proposeModel != "" && !anthropic.IsAnthropicModel(proposeModel) {
		return fmt.Errorf("--model %q is not a claude-* model; propose requires a claude model so the outline warms the cache a later gen reads", proposeModel)
	}

	repoPath, err := resolveProposeRepoPath(proposeRepo)
	if err != nil {
		return err
	}

	// Reuse gen's exact resolved options shape for the freeze-verify so the
	// context we verify is the one docgen will build.
	opts := genOptions{
		Model:    proposeModel,
		CacheTTL: proposeCacheTTL,
		Out:      os.Stdout,
	}

	// Freeze-verify the context before any API spend — a broken preset or an
	// empty/near-empty context stops here rather than after a wasted request.
	if !proposeDryRun {
		if err := freezeVerifyContext(ctx, repoPath, opts); err != nil {
			return fmt.Errorf("freeze-verify: %w", err)
		}
	}

	// Output dir: the same staging root as changelogs, under a proposal/ subdir,
	// so 'grove release clear-plan' (which removes the whole staging tree) clears
	// it too.
	stagingDir, err := getStagingDirPath()
	if err != nil {
		return fmt.Errorf("failed to resolve staging dir: %w", err)
	}
	outputDir := filepath.Join(stagingDir, proposeRepo, "proposal")

	usagePath, usageCleanup, err := newProposeUsageTemp()
	if err != nil {
		return err
	}
	defer usageCleanup()

	args := buildProposeArgs(outputDir, usagePath, proposeModel, proposeCacheTTL, proposeDryRun)

	displayInfo(fmt.Sprintf("Running: docgen %s (in %s)", strings.Join(args, " "), repoPath))
	docCmd := delegation.CommandContext(ctx, "docgen", args...)
	docCmd.Dir = repoPath
	docCmd.Stdout = os.Stdout
	docCmd.Stderr = os.Stderr
	if runErr := docCmd.Run(); runErr != nil {
		return fmt.Errorf("docgen propose failed: %w", runErr)
	}

	if proposeDryRun {
		displaySuccess(fmt.Sprintf("Dry run: proposal request assembled under %s (no API spend)", outputDir))
		return nil
	}

	// Report where the bundle landed plus the request's cache usage.
	displaySuccess(fmt.Sprintf("Proposal bundle staged: %s", outputDir))
	if report := parseDocgenUsageReport(usagePath); report != nil {
		displayInfo(fmt.Sprintf("Cache usage: write=%d read=%d est_cost=$%.4f",
			report.TotalCacheWriteTokens, report.TotalCacheReadTokens, report.TotalEstCostUSD))
	}
	displayInfo("Review PROPOSAL.md, edit proposed.docgen.config.yml + prompts/, then apply the parts you accept to the notebook and run 'grove release gen' (same repo + model) to ride the warm cache")
	return nil
}

// resolveProposeRepoPath locates the repo working dir for propose. It works
// standalone: if a release plan exists it resolves through the plan (matching
// gen), otherwise it falls back to the sibling-directory search gen uses when a
// repo is not under the plan root.
func resolveProposeRepoPath(repoName string) (string, error) {
	if plan, err := release.LoadPlan(); err == nil && plan != nil {
		if p, perr := resolveGenRepoPath(plan, repoName); perr == nil {
			return p, nil
		}
	}
	if p := findRepositoryPath(repoName); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("could not locate repository %q (no release plan, and not found as a sibling directory); run from the ecosystem root", repoName)
}

// buildProposeArgs assembles the `docgen propose` argv. It mirrors gen's flag
// conventions (runDocgenForRepo): --model and --cache-ttl are passed through only
// when set, so an unset --cache-ttl lets docgen apply its propose default (1h)
// and the model matches whatever a later gen run uses. --output-dir and
// --usage-json are always present.
func buildProposeArgs(outputDir, usagePath, model, cacheTTL string, dryRun bool) []string {
	args := []string{"propose", "--output-dir", outputDir, "--usage-json", usagePath}
	if model != "" {
		args = append(args, "--model", model)
	}
	if cacheTTL != "" {
		args = append(args, "--cache-ttl", cacheTTL)
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	return args
}

// newProposeUsageTemp creates a temp file for docgen's --usage-json output and
// returns its path plus a cleanup func.
func newProposeUsageTemp() (string, func(), error) {
	f, err := os.CreateTemp("", "grove-propose-usage-*.json")
	if err != nil {
		return "", func() {}, fmt.Errorf("creating usage report temp file: %w", err)
	}
	path := f.Name()
	_ = f.Close()
	return path, func() { _ = os.Remove(path) }, nil
}
