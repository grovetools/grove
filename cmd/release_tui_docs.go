package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/core/tui/theme"
	"gopkg.in/yaml.v3"

	"github.com/grovetools/grove/pkg/release"
)

// docsSectionRow is one row in the TUI docs panel: a docgen section with its
// notebook status (draft/dev/production) and whether `grove release gen` staged
// it in the current plan.
type docsSectionRow struct {
	Name      string
	Title     string
	Status    string // draft | dev | production | "" (unknown)
	Generated bool   // section is in the repo's plan.DocsSections scope (or all were generated)
}

// docgenSectionConfig is the minimal shape we read out of a repo's notebook
// docgen.config.yml — just the section name/title/status. Defined locally so we
// do not import docgen's config package into grove.
type docgenSectionConfig struct {
	Sections []struct {
		Name   string `yaml:"name"`
		Title  string `yaml:"title"`
		Status string `yaml:"status"`
	} `yaml:"sections"`
}

// resolveNotebookDocgenDir returns the absolute notebook docgen directory for a
// repo (the parent of docs/ and docgen.config.yml), via the core workspace API
// — never a hand-rolled path. Mirrors cmd/record.go's resolution.
func resolveNotebookDocgenDir(rootDir, repoName string) (string, error) {
	repoPath := filepath.Join(rootDir, repoName)
	node, err := workspace.GetProjectByPath(repoPath)
	if err != nil || node == nil {
		return "", fmt.Errorf("could not resolve workspace for %s: %w", repoPath, err)
	}
	cfg, err := config.LoadDefault()
	if err != nil {
		return "", fmt.Errorf("load core config: %w", err)
	}
	locator := workspace.NewNotebookLocator(cfg)
	docgenDir, err := locator.GetDocgenDir(node)
	if err != nil {
		return "", fmt.Errorf("resolve docgen dir: %w", err)
	}
	return docgenDir, nil
}

// collectDocsSections builds the docs-panel rows for a repo from the notebook's
// docgen.config.yml (section names + statuses) enriched with the repo's plan gen
// state (which sections gen staged). An empty repo.DocsSections with
// DocsGenerated=true means the whole repo was generated, so every section is
// marked Generated. If the notebook config cannot be read, it falls back to the
// plan's DocsSections list alone.
func collectDocsSections(rootDir, repoName string, repo *release.RepoReleasePlan) []docsSectionRow {
	generatedAll := repo != nil && repo.DocsGenerated && len(repo.DocsSections) == 0
	generatedScope := map[string]bool{}
	if repo != nil {
		for _, s := range repo.DocsSections {
			generatedScope[s] = true
		}
	}

	docgenDir, err := resolveNotebookDocgenDir(rootDir, repoName)
	if err == nil {
		cfgPath := filepath.Join(docgenDir, "docgen.config.yml")
		if data, readErr := os.ReadFile(cfgPath); readErr == nil {
			var cfg docgenSectionConfig
			if yaml.Unmarshal(data, &cfg) == nil && len(cfg.Sections) > 0 {
				rows := make([]docsSectionRow, 0, len(cfg.Sections))
				for _, s := range cfg.Sections {
					rows = append(rows, docsSectionRow{
						Name:      s.Name,
						Title:     s.Title,
						Status:    s.Status,
						Generated: generatedAll || generatedScope[s.Name],
					})
				}
				return rows
			}
		}
	}

	// Fallback: no readable notebook config — list whatever the plan recorded.
	var names []string
	for s := range generatedScope {
		names = append(names, s)
	}
	sort.Strings(names)
	rows := make([]docsSectionRow, 0, len(names))
	for _, n := range names {
		rows = append(rows, docsSectionRow{Name: n, Generated: true})
	}
	return rows
}

// buildDocsDiffCmd constructs the (unrun) `git diff --no-index` command that
// compares the repo's production docs/ tree against the notebook docgen docs/
// tree. Factored out for unit testing of the command construction (the args and
// the two directory operands) without running git. Returns an error when the
// notebook docgen dir cannot be resolved.
func buildDocsDiffCmd(rootDir, repoName string) (*exec.Cmd, error) {
	docgenDir, err := resolveNotebookDocgenDir(rootDir, repoName)
	if err != nil {
		return nil, err
	}
	repoDocs := filepath.Join(rootDir, repoName, "docs")
	notebookDocs := filepath.Join(docgenDir, "docs")
	return docsDiffCommand(filepath.Join(rootDir, repoName), repoDocs, notebookDocs), nil
}

// docsDiffCommand is the pure constructor for the docs diff command: a
// `git diff --no-index` over the repo's production docs/ (left) and the notebook
// docgen docs/ (right). Kept free of notebook resolution so the command shape
// (args + operands) is unit-testable without a live notebook.
func docsDiffCommand(workDir, repoDocs, notebookDocs string) *exec.Cmd {
	// --no-index diffs two arbitrary paths; the left (repo/docs) is already the
	// production-filtered copy, the right (notebook) is the full generated set.
	cmd := exec.Command("git", "diff", "--no-index", "--", repoDocs, notebookDocs)
	cmd.Dir = workDir
	return cmd
}

// renderDocsDiff runs buildDocsDiffCmd and returns its output for the pager. A
// clean tree (git diff --no-index exits 0 with no output) or a resolution error
// both yield a friendly message rather than an empty screen.
func renderDocsDiff(rootDir, repoName string) string {
	cmd, err := buildDocsDiffCmd(rootDir, repoName)
	if err != nil {
		return fmt.Sprintf("Could not resolve notebook docs for %s:\n\n%v", repoName, err)
	}
	out, _ := cmd.CombinedOutput() // exit 1 == differences found; that is expected
	if strings.TrimSpace(string(out)) == "" {
		return fmt.Sprintf("No differences between %s/docs and the notebook docgen docs.\n\n(Repo docs are the production-filtered copy; run 'grove release apply' to publish notebook changes.)", repoName)
	}
	return string(out)
}

// approvalBlocker returns a human-readable reason approval is blocked for a
// repo, or "" when approval is allowed. Approval covers docs + changelog
// together, so a recorded generation error on either blocks it.
func approvalBlocker(repo *release.RepoReleasePlan) string {
	if repo == nil {
		return ""
	}
	if strings.TrimSpace(repo.GenError) != "" {
		return "docs/changelog generation failed — " + firstLine(repo.GenError) + " (regenerate with 'G'/'g')"
	}
	if strings.TrimSpace(repo.ChangelogGenError) != "" {
		return "changelog generation failed — " + firstLine(repo.ChangelogGenError) + " (regenerate with 'g')"
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// renderRepoGenDetail renders the per-repo gen usage line for the table detail
// area: cache write/read tokens, estimated cost, and the greyed check-stage slot
// (always "check: skipped" in this effort). A recorded gen error is surfaced in
// red so the user knows why approval is blocked.
func renderRepoGenDetail(repo *release.RepoReleasePlan) string {
	check := repo.CheckStatus
	if check == "" {
		check = "skipped"
	}
	usage := fmt.Sprintf("cache: write %d / read %d tok  •  est $%.4f  •  ",
		repo.CacheWriteTokens, repo.CacheReadTokens, repo.GenEstCostUSD)
	line := theme.DefaultTheme.Muted.Render(usage) +
		theme.DefaultTheme.Muted.Render(fmt.Sprintf("check: %s", check))

	if blocker := approvalBlocker(repo); blocker != "" {
		line += "\n" + theme.DefaultTheme.Error.Render(theme.IconError+" "+blocker)
	}
	return line
}

// docsRegenMsg reports the outcome of an inline docs regeneration.
type docsRegenMsg struct {
	repoName   string
	section    string // "" == whole repo
	cacheWrite int64
	cacheRead  int64
	estCostUSD float64
	err        error
}

// regenDocsCmd regenerates docs (optionally a single section) for a repo through
// the exact Phase 3 gen path (genOneRepo), so inline TUI regen and headless
// `grove release gen --repo X --sections y` share one code path. It constructs
// its own genOptions (model/diff/TTL empty ⇒ the same config-default fallbacks
// as a flagless headless run) with docgen's streaming output routed to
// io.Discard so it does not corrupt the alt-screen.
func regenDocsCmd(plan *release.ReleasePlan, repoName, section string) tea.Cmd {
	return func() tea.Msg {
		repo, ok := plan.Repos[repoName]
		if !ok {
			return docsRegenMsg{repoName: repoName, section: section, err: fmt.Errorf("repo %q not in plan", repoName)}
		}

		opts := genOptions{Repos: []string{repoName}, Out: io.Discard}
		if section != "" {
			opts.Sections = []string{section}
		}

		res := genOneRepo(context.Background(), plan, repoName, repo, opts)
		_ = release.SavePlan(plan)

		return docsRegenMsg{
			repoName:   repoName,
			section:    section,
			cacheWrite: res.CacheWriteTokens,
			cacheRead:  res.CacheReadTokens,
			estCostUSD: res.EstCostUSD,
			err:        res.Err,
		}
	}
}

// updateDocs handles keys in the docs review panel.
func (m releaseTuiModel) updateDocs(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Back), key.Matches(msg, m.keys.ViewDocs):
		m.currentView = viewTable
		return m, nil

	case key.Matches(msg, m.keys.Base.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.Base.Up):
		if m.docsIndex > 0 {
			m.docsIndex--
		}
		return m, nil

	case key.Matches(msg, m.keys.Base.Down):
		if m.docsIndex < len(m.docsSections)-1 {
			m.docsIndex++
		}
		return m, nil

	case key.Matches(msg, m.keys.DiffDocs):
		m.viewport.SetContent(renderDocsDiff(m.plan.RootDir, m.docsRepo))
		m.viewport.GotoTop()
		m.currentView = viewDiff
		return m, nil

	case key.Matches(msg, m.keys.RegenDocs):
		// Regenerate the selected section only (scoped path).
		if !m.generating && m.docsIndex < len(m.docsSections) {
			section := m.docsSections[m.docsIndex].Name
			m.generating = true
			m.regenCount++
			m.genProgress = fmt.Sprintf("Regenerating section %s for %s...", section, m.docsRepo)
			return m, tea.Batch(regenDocsCmd(m.plan, m.docsRepo, section), tickSpinner())
		}
		return m, nil
	}
	return m, nil
}

// updateDiff handles keys in the docs diff pager.
func (m releaseTuiModel) updateDiff(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Back), key.Matches(msg, m.keys.DiffDocs):
		// Return to the docs panel if it was the entry point, else the table.
		if m.docsRepo != "" && len(m.docsSections) > 0 {
			m.currentView = viewDocs
		} else {
			m.currentView = viewTable
		}
		return m, nil

	case key.Matches(msg, m.keys.Base.Quit):
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// viewDocs renders the docs review panel: the section list with notebook status
// and gen state, plus the per-repo usage/cost/check detail.
func (m releaseTuiModel) viewDocs() string {
	header := theme.DefaultTheme.Header.Render(fmt.Sprintf("%s Docs Review: %s", theme.IconNote, m.docsRepo))

	var b strings.Builder
	if len(m.docsSections) == 0 {
		b.WriteString(theme.DefaultTheme.Muted.Render(
			"No docs sections found. Run 'grove release gen' (or press 'G') to generate docs for this repo.\n"))
	} else {
		for i, s := range m.docsSections {
			cursor := "  "
			if i == m.docsIndex {
				cursor = theme.IconArrowRightBold + " "
			}
			name := s.Name
			if s.Title != "" {
				name = fmt.Sprintf("%s (%s)", s.Title, s.Name)
			}

			var status string
			switch s.Status {
			case "production":
				status = theme.DefaultTheme.Success.Render("production")
			case "dev":
				status = theme.DefaultTheme.Warning.Render("dev")
			case "draft":
				status = theme.DefaultTheme.Muted.Render("draft")
			default:
				status = theme.DefaultTheme.Muted.Render("-")
			}

			gen := theme.DefaultTheme.Muted.Render("not staged")
			if s.Generated {
				gen = theme.DefaultTheme.Success.Render(theme.IconSuccess + " staged")
			}

			line := fmt.Sprintf("%s%-40s  %-24s  %s", cursor, name, status, gen)
			if i == m.docsIndex {
				line = theme.DefaultTheme.Selected.Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	if repo, ok := m.plan.Repos[m.docsRepo]; ok {
		b.WriteString("\n" + renderRepoGenDetail(repo) + "\n")
	}

	help := theme.DefaultTheme.Muted.Render(
		"↑/↓: section • G: regen section • D: diff vs notebook • esc: back • q: quit")

	return fmt.Sprintf("%s\n\n%s\n%s", header, b.String(), help)
}

// viewDiff renders the notebook-vs-repo docs diff pager.
func (m releaseTuiModel) viewDiff() string {
	header := theme.DefaultTheme.Header.Render(
		fmt.Sprintf("%s Docs Diff (repo/docs vs notebook): %s", theme.IconDiff, m.docsRepo))
	help := theme.DefaultTheme.Muted.Render("↑/↓: scroll • esc: back • q: quit")
	return fmt.Sprintf("%s\n\n%s\n\n%s", header, m.viewport.View(), help)
}
