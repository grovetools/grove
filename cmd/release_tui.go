package cmd

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
	Up              key.Binding
	Down            key.Binding
	Toggle          key.Binding
	Tab             key.Binding
	SelectAll       key.Binding
	DeselectAll     key.Binding
	SelectMajor     key.Binding
	SelectMinor     key.Binding
	SelectPatch     key.Binding
	ApplySuggestion key.Binding
	ViewChangelog   key.Binding
	EditChangelog   key.Binding
	EditRepoChangelog key.Binding
	GenerateChangelog key.Binding
	GenerateAll     key.Binding
	WriteChangelog  key.Binding
	EditRules       key.Binding
	ResetRules      key.Binding
	ToggleDryRun    key.Binding
	TogglePush      key.Binding
	ToggleSyncDeps  key.Binding
	Approve         key.Binding
	Apply           key.Binding
	Help            key.Binding
	Quit            key.Binding
	Back            key.Binding
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
	Toggle: key.NewBinding(
		key.WithKeys(" ", "x"),
		key.WithHelp("space/x", "toggle selection"),
	),
	Tab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch view"),
	),
	SelectAll: key.NewBinding(
		key.WithKeys("ctrl+a"),
		key.WithHelp("ctrl+a", "select all"),
	),
	DeselectAll: key.NewBinding(
		key.WithKeys("ctrl+d"),
		key.WithHelp("ctrl+d", "deselect all"),
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
	ApplySuggestion: key.NewBinding(
		key.WithKeys("s"),
		key.WithHelp("s", "apply suggestion"),
	),
	ViewChangelog: key.NewBinding(
		key.WithKeys("v"),
		key.WithHelp("v", "view changelog"),
	),
	EditChangelog: key.NewBinding(
		key.WithKeys("e"),
		key.WithHelp("e", "edit staged changelog"),
	),
	EditRepoChangelog: key.NewBinding(
		key.WithKeys("E"),
		key.WithHelp("E", "edit repo CHANGELOG.md"),
	),
	GenerateChangelog: key.NewBinding(
		key.WithKeys("g"),
		key.WithHelp("g", "generate changelog (LLM)"),
	),
	GenerateAll: key.NewBinding(
		key.WithKeys("G"),
		key.WithHelp("G", "generate all changelogs"),
	),
	WriteChangelog: key.NewBinding(
		key.WithKeys("w"),
		key.WithHelp("w", "write changelog to repo"),
	),
	EditRules: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "edit LLM rules"),
	),
	ResetRules: key.NewBinding(
		key.WithKeys("R"),
		key.WithHelp("R", "reset all rules to *"),
	),
	ToggleDryRun: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "toggle dry-run mode"),
	),
	TogglePush: key.NewBinding(
		key.WithKeys("P"),
		key.WithHelp("P", "toggle push to remote"),
	),
	ToggleSyncDeps: key.NewBinding(
		key.WithKeys("S"),
		key.WithHelp("S", "toggle sync dependencies"),
	),
	Approve: key.NewBinding(
		key.WithKeys("a"),
		key.WithHelp("a", "approve"),
	),
	Apply: key.NewBinding(
		key.WithKeys("A"),
		key.WithHelp("A", "apply release"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "toggle help"),
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
	viewSettings  = "settings"
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
	generating    bool          // Whether changelog generation is in progress
	genProgress   string        // Progress message for generation
	spinner       int           // Spinner animation frame
	genQueue      []string      // Queue of repos to generate changelogs for
	genCurrent    string        // Current repo being generated
	genCompleted  int           // Number of completed generations
	dryRun        bool          // Whether to run in dry-run mode
	showHelp      bool          // Whether to show help popup
	shouldApply   bool          // Flag to indicate we should exit and apply
	push          bool          // Whether to push to remote
	syncDeps      bool          // Whether to sync dependencies
	settingsIndex int           // Currently selected setting in settings view
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
		dryRun:      true, // Start in dry-run mode for safety
		showHelp:    false,
	}
}

func (m releaseTuiModel) Init() tea.Cmd {
	return nil
}

func (m releaseTuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Always update the viewport, regardless of the message type
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Adjust viewport size - leave room for header and footer
		m.viewport.Width = msg.Width - 4
		m.viewport.Height = msg.Height - 8 // More room for header/footer
		return m, vpCmd

	case tea.KeyMsg:
		switch m.currentView {
		case viewTable:
			return m.updateTable(msg)
		case viewChangelog:
			return m.updateChangelog(msg)
		case viewSettings:
			return m.updateSettings(msg)
		}


	case changelogGeneratedMsg:
		// Update the repo with results
		if msg.err == nil {
			if repo, ok := m.plan.Repos[msg.repoName]; ok {
				repo.ChangelogPath = msg.changelogPath
				repo.ChangelogCommit = msg.changelogCommit // Save the commit hash
				if msg.suggestion != "" {
					repo.SuggestedBump = msg.suggestion
					repo.SuggestionReasoning = msg.suggestionReasoning
					// Update the selected bump and next version if not already set
					if repo.SelectedBump == "" {
						repo.SelectedBump = msg.suggestion
						repo.NextVersion = calculateNextVersion(repo.CurrentVersion, msg.suggestion)
					}
				}
				// Save the updated plan
				release.SavePlan(m.plan)
			}
		}
		
		// Check if we have more in the queue
		if len(m.genQueue) > 0 {
			// Remove completed repo from queue
			for i, name := range m.genQueue {
				if name == msg.repoName {
					m.genQueue = append(m.genQueue[:i], m.genQueue[i+1:]...)
					m.genCompleted++
					break
				}
			}
			
			// Process next in queue
			if len(m.genQueue) > 0 {
				m.genCurrent = m.genQueue[0]
				total := m.genCompleted + len(m.genQueue)
				m.genProgress = fmt.Sprintf("Generating changelog for %s (%d/%d)", m.genCurrent, m.genCompleted+1, total)
				
				if repo, ok := m.plan.Repos[m.genCurrent]; ok {
					return m, generateChangelogCmd(m.plan.RootDir, m.genCurrent, repo)
				}
			} else {
				// All done
				m.generating = false
				m.genProgress = fmt.Sprintf("✅ Generated changelogs for %d repositories", m.genCompleted)
				m.genQueue = nil
				m.genCurrent = ""
				m.genCompleted = 0
				return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
					return clearProgressMsg{}
				})
			}
		} else {
			// Single generation mode
			m.generating = false
			if msg.err != nil {
				m.genProgress = fmt.Sprintf("❌ Failed to generate changelog for %s: %v", msg.repoName, msg.err)
			} else {
				m.genProgress = fmt.Sprintf("✅ Changelog generated for %s", msg.repoName)
			}
			// Clear progress message after 3 seconds
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearProgressMsg{}
			})
		}
	
	case clearProgressMsg:
		m.genProgress = ""
		return m, nil
	
	case spinnerTickMsg:
		// Only animate if we're generating
		if m.generating {
			m.spinner = (m.spinner + 1) % 4
			return m, tickSpinner()
		}
		return m, nil
	}

	return m, nil
}

func (m releaseTuiModel) updateTable(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle help popup first - only respond to close keys when help is shown
	if m.showHelp {
		switch msg.String() {
		case "?", "q", "esc", "ctrl+c":
			m.showHelp = false
			return m, nil
		default:
			// Ignore ALL other keys when help is shown, including 'A'
			return m, nil
		}
	}
	
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

	case key.Matches(msg, m.keys.Tab):
		m.currentView = viewSettings
		return m, nil

	case key.Matches(msg, m.keys.Toggle):
		if m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			if repo, ok := m.plan.Repos[repoName]; ok {
				// Only allow toggling repos that have changes
				if repo.CurrentVersion != repo.NextVersion {
					repo.Selected = !repo.Selected
					// If deselected, also unapprove
					if !repo.Selected {
						repo.Status = "Pending Review"
					}
				}
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.SelectAll):
		// Select all repositories that have changes
		selectedCount := 0
		for _, repoName := range m.repoNames {
			if repo, ok := m.plan.Repos[repoName]; ok {
				// Only select repos that have changes
				if repo.CurrentVersion != repo.NextVersion {
					repo.Selected = true
					selectedCount++
				}
			}
		}
		if selectedCount > 0 {
			m.genProgress = fmt.Sprintf("✓ Selected %d repositories", selectedCount)
			// Save the updated plan
			release.SavePlan(m.plan)
			return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
				return clearProgressMsg{}
			})
		}
		return m, nil

	case key.Matches(msg, m.keys.DeselectAll):
		// Deselect all repositories
		deselectedCount := 0
		for _, repoName := range m.repoNames {
			if repo, ok := m.plan.Repos[repoName]; ok {
				if repo.Selected {
					repo.Selected = false
					repo.Status = "Pending Review" // Also unapprove
					deselectedCount++
				}
			}
		}
		if deselectedCount > 0 {
			m.genProgress = fmt.Sprintf("✓ Deselected %d repositories", deselectedCount)
			// Save the updated plan
			release.SavePlan(m.plan)
			return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
				return clearProgressMsg{}
			})
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

	case key.Matches(msg, m.keys.ApplySuggestion):
		// Apply the LLM's suggested version bump
		if m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			if repo, ok := m.plan.Repos[repoName]; ok {
				// Only apply if there's a suggestion
				if repo.SuggestedBump != "" {
					repo.SelectedBump = repo.SuggestedBump
					repo.NextVersion = calculateNextVersion(repo.CurrentVersion, repo.SuggestedBump)
					m.genProgress = fmt.Sprintf("✅ Applied %s version bump to %s", 
						strings.ToUpper(repo.SuggestedBump), repoName)
					// Save the updated plan
					release.SavePlan(m.plan)
					// Clear progress after a moment
					return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
						return clearProgressMsg{}
					})
				} else {
					m.genProgress = "⚠️ No suggestion available. Generate changelog first with 'g'"
					return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
						return clearProgressMsg{}
					})
				}
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.EditChangelog):
		// Edit the staged changelog for the selected repository
		if m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			if repo, ok := m.plan.Repos[repoName]; ok {
				if repo.ChangelogPath != "" {
					// Open staged changelog in editor
					return m, editFileCmd(repo.ChangelogPath)
				} else {
					// No staged changelog, try to edit the repo's CHANGELOG.md
					repoPath := filepath.Join(m.plan.RootDir, repoName)
					changelogPath := filepath.Join(repoPath, "CHANGELOG.md")
					
					// Check if CHANGELOG.md exists
					if _, err := os.Stat(changelogPath); err == nil {
						return m, editFileCmd(changelogPath)
					} else {
						m.genProgress = "⚠️ No changelog found. Generate one first with 'g'"
						return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
							return clearProgressMsg{}
						})
					}
				}
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.EditRepoChangelog):
		// Edit the repository's actual CHANGELOG.md file
		if m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			repoPath := filepath.Join(m.plan.RootDir, repoName)
			changelogPath := filepath.Join(repoPath, "CHANGELOG.md")
			
			// Check if CHANGELOG.md exists, create if not
			if _, err := os.Stat(changelogPath); os.IsNotExist(err) {
				// Create with a basic template
				template := fmt.Sprintf("# Changelog\n\nAll notable changes to %s will be documented in this file.\n\nThe format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),\nand this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).\n\n", repoName)
				if err := os.WriteFile(changelogPath, []byte(template), 0644); err != nil {
					m.genProgress = fmt.Sprintf("❌ Failed to create CHANGELOG.md: %v", err)
					return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
						return clearProgressMsg{}
					})
				}
			}
			
			// Open in editor
			return m, editFileCmd(changelogPath)
		}
		return m, nil

	case key.Matches(msg, m.keys.ViewChangelog):
		if m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			if repo, ok := m.plan.Repos[repoName]; ok {
				// Only view if repo has changes
				if repo.CurrentVersion != repo.NextVersion {
					m.currentView = viewChangelog
					// Load changelog content
					if repo.ChangelogPath != "" {
						content, err := os.ReadFile(repo.ChangelogPath)
						if err != nil {
							m.viewport.SetContent(fmt.Sprintf("Error loading changelog: %v", err))
						} else {
							m.viewport.SetContent(string(content))
						}
					} else {
						m.viewport.SetContent("No changelog generated yet. Press 'g' in the main view to generate.")
					}
				}
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.WriteChangelog):
		// Write changelog to repository CHANGELOG.md
		if m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			if repo, ok := m.plan.Repos[repoName]; ok {
				if repo.ChangelogPath != "" {
					// Read the staged changelog
					stagedContent, err := os.ReadFile(repo.ChangelogPath)
					if err != nil {
						m.genProgress = fmt.Sprintf("❌ Failed to read staged changelog: %v", err)
						return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
							return clearProgressMsg{}
						})
					}
					
					// Write to the repository's CHANGELOG.md
					repoPath := filepath.Join(m.plan.RootDir, repoName)
					changelogPath := filepath.Join(repoPath, "CHANGELOG.md")
					
					// Read existing content if file exists
					var existingContent []byte
					if _, err := os.Stat(changelogPath); err == nil {
						existingContent, _ = os.ReadFile(changelogPath)
					}
					
					// Prepend new changelog to existing content
					var newContent []byte
					if len(existingContent) > 0 {
						newContent = append(stagedContent, '\n')
						newContent = append(newContent, existingContent...)
					} else {
						newContent = stagedContent
					}
					
					// Write the file
					if err := os.WriteFile(changelogPath, newContent, 0644); err != nil {
						m.genProgress = fmt.Sprintf("❌ Failed to write changelog: %v", err)
					} else {
						// Calculate and store hash of the written content
						hash := sha256.Sum256(newContent)
						repo.ChangelogHash = fmt.Sprintf("%x", hash)
						repo.ChangelogState = "clean"
						release.SavePlan(m.plan)
						
						m.genProgress = fmt.Sprintf("✅ Changelog written to %s", changelogPath)
						
						// Open in editor for further editing
						return m, tea.Batch(
							editFileCmd(changelogPath),
							tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
								return clearProgressMsg{}
							}),
						)
					}
					
					return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
						return clearProgressMsg{}
					})
				} else {
					m.genProgress = "⚠️ No changelog generated yet. Press 'g' to generate first."
					return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
						return clearProgressMsg{}
					})
				}
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.ResetRules):
		// Reset rules to "*" for all selected repositories
		resetCount := 0
		for _, repoName := range m.repoNames {
			if repo, ok := m.plan.Repos[repoName]; ok {
				// Only reset for selected repos
				if repo.Selected {
					repoPath := filepath.Join(m.plan.RootDir, repoName)
					rulesPath := filepath.Join(repoPath, ".grove", "rules")
					
					// Create .grove directory if it doesn't exist
					groveDir := filepath.Join(repoPath, ".grove")
					if err := os.MkdirAll(groveDir, 0755); err != nil {
						continue
					}
					
					// Write "*" to rules file to include all files
					content := "# Grove rules - include all repository files\n*\n"
					if err := os.WriteFile(rulesPath, []byte(content), 0644); err != nil {
						continue
					}
					resetCount++
				}
			}
		}
		
		if resetCount > 0 {
			m.genProgress = fmt.Sprintf("✅ Reset rules to '*' for %d repositories", resetCount)
		} else {
			m.genProgress = "⚠️ No repositories selected"
		}
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return clearProgressMsg{}
		})

	case key.Matches(msg, m.keys.EditRules):
		// Edit rules file for the selected repository
		if m.selectedIndex < len(m.repoNames) && !m.generating {
			repoName := m.repoNames[m.selectedIndex]
			repoPath := filepath.Join(m.plan.RootDir, repoName)
			rulesPath := filepath.Join(repoPath, ".grove", "rules")
			
			// Create rules file if it doesn't exist
			if _, err := os.Stat(rulesPath); os.IsNotExist(err) {
				groveDir := filepath.Join(repoPath, ".grove")
				os.MkdirAll(groveDir, 0755)
				content := `# Grove rules file for LLM context
# Add file paths or patterns here, one per line
# Examples:
#   README.md
#   docs/*.md
#   src/main.go
`
				os.WriteFile(rulesPath, []byte(content), 0644)
			}
			
			// Open in editor
			return m, editRulesCmd(rulesPath)
		}
		return m, nil

	case key.Matches(msg, m.keys.GenerateChangelog):
		if m.selectedIndex < len(m.repoNames) && !m.generating {
			repoName := m.repoNames[m.selectedIndex]
			if repo, ok := m.plan.Repos[repoName]; ok {
				// Only generate for repos with changes
				if repo.CurrentVersion != repo.NextVersion {
					m.generating = true
					m.genProgress = fmt.Sprintf("Generating changelog for %s", repoName)
					// Start spinner animation
					return m, tea.Batch(
						generateChangelogCmd(m.plan.RootDir, repoName, repo),
						tickSpinner(),
					)
				}
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.GenerateAll):
		if !m.generating {
			// Build queue of repos that need changelogs
			var queue []string
			for _, repoName := range m.repoNames {
				if repo, ok := m.plan.Repos[repoName]; ok {
					// Only include selected repos with changes
					if repo.Selected && repo.CurrentVersion != repo.NextVersion {
						queue = append(queue, repoName)
					}
				}
			}
			
			if len(queue) > 0 {
				m.generating = true
				m.genQueue = queue
				m.genCompleted = 0
				m.genCurrent = queue[0]
				m.genProgress = fmt.Sprintf("Generating changelog for %s (%d/%d)", m.genCurrent, 1, len(queue))
				
				// Start generating the first one
				if repo, ok := m.plan.Repos[m.genCurrent]; ok {
					return m, tea.Batch(
						generateChangelogCmd(m.plan.RootDir, m.genCurrent, repo),
						tickSpinner(),
					)
				}
			} else {
				m.genProgress = "No repositories selected or need changes"
				return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
					return clearProgressMsg{}
				})
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.Approve):
		// Toggle approval status for selected repository
		if m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			if repo, ok := m.plan.Repos[repoName]; ok {
				if repo.CurrentVersion != repo.NextVersion {
					if repo.Status == "Approved" {
						repo.Status = "Pending Review"
						m.genProgress = fmt.Sprintf("⏳ Unapproved %s", repoName)
					} else {
						repo.Status = "Approved"
						m.genProgress = fmt.Sprintf("✅ Approved %s for release", repoName)
					}
					// Save the updated plan
					release.SavePlan(m.plan)
					return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
						return clearProgressMsg{}
					})
				}
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.Help):
		m.showHelp = !m.showHelp
		return m, nil

	case key.Matches(msg, m.keys.Apply):
		// Check if all selected repos are approved
		allApproved := true
		hasSelection := false
		for repoName, repo := range m.plan.Repos {
			if repo.Selected && repo.CurrentVersion != repo.NextVersion {
				hasSelection = true
				if repo.Status != "Approved" {
					allApproved = false
					break
				}
				
				// Check for dirty changelog
				if repo.ChangelogHash != "" {
					if isDirty := checkChangelogDirty(m.plan.RootDir, repoName, repo); isDirty {
						repo.ChangelogState = "dirty"
					}
				}
			}
		}
		if allApproved && hasSelection {
			// Save the plan and set flag to exit TUI
			release.SavePlan(m.plan)
			// Set the global dry-run flag
			releaseDryRun = m.dryRun
			m.shouldApply = true
			return m, tea.Quit
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

	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	}

	// Allow scrolling in the viewport
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m releaseTuiModel) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	const numSettings = 3 // dry-run, push, sync-deps
	
	switch {
	case key.Matches(msg, m.keys.Tab), key.Matches(msg, m.keys.Back):
		m.currentView = viewTable
		return m, nil

	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.Up) || msg.String() == "k":
		if m.settingsIndex > 0 {
			m.settingsIndex--
		}
		return m, nil

	case key.Matches(msg, m.keys.Down) || msg.String() == "j":
		if m.settingsIndex < numSettings-1 {
			m.settingsIndex++
		}
		return m, nil

	case msg.String() == " " || msg.String() == "enter" || msg.String() == "x":
		// Toggle the selected setting
		switch m.settingsIndex {
		case 0: // Dry-run
			m.dryRun = !m.dryRun
			if m.dryRun {
				m.genProgress = "🔍 DRY-RUN MODE: Commands will be shown but not executed"
			} else {
				m.genProgress = "⚠️ LIVE MODE: Commands will be executed for real"
			}
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearProgressMsg{}
			})
			
		case 1: // Push
			m.push = !m.push
			if m.push {
				m.genProgress = "🚀 PUSH ENABLED: Will push to remote after release"
			} else {
				m.genProgress = "📦 PUSH DISABLED: Changes will stay local"
			}
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearProgressMsg{}
			})
			
		case 2: // Sync deps
			m.syncDeps = !m.syncDeps
			if m.syncDeps {
				m.genProgress = "🔗 SYNC DEPS ENABLED: Will update dependency references"
			} else {
				m.genProgress = "⛓️ SYNC DEPS DISABLED: Dependencies unchanged"
			}
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearProgressMsg{}
			})
		}
		
	case key.Matches(msg, m.keys.ToggleDryRun):
		m.settingsIndex = 0 // Jump to dry-run setting
		m.dryRun = !m.dryRun
		if m.dryRun {
			m.genProgress = "🔍 DRY-RUN MODE: Commands will be shown but not executed"
		} else {
			m.genProgress = "⚠️ LIVE MODE: Commands will be executed for real"
		}
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return clearProgressMsg{}
		})
		
	case key.Matches(msg, m.keys.TogglePush):
		m.settingsIndex = 1 // Jump to push setting
		m.push = !m.push
		if m.push {
			m.genProgress = "🚀 PUSH ENABLED: Will push to remote after release"
		} else {
			m.genProgress = "📦 PUSH DISABLED: Changes will stay local"
		}
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return clearProgressMsg{}
		})
		
	case key.Matches(msg, m.keys.ToggleSyncDeps):
		m.settingsIndex = 2 // Jump to sync-deps setting
		m.syncDeps = !m.syncDeps
		if m.syncDeps {
			m.genProgress = "🔗 SYNC DEPS ENABLED: Will update dependency references"
		} else {
			m.genProgress = "⛓️ SYNC DEPS DISABLED: Dependencies unchanged"
		}
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return clearProgressMsg{}
		})
	}

	return m, nil
}

func (m releaseTuiModel) View() string {
	// Show help overlay if active
	if m.showHelp {
		helpView := m.renderHelp()
		return lipgloss.NewStyle().
			Width(m.width).
			Height(m.height).
			Align(lipgloss.Center, lipgloss.Center).
			Render(helpView)
	}
	
	switch m.currentView {
	case viewChangelog:
		return m.viewChangelog()
	case viewSettings:
		return m.viewSettings()
	default:
		return m.viewTable()
	}
}

// renderHelp renders the help popup with legend and navigation
func (m releaseTuiModel) renderHelp() string {
	// Create styles
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("241")).
		Padding(2, 3).
		Width(80).
		Align(lipgloss.Center)
	
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FF79C6")).
		MarginBottom(1)
	
	keyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#8BE9FD")).
		Bold(true)
	
	// Format a key-value pair with consistent spacing
	formatPair := func(key, desc string) string {
		// Style the key
		styledKey := keyStyle.Render(key)
		// Pad to ensure alignment (10 char width for key column)
		padding := 10 - len(key)
		if padding < 0 {
			padding = 0
		}
		return fmt.Sprintf("%s%s %s", styledKey, strings.Repeat(" ", padding), desc)
	}
	
	// Left column - Navigation and Version
	leftLines := []string{
		lipgloss.NewStyle().Bold(true).Render("Navigation:"),
		"",
		formatPair("↑/↓,j/k", "Navigate repositories"),
		formatPair("space", "Toggle selection"),
		formatPair("Tab", "Settings menu"),
		formatPair("Ctrl+A", "Select all"),
		formatPair("Ctrl+D", "Deselect all"),
		formatPair("v", "View changelog"),
		formatPair("q", "Quit"),
		"",
		lipgloss.NewStyle().Bold(true).Render("Version Bump:"),
		"",
		formatPair("m", "Major version"),
		formatPair("n", "Minor version"),
		formatPair("p", "Patch version"),
		formatPair("s", "Apply LLM suggestion"),
		"",
		lipgloss.NewStyle().Bold(true).Render("Release:"),
		"",
		formatPair("a", "Toggle approval"),
		formatPair("A", "Apply release"),
		"",
		formatPair("?", "Toggle help"),
	}
	
	// Right column - Changelog and Status
	rightLines := []string{
		lipgloss.NewStyle().Bold(true).Render("Changelog:"),
		"",
		formatPair("g", "Generate current"),
		formatPair("G", "Generate selected"),
		formatPair("e", "Edit staged"),
		formatPair("E", "Edit CHANGELOG.md"),
		formatPair("w", "Write to repo"),
		"",
		lipgloss.NewStyle().Bold(true).Render("LLM Rules:"),
		"",
		formatPair("r", "Edit rules"),
		formatPair("R", "Reset to '*'"),
		"",
		lipgloss.NewStyle().Bold(true).Render("Status:"),
		"",
		"✓         Generated",
		"⚠         Stale (new commits)",
		"⏳        Pending",
		"✓         Approved",
		"[✓]       Selected",
	}
	
	// Ensure both columns have the same number of lines
	for len(leftLines) < len(rightLines) {
		leftLines = append(leftLines, "")
	}
	for len(rightLines) < len(leftLines) {
		rightLines = append(rightLines, "")
	}
	
	// Style columns with fixed width
	leftColumn := lipgloss.NewStyle().Width(35).Render(strings.Join(leftLines, "\n"))
	rightColumn := lipgloss.NewStyle().Width(35).Render(strings.Join(rightLines, "\n"))
	
	// Join columns horizontally
	columns := lipgloss.JoinHorizontal(lipgloss.Top, leftColumn, rightColumn)
	
	// Add title
	title := titleStyle.Render("🚀 Grove Release Manager - Help")
	
	// Combine title and columns
	content := lipgloss.JoinVertical(lipgloss.Center, title, columns)
	
	return boxStyle.Render(content)
}

func (m releaseTuiModel) viewTable() string {
	headerText := "🚀 Grove Release Manager"
	var modes []string
	if m.dryRun {
		modes = append(modes, "DRY-RUN")
	}
	if m.push {
		modes = append(modes, "PUSH")
	}
	if m.syncDeps {
		modes = append(modes, "SYNC-DEPS")
	}
	if len(modes) > 0 {
		headerText += " [" + strings.Join(modes, " | ") + "]"
	}
	header := releaseTuiHeaderStyle.Render(headerText)

	// Create table
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4"))).
		Headers("", "Repository", "Branch", "Git Status", "Changes/Release", "Proposed", "Status", "Changelog")

	// Count selected repositories with changes
	selectedCount := 0
	for _, repo := range m.plan.Repos {
		if repo.Selected && repo.NextVersion != repo.CurrentVersion {
			selectedCount++
		}
	}

	// Add parent ecosystem repository first if it exists
	if m.plan.ParentVersion != "" {
		row := []string{
			"",  // No selection checkbox for parent
			"grove-ecosystem",
			"-",  // Branch
			"-",  // Git Status
			m.plan.ParentCurrentVersion,  // Changes/Release (current version)
			m.plan.ParentVersion,
			"-",
			"-", // No changelog status for parent
		}
		
		t.Row(row...)
		
		// Add separator row
		t.Row("", "───────────────", "──────", "──────────", "───────────────", "──────────", "─────────", "──────────")
	}

	// Add rows for each repository
	for i, repoName := range m.repoNames {
		repo := m.plan.Repos[repoName]

		// Format status
		var statusStr string
		if repo.CurrentVersion == repo.NextVersion {
			statusStr = "-"
		} else if repo.Status == "Approved" {
			statusStr = releaseTuiStatusApprovedStyle.Render("✓ Approved")
		} else {
			statusStr = releaseTuiStatusPendingStyle.Render("⏳ Pending")
		}

		// Selection checkbox
		checkbox := "[ ]"
		if repo.Selected {
			checkbox = "[✓]"
		}
		// Only show checkbox for repos with changes
		if repo.CurrentVersion == repo.NextVersion {
			checkbox = ""
		}

		// Format changelog status
		var changelogStatus string
		if repo.CurrentVersion == repo.NextVersion {
			changelogStatus = "-"
		} else if repo.ChangelogState == "dirty" {
			// Changelog was written and modified by user
			changelogStatus = lipgloss.NewStyle().Foreground(lipgloss.Color("#8BE9FD")).Bold(true).Render("📝 Modified")
		} else if repo.ChangelogPath != "" {
			if _, err := os.Stat(repo.ChangelogPath); err == nil {
				// Check if the changelog is stale (different commit)
				if repo.ChangelogCommit != "" {
					// Get current commit for this repo
					repoPath := filepath.Join(m.plan.RootDir, repoName)
					cmd := exec.Command("git", "rev-parse", "HEAD")
					cmd.Dir = repoPath
					currentCommitBytes, err := cmd.Output()
					if err == nil {
						currentCommit := strings.TrimSpace(string(currentCommitBytes))
						if currentCommit != repo.ChangelogCommit {
							// Changelog is stale - generated from different commit
							changelogStatus = lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Render("⚠ Stale")
						} else {
							changelogStatus = releaseTuiStatusApprovedStyle.Render("✓ Generated")
						}
					} else {
						// Couldn't get current commit, assume generated
						changelogStatus = releaseTuiStatusApprovedStyle.Render("✓ Generated")
					}
				} else {
					// No commit tracked, assume generated (for backwards compat)
					changelogStatus = releaseTuiStatusApprovedStyle.Render("✓ Generated")
				}
			} else {
				changelogStatus = releaseTuiStatusPendingStyle.Render("⏳ Pending")
			}
		} else {
			changelogStatus = releaseTuiStatusPendingStyle.Render("⏳ Pending")
		}

		// Format Git status
		var gitStatusStr string
		if !repo.IsDirty {
			gitStatusStr = lipgloss.NewStyle().Foreground(lipgloss.Color("#50FA7B")).Render("✓ Clean")
		} else {
			// Build status string with counts
			var parts []string
			if repo.StagedCount > 0 {
				parts = append(parts, fmt.Sprintf("S:%d", repo.StagedCount))
			}
			if repo.ModifiedCount > 0 {
				parts = append(parts, fmt.Sprintf("M:%d", repo.ModifiedCount))
			}
			if repo.UntrackedCount > 0 {
				parts = append(parts, fmt.Sprintf("?:%d", repo.UntrackedCount))
			}
			gitStatusStr = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB86C")).Render(strings.Join(parts, " "))
		}

		// Format Changes/Release column
		changesReleaseStr := repo.CurrentVersion
		if repo.CommitsSinceLastTag > 0 {
			changesReleaseStr = fmt.Sprintf("%s (↑%d)", repo.CurrentVersion, repo.CommitsSinceLastTag)
		}

		row := []string{
			checkbox,
			repoName,
			repo.Branch,
			gitStatusStr,
			changesReleaseStr,
			repo.NextVersion,
			statusStr,
			changelogStatus,
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
						if repoPlan.Selected && repoPlan.CurrentVersion != repoPlan.NextVersion {
							releaseInfo += fmt.Sprintf("  • %s: %s → %s (%s)\n", 
								repo, repoPlan.CurrentVersion, repoPlan.NextVersion, repoPlan.SelectedBump)
						}
					}
				}
			}
		}
		if selectedCount > 0 {
			releaseInfo += fmt.Sprintf("\n%d repositories selected for release.\n", selectedCount)
		} else {
			releaseInfo += "\nNo repositories selected. Use space to select repositories.\n"
		}
	}


	// Footer with help hint
	var footer string
	if m.showHelp {
		footer = ""
	} else {
		footer = releaseTuiHelpStyle.Render("Press ? for help • q to quit")
	}

	// Show suggestion reasoning for selected repo
	var reasoning string
	if m.selectedIndex < len(m.repoNames) {
		repo := m.plan.Repos[m.repoNames[m.selectedIndex]]
		if repo.SuggestionReasoning != "" && repo.CurrentVersion != repo.NextVersion {
			// Create styled version bump display
			var bumpType string
			switch strings.ToLower(repo.SuggestedBump) {
			case "major":
				bumpType = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF5555")).Render("MAJOR")
			case "minor":
				bumpType = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFB86C")).Render("MINOR")
			case "patch":
				bumpType = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#50FA7B")).Render("PATCH")
			default:
				bumpType = lipgloss.NewStyle().Bold(true).Render(strings.ToUpper(repo.SuggestedBump))
			}
			
			versionChange := lipgloss.NewStyle().Bold(true).Render(
				fmt.Sprintf("%s → %s", repo.CurrentVersion, repo.NextVersion),
			)
			
			reasoning = fmt.Sprintf("\n\n💡 Suggested: %s version bump (%s)\n   %s", 
				bumpType, versionChange, repo.SuggestionReasoning)
		}
	}

	// Show generation progress if generating
	var progress string
	if m.genProgress != "" {
		spinnerChars := []string{"⣷", "⣯", "⣟", "⡿", "⢿", "⣻", "⣽", "⣾"}
		spinner := spinnerChars[m.spinner%len(spinnerChars)]
		progress = fmt.Sprintf("\n\n%s %s", spinner, m.genProgress)
		
		// Show queue status if batch generating
		if len(m.genQueue) > 0 {
			remaining := m.genQueue[1:]
			if len(remaining) > 0 {
				names := strings.Join(remaining, ", ")
				if len(names) > 60 {
					names = names[:57] + "..."
				}
				progress += fmt.Sprintf("\n   Queued: %s", names)
			}
		}
	}

	// Combine all content that should be scrollable
	fullContent := fmt.Sprintf("%s%s%s%s", tableStr, releaseInfo, reasoning, progress)
	
	// Update viewport with the content
	m.viewport.SetContent(fullContent)
	
	// Return header, viewport, and footer
	return fmt.Sprintf("%s\n\n%s\n\n%s", header, m.viewport.View(), footer)
}

func (m releaseTuiModel) viewChangelog() string {
	header := releaseTuiChangelogHeaderStyle.Render(fmt.Sprintf("📝 Changelog Preview: %s", m.repoNames[m.selectedIndex]))
	
	help := releaseTuiHelpStyle.Render("a: approve • esc: back • q: quit")

	return fmt.Sprintf("%s\n\n%s\n\n%s", header, m.viewport.View(), help)
}

func (m releaseTuiModel) viewSettings() string {
	headerText := "⚙️ Release Settings"
	header := releaseTuiHeaderStyle.Render(headerText)
	
	// Create toggle items with current states
	toggleStyle := lipgloss.NewStyle().
		Padding(1, 2).
		MarginBottom(1)
	
	activeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("42")).
		Bold(true)
		
	inactiveStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241"))
	
	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("226")).
		Bold(true)
	
	// Build settings list
	var settings []string
	
	// Helper to format each setting
	formatSetting := func(index int, key, title, desc string, enabled bool) string {
		state := inactiveStyle.Render("OFF")
		if enabled {
			state = activeStyle.Render("ON")
		}
		
		selector := "  "
		if index == m.settingsIndex {
			selector = "▸ "
			// Highlight the key when selected
			key = selectedStyle.Render("[" + key + "]")
		} else {
			key = "[" + key + "]"
		}
		
		line1 := fmt.Sprintf("%s%s %s: %s", selector, key, title, state)
		line2 := fmt.Sprintf("      %s", desc)
		
		if index == m.settingsIndex {
			// Highlight the entire setting when selected
			return selectedStyle.Render(line1) + "\n" + desc
		}
		return line1 + "\n" + line2
	}
	
	// Dry run toggle
	settings = append(settings, formatSetting(0, "d", "Dry Run Mode", "Preview commands without executing them", m.dryRun))
	
	// Push toggle
	settings = append(settings, formatSetting(1, "P", "Push to Remote", "Push changes to remote repositories after release", m.push))
	
	// Sync deps toggle  
	settings = append(settings, formatSetting(2, "S", "Sync Dependencies", "Update dependency versions across repositories", m.syncDeps))
	
	content := toggleStyle.Render(strings.Join(settings, "\n\n"))
	
	// Progress message if any
	progressMsg := ""
	if m.genProgress != "" {
		progressMsg = "\n\n" + lipgloss.NewStyle().
			Foreground(lipgloss.Color("226")).
			Render(m.genProgress)
	}
	
	help := releaseTuiHelpStyle.Render("↑/↓,j/k: navigate • space/enter: toggle • tab/esc: back • q: quit")
	
	// Combine all elements
	return fmt.Sprintf("%s\n\n%s%s\n\n%s", header, content, progressMsg, help)
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
type changelogGeneratedMsg struct{
	repoName            string
	changelogPath       string
	changelogCommit     string
	suggestion          string
	suggestionReasoning string
	err                 error
}

type clearProgressMsg struct{}
type spinnerTickMsg struct{}

// Command to tick the spinner animation
func tickSpinner() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}


// Command to edit rules file in external editor
func editRulesCmd(rulesPath string) tea.Cmd {
	return tea.ExecProcess(exec.Command(getEditor(), rulesPath), func(err error) tea.Msg {
		if err != nil {
			return fmt.Errorf("failed to edit rules file: %w", err)
		}
		return nil
	})
}

// Command to edit a file in external editor
func editFileCmd(filePath string) tea.Cmd {
	return tea.ExecProcess(exec.Command(getEditor(), filePath), func(err error) tea.Msg {
		if err != nil {
			return fmt.Errorf("failed to edit file: %w", err)
		}
		return nil
	})
}

// Get the user's preferred editor
func getEditor() string {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim" // Default to vim if EDITOR is not set
	}
	return editor
}

// Command to generate changelog for a specific repository
func generateChangelogCmd(rootDir, repoName string, repo *release.RepoReleasePlan) tea.Cmd {
	return func() tea.Msg {
		// Get repository path
		wsPath := filepath.Join(rootDir, repoName)
		
		// Get current commit hash
		commitCmd := exec.Command("git", "rev-parse", "HEAD")
		commitCmd.Dir = wsPath
		commitOutput, err := commitCmd.Output()
		if err != nil {
			return changelogGeneratedMsg{
				repoName: repoName,
				err:      fmt.Errorf("failed to get current commit: %w", err),
			}
		}
		currentCommit := strings.TrimSpace(string(commitOutput))
		
		// Get last tag
		lastTag, _ := getLastTag(wsPath)
		
		commitRange := "HEAD"
		if lastTag != "" {
			commitRange = fmt.Sprintf("%s..HEAD", lastTag)
		}
		
		// Gather git context with commit hashes
		// Format includes both full and short hash for easy reference
		logCmd := exec.Command("git", "log", commitRange, "--pretty=format:commit %H (%h)%nAuthor: %an <%ae>%nDate: %ad%nCommit: %cn <%ce>%nCommitDate: %cd%n%n    %s%n%n%b%n")
		logCmd.Dir = wsPath
		logOutput, err := logCmd.CombinedOutput()
		if err != nil {
			return changelogGeneratedMsg{
				repoName: repoName,
				err:      fmt.Errorf("failed to get git log: %w", err),
			}
		}
		
		diffCmd := exec.Command("git", "diff", "--stat", commitRange)
		diffCmd.Dir = wsPath
		diffOutput, err := diffCmd.CombinedOutput()
		if err != nil {
			return changelogGeneratedMsg{
				repoName: repoName,
				err:      fmt.Errorf("failed to get git diff: %w", err),
			}
		}
		
		gitContext := fmt.Sprintf("GIT LOG:\n%s\n\nGIT DIFF STAT:\n%s", string(logOutput), string(diffOutput))
		
		// Generate changelog with LLM (skip interactive prompts in TUI mode)
		result, err := generateChangelogWithLLMInteractive(gitContext, repo.NextVersion, wsPath, true)
		if err != nil {
			return changelogGeneratedMsg{
				repoName: repoName,
				err:      fmt.Errorf("failed to generate LLM changelog: %w", err),
			}
		}
		
		// Save changelog to staging
		stagingDir, _ := getStagingDirPath()
		changelogPath := filepath.Join(stagingDir, repoName, "CHANGELOG.md")
		if err := os.MkdirAll(filepath.Dir(changelogPath), 0755); err != nil {
			return changelogGeneratedMsg{
				repoName: repoName,
				err:      fmt.Errorf("failed to create changelog directory: %w", err),
			}
		}
		
		if err := os.WriteFile(changelogPath, []byte(result.Changelog), 0644); err != nil {
			return changelogGeneratedMsg{
				repoName: repoName,
				err:      fmt.Errorf("failed to write changelog: %w", err),
			}
		}
		
		// Return the message with all the information including LLM suggestion and commit
		return changelogGeneratedMsg{
			repoName:            repoName,
			changelogPath:       changelogPath,
			changelogCommit:     currentCommit,
			suggestion:          result.Suggestion,
			suggestionReasoning: result.Justification,
			err:                 nil,
		}
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
	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("error running TUI: %w", err)
	}
	
	// Check if we should apply the release
	if model, ok := finalModel.(releaseTuiModel); ok && model.shouldApply {
		// Set the global flags from the TUI state
		releasePush = model.push
		releaseSyncDeps = model.syncDeps
		releaseDryRun = model.dryRun
		
		// Exit the TUI and run the release in the terminal
		// runReleaseApply will print its own header
		return runReleaseApply(ctx)
	}
	
	return nil
}

// checkChangelogDirty checks if a changelog file has been modified since we wrote it
func checkChangelogDirty(rootDir, repoName string, repo *release.RepoReleasePlan) bool {
	if repo.ChangelogHash == "" {
		return false // No hash stored, can't check
	}
	
	changelogPath := filepath.Join(rootDir, repoName, "CHANGELOG.md")
	currentContent, err := os.ReadFile(changelogPath)
	if err != nil {
		return false // Can't read file, assume clean
	}
	
	// Calculate current hash
	currentHash := sha256.Sum256(currentContent)
	currentHashStr := fmt.Sprintf("%x", currentHash)
	
	// Compare with stored hash
	isDirty := currentHashStr != repo.ChangelogHash
	
	// If dirty, validate that it still contains the expected version header
	if isDirty && repo.NextVersion != "" {
		// Check if the changelog contains the expected version header
		expectedHeader := fmt.Sprintf("## %s", repo.NextVersion)
		if !strings.Contains(string(currentContent), expectedHeader) {
			// Warning: changelog was modified but doesn't contain expected version
			// This is still considered dirty, but might need a warning
			fmt.Printf("⚠️  Warning: Changelog for %s was modified but doesn't contain expected version header '%s'\n", 
				repoName, expectedHeader)
		}
	}
	
	return isDirty
}

// newReleaseTuiCmd creates the 'grove release tui' subcommand
func newReleaseTuiCmd() *cobra.Command {
	var forceFresh bool
	
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
			
			// Clear stale plan if requested
			if forceFresh {
				fmt.Println("Clearing existing release plan...")
				if err := release.ClearPlan(); err != nil {
					return fmt.Errorf("failed to clear plan: %w", err)
				}
			}
			
			return runReleaseTUI(ctx)
		},
	}
	
	cmd.Flags().BoolVar(&forceFresh, "fresh", false, "Clear any existing release plan and start fresh")
	
	return cmd
}

