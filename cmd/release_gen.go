package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/util/delegation"
	"github.com/grovetools/grove-anthropic/pkg/anthropic"
	"github.com/grovetools/grove/pkg/release"
)

// Freeze-verify floors: the context fileset a repo builds before any API spend
// must clear these, or gen fails that repo loudly rather than fanning out over
// an empty/near-empty prefix (the freeze-verify lesson — a lint-clean rules file
// can still freeze nothing). A real repo's cx context is tens of KB across
// several files; these floors only reject the degenerate empty/near-empty case.
const (
	genMinContextFiles = 1
	genMinContextBytes = 500
)

// gen flag vars (scoped to `grove release gen`; do not reuse the `grove
// changelog` flag globals so the two commands stay independent).
var (
	genRepos    []string
	genSections []string
	genDiff     string
	genCacheTTL string
	genModel    string
)

// genDocgenStdout is where runDocgenForRepo streams `docgen generate` output.
// It defaults to os.Stdout for the headless CLI path; the release TUI redirects
// it to io.Discard during inline regen so docgen's streaming output does not
// corrupt the alt-screen. Only one gen/regen runs at a time (guarded by the
// TUI's `generating` flag), so mutating this global is safe.
var genDocgenStdout io.Writer = os.Stdout

// newReleaseGenCmd creates the 'grove release gen' subcommand: a fully headless
// batch that, per planned repo, warms one shared cx-context prefix and fans out
// the docgen section wave + the changelog rider against it, staging everything
// for review (notebook doc drafts + release staging) and recording per-repo gen
// status and cache usage into the release plan. No prompts, no commits, no push.
func newReleaseGenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gen",
		Short: "Headlessly generate docs + changelog for planned repos (staged for review)",
		Long: `Generate documentation sections and a changelog for each repository in the
release plan, staging everything for human review. Runs fully headless.

For every planned repo (or the --repo subset), gen:
  1. builds and freeze-verifies the repo's cx context (fails the repo loudly if
     the context fileset is empty or absurdly small — before any API spend);
  2. shells 'docgen generate' to fan out the doc sections over a shared,
     cache_control-breakpointed prompt prefix (warming the cache);
  3. runs the changelog rider in-process against a byte-identical prefix, so it
     cache-reads the context docgen just warmed instead of re-paying for it;
  4. records per-repo docs/changelog status + cache usage into the release plan;
  5. stages doc drafts (notebook) and the changelog (release staging).

Nothing is committed or pushed. Review with 'grove release tui' and publish with
'grove release apply'. Re-runs are idempotent and overwrite staging; a scoped
re-run (--repo X --sections a,b) within the cache TTL rides the warm prefix.

Pass a claude-* --model so docgen and the changelog share one prefix; that is
what makes the changelog cache-read the docs prefix.

Examples:
  grove release gen                                   # all planned repos
  grove release gen --repo flow --model claude-haiku-4-5
  grove release gen --repo flow --sections 03-workflows   # scoped idempotent re-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReleaseGen(context.Background())
		},
	}

	cmd.Flags().StringSliceVar(&genRepos, "repo", nil, "Only generate for these repos (default: all selected repos in the plan)")
	cmd.Flags().StringSliceVar(&genSections, "sections", nil, "Only generate these docgen sections (scopes docgen -s; changelog still runs)")
	cmd.Flags().StringVar(&genDiff, "diff", "", "Changelog git diff depth: none|stat|full (default: config/stat)")
	cmd.Flags().StringVar(&genCacheTTL, "cache-ttl", "", "Shared-prefix cache TTL: 5m (default) or 1h")
	cmd.Flags().StringVar(&genModel, "model", "", "Model for docs + changelog; a claude-* model enables the shared cache fan-out")

	return cmd
}

// genRepoResult holds one repo's gen outcome for the summary table.
type genRepoResult struct {
	Repo             string
	Sections         string
	CacheWriteTokens int64
	CacheReadTokens  int64
	EstCostUSD       float64
	Status           string
	Err              error
}

// runReleaseGen executes the headless docs+changelog generation over the plan.
func runReleaseGen(ctx context.Context) error {
	displayPhase("Generating Release Docs + Changelogs")

	plan, err := release.LoadPlan()
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no release plan found - run 'grove release plan' first")
		}
		return fmt.Errorf("failed to load release plan: %w", err)
	}

	if genModel != "" && !anthropic.IsAnthropicModel(genModel) {
		displayWarning(fmt.Sprintf("--model %q is not a claude-* model; docs+changelog will not share a cached prefix (no cache-read savings)", genModel))
	}

	// Resolve the repo set: explicit --repo subset, else every selected repo.
	repos, err := resolveGenRepos(plan)
	if err != nil {
		return err
	}
	displayInfo(fmt.Sprintf("Generating for %d repo(s): %s", len(repos), strings.Join(repos, ", ")))

	var results []genRepoResult
	anyFailed := false

	for _, repoName := range repos {
		repoPlan := plan.Repos[repoName]
		displaySection(fmt.Sprintf("%s  %s", repoName, repoPlan.NextVersion))

		res := genOneRepo(ctx, plan, repoName, repoPlan)
		results = append(results, res)
		if res.Err != nil {
			anyFailed = true
			displayError(fmt.Sprintf("%s: %v", repoName, res.Err))
		}

		// Persist per-repo progress immediately so a mid-run failure still leaves
		// completed repos recorded in the plan.
		if saveErr := release.SavePlan(plan); saveErr != nil {
			displayWarning(fmt.Sprintf("failed to save plan after %s: %v", repoName, saveErr))
		}
	}

	printGenSummary(results)

	if anyFailed {
		return fmt.Errorf("release gen failed for one or more repos")
	}
	displaySuccess("Release docs + changelogs staged for review")
	displayInfo("Next: review with 'grove release tui', then 'grove release apply'")
	return nil
}

// resolveGenRepos returns the ordered repo set to generate for. An explicit
// --repo subset is validated against the plan; otherwise every Selected repo is
// used (repos with changes). The order follows the plan's release levels so
// output is stable.
func resolveGenRepos(plan *release.ReleasePlan) ([]string, error) {
	// Flatten release levels into a stable order, then filter.
	var order []string
	seen := map[string]bool{}
	for _, level := range plan.ReleaseLevels {
		for _, r := range level {
			if !seen[r] {
				order = append(order, r)
				seen[r] = true
			}
		}
	}
	// Include any plan repos not present in release levels (defensive).
	var extras []string
	for r := range plan.Repos {
		if !seen[r] {
			extras = append(extras, r)
		}
	}
	sort.Strings(extras)
	order = append(order, extras...)

	if len(genRepos) > 0 {
		want := map[string]bool{}
		for _, r := range genRepos {
			if _, ok := plan.Repos[r]; !ok {
				return nil, fmt.Errorf("repo %q is not in the release plan", r)
			}
			want[r] = true
		}
		var out []string
		for _, r := range order {
			if want[r] {
				out = append(out, r)
			}
		}
		return out, nil
	}

	var out []string
	for _, r := range order {
		if plan.Repos[r] != nil && plan.Repos[r].Selected {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no selected repos in the plan; pass --repo to target repos explicitly")
	}
	return out, nil
}

// genOneRepo runs the freeze-verify → docs → changelog pipeline for one repo and
// records its outcome into the plan. It never returns an error directly; the
// per-repo error is carried on the result and mirrored into repoPlan.GenError.
func genOneRepo(ctx context.Context, plan *release.ReleasePlan, repoName string, repoPlan *release.RepoReleasePlan) genRepoResult {
	res := genRepoResult{Repo: repoName, Sections: "-", Status: "failed"}

	repoPath, err := resolveGenRepoPath(plan, repoName)
	if err != nil {
		res.Err = err
		repoPlan.GenError = err.Error()
		return res
	}

	// Record the (opening-only) check command + status. We never execute it in
	// this effort; a configured command is still surfaced for later phases.
	repoPlan.CheckCommand = loadRepoCheckCommand(repoPath)
	repoPlan.CheckStatus = "skipped"

	// (a) Context freeze-verify — guard before any API spend.
	if err := freezeVerifyContext(ctx, repoPath); err != nil {
		res.Err = err
		repoPlan.GenError = err.Error()
		return res
	}

	// (b) Warm prefix + docs via docgen fan-out.
	docsUsage, sections, err := runDocgenForRepo(ctx, repoPath)
	if err != nil {
		res.Err = fmt.Errorf("docgen: %w", err)
		repoPlan.GenError = res.Err.Error()
		return res
	}

	// (c) Changelog rider in-process — rides the prefix docgen just warmed.
	clUsage, staged, err := runChangelogRider(repoPath, repoName, repoPlan)
	if err != nil {
		res.Err = fmt.Errorf("changelog: %w", err)
		repoPlan.GenError = res.Err.Error()
		// Docs succeeded; record that partial progress below before returning.
	}

	// (d) Record per-repo gen state.
	repoPlan.DocsGenerated = true
	repoPlan.DocsGeneratedAt = time.Now()
	repoPlan.DocsSections = append([]string{}, genSections...)
	repoPlan.ChangelogStaged = staged != ""
	if staged != "" {
		repoPlan.ChangelogPath = staged
	}
	repoPlan.GenError = ""
	if res.Err != nil {
		repoPlan.GenError = res.Err.Error()
	}

	var cacheWrite, cacheRead int64
	var cost float64
	if docsUsage != nil {
		cacheWrite += docsUsage.TotalCacheWriteTokens
		cacheRead += docsUsage.TotalCacheReadTokens
		cost += docsUsage.TotalEstCostUSD
	}
	if clUsage != nil {
		cacheWrite += clUsage.CacheCreationTokens
		cacheRead += clUsage.CacheReadTokens
		cost += clUsage.EstimatedCostUSD
	}
	repoPlan.CacheWriteTokens = cacheWrite
	repoPlan.CacheReadTokens = cacheRead
	repoPlan.GenEstCostUSD = cost

	res.CacheWriteTokens = cacheWrite
	res.CacheReadTokens = cacheRead
	res.EstCostUSD = cost
	res.Sections = sections
	if res.Err == nil {
		res.Status = "staged"
	}
	return res
}

// resolveGenRepoPath resolves a repo name to its working directory, consistent
// with apply (plan.RootDir/<repo>), falling back to a broader search.
func resolveGenRepoPath(plan *release.ReleasePlan, repoName string) (string, error) {
	candidate := filepath.Join(plan.RootDir, repoName)
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate, nil
	}
	if p := findRepositoryPath(repoName); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("could not locate repository %q under %s", repoName, plan.RootDir)
}

// freezeVerifyContext builds the repo's cx context and asserts the resulting
// fileset clears the freeze-verify floors before any API spend.
func freezeVerifyContext(ctx context.Context, repoPath string) error {
	cxCmd := delegation.CommandContext(ctx, "cx", "generate")
	cxCmd.Dir = repoPath
	cxCmd.Stdout = os.Stderr
	cxCmd.Stderr = os.Stderr
	if err := cxCmd.Run(); err != nil {
		return fmt.Errorf("freeze-verify: failed to build cx context: %w", err)
	}

	files := anthropic.WorkDirContextFiles(repoPath)
	totalBytes, err := verifyContextFileset(repoPath, files)
	if err != nil {
		return err
	}
	displayInfo(fmt.Sprintf("Context freeze-verify OK: %d file(s), %d bytes", len(files), totalBytes))
	return nil
}

// verifyContextFileset is the pure freeze-verify guard: it asserts the context
// fileset clears the file-count and total-byte floors, returning the measured
// total byte count on success. Factored out of freezeVerifyContext so the guard
// is unit-testable without cx or any API spend.
func verifyContextFileset(repoPath string, files []string) (int64, error) {
	if len(files) < genMinContextFiles {
		return 0, fmt.Errorf("freeze-verify: cx produced no context files in %s (expected >= %d) — refusing to spend on an empty prefix", repoPath, genMinContextFiles)
	}
	var totalBytes int64
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			totalBytes += info.Size()
		}
	}
	if totalBytes < genMinContextBytes {
		return totalBytes, fmt.Errorf("freeze-verify: cx context is only %d bytes across %d file(s) in %s (floor %d) — refusing to spend on a near-empty prefix", totalBytes, len(files), repoPath, genMinContextBytes)
	}
	return totalBytes, nil
}

// runDocgenForRepo shells `docgen generate` in repoPath with gen's model/ttl/
// section scope, writing a machine-readable usage report it then parses and
// returns. docgen's live output is streamed so per-section usage is visible.
func runDocgenForRepo(ctx context.Context, repoPath string) (*docgenUsageReport, string, error) {
	usageFile, err := os.CreateTemp("", "grove-gen-docgen-usage-*.json")
	if err != nil {
		return nil, "-", fmt.Errorf("creating usage report temp file: %w", err)
	}
	usagePath := usageFile.Name()
	_ = usageFile.Close()
	defer os.Remove(usagePath)

	args := []string{"generate", "--usage-json", usagePath}
	if genModel != "" {
		args = append(args, "--model", genModel)
	}
	if genCacheTTL != "" {
		args = append(args, "--cache-ttl", genCacheTTL)
	}
	for _, s := range genSections {
		args = append(args, "-s", s)
	}

	displayInfo(fmt.Sprintf("Running: docgen %s", strings.Join(args, " ")))
	docCmd := delegation.CommandContext(ctx, "docgen", args...)
	docCmd.Dir = repoPath
	docCmd.Stdout = genDocgenStdout
	docCmd.Stderr = genDocgenStdout
	runErr := docCmd.Run()

	// Parse the usage report even if docgen returned an error — it is written in
	// a deferred pass so partial usage is still recorded.
	report := parseDocgenUsageReport(usagePath)
	sections := "-"
	if report != nil && len(report.Sections) > 0 {
		names := make([]string, 0, len(report.Sections))
		for _, s := range report.Sections {
			names = append(names, s.Section)
		}
		sections = strings.Join(names, ",")
	} else if len(genSections) > 0 {
		sections = strings.Join(genSections, ",")
	}

	if runErr != nil {
		return report, sections, fmt.Errorf("docgen generate failed: %w", runErr)
	}
	return report, sections, nil
}

// docgenUsageReport mirrors docgen's generator.UsageReport JSON shape (the
// --usage-json output). Defined locally to avoid importing docgen's generator
// package into grove.
type docgenUsageReport struct {
	Model    string `json:"model"`
	Sections []struct {
		Section string `json:"section"`
	} `json:"sections"`
	TotalInputTokens      int64   `json:"total_input_tokens"`
	TotalOutputTokens     int64   `json:"total_output_tokens"`
	TotalCacheWriteTokens int64   `json:"total_cache_write_tokens"`
	TotalCacheReadTokens  int64   `json:"total_cache_read_tokens"`
	TotalEstCostUSD       float64 `json:"total_est_cost_usd"`
}

func parseDocgenUsageReport(path string) *docgenUsageReport {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var r docgenUsageReport
	if err := json.Unmarshal(data, &r); err != nil {
		return nil
	}
	return &r
}

// runChangelogRider generates the changelog for one repo in-process against a
// byte-identical shared prefix (so it cache-reads the context docgen just
// warmed), stages it, and returns its cache usage and staged path. It uses the
// skipPrompt=true route so nothing is interactive.
func runChangelogRider(repoPath, repoName string, repoPlan *release.RepoReleasePlan) (*anthropic.UsageResult, string, error) {
	settings := resolveChangelogSettings(repoPath, true)
	if genModel != "" {
		settings.Model = genModel
	}
	if genDiff != "" {
		settings.Diff = genDiff
	}
	if genCacheTTL != "" {
		settings.CacheTTL = genCacheTTL
	}
	// Re-validate the (possibly gen-overridden) values.
	switch settings.Diff {
	case "none", "stat", "full":
	default:
		settings.Diff = changelogDefaultDiff
	}
	if settings.CacheTTL != anthropic.CacheTTL5m && settings.CacheTTL != anthropic.CacheTTL1h {
		settings.CacheTTL = changelogDefaultCacheTTL
	}

	gitContext, err := buildChangelogGitContext(repoPath, settings, true)
	if err != nil {
		return nil, "", err
	}

	result, usage, err := generateChangelogResultWithUsage(gitContext, repoPlan.NextVersion, repoPath, settings, true)
	if err != nil {
		return nil, "", err
	}

	stagedPath, err := stageChangelogNamed(repoName, result.Changelog)
	if err != nil {
		return usage, "", err
	}

	// Refresh the plan's version suggestion from the fresh LLM analysis.
	if result.Suggestion != "" {
		repoPlan.SuggestedBump = result.Suggestion
		repoPlan.SuggestionReasoning = result.Justification
	}
	displayInfo(fmt.Sprintf("Staged changelog: %s (suggested bump: %s)", stagedPath, result.Suggestion))
	return usage, stagedPath, nil
}

// loadRepoCheckCommand reads the optional [llm].check_command for a repo. This
// is recorded but never executed in this effort (opening only).
func loadRepoCheckCommand(repoPath string) string {
	coreCfg, err := config.LoadFrom(repoPath)
	if err != nil {
		return ""
	}
	var llmCfg LLMConfig
	if err := coreCfg.UnmarshalExtension("llm", &llmCfg); err != nil {
		return ""
	}
	return strings.TrimSpace(llmCfg.CheckCommand)
}

// printGenSummary prints the final per-repo summary table.
func printGenSummary(results []genRepoResult) {
	fmt.Println()
	fmt.Println("Release gen summary:")
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "REPO\tSECTIONS\tCACHE WRITE\tCACHE READ\tEST COST\tSTATUS")
	var totWrite, totRead int64
	var totCost float64
	for _, r := range results {
		status := r.Status
		if r.Err != nil {
			status = "FAILED"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t$%.4f\t%s\n", r.Repo, r.Sections, r.CacheWriteTokens, r.CacheReadTokens, r.EstCostUSD, status)
		totWrite += r.CacheWriteTokens
		totRead += r.CacheReadTokens
		totCost += r.EstCostUSD
	}
	fmt.Fprintf(w, "TOTAL\t\t%d\t%d\t$%.4f\t\n", totWrite, totRead, totCost)
	_ = w.Flush()
}
