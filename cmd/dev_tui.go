package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/tui/components/help"
	tablecomponent "github.com/mattsolo1/grove-core/tui/components/table"
	"github.com/mattsolo1/grove-core/tui/keymap"
	"github.com/mattsolo1/grove-core/tui/theme"
	"github.com/mattsolo1/grove-meta/pkg/devlinks"
	"github.com/mattsolo1/grove-meta/pkg/reconciler"
	"github.com/mattsolo1/grove-meta/pkg/sdk"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

// Tool item for the table
type toolItem struct {
	name          string
	repoName      string
	status        string // "dev", "release", "not installed"
	activeVersion string
	latestRelease string
	devLinks      []string // available dev links
	worktrees     []worktreeInfo
	latestLoaded  bool // whether latest release has been fetched
}

type worktreeInfo struct {
	name string
	path string
}

func (w worktreeInfo) Title() string       { return w.name }
func (w worktreeInfo) Description() string { return w.path }
func (w worktreeInfo) FilterValue() string { return w.name }


// Custom key bindings
type keyMap struct {
	keymap.Base
	Install key.Binding
	SetDev  key.Binding
	Reset   key.Binding
	Refresh key.Binding
	Enter   key.Binding
	Back    key.Binding
}

var keys = keyMap{
	Base: keymap.NewBase(),
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
	tools             []toolItem
	keys              keyMap
	manager           *sdk.Manager
	reconciler        *reconciler.Reconciler
	devConfig         *devlinks.Config
	groveHome         string
	statusMessage     string
	err               error
	mode              string // "table" or "worktree-select"
	selectedIndex     int
	selectedTool      *toolItem
	worktreeList      list.Model
	help              help.Model
	width             int
	height            int
	loading           bool
	spinner           spinner.Model
	versionCache      map[string]string // cache for dev binary versions
	versionCacheMutex sync.RWMutex
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

	// Create spinner
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	m := &model{
		keys:          keys,
		manager:       manager,
		reconciler:    r,
		devConfig:     devConfig,
		groveHome:     groveHome,
		mode:          "table",
		selectedIndex: 0,
		loading:       true,
		spinner:       s,
		versionCache:  make(map[string]string),
	}

	return m, nil
}

func (m *model) Init() tea.Cmd {
	// Start with spinner and load tools quickly
	return tea.Batch(
		m.spinner.Tick,
		m.loadToolsQuickly,
	)
}

// loadToolsQuickly loads tools without fetching latest releases or dev versions
func (m *model) loadToolsQuickly() tea.Msg {
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
			name:         toolName,
			repoName:     repoName,
			status:       "not installed",
			latestLoaded: false,
		}

		// Get effective source from reconciler
		source, version, _ := m.reconciler.GetEffectiveSource(toolName)

		if source == "dev" {
			tool.status = "dev"
			tool.activeVersion = version
			// Don't fetch dev version here - will do lazily
		} else if source == "release" {
			tool.status = "release"
			tool.activeVersion = version
		}

		// Get available dev links (quick)
		if binInfo, exists := m.devConfig.Binaries[toolName]; exists {
			for alias := range binInfo.Links {
				tool.devLinks = append(tool.devLinks, alias)
			}
			sort.Strings(tool.devLinks)
		}

		// Discover worktrees (quick - just check directories)
		tool.worktrees = m.discoverWorktreesQuick(toolName, repoName)

		tools = append(tools, tool)
	}

	// Sort tools by name
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].name < tools[j].name
	})

	m.tools = tools
	m.loading = false

	// Start fetching latest releases in background
	return m.fetchLatestReleasesInBackground()
}

// discoverWorktreesQuick quickly discovers worktrees without checking binaries
func (m *model) discoverWorktreesQuick(toolName, repoName string) []worktreeInfo {
	var worktrees []worktreeInfo

	// Check existing dev links first (these are known to work)
	if binInfo, exists := m.devConfig.Binaries[toolName]; exists {
		for alias, linkInfo := range binInfo.Links {
			worktrees = append(worktrees, worktreeInfo{
				name: alias,
				path: linkInfo.WorktreePath,
			})
		}
	}

	return worktrees
}

// fetchLatestReleasesInBackground fetches latest releases asynchronously
func (m *model) fetchLatestReleasesInBackground() tea.Cmd {
	return func() tea.Msg {
		// Fetch latest releases in parallel with limited concurrency
		sem := make(chan struct{}, 5) // Limit to 5 concurrent requests
		var wg sync.WaitGroup
		
		for i := range m.tools {
			if m.tools[i].latestLoaded {
				continue
			}
			
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				
				// Get latest release
				latestVersion, _ := m.manager.GetLatestVersionTag(m.tools[idx].repoName)
				if latestVersion == "" {
					latestVersion, _ = m.manager.GetLatestVersionTag(m.tools[idx].name)
				}
				m.tools[idx].latestRelease = latestVersion
				m.tools[idx].latestLoaded = true
			}(i)
		}
		
		wg.Wait()
		return latestReleasesLoadedMsg{}
	}
}

// Message types
type latestReleasesLoadedMsg struct{}
type devVersionLoadedMsg struct {
	toolName string
	version  string
}

func (m *model) loadDevVersion(toolName string, binaryPath string) tea.Cmd {
	return func() tea.Msg {
		// Check cache first
		m.versionCacheMutex.RLock()
		if cached, exists := m.versionCache[binaryPath]; exists {
			m.versionCacheMutex.RUnlock()
			return devVersionLoadedMsg{toolName: toolName, version: cached}
		}
		m.versionCacheMutex.RUnlock()

		// Get version
		version := getTUIDevBinaryVersion(binaryPath)
		
		// Cache it
		m.versionCacheMutex.Lock()
		m.versionCache[binaryPath] = version
		m.versionCacheMutex.Unlock()
		
		return devVersionLoadedMsg{toolName: toolName, version: version}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.mode == "worktree-select" {
			switch {
			case key.Matches(msg, keys.Back):
				m.mode = "table"
				m.selectedTool = nil
				return m, nil
			case key.Matches(msg, keys.Enter):
				if i, ok := m.worktreeList.SelectedItem().(worktreeInfo); ok {
					return m, m.setDevVersion(m.selectedTool, i)
				}
			case key.Matches(msg, keys.Base.Quit):
				return m, tea.Quit
			default:
				var cmd tea.Cmd
				m.worktreeList, cmd = m.worktreeList.Update(msg)
				return m, cmd
			}
		} else {
			// Table mode
			switch {
			case key.Matches(msg, keys.Base.Quit):
				return m, tea.Quit
			case key.Matches(msg, keys.Base.Up):
				if m.selectedIndex > 0 {
					m.selectedIndex--
					// Load dev version if needed
					if m.selectedIndex < len(m.tools) && m.tools[m.selectedIndex].status == "dev" {
						tool := &m.tools[m.selectedIndex]
						if binInfo, exists := m.devConfig.Binaries[tool.name]; exists && binInfo.Current != "" {
							if linkInfo, exists := binInfo.Links[binInfo.Current]; exists {
								if tool.activeVersion == binInfo.Current { // Not yet loaded
									return m, m.loadDevVersion(tool.name, linkInfo.Path)
								}
							}
						}
					}
				}
				return m, nil
			case key.Matches(msg, keys.Base.Down):
				if m.selectedIndex < len(m.tools)-1 {
					m.selectedIndex++
					// Load dev version if needed
					if m.selectedIndex < len(m.tools) && m.tools[m.selectedIndex].status == "dev" {
						tool := &m.tools[m.selectedIndex]
						if binInfo, exists := m.devConfig.Binaries[tool.name]; exists && binInfo.Current != "" {
							if linkInfo, exists := binInfo.Links[binInfo.Current]; exists {
								if tool.activeVersion == binInfo.Current { // Not yet loaded
									return m, m.loadDevVersion(tool.name, linkInfo.Path)
								}
							}
						}
					}
				}
				return m, nil
			case key.Matches(msg, keys.Base.Help):
				m.help.ShowAll = !m.help.ShowAll
				return m, nil
			case key.Matches(msg, keys.Install):
				if m.selectedIndex < len(m.tools) {
					tool := &m.tools[m.selectedIndex]
					return m, m.installLatest(tool)
				}
			case key.Matches(msg, keys.SetDev):
				if m.selectedIndex < len(m.tools) {
					tool := &m.tools[m.selectedIndex]
					return m, m.showWorktreeSelect(tool)
				}
			case key.Matches(msg, keys.Reset):
				if m.selectedIndex < len(m.tools) {
					tool := &m.tools[m.selectedIndex]
					return m, m.resetToRelease(tool)
				}
			case key.Matches(msg, keys.Refresh):
				m.loading = true
				m.versionCache = make(map[string]string) // Clear cache
				return m, tea.Batch(
					m.spinner.Tick,
					m.loadToolsQuickly,
				)
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.worktreeList.Width() > 0 {
			m.worktreeList.SetWidth(msg.Width)
			m.worktreeList.SetHeight(msg.Height - 2)
		}
		return m, nil

	case spinner.TickMsg:
		if m.loading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case latestReleasesLoadedMsg:
		// Latest releases have been loaded, no need to do anything special
		return m, nil

	case devVersionLoadedMsg:
		// Update the tool with the loaded version
		for i := range m.tools {
			if m.tools[i].name == msg.toolName {
				if msg.version != "" {
					m.tools[i].activeVersion = msg.version
				}
				break
			}
		}
		return m, nil

	case statusMsg:
		m.statusMessage = string(msg)
		// Refresh the tools after an action
		m.loading = true
		return m, tea.Batch(
			m.spinner.Tick,
			m.loadToolsQuickly,
		)

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

	return m, nil
}

func (m *model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress q to quit.", m.err)
	}

	if m.loading && len(m.tools) == 0 {
		return fmt.Sprintf("\n %s Loading tools...\n", m.spinner.View())
	}

	if m.mode == "worktree-select" {
		return m.worktreeList.View() + "\n" + m.statusMessage
	}

	// Build table view
	headers := []string{"TOOL", "REPOSITORY", "STATUS", "CURRENT VERSION", "LATEST"}

	// Build rows
	var rows [][]string
	for i, tool := range m.tools {
		currentVersion := "-"
		displayVersion := ""
		statusSymbol := ""

		// Determine status symbol with styling
		switch tool.status {
		case "dev":
			statusSymbol = "◆ dev"
		case "release":
			statusSymbol = "● release"
		default:
			statusSymbol = "○ not installed"
		}

		if tool.status == "dev" && tool.activeVersion != "" {
			displayVersion = tool.activeVersion
			currentVersion = displayVersion
		} else if tool.status == "release" && tool.activeVersion != "" {
			displayVersion = tool.activeVersion
			currentVersion = displayVersion
		}

		// Add release status indicator
		releaseStatus := tool.latestRelease
		styledReleaseStatus := ""
		if !tool.latestLoaded {
			styledReleaseStatus = "..."
		} else if releaseStatus == "" {
			styledReleaseStatus = "-"
		} else if displayVersion != "" && displayVersion == tool.latestRelease {
			styledReleaseStatus = fmt.Sprintf("%s ✓", tool.latestRelease)
		} else if tool.latestRelease != "" {
			styledReleaseStatus = fmt.Sprintf("%s ↑", tool.latestRelease)
		}

		row := []string{
			tool.name,
			tool.repoName,
			statusSymbol,
			currentVersion,
			styledReleaseStatus,
		}

		// Apply selection highlighting
		if i == m.selectedIndex {
			for j := range row {
				row[j] = theme.DefaultTheme.Selected.Render(row[j])
			}
		} else {
			// Apply normal styling
			row[0] = theme.DefaultTheme.Bold.Render(row[0])
			row[1] = theme.DefaultTheme.Info.Render(row[1])
			
			switch tool.status {
			case "dev":
				row[2] = theme.DefaultTheme.Warning.Render(statusSymbol)
			case "release":
				row[2] = theme.DefaultTheme.Success.Render(statusSymbol)
			default:
				row[2] = theme.DefaultTheme.Muted.Render(statusSymbol)
			}
			
			row[3] = theme.DefaultTheme.Bold.Render(currentVersion)
			
			if !tool.latestLoaded {
				row[4] = theme.DefaultTheme.Muted.Render("...")
			} else if releaseStatus == "" {
				row[4] = theme.DefaultTheme.Muted.Render("-")
			} else if displayVersion != "" && displayVersion == tool.latestRelease {
				row[4] = theme.DefaultTheme.Success.Render(styledReleaseStatus)
			} else if tool.latestRelease != "" {
				row[4] = theme.DefaultTheme.Warning.Render(styledReleaseStatus)
			}
		}

		rows = append(rows, row)
	}

	// Create styled table using grove-core component
	t := tablecomponent.NewStyledTable()
	t.Headers(headers...)
	t.Rows(rows...)

	// Build full view
	var view strings.Builder
	view.WriteString(t.String())
	view.WriteString("\n\n")

	// Add help
	view.WriteString(m.help.View())

	// Add status message if any
	if m.statusMessage != "" {
		view.WriteString("\n")
		view.WriteString(m.statusMessage)
	}

	return view.String()
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
		// Do full worktree discovery now
		tool.worktrees = m.discoverWorktreesFull(tool.name, tool.repoName)
		
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

// discoverWorktreesFull does a full worktree discovery including checking for binaries
func (m *model) discoverWorktreesFull(toolName, repoName string) []worktreeInfo {
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

		m.mode = "table"
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

// getTUIDevBinaryVersion attempts to get version from a dev binary
func getTUIDevBinaryVersion(binaryPath string) string {
	if binaryPath == "" || !strings.Contains(binaryPath, "/") {
		return ""
	}

	// Set a timeout for the version command
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second) // Reduced timeout
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, "version")
	cmd.Env = append(os.Environ(), "NO_COLOR=1") // Disable color output

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	// Run with timeout
	if err := cmd.Run(); err != nil {
		return ""
	}

	output := out.String()

	// Try to extract version from output
	// First try to find a line starting with "Version:"
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "Version:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return parts[1]
			}
		}
	}

	// Fallback to pattern matching
	// Look for patterns like "Version: main-986ed5d-dirty" or "v0.2.8"
	versionPatterns := []string{
		`Version:\s+(\S+)`,
		`version\s+(\S+)`,
		`(v\d+\.\d+\.\d+\S*)`,
		`(main-[a-f0-9]+-?(?:dirty)?)`,
		`([a-zA-Z]+-[a-f0-9]+-?(?:dirty)?)`, // branch-hash-dirty pattern
	}

	for _, pattern := range versionPatterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(output)
		if len(matches) > 1 {
			version := matches[1]
			// Clean up the version string
			version = strings.TrimSpace(version)
			return version
		}
	}

	return ""
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