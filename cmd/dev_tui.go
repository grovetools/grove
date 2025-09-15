package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/devlinks"
	"github.com/mattsolo1/grove-meta/pkg/reconciler"
	"github.com/mattsolo1/grove-meta/pkg/sdk"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

// Tool item for the list
type toolItem struct {
	name          string
	repoName      string
	status        string // "dev", "release", "not installed"
	activeVersion string
	latestRelease string
	devLinks      []string // available dev links
	worktrees     []worktreeInfo
}

type worktreeInfo struct {
	name string
	path string
}

func (i toolItem) Title() string { return i.name }
func (i toolItem) Description() string {
	status := ""
	switch i.status {
	case "dev":
		status = fmt.Sprintf("◆ dev: %s", i.activeVersion)
	case "release":
		status = fmt.Sprintf("● release: %s", i.activeVersion)
	default:
		status = "○ not installed"
	}
	
	if i.latestRelease != "" && i.activeVersion != i.latestRelease {
		status += fmt.Sprintf(" (latest: %s)", i.latestRelease)
	}
	return status
}
func (i toolItem) FilterValue() string { return i.name }

// Custom key bindings
type keyMap struct {
	Install    key.Binding
	SetDev     key.Binding
	Reset      key.Binding
	Refresh    key.Binding
	Help       key.Binding
	Quit       key.Binding
	Enter      key.Binding
	Back       key.Binding
}

var keys = keyMap{
	Install: key.NewBinding(
		key.WithKeys("i"),
		key.WithHelp("i", "install latest"),
	),
	SetDev: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "set dev version"),
	),
	Reset: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "reset to release"),
	),
	Refresh: key.NewBinding(
		key.WithKeys("R"),
		key.WithHelp("R", "refresh"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "toggle help"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "select"),
	),
	Back: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back"),
	),
}

type model struct {
	list          list.Model
	tools         []toolItem
	keys          keyMap
	manager       *sdk.Manager
	reconciler    *reconciler.Reconciler
	devConfig     *devlinks.Config
	groveHome     string
	statusMessage string
	err           error
	mode          string // "list" or "worktree-select"
	selectedTool  *toolItem
	worktreeList  list.Model
}

func initialModel() (*model, error) {
	// Create SDK manager
	manager, err := sdk.NewManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create SDK manager: %w", err)
	}

	// Auto-detect gh CLI
	if checkGHAuth() {
		manager.SetUseGH(true)
	}

	// Load tool versions
	groveHome := filepath.Join(os.Getenv("HOME"), ".grove")
	toolVersions, err := sdk.LoadToolVersions(groveHome)
	if err != nil {
		toolVersions = &sdk.ToolVersions{
			Versions: make(map[string]string),
		}
	}

	// Create reconciler
	r, err := reconciler.NewWithToolVersions(toolVersions)
	if err != nil {
		return nil, fmt.Errorf("failed to create reconciler: %w", err)
	}

	// Load dev config
	devConfig, err := devlinks.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load dev config: %w", err)
	}

	m := &model{
		keys:       keys,
		manager:    manager,
		reconciler: r,
		devConfig:  devConfig,
		groveHome:  groveHome,
		mode:       "list",
	}

	// Load tools
	if err := m.loadTools(); err != nil {
		return nil, err
	}

	// Setup list
	items := make([]list.Item, len(m.tools))
	for i, tool := range m.tools {
		items[i] = tool
	}

	const defaultWidth = 80
	const listHeight = 20

	l := list.New(items, list.NewDefaultDelegate(), defaultWidth, listHeight)
	l.Title = "Grove Dev Manager"
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)
	l.Styles.Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#BD93F9")).
		MarginLeft(2)
	l.Styles.PaginationStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#6272A4")).
		PaddingLeft(4)
	l.Styles.HelpStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#6272A4")).
		PaddingLeft(4).
		PaddingBottom(1)
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			keys.Install,
			keys.SetDev,
			keys.Reset,
		}
	}

	m.list = l

	return m, nil
}

func (m *model) loadTools() error {
	// Get available tools from workspace discovery and SDK
	toolMap := make(map[string]string) // toolName -> repoName

	// Try to discover from workspaces first
	if rootDir, err := workspace.FindRoot(""); err == nil {
		if workspaces, err := workspace.Discover(rootDir); err == nil {
			for _, wsPath := range workspaces {
				if binaries, err := workspace.DiscoverLocalBinaries(wsPath); err == nil {
					for _, binary := range binaries {
						repoName := filepath.Base(wsPath)
						toolMap[binary.Name] = repoName
					}
				}
			}
		}
	}

	// Add any tools from SDK
	sdkToolToRepo := sdk.GetToolToRepoMap()
	for toolName, repoName := range sdkToolToRepo {
		if _, exists := toolMap[toolName]; !exists {
			toolMap[toolName] = repoName
		}
	}

	// Build tool info
	var tools []toolItem
	for toolName, repoName := range toolMap {
		tool := toolItem{
			name:     toolName,
			repoName: repoName,
			status:   "not installed",
		}

		// Get effective source from reconciler
		source, version, _ := m.reconciler.GetEffectiveSource(toolName)

		if source == "dev" {
			tool.status = "dev"
			tool.activeVersion = version
		} else if source == "release" {
			tool.status = "release"
			tool.activeVersion = version
		}

		// Get available dev links
		if binInfo, exists := m.devConfig.Binaries[toolName]; exists {
			for alias := range binInfo.Links {
				tool.devLinks = append(tool.devLinks, alias)
			}
			sort.Strings(tool.devLinks)
		}

		// Get latest release
		latestVersion, _ := m.manager.GetLatestVersionTag(repoName)
		if latestVersion == "" {
			latestVersion, _ = m.manager.GetLatestVersionTag(toolName)
		}
		tool.latestRelease = latestVersion

		// Discover worktrees for this tool
		tool.worktrees = m.discoverWorktrees(toolName, repoName)

		tools = append(tools, tool)
	}

	// Sort tools by name
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].name < tools[j].name
	})

	m.tools = tools
	return nil
}

func (m *model) discoverWorktrees(toolName, repoName string) []worktreeInfo {
	var worktrees []worktreeInfo

	// Check for .grove-worktrees directory
	home := os.Getenv("HOME")
	worktreesDir := filepath.Join(home, ".grove-worktrees")
	
	// Try both the repo name and variations
	repoPatterns := []string{
		repoName,
		"grove-" + toolName,
		toolName,
	}

	for _, pattern := range repoPatterns {
		repoWorktreesDir := filepath.Join(worktreesDir, pattern)
		if entries, err := os.ReadDir(repoWorktreesDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					wtPath := filepath.Join(repoWorktreesDir, entry.Name())
					// Check if this worktree has the binary
					binPath := filepath.Join(wtPath, "bin", toolName)
					if _, err := os.Stat(binPath); err == nil {
						worktrees = append(worktrees, worktreeInfo{
							name: entry.Name(),
							path: wtPath,
						})
					}
				}
			}
		}
	}

	// Also check existing dev links
	if binInfo, exists := m.devConfig.Binaries[toolName]; exists {
		for alias, linkInfo := range binInfo.Links {
			// Check if this is already in worktrees
			found := false
			for _, wt := range worktrees {
				if wt.path == linkInfo.WorktreePath {
					found = true
					break
				}
			}
			if !found {
				worktrees = append(worktrees, worktreeInfo{
					name: alias,
					path: linkInfo.WorktreePath,
				})
			}
		}
	}

	return worktrees
}

func (m *model) Init() tea.Cmd {
	return nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.mode == "worktree-select" {
			switch {
			case key.Matches(msg, keys.Back):
				m.mode = "list"
				m.selectedTool = nil
				return m, nil
			case key.Matches(msg, keys.Enter):
				if i, ok := m.worktreeList.SelectedItem().(worktreeInfo); ok {
					return m, m.setDevVersion(m.selectedTool, i)
				}
			case key.Matches(msg, keys.Quit):
				return m, tea.Quit
			default:
				var cmd tea.Cmd
				m.worktreeList, cmd = m.worktreeList.Update(msg)
				return m, cmd
			}
		} else {
			// List mode
			switch {
			case key.Matches(msg, keys.Quit):
				return m, tea.Quit
			case key.Matches(msg, keys.Install):
				if i, ok := m.list.SelectedItem().(toolItem); ok {
					return m, m.installLatest(&i)
				}
			case key.Matches(msg, keys.SetDev):
				if i, ok := m.list.SelectedItem().(toolItem); ok {
					return m, m.showWorktreeSelect(&i)
				}
			case key.Matches(msg, keys.Reset):
				if i, ok := m.list.SelectedItem().(toolItem); ok {
					return m, m.resetToRelease(&i)
				}
			case key.Matches(msg, keys.Refresh):
				if err := m.loadTools(); err != nil {
					m.statusMessage = fmt.Sprintf("Error refreshing: %v", err)
				} else {
					items := make([]list.Item, len(m.tools))
					for i, tool := range m.tools {
						items[i] = tool
					}
					m.list.SetItems(items)
					m.statusMessage = "Refreshed tool list"
				}
				return m, nil
			}
		}

	case tea.WindowSizeMsg:
		m.list.SetWidth(msg.Width)
		m.list.SetHeight(msg.Height - 2)
		if m.worktreeList.Width() > 0 {
			m.worktreeList.SetWidth(msg.Width)
			m.worktreeList.SetHeight(msg.Height - 2)
		}
		return m, nil

	case statusMsg:
		m.statusMessage = string(msg)
		// Refresh the tools after an action
		if err := m.loadTools(); err == nil {
			items := make([]list.Item, len(m.tools))
			for i, tool := range m.tools {
				items[i] = tool
			}
			m.list.SetItems(items)
		}
		return m, nil

	case errMsg:
		m.err = msg.err
		m.statusMessage = fmt.Sprintf("Error: %v", msg.err)
		return m, nil
	}

	if m.mode == "worktree-select" {
		var cmd tea.Cmd
		m.worktreeList, cmd = m.worktreeList.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress q to quit.", m.err)
	}

	if m.mode == "worktree-select" {
		return m.worktreeList.View() + "\n" + m.statusMessage
	}

	return m.list.View() + "\n" + m.statusMessage
}

// Commands
type statusMsg string
type errMsg struct{ err error }

func (m *model) installLatest(tool *toolItem) tea.Cmd {
	return func() tea.Msg {
		if tool.latestRelease == "" {
			return statusMsg(fmt.Sprintf("No release available for %s", tool.name))
		}

		// Install the latest version using grove install command
		cmd := exec.Command("grove", "install", tool.name, tool.latestRelease)
		if err := cmd.Run(); err != nil {
			return errMsg{err: fmt.Errorf("failed to install %s: %w", tool.name, err)}
		}

		// Update tool versions
		toolVersions, _ := sdk.LoadToolVersions(m.groveHome)
		if toolVersions == nil {
			toolVersions = &sdk.ToolVersions{Versions: make(map[string]string)}
		}
		toolVersions.Versions[tool.name] = tool.latestRelease
		if err := toolVersions.Save(m.groveHome); err != nil {
			return errMsg{err: fmt.Errorf("failed to save tool versions: %w", err)}
		}

		// Reconcile to update symlinks
		if err := m.reconciler.Reconcile(tool.name); err != nil {
			return errMsg{err: fmt.Errorf("failed to reconcile: %w", err)}
		}

		return statusMsg(fmt.Sprintf("Installed %s %s", tool.name, tool.latestRelease))
	}
}

func (m *model) showWorktreeSelect(tool *toolItem) tea.Cmd {
	return func() tea.Msg {
		if len(tool.worktrees) == 0 {
			return statusMsg(fmt.Sprintf("No worktrees found for %s", tool.name))
		}

		// Create worktree selection list
		items := make([]list.Item, len(tool.worktrees))
		for i, wt := range tool.worktrees {
			items[i] = wt
		}

		l := list.New(items, list.NewDefaultDelegate(), 80, 20)
		l.Title = fmt.Sprintf("Select worktree for %s", tool.name)
		l.SetShowStatusBar(false)
		l.SetFilteringEnabled(false)

		m.worktreeList = l
		m.selectedTool = tool
		m.mode = "worktree-select"

		return nil
	}
}

func (w worktreeInfo) Title() string       { return w.name }
func (w worktreeInfo) Description() string { return w.path }
func (w worktreeInfo) FilterValue() string { return w.name }

func (m *model) setDevVersion(tool *toolItem, worktree worktreeInfo) tea.Cmd {
	return func() tea.Msg {
		// Discover binaries in the worktree
		binaries, err := workspace.DiscoverLocalBinaries(worktree.path)
		if err != nil {
			return errMsg{err: fmt.Errorf("failed to discover binaries: %w", err)}
		}

		// Find the specific binary
		var binaryPath string
		for _, bin := range binaries {
			if bin.Name == tool.name {
				binaryPath = bin.Path
				break
			}
		}

		if binaryPath == "" {
			return errMsg{err: fmt.Errorf("binary %s not found in worktree", tool.name)}
		}

		// Register the binary
		if m.devConfig.Binaries[tool.name] == nil {
			m.devConfig.Binaries[tool.name] = &devlinks.BinaryLinks{
				Links: make(map[string]devlinks.LinkInfo),
			}
		}

		linkInfo := devlinks.LinkInfo{
			Path:         binaryPath,
			WorktreePath: worktree.path,
			RegisteredAt: "now",
		}

		m.devConfig.Binaries[tool.name].Links[worktree.name] = linkInfo

		// Save config
		if err := devlinks.SaveConfig(m.devConfig); err != nil {
			return errMsg{err: fmt.Errorf("failed to save config: %w", err)}
		}

		// Activate the link
		if err := activateDevLink(tool.name, worktree.name); err != nil {
			return errMsg{err: fmt.Errorf("failed to activate link: %w", err)}
		}

		m.mode = "list"
		m.selectedTool = nil
		return statusMsg(fmt.Sprintf("Set %s to dev version from %s", tool.name, worktree.name))
	}
}

func (m *model) resetToRelease(tool *toolItem) tea.Cmd {
	return func() tea.Msg {
		// Check if there's a main dev link
		if binInfo, exists := m.devConfig.Binaries[tool.name]; exists {
			if _, hasMain := binInfo.Links["main"]; hasMain {
				// Switch to main
				if err := activateDevLink(tool.name, "main"); err != nil {
					return errMsg{err: fmt.Errorf("failed to switch to main: %w", err)}
				}
				return statusMsg(fmt.Sprintf("Reset %s to main dev version", tool.name))
			}

			// Clear current dev link
			binInfo.Current = ""
			if err := devlinks.SaveConfig(m.devConfig); err != nil {
				return errMsg{err: fmt.Errorf("failed to save config: %w", err)}
			}
		}

		// Switch to release version
		activeVersion, err := m.manager.GetToolVersion(tool.name)
		if err != nil {
			return statusMsg(fmt.Sprintf("No release version available for %s", tool.name))
		}

		// Reconcile to update symlink
		if err := m.reconciler.Reconcile(tool.name); err != nil {
			return errMsg{err: fmt.Errorf("failed to reconcile: %w", err)}
		}

		return statusMsg(fmt.Sprintf("Reset %s to release version %s", tool.name, activeVersion))
	}
}

func newDevTuiCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("tui", "Interactive tool version manager")

	cmd.Long = `Launch an interactive terminal UI for managing Grove tool versions.
This interface allows you to:
- View all Grove tools and their current versions
- Install the latest release version
- Switch to development versions from worktrees
- Reset tools back to release versions`

	cmd.Example = `  # Launch the interactive UI
  grove dev tui`

	cmd.Args = cobra.NoArgs

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		model, err := initialModel()
		if err != nil {
			return fmt.Errorf("failed to initialize: %w", err)
		}

		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("error running program: %w", err)
		}

		return nil
	}

	return cmd
}