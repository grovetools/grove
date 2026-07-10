package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	proposeFresh    bool
	proposeFollowup string
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
'grove release gen', so the prefix and model match a later gen run. Each run is
staged under its own timestamped run dir, and a 'latest' symlink points at the
most recent successful live run:
  <state>/release/staging/<repo>/proposal/
    runs/<timestamp>/            one dir per run
      PROPOSAL.md                rationale + proposed outline
      proposed.docgen.config.yml a complete, valid config (current settings kept)
      prompts/<nn>-<name>.md     one draft prompt per prose section
      transcript.json            the exact turns, replayable by --followup
    latest -> runs/<timestamp>   the most recent successful live run

No release plan is required. Nothing is committed or pushed, and the live
notebook config/prompts are never overwritten.

Modes:
  --fresh     green-field: propose an outline from the code alone (the current
              sections/prompts/README are withheld). Excludes --followup.
  --followup  refine the latest proposal: replay its transcript and add the
              given feedback as a new turn. The model must match the prior
              run's: omit --model to reuse the model recorded in the prior
              run's transcript automatically; an explicit mismatching --model
              errors. Excludes --fresh.

Examples:
  grove release propose --repo flow --model claude-haiku-4-5
  grove release propose --repo flow --model claude-haiku-4-5 --cache-ttl 1h
  grove release propose --repo flow --dry-run   # assemble the request, no API spend
  grove release propose --repo flow --model claude-haiku-4-5 --fresh
  grove release propose --repo flow --followup "merge the CLI pages"   # model inferred from the prior run`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReleasePropose(context.Background())
		},
	}

	cmd.Flags().StringVar(&proposeRepo, "repo", "", "Repository to propose a docs outline for (required)")
	cmd.Flags().StringVar(&proposeModel, "model", "", "Claude model whose cache the proposal warms (must match a later gen); a claude-* model is required for the shared cache; with --followup, omit to reuse the prior run's model")
	cmd.Flags().StringVar(&proposeCacheTTL, "cache-ttl", "", "Shared-prefix cache TTL: 5m or 1h (docgen defaults propose to 1h — review outlasts 5m)")
	cmd.Flags().BoolVar(&proposeDryRun, "dry-run", false, "Assemble the request suffix without any API spend")
	cmd.Flags().BoolVar(&proposeFresh, "fresh", false, "Green-field: propose from the code alone (withhold the current sections/prompts/README); excludes --followup")
	cmd.Flags().StringVar(&proposeFollowup, "followup", "", "Reviewer feedback that refines the latest proposal in a second turn (replays its transcript; omit --model to reuse the prior run's model); excludes --fresh")

	_ = cmd.MarkFlagRequired("repo")

	return cmd
}

// runReleasePropose resolves the repo, freeze-verifies its cx context (the same
// pre-spend guard gen pays), computes the staging output dir, and shells
// `docgen propose` to warm the prefix + write the bundle.
func runReleasePropose(ctx context.Context) error {
	displayPhase("Proposing Docs Outline")

	if proposeFresh && strings.TrimSpace(proposeFollowup) != "" {
		return fmt.Errorf("--fresh and --followup are mutually exclusive: --fresh reframes a turn-0 proposal, --followup refines the latest one")
	}
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

	// Staging layout: the same staging root as changelogs, under proposal/.
	// 'grove release clear-plan' PRESERVES this proposal/ subtree by default
	// (it is drafting-loop run history awaiting review) and wipes it only with
	// --include-proposals. Each run gets its own timestamped dir under
	// proposal/runs/; a successful LIVE run repoints proposal/latest at it.
	stagingDir, err := getStagingDirPath()
	if err != nil {
		return fmt.Errorf("failed to resolve staging dir: %w", err)
	}
	proposalDir := filepath.Join(stagingDir, proposeRepo, "proposal")

	// A --followup replays the latest run's transcript, so resolve it up front
	// (before minting a new run dir) and fail clearly when there is no prior run.
	var transcriptPath string
	if strings.TrimSpace(proposeFollowup) != "" {
		latestDir, lerr := resolveProposeLatestDir(proposalDir)
		if lerr != nil {
			return lerr
		}
		transcriptPath = filepath.Join(latestDir, "transcript.json")
		if _, serr := os.Stat(transcriptPath); serr != nil {
			return fmt.Errorf("latest proposal run %s has no transcript.json to follow up on: %w", latestDir, serr)
		}
	}

	runLeaf, outputDir, err := newProposeRunDir(proposalDir, time.Now())
	if err != nil {
		return err
	}

	usagePath, usageCleanup, err := newProposeUsageTemp()
	if err != nil {
		return err
	}
	defer usageCleanup()

	args := buildProposeArgs(proposeArgsInput{
		outputDir:      outputDir,
		usagePath:      usagePath,
		model:          proposeModel,
		cacheTTL:       proposeCacheTTL,
		dryRun:         proposeDryRun,
		fresh:          proposeFresh,
		followup:       proposeFollowup,
		transcriptPath: transcriptPath,
	})

	displayInfo(fmt.Sprintf("Running: docgen %s (in %s)", strings.Join(args, " "), repoPath))
	docCmd := delegation.CommandContext(ctx, "docgen", args...)
	docCmd.Dir = repoPath
	docCmd.Stdout = os.Stdout
	docCmd.Stderr = os.Stderr
	if runErr := docCmd.Run(); runErr != nil {
		return fmt.Errorf("docgen propose failed: %w", runErr)
	}

	if proposeDryRun {
		// Dry runs get their own run dir but never repoint latest.
		displaySuccess(fmt.Sprintf("Dry run: proposal request assembled under %s (no API spend)", outputDir))
		return nil
	}

	// A successful live run becomes the new latest.
	if err := repointProposeLatest(proposalDir, runLeaf); err != nil {
		displayInfo(fmt.Sprintf("warning: could not repoint the 'latest' symlink: %v", err))
	}

	// Report where the bundle landed plus the request's cache usage.
	displaySuccess(fmt.Sprintf("Proposal bundle staged: %s (latest -> runs/%s)", outputDir, runLeaf))
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

// proposeArgsInput carries the fields buildProposeArgs threads into the
// `docgen propose` argv.
type proposeArgsInput struct {
	outputDir      string
	usagePath      string
	model          string
	cacheTTL       string
	dryRun         bool
	fresh          bool
	followup       string
	transcriptPath string
}

// buildProposeArgs assembles the `docgen propose` argv. It mirrors gen's flag
// conventions (runDocgenForRepo): --model and --cache-ttl are passed through only
// when set, so an unset --cache-ttl lets docgen apply its propose default (1h)
// and the model matches whatever a later gen run uses. --output-dir and
// --usage-json are always present; --fresh / --followup+--transcript are added
// only for those modes.
func buildProposeArgs(in proposeArgsInput) []string {
	args := []string{"propose", "--output-dir", in.outputDir, "--usage-json", in.usagePath}
	if in.model != "" {
		args = append(args, "--model", in.model)
	}
	if in.cacheTTL != "" {
		args = append(args, "--cache-ttl", in.cacheTTL)
	}
	if in.dryRun {
		args = append(args, "--dry-run")
	}
	if in.fresh {
		args = append(args, "--fresh")
	}
	if strings.TrimSpace(in.followup) != "" {
		args = append(args, "--followup", in.followup)
		if in.transcriptPath != "" {
			args = append(args, "--transcript", in.transcriptPath)
		}
	}
	return args
}

// newProposeRunDir mints a fresh, collision-free run dir under
// <proposalDir>/runs and returns its leaf name and absolute path. The leaf is
// the "20060102-150405" timestamp, with "-2", "-3", … appended when a dir of
// that name already exists (two runs within the same second). The runs parent
// is created; the run dir itself is created so a collision check is atomic.
func newProposeRunDir(proposalDir string, now time.Time) (leaf, dir string, err error) {
	runsDir := filepath.Join(proposalDir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil { //nolint:gosec // internal staging tree
		return "", "", fmt.Errorf("creating proposal runs dir: %w", err)
	}
	leaf = proposeRunDirName(now, func(candidate string) bool {
		_, statErr := os.Stat(filepath.Join(runsDir, candidate))
		return statErr == nil
	})
	dir = filepath.Join(runsDir, leaf)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // internal staging tree
		return "", "", fmt.Errorf("creating proposal run dir: %w", err)
	}
	return leaf, dir, nil
}

// proposeRunDirName returns a collision-free run-dir leaf name for now: the
// "20060102-150405" timestamp, with "-2", "-3", … suffixed while exists reports
// the candidate already present. exists is injected so the naming is
// unit-testable without a filesystem.
func proposeRunDirName(now time.Time, exists func(candidate string) bool) string {
	base := now.Format("20060102-150405")
	if !exists(base) {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !exists(candidate) {
			return candidate
		}
	}
}

// repointProposeLatest points <proposalDir>/latest at runs/<runLeaf> using a
// RELATIVE symlink target (so the staging tree stays relocatable), removing any
// existing link first. Only a successful live run calls this.
func repointProposeLatest(proposalDir, runLeaf string) error {
	link := filepath.Join(proposalDir, "latest")
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing old latest symlink: %w", err)
	}
	if err := os.Symlink(filepath.Join("runs", runLeaf), link); err != nil {
		return fmt.Errorf("creating latest symlink: %w", err)
	}
	return nil
}

// resolveProposeLatestDir returns the absolute run dir the <proposalDir>/latest
// symlink points at, or a clear error when no prior run exists.
func resolveProposeLatestDir(proposalDir string) (string, error) {
	link := filepath.Join(proposalDir, "latest")
	target, err := os.Readlink(link)
	if err != nil {
		return "", fmt.Errorf("no previous proposal run to follow up on (expected the 'latest' symlink at %s); run 'grove release propose' first: %w", link, err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(proposalDir, target)
	}
	return target, nil
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
