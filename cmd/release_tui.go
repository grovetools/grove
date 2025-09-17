package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/mattsolo1/grove-meta/pkg/release"
	"github.com/spf13/cobra"
)

// Define styles for the release TUI
var (
	releaseTuiHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FF79C6")).
				MarginBottom(1)

	releaseTuiStatusPendingStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#FFB86C"))

	releaseTuiStatusApprovedStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#50FA7B"))

	releaseTuiSuggestedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#8BE9FD"))

	releaseTuiSelectedStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#44475A")).
				Bold(true)

	releaseTuiHelpStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#6272A4")).
				MarginTop(1)

	releaseTuiChangelogHeaderStyle = lipgloss.NewStyle().
					Bold(true).
					Foreground(lipgloss.Color("#BD93F9")).
					MarginBottom(1)

	releaseTuiChangelogBodyStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#F8F8F2"))
)

// Key bindings for the release TUI
type releaseKeyMap struct {
	Up           key.Binding
	Down         key.Binding
	SelectMajor  key.Binding
	SelectMinor  key.Binding
	SelectPatch  key.Binding
	ViewChangelog key.Binding
	Approve      key.Binding
	Apply        key.Binding
	Quit         key.Binding
	Back         key.Binding
}

var releaseKeys = releaseKeyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	SelectMajor: key.NewBinding(
		key.WithKeys("m"),
		key.WithHelp("m", "set major"),
	),
	SelectMinor: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "set minor"),
	),
	SelectPatch: key.NewBinding(
		key.WithKeys("p"),
		key.WithHelp("p", "set patch"),
	),
	ViewChangelog: key.NewBinding(
		key.WithKeys("v"),
		key.WithHelp("v", "view changelog"),
	),
	Approve: key.NewBinding(
		key.WithKeys("a"),
		key.WithHelp("a", "approve"),
	),
	Apply: key.NewBinding(
		key.WithKeys("A"),
		key.WithHelp("A", "apply release"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	Back: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back"),
	),
}

// TUI views
const (
	viewTable     = "table"
	viewChangelog = "changelog"
	viewApplying  = "applying"
)

// releaseTuiModel represents the TUI state
type releaseTuiModel struct {
	plan          *release.ReleasePlan
	keys          releaseKeyMap
	currentView   string
	selectedIndex int
	repoNames     []string // Ordered list of repo names for consistent navigation
	viewport      viewport.Model
	width         int
	height        int
	err           error
	applyOutput   string
	applying      bool
}

func initialReleaseModel(plan *release.ReleasePlan) releaseTuiModel {
	// Extract and sort repo names for consistent ordering
	repoNames := make([]string, 0, len(plan.Repos))
	for name := range plan.Repos {
		repoNames = append(repoNames, name)
	}
	// Use the topological sort from the plan if available
	if len(plan.ReleaseLevels) > 0 {
		sortedNames := make([]string, 0, len(repoNames))
		for _, level := range plan.ReleaseLevels {
			for _, repo := range level {
				if _, exists := plan.Repos[repo]; exists {
					sortedNames = append(sortedNames, repo)
				}
			}
		}
		repoNames = sortedNames
	}

	return releaseTuiModel{
		plan:        plan,
		keys:        releaseKeys,
		currentView: viewTable,
		repoNames:   repoNames,
		viewport:    viewport.New(80, 20),
	}
}

func (m releaseTuiModel) Init() tea.Cmd {
	return nil
}

func (m releaseTuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width - 4
		m.viewport.Height = msg.Height - 6
		return m, nil

	case tea.KeyMsg:
		switch m.currentView {
		case viewTable:
			return m.updateTable(msg)
		case viewChangelog:
			return m.updateChangelog(msg)
		case viewApplying:
			return m.updateApplying(msg)
		}

	case applyCompleteMsg:
		m.applying = false
		if msg.err != nil {
			m.err = msg.err
			m.applyOutput += fmt.Sprintf("\n\nError: %v", msg.err)
		} else {
			m.applyOutput += "\n\n✅ Release completed successfully!"
		}
		return m, nil

	case applyProgressMsg:
		m.applyOutput += msg.text
		m.viewport.SetContent(m.applyOutput)
		m.viewport.GotoBottom()
		return m, nil
	}

	return m, nil
}

func (m releaseTuiModel) updateTable(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.Up):
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
		return m, nil

	case key.Matches(msg, m.keys.Down):
		if m.selectedIndex < len(m.repoNames)-1 {
			m.selectedIndex++
		}
		return m, nil

	case key.Matches(msg, m.keys.SelectMajor):
		if m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			if repo, ok := m.plan.Repos[repoName]; ok {
				repo.SelectedBump = "major"
				repo.NextVersion = calculateNextVersion(repo.CurrentVersion, "major")
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.SelectMinor):
		if m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			if repo, ok := m.plan.Repos[repoName]; ok {
				repo.SelectedBump = "minor"
				repo.NextVersion = calculateNextVersion(repo.CurrentVersion, "minor")
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.SelectPatch):
		if m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			if repo, ok := m.plan.Repos[repoName]; ok {
				repo.SelectedBump = "patch"
				repo.NextVersion = calculateNextVersion(repo.CurrentVersion, "patch")
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.ViewChangelog):
		if m.selectedIndex < len(m.repoNames) {
			m.currentView = viewChangelog
			// Load changelog content
			repoName := m.repoNames[m.selectedIndex]
			if repo, ok := m.plan.Repos[repoName]; ok && repo.ChangelogPath != "" {
				content, err := os.ReadFile(repo.ChangelogPath)
				if err != nil {
					m.viewport.SetContent(fmt.Sprintf("Error loading changelog: %v", err))
				} else {
					m.viewport.SetContent(string(content))
				}
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.Apply):
		// Check if all repos are approved
		allApproved := true
		for _, repo := range m.plan.Repos {
			if repo.Status != "Approved" {
				allApproved = false
				break
			}
		}
		if allApproved {
			m.currentView = viewApplying
			m.applying = true
			m.applyOutput = "🚀 Starting release process...\n\n"
			m.viewport.SetContent(m.applyOutput)
			// Save the plan before applying
			release.SavePlan(m.plan)
			return m, applyRelease(m.plan)
		}
		return m, nil
	}

	return m, nil
}

func (m releaseTuiModel) updateChangelog(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Back):
		m.currentView = viewTable
		return m, nil

	case key.Matches(msg, m.keys.Approve):
		if m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			if repo, ok := m.plan.Repos[repoName]; ok {
				repo.Status = "Approved"
			}
		}
		m.currentView = viewTable
		return m, nil

	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	}

	// Allow scrolling in the viewport
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m releaseTuiModel) updateApplying(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		if !m.applying {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m releaseTuiModel) View() string {
	switch m.currentView {
	case viewChangelog:
		return m.viewChangelog()
	case viewApplying:
		return m.viewApplying()
	default:
		return m.viewTable()
	}
}

func (m releaseTuiModel) viewTable() string {
	header := releaseTuiHeaderStyle.Render("🚀 Grove Release Manager")

	// Create table
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4"))).
		Headers("Repository", "Current", "Proposed", "Increment", "Status")

	// Count repositories with changes
	reposWithChanges := 0
	for _, repo := range m.plan.Repos {
		if repo.NextVersion != repo.CurrentVersion {
			reposWithChanges++
		}
	}

	// Add parent ecosystem repository first if it exists
	if m.plan.ParentVersion != "" {
		incrementType := "date"
		if m.plan.ParentCurrentVersion != "" && m.plan.ParentCurrentVersion == m.plan.ParentVersion {
			incrementType = "-"
		}
		
		row := []string{
			"grove-ecosystem",
			m.plan.ParentCurrentVersion,
			m.plan.ParentVersion,
			incrementType,
			"-",
		}
		
		t.Row(row...)
		
		// Add separator row
		t.Row("───────────────", "─────────", "──────────", "───────────", "─────────")
	}

	// Add rows for each repository
	for i, repoName := range m.repoNames {
		repo := m.plan.Repos[repoName]

		// Determine increment type
		incrementType := repo.SelectedBump
		if repo.CurrentVersion == repo.NextVersion {
			incrementType = "-"
		}

		// Format status
		var statusStr string
		if repo.CurrentVersion == repo.NextVersion {
			statusStr = "-"
		} else if repo.Status == "Approved" {
			statusStr = releaseTuiStatusApprovedStyle.Render("✓ Approved")
		} else {
			statusStr = releaseTuiStatusPendingStyle.Render("⏳ Pending")
		}

		row := []string{
			repoName,
			repo.CurrentVersion,
			repo.NextVersion,
			incrementType,
			statusStr,
		}

		// Highlight selected row (but not for repos without changes)
		if i == m.selectedIndex && repo.CurrentVersion != repo.NextVersion {
			for j, cell := range row {
				row[j] = releaseTuiSelectedStyle.Render(cell)
			}
		}

		t.Row(row...)
	}

	tableStr := t.Render()

	// Show release order information
	releaseInfo := ""
	if len(m.plan.ReleaseLevels) > 0 {
		releaseInfo = "\n📋 Release Order (by dependency level)\n\n"
		for i, level := range m.plan.ReleaseLevels {
			if len(level) > 0 {
				releaseInfo += fmt.Sprintf("Level %d", i+1)
				if len(level) > 1 {
					releaseInfo += " (can release in parallel)"
				}
				releaseInfo += ":\n"
				for _, repo := range level {
					if repoPlan, ok := m.plan.Repos[repo]; ok {
						if repoPlan.CurrentVersion != repoPlan.NextVersion {
							releaseInfo += fmt.Sprintf("  • %s: %s → %s (%s)\n", 
								repo, repoPlan.CurrentVersion, repoPlan.NextVersion, repoPlan.SelectedBump)
						}
					}
				}
			}
		}
		releaseInfo += fmt.Sprintf("\n%d repositories will be released.\n", reposWithChanges)
	}

	// Check if all approved for help text
	allApproved := true
	needsApproval := false
	for _, repo := range m.plan.Repos {
		if repo.CurrentVersion != repo.NextVersion {
			needsApproval = true
			if repo.Status != "Approved" {
				allApproved = false
				break
			}
		}
	}

	helpItems := []string{
		"↑/↓: navigate",
		"m/n/p: set major/minor/patch",
		"v: view changelog",
	}
	if allApproved && needsApproval {
		helpItems = append(helpItems, "A: apply release")
	}
	helpItems = append(helpItems, "q: quit")

	help := releaseTuiHelpStyle.Render(strings.Join(helpItems, " • "))

	// Show suggestion reasoning for selected repo
	var reasoning string
	if m.selectedIndex < len(m.repoNames) {
		repo := m.plan.Repos[m.repoNames[m.selectedIndex]]
		if repo.SuggestionReasoning != "" && repo.CurrentVersion != repo.NextVersion {
			reasoning = fmt.Sprintf("\n💡 %s", repo.SuggestionReasoning)
		}
	}

	return fmt.Sprintf("%s\n\n%s%s%s\n\n%s", header, tableStr, releaseInfo, reasoning, help)
}

func (m releaseTuiModel) viewChangelog() string {
	header := releaseTuiChangelogHeaderStyle.Render(fmt.Sprintf("📝 Changelog Preview: %s", m.repoNames[m.selectedIndex]))
	
	help := releaseTuiHelpStyle.Render("a: approve • esc: back • q: quit")

	return fmt.Sprintf("%s\n\n%s\n\n%s", header, m.viewport.View(), help)
}

func (m releaseTuiModel) viewApplying() string {
	header := releaseTuiHeaderStyle.Render("🔄 Applying Release...")

	var help string
	if m.applying {
		help = releaseTuiHelpStyle.Render("Release in progress...")
	} else {
		help = releaseTuiHelpStyle.Render("q: quit")
	}

	return fmt.Sprintf("%s\n\n%s\n\n%s", header, m.viewport.View(), help)
}

// Helper function to calculate next version
func calculateNextVersion(current, bump string) string {
	parts := strings.Split(current, ".")
	if len(parts) != 3 {
		return current // Return as-is if not semantic version
	}

	major, minor, patch := parts[0], parts[1], parts[2]

	// Parse version numbers
	var majorNum, minorNum, patchNum int
	fmt.Sscanf(major, "v%d", &majorNum)
	fmt.Sscanf(minor, "%d", &minorNum)
	fmt.Sscanf(patch, "%d", &patchNum)

	switch bump {
	case "major":
		majorNum++
		minorNum = 0
		patchNum = 0
	case "minor":
		minorNum++
		patchNum = 0
	case "patch":
		patchNum++
	}

	return fmt.Sprintf("v%d.%d.%d", majorNum, minorNum, patchNum)
}

// Messages for async operations
type applyCompleteMsg struct {
	err error
}

type applyProgressMsg struct {
	text string
}

// Command to apply the release
func applyRelease(plan *release.ReleasePlan) tea.Cmd {
	return func() tea.Msg {
		// Execute the release apply
		ctx := context.Background()
		err := runReleaseApply(ctx)
		return applyCompleteMsg{err: err}
	}
}

// runReleaseTUI starts the interactive release TUI
func runReleaseTUI(ctx context.Context) error {
	// First, generate or load the release plan
	plan, err := release.LoadPlan()
	if err != nil {
		if os.IsNotExist(err) {
			// Generate a new plan
			fmt.Println("No existing release plan found. Generating new plan...")
			plan, err = runReleasePlan(ctx)
			if err != nil {
				return fmt.Errorf("failed to generate release plan: %w", err)
			}
		} else {
			return fmt.Errorf("failed to load release plan: %w", err)
		}
	}

	// Start the TUI
	p := tea.NewProgram(initialReleaseModel(plan), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("error running TUI: %w", err)
	}
	return nil
}

// newReleaseTuiCmd creates the 'grove release tui' subcommand
func newReleaseTuiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Launch interactive TUI for release planning",
		Long: `Launch an interactive Terminal User Interface for release planning.

This command provides an interactive way to:
- Review repositories with changes
- See LLM-suggested version bumps with justifications
- Manually adjust version bump types (major/minor/patch)
- Preview and approve changelogs
- Execute the release once all repositories are approved

The release plan is persisted in ~/.grove/release_plan.json and can be
resumed if interrupted.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			return runReleaseTUI(ctx)
		},
	}
	return cmd
}

