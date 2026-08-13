package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/tui/theme"
	"github.com/spf13/cobra"

	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/sdk"
	"github.com/grovetools/grove/pkg/shell"
)

// Type alias for the extracted keymap
type onboardKeyMap = grovekeymap.OnboardKeyMap

// onboardStep represents the current step in the onboarding wizard
type onboardStep int

const (
	onboardStepWelcome onboardStep = iota
	onboardStepPath
	onboardStepInstall
	onboardStepSetup
	onboardStepDone
)

// onboardKeys is the singleton instance of the onboard wizard TUI keymap.
var onboardKeys = func() grovekeymap.OnboardKeyMap {
	cfg, _ := config.LoadDefault()
	return grovekeymap.NewOnboardKeyMap(cfg)
}()

// Toggle key for checkboxes
var toggleKey = key.NewBinding(
	key.WithKeys(" "),
	key.WithHelp("space", "toggle"),
)

// toolItem represents a selectable tool in the onboarding wizard
type toolItem struct {
	repoName    string
	alias       string
	description string
	selected    bool
}

// Default tools to pre-select during onboarding
var defaultSelectedTools = map[string]bool{
	"nav":     true,
	"flow":    true,
	"nb":      true,
	"cx":      true,
	"hooks":   true,
	"core":    true,
	"aglogs":  true,
	"skills":  true,
	"treemux": true,
}

// onboardModel is the TUI model for onboarding
type onboardModel struct {
	step         onboardStep
	shellManager *shell.Manager
	shellType    shell.ShellType
	rcFile       string
	// binDir is grove's private toolchain dir — where tools are INSTALLED. It
	// is not expected on the user's PATH.
	binDir string
	// linkDir is the user's own global bin dir (~/.local/bin), the only
	// directory onboarding cares about PATH-wise, because the only name grove
	// puts there is `grove` itself.
	linkDir string

	// PATH step state
	pathAlreadySet bool
	pathAdded      bool
	pathError      error

	// Hub link state: ~/.local/bin/grove -> paths.BinDir()/grove
	linkPath    string
	linkCreated bool
	linkError   error

	// Install step state
	tools          []toolItem
	toolCursor     int
	installing     bool
	installDone    bool
	installError   error
	installOutput  *bytes.Buffer
	installSpinner spinner.Model

	// Setup step state
	runSetup bool

	// TUI state
	keys   onboardKeyMap
	width  int
	height int
	ready  bool
}

// installDoneMsg signals that installation completed
type installDoneMsg struct {
	err error
}

func newOnboardModel() *onboardModel {
	// Initialize shell manager
	shellMgr, _ := shell.NewManager()

	// Where tools are installed (private) vs. where the one global name lives.
	binDir := paths.BinDir()
	linkDir := filepath.Join("~", ".local", "bin")
	if dir, err := exposeDir(); err == nil {
		linkDir = dir
	}

	// The PATH question is about ~/.local/bin, not about grove's toolchain dir:
	// the toolchain is reached as `grove <tool>` and never needs to be on PATH.
	var pathAlreadySet bool
	if shellMgr != nil {
		pathAlreadySet = shellMgr.PathIncludes(linkDir)
	}

	// Detect shell
	var shellType shell.ShellType
	var rcFile string
	if shellMgr != nil {
		shellType, _ = shellMgr.Detect()
		rcFile, _ = shellMgr.GetRcFile(shellType)
	}

	// Create spinner
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = theme.DefaultTheme.Highlight

	// Build tools list from registry
	tools := buildToolsList()

	return &onboardModel{
		step:           onboardStepWelcome,
		shellManager:   shellMgr,
		shellType:      shellType,
		rcFile:         rcFile,
		binDir:         binDir,
		linkDir:        linkDir,
		pathAlreadySet: pathAlreadySet,
		tools:          tools,
		toolCursor:     0,
		installOutput:  &bytes.Buffer{},
		installSpinner: s,
		keys:           onboardKeys,
	}
}

// buildToolsList creates the list of tools from the SDK registry
func buildToolsList() []toolItem {
	registry := sdk.GetToolRegistry()
	var tools []toolItem

	// Sort by alias for consistent ordering
	var aliases []string
	aliasToRepo := make(map[string]string)
	for repoName, info := range registry {
		// Skip grove itself - it's already installed
		if info.Alias == "grove" {
			continue
		}
		// Skip nvim plugin - it's not a CLI tool
		if info.Alias == "grove-nvim" {
			continue
		}
		aliases = append(aliases, info.Alias)
		aliasToRepo[info.Alias] = repoName
	}
	sort.Strings(aliases)

	for _, alias := range aliases {
		repoName := aliasToRepo[alias]
		info := registry[repoName]
		tools = append(tools, toolItem{
			repoName:    repoName,
			alias:       info.Alias,
			description: info.Description,
			selected:    defaultSelectedTools[info.Alias],
		})
	}

	return tools
}

func (m *onboardModel) Init() tea.Cmd {
	return m.installSpinner.Tick
}

func (m *onboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.installSpinner, cmd = m.installSpinner.Update(msg)
		return m, cmd

	case installDoneMsg:
		m.installing = false
		m.installDone = true
		m.installError = msg.err
		return m, nil
	}

	return m, nil
}

func (m *onboardModel) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global quit handling
	if key.Matches(msg, m.keys.Base.Quit) {
		return m, tea.Quit
	}

	switch m.step {
	case onboardStepWelcome:
		if key.Matches(msg, m.keys.Confirm) {
			// The hub link is not optional and not a question: `grove` is the
			// one name the install owns, and every other tool is reached
			// through it. The rc-file edit below is the only part that needs
			// consent, and only when ~/.local/bin is not already on PATH.
			m.ensureLink()
			if m.pathAlreadySet {
				m.step = onboardStepInstall
			} else {
				m.step = onboardStepPath
			}
		}
		return m, nil

	case onboardStepPath:
		switch {
		case key.Matches(msg, m.keys.Yes), key.Matches(msg, m.keys.Confirm):
			// Add ~/.local/bin (NOT grove's toolchain dir) to PATH.
			if m.shellManager != nil {
				m.pathError = m.shellManager.AddToPath(m.linkDir)
				m.pathAdded = m.pathError == nil
			}
			m.step = onboardStepInstall
		case key.Matches(msg, m.keys.No), key.Matches(msg, m.keys.Skip):
			// Skip PATH setup
			m.step = onboardStepInstall
		}
		return m, nil

	case onboardStepInstall:
		if m.installing {
			// Already installing, ignore keypresses
			return m, nil
		}
		if m.installDone {
			// Installation finished, move to setup step
			m.step = onboardStepSetup
			return m, nil
		}
		switch {
		case key.Matches(msg, m.keys.Base.Up):
			if m.toolCursor > 0 {
				m.toolCursor--
			}
		case key.Matches(msg, m.keys.Base.Down):
			if m.toolCursor < len(m.tools)-1 {
				m.toolCursor++
			}
		case key.Matches(msg, toggleKey):
			// Toggle tool selection
			if m.toolCursor >= 0 && m.toolCursor < len(m.tools) {
				m.tools[m.toolCursor].selected = !m.tools[m.toolCursor].selected
			}
		case key.Matches(msg, m.keys.Confirm):
			// Start installation with selected tools
			m.installing = true
			return m, m.runInstallCmd()
		case key.Matches(msg, m.keys.Skip):
			// Skip installation
			m.step = onboardStepSetup
		}
		return m, nil

	case onboardStepSetup:
		switch {
		case key.Matches(msg, m.keys.Yes), key.Matches(msg, m.keys.Confirm):
			m.runSetup = true
			m.step = onboardStepDone
			return m, tea.Quit
		case key.Matches(msg, m.keys.No), key.Matches(msg, m.keys.Skip):
			m.runSetup = false
			m.step = onboardStepDone
			return m, tea.Quit
		}
		return m, nil

	case onboardStepDone:
		return m, tea.Quit
	}

	return m, nil
}

// ensureLink puts the single global name in place: ~/.local/bin/grove pointing
// at the managed binary. A refusal (something else already owns that name) is
// recorded and shown, never fatal — the rest of onboarding is unaffected.
func (m *onboardModel) ensureLink() {
	path, created, err := ensureGroveLink()
	m.linkPath = path
	m.linkCreated = created
	m.linkError = err
}

func (m *onboardModel) runInstallCmd() tea.Cmd {
	// Build list of selected tools
	var selectedTools []string
	for _, tool := range m.tools {
		if tool.selected {
			selectedTools = append(selectedTools, tool.repoName)
		}
	}

	return func() tea.Msg {
		// Capture stdout/stderr during install
		oldStdout := os.Stdout
		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stdout = w
		os.Stderr = w

		// Run the install with selected tools
		var err error
		if len(selectedTools) > 0 {
			err = runInstall(nil, selectedTools, checkGHAuth())
		}

		// Restore stdout/stderr
		w.Close()
		os.Stdout = oldStdout
		os.Stderr = oldStderr

		// Read captured output
		_, _ = io.Copy(m.installOutput, r)
		r.Close()

		return installDoneMsg{err: err}
	}
}

func (m *onboardModel) View() string {
	if !m.ready {
		return "Initializing..."
	}

	var content strings.Builder
	t := theme.DefaultTheme

	// Page container style
	pageStyle := lipgloss.NewStyle().Padding(1, 2)

	switch m.step {
	case onboardStepWelcome:
		content.WriteString(m.viewWelcome())
	case onboardStepPath:
		content.WriteString(m.viewPath())
	case onboardStepInstall:
		content.WriteString(m.viewInstall())
	case onboardStepSetup:
		content.WriteString(m.viewSetup())
	case onboardStepDone:
		content.WriteString(m.viewDone())
	}

	// Footer
	content.WriteString("\n\n")
	var statusText string
	switch m.step {
	case onboardStepWelcome:
		statusText = "enter: continue • q: quit"
	case onboardStepPath:
		statusText = "y: yes • n: no • q: quit"
	case onboardStepInstall:
		if m.installing {
			statusText = "Installing... please wait"
		} else if m.installDone {
			statusText = "enter: continue • q: quit"
		} else {
			statusText = "space: toggle • enter: install • s: skip • q: quit"
		}
	case onboardStepSetup:
		statusText = "y: yes • n: no • q: quit"
	case onboardStepDone:
		statusText = "enter/q: exit"
	}
	content.WriteString(t.Muted.Render(statusText))

	return pageStyle.Render(content.String())
}

func (m *onboardModel) viewWelcome() string {
	var content strings.Builder
	t := theme.DefaultTheme

	// Header with grove icon
	header := t.Success.Render(theme.IconPineTreeBox) + " " + t.Bold.Render("Welcome to Grove!")
	content.WriteString(header)
	content.WriteString("\n\n")

	// Welcome message
	msg := `Grove is a workspace orchestrator and tool manager for software development.

This wizard will help you:
  1. Put 'grove' on your PATH (the only name Grove claims)
  2. Install all Grove tools
  3. Configure your environment

Let's get you set up!`
	content.WriteString(t.Normal.Render(msg))
	content.WriteString("\n\n")

	content.WriteString(t.Info.Render("Press Enter to continue..."))

	return content.String()
}

func (m *onboardModel) viewPath() string {
	var content strings.Builder
	t := theme.DefaultTheme

	// Header
	header := t.Info.Render(theme.IconShell) + " " + t.Bold.Render("PATH Configuration")
	content.WriteString(header)
	content.WriteString("\n\n")

	// Explain the hub: one name in the user's namespace, tools behind it.
	content.WriteString(t.Normal.Render("Grove puts exactly one name in your PATH:"))
	content.WriteString("\n")
	content.WriteString(t.Highlight.Render("  " + m.linkPath))
	content.WriteString("\n")
	content.WriteString(t.Muted.Render("  every other tool runs as 'grove <tool>' (or 'grove expose <tool>' for a bare name)"))
	content.WriteString("\n\n")

	switch {
	case m.linkError != nil:
		content.WriteString(t.Warning.Render(theme.IconWarning + " " + m.linkError.Error()))
		content.WriteString("\n\n")
	case m.linkCreated:
		content.WriteString(t.Success.Render(theme.IconSuccess) + " Linked " + m.linkPath)
		content.WriteString("\n\n")
	case m.linkPath != "":
		content.WriteString(t.Success.Render(theme.IconSuccess) + " " + m.linkPath + " already points at Grove")
		content.WriteString("\n\n")
	}

	if m.shellManager != nil && m.shellType != "" {
		rcFileName := m.shellManager.GetRcFileName(m.shellType)
		shellName := m.shellManager.GetShellName(m.shellType)
		exportLine := m.shellManager.GetPathExportLine(m.linkDir, m.shellType)

		content.WriteString(t.Normal.Render(m.linkDir + " is not on your PATH yet."))
		content.WriteString("\n\n")

		content.WriteString(t.Normal.Render(fmt.Sprintf("Detected shell: %s", shellName)))
		content.WriteString("\n\n")

		content.WriteString(t.Bold.Render(fmt.Sprintf("Add the following to your %s?", rcFileName)))
		content.WriteString("\n\n")

		// Show the line that will be added
		boxStyle := t.Box.Width(min(m.width-12, 60))
		content.WriteString(boxStyle.Render(exportLine))
		content.WriteString("\n\n")
	} else {
		content.WriteString(t.Warning.Render("Could not detect your shell — add " + m.linkDir + " to your PATH manually."))
		content.WriteString("\n\n")
	}

	content.WriteString(t.Info.Render("[Y]es / [N]o"))

	return content.String()
}

func (m *onboardModel) viewInstall() string {
	var content strings.Builder
	t := theme.DefaultTheme

	// Header
	header := t.Highlight.Render(theme.IconRunning) + " " + t.Bold.Render("Install Grove Tools")
	content.WriteString(header)
	content.WriteString("\n\n")

	// A refused hub link must not be silent — the PATH step is skipped entirely
	// when ~/.local/bin is already on PATH, so this is the only place it shows.
	if m.linkError != nil {
		content.WriteString(t.Warning.Render(theme.IconWarning+" ") + m.linkError.Error())
		content.WriteString("\n\n")
	} else if m.linkCreated {
		content.WriteString(t.Success.Render(theme.IconSuccess) + " Linked " + m.linkPath)
		content.WriteString("\n\n")
	}

	// Show PATH result if we just did that
	if m.pathAdded {
		content.WriteString(t.Success.Render(theme.IconSuccess) + " Added " + m.linkDir + " to your PATH")
		content.WriteString("\n\n")
	} else if m.pathError != nil {
		content.WriteString(t.Error.Render(theme.IconError) + " Failed to add PATH: " + m.pathError.Error())
		content.WriteString("\n\n")
	}

	if m.installing {
		// Show spinner while installing
		content.WriteString(m.installSpinner.View() + " Installing Grove tools...")
		content.WriteString("\n\n")
		content.WriteString(t.Muted.Render("Downloading from GitHub releases..."))
	} else if m.installDone {
		if m.installError != nil {
			content.WriteString(t.Error.Render(theme.IconError) + " Installation failed: " + m.installError.Error())
		} else {
			content.WriteString(t.Success.Render(theme.IconSuccess) + " Selected tools installed successfully!")
		}
		content.WriteString("\n\n")
		content.WriteString(t.Info.Render("Press Enter to continue..."))
	} else {
		// Explain how installation works
		content.WriteString(t.Muted.Render("Tools are downloaded from GitHub releases and installed to:"))
		content.WriteString("\n")
		content.WriteString(t.Highlight.Render("  " + m.binDir))
		content.WriteString("\n")
		content.WriteString(t.Muted.Render("  (that dir stays out of your PATH — run them as 'grove <tool>')"))
		content.WriteString("\n\n")

		// Show tool selection list
		content.WriteString(t.Bold.Render("Select tools to install:"))
		content.WriteString(" " + t.Muted.Render("(space to toggle, enter to install)"))
		content.WriteString("\n\n")

		// Calculate max alias width for alignment
		maxAliasWidth := 0
		for _, tool := range m.tools {
			if len(tool.alias) > maxAliasWidth {
				maxAliasWidth = len(tool.alias)
			}
		}

		for i, tool := range m.tools {
			// Cursor indicator
			cursor := "  "
			if i == m.toolCursor {
				cursor = t.Highlight.Render(theme.IconArrowRightBold) + " "
			}

			// Checkbox
			checkbox := iconCheckboxUnselected
			checkStyle := t.Muted
			if tool.selected {
				checkbox = iconCheckboxSelected
				checkStyle = t.Success
			}

			// Tool name with padding
			aliasStr := fmt.Sprintf("%-*s", maxAliasWidth, tool.alias)

			// Truncate description if needed
			desc := tool.description
			maxDescLen := m.width - maxAliasWidth - 12
			if maxDescLen > 0 && len(desc) > maxDescLen {
				desc = desc[:maxDescLen-3] + "..."
			}

			content.WriteString(fmt.Sprintf("%s%s %s %s\n",
				cursor,
				checkStyle.Render(checkbox),
				t.Bold.Render(aliasStr),
				t.Muted.Render(desc)))
		}

		content.WriteString("\n")
		content.WriteString(t.Muted.Render("Press ") + t.Info.Render("s") + t.Muted.Render(" to skip installation"))
	}

	return content.String()
}

func (m *onboardModel) viewSetup() string {
	var content strings.Builder
	t := theme.DefaultTheme

	// Header
	header := t.Success.Render(theme.IconSparkle) + " " + t.Bold.Render("Initial Configuration")
	content.WriteString(header)
	content.WriteString("\n\n")

	// Show install result
	if m.installDone && m.installError == nil {
		content.WriteString(t.Success.Render(theme.IconSuccess) + " Tools installed successfully!")
		content.WriteString("\n\n")
	}

	content.WriteString(t.Normal.Render("The setup wizard can configure:"))
	content.WriteString("\n\n")

	items := []struct {
		icon string
		name string
		desc string
	}{
		{theme.IconPineTreeBox, "Ecosystem", "Where your projects live"},
		{theme.IconNotebook, "Notebook", "For notes and development plans"},
		{theme.IconRobot, "Gemini API", "LLM-powered features"},
		{theme.IconShell, "tmux bindings", "Quick access to Grove tools"},
	}

	for _, item := range items {
		content.WriteString(fmt.Sprintf("  %s %s %s\n",
			t.Highlight.Render(item.icon),
			t.Bold.Render(item.name),
			t.Muted.Render("- "+item.desc)))
	}
	content.WriteString("\n")

	content.WriteString(t.Bold.Render("Run the setup wizard now? "))
	content.WriteString(t.Info.Render("[Y]es / [N]o"))

	return content.String()
}

func (m *onboardModel) viewDone() string {
	var content strings.Builder
	t := theme.DefaultTheme

	// Header
	header := t.Success.Render(theme.IconSuccess) + " " + t.Bold.Render("Onboarding Complete!")
	content.WriteString(header)
	content.WriteString("\n\n")

	// Summary
	content.WriteString(t.Bold.Render("Summary:"))
	content.WriteString("\n\n")

	if m.linkError != nil {
		content.WriteString(t.Warning.Render(theme.IconWarning+" ") + m.linkError.Error())
		content.WriteString("\n")
	} else if m.linkCreated {
		content.WriteString(t.Success.Render(theme.IconSuccess) + " Linked " + m.linkPath)
		content.WriteString("\n")
	}

	if m.pathAdded {
		content.WriteString(t.Success.Render(theme.IconSuccess) + " PATH configured (" + m.linkDir + ")")
		content.WriteString("\n")
	}

	if m.installDone && m.installError == nil {
		content.WriteString(t.Success.Render(theme.IconSuccess) + " Tools installed")
		content.WriteString("\n")
	}

	content.WriteString("\n")
	content.WriteString(t.Bold.Render("Next Steps:"))
	content.WriteString("\n\n")

	if m.pathAdded {
		content.WriteString(t.Muted.Render("  1. Restart your terminal or run: source ~/" + m.shellManager.GetRcFileName(m.shellType)))
		content.WriteString("\n")
	}

	content.WriteString(t.Muted.Render("  2. Run 'grove list' to see available tools"))
	content.WriteString("\n")

	if !m.runSetup {
		content.WriteString(t.Muted.Render("  3. Run 'grove setup' to configure your environment"))
		content.WriteString("\n")
	}

	// Every tool is reachable as 'grove <tool>'; bare names are opt-in, one at
	// a time, so onboarding never quietly claims names in the user's namespace.
	content.WriteString("\n")
	content.WriteString(t.Muted.Render("Tools run as 'grove <tool>' (e.g. 'grove nb list', 'grove mux')."))
	content.WriteString("\n")
	content.WriteString(t.Muted.Render("Want a bare name? 'grove expose cx' links it into ~/.local/bin ('grove hide cx' undoes it)."))
	content.WriteString("\n")

	return content.String()
}

func newOnboardCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("onboard", "Initial system onboarding")
	cmd.Long = `Initial system onboarding wizard for Grove.

This command is typically run automatically by the install script to guide
new users through the initial setup process:

  1. PATH configuration - Links ~/.local/bin/grove at the managed binary and,
     if that directory is not on your PATH, offers to add it to your shell
     config. Grove claims no other name: tools run as 'grove <tool>'.
  2. Tool installation - Installs all Grove tools from GitHub releases
  3. Setup wizard - Runs the interactive setup wizard for configuration

You can also run this command manually to re-run the onboarding process.`

	// Mark as hidden - intended to be run by install script
	cmd.Hidden = true

	cmd.RunE = runOnboard
	cmd.SilenceUsage = true

	return cmd
}

func init() {
	rootCmd.AddCommand(newOnboardCmd())
}

func runOnboard(cmd *cobra.Command, args []string) error {
	model := newOnboardModel()

	p := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("error running onboarding wizard: %w", err)
	}

	// Check if user wants to run setup
	m, ok := finalModel.(*onboardModel)
	if ok && m.runSetup {
		fmt.Println() // Add spacing
		return runSetup(cmd, args)
	}

	return nil
}
