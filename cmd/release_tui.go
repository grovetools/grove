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
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/logging"
	tablecomponent "github.com/grovetools/core/tui/components/table"
	"github.com/grovetools/core/tui/components/help"
	"github.com/grovetools/core/tui/keymap"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/release"
	"github.com/spf13/cobra"
)

// TUI logger for debugging
var tuiLog = logging.NewUnifiedLogger("grove-meta.release-tui")


// Key bindings for the release TUI
type releaseKeyMap struct {
	keymap.Base
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
	Back            key.Binding
}

var releaseKeys = releaseKeyMap{
	Base: keymap.NewBase(),
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
	Back: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back"),
	),
}

// ShortHelp returns key bindings for the short help view
func (k releaseKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{
		k.Toggle, k.Approve, k.Base.Quit,
	}
}

// FullHelp returns all key bindings for the full help view
func (k releaseKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		// Navigation
		{k.Base.Up, k.Base.Down, k.Tab},
		// Selection
		{k.Toggle, k.SelectAll, k.DeselectAll},
		// Version bumps
		{k.SelectMajor, k.SelectMinor, k.SelectPatch, k.ApplySuggestion},
		// Changelog
		{k.ViewChangelog, k.EditChangelog, k.EditRepoChangelog},
		{k.GenerateChangelog, k.GenerateAll, k.WriteChangelog},
		// LLM rules
		{k.EditRules, k.ResetRules},
		// Settings
		{k.ToggleDryRun, k.TogglePush, k.ToggleSyncDeps},
		// Actions
		{k.Approve, k.Back, k.Base.Quit, k.Base.Help},
	}
}

// Sections returns grouped sections of key bindings for the full help view.
// Only includes sections that the release TUI actually implements.
func (k releaseKeyMap) Sections() []keymap.Section {
	// Customize navigation for release TUI
	nav := k.Base.NavigationSection()
	nav.Bindings = []key.Binding{k.Up, k.Down, k.Tab}

	return []keymap.Section{
		nav,
		{
			Name:     "Selection",
			Bindings: []key.Binding{k.Toggle, k.SelectAll, k.DeselectAll},
		},
		{
			Name:     "Version Bumps",
			Bindings: []key.Binding{k.SelectMajor, k.SelectMinor, k.SelectPatch, k.ApplySuggestion},
		},
		{
			Name:     "Changelog",
			Bindings: []key.Binding{k.ViewChangelog, k.EditChangelog, k.EditRepoChangelog, k.GenerateChangelog, k.GenerateAll, k.WriteChangelog},
		},
		{
			Name:     "LLM Rules",
			Bindings: []key.Binding{k.EditRules, k.ResetRules},
		},
		{
			Name:     "Settings",
			Bindings: []key.Binding{k.ToggleDryRun, k.TogglePush, k.ToggleSyncDeps},
		},
		{
			Name:     "Actions",
			Bindings: []key.Binding{k.Approve, k.Back},
		},
		k.Base.SystemSection(),
	}
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
	help          help.Model
	width         int
	height        int
	err           error
	applyOutput   string
	applying      bool
	generating    bool          // Whether changelog generation is in progress
	genProgress   string        // Progress message for generation
	spinner       int           // Spinner animation frame
	genQueue      []string      // Queue of repos being generated (tracks pending)
	genCurrent    string        // Current repo being generated (for single mode display)
	genCompleted  int           // Number of completed generations
	genTotal      int           // Total number of repos being generated (for parallel mode)
	dryRun        bool          // Whether to run in dry-run mode
	shouldApply   bool          // Flag to indicate we should exit and apply
	push          bool          // Whether to push to remote
	settingsIndex int           // Currently selected setting in settings view
	llmModel      string        // LLM model being used for changelog generation
	cxRulesPath   string        // Path to cx rules being used
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

	// Determine LLM model from config (supports both grove.yml and grove.toml)
	llmModel := "gemini-1.5-flash-latest" // Default
	if cfg, err := config.LoadFrom(plan.RootDir); err == nil {
		var llmCfg struct {
			DefaultModel string `yaml:"default_model"`
		}
		if err := cfg.UnmarshalExtension("llm", &llmCfg); err == nil && llmCfg.DefaultModel != "" {
			llmModel = llmCfg.DefaultModel
		}
	}

	// Look for cx rules path (check first repo with .grove/rules or .cx/docs.rules)
	cxRulesPath := ""
	for _, repoName := range repoNames {
		rulesPath := filepath.Join(plan.RootDir, repoName, ".grove", "rules")
		if _, err := os.Stat(rulesPath); err == nil {
			cxRulesPath = ".grove/rules"
			break
		}
		cxPath := filepath.Join(plan.RootDir, repoName, ".cx", "docs.rules")
		if _, err := os.Stat(cxPath); err == nil {
			cxRulesPath = ".cx/docs.rules"
			break
		}
	}
	if cxRulesPath == "" {
		cxRulesPath = "(none)"
	}

	return releaseTuiModel{
		plan:        plan,
		keys:        releaseKeys,
		currentView: viewTable,
		repoNames:   repoNames,
		viewport:    viewport.New(80, 20),
		help:        help.New(releaseKeys),
		dryRun:      true, // Start in dry-run mode for safety
		llmModel:    llmModel,
		cxRulesPath: cxRulesPath,
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
		// Update help component size for proper rendering
		m.help.SetSize(msg.Width, msg.Height)
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
			// Remove completed repo from queue (parallel mode - all already running)
			for i, name := range m.genQueue {
				if name == msg.repoName {
					m.genQueue = append(m.genQueue[:i], m.genQueue[i+1:]...)
					m.genCompleted++
					break
				}
			}

			// Update progress (parallel mode - just track completion)
			if len(m.genQueue) > 0 {
				m.genProgress = fmt.Sprintf("Generating changelogs... (%d/%d completed)", m.genCompleted, m.genTotal)
			} else {
				// All done
				m.generating = false
				m.genProgress = fmt.Sprintf("%s Generated changelogs for %d repositories", theme.IconSuccess, m.genCompleted)
				m.genQueue = nil
				m.genCurrent = ""
				m.genCompleted = 0
				m.genTotal = 0
				return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
					return clearProgressMsg{}
				})
			}
			return m, nil
		} else {
			// Single generation mode
			m.generating = false
			if msg.err != nil {
				m.genProgress = fmt.Sprintf("%s Failed to generate changelog for %s: %v", theme.IconError, msg.repoName, msg.err)
			} else {
				m.genProgress = fmt.Sprintf("%s Changelog generated for %s", theme.IconSuccess, msg.repoName)
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
	if m.help.ShowAll {
		switch msg.String() {
		case "?", "q", "esc", "ctrl+c":
			m.help.Toggle()
			return m, nil
		default:
			// Ignore ALL other keys when help is shown, including 'A'
			return m, nil
		}
	}
	
	switch {
	case key.Matches(msg, m.keys.Base.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.Base.Up):
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
		return m, nil

	case key.Matches(msg, m.keys.Base.Down):
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
			m.genProgress = fmt.Sprintf("%s Selected %d repositories", theme.IconSuccess, selectedCount)
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
			m.genProgress = fmt.Sprintf("%s Deselected %d repositories", theme.IconSuccess, deselectedCount)
			// Save the updated plan
			release.SavePlan(m.plan)
			return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
				return clearProgressMsg{}
			})
		}
		return m, nil

	case key.Matches(msg, m.keys.SelectMajor):
		if m.plan.Type != "rc" && m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			if repo, ok := m.plan.Repos[repoName]; ok {
				oldVersion := repo.NextVersion
				repo.SelectedBump = "major"
				repo.NextVersion = calculateNextVersion(repo.CurrentVersion, "major")
				// Update the staged changelog if it exists
				if repo.ChangelogPath != "" {
					if err := updateStagedChangelogVersion(repo.ChangelogPath, oldVersion, repo.NextVersion); err != nil {
						m.genProgress = fmt.Sprintf("%s Failed to update changelog version: %v", theme.IconWarning, err)
					}
				}
				// Also update the repo's CHANGELOG.md if it has the old version
				if err := updateRepoChangelogVersion(m.plan.RootDir, repoName, oldVersion, repo.NextVersion); err != nil {
					m.genProgress = fmt.Sprintf("%s Failed to update repo changelog: %v", theme.IconWarning, err)
				}
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.SelectMinor):
		if m.plan.Type != "rc" && m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			if repo, ok := m.plan.Repos[repoName]; ok {
				oldVersion := repo.NextVersion
				repo.SelectedBump = "minor"
				repo.NextVersion = calculateNextVersion(repo.CurrentVersion, "minor")
				// Update the staged changelog if it exists
				if repo.ChangelogPath != "" {
					if err := updateStagedChangelogVersion(repo.ChangelogPath, oldVersion, repo.NextVersion); err != nil {
						m.genProgress = fmt.Sprintf("%s Failed to update changelog version: %v", theme.IconWarning, err)
					}
				}
				// Also update the repo's CHANGELOG.md if it has the old version
				if err := updateRepoChangelogVersion(m.plan.RootDir, repoName, oldVersion, repo.NextVersion); err != nil {
					m.genProgress = fmt.Sprintf("%s Failed to update repo changelog: %v", theme.IconWarning, err)
				}
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.SelectPatch):
		if m.plan.Type != "rc" && m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			if repo, ok := m.plan.Repos[repoName]; ok {
				oldVersion := repo.NextVersion
				repo.SelectedBump = "patch"
				repo.NextVersion = calculateNextVersion(repo.CurrentVersion, "patch")
				// Update the staged changelog if it exists
				if repo.ChangelogPath != "" {
					if err := updateStagedChangelogVersion(repo.ChangelogPath, oldVersion, repo.NextVersion); err != nil {
						m.genProgress = fmt.Sprintf("%s Failed to update changelog version: %v", theme.IconWarning, err)
					}
				}
				// Also update the repo's CHANGELOG.md if it has the old version
				if err := updateRepoChangelogVersion(m.plan.RootDir, repoName, oldVersion, repo.NextVersion); err != nil {
					m.genProgress = fmt.Sprintf("%s Failed to update repo changelog: %v", theme.IconWarning, err)
				}
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.ApplySuggestion):
		// Apply the LLM's suggested version bump
		if m.plan.Type != "rc" && m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			if repo, ok := m.plan.Repos[repoName]; ok {
				// Only apply if there's a suggestion
				if repo.SuggestedBump != "" {
					oldVersion := repo.NextVersion
					repo.SelectedBump = repo.SuggestedBump
					repo.NextVersion = calculateNextVersion(repo.CurrentVersion, repo.SuggestedBump)
					// Update the staged changelog if it exists
					if repo.ChangelogPath != "" {
						if err := updateStagedChangelogVersion(repo.ChangelogPath, oldVersion, repo.NextVersion); err != nil {
							m.genProgress = fmt.Sprintf("%s Failed to update changelog version: %v", theme.IconWarning, err)
							return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
								return clearProgressMsg{}
							})
						}
					}
					// Also update the repo's CHANGELOG.md if it has the old version
					if err := updateRepoChangelogVersion(m.plan.RootDir, repoName, oldVersion, repo.NextVersion); err != nil {
						m.genProgress = fmt.Sprintf("%s Failed to update repo changelog: %v", theme.IconWarning, err)
						return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
							return clearProgressMsg{}
						})
					}
					m.genProgress = fmt.Sprintf("%s Applied %s version bump to %s", theme.IconSuccess,
						strings.ToUpper(repo.SuggestedBump), repoName)
					// Save the updated plan
					release.SavePlan(m.plan)
					// Clear progress after a moment
					return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
						return clearProgressMsg{}
					})
				} else {
					m.genProgress = fmt.Sprintf("%s No suggestion available. Generate changelog first with 'g'", theme.IconWarning)
					return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
						return clearProgressMsg{}
					})
				}
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.EditChangelog):
		// Edit the staged changelog for the selected repository
		if m.plan.Type == "full" && m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			repoPath := filepath.Join(m.plan.RootDir, repoName)
			if repo, ok := m.plan.Repos[repoName]; ok {
				if repo.ChangelogPath != "" {
					// Open staged changelog in editor with repo as working directory
					return m, editFileCmd(repo.ChangelogPath, repoPath)
				} else {
					// No staged changelog, try to edit the repo's CHANGELOG.md
					changelogPath := filepath.Join(repoPath, "CHANGELOG.md")

					// Check if CHANGELOG.md exists
					if _, err := os.Stat(changelogPath); err == nil {
						return m, editFileCmd(changelogPath, repoPath)
					} else {
						m.genProgress = fmt.Sprintf("%s No changelog found. Generate one first with 'g'", theme.IconWarning)
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
		if m.plan.Type == "full" && m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			repoPath := filepath.Join(m.plan.RootDir, repoName)
			changelogPath := filepath.Join(repoPath, "CHANGELOG.md")

			// Check if CHANGELOG.md exists, create if not
			if _, err := os.Stat(changelogPath); os.IsNotExist(err) {
				// Create with a basic template
				template := fmt.Sprintf("# Changelog\n\nAll notable changes to %s will be documented in this file.\n\nThe format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),\nand this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).\n\n", repoName)
				if err := os.WriteFile(changelogPath, []byte(template), 0644); err != nil {
					m.genProgress = fmt.Sprintf("%s Failed to create CHANGELOG.md: %v", theme.IconError, err)
					return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
						return clearProgressMsg{}
					})
				}
			}

			// Open in editor with repo as working directory
			return m, editFileCmd(changelogPath, repoPath)
		}
		return m, nil

	case key.Matches(msg, m.keys.ViewChangelog):
		if m.plan.Type == "full" && m.selectedIndex < len(m.repoNames) {
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
		if m.plan.Type == "full" && m.selectedIndex < len(m.repoNames) {
			repoName := m.repoNames[m.selectedIndex]
			if repo, ok := m.plan.Repos[repoName]; ok {
				if repo.ChangelogPath != "" {
					// Read the staged changelog
					stagedContent, err := os.ReadFile(repo.ChangelogPath)
					if err != nil {
						m.genProgress = fmt.Sprintf("%s Failed to read staged changelog: %v", theme.IconError, err)
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
						m.genProgress = fmt.Sprintf("%s Failed to write changelog: %v", theme.IconError, err)
					} else {
						// Calculate and store hash of the written content
						hash := sha256.Sum256(newContent)
						repo.ChangelogHash = fmt.Sprintf("%x", hash)
						repo.ChangelogState = "clean"
						release.SavePlan(m.plan)
						
						m.genProgress = fmt.Sprintf("%s Changelog written to %s", theme.IconSuccess, changelogPath)
						
						// Open in editor for further editing
						return m, tea.Batch(
							editFileCmd(changelogPath, repoPath),
							tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
								return clearProgressMsg{}
							}),
						)
					}
					
					return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
						return clearProgressMsg{}
					})
				} else {
					m.genProgress = fmt.Sprintf("%s No changelog generated yet. Press 'g' to generate first.", theme.IconWarning)
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
			m.genProgress = fmt.Sprintf("%s Reset rules to '*' for %d repositories", theme.IconSuccess, resetCount)
		} else {
			m.genProgress = fmt.Sprintf("%s No repositories selected", theme.IconWarning)
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

			// Open in editor with repo as working directory
			return m, editRulesCmd(rulesPath, repoPath)
		}
		return m, nil

	case key.Matches(msg, m.keys.GenerateChangelog):
		ctx := context.Background()
		tuiLog.Debug("GenerateChangelog key pressed").
			Field("plan_type", m.plan.Type).
			Field("selected_index", m.selectedIndex).
			Field("repo_count", len(m.repoNames)).
			Field("generating", m.generating).
			StructuredOnly(). // Don't output to TUI
			Log(ctx)

		if m.plan.Type == "full" && m.selectedIndex < len(m.repoNames) && !m.generating {
			repoName := m.repoNames[m.selectedIndex]
			tuiLog.Debug("Checking repo for changelog generation").
				Field("repo_name", repoName).
				StructuredOnly().
				Log(ctx)

			if repo, ok := m.plan.Repos[repoName]; ok {
				tuiLog.Debug("Repo found in plan").
					Field("repo_name", repoName).
					Field("current_version", repo.CurrentVersion).
					Field("next_version", repo.NextVersion).
					Field("has_changes", repo.CurrentVersion != repo.NextVersion).
					StructuredOnly().
					Log(ctx)

				// Only generate for repos with changes
				if repo.CurrentVersion != repo.NextVersion {
					tuiLog.Info("Starting changelog generation").
						Field("repo_name", repoName).
						StructuredOnly().
						Log(ctx)
					m.generating = true
					m.genProgress = fmt.Sprintf("Generating changelog for %s", repoName)
					// Start spinner animation
					return m, tea.Batch(
						generateChangelogCmd(m.plan.RootDir, repoName, repo),
						tickSpinner(),
					)
				} else {
					tuiLog.Debug("Skipping - no changes detected").
						Field("repo_name", repoName).
						StructuredOnly().
						Log(ctx)
				}
			} else {
				tuiLog.Warn("Repo not found in plan").
					Field("repo_name", repoName).
					StructuredOnly().
					Log(ctx)
			}
		} else {
			tuiLog.Debug("Conditions not met for changelog generation").
				Field("is_full_plan", m.plan.Type == "full").
				Field("valid_index", m.selectedIndex < len(m.repoNames)).
				Field("not_generating", !m.generating).
				StructuredOnly().
				Log(ctx)
		}
		return m, nil

	case key.Matches(msg, m.keys.GenerateAll):
		if m.plan.Type == "full" && !m.generating {
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
				m.genTotal = len(queue)
				m.genCompleted = 0
				m.genCurrent = ""
				m.genProgress = fmt.Sprintf("Generating changelogs for %d repositories in parallel...", len(queue))

				// Launch ALL changelog generations in parallel
				var cmds []tea.Cmd
				cmds = append(cmds, tickSpinner())
				for _, repoName := range queue {
					if repo, ok := m.plan.Repos[repoName]; ok {
						cmds = append(cmds, generateChangelogCmd(m.plan.RootDir, repoName, repo))
					}
				}
				return m, tea.Batch(cmds...)
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
						m.genProgress = fmt.Sprintf("%s Unapproved %s", theme.IconPending, repoName)
					} else {
						repo.Status = "Approved"
						m.genProgress = fmt.Sprintf("%s Approved %s for release", theme.IconSuccess, repoName)
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

	case key.Matches(msg, m.keys.Base.Help):
		m.help.Toggle()
		return m, nil

	}

	return m, nil
}

func (m releaseTuiModel) updateChangelog(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Back):
		m.currentView = viewTable
		return m, nil

	case key.Matches(msg, m.keys.Base.Quit):
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

	case key.Matches(msg, m.keys.Base.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.Base.Up) || msg.String() == "k":
		if m.settingsIndex > 0 {
			m.settingsIndex--
		}
		return m, nil

	case key.Matches(msg, m.keys.Base.Down) || msg.String() == "j":
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
				m.genProgress = fmt.Sprintf("%s DRY-RUN MODE: Commands will be shown but not executed", theme.IconFilter)
			} else {
				m.genProgress = fmt.Sprintf("%s LIVE MODE: Commands will be executed for real", theme.IconWarning)
			}
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearProgressMsg{}
			})
			
		case 1: // Push
			m.push = !m.push
			if m.push {
				m.genProgress = fmt.Sprintf("%s PUSH ENABLED: Will push to remote after release", theme.IconSparkle)
			} else {
				m.genProgress = fmt.Sprintf("%s PUSH DISABLED: Changes will stay local", theme.IconArchive)
			}
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearProgressMsg{}
			})
		}
		
	case key.Matches(msg, m.keys.ToggleDryRun):
		m.settingsIndex = 0 // Jump to dry-run setting
		m.dryRun = !m.dryRun
		if m.dryRun {
			m.genProgress = fmt.Sprintf("%s DRY-RUN MODE: Commands will be shown but not executed", theme.IconFilter)
		} else {
			m.genProgress = fmt.Sprintf("%s LIVE MODE: Commands will be executed for real", theme.IconWarning)
		}
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return clearProgressMsg{}
		})
		
	case key.Matches(msg, m.keys.TogglePush):
		m.settingsIndex = 1 // Jump to push setting
		m.push = !m.push
		if m.push {
			m.genProgress = fmt.Sprintf("%s PUSH ENABLED: Will push to remote after release", theme.IconSparkle)
		} else {
			m.genProgress = fmt.Sprintf("%s PUSH DISABLED: Changes will stay local", theme.IconArchive)
		}
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return clearProgressMsg{}
		})
		
	}

	return m, nil
}

func (m releaseTuiModel) View() string {
	// Show help overlay if active
	if m.help.ShowAll {
		return m.help.View()
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


func (m releaseTuiModel) viewTable() string {
	headerText := theme.IconSparkle + " Grove Release Manager"
	var modes []string
	if m.dryRun {
		modes = append(modes, "DRY-RUN")
	}
	if m.push {
		modes = append(modes, "PUSH")
	}
	if len(modes) > 0 {
		headerText += " [" + strings.Join(modes, " | ") + "]"
	}
	header := theme.DefaultTheme.Header.Render(headerText)

	// LLM info line
	llmInfo := theme.DefaultTheme.Muted.Render(fmt.Sprintf("  LLM: %s  |  Rules: %s", m.llmModel, m.cxRulesPath))

	// Create table
	t := tablecomponent.NewStyledTable().
		Border(lipgloss.NormalBorder()).
		BorderStyle(theme.DefaultTheme.Muted)
	
	// Conditionally add headers based on plan type
	// First column is arrow indicator, second is checkbox
	if m.plan.Type == "full" {
		t.Headers(" ", " ", "Repository", "Branch", "Git Status", "Changes/Release", "Proposed", "Status", "Changelog")
	} else {
		t.Headers(" ", " ", "Repository", "Branch", "Git Status", "Changes/Release", "Proposed", "Status")
	}

	// Count selected repositories with changes
	selectedCount := 0
	for _, repo := range m.plan.Repos {
		if repo.Selected && repo.NextVersion != repo.CurrentVersion {
			selectedCount++
		}
	}

	// Add parent ecosystem repository first if it exists
	if m.plan.ParentVersion != "" {
		var row []string
		if m.plan.Type == "full" {
			row = []string{
				" ",  // Arrow indicator (none for parent)
				" ",  // No selection checkbox for parent
				"grove-ecosystem",
				"-",  // Branch
				"-",  // Git Status
				m.plan.ParentCurrentVersion,  // Changes/Release (current version)
				m.plan.ParentVersion,
				"-",
				"-", // No changelog status for parent
			}
		} else {
			row = []string{
				" ",  // Arrow indicator (none for parent)
				" ",  // No selection checkbox for parent
				"grove-ecosystem",
				"-",  // Branch
				"-",  // Git Status
				m.plan.ParentCurrentVersion,  // Changes/Release (current version)
				m.plan.ParentVersion,
				"-",
			}
		}

		t.Row(row...)

		// Add separator row
		if m.plan.Type == "full" {
			t.Row(" ", " ", "───────────────", "──────", "──────────", "───────────────", "──────────", "─────────", "──────────")
		} else {
			t.Row(" ", " ", "───────────────", "──────", "──────────", "───────────────", "──────────", "─────────")
		}
	}

	// Add rows for each repository
	for i, repoName := range m.repoNames {
		repo := m.plan.Repos[repoName]

		// Format status
		var statusStr string
		if repo.CurrentVersion == repo.NextVersion {
			statusStr = "-"
		} else if repo.Status == "Approved" {
			statusStr = theme.DefaultTheme.Success.Render(theme.IconSuccess + " Approved")
		} else {
			statusStr = theme.DefaultTheme.Warning.Render(theme.IconPending + " Pending")
		}

		// Selection checkbox
		checkbox := theme.IconUnselect
		if repo.Selected {
			checkbox = theme.IconSuccess
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
			changelogStatus = theme.DefaultTheme.Info.Copy().Bold(true).Render(theme.IconNote + " Modified")
		} else if repoChangelogVersion := getRepoChangelogVersion(m.plan.RootDir, repoName); repoChangelogVersion != "" {
			// Repo's CHANGELOG.md is dirty and has a version entry
			changelogStatus = theme.DefaultTheme.Success.Render(fmt.Sprintf("%s In Repo (%s)", theme.IconSuccess, repoChangelogVersion))
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
							changelogStatus = theme.DefaultTheme.Warning.Render(theme.IconWarning + " Stale")
						} else {
							changelogStatus = theme.DefaultTheme.Success.Render(theme.IconSuccess + " Generated")
						}
					} else {
						// Couldn't get current commit, assume generated
						changelogStatus = theme.DefaultTheme.Success.Render(theme.IconSuccess + " Generated")
					}
				} else {
					// No commit tracked, assume generated (for backwards compat)
					changelogStatus = theme.DefaultTheme.Success.Render(theme.IconSuccess + " Generated")
				}
			} else {
				changelogStatus = theme.DefaultTheme.Warning.Render(theme.IconPending + " Pending")
			}
		} else {
			changelogStatus = theme.DefaultTheme.Warning.Render(theme.IconPending + " Pending")
		}

		// Format Git status
		var gitStatusStr string
		if !repo.IsDirty {
			gitStatusStr = theme.DefaultTheme.Success.Render(theme.IconSuccess + " Clean")
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
			gitStatusStr = theme.DefaultTheme.Warning.Render(strings.Join(parts, " "))
		}

		// Format Changes/Release column
		changesReleaseStr := repo.CurrentVersion
		if repo.CommitsSinceLastTag > 0 {
			changesReleaseStr = fmt.Sprintf("%s (%s%d)", repo.CurrentVersion, theme.IconArrowUp, repo.CommitsSinceLastTag)
		}

		// Arrow indicator for selected row
		arrow := " "
		if i == m.selectedIndex && repo.CurrentVersion != repo.NextVersion {
			arrow = theme.IconArrowRightBold
		}

		var row []string
		if m.plan.Type == "full" {
			row = []string{
				arrow,
				checkbox,
				repoName,
				repo.Branch,
				gitStatusStr,
				changesReleaseStr,
				repo.NextVersion,
				statusStr,
				changelogStatus,
			}
		} else {
			row = []string{
				arrow,
				checkbox,
				repoName,
				repo.Branch,
				gitStatusStr,
				changesReleaseStr,
				repo.NextVersion,
				statusStr,
			}
		}

		t.Row(row...)
	}

	tableStr := t.Render()

	// Show release order information
	releaseInfo := ""
	if len(m.plan.ReleaseLevels) > 0 {
		releaseInfo = fmt.Sprintf("\n%s Release Order (by dependency level)\n\n", theme.IconPlan)
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
	if m.help.ShowAll {
		footer = ""
	} else {
		footer = theme.DefaultTheme.Muted.Render("Press ? for help • q to quit")
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
				bumpType = theme.DefaultTheme.Error.Copy().Bold(true).Render("MAJOR")
			case "minor":
				bumpType = theme.DefaultTheme.Warning.Copy().Bold(true).Render("MINOR")
			case "patch":
				bumpType = theme.DefaultTheme.Success.Copy().Bold(true).Render("PATCH")
			default:
				bumpType = theme.DefaultTheme.Bold.Render(strings.ToUpper(repo.SuggestedBump))
			}
			
			versionChange := theme.DefaultTheme.Bold.Render(
				fmt.Sprintf("%s → %s", repo.CurrentVersion, repo.NextVersion),
			)
			
			reasoning = fmt.Sprintf("\n\n%s Suggested: %s version bump (%s)\n   %s", theme.IconLightbulb, 
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

	// Create content with margin
	content := fmt.Sprintf("%s\n%s\n\n%s\n\n%s", header, llmInfo, m.viewport.View(), footer)

	// Apply margin around the entire TUI
	marginStyle := lipgloss.NewStyle().
		Padding(1, 2) // 1 line top/bottom, 2 chars left/right

	return marginStyle.Render(content)
}

func (m releaseTuiModel) viewChangelog() string {
	header := theme.DefaultTheme.Header.Render(fmt.Sprintf("%s Changelog Preview: %s", theme.IconNote, m.repoNames[m.selectedIndex]))
	
	help := theme.DefaultTheme.Muted.Render("a: approve • esc: back • q: quit")

	return fmt.Sprintf("%s\n\n%s\n\n%s", header, m.viewport.View(), help)
}

func (m releaseTuiModel) viewSettings() string {
	headerText := theme.IconShell + " Release Settings"
	header := theme.DefaultTheme.Header.Render(headerText)
	
	// Create toggle items with current states
	toggleStyle := lipgloss.NewStyle().
		Padding(1, 2).
		MarginBottom(1)
	
	activeStyle := theme.DefaultTheme.Success.Copy().
		Bold(true)
		
	inactiveStyle := theme.DefaultTheme.Muted
	
	selectedStyle := theme.DefaultTheme.Selected.Copy().
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
	
	
	content := toggleStyle.Render(strings.Join(settings, "\n\n"))
	
	// Progress message if any
	progressMsg := ""
	if m.genProgress != "" {
		progressMsg = "\n\n" + theme.DefaultTheme.Warning.
			Render(m.genProgress)
	}
	
	help := theme.DefaultTheme.Muted.Render("↑/↓,j/k: navigate • space/enter: toggle • tab/esc: back • q: quit")
	
	// Combine all elements
	return fmt.Sprintf("%s\n\n%s%s\n\n%s", header, content, progressMsg, help)
}

// Helper function to calculate next version
func calculateNextVersion(current, bump string) string {
	// Strip prerelease suffix (e.g., v0.4.1-nightly.abc123 -> v0.4.1)
	baseVersion := current
	if idx := strings.Index(current, "-"); idx != -1 {
		baseVersion = current[:idx]
	}

	parts := strings.Split(baseVersion, ".")
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
func editRulesCmd(rulesPath, repoPath string) tea.Cmd {
	cmd := exec.Command(getEditor(), rulesPath)
	cmd.Dir = repoPath // Set working directory to repo for proper context
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return fmt.Errorf("failed to edit rules file: %w", err)
		}
		return nil
	})
}

// Command to edit a file in external editor
func editFileCmd(filePath, workDir string) tea.Cmd {
	cmd := exec.Command(getEditor(), filePath)
	if workDir != "" {
		cmd.Dir = workDir // Set working directory for proper context
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
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
			return fmt.Errorf("no release plan found. Please run 'grove release plan' first")
		}
		return fmt.Errorf("failed to load release plan: %w", err)
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
		releaseDryRun = model.dryRun
		
		// Exit the TUI and run the release in the terminal
		// runReleaseApply will print its own header
		return runReleaseApply(ctx)
	}
	
	return nil
}

// updateStagedChangelogVersion updates the version in a staged changelog file
func updateStagedChangelogVersion(changelogPath, oldVersion, newVersion string) error {
	if changelogPath == "" || oldVersion == "" || newVersion == "" || oldVersion == newVersion {
		return nil // Nothing to update
	}

	// Read the staged changelog
	content, err := os.ReadFile(changelogPath)
	if err != nil {
		return fmt.Errorf("failed to read staged changelog: %w", err)
	}

	// Replace the old version with the new version in the header
	// Look for "## oldVersion (date)" pattern
	oldHeader := fmt.Sprintf("## %s (", oldVersion)
	newHeader := fmt.Sprintf("## %s (", newVersion)

	updatedContent := strings.Replace(string(content), oldHeader, newHeader, 1)

	// Write back the updated content
	if err := os.WriteFile(changelogPath, []byte(updatedContent), 0644); err != nil {
		return fmt.Errorf("failed to write updated changelog: %w", err)
	}

	return nil
}

// getRepoChangelogVersion checks if the repo's CHANGELOG.md has uncommitted changes (is dirty in git)
// and returns the version found in the first entry. Returns empty string if not dirty or no version found.
func getRepoChangelogVersion(rootDir, repoName string) string {
	repoPath := filepath.Join(rootDir, repoName)

	// Check if CHANGELOG.md is modified in git (staged or unstaged)
	cmd := exec.Command("git", "status", "--porcelain", "CHANGELOG.md")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil || len(strings.TrimSpace(string(output))) == 0 {
		return "" // Not dirty
	}

	// Read the changelog to find the first version header
	changelogPath := filepath.Join(repoPath, "CHANGELOG.md")
	content, err := os.ReadFile(changelogPath)
	if err != nil {
		return ""
	}

	// Find the first version header pattern "## vX.Y.Z" or "## [vX.Y.Z]"
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "## v") {
			// Extract version - stop at space or parenthesis
			version := strings.TrimPrefix(line, "## ")
			if idx := strings.IndexAny(version, " ("); idx != -1 {
				version = version[:idx]
			}
			return version
		}
		if strings.HasPrefix(line, "## [v") {
			// Handle [vX.Y.Z] format
			version := strings.TrimPrefix(line, "## [")
			if idx := strings.Index(version, "]"); idx != -1 {
				version = version[:idx]
			}
			return version
		}
	}

	return ""
}

// updateRepoChangelogVersion updates the version in the repo's CHANGELOG.md file
// This is called when the user bumps the version after having written the changelog to the repo
// It finds the first version header and replaces it with the new version
func updateRepoChangelogVersion(rootDir, repoName, oldVersion, newVersion string) error {
	if newVersion == "" {
		return nil // Nothing to update
	}

	changelogPath := filepath.Join(rootDir, repoName, "CHANGELOG.md")

	// Check if the file exists
	content, err := os.ReadFile(changelogPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No repo changelog to update
		}
		return fmt.Errorf("failed to read repo changelog: %w", err)
	}

	// Find the current version in the repo changelog (first ## vX.Y.Z header)
	currentRepoVersion := ""
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## v") {
			// Extract version up to space or paren
			ver := strings.TrimPrefix(trimmed, "## ")
			if idx := strings.IndexAny(ver, " ("); idx != -1 {
				ver = ver[:idx]
			}
			currentRepoVersion = ver
			break
		}
	}

	if currentRepoVersion == "" || currentRepoVersion == newVersion {
		return nil // No version found or already correct
	}

	// Replace the current version with the new version in the header
	// Match pattern "## vX.Y.Z (" to be precise
	oldHeader := fmt.Sprintf("## %s (", currentRepoVersion)
	newHeader := fmt.Sprintf("## %s (", newVersion)
	updatedContent := strings.Replace(string(content), oldHeader, newHeader, 1)

	// Write back the updated content
	if err := os.WriteFile(changelogPath, []byte(updatedContent), 0644); err != nil {
		return fmt.Errorf("failed to write updated repo changelog: %w", err)
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
			fmt.Printf("%s  Warning: Changelog for %s was modified but doesn't contain expected version header '%s'\n",
				theme.IconWarning, repoName, expectedHeader)
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

The release plan is persisted in the Grove state directory and can be
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

