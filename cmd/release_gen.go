package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/core/tui/theme"
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
// changelog` flag globals so the two commands stay independent). These are
// only read once, at the top of runReleaseGen, to build the run's genOptions;
// everything below the command entry point works off the options value so the
// per-repo pipeline is re-entrant (the TUI's inline regen constructs its own
// genOptions instead of mutating shared state).
var (
	genRepos       []string
	genSections    []string
	genSkipDocs    []string
	genDiff        string
	genCacheTTL    string
	genModel       string
	genConcurrency int
	genRepoTimeout time.Duration
	genRetries     int
	genRetryFailed bool
	genDryRun      bool
)

// Retry pacing for transient gen failures (shape mirrors
// release.WaitForModuleAvailabilityWithConfig: exponential backoff with a cap,
// plus jitter here since several workers may fail in the same rate-limit
// window).
const (
	genRetryInitialBackoff = 5 * time.Second
	genRetryMaxBackoff     = 60 * time.Second
)

// genOptions is the resolved, immutable configuration for one gen invocation
// (headless run or a single TUI inline regen). Passing it explicitly — rather
// than reading the flag globals — is what makes genOneRepo safe to run for
// several repos concurrently.
type genOptions struct {
	Repos    []string // --repo subset (empty ⇒ all selected repos in the plan)
	Sections []string // --sections scope for docgen -s (empty ⇒ all sections)
	SkipDocs []string // --skip-docs: force changelog-only for these repos
	Model    string   // --model override for docs + changelog
	Diff     string   // --diff override for the changelog git context
	CacheTTL string   // --cache-ttl override for the shared prefix

	// RetryFailed narrows the resolved repo set to repos whose last gen run
	// recorded a GenError — the one-command post-mortem re-run.
	RetryFailed bool

	// Out receives everything a single repo's pipeline emits: gen's own
	// progress lines plus the streamed docgen/cx subprocess output. The
	// headless path hands each repo a private buffer (flushed as one block by
	// the collector); the TUI passes io.Discard so nothing corrupts the
	// alt-screen.
	Out io.Writer
}

// logf writes one per-repo progress line to the options' output sink. Worker-
// side code must use this instead of displayInfo/ulog, which write straight to
// the terminal and would interleave across concurrent repos.
func (o genOptions) logf(format string, a ...any) {
	fmt.Fprintf(o.Out, format+"\n", a...)
}

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

Repos with no docgen config are auto-detected and carried through for versioning
+ changelog only (they bypass the freeze-verify and docgen stages and go straight
to the changelog rider) — so an ecosystem-wide run no longer dies on the first
unconfigured repo. Use --skip-docs to force that same changelog-only treatment
for a configured repo (e.g. one with unresolved doc sections).

Examples:
  grove release gen                                   # all planned repos
  grove release gen --repo flow --model claude-haiku-4-5
  grove release gen --repo flow --sections 03-workflows   # scoped idempotent re-run
  grove release gen --skip-docs cloud,sync,memory --model claude-haiku-4-5  # changelog-only for config-less repos`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReleaseGen(context.Background())
		},
	}

	cmd.Flags().StringSliceVar(&genRepos, "repo", nil, "Only generate for these repos (default: all selected repos in the plan)")
	cmd.Flags().StringSliceVar(&genSections, "sections", nil, "Only generate these docgen sections (scopes docgen -s; changelog still runs)")
	cmd.Flags().StringSliceVar(&genSkipDocs, "skip-docs", nil, "Force changelog-only for these repos (still stage a changelog): use for configured repos with unresolved sections; configless repos are auto-skipped")
	cmd.Flags().StringVar(&genDiff, "diff", "", "Changelog git diff depth: none|stat|full (default: config/stat)")
	cmd.Flags().StringVar(&genCacheTTL, "cache-ttl", "", "Shared-prefix cache TTL: 5m (default) or 1h")
	cmd.Flags().StringVar(&genModel, "model", "", "Model for docs + changelog; a claude-* model enables the shared cache fan-out")
	cmd.Flags().IntVar(&genConcurrency, "concurrency", 3, "Repos generated in parallel; docgen generates sections sequentially within a repo, so total in-flight LLM requests ≈ this value")
	cmd.Flags().DurationVar(&genRepoTimeout, "repo-timeout", 15*time.Minute, "Per-repo timeout for one gen attempt (cancellation propagates to the cx/docgen subprocesses; the in-process changelog request is not interrupted)")
	cmd.Flags().IntVar(&genRetries, "retries", 2, "Retries per repo for transient failures (a retry within the cache TTL rides the prefix the failed attempt warmed)")
	cmd.Flags().BoolVar(&genRetryFailed, "retry-failed", false, "Only generate for repos whose last gen run recorded an error (composes with --repo)")
	cmd.Flags().BoolVar(&genDryRun, "dry-run", false, "Resolve repos, build + freeze-verify each cx context, and report what would run — no API spend")

	return cmd
}

// Pipeline stages recorded on a failed genRepoResult so the retry wrapper can
// scope the next attempt to the work that actually failed.
const (
	genStageResolve      = "resolve"
	genStageFreezeVerify = "freeze-verify"
	genStageDocgen       = "docgen"
	genStageChangelog    = "changelog"
)

// genRepoResult holds one repo's gen outcome for the summary table.
type genRepoResult struct {
	Repo             string
	Sections         string
	CacheWriteTokens int64
	CacheReadTokens  int64
	EstCostUSD       float64
	Status           string
	Err              error

	// failedStage identifies which pipeline stage produced Err ("" on
	// success); docsFailedSections carries the failed docgen sections from the
	// usage report (empty when docgen is too old to report them, in which case
	// a retry re-runs all sections).
	failedStage        string
	docsFailedSections []string
}

// genPermanentError marks failures that must never be retried: freeze-verify
// floor violations (the cost guard — retrying an empty context re-spends
// nothing but wastes a worker slot and confuses users) and unresolvable repo
// paths.
type genPermanentError struct{ err error }

func (e *genPermanentError) Error() string { return e.err.Error() }
func (e *genPermanentError) Unwrap() error { return e.err }

// genTransientErrorMarkers are the error-string fragments recognized as known
// transient (HTTP 429/5xx and overload from the Anthropic SDK, network
// hiccups, per-attempt timeouts). Matching on strings is unavoidable here:
// docgen failures cross an exec boundary and carry only a stderr tail. The
// markers only refine the retry log line ("transient" vs "unclassified") —
// unclassified errors retry too, since attempts are bounded and a retry
// within the cache TTL cache-reads the prefix the failed attempt warmed.
var genTransientErrorMarkers = []string{
	"429", "500", "502", "503", "529",
	"overloaded", "rate limit", "timeout", "deadline exceeded",
	"connection reset", "temporarily", "stream error",
}

// isRetryableGenError classifies a per-repo gen failure: permanent wrappers
// and user cancellation never retry; everything else does (see the marker
// comment above for why unclassified defaults to retry).
func isRetryableGenError(err error) bool {
	if err == nil {
		return false
	}
	var perm *genPermanentError
	if errors.As(err, &perm) {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	return true
}

// isKnownTransientGenError reports whether err matches a known-transient
// marker; used only to word the retry log line.
func isKnownTransientGenError(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, m := range genTransientErrorMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// genWorkerResult carries one repo's completed gen work from a worker to the
// collector: the mutated repo-plan copy to fold back into the shared plan, the
// summary result, and the repo's buffered log block.
type genWorkerResult struct {
	repo string
	plan release.RepoReleasePlan
	res  genRepoResult
	log  []byte
}

// runGenPool runs work for every repo across `concurrency` workers and funnels
// all events to the calling goroutine: onStart fires when a worker picks up a
// repo, onResult when it finishes. Because both callbacks run only on the
// caller's goroutine, the caller is the single writer for the shared plan and
// any progress state — no locks needed. Each worker hands work a private
// buffer; the buffered bytes come back on the result so the caller can flush
// each repo's output as one uninterleaved block. Workers stop picking up new
// repos once ctx is canceled (in-flight repos still finish and report), so the
// returned results may cover fewer repos than requested. concurrency=1 goes
// through this same path — there is no separate sequential branch.
func runGenPool(
	ctx context.Context,
	repos []string,
	concurrency int,
	work func(ctx context.Context, repo string, out io.Writer) genWorkerResult,
	onStart func(repo string),
	onResult func(genWorkerResult),
) []genWorkerResult {
	if concurrency < 1 {
		concurrency = 1
	}

	type poolEvent struct {
		started bool
		repo    string
		result  genWorkerResult
	}
	jobs := make(chan string)
	events := make(chan poolEvent)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for repo := range jobs {
				if ctx.Err() != nil {
					continue // canceled: drain the queue without starting new repos
				}
				events <- poolEvent{started: true, repo: repo}
				var buf bytes.Buffer
				r := work(ctx, repo, &buf)
				r.log = buf.Bytes()
				events <- poolEvent{repo: repo, result: r}
			}
		}()
	}
	go func() {
		for _, r := range repos {
			jobs <- r
		}
		close(jobs)
		wg.Wait()
		close(events)
	}()

	var results []genWorkerResult
	for ev := range events {
		if ev.started {
			if onStart != nil {
				onStart(ev.repo)
			}
			continue
		}
		if onResult != nil {
			onResult(ev.result)
		}
		results = append(results, ev.result)
	}
	return results
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

	// Snapshot the flag globals into the run's immutable options. Everything
	// below works off opts; the flag vars are not read again.
	opts := genOptions{
		Repos:       genRepos,
		Sections:    genSections,
		SkipDocs:    genSkipDocs,
		Model:       genModel,
		Diff:        genDiff,
		CacheTTL:    genCacheTTL,
		RetryFailed: genRetryFailed,
		Out:         os.Stdout,
	}

	// Resolve the repo set: explicit --repo subset, else every selected repo.
	repos, err := resolveGenRepos(plan, opts)
	if err != nil {
		return err
	}
	if genDryRun {
		return runGenDryRun(ctx, plan, repos, opts)
	}

	displayInfo(fmt.Sprintf("Generating for %d repo(s), concurrency %d: %s", len(repos), genConcurrency, strings.Join(repos, ", ")))

	// Worker: runs one repo's pipeline against a private copy of its repo plan
	// and a private log buffer. Workers never touch the shared plan — SavePlan
	// marshals the whole thing, so any shared mutation would race the
	// collector's per-repo saves.
	work := func(ctx context.Context, repoName string, out io.Writer) genWorkerResult {
		rp := *plan.Repos[repoName] // struct copy; plan map is read-only from workers
		rp.DocsSections = append([]string(nil), rp.DocsSections...)
		wopts := opts
		wopts.Out = out
		res := genOneRepoWithRetry(ctx, plan, repoName, &rp, wopts, genRetries, genRepoTimeout)
		return genWorkerResult{repo: repoName, plan: rp, res: res}
	}

	// Collector state: both callbacks run on this goroutine only (single
	// writer), so the counters, the shared plan, and SavePlan need no locking.
	total := len(repos)
	done, running := 0, 0
	onStart := func(repo string) {
		running++
		displayInfo(fmt.Sprintf("[%d/%d done, %d running] %s started", done, total, running, repo))
	}
	onResult := func(r genWorkerResult) {
		running--
		done++
		*plan.Repos[r.repo] = r.plan

		// Persist per-repo progress immediately so a mid-run failure still
		// leaves completed repos recorded in the plan.
		if saveErr := release.SavePlan(plan); saveErr != nil {
			displayWarning(fmt.Sprintf("failed to save plan after %s: %v", r.repo, saveErr))
		}

		// Flush the repo's buffered pipeline output as one delimited block.
		displaySection(fmt.Sprintf("%s  %s", r.repo, r.plan.NextVersion))
		if len(r.log) > 0 {
			_, _ = os.Stdout.Write(r.log)
		}
		if r.res.Err != nil {
			displayError(fmt.Sprintf("[%d/%d done, %d running] %s: %v", done, total, running, r.repo, r.res.Err))
		} else {
			displayInfo(fmt.Sprintf("[%d/%d done, %d running] %s %s ($%.4f)", done, total, running, r.repo, theme.IconSuccess, r.res.EstCostUSD))
		}
	}

	workerResults := runGenPool(ctx, repos, genConcurrency, work, onStart, onResult)

	// Results arrive completion-ordered; restore the plan-level order so the
	// summary is deterministic.
	orderIdx := make(map[string]int, len(repos))
	for i, r := range repos {
		orderIdx[r] = i
	}
	sort.Slice(workerResults, func(i, j int) bool {
		return orderIdx[workerResults[i].repo] < orderIdx[workerResults[j].repo]
	})

	var results []genRepoResult
	anyFailed := false
	for _, wr := range workerResults {
		results = append(results, wr.res)
		if wr.res.Err != nil {
			anyFailed = true
		}
	}
	if len(workerResults) < total {
		displayWarning(fmt.Sprintf("%d repo(s) were not generated (run canceled before they started)", total-len(workerResults)))
		anyFailed = true
	}

	printGenSummary(results)

	if anyFailed {
		return fmt.Errorf("release gen failed for one or more repos")
	}
	displaySuccess("Release docs + changelogs staged for review")
	displayInfo("Next: review with 'grove release tui', then 'grove release apply'")
	return nil
}

// runGenDryRun previews an ecosystem-wide gen without any API spend: per repo
// it resolves the path, determines docs vs changelog-only mode, lists the
// section scope from the notebook docgen config, and builds + freeze-verifies
// the cx context (local subprocess only — this is exactly the guard a real run
// pays before spending, so a clean dry run means a real run won't die on
// context floors). Repos run sequentially; the point is the report, not speed.
func runGenDryRun(ctx context.Context, plan *release.ReleasePlan, repos []string, opts genOptions) error {
	fanout := "config-default model"
	if opts.Model != "" {
		if anthropic.IsAnthropicModel(opts.Model) {
			fanout = fmt.Sprintf("%s (shared cache fan-out)", opts.Model)
		} else {
			fanout = fmt.Sprintf("%s (no shared prefix — not a claude-* model)", opts.Model)
		}
	}
	displayInfo(fmt.Sprintf("Dry run: %d repo(s), %s — building + freeze-verifying contexts, no API spend", len(repos), fanout))

	type dryRow struct {
		repo, mode, sections, ctxDesc, note string
		failed                              bool
	}
	var rows []dryRow
	for _, repoName := range repos {
		repoPlan := plan.Repos[repoName]
		row := dryRow{repo: repoName, sections: "(all)", ctxDesc: "-"}
		if len(opts.Sections) > 0 {
			row.sections = strings.Join(opts.Sections, ",")
		} else if secRows := collectDocsSections(plan.RootDir, repoName, repoPlan); len(secRows) > 0 {
			names := make([]string, 0, len(secRows))
			for _, s := range secRows {
				names = append(names, s.Name)
			}
			row.sections = strings.Join(names, ",")
		}

		repoPath, err := resolveGenRepoPath(plan, repoName)
		if err != nil {
			row.mode, row.note, row.failed = "-", err.Error(), true
			rows = append(rows, row)
			continue
		}

		skipDocs := repoInSet(repoName, opts.SkipDocs)
		autoSkip := false
		if !skipDocs && !repoHasDocgenConfig(repoPath) {
			skipDocs, autoSkip = true, true
		}
		switch {
		case autoSkip:
			row.mode, row.sections, row.note = "changelog-only", "-", "no docgen config (auto-skip)"
		case skipDocs:
			row.mode, row.sections, row.note = "changelog-only", "-", "--skip-docs"
		default:
			row.mode = "docs+changelog"
		}

		if err := freezeVerifyContext(ctx, repoPath, opts); err != nil {
			row.note, row.failed = err.Error(), true
		} else {
			files := anthropic.WorkDirContextFiles(repoPath)
			if totalBytes, vErr := verifyContextFileset(repoPath, files); vErr == nil {
				row.ctxDesc = fmt.Sprintf("%d file(s) / %d bytes", len(files), totalBytes)
			}
		}
		rows = append(rows, row)
	}

	fmt.Println()
	fmt.Println("Release gen dry run:")
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "REPO\tMODE\tSECTIONS\tCONTEXT\tNOTES")
	anyFailed := false
	for _, r := range rows {
		note := r.note
		if r.failed {
			anyFailed = true
			note = "FAILED: " + firstLine(note)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.repo, r.mode, r.sections, r.ctxDesc, note)
	}
	_ = w.Flush()

	if anyFailed {
		return fmt.Errorf("dry run found repos that would fail; fix them before spending")
	}
	displaySuccess("Dry run clean — a real run would generate for every listed repo")
	return nil
}

// resolveGenRepos returns the ordered repo set to generate for. An explicit
// --repo subset is validated against the plan; otherwise every Selected repo is
// used (repos with changes). The order follows the plan's release levels so
// output is stable.
func resolveGenRepos(plan *release.ReleasePlan, opts genOptions) ([]string, error) {
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

	var out []string
	if len(opts.Repos) > 0 {
		want := map[string]bool{}
		for _, r := range opts.Repos {
			if _, ok := plan.Repos[r]; !ok {
				return nil, fmt.Errorf("repo %q is not in the release plan", r)
			}
			want[r] = true
		}
		for _, r := range order {
			if want[r] {
				out = append(out, r)
			}
		}
	} else {
		for _, r := range order {
			if plan.Repos[r] != nil && plan.Repos[r].Selected {
				out = append(out, r)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("no selected repos in the plan; pass --repo to target repos explicitly")
		}
	}

	// --retry-failed narrows the selection to repos whose last gen run
	// recorded an error, making a post-mortem re-run one command.
	if opts.RetryFailed {
		var failed []string
		for _, r := range out {
			if plan.Repos[r] != nil && strings.TrimSpace(plan.Repos[r].GenError) != "" {
				failed = append(failed, r)
			}
		}
		if len(failed) == 0 {
			return nil, fmt.Errorf("--retry-failed: none of the %d resolved repo(s) has a recorded gen error", len(out))
		}
		out = failed
	}
	return out, nil
}

// genOneRepo runs the freeze-verify → docs → changelog pipeline for one repo and
// records its outcome into the plan. It never returns an error directly; the
// per-repo error is carried on the result and mirrored into repoPlan.GenError.
func genOneRepo(ctx context.Context, plan *release.ReleasePlan, repoName string, repoPlan *release.RepoReleasePlan, opts genOptions) genRepoResult {
	res := genRepoResult{Repo: repoName, Sections: "-", Status: "failed"}

	repoPath, err := resolveGenRepoPath(plan, repoName)
	if err != nil {
		res.Err = err
		res.failedStage = genStageResolve
		repoPlan.GenError = err.Error()
		return res
	}

	// Record the (opening-only) check command + status. We never execute it in
	// this effort; a configured command is still surfaced for later phases.
	repoPlan.CheckCommand = loadRepoCheckCommand(repoPath)
	repoPlan.CheckStatus = "skipped"

	// --skip-docs: repos with no docgen config (or unresolved doc sections) are
	// carried through the release for versioning + changelog only. Bypass the
	// freeze-verify and docgen stages entirely and go straight to the changelog
	// rider, rather than failing the repo on a docgen error.
	skipDocs := repoInSet(repoName, opts.SkipDocs)

	// Auto-skip: a repo with no docgen config would shell docgen only to
	// hard-fail on "failed to load docgen config: file does not exist". Detect
	// that up front and carry the repo changelog-only, so an ecosystem-wide
	// `grove release gen` doesn't die on the first unconfigured repo and the
	// user need not hand-enumerate every configless repo in --skip-docs.
	autoSkipDocs := false
	if !skipDocs && !repoHasDocgenConfig(repoPath) {
		skipDocs = true
		autoSkipDocs = true
	}

	var docsUsage *docgenUsageReport
	sections := "-"
	if skipDocs {
		if autoSkipDocs {
			opts.logf("%s: no docgen config; changelog only (auto-skip docs)", repoName)
		} else {
			opts.logf("%s: --skip-docs set; changelog only (no docs generated)", repoName)
		}
	} else {
		// (a) Context freeze-verify — guard before any API spend.
		if err := freezeVerifyContext(ctx, repoPath, opts); err != nil {
			res.Err = err
			res.failedStage = genStageFreezeVerify
			repoPlan.GenError = err.Error()
			return res
		}

		// (b) Warm prefix + docs via docgen fan-out.
		var docErr error
		docsUsage, sections, docErr = runDocgenForRepo(ctx, repoPath, opts)
		if docErr != nil {
			res.Err = fmt.Errorf("docgen: %w", docErr)
			res.failedStage = genStageDocgen
			repoPlan.GenError = res.Err.Error()
			// A partial docgen run still spent tokens (the usage report is
			// written even on failure) and may name the failed sections —
			// surface both so the retry wrapper can account and scope.
			if docsUsage != nil {
				res.CacheWriteTokens = docsUsage.TotalCacheWriteTokens
				res.CacheReadTokens = docsUsage.TotalCacheReadTokens
				res.EstCostUSD = docsUsage.TotalEstCostUSD
				res.docsFailedSections = docsUsage.FailedSections
			}
			return res
		}
	}

	// (c) Changelog rider in-process — rides the prefix docgen just warmed.
	clUsage, staged, err := runChangelogRider(repoPath, repoName, repoPlan, opts)
	if err != nil {
		res.Err = fmt.Errorf("changelog: %w", err)
		res.failedStage = genStageChangelog
		repoPlan.GenError = res.Err.Error()
		repoPlan.ChangelogGenError = err.Error()
		// Docs succeeded; record that partial progress below before returning.
	} else {
		repoPlan.ChangelogGenError = ""
	}

	// (d) Record per-repo gen state.
	// A skip-docs pass (forced or auto) records nothing about docs rather than
	// clearing prior state: a changelog-only re-run must not erase the record
	// of docs a previous pass staged.
	if !skipDocs {
		repoPlan.DocsGenerated = true
		repoPlan.DocsGeneratedAt = time.Now()
		repoPlan.DocsSections = append([]string{}, opts.Sections...)
	}
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
		if skipDocs {
			res.Status = "changelog-only"
		} else {
			res.Status = "staged"
		}
	}
	return res
}

// genOneRepoWithRetry runs genOneRepo with bounded retries for transient
// failures, holding the worker's slot for the whole sequence so concurrency
// stays honest. Each attempt gets a fresh per-attempt timeout. Later attempts
// are scoped to the work that actually failed: a docgen failure whose usage
// report names the failed sections retries only those sections (falling back
// to a full re-run when the shelled docgen is too old to report them), and a
// changelog failure after staged docs retries changelog-only. Cache usage is
// accumulated across attempts — every attempt's spend is real spend — and the
// attempt count is recorded on the repo plan for the TUI.
func genOneRepoWithRetry(ctx context.Context, plan *release.ReleasePlan, repoName string, rp *release.RepoReleasePlan, opts genOptions, retries int, attemptTimeout time.Duration) genRepoResult {
	origSections := append([]string(nil), opts.Sections...)

	var totWrite, totRead int64
	var totCost float64
	docsDone := false   // docs staged by an earlier attempt this run
	docsSections := "-" // sections string from that successful docs attempt
	scopedRetry := false

	var res genRepoResult
	attempt := 0
	backoff := genRetryInitialBackoff
	for {
		attempt++
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		res = genOneRepo(attemptCtx, plan, repoName, rp, opts)
		cancel()

		totWrite += res.CacheWriteTokens
		totRead += res.CacheReadTokens
		totCost += res.EstCostUSD
		rp.GenAttempts = attempt

		if res.Err == nil || attempt > retries || !isRetryableGenError(res.Err) || ctx.Err() != nil {
			break
		}

		// Narrow the next attempt to the failed work.
		switch res.failedStage {
		case genStageDocgen:
			if len(res.docsFailedSections) > 0 {
				opts.Sections = append([]string(nil), res.docsFailedSections...)
				scopedRetry = true
				opts.logf("retry will regenerate only the failed section(s): %s", strings.Join(res.docsFailedSections, ", "))
			}
		case genStageChangelog:
			if !repoInSet(repoName, opts.SkipDocs) {
				docsDone = true
				docsSections = res.Sections
				opts.SkipDocs = append(append([]string(nil), opts.SkipDocs...), repoName)
				opts.logf("docs already staged; retry will regenerate the changelog only")
			}
		}

		kind := "unclassified"
		if isKnownTransientGenError(res.Err) {
			kind = "transient"
		}
		sleep := backoff + time.Duration(rand.Int63n(int64(backoff)/2+1))
		opts.logf("%s attempt %d/%d failed (%s error): %v — retrying in %s (a retry within the cache TTL rides the warmed prefix)",
			repoName, attempt, retries+1, kind, res.Err, sleep.Round(time.Second))
		select {
		case <-ctx.Done():
			return finalizeRetriedResult(res, rp, totWrite, totRead, totCost, docsDone, docsSections, scopedRetry, origSections)
		case <-time.After(sleep):
		}
		backoff *= 2
		if backoff > genRetryMaxBackoff {
			backoff = genRetryMaxBackoff
		}
	}
	return finalizeRetriedResult(res, rp, totWrite, totRead, totCost, docsDone, docsSections, scopedRetry, origSections)
}

// finalizeRetriedResult folds multi-attempt bookkeeping back into the final
// result and the repo-plan copy: accumulated usage totals, the run's original
// section scope (not a narrowed retry scope), and a "staged" status when docs
// from an earlier attempt plus a retried changelog together completed the repo.
func finalizeRetriedResult(res genRepoResult, rp *release.RepoReleasePlan, totWrite, totRead int64, totCost float64, docsDone bool, docsSections string, scopedRetry bool, origSections []string) genRepoResult {
	res.CacheWriteTokens, res.CacheReadTokens, res.EstCostUSD = totWrite, totRead, totCost
	rp.CacheWriteTokens, rp.CacheReadTokens, rp.GenEstCostUSD = totWrite, totRead, totCost
	if res.Err == nil {
		if docsDone {
			res.Status = "staged"
			res.Sections = docsSections
		}
		if scopedRetry || docsDone {
			rp.DocsSections = append([]string{}, origSections...)
		}
	}
	return res
}

// repoInSet reports whether repo appears in set (exact match).
func repoInSet(repo string, set []string) bool {
	for _, r := range set {
		if r == repo {
			return true
		}
	}
	return false
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
	return "", &genPermanentError{fmt.Errorf("could not locate repository %q under %s", repoName, plan.RootDir)}
}

// freezeVerifyContext builds the repo's cx context and asserts the resulting
// fileset clears the freeze-verify floors before any API spend. The cx
// subprocess streams into opts.Out (per-repo sink), not the process stderr.
func freezeVerifyContext(ctx context.Context, repoPath string, opts genOptions) error {
	cxCmd := delegation.CommandContext(ctx, "cx", "generate")
	cxCmd.Dir = repoPath
	cxCmd.Stdout = opts.Out
	cxCmd.Stderr = opts.Out
	if err := cxCmd.Run(); err != nil {
		return fmt.Errorf("freeze-verify: failed to build cx context: %w", err)
	}

	files := anthropic.WorkDirContextFiles(repoPath)
	totalBytes, err := verifyContextFileset(repoPath, files)
	if err != nil {
		return err
	}
	opts.logf("Context freeze-verify OK: %d file(s), %d bytes", len(files), totalBytes)
	return nil
}

// verifyContextFileset is the pure freeze-verify guard: it asserts the context
// fileset clears the file-count and total-byte floors, returning the measured
// total byte count on success. Factored out of freezeVerifyContext so the guard
// is unit-testable without cx or any API spend.
func verifyContextFileset(repoPath string, files []string) (int64, error) {
	// Floor violations are permanent: retrying an empty context cannot succeed
	// and must never burn retry attempts.
	if len(files) < genMinContextFiles {
		return 0, &genPermanentError{fmt.Errorf("freeze-verify: cx produced no context files in %s (expected >= %d) — refusing to spend on an empty prefix", repoPath, genMinContextFiles)}
	}
	var totalBytes int64
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			totalBytes += info.Size()
		}
	}
	if totalBytes < genMinContextBytes {
		return totalBytes, &genPermanentError{fmt.Errorf("freeze-verify: cx context is only %d bytes across %d file(s) in %s (floor %d) — refusing to spend on a near-empty prefix", totalBytes, len(files), repoPath, genMinContextBytes)}
	}
	return totalBytes, nil
}

// docgenConfigFileName mirrors docgen's config.ConfigFileName. Re-declared here
// so grove can probe for config presence without importing docgen's packages
// (the same decoupling rationale as docgenUsageReport below).
const docgenConfigFileName = "docgen.config.yml"

// repoHasDocgenConfig reports whether the repo at repoPath has a docgen config,
// resolved the same way docgen's config.LoadWithNotebook does: notebook-first
// (workspaces/<repo>/docgen/docgen.config.yml, via the shared core locator that
// docgen itself uses), then the legacy repo-local docs/ fallback. gen uses this
// to auto-skip docs for unconfigured repos rather than shelling docgen only to
// hit "failed to load docgen config: file does not exist".
func repoHasDocgenConfig(repoPath string) bool {
	// Notebook-first — the authoritative resolver docgen shares.
	if node, err := workspace.GetProjectByPath(repoPath); err == nil {
		if cfg, cfgErr := config.LoadDefault(); cfgErr == nil {
			locator := workspace.NewNotebookLocator(cfg)
			if docgenDir, dirErr := locator.GetDocgenDir(node); dirErr == nil {
				if _, statErr := os.Stat(filepath.Join(docgenDir, docgenConfigFileName)); statErr == nil {
					return true
				}
			}
		}
	}
	// Legacy repo-local fallback (docs/docgen.config.yml).
	if _, err := os.Stat(filepath.Join(repoPath, "docs", docgenConfigFileName)); err == nil {
		return true
	}
	return false
}

// runDocgenForRepo shells `docgen generate` in repoPath with gen's model/ttl/
// section scope, writing a machine-readable usage report it then parses and
// returns. docgen's live output is streamed so per-section usage is visible.
func runDocgenForRepo(ctx context.Context, repoPath string, opts genOptions) (*docgenUsageReport, string, error) {
	usageFile, err := os.CreateTemp("", "grove-gen-docgen-usage-*.json")
	if err != nil {
		return nil, "-", fmt.Errorf("creating usage report temp file: %w", err)
	}
	usagePath := usageFile.Name()
	_ = usageFile.Close()
	defer os.Remove(usagePath)

	args := []string{"generate", "--usage-json", usagePath}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.CacheTTL != "" {
		args = append(args, "--cache-ttl", opts.CacheTTL)
	}
	for _, s := range opts.Sections {
		args = append(args, "-s", s)
	}

	opts.logf("Running: docgen %s", strings.Join(args, " "))
	docCmd := delegation.CommandContext(ctx, "docgen", args...)
	docCmd.Dir = repoPath
	docCmd.Stdout = opts.Out
	docCmd.Stderr = opts.Out
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
	} else if len(opts.Sections) > 0 {
		sections = strings.Join(opts.Sections, ",")
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
	// FailedSections names the sections that failed, in a form docgen's -s
	// accepts; absent/empty from an older docgen binary (grove shells the
	// binary, so version skew is possible), in which case retries fall back to
	// a full re-run.
	FailedSections        []string `json:"failed_sections"`
	TotalInputTokens      int64    `json:"total_input_tokens"`
	TotalOutputTokens     int64    `json:"total_output_tokens"`
	TotalCacheWriteTokens int64    `json:"total_cache_write_tokens"`
	TotalCacheReadTokens  int64    `json:"total_cache_read_tokens"`
	TotalEstCostUSD       float64  `json:"total_est_cost_usd"`
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
func runChangelogRider(repoPath, repoName string, repoPlan *release.RepoReleasePlan, opts genOptions) (*anthropic.UsageResult, string, error) {
	settings := resolveChangelogSettings(repoPath, true)
	if opts.Model != "" {
		settings.Model = opts.Model
	}
	if opts.Diff != "" {
		settings.Diff = opts.Diff
	}
	if opts.CacheTTL != "" {
		settings.CacheTTL = opts.CacheTTL
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

	// Record the commit the changelog was generated at so the TUI's staleness
	// check covers gen-staged changelogs, not just TUI-generated ones.
	if head, headErr := gitHeadCommit(repoPath); headErr == nil {
		repoPlan.ChangelogCommit = head
	}

	// Refresh the plan's version suggestion from the fresh LLM analysis.
	if result.Suggestion != "" {
		repoPlan.SuggestedBump = result.Suggestion
		repoPlan.SuggestionReasoning = result.Justification
	}
	opts.logf("Staged changelog: %s (suggested bump: %s)", stagedPath, result.Suggestion)
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
