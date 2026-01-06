package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/tui/components/help"
	"github.com/mattsolo1/grove-core/tui/keymap"
	"github.com/mattsolo1/grove-core/tui/theme"
	"github.com/mattsolo1/grove-meta/pkg/setup"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// Command flags
var (
	setupOnlySteps   []string
	setupDefaults    bool
	setupDryRun      bool
)

// wizardStep represents the current step in the setup wizard
type wizardStep int

const (
	stepSelectComponents wizardStep = iota
	stepEcosystem
	stepNotebook
	stepGeminiKey
	stepTmuxBindings
	stepNeovimPlugin
	stepSummary
)

// componentItem represents a configurable component in the setup wizard
type componentItem struct {
	id          string
	title       string
	description string
	selected    bool
}

func (i componentItem) Title() string       { return i.title }
func (i componentItem) Description() string { return i.description }
func (i componentItem) FilterValue() string { return i.title }

// setupKeyMap defines key bindings for the setup wizard
type setupKeyMap struct {
	keymap.Base
	Select  key.Binding
	Confirm key.Binding
	Back    key.Binding
}

var setupKeys = setupKeyMap{
	Base: keymap.NewBase(),
	Select: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("space", "toggle selection"),
	),
	Confirm: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "confirm"),
	),
	Back: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "go back"),
	),
}

func (k setupKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Select, k.Confirm, k.Base.Quit}
}

func (k setupKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Base.Up, k.Base.Down, k.Select},
		{k.Confirm, k.Back, k.Base.Quit},
	}
}

// componentDelegate renders component items with checkboxes
type componentDelegate struct{}

func (d componentDelegate) Height() int                             { return 2 }
func (d componentDelegate) Spacing() int                            { return 0 }
func (d componentDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d componentDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	i, ok := item.(componentItem)
	if !ok {
		return
	}

	checkbox := "[ ]"
	if i.selected {
		checkbox = "[x]"
	}

	// Style based on selection state
	checkStyle := theme.DefaultTheme.Muted
	if i.selected {
		checkStyle = theme.DefaultTheme.Success
	}

	title := fmt.Sprintf("%s %s", checkStyle.Render(checkbox), i.title)
	desc := theme.DefaultTheme.Muted.Render("    " + i.description)

	// Highlight selected item
	if index == m.Index() {
		title = theme.DefaultTheme.Selected.Render(title)
		desc = theme.DefaultTheme.Selected.Copy().Faint(true).Render(desc)
	}

	fmt.Fprintf(w, "%s\n%s", title, desc)
}

// inputStep represents which input we're currently on
type inputStep int

const (
	inputPath inputStep = iota
	inputName
	inputMethod // for gemini key method selection
	inputValue  // for actual input value
)

// geminiKeyMethod represents how the Gemini API key is provided
type geminiKeyMethod int

const (
	geminiMethodCommand geminiKeyMethod = iota
	geminiMethodDirect
)

// setupModel is the main TUI model for the setup wizard
type setupModel struct {
	// Current wizard state
	step           wizardStep
	selectedSteps  map[string]bool
	components     []componentItem
	componentList  list.Model

	// Text inputs for various steps
	textInput      textinput.Model
	currentInput   inputStep

	// Ecosystem step state
	ecosystemPath  string
	ecosystemName  string

	// Notebook step state
	notebookPath   string

	// Gemini key step state
	geminiMethod   geminiKeyMethod
	geminiValue    string
	methodList     list.Model

	// Service and config
	service        *setup.Service
	yamlHandler    *setup.YAMLHandler

	// TUI state
	keys           setupKeyMap
	help           help.Model
	width          int
	height         int
	ready          bool
	err            error

	// For step navigation
	orderedSteps   []wizardStep
	currentStepIdx int
}

// methodItem for gemini key method selection
type methodItem struct {
	id    string
	title string
	desc  string
}

func (i methodItem) Title() string       { return i.title }
func (i methodItem) Description() string { return i.desc }
func (i methodItem) FilterValue() string { return i.title }

func newSetupModel(service *setup.Service, selectedOnly map[string]bool) *setupModel {
	// Initialize components
	components := []componentItem{
		{id: "ecosystem", title: "Ecosystem Directory", description: "Configure a Grove ecosystem directory", selected: true},
		{id: "notebook", title: "Notebook Directory", description: "Set up a notebook directory for notes and plans", selected: true},
		{id: "gemini", title: "Gemini API Key", description: "Configure Gemini API access for LLM features", selected: true},
		{id: "tmux", title: "tmux Popup Bindings", description: "Add tmux popup bindings for Grove tools", selected: false},
		{id: "nvim", title: "Neovim Plugin", description: "Configure the grove-nvim Neovim plugin", selected: false},
	}

	// Apply --only filter if provided
	if len(selectedOnly) > 0 {
		for i := range components {
			components[i].selected = selectedOnly[components[i].id]
		}
	}

	// Convert to list items
	listItems := make([]list.Item, len(components))
	for i := range components {
		listItems[i] = components[i]
	}

	// Create component list
	delegate := componentDelegate{}
	componentList := list.New(listItems, delegate, 0, 0)
	componentList.Title = "Select Components to Configure"
	componentList.SetShowStatusBar(false)
	componentList.SetShowHelp(false)
	componentList.SetShowPagination(false)
	componentList.SetFilteringEnabled(false)
	componentList.DisableQuitKeybindings()

	// Create gemini method list
	methodItems := []list.Item{
		methodItem{id: "command", title: "Shell Command", desc: "Retrieve API key via a shell command (recommended)"},
		methodItem{id: "direct", title: "Direct Key", desc: "Enter the API key directly"},
	}
	methodList := list.New(methodItems, list.NewDefaultDelegate(), 0, 0)
	methodList.Title = "API Key Method"
	methodList.SetShowStatusBar(false)
	methodList.SetShowHelp(false)
	methodList.SetShowPagination(false)
	methodList.SetFilteringEnabled(false)
	methodList.DisableQuitKeybindings()

	// Create text input
	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 50

	// Set default paths
	homeDir, _ := os.UserHomeDir()
	defaultEcosystemPath := filepath.Join(homeDir, "Code", "grove-projects")
	defaultNotebookPath := filepath.Join(homeDir, "notebooks")

	return &setupModel{
		step:          stepSelectComponents,
		selectedSteps: make(map[string]bool),
		components:    components,
		componentList: componentList,
		textInput:     ti,
		currentInput:  inputPath,
		ecosystemPath: defaultEcosystemPath,
		ecosystemName: "grove-projects",
		notebookPath:  defaultNotebookPath,
		geminiMethod:  geminiMethodCommand,
		geminiValue:   "op read 'op://Private/Gemini API Key/credential' --no-newline",
		methodList:    methodList,
		service:       service,
		yamlHandler:   setup.NewYAMLHandler(service),
		keys:          setupKeys,
		help:          help.New(setupKeys),
		ready:         false,
	}
}

func (m *setupModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.step {
		case stepSelectComponents:
			return m.updateComponentSelection(msg)
		case stepEcosystem:
			return m.updateEcosystemStep(msg)
		case stepNotebook:
			return m.updateNotebookStep(msg)
		case stepGeminiKey:
			return m.updateGeminiKeyStep(msg)
		case stepTmuxBindings:
			return m.updateTmuxBindingsStep(msg)
		case stepNeovimPlugin:
			return m.updateNeovimPluginStep(msg)
		case stepSummary:
			return m.updateSummaryStep(msg)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.componentList.SetSize(msg.Width-4, msg.Height-8)
		m.methodList.SetSize(msg.Width-4, 6)
		// Account for box border (2) + padding (4) + some margin
		m.textInput.Width = msg.Width - 12
		m.ready = true
		return m, nil
	}

	return m, tea.Batch(cmds...)
}

func (m *setupModel) updateComponentSelection(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Base.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.Select):
		// Toggle selection
		idx := m.componentList.Index()
		if idx >= 0 && idx < len(m.components) {
			m.components[idx].selected = !m.components[idx].selected
			// Update list item
			listItems := make([]list.Item, len(m.components))
			for i := range m.components {
				listItems[i] = m.components[i]
			}
			m.componentList.SetItems(listItems)
		}
		return m, nil

	case key.Matches(msg, m.keys.Confirm):
		// Build list of selected steps and transition
		m.buildOrderedSteps()
		if len(m.orderedSteps) > 0 {
			m.currentStepIdx = 0
			m.step = m.orderedSteps[0]
			m.prepareStepInput()
		} else {
			m.step = stepSummary
		}
		return m, nil
	}

	// Update list navigation
	var cmd tea.Cmd
	m.componentList, cmd = m.componentList.Update(msg)
	return m, cmd
}

func (m *setupModel) buildOrderedSteps() {
	m.orderedSteps = nil
	for _, c := range m.components {
		if c.selected {
			m.selectedSteps[c.id] = true
			switch c.id {
			case "ecosystem":
				m.orderedSteps = append(m.orderedSteps, stepEcosystem)
			case "notebook":
				m.orderedSteps = append(m.orderedSteps, stepNotebook)
			case "gemini":
				m.orderedSteps = append(m.orderedSteps, stepGeminiKey)
			case "tmux":
				m.orderedSteps = append(m.orderedSteps, stepTmuxBindings)
			case "nvim":
				m.orderedSteps = append(m.orderedSteps, stepNeovimPlugin)
			}
		}
	}
}

func (m *setupModel) prepareStepInput() {
	switch m.step {
	case stepEcosystem:
		m.currentInput = inputPath
		m.textInput.SetValue(m.ecosystemPath)
		m.textInput.Placeholder = "Path to ecosystem directory"
	case stepNotebook:
		m.currentInput = inputPath
		m.textInput.SetValue(m.notebookPath)
		m.textInput.Placeholder = "Path to notebook directory"
	case stepGeminiKey:
		m.currentInput = inputMethod
	}
}

func (m *setupModel) nextStep() {
	m.currentStepIdx++
	if m.currentStepIdx < len(m.orderedSteps) {
		m.step = m.orderedSteps[m.currentStepIdx]
		m.prepareStepInput()
	} else {
		m.step = stepSummary
		m.executeSetup()
	}
}

func (m *setupModel) prevStep() {
	if m.currentStepIdx > 0 {
		m.currentStepIdx--
		m.step = m.orderedSteps[m.currentStepIdx]
		m.prepareStepInput()
	} else {
		m.step = stepSelectComponents
	}
}

func (m *setupModel) updateEcosystemStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Base.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Back):
		m.prevStep()
		return m, nil
	case key.Matches(msg, m.keys.Confirm):
		if m.currentInput == inputPath {
			m.ecosystemPath = m.textInput.Value()
			m.currentInput = inputName
			m.textInput.SetValue(m.ecosystemName)
			m.textInput.Placeholder = "Ecosystem name"
		} else {
			m.ecosystemName = m.textInput.Value()
			m.nextStep()
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m *setupModel) updateNotebookStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Base.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Back):
		m.prevStep()
		return m, nil
	case key.Matches(msg, m.keys.Confirm):
		m.notebookPath = m.textInput.Value()
		m.nextStep()
		return m, nil
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m *setupModel) updateGeminiKeyStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Base.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Back):
		if m.currentInput == inputValue {
			m.currentInput = inputMethod
		} else {
			m.prevStep()
		}
		return m, nil
	case key.Matches(msg, m.keys.Confirm):
		if m.currentInput == inputMethod {
			idx := m.methodList.Index()
			if idx == 0 {
				m.geminiMethod = geminiMethodCommand
				m.textInput.SetValue("op read 'op://Private/Gemini API Key/credential' --no-newline")
				m.textInput.Placeholder = "Command to retrieve API key"
			} else {
				m.geminiMethod = geminiMethodDirect
				m.textInput.SetValue("")
				m.textInput.Placeholder = "Gemini API key"
				m.textInput.EchoMode = textinput.EchoPassword
			}
			m.currentInput = inputValue
		} else {
			m.geminiValue = m.textInput.Value()
			m.textInput.EchoMode = textinput.EchoNormal
			m.nextStep()
		}
		return m, nil
	}

	if m.currentInput == inputMethod {
		var cmd tea.Cmd
		m.methodList, cmd = m.methodList.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m *setupModel) updateTmuxBindingsStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Base.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Back):
		m.prevStep()
		return m, nil
	case key.Matches(msg, m.keys.Confirm):
		m.nextStep()
		return m, nil
	}
	return m, nil
}

func (m *setupModel) updateNeovimPluginStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Base.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Back):
		m.prevStep()
		return m, nil
	case key.Matches(msg, m.keys.Confirm):
		m.nextStep()
		return m, nil
	}
	return m, nil
}

func (m *setupModel) updateSummaryStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Base.Quit), key.Matches(msg, m.keys.Confirm):
		return m, tea.Quit
	}
	return m, nil
}

func (m *setupModel) executeSetup() {
	// Execute each selected configuration step
	if m.selectedSteps["ecosystem"] {
		m.setupEcosystem()
	}
	if m.selectedSteps["notebook"] {
		m.setupNotebook()
	}
	if m.selectedSteps["gemini"] {
		m.setupGeminiKey()
	}
	if m.selectedSteps["tmux"] {
		m.setupTmuxBindings()
	}
	if m.selectedSteps["nvim"] {
		m.setupNeovimPlugin()
	}
}

func (m *setupModel) setupEcosystem() {
	// Create ecosystem directory
	m.service.MkdirAll(m.ecosystemPath, 0755)

	// Create grove.yml for ecosystem
	groveYMLContent := fmt.Sprintf(`name: %s
description: Grove ecosystem
workspaces:
  - "*"
`, m.ecosystemName)
	m.service.WriteFile(filepath.Join(m.ecosystemPath, "grove.yml"), []byte(groveYMLContent), 0644)

	// Create go.work
	goWorkContent := `go 1.24.4

use (
)
`
	m.service.WriteFile(filepath.Join(m.ecosystemPath, "go.work"), []byte(goWorkContent), 0644)

	// Create .gitignore
	gitignoreContent := `# Binaries
bin/
*.exe

# Test and coverage
*.test
*.out
coverage.html

# OS files
.DS_Store
Thumbs.db

# IDE files
.vscode/
.idea/
*.swp
*.swo

# Temporary files
*.tmp
*.bak
`
	m.service.WriteFile(filepath.Join(m.ecosystemPath, ".gitignore"), []byte(gitignoreContent), 0644)

	// Create Makefile
	makefileContent := `# Grove ecosystem Makefile

.PHONY: all build test clean

PACKAGES ?=
BINARIES ?=

all: build

build:
	@echo "Building all packages..."
	@for pkg in $(PACKAGES); do \
		echo "Building $$pkg..."; \
		$(MAKE) -C $$pkg build || exit 1; \
	done

test:
	@echo "Testing all packages..."
	@for pkg in $(PACKAGES); do \
		echo "Testing $$pkg..."; \
		$(MAKE) -C $$pkg test || exit 1; \
	done

clean:
	@echo "Cleaning all packages..."
	@for pkg in $(PACKAGES); do \
		echo "Cleaning $$pkg..."; \
		$(MAKE) -C $$pkg clean || exit 1; \
	done
`
	m.service.WriteFile(filepath.Join(m.ecosystemPath, "Makefile"), []byte(makefileContent), 0644)

	// Create .grove/workspace marker
	m.service.MkdirAll(filepath.Join(m.ecosystemPath, ".grove"), 0755)
	timestamp := time.Now().UTC().Format(time.RFC3339)
	workspaceContent := fmt.Sprintf(`branch: main
plan: %s-ecosystem-root
created_at: %s
ecosystem: true
repos: []
`, m.ecosystemName, timestamp)
	m.service.WriteFile(filepath.Join(m.ecosystemPath, ".grove", "workspace"), []byte(workspaceContent), 0644)

	// Update global config with ecosystem path
	root, err := m.yamlHandler.LoadGlobalConfig()
	if err == nil {
		// Add to groves section
		setup.SetValue(root, map[string]interface{}{
			"path":    m.ecosystemPath,
			"enabled": true,
		}, "groves", m.ecosystemName)
		m.yamlHandler.SaveGlobalConfig(root)
	}
}

func (m *setupModel) setupNotebook() {
	// Create notebook directory
	m.service.MkdirAll(m.notebookPath, 0755)

	// Update global config
	root, err := m.yamlHandler.LoadGlobalConfig()
	if err == nil {
		setup.SetValue(root, m.notebookPath, "notebooks", "path")
		m.yamlHandler.SaveGlobalConfig(root)
	}
}

func (m *setupModel) setupGeminiKey() {
	root, err := m.yamlHandler.LoadGlobalConfig()
	if err == nil {
		if m.geminiMethod == geminiMethodCommand {
			setup.SetValue(root, m.geminiValue, "gemini", "api_key_command")
		} else {
			setup.SetValue(root, m.geminiValue, "gemini", "api_key")
		}
		m.yamlHandler.SaveGlobalConfig(root)
	}
}

func (m *setupModel) setupTmuxBindings() {
	popupsContent := `# Grove tmux popup bindings
# Source this file from your tmux.conf: source-file ~/.config/tmux/popups.conf

# Popup dimensions
POPUP_WIDTH="90%"
POPUP_HEIGHT="85%"

# Claude Code popup (prefix + c)
bind-key c display-popup -E -w "$POPUP_WIDTH" -h "$POPUP_HEIGHT" -d "#{pane_current_path}" "claude"

# Grove flow popup (prefix + f)
bind-key f display-popup -E -w "$POPUP_WIDTH" -h "$POPUP_HEIGHT" -d "#{pane_current_path}" "grove flow"

# Grove notebook popup (prefix + n)
bind-key n display-popup -E -w "$POPUP_WIDTH" -h "$POPUP_HEIGHT" -d "#{pane_current_path}" "grove nb"

# Context builder popup (prefix + x)
bind-key x display-popup -E -w "$POPUP_WIDTH" -h "$POPUP_HEIGHT" -d "#{pane_current_path}" "cx"
`
	m.service.WriteFile("~/.config/tmux/popups.conf", []byte(popupsContent), 0644)

	// Check if tmux.conf already sources popups.conf
	tmuxConfPath := "~/.config/tmux/tmux.conf"
	contains, _ := m.service.FileContains(tmuxConfPath, "popups.conf")
	if !contains {
		m.service.AppendToFile(tmuxConfPath, "\n# Grove popup bindings\nsource-file ~/.config/tmux/popups.conf\n")
	}
}

func (m *setupModel) setupNeovimPlugin() {
	nvimPluginContent := `-- Grove Neovim plugin configuration
-- Add this to your lazy.nvim plugin specs

return {
  {
    dir = "~/.grove/nvim-plugins/grove-nvim",
    name = "grove-nvim",
    dependencies = {
      "nvim-lua/plenary.nvim",
      "nvim-telescope/telescope.nvim",
    },
    config = function()
      require("grove-nvim").setup({
        -- Enable context file jumping
        context_jump = true,
        -- Enable workflow integration
        workflow = true,
        -- Keymaps
        keymaps = {
          -- Jump to context file under cursor
          context_jump = "<leader>gc",
          -- Open Grove flow
          flow = "<leader>gf",
          -- Open Grove notebook
          notebook = "<leader>gn",
        },
      })
    end,
  },
}
`
	m.service.WriteFile("~/.config/nvim/lua/plugins/grove.lua", []byte(nvimPluginContent), 0644)
}

func (m *setupModel) View() string {
	if !m.ready {
		return "Initializing..."
	}

	var content strings.Builder

	// Header
	headerStyle := theme.DefaultTheme.Header.Copy().
		Width(m.width).
		Padding(1, 2)

	switch m.step {
	case stepSelectComponents:
		content.WriteString(headerStyle.Render("Grove Setup Wizard"))
		content.WriteString("\n\n")
		content.WriteString(m.viewComponentSelection())

	case stepEcosystem:
		content.WriteString(headerStyle.Render("Ecosystem Directory"))
		content.WriteString("\n\n")
		content.WriteString(m.viewEcosystemStep())

	case stepNotebook:
		content.WriteString(headerStyle.Render("Notebook Directory"))
		content.WriteString("\n\n")
		content.WriteString(m.viewNotebookStep())

	case stepGeminiKey:
		content.WriteString(headerStyle.Render("Gemini API Key"))
		content.WriteString("\n\n")
		content.WriteString(m.viewGeminiKeyStep())

	case stepTmuxBindings:
		content.WriteString(headerStyle.Render("tmux Popup Bindings"))
		content.WriteString("\n\n")
		content.WriteString(m.viewTmuxBindingsStep())

	case stepNeovimPlugin:
		content.WriteString(headerStyle.Render("Neovim Plugin"))
		content.WriteString("\n\n")
		content.WriteString(m.viewNeovimPluginStep())

	case stepSummary:
		content.WriteString(headerStyle.Render("Setup Complete"))
		content.WriteString("\n\n")
		content.WriteString(m.viewSummary())
	}

	// Status bar
	statusStyle := theme.DefaultTheme.Muted.Copy().
		Width(m.width).
		Padding(0, 2)

	var statusText string
	if m.service.IsDryRun() {
		statusText = "[DRY RUN] "
	}

	switch m.step {
	case stepSelectComponents:
		statusText += "space: toggle | enter: confirm | q: quit"
	case stepSummary:
		statusText += "enter/q: exit"
	default:
		statusText += "enter: confirm | esc: back | q: quit"
	}

	content.WriteString("\n")
	content.WriteString(statusStyle.Render(statusText))

	return content.String()
}

func (m *setupModel) viewComponentSelection() string {
	return m.componentList.View()
}

func (m *setupModel) viewEcosystemStep() string {
	var content strings.Builder

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Muted.GetForeground()).
		Padding(1, 2).
		Width(m.width - 4)

	if m.currentInput == inputPath {
		content.WriteString("Where should your ecosystem be created?\n\n")
		content.WriteString(m.textInput.View())
		content.WriteString("\n\n")
		content.WriteString(theme.DefaultTheme.Muted.Render("The filesystem directory where your projects will live."))
	} else {
		content.WriteString("What should this ecosystem be called?\n\n")
		content.WriteString(m.textInput.View())
		content.WriteString("\n\n")
		content.WriteString(theme.DefaultTheme.Muted.Render(fmt.Sprintf("Used in config files to identify this ecosystem. Path: %s", m.ecosystemPath)))
	}

	return boxStyle.Render(content.String())
}

func (m *setupModel) viewNotebookStep() string {
	var content strings.Builder

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Muted.GetForeground()).
		Padding(1, 2).
		Width(m.width - 4)

	content.WriteString("Enter the path for your notebook directory:\n\n")
	content.WriteString(m.textInput.View())
	content.WriteString("\n\n")
	content.WriteString(theme.DefaultTheme.Muted.Render("This directory will store your notes and plans."))

	return boxStyle.Render(content.String())
}

func (m *setupModel) viewGeminiKeyStep() string {
	var content strings.Builder

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Muted.GetForeground()).
		Padding(1, 2).
		Width(m.width - 4)

	if m.currentInput == inputMethod {
		content.WriteString("How would you like to provide your Gemini API key?\n\n")
		content.WriteString(m.methodList.View())
	} else {
		if m.geminiMethod == geminiMethodCommand {
			content.WriteString("Enter the command to retrieve your API key:\n\n")
			content.WriteString(m.textInput.View())
			content.WriteString("\n\n")
			content.WriteString(theme.DefaultTheme.Muted.Render("Example: op read 'op://Private/Gemini API Key/credential' --no-newline"))
		} else {
			content.WriteString("Enter your Gemini API key:\n\n")
			content.WriteString(m.textInput.View())
			content.WriteString("\n\n")
			content.WriteString(theme.DefaultTheme.Muted.Render("The key will be stored in your global grove config."))
		}
	}

	return boxStyle.Render(content.String())
}

func (m *setupModel) viewTmuxBindingsStep() string {
	var content strings.Builder

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Muted.GetForeground()).
		Padding(1, 2).
		Width(m.width - 4)

	content.WriteString("The following tmux bindings will be configured:\n\n")

	bindingsStyle := theme.DefaultTheme.Muted
	content.WriteString(bindingsStyle.Render("  prefix + c  →  Claude Code popup\n"))
	content.WriteString(bindingsStyle.Render("  prefix + f  →  Grove flow popup\n"))
	content.WriteString(bindingsStyle.Render("  prefix + n  →  Grove notebook popup\n"))
	content.WriteString(bindingsStyle.Render("  prefix + x  →  Context builder popup\n"))
	content.WriteString("\n")
	content.WriteString(theme.DefaultTheme.Muted.Render("Files to be created/modified:\n"))
	content.WriteString(theme.DefaultTheme.Muted.Render("  ~/.config/tmux/popups.conf\n"))
	content.WriteString(theme.DefaultTheme.Muted.Render("  ~/.config/tmux/tmux.conf (append source-file)"))
	content.WriteString("\n\n")
	content.WriteString("Press Enter to continue or esc to go back.")

	return boxStyle.Render(content.String())
}

func (m *setupModel) viewNeovimPluginStep() string {
	var content strings.Builder

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Muted.GetForeground()).
		Padding(1, 2).
		Width(m.width - 4)

	content.WriteString("The grove-nvim plugin configuration will be created:\n\n")
	content.WriteString(theme.DefaultTheme.Muted.Render("  ~/.config/nvim/lua/plugins/grove.lua\n\n"))
	content.WriteString("Features:\n")
	content.WriteString(theme.DefaultTheme.Muted.Render("  - Context file jumping (<leader>gc)\n"))
	content.WriteString(theme.DefaultTheme.Muted.Render("  - Grove flow integration (<leader>gf)\n"))
	content.WriteString(theme.DefaultTheme.Muted.Render("  - Grove notebook access (<leader>gn)\n"))
	content.WriteString("\n")
	content.WriteString("Press Enter to continue or esc to go back.")

	return boxStyle.Render(content.String())
}

func (m *setupModel) viewSummary() string {
	var content strings.Builder

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Success.GetForeground()).
		Padding(1, 2).
		Width(m.width - 4)

	if m.service.IsDryRun() {
		content.WriteString(theme.DefaultTheme.Warning.Render("DRY RUN - No changes were made\n\n"))
		content.WriteString("The following actions would be performed:\n\n")
	} else {
		content.WriteString(theme.DefaultTheme.Success.Render("Setup completed successfully!\n\n"))
		content.WriteString("Actions performed:\n\n")
	}

	checkStyle := theme.DefaultTheme.Success
	for _, action := range m.service.Actions() {
		if action.Success {
			content.WriteString(checkStyle.Render("  ✓ "))
			content.WriteString(action.Description)
			content.WriteString("\n")
		} else {
			content.WriteString(theme.DefaultTheme.Error.Render("  ✗ "))
			content.WriteString(action.Description)
			if action.Error != nil {
				content.WriteString(fmt.Sprintf(" (%s)", action.Error.Error()))
			}
			content.WriteString("\n")
		}
	}

	content.WriteString("\n")
	content.WriteString(theme.DefaultTheme.Header.Render("Next Steps:\n"))
	content.WriteString(theme.DefaultTheme.Muted.Render("  1. Restart your terminal or source your shell config\n"))
	content.WriteString(theme.DefaultTheme.Muted.Render("  2. Run 'grove list' to see available tools\n"))
	content.WriteString(theme.DefaultTheme.Muted.Render("  3. Start building with 'grove build' in your ecosystem\n"))

	return boxStyle.Render(content.String())
}

func newSetupCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("setup", "Interactive setup wizard for Grove")
	cmd.Long = `Interactive setup wizard for configuring Grove.

The setup wizard guides you through configuring:
- Ecosystem directory: Where your Grove projects live
- Notebook directory: For notes and development plans
- Gemini API key: For LLM-powered features
- tmux popup bindings: Quick access to Grove tools
- Neovim plugin: IDE integration

Examples:
  # Run the interactive setup wizard
  grove setup

  # Run with defaults (non-interactive)
  grove setup --defaults

  # Run specific steps only
  grove setup --only ecosystem,notebook

  # Preview changes without making them
  grove setup --dry-run`

	cmd.RunE = runSetup
	cmd.SilenceUsage = true

	cmd.Flags().StringSliceVar(&setupOnlySteps, "only", nil, "Run only specific setup steps (ecosystem,notebook,gemini,tmux,nvim)")
	cmd.Flags().BoolVar(&setupDefaults, "defaults", false, "Use default values without interactive prompts")
	cmd.Flags().BoolVar(&setupDryRun, "dry-run", false, "Preview changes without making them")

	return cmd
}

func runSetup(cmd *cobra.Command, args []string) error {
	logger := logging.NewLogger("setup")
	pretty := logging.NewPrettyLogger()

	// Create service
	service := setup.NewService(setupDryRun)

	// Parse --only flag
	selectedOnly := make(map[string]bool)
	if len(setupOnlySteps) > 0 {
		for _, step := range setupOnlySteps {
			selectedOnly[strings.TrimSpace(step)] = true
		}
	}

	// Non-interactive mode
	if setupDefaults {
		return runSetupDefaults(service, selectedOnly, logger, pretty)
	}

	// Interactive TUI mode
	model := newSetupModel(service, selectedOnly)

	// If --only was provided, skip component selection
	if len(selectedOnly) > 0 {
		model.buildOrderedSteps()
		if len(model.orderedSteps) > 0 {
			model.step = model.orderedSteps[0]
			model.prepareStepInput()
		}
	}

	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("error running setup wizard: %w", err)
	}

	return nil
}

func runSetupDefaults(service *setup.Service, selectedOnly map[string]bool, logger *logrus.Entry, pretty *logging.PrettyLogger) error {
	yamlHandler := setup.NewYAMLHandler(service)

	// Determine which steps to run
	runAll := len(selectedOnly) == 0

	homeDir, _ := os.UserHomeDir()

	if runAll || selectedOnly["ecosystem"] {
		pretty.InfoPretty("Setting up ecosystem directory...")
		ecosystemPath := filepath.Join(homeDir, "Code", "grove-projects")
		ecosystemName := "grove-projects"

		service.MkdirAll(ecosystemPath, 0755)

		groveYMLContent := fmt.Sprintf(`name: %s
description: Grove ecosystem
workspaces:
  - "*"
`, ecosystemName)
		service.WriteFile(filepath.Join(ecosystemPath, "grove.yml"), []byte(groveYMLContent), 0644)

		goWorkContent := `go 1.24.4

use (
)
`
		service.WriteFile(filepath.Join(ecosystemPath, "go.work"), []byte(goWorkContent), 0644)

		root, _ := yamlHandler.LoadGlobalConfig()
		setup.SetValue(root, map[string]interface{}{
			"path":    ecosystemPath,
			"enabled": true,
		}, "groves", ecosystemName)
		yamlHandler.SaveGlobalConfig(root)
	}

	if runAll || selectedOnly["notebook"] {
		pretty.InfoPretty("Setting up notebook directory...")
		notebookPath := filepath.Join(homeDir, "notebooks")
		service.MkdirAll(notebookPath, 0755)

		root, _ := yamlHandler.LoadGlobalConfig()
		setup.SetValue(root, notebookPath, "notebooks", "path")
		yamlHandler.SaveGlobalConfig(root)
	}

	if selectedOnly["gemini"] {
		pretty.InfoPretty("Skipping Gemini API key (requires interactive input)...")
	}

	if selectedOnly["tmux"] {
		pretty.InfoPretty("Setting up tmux bindings...")
		popupsContent := `# Grove tmux popup bindings
bind-key c display-popup -E -w "90%" -h "85%" -d "#{pane_current_path}" "claude"
bind-key f display-popup -E -w "90%" -h "85%" -d "#{pane_current_path}" "grove flow"
bind-key n display-popup -E -w "90%" -h "85%" -d "#{pane_current_path}" "grove nb"
bind-key x display-popup -E -w "90%" -h "85%" -d "#{pane_current_path}" "cx"
`
		service.WriteFile("~/.config/tmux/popups.conf", []byte(popupsContent), 0644)

		tmuxConfPath := "~/.config/tmux/tmux.conf"
		contains, _ := service.FileContains(tmuxConfPath, "popups.conf")
		if !contains {
			service.AppendToFile(tmuxConfPath, "\nsource-file ~/.config/tmux/popups.conf\n")
		}
	}

	if selectedOnly["nvim"] {
		pretty.InfoPretty("Setting up Neovim plugin...")
		nvimPluginContent := `return {
  {
    dir = "~/.grove/nvim-plugins/grove-nvim",
    name = "grove-nvim",
    config = function()
      require("grove-nvim").setup({})
    end,
  },
}
`
		service.WriteFile("~/.config/nvim/lua/plugins/grove.lua", []byte(nvimPluginContent), 0644)
	}

	// Print summary
	pretty.Success("Setup complete!")
	for _, action := range service.Actions() {
		if action.Success {
			pretty.InfoPretty(fmt.Sprintf("  ✓ %s", action.Description))
		} else {
			pretty.InfoPretty(fmt.Sprintf("  ✗ %s: %v", action.Description, action.Error))
		}
	}

	return nil
}
