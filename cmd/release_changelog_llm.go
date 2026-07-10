package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/logging"
	"github.com/grovetools/core/util/delegation"
	grovecontext "github.com/grovetools/cx/pkg/context"
	docgenconfig "github.com/grovetools/docgen/pkg/config"
	"github.com/grovetools/grove-anthropic/pkg/anthropic"
)

// Logger for changelog LLM operations
var changelogLog = logging.NewUnifiedLogger("grove-meta.changelog-llm")

// Changelog generation defaults. The diff toggle and token cap are exposed as
// `grove changelog` flags and grove.yml [llm] config; these are the built-in
// fallbacks when neither is set.
const (
	changelogDefaultDiff     = "stat" // none|stat|full
	changelogDefaultTokenCap = 60000  // est. tokens; full diff over this falls back to stat
	changelogDefaultCacheTTL = "5m"   // claude prefix cache TTL: 5m|1h
	changelogDefaultModel    = "gemini-2.5-flash"
	changelogTokenBytesRatio = 4 // ~4 chars/token estimate for the cap guard

	// changelogCtxBudgetBytes caps the whole-repo cx context the claude changelog
	// path uploads as its cached prefix. A changelog's real subject — the commit
	// log and diff — is already embedded in the task prompt, so the ambient repo
	// source is secondary; for large repos it blows Claude's 200k window ("prompt
	// is too long"). Above this budget we drop the source prefix and send the
	// changelog diff-only. 500 KB ≈ ~160k real tokens (Go source runs ~3.1 B/tok),
	// leaving ample headroom for the prompt tail under 200k. The largest repo that
	// generated cleanly (agentlogs, ~473 KB) stays under it; the smallest that
	// overflowed (hooks, ~623 KB) falls to diff-only.
	changelogCtxBudgetBytes = 500_000
)

// Flag-backed overrides for `grove changelog` (empty/zero ⇒ config, then default).
var (
	changelogDiffFlag     string
	changelogModelFlag    string
	changelogCacheTTLFlag string
	changelogTokenCapFlag int
)

// changelogSettings is the resolved changelog-generation configuration for one
// repo: flag > grove.yml [llm] > built-in default.
type changelogSettings struct {
	Model    string
	Diff     string // none|stat|full
	CacheTTL string // 5m|1h
	TokenCap int
}

// resolveChangelogSettings resolves changelog-gen settings for repoPath with
// flag > config > default precedence. quiet suppresses the "using model from
// config" notice (TUI mode).
func resolveChangelogSettings(repoPath string, quiet bool) changelogSettings {
	s := changelogSettings{
		Model:    changelogDefaultModel,
		Diff:     changelogDefaultDiff,
		CacheTTL: changelogDefaultCacheTTL,
		TokenCap: changelogDefaultTokenCap,
	}

	if coreCfg, err := config.LoadFrom(repoPath); err == nil {
		var llmCfg LLMConfig
		if err := coreCfg.UnmarshalExtension("llm", &llmCfg); err == nil {
			// changelog_model wins over default_model for changelog generation.
			if llmCfg.ChangelogModel != "" {
				s.Model = llmCfg.ChangelogModel
			} else if llmCfg.DefaultModel != "" {
				s.Model = llmCfg.DefaultModel
			}
			if llmCfg.ChangelogDiff != "" {
				s.Diff = llmCfg.ChangelogDiff
			}
			if llmCfg.ChangelogCacheTTL != "" {
				s.CacheTTL = llmCfg.ChangelogCacheTTL
			}
			if llmCfg.ChangelogTokenCap > 0 {
				s.TokenCap = llmCfg.ChangelogTokenCap
			}
		}
	}

	// Flags take final precedence.
	if changelogModelFlag != "" {
		s.Model = changelogModelFlag
	}
	if changelogDiffFlag != "" {
		s.Diff = changelogDiffFlag
	}
	if changelogCacheTTLFlag != "" {
		s.CacheTTL = changelogCacheTTLFlag
	}
	if changelogTokenCapFlag > 0 {
		s.TokenCap = changelogTokenCapFlag
	}

	switch s.Diff {
	case "none", "stat", "full":
	default:
		fmt.Printf("Warning: unknown --diff %q, using %q\n", s.Diff, changelogDefaultDiff)
		s.Diff = changelogDefaultDiff
	}
	if s.CacheTTL != anthropic.CacheTTL5m && s.CacheTTL != anthropic.CacheTTL1h {
		s.CacheTTL = changelogDefaultCacheTTL
	}
	if s.TokenCap <= 0 {
		s.TokenCap = changelogDefaultTokenCap
	}

	if !quiet && s.Model != changelogDefaultModel {
		fmt.Printf("Using changelog model: %s\n", s.Model)
	}
	return s
}

// LLMChangelogResult holds the structured response from the LLM.
type LLMChangelogResult struct {
	Suggestion    string `json:"suggestion"`    // "major", "minor", or "patch"
	Justification string `json:"justification"` // A brief reason for the suggestion
	Changelog     string `json:"changelog"`     // The full markdown changelog
}

// runLLMChangelog is the main entry point for LLM-based changelog generation.
//
// The generated changelog is written to the release STAGING directory
// (paths.StateDir()/release/staging/<repo>/CHANGELOG.md — see getStagingDirPath),
// never straight into the repository's working-tree CHANGELOG.md. Staging keeps generation and approval
// separate: nothing an LLM writes reaches a commit until a human reviews and
// applies it. The staged path is printed so a caller (or the TUI) can preview
// and, on approval, promote it.
func runLLMChangelog(repoPath, newVersion string) error {
	settings := resolveChangelogSettings(repoPath, false)

	gitContext, err := buildChangelogGitContext(repoPath, settings, false)
	if err != nil {
		return err
	}

	result, err := generateChangelogResult(gitContext, newVersion, repoPath, settings, false)
	if err != nil {
		return fmt.Errorf("failed to generate changelog with LLM: %w", err)
	}

	stagedPath, err := stageChangelogNamed(filepath.Base(filepath.Clean(repoPath)), result.Changelog)
	if err != nil {
		return err
	}

	fmt.Printf("Staged changelog for review: %s\n", stagedPath)
	fmt.Printf("Suggested version bump: %s (%s)\n", result.Suggestion, result.Justification)
	return nil
}

// stageGeneratedChangelog generates a changelog for repoPath (LLM when useLLM,
// else conventional-commit) and writes it to the release staging dir under
// repoName. It returns the staged path. It never touches the working tree or
// pushes; the reviewed apply path promotes the staged file on approval. Used by
// orchestrateRelease so a fresh changelog is staged-for-review rather than
// auto-committed.
func stageGeneratedChangelog(repoPath, repoName, newVersion string, useLLM bool) (string, error) {
	var content string
	if useLLM {
		settings := resolveChangelogSettings(repoPath, true)
		gitContext, err := buildChangelogGitContext(repoPath, settings, true)
		if err != nil {
			return "", err
		}
		result, err := generateChangelogResult(gitContext, newVersion, repoPath, settings, true)
		if err != nil {
			return "", err
		}
		content = result.Changelog
	} else {
		c, err := generateConventionalChangelogContent(repoPath, newVersion)
		if err != nil {
			return "", err
		}
		content = c
	}
	return stageChangelogNamed(repoName, content)
}

// stageChangelogNamed writes changelogContent to the release staging directory
// under repoName and returns the staged path. It does NOT touch any repo working
// tree; promotion into CHANGELOG.md happens only through the reviewed apply path.
func stageChangelogNamed(repoName, changelogContent string) (string, error) {
	stagingDir, err := getStagingDirPath()
	if err != nil {
		return "", fmt.Errorf("failed to resolve staging dir: %w", err)
	}
	stagedPath := filepath.Join(stagingDir, repoName, "CHANGELOG.md")
	if err := os.MkdirAll(filepath.Dir(stagedPath), 0o755); err != nil {
		return "", fmt.Errorf("failed to create staging dir: %w", err)
	}
	if err := os.WriteFile(stagedPath, []byte(changelogContent), 0o600); err != nil {
		return "", fmt.Errorf("failed to write staged changelog: %w", err)
	}
	return stagedPath, nil
}

// buildChangelogGitContext assembles the git material (log + optional diff) fed
// to the changelog LLM, honoring the diff-depth toggle and token-cap guard:
//   - none: git log only, no diff.
//   - stat: git log + `git diff --stat` (the historical default).
//   - full: git log + full `git diff`; if the diff's estimated token count
//     exceeds settings.TokenCap it auto-falls back to stat with a printed notice.
//
// This git material is always the volatile, per-request tail — for claude models
// it goes in the task prompt AFTER the cached cx-context prefix, never inside it.
func buildChangelogGitContext(repoPath string, settings changelogSettings, quiet bool) (string, error) {
	lastTag, err := getLastTag(repoPath)
	if err != nil {
		if !quiet {
			fmt.Printf("Warning: could not get last tag for %s, analyzing all commits: %v\n", repoPath, err)
		}
		lastTag = ""
	}
	commitRange := "HEAD"
	if lastTag != "" {
		commitRange = fmt.Sprintf("%s..HEAD", lastTag)
		if !quiet {
			fmt.Printf("Analyzing commits from %s to HEAD\n", lastTag)
		}
	} else if !quiet {
		fmt.Println("Analyzing all commits (no previous tag found)")
	}

	logCmd := exec.Command("git", "log", commitRange, "--pretty=format:commit %H (%h)%nAuthor: %an <%ae>%nDate: %ad%nCommit: %cn <%ce>%nCommitDate: %cd%n%n    %s%n%n%b%n")
	logCmd.Dir = repoPath
	logOutput, err := logCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get git log: %w\n%s", err, string(logOutput))
	}

	gitContext := fmt.Sprintf("GIT LOG:\n%s", string(logOutput))

	switch settings.Diff {
	case "none":
		// Log only.
	case "full":
		fullDiff, err := runGitDiff(repoPath, commitRange, false)
		if err != nil {
			return "", err
		}
		estTokens := len(fullDiff) / changelogTokenBytesRatio
		if estTokens > settings.TokenCap {
			statDiff, err := runGitDiff(repoPath, commitRange, true)
			if err != nil {
				return "", err
			}
			if !quiet {
				fmt.Printf("Notice: full diff ~%d tokens exceeds cap %d; falling back to --diff stat\n", estTokens, settings.TokenCap)
			}
			gitContext += fmt.Sprintf("\n\nGIT DIFF STAT:\n%s", statDiff)
		} else {
			gitContext += fmt.Sprintf("\n\nGIT DIFF:\n%s", fullDiff)
		}
	default: // "stat"
		statDiff, err := runGitDiff(repoPath, commitRange, true)
		if err != nil {
			return "", err
		}
		gitContext += fmt.Sprintf("\n\nGIT DIFF STAT:\n%s", statDiff)
	}

	return gitContext, nil
}

// runGitDiff returns the git diff for commitRange; stat=true yields --stat.
func runGitDiff(repoPath, commitRange string, stat bool) (string, error) {
	args := []string{"diff"}
	if stat {
		args = append(args, "--stat")
	}
	args = append(args, commitRange)
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get git diff: %w\n%s", err, string(out))
	}
	return string(out), nil
}

// totalFileBytes sums the on-disk sizes of the given files, skipping any that
// cannot be stat'd. Used to budget the changelog's cx-context prefix against
// Claude's context window before uploading it.
func totalFileBytes(files []string) int64 {
	var total int64
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			total += info.Size()
		}
	}
	return total
}

// gitHeadCommit returns the repo's current HEAD commit hash. Recorded next to
// a freshly staged changelog (RepoReleasePlan.ChangelogCommit) so the TUI's
// staleness check can flag changelogs generated before newer commits landed.
func gitHeadCommit(repoPath string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get current commit: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// getLastTag finds the most recent git tag in a repository.
func getLastTag(repoPath string) (string, error) {
	cmd := exec.Command("git", "describe", "--tags", "--abbrev=0")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// promptForRulesEdit checks if a .grove/rules file exists and asks the user if they want to edit it
// skipPrompt will skip the interactive prompt (useful for TUI mode)
func promptForRulesEdit(repoPath string, skipPrompt bool) error {
	mgr := grovecontext.NewManager(repoPath)

	rulesPath := mgr.ResolveRulesPath()

	// In TUI mode, just ensure the rules file exists but don't prompt
	if skipPrompt {
		_, err := os.Stat(rulesPath)
		if os.IsNotExist(err) {
			// Create .grove directory if it doesn't exist
			groveDir := filepath.Join(repoPath, ".grove")
			if err := os.MkdirAll(groveDir, 0o755); err != nil {
				return fmt.Errorf("failed to create .grove directory: %w", err)
			}

			// Create empty rules file with helpful comments
			content := `# Grove rules file for LLM context
# Add file paths or patterns here, one per line
# Examples:
#   README.md
#   docs/*.md
#   src/main.go
`
			if err := os.WriteFile(rulesPath, []byte(content), 0o600); err != nil {
				return fmt.Errorf("failed to create rules file: %w", err)
			}
			fmt.Printf("Created %s (edit to customize LLM context)\n", rulesPath)
		}
		return nil
	}

	// Check if rules file exists
	_, err := os.Stat(rulesPath)
	if os.IsNotExist(err) {
		// No rules file, ask if user wants to create one
		fmt.Printf("\nNo .grove/rules file found in %s\n", filepath.Base(repoPath))
		fmt.Print("Would you like to:\n")
		fmt.Print("  1) Auto-populate from files changed since last release\n")
		fmt.Print("  2) Create empty rules file and edit manually\n")
		fmt.Print("  3) Skip (no additional context)\n")
		fmt.Print("Choice [1/2/3]: ")

		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(response)

		switch response {
		case "1":
			// Auto-populate using cx from-git since
			if err := autoPopulateRules(repoPath); err != nil {
				fmt.Printf("Warning: Failed to auto-populate rules: %v\n", err)
				fmt.Print("Would you like to create and edit manually instead? (y/N): ")
				response, _ = reader.ReadString('\n')
				response = strings.TrimSpace(strings.ToLower(response))
				if response == "y" || response == "yes" {
					return createAndEditRules(repoPath)
				}
			} else {
				fmt.Print("Would you like to review/edit the auto-populated rules? (y/N): ")
				response, _ = reader.ReadString('\n')
				response = strings.TrimSpace(strings.ToLower(response))
				if response == "y" || response == "yes" {
					return openInEditor(rulesPath)
				}
			}
		case "2":
			return createAndEditRules(repoPath)
		case "3":
			// Skip
			return nil
		default:
			// Default to skip
			return nil
		}
	} else if err == nil {
		// Rules file exists, ask if user wants to edit it
		fmt.Printf("\nFound .grove/rules file in %s\n", filepath.Base(repoPath))
		fmt.Print("Would you like to:\n")
		fmt.Print("  1) Update from files changed since last release\n")
		fmt.Print("  2) Edit manually\n")
		fmt.Print("  3) Use as-is\n")
		fmt.Print("Choice [1/2/3]: ")

		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(response)

		switch response {
		case "1":
			// Update using cx from-git since
			if err := autoPopulateRules(repoPath); err != nil {
				fmt.Printf("Warning: Failed to update rules: %v\n", err)
			}
			fmt.Print("Would you like to review/edit the updated rules? (y/N): ")
			response, _ = reader.ReadString('\n')
			response = strings.TrimSpace(strings.ToLower(response))
			if response == "y" || response == "yes" {
				return openInEditor(rulesPath)
			}
		case "2":
			return openInEditor(rulesPath)
		case "3":
			// Use as-is
			return nil
		default:
			// Default to use as-is
			return nil
		}
	}

	return nil
}

// createAndEditRules creates a new rules file and opens it in editor
func createAndEditRules(repoPath string) error {
	mgr := grovecontext.NewManager(repoPath)
	rulesPath := mgr.ResolveRulesPath()

	// Create parent directory if it doesn't exist
	groveDir := filepath.Dir(rulesPath)
	if err := os.MkdirAll(groveDir, 0o755); err != nil {
		return fmt.Errorf("failed to create .grove directory: %w", err)
	}

	// Create rules file with helpful comments
	content := `# Grove rules file for LLM context
# Add file paths or patterns here, one per line
# Examples:
#   README.md
#   docs/*.md
#   src/main.go
`
	if err := os.WriteFile(rulesPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("failed to create rules file: %w", err)
	}

	fmt.Printf("Created %s\n", rulesPath)
	return openInEditor(rulesPath)
}

// autoPopulateRules uses cx from-git since to populate the rules file
func autoPopulateRules(repoPath string) error {
	// Get the last tag to use as the --since value
	lastTag, err := getLastTag(repoPath)
	if err != nil || lastTag == "" {
		return fmt.Errorf("no previous tag found to use as reference")
	}

	fmt.Printf("Auto-populating rules from files changed since %s...\n", lastTag)

	// Run 'grove cx from-git' for workspace-awareness
	cmd := delegation.Command("cx", "from-git", "--since", lastTag)
	cmd.Dir = repoPath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run grove cx from-git: %w", err)
	}

	fmt.Printf("Successfully populated rules file with changed files\n")
	return nil
}

// openInEditor opens a file in the user's preferred editor
func openInEditor(filepath string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim" // Default to vim if EDITOR is not set
	}

	cmd := exec.Command(editor, filepath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("Opening %s in %s...\n", filepath, editor)
	return cmd.Run()
}

// generateChangelogWithLLM constructs a prompt and generates a changelog.
// It returns the LLMChangelogResult struct. Callers (release plan, TUI) that
// already assembled gitContext use this and generateChangelogWithLLMInteractive;
// the resolved model/TTL come from flags/config/defaults.
func generateChangelogWithLLM(gitContext, newVersion, repoPath string) (*LLMChangelogResult, error) {
	return generateChangelogWithLLMInteractive(gitContext, newVersion, repoPath, false)
}

// generateChangelogWithLLMInteractive constructs a prompt and generates a changelog.
// skipPrompt controls whether to skip interactive prompts (useful for TUI mode).
func generateChangelogWithLLMInteractive(gitContext, newVersion, repoPath string, skipPrompt bool) (*LLMChangelogResult, error) {
	settings := resolveChangelogSettings(repoPath, skipPrompt)
	return generateChangelogResult(gitContext, newVersion, repoPath, settings, skipPrompt)
}

// generateChangelogResult is the changelog-generation core: it optionally
// refreshes the repo's LLM rules file, builds the JSON-response prompt, routes
// the request to the right backend, and parses + normalizes the result.
//
// Claude models (claude-*) go through the shared-prefix cache fan-out: the repo's
// cx context is built once and cached behind a single breakpoint, and the
// changelog request rides on that prefix with the git material as the volatile
// task tail (AFTER the breakpoint). This is byte-identical to the prefix docgen
// builds for the same repo, so a changelog co-scheduled with a docs wave (Phase
// 3) cache-reads the already-warmed context instead of re-paying for it. When no
// cx context can be built, or the claude path errors, generation falls back to
// the universal `grove llm request` facade. Gemini and other providers always
// use the facade — the historical path, unchanged.
func generateChangelogResult(gitContext, newVersion, repoPath string, settings changelogSettings, skipPrompt bool) (*LLMChangelogResult, error) {
	result, _, err := generateChangelogResultWithUsage(gitContext, newVersion, repoPath, settings, skipPrompt)
	return result, err
}

// generateChangelogResultWithUsage is generateChangelogResult plus the request's
// cache usage. The usage is non-nil only when the request went through the
// claude shared-prefix fan-out (the `grove llm` facade path does not surface
// token accounting in-process); callers that don't need usage use the wrapper
// above. `grove release gen` uses this to total the changelog's cache_read into
// the repo's gen wave (the changelog rides the prefix docgen just warmed).
func generateChangelogResultWithUsage(gitContext, newVersion, repoPath string, settings changelogSettings, skipPrompt bool) (*LLMChangelogResult, *anthropic.UsageResult, error) {
	// Prompt for rules file editing (best-effort; rules are optional context).
	if err := promptForRulesEdit(repoPath, skipPrompt); err != nil {
		fmt.Printf("Warning: Failed to handle rules file: %v\n", err)
	}

	currentDate := time.Now().Format("2006-01-02")
	prompt := buildChangelogPrompt(gitContext, newVersion, currentDate)

	var rawOutput string
	var usage *anthropic.UsageResult
	var err error
	if anthropic.IsAnthropicModel(settings.Model) {
		rawOutput, usage, err = callChangelogViaSharedPrefix(repoPath, prompt, settings, skipPrompt)
		if err != nil {
			// Claude path failed (e.g. no cx context) — fall back to the facade,
			// which also serves claude models via `grove llm request`.
			if !skipPrompt {
				fmt.Printf("Notice: cache fan-out changelog failed (%v); falling back to grove llm\n", err)
			}
			changelogLog.Warn("Shared-prefix changelog failed; falling back to grove llm").
				Err(err).Field("model", settings.Model).StructuredOnly().Log(context.Background())
			usage = nil
			rawOutput, err = callChangelogViaGroveLLM(repoPath, prompt, settings.Model, skipPrompt)
		}
	} else {
		rawOutput, err = callChangelogViaGroveLLM(repoPath, prompt, settings.Model, skipPrompt)
	}
	if err != nil {
		return nil, nil, err
	}

	result, err := parseChangelogResult(rawOutput, newVersion, currentDate)
	if err != nil {
		return nil, nil, err
	}
	return result, usage, nil
}

// buildChangelogPrompt assembles the JSON-response changelog prompt from the git
// material, version, and date. The git material is embedded as the task tail so
// it can ride after a cached context prefix for claude models.
func buildChangelogPrompt(gitContext, newVersion, currentDate string) string {
	return fmt.Sprintf(`You are a technical writer responsible for creating release notes and suggesting semantic version bumps.
Based on the provided git log and diff, analyze the changes and generate a JSON object with three fields: "suggestion", "justification", and "changelog".

**JSON Schema:**
{
  "suggestion": "major|minor|patch",
  "justification": "A brief, one-sentence explanation for the version bump suggestion.",
  "changelog": "The full changelog in Markdown format."
}

**Instructions for Analysis:**
- **major:** Suggest if there are breaking changes (e.g., commits with "!" like "feat!:", or "BREAKING CHANGE:" in the body).
- **minor:** Suggest if new features are added without breaking changes (e.g., "feat:" commits).
- **patch:** Suggest for bug fixes, performance improvements, or chores (e.g., "fix:", "perf:", "chore:").

**Instructions for Changelog:**
1. Format the changelog in Markdown using the "Keep a Changelog" standard.
2. Start with a level 2 heading for the version, including the current date: "## %s (%s)"
3. Begin with a summary section that organizes changes into topic-based paragraphs. Each paragraph should focus on a related set of changes (e.g., one for UI improvements, one for new commands, one for bug fixes). Include commit hash citations in parentheses when referencing specific changes.
   Example: "The new workspace list command (a1b2c3d) provides convenient access to all discovered workspaces, with support for JSON output (b2c3d4e) for scripting integration."
   Write one paragraph per major topic or theme. No emojis.
4. After the summary, categorize changes into sections (e.g., ### Features, ### Bug Fixes, ### BREAKING CHANGES). Omit empty sections. No emojis in section headers.
5. For each changelog entry, include the short commit hash (first 7 characters) directly at the end without brackets. Format: "Description of change (commit)"
   Example: "Add workspace list command with JSON output support (a1b2c3d)"
   GitHub will automatically convert these 7-character hashes into clickable links.
6. At the very end, include a "### File Changes" section with the provided git diff stat in a code block.
7. Do not include any preamble or explanation.
8. Do not use any emojis anywhere in the changelog.

**Context from Git:**
---
%s
---

Generate the JSON object now:`, newVersion, currentDate, gitContext)
}

// callChangelogViaSharedPrefix issues the changelog request as a rider on the
// repo's shared cx-context prefix (claude models only). It builds the cx context
// once, uploads it behind a single cache breakpoint byte-identical to docgen's
// prefix, then fires the request with the changelog prompt as the volatile tail.
// Returns the raw model text.
func callChangelogViaSharedPrefix(repoPath, prompt string, settings changelogSettings, quiet bool) (string, *anthropic.UsageResult, error) {
	// Resolve the same explicit artifact used by docgen and freeze verification.
	// This preserves byte-identical shared-prefix contents by construction.
	rulesPath, err := docgenconfig.ResolveDocsRulesFile(repoPath)
	if err != nil {
		return "", nil, fmt.Errorf("resolve docgen rules: %w", err)
	}
	cxArgs := []string{"generate"}
	if rulesPath != "" {
		cxArgs = append(cxArgs, "--rules-file", rulesPath)
	}
	cxCmd := delegation.Command("cx", cxArgs...)
	cxCmd.Dir = repoPath
	cxCmd.Stdout = io.Discard
	cxCmd.Stderr = io.Discard
	if err := cxCmd.Run(); err != nil {
		return "", nil, fmt.Errorf("failed to build cx context: %w", err)
	}

	ctxFiles := anthropic.WorkDirContextFiles(repoPath)
	if len(ctxFiles) == 0 {
		return "", nil, fmt.Errorf("cx produced no context in %s", repoPath)
	}

	// Scope the context to Claude's window. The whole-repo cx context is uploaded
	// as the cached prefix, but the changelog's real subject (git log + diff) is
	// already in the task prompt, so for large repos the source prefix is both
	// redundant and fatal — it overflows the 200k limit ("prompt is too long").
	// Above the budget, drop the source and anchor the prefix on a tiny note so
	// the request is effectively diff-only and always fits.
	var (
		prefix *anthropic.SharedPrefix
		err    error
	)
	if ctxBytes := totalFileBytes(ctxFiles); ctxBytes > changelogCtxBudgetBytes {
		if !quiet {
			fmt.Printf("Changelog context %d bytes exceeds %d budget for %s; sending diff-only (git log + diff carry the changelog)\n",
				ctxBytes, changelogCtxBudgetBytes, filepath.Base(repoPath))
		}
		changelogLog.Info("Changelog context over budget; diff-only").
			Field("repo", filepath.Base(repoPath)).Field("ctx_bytes", ctxBytes).
			Field("budget_bytes", changelogCtxBudgetBytes).
			StructuredOnly().Log(context.Background())
		note := fmt.Sprintf("Repository: %s\n(The commit log and diff for this changelog are provided in the task prompt.)\n", filepath.Base(repoPath))
		prefix, err = anthropic.NewSharedPrefix("", []byte(note), anthropic.SharedPrefixOptions{
			Model:     settings.Model,
			TTL:       settings.CacheTTL,
			MaxTokens: 8192,
			Caller:    "grove-changelog",
		})
	} else {
		prefix, err = anthropic.NewSharedPrefixFromFiles("", ctxFiles, anthropic.SharedPrefixOptions{
			Model:     settings.Model,
			TTL:       settings.CacheTTL,
			MaxTokens: 8192,
			Caller:    "grove-changelog",
		})
	}
	if err != nil {
		return "", nil, fmt.Errorf("failed to set up shared prefix: %w", err)
	}
	defer func() { _ = prefix.Close() }()

	if !quiet {
		fmt.Printf("Generating changelog via cache fan-out: model=%s ttl=%s prefix_docs=%d\n", prefix.Model(), settings.CacheTTL, len(ctxFiles))
	}

	text, usage, err := prefix.Request(context.Background(), prompt)
	if err != nil {
		return "", nil, fmt.Errorf("cache fan-out changelog request failed: %w", err)
	}
	logChangelogCacheUsage(usage, quiet)
	return text, usage, nil
}

// logChangelogCacheUsage prints and structurally logs the changelog request's
// cache write/read accounting so caching is verifiable from the CLI.
func logChangelogCacheUsage(u *anthropic.UsageResult, quiet bool) {
	if u == nil {
		return
	}
	if !quiet {
		fmt.Printf("Cache usage [changelog]: model=%s input=%d output=%d cache_write=%d (5m=%d 1h=%d) cache_read=%d est_cost=$%.4f\n",
			u.Model, u.InputTokens, u.OutputTokens, u.CacheCreationTokens, u.CacheWrite5m, u.CacheWrite1h, u.CacheReadTokens, u.EstimatedCostUSD)
	}
	changelogLog.Info("Changelog cache usage").
		Field("model", u.Model).
		Field("input", u.InputTokens).
		Field("output", u.OutputTokens).
		Field("cache_write", u.CacheCreationTokens).
		Field("cache_read", u.CacheReadTokens).
		Field("est_cost_usd", u.EstimatedCostUSD).
		StructuredOnly().
		Log(context.Background())
}

// callChangelogViaGroveLLM runs the changelog prompt through the `grove llm
// request` facade (the historical path; serves gemini and other providers, and
// is the claude fallback). Returns the raw model text.
func callChangelogViaGroveLLM(repoPath, prompt, model string, skipPrompt bool) (string, error) {
	// Write prompt to a temporary file
	tmpFile, err := os.CreateTemp("", "grove-changelog-prompt-*.md")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary prompt file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(prompt); err != nil {
		return "", fmt.Errorf("failed to write to temporary prompt file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return "", fmt.Errorf("failed to close temporary prompt file: %w", err)
	}

	// Create a temp file for the LLM response output
	outputFile, err := os.CreateTemp("", "grove-changelog-response-*.json")
	if err != nil {
		return "", fmt.Errorf("failed to create output file: %w", err)
	}
	outputPath := outputFile.Name()
	outputFile.Close()
	defer os.Remove(outputPath)

	// Use --output for a clean response and --max-output-tokens for full changelogs.
	args := []string{
		"llm",
		"request",
		"--model", model,
		"--file", tmpFile.Name(),
		"--output", outputPath,
		"--max-output-tokens", "8192",
		"--yes",
	}

	// Create a log file for provider console output
	logFile, err := os.CreateTemp("", "grove-llm-changelog-*.log")
	if err != nil {
		return "", fmt.Errorf("failed to create log file: %w", err)
	}
	logPath := logFile.Name()
	defer logFile.Close()

	if !skipPrompt {
		fmt.Printf("Calling grove llm with model %s for changelog + version suggestion...\n", model)
		fmt.Printf("Logging output to: %s\n", logPath)
	}

	ctx := context.Background()
	changelogLog.Debug("Calling grove llm").
		Field("model", model).
		Field("repo_path", repoPath).
		Field("prompt_file", tmpFile.Name()).
		Field("output_file", outputPath).
		Field("full_command", "llm "+strings.Join(args[1:], " ")).
		Field("args", args).
		StructuredOnly().
		Log(ctx)

	llmCmd := delegation.Command(args[0], args[1:]...)
	llmCmd.Dir = repoPath // Set working directory to the repository being analyzed

	// Redirect both stdout and stderr to log file to prevent TUI mangling
	llmCmd.Stdout = logFile
	llmCmd.Stderr = logFile

	err = llmCmd.Run()

	changelogLog.Debug("grove llm command completed").
		Field("has_error", err != nil).
		StructuredOnly().
		Log(ctx)

	if err != nil {
		logContent, _ := os.ReadFile(logPath)
		changelogLog.Error("grove llm request failed").
			Err(err).
			Field("log_path", logPath).
			Field("log_content", string(logContent)).
			StructuredOnly().
			Log(ctx)
		return "", fmt.Errorf("failed to execute 'grove llm request': %w\nLog: %s\nOutput: %s", err, logPath, string(logContent))
	}

	output, err := os.ReadFile(outputPath)
	if err != nil {
		return "", fmt.Errorf("failed to read LLM response from output file: %w", err)
	}

	changelogLog.Debug("Read LLM response from file").
		Field("output_len", len(output)).
		StructuredOnly().
		Log(ctx)

	return string(output), nil
}

// parseChangelogResult extracts the JSON object from raw model output, unmarshals
// it, normalizes trailing newline, and enforces the correct version header
// (LLMs frequently hallucinate the version from git history).
func parseChangelogResult(rawOutput, newVersion, currentDate string) (*LLMChangelogResult, error) {
	ctx := context.Background()

	// LLMs sometimes wrap JSON in ```json ... ``` blocks or add console styling.
	jsonString := extractJSONFromOutput(rawOutput)

	previewLen := len(jsonString)
	if previewLen > 200 {
		previewLen = 200
	}
	changelogLog.Debug("Cleaned JSON string").
		Field("json_len", len(jsonString)).
		Field("json_preview", jsonString[:previewLen]).
		StructuredOnly().
		Log(ctx)

	var result LLMChangelogResult
	if err := json.Unmarshal([]byte(jsonString), &result); err != nil {
		changelogLog.Error("Failed to unmarshal JSON").
			Err(err).
			Field("json_string", jsonString).
			StructuredOnly().
			Log(ctx)
		return nil, fmt.Errorf("failed to unmarshal LLM response into JSON: %w\nResponse:\n%s", err, rawOutput)
	}

	changelogLog.Info("Successfully parsed LLM response").
		Field("suggestion", result.Suggestion).
		Field("justification_len", len(result.Justification)).
		Field("changelog_len", len(result.Changelog)).
		StructuredOnly().
		Log(ctx)

	if result.Changelog != "" && !strings.HasSuffix(result.Changelog, "\n") {
		result.Changelog = result.Changelog + "\n"
	}

	result.Changelog = fixChangelogHeader(result.Changelog, newVersion, currentDate)
	return &result, nil
}

// fixChangelogHeader ensures the changelog has the correct version and date in the header.
// LLMs frequently ignore explicit instructions and hallucinate version numbers from
// git history, so we deterministically fix the header after generation.
func fixChangelogHeader(changelog, version, date string) string {
	if changelog == "" {
		return changelog
	}

	lines := strings.SplitN(changelog, "\n", 2)
	if len(lines) == 0 {
		return changelog
	}

	// Check if first line is a version header (## v...)
	firstLine := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(firstLine, "## ") {
		// No header found, prepend the correct one
		correctHeader := fmt.Sprintf("## %s (%s)\n\n", version, date)
		return correctHeader + changelog
	}

	// Replace the existing header with the correct one
	correctHeader := fmt.Sprintf("## %s (%s)", version, date)
	if len(lines) == 2 {
		return correctHeader + "\n" + lines[1]
	}
	return correctHeader + "\n"
}

// extractJSONFromOutput extracts a JSON object from LLM output that may contain
// console styling, markdown code blocks, or other surrounding content.
// Handles nested code blocks within the JSON content.
func extractJSONFromOutput(output string) string {
	output = strings.TrimSpace(output)

	// Try to find JSON within ```json ... ``` code blocks first
	// The JSON content may contain nested ``` blocks (e.g., in changelog markdown),
	// so we need to find the closing ``` that's at the start of a line after the JSON object closes.
	if idx := strings.Index(output, "```json"); idx != -1 {
		start := idx + len("```json")
		rest := output[start:]

		// Look for the closing ``` by finding where the JSON object ends (})
		// and then finding the next ``` after that
		jsonStart := strings.Index(rest, "{")
		if jsonStart != -1 {
			// Find the end of the JSON object by tracking brace depth
			depth := 0
			inString := false
			escape := false
			jsonEnd := -1

			for i := jsonStart; i < len(rest); i++ {
				c := rest[i]
				if escape {
					escape = false
					continue
				}
				if c == '\\' && inString {
					escape = true
					continue
				}
				if c == '"' {
					inString = !inString
					continue
				}
				if inString {
					continue
				}
				if c == '{' {
					depth++
				} else if c == '}' {
					depth--
					if depth == 0 {
						jsonEnd = i + 1
						break
					}
				}
			}

			if jsonEnd != -1 {
				return strings.TrimSpace(rest[jsonStart:jsonEnd])
			}
		}
	}

	// Try to find JSON within ``` ... ``` code blocks (without language specifier)
	if idx := strings.Index(output, "```\n{"); idx != -1 {
		start := idx + len("```\n")
		rest := output[start:]

		// Same logic: track brace depth to find the JSON object end
		depth := 0
		inString := false
		escape := false
		jsonEnd := -1

		for i := 0; i < len(rest); i++ {
			c := rest[i]
			if escape {
				escape = false
				continue
			}
			if c == '\\' && inString {
				escape = true
				continue
			}
			if c == '"' {
				inString = !inString
				continue
			}
			if inString {
				continue
			}
			if c == '{' {
				depth++
			} else if c == '}' {
				depth--
				if depth == 0 {
					jsonEnd = i + 1
					break
				}
			}
		}

		if jsonEnd != -1 {
			return strings.TrimSpace(rest[:jsonEnd])
		}
	}

	// Try to find raw JSON object by looking for { and tracking brace depth
	startIdx := strings.Index(output, "{")
	if startIdx == -1 {
		return output // No JSON object found, return as-is
	}

	// Track brace depth to find the matching closing }
	depth := 0
	inString := false
	escape := false

	for i := startIdx; i < len(output); i++ {
		c := output[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inString {
			escape = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				return strings.TrimSpace(output[startIdx : i+1])
			}
		}
	}

	return output // No matching } found
}
