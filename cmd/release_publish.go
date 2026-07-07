package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/core/util/delegation"
	"github.com/sirupsen/logrus"

	"github.com/grovetools/grove/pkg/release"
)

// publishRepo promotes a repo's reviewed docs + changelog into its working tree
// and pushes them as ONE commit, immediately before the release tag is created.
// It is the approval-gated counterpart to `grove release gen`: gen stages, apply
// publishes. Steps:
//
//  1. `docgen sync to-repo` + `docgen sync-readme` copy the production-filtered
//     docs and README from the notebook into the repo (best-effort — repos with
//     no docgen config are skipped with a log line, never fatal).
//  2. The approved staged CHANGELOG.md (release staging dir) is copied over the
//     repo's CHANGELOG.md.
//  3. Everything currently uncommitted — docs/, README.md, CHANGELOG.md, docgen
//     structured-output artifacts (e.g. pkg/docs/docs.json), plus any dep-bump
//     go.mod/go.sum not already committed — is staged with `git add -A` and
//     committed as one publish commit, then pushed.
//
// It returns pushed=true only when a commit was actually created and pushed; a
// worktree with nothing to publish is a silent no-op (pushed=false, nil). Dry
// runs are handled by the caller (publishRepo is only invoked when !dry-run).
func publishRepo(ctx context.Context, wsPath, repo, version string, plan *release.ReleasePlan, logger *logrus.Logger) (bool, error) {
	displayInfo(fmt.Sprintf("Publishing docs + changelog for %s...", repo))

	// (1) Sync docs from the notebook into the repo. Best-effort: a repo with no
	// docgen config (or docgen absent) must not fail the release.
	runDocgenSync(ctx, wsPath, repo, logger, "sync", "to-repo")
	runDocgenSync(ctx, wsPath, repo, logger, "sync-readme")

	// (2) Promote the approved staged changelog into the working tree.
	if err := promoteStagedChangelog(wsPath, repo); err != nil {
		// Non-fatal: a repo may legitimately have no staged changelog (docs-only
		// change). Log and continue to the commit step.
		logger.WithError(err).Warnf("No staged changelog promoted for %s", repo)
	}

	// (3) Stage everything and commit as one publish commit if anything changed.
	if err := executeGitCommand(ctx, wsPath, []string{"add", "-A"}, fmt.Sprintf("Stage publish artifacts for %s", repo), logger); err != nil {
		return false, err
	}

	// Nothing staged ⇒ nothing to publish. `git diff --cached --quiet` exits 0
	// when there are no staged changes.
	diffCmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--quiet")
	diffCmd.Dir = wsPath
	if err := diffCmd.Run(); err == nil {
		displayInfo(fmt.Sprintf("Nothing to publish for %s (docs + changelog unchanged)", repo))
		return false, nil
	}

	commitMsg := fmt.Sprintf("docs(release): publish docs + changelog for %s@%s", repo, version)
	if err := executeGitCommand(ctx, wsPath, []string{"commit", "-m", commitMsg}, fmt.Sprintf("Commit publish for %s", repo), logger); err != nil {
		return false, err
	}

	targetBranch := "main"
	if plan != nil && plan.Type == "rc" {
		targetBranch = "rc-nightly"
	}
	if err := executeGitCommand(ctx, wsPath, []string{"push", "origin", "HEAD:" + targetBranch}, fmt.Sprintf("Push publish for %s", repo), logger); err != nil {
		return false, err
	}

	displayComplete(fmt.Sprintf("Published docs + changelog for %s@%s", repo, version))
	return true, nil
}

// runDocgenSync shells one docgen sync invocation (`docgen sync to-repo` or
// `docgen sync-readme`) in the repo. It is best-effort: a missing docgen binary
// or a repo with no docgen config produces an informational skip, never an
// error — docs are optional per repo.
func runDocgenSync(ctx context.Context, wsPath, repo string, logger *logrus.Logger, args ...string) {
	label := strings.Join(args, " ")
	if releaseDryRun {
		displayInfo(fmt.Sprintf("%s [DRY RUN] docgen %s (%s)", theme.IconInfo, label, repo))
		return
	}
	cmd := delegation.CommandContext(ctx, "docgen", args...)
	cmd.Dir = wsPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Downgrade to an informational skip: no docgen config / no docgen binary
		// / nothing to sync are all normal for many repos.
		logger.WithError(err).Debugf("docgen %s skipped for %s: %s", label, repo, strings.TrimSpace(string(out)))
		displayInfo(fmt.Sprintf("docgen %s skipped for %s (no docgen config or nothing to sync)", label, repo))
		return
	}
	if trimmed := strings.TrimSpace(string(out)); trimmed != "" {
		logger.WithField("repo", repo).Debugf("docgen %s: %s", label, trimmed)
	}
	displayInfo(fmt.Sprintf("docgen %s complete for %s", label, repo))
}

// promoteStagedChangelog copies the approved staged CHANGELOG.md for repo into
// its working-tree CHANGELOG.md. It returns an error when no staged changelog
// exists (the caller treats that as non-fatal).
func promoteStagedChangelog(wsPath, repo string) error {
	stagingDir, err := getStagingDirPath()
	if err != nil {
		return err
	}
	staged := filepath.Join(stagingDir, repo, "CHANGELOG.md")
	data, err := os.ReadFile(staged)
	if err != nil {
		return fmt.Errorf("read staged changelog %s: %w", staged, err)
	}
	dest := filepath.Join(wsPath, "CHANGELOG.md")
	if err := os.WriteFile(dest, data, 0o644); err != nil { //nolint:gosec // G306: CHANGELOG is world-readable content
		return fmt.Errorf("write %s: %w", dest, err)
	}
	return nil
}
