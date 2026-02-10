package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/logging"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/tui/components/help"
	"github.com/grovetools/core/tui/keymap"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/setup"
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
	stepConfigFormat
	stepTUITheme
	stepEcosystem
	stepFirstProject
	stepNotebook
	stepGeminiKey
	stepFlowSettings
	stepTmuxBindings
	stepAgentSettings
	stepNeovimPlugin
	stepPlanPreservation
	stepSummary
)

// configFormat represents the configuration file format
type configFormat int

const (
	formatYAML configFormat = iota
	formatTOML
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

	// Cursor: fat arrow for selected row
	cursor := "  "
	if index == m.Index() {
		cursor = theme.DefaultTheme.Highlight.Render(theme.IconArrowRightBold) + " "
	}

	// Checkbox using theme icons
	checkbox := theme.IconStatusTodo
	checkStyle := theme.DefaultTheme.Muted
	if i.selected {
		checkbox = theme.IconStatusCompleted
		checkStyle = theme.DefaultTheme.Success
	}

	// Title styling
	title := i.title
	if index == m.Index() {
		title = theme.DefaultTheme.Bold.Render(title)
	}

	// Description
	desc := theme.DefaultTheme.Muted.Render(i.description)

	// Render: cursor + checkbox + title on first line, indented description on second
	fmt.Fprintf(w, "%s%s %s\n", cursor, checkStyle.Render(checkbox), title)
	fmt.Fprintf(w, "     %s", desc)
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

	// Config format step state
	configFormat   configFormat
	formatList     list.Model

	// TUI theme step state
	tuiTheme       string
	themeList      list.Model

	// Ecosystem step state
	ecosystemPath  string
	ecosystemName  string

	// First project step state
	firstProjectName string
	skipFirstProject bool

	// Notebook step state
	notebookPath   string

	// Gemini key step state
	geminiMethod   geminiKeyMethod
	geminiValue    string
	methodList     list.Model

	// Flow settings step state
	flowOneshotModel string

	// Agent settings step state
	claudeArgs     []string

	// Hooks step state
	planPreservationEnabled bool

	// Service and config handlers
	service        *setup.Service
	yamlHandler    *setup.YAMLHandler
	tomlHandler    *setup.TOMLHandler

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
		{id: "tui", title: "TUI Theme", description: "Select the color theme for terminal interfaces", selected: true},
		{id: "ecosystem", title: "Ecosystem Directory", description: "Configure a Grove ecosystem directory", selected: true},
		{id: "notebook", title: "Notebook Directory", description: "Set up a notebook directory for notes and plans", selected: true},
		{id: "gemini", title: "Gemini API Key", description: "Configure Gemini API access for LLM features", selected: true},
		{id: "flow", title: "Flow Settings", description: "Configure default model for LLM jobs", selected: true},
		{id: "tmux", title: "tmux Popup Bindings", description: "Add tmux popup bindings for Grove tools", selected: false},
		{id: "agent", title: "Agent Settings", description: "Configure Claude agent arguments", selected: false},
		{id: "nvim", title: "Neovim Plugin", description: "Configure the grove-nvim Neovim plugin", selected: false},
		{id: "hooks", title: "Hooks Configuration", description: "Configure plan preservation for Claude Code plans", selected: false},
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
	componentList.SetShowTitle(false)
	componentList.SetShowStatusBar(false)
	componentList.SetShowHelp(false)
	componentList.SetShowPagination(false)
	componentList.SetFilteringEnabled(false)
	componentList.DisableQuitKeybindings()

	// Create config format list
	formatItems := []list.Item{
		methodItem{id: "toml", title: "TOML", desc: "Modern, clean configuration format (recommended)"},
		methodItem{id: "yaml", title: "YAML", desc: "Traditional YAML configuration format"},
	}
	formatList := list.New(formatItems, list.NewDefaultDelegate(), 0, 0)
	formatList.Title = "Configuration Format"
	formatList.SetShowStatusBar(false)
	formatList.SetShowHelp(false)
	formatList.SetShowPagination(false)
	formatList.SetFilteringEnabled(false)
	formatList.DisableQuitKeybindings()

	// Create theme list
	themeItems := []list.Item{
		methodItem{id: "terminal", title: "Terminal", desc: "Uses your terminal's default colors"},
		methodItem{id: "gruvbox", title: "Gruvbox", desc: "Warm, retro color scheme"},
		methodItem{id: "kanagawa", title: "Kanagawa", desc: "Dark theme inspired by Japanese art"},
	}
	themeList := list.New(themeItems, list.NewDefaultDelegate(), 0, 0)
	themeList.Title = "TUI Theme"
	themeList.SetShowStatusBar(false)
	themeList.SetShowHelp(false)
	themeList.SetShowPagination(false)
	themeList.SetFilteringEnabled(false)
	themeList.DisableQuitKeybindings()

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
		step:             stepSelectComponents,
		selectedSteps:   make(map[string]bool),
		components:      components,
		componentList:   componentList,
		textInput:       ti,
		currentInput:    inputPath,
		configFormat:    formatTOML,
		formatList:      formatList,
		tuiTheme:        "terminal",
		themeList:       themeList,
		ecosystemPath:   defaultEcosystemPath,
		ecosystemName:   "grove-projects",
		notebookPath:    defaultNotebookPath,
		geminiMethod:    geminiMethodCommand,
		geminiValue:     "op read 'op://Private/Gemini API Key/credential' --no-newline",
		methodList:      methodList,
		flowOneshotModel: "gemini-3-pro-preview",
		claudeArgs:      []string{"--dangerously-skip-permissions", "--chrome"},
		service:         service,
		yamlHandler:     setup.NewYAMLHandler(service),
		tomlHandler:     setup.NewTOMLHandler(service),
		keys:            setupKeys,
		help:            help.New(setupKeys),
		ready:           false,
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
		case stepConfigFormat:
			return m.updateConfigFormatStep(msg)
		case stepTUITheme:
			return m.updateTUIThemeStep(msg)
		case stepEcosystem:
			return m.updateEcosystemStep(msg)
		case stepFirstProject:
			return m.updateFirstProjectStep(msg)
		case stepNotebook:
			return m.updateNotebookStep(msg)
		case stepGeminiKey:
			return m.updateGeminiKeyStep(msg)
		case stepFlowSettings:
			return m.updateFlowSettingsStep(msg)
		case stepTmuxBindings:
			return m.updateTmuxBindingsStep(msg)
		case stepAgentSettings:
			return m.updateAgentSettingsStep(msg)
		case stepNeovimPlugin:
			return m.updateNeovimPluginStep(msg)
		case stepPlanPreservation:
			return m.updatePlanPreservationStep(msg)
		case stepSummary:
			return m.updateSummaryStep(msg)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.componentList.SetSize(msg.Width-4, msg.Height-8)
		m.methodList.SetSize(msg.Width-4, 6)
		m.formatList.SetSize(msg.Width-4, 6)
		m.themeList.SetSize(msg.Width-4, 8)
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

func (m *setupModel) updateConfigFormatStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Base.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Back):
		m.prevStep()
		return m, nil
	case key.Matches(msg, m.keys.Confirm):
		idx := m.formatList.Index()
		if idx == 0 {
			m.configFormat = formatTOML
		} else {
			m.configFormat = formatYAML
		}
		m.nextStep()
		return m, nil
	}

	var cmd tea.Cmd
	m.formatList, cmd = m.formatList.Update(msg)
	return m, cmd
}

func (m *setupModel) updateTUIThemeStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Base.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Back):
		m.prevStep()
		return m, nil
	case key.Matches(msg, m.keys.Confirm):
		idx := m.themeList.Index()
		switch idx {
		case 0:
			m.tuiTheme = "terminal"
		case 1:
			m.tuiTheme = "gruvbox"
		case 2:
			m.tuiTheme = "kanagawa"
		}
		m.nextStep()
		return m, nil
	}

	var cmd tea.Cmd
	m.themeList, cmd = m.themeList.Update(msg)
	return m, cmd
}

func (m *setupModel) buildOrderedSteps() {
	m.orderedSteps = nil

	// Config format is always first
	m.orderedSteps = append(m.orderedSteps, stepConfigFormat)

	for _, c := range m.components {
		if c.selected {
			m.selectedSteps[c.id] = true
			switch c.id {
			case "tui":
				m.orderedSteps = append(m.orderedSteps, stepTUITheme)
			case "ecosystem":
				m.orderedSteps = append(m.orderedSteps, stepEcosystem)
				m.orderedSteps = append(m.orderedSteps, stepFirstProject)
			case "notebook":
				m.orderedSteps = append(m.orderedSteps, stepNotebook)
			case "gemini":
				m.orderedSteps = append(m.orderedSteps, stepGeminiKey)
			case "flow":
				m.orderedSteps = append(m.orderedSteps, stepFlowSettings)
			case "tmux":
				m.orderedSteps = append(m.orderedSteps, stepTmuxBindings)
			case "agent":
				m.orderedSteps = append(m.orderedSteps, stepAgentSettings)
			case "nvim":
				m.orderedSteps = append(m.orderedSteps, stepNeovimPlugin)
			case "hooks":
				m.orderedSteps = append(m.orderedSteps, stepPlanPreservation)
			}
		}
	}
}

func (m *setupModel) prepareStepInput() {
	switch m.step {
	case stepConfigFormat:
		// No text input needed, uses formatList
	case stepTUITheme:
		// No text input needed, uses themeList
	case stepEcosystem:
		m.currentInput = inputPath
		m.textInput.SetValue(m.ecosystemPath)
		m.textInput.Placeholder = "Path to ecosystem directory"
	case stepFirstProject:
		m.currentInput = inputName
		m.textInput.SetValue(m.firstProjectName)
		m.textInput.Placeholder = "my-project"
	case stepNotebook:
		m.currentInput = inputPath
		m.textInput.SetValue(m.notebookPath)
		m.textInput.Placeholder = "Path to notebook directory"
	case stepGeminiKey:
		m.currentInput = inputMethod
	case stepFlowSettings:
		m.currentInput = inputValue
		m.textInput.SetValue(m.flowOneshotModel)
		m.textInput.Placeholder = "gemini-3-pro-preview"
	case stepAgentSettings:
		m.currentInput = inputValue
		m.textInput.SetValue(strings.Join(m.claudeArgs, ", "))
		m.textInput.Placeholder = "--dangerously-skip-permissions, --chrome"
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

func (m *setupModel) updateFirstProjectStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Base.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Back):
		m.prevStep()
		return m, nil
	case key.Matches(msg, m.keys.Confirm):
		m.firstProjectName = m.textInput.Value()
		m.skipFirstProject = m.firstProjectName == ""
		m.nextStep()
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

func (m *setupModel) updateFlowSettingsStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Base.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Back):
		m.prevStep()
		return m, nil
	case key.Matches(msg, m.keys.Confirm):
		m.flowOneshotModel = m.textInput.Value()
		m.nextStep()
		return m, nil
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

func (m *setupModel) updateAgentSettingsStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Base.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Back):
		m.prevStep()
		return m, nil
	case key.Matches(msg, m.keys.Confirm):
		// Parse comma-separated args
		argsStr := m.textInput.Value()
		if argsStr != "" {
			m.claudeArgs = strings.Split(argsStr, ",")
			for i := range m.claudeArgs {
				m.claudeArgs[i] = strings.TrimSpace(m.claudeArgs[i])
			}
		}
		m.nextStep()
		return m, nil
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
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

func (m *setupModel) updatePlanPreservationStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Base.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Back):
		m.prevStep()
		return m, nil
	case key.Matches(msg, m.keys.Select), key.Matches(msg, key.NewBinding(key.WithKeys("y", "n"))):
		// Toggle or set based on key
		if msg.String() == "y" {
			m.planPreservationEnabled = true
		} else if msg.String() == "n" {
			m.planPreservationEnabled = false
		} else {
			m.planPreservationEnabled = !m.planPreservationEnabled
		}
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
	// Based on format selection, use the appropriate handler
	if m.configFormat == formatTOML {
		m.executeSetupTOML()
	} else {
		m.executeSetupYAML()
	}

	// These don't depend on config format
	if m.selectedSteps["ecosystem"] {
		m.setupEcosystemFiles()
		if !m.skipFirstProject && m.firstProjectName != "" {
			m.setupFirstProject()
		}
	}
	if m.selectedSteps["tmux"] {
		m.setupTmuxBindings()
	}
	if m.selectedSteps["nvim"] {
		m.setupNeovimPlugin()
	}
}

func (m *setupModel) executeSetupTOML() {
	// Build the complete config as a map
	config := make(map[string]interface{})

	// TUI theme
	if m.selectedSteps["tui"] {
		config["tui"] = map[string]interface{}{
			"theme": m.tuiTheme,
		}
	}

	// Flow settings
	if m.selectedSteps["flow"] {
		config["flow"] = map[string]interface{}{
			"oneshot_model": m.flowOneshotModel,
		}
	}

	// Tmux settings (available keys)
	if m.selectedSteps["tmux"] {
		config["tmux"] = map[string]interface{}{
			"available_keys": []string{"w", "e", "r", "t", "y", "o", "a", "f", "g", "v"},
		}
	}

	// Ecosystem/groves
	if m.selectedSteps["ecosystem"] {
		config["groves"] = map[string]interface{}{
			m.ecosystemName: map[string]interface{}{
				"path":        m.ecosystemPath,
				"enabled":     true,
				"description": "My projects",
				"notebook":    "nb",
			},
		}
	}

	// Notebook
	if m.selectedSteps["notebook"] {
		config["notebooks"] = map[string]interface{}{
			"path": m.notebookPath,
			"rules": map[string]interface{}{
				"default": "personal",
			},
		}
		// Create notebook directory
		m.service.MkdirAll(m.notebookPath, 0755)
	}

	// Agent settings
	if m.selectedSteps["agent"] {
		config["agent"] = map[string]interface{}{
			"providers": map[string]interface{}{
				"claude": map[string]interface{}{
					"args": m.claudeArgs,
				},
			},
		}
	}

	// Gemini settings
	if m.selectedSteps["gemini"] {
		geminiConfig := make(map[string]interface{})
		if m.geminiMethod == geminiMethodCommand {
			geminiConfig["api_key_command"] = m.geminiValue
		} else {
			geminiConfig["api_key"] = m.geminiValue
		}
		config["gemini"] = geminiConfig
	}

	// Hooks / plan preservation
	if m.selectedSteps["hooks"] {
		config["hooks"] = map[string]interface{}{
			"plan_preservation": map[string]interface{}{
				"enabled":      m.planPreservationEnabled,
				"job_type":     "file",
				"title_prefix": "claude-plan",
				"kebab_case":   true,
			},
		}
	}

	// Save the config
	m.tomlHandler.SaveGlobalConfig(config)
}

func (m *setupModel) executeSetupYAML() {
	// Use the existing YAML handler for backwards compatibility
	root, _ := m.yamlHandler.LoadGlobalConfig()

	// TUI theme
	if m.selectedSteps["tui"] {
		setup.SetValue(root, m.tuiTheme, "tui", "theme")
	}

	// Flow settings
	if m.selectedSteps["flow"] {
		setup.SetValue(root, m.flowOneshotModel, "flow", "oneshot_model")
	}

	// Tmux settings
	if m.selectedSteps["tmux"] {
		setup.SetValue(root, []string{"w", "e", "r", "t", "y", "o", "a", "f", "g", "v"}, "tmux", "available_keys")
	}

	// Ecosystem/groves
	if m.selectedSteps["ecosystem"] {
		setup.SetValue(root, map[string]interface{}{
			"path":        m.ecosystemPath,
			"enabled":     true,
			"description": "My projects",
			"notebook":    "nb",
		}, "groves", m.ecosystemName)
	}

	// Notebook
	if m.selectedSteps["notebook"] {
		setup.SetValue(root, m.notebookPath, "notebooks", "path")
		setup.SetValue(root, "personal", "notebooks", "rules", "default")
		// Create notebook directory
		m.service.MkdirAll(m.notebookPath, 0755)
	}

	// Agent settings
	if m.selectedSteps["agent"] {
		setup.SetValue(root, m.claudeArgs, "agent", "providers", "claude", "args")
	}

	// Gemini settings
	if m.selectedSteps["gemini"] {
		if m.geminiMethod == geminiMethodCommand {
			setup.SetValue(root, m.geminiValue, "gemini", "api_key_command")
		} else {
			setup.SetValue(root, m.geminiValue, "gemini", "api_key")
		}
	}

	// Hooks / plan preservation
	if m.selectedSteps["hooks"] {
		setup.SetValue(root, m.planPreservationEnabled, "hooks", "plan_preservation", "enabled")
		if m.planPreservationEnabled {
			setup.SetValue(root, "file", "hooks", "plan_preservation", "job_type")
			setup.SetValue(root, "claude-plan", "hooks", "plan_preservation", "title_prefix")
			setup.SetValue(root, true, "hooks", "plan_preservation", "kebab_case")
		}
	}

	m.yamlHandler.SaveGlobalConfig(root)
}

func (m *setupModel) setupEcosystemFiles() {
	// Create ecosystem directory
	m.service.MkdirAll(m.ecosystemPath, 0755)

	// Create grove.yml or grove.toml for ecosystem based on format selection
	if m.configFormat == formatTOML {
		groveTOMLContent := fmt.Sprintf(`name = "%s"
description = "A Grove ecosystem"
workspaces = ["*"]
`, m.ecosystemName)
		m.service.WriteFile(filepath.Join(m.ecosystemPath, "grove.toml"), []byte(groveTOMLContent), 0644)
	} else {
		groveYMLContent := fmt.Sprintf(`name: %s
description: A Grove ecosystem
workspaces:
  - "*"
`, m.ecosystemName)
		m.service.WriteFile(filepath.Join(m.ecosystemPath, "grove.yml"), []byte(groveYMLContent), 0644)
	}

	// Create .gitignore
	gitignoreContent := `# OS files
.DS_Store
Thumbs.db

# Editor files
*.swp
*.swo
*~
`
	m.service.WriteFile(filepath.Join(m.ecosystemPath, ".gitignore"), []byte(gitignoreContent), 0644)

	// Create README.md
	readmeContent := fmt.Sprintf(`# %s

A Grove ecosystem for managing related projects.

## Getting Started

Add projects to this directory and they will be automatically discovered by Grove tools.
`, m.ecosystemName)
	m.service.WriteFile(filepath.Join(m.ecosystemPath, "README.md"), []byte(readmeContent), 0644)

	// Initialize git repository
	m.service.RunGitInit(m.ecosystemPath)
}

func (m *setupModel) setupFirstProject() {
	projectPath := filepath.Join(m.ecosystemPath, m.firstProjectName)

	// Create project directory
	m.service.MkdirAll(projectPath, 0755)

	// Create grove.yml or grove.toml for the project based on format selection
	if m.configFormat == formatTOML {
		groveTOMLContent := fmt.Sprintf(`name = "%s"
description = "A Grove project"
`, m.firstProjectName)
		m.service.WriteFile(filepath.Join(projectPath, "grove.toml"), []byte(groveTOMLContent), 0644)
	} else {
		groveYMLContent := fmt.Sprintf(`name: %s
description: A Grove project
`, m.firstProjectName)
		m.service.WriteFile(filepath.Join(projectPath, "grove.yml"), []byte(groveYMLContent), 0644)
	}

	// Create README.md
	readmeContent := fmt.Sprintf(`# %s

A Grove project.
`, m.firstProjectName)
	m.service.WriteFile(filepath.Join(projectPath, "README.md"), []byte(readmeContent), 0644)

	// Initialize git repository
	m.service.RunGitInit(projectPath)
}

func (m *setupModel) setupTmuxBindings() {
	popupsContent := `# Grove tmux popup bindings
# Source this file from your tmux.conf: source-file ~/.config/tmux/popups.conf

# --- Popup Settings ---
set -g popup-border-lines none

# --- Grove Flow Plan Status ---
bind-key -n C-p run-shell "PATH=$PATH:$HOME/.local/share/grove/bin flow tmux status"

# --- Gmux Session Switcher ---
bind-key -n C-f display-popup -w 100% -h 98% -x C -y C -E "HOME=$HOME PATH=$PATH:$HOME/.local/share/grove/bin gmux sz"

# --- Context (cx) View ---
bind-key -n M-v display-popup -w 100% -h 98% -x C -y C -E "PATH=$PATH:$HOME/.local/share/grove/bin cx view"

# --- NB (Notes) TUI ---
bind-key -n C-n run-shell "PATH=$PATH:$HOME/.local/share/grove/bin nb tmux tui"

# --- Core Editor ---
bind-key -n C-e run-shell "PATH=$PATH:$HOME/.local/share/grove/bin core tmux editor"
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
	// Abbreviate path for user-friendly config
	nvimPluginDir := filepath.Join(paths.DataDir(), "nvim-plugins", "grove-nvim")
	displayPath := setup.AbbreviatePath(nvimPluginDir)
	// Ensure forward slashes for Lua path compatibility
	displayPath = strings.ReplaceAll(displayPath, "\\", "/")

	nvimPluginContent := fmt.Sprintf(`-- Grove Neovim plugin configuration
-- Add this to your lazy.nvim plugin specs

return {
  {
    dir = "%s",
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
`, displayPath)
	m.service.WriteFile("~/.config/nvim/lua/plugins/grove.lua", []byte(nvimPluginContent), 0644)
}

func (m *setupModel) View() string {
	if !m.ready {
		return "Initializing..."
	}

	var content strings.Builder

	// Page container style
	pageStyle := lipgloss.NewStyle().Padding(1, 2)

	// Get title for header
	var title string
	switch m.step {
	case stepSelectComponents:
		title = theme.DefaultTheme.Success.Render(theme.IconTree) + " Grove Setup Wizard"
	case stepConfigFormat:
		title = theme.DefaultTheme.Info.Render(theme.IconNote) + " Configuration Format"
	case stepTUITheme:
		title = theme.DefaultTheme.Highlight.Render(theme.IconSparkle) + " TUI Theme"
	case stepEcosystem:
		title = theme.DefaultTheme.Success.Render(theme.IconTree) + " Ecosystem Directory"
	case stepFirstProject:
		title = theme.DefaultTheme.Info.Render(theme.IconRepo) + " First Project"
	case stepNotebook:
		title = theme.DefaultTheme.Warning.Render(theme.IconNote) + " Notebook Directory"
	case stepGeminiKey:
		title = theme.DefaultTheme.Highlight.Render(theme.IconSparkle) + " Gemini API Key"
	case stepFlowSettings:
		title = theme.DefaultTheme.Info.Render(theme.IconRunning) + " Flow Settings"
	case stepTmuxBindings:
		title = theme.DefaultTheme.Success.Render(theme.IconShell) + " tmux Popup Bindings"
	case stepAgentSettings:
		title = theme.DefaultTheme.Highlight.Render(theme.IconLightbulb) + " Agent Settings"
	case stepNeovimPlugin:
		title = theme.DefaultTheme.Info.Render(theme.IconNote) + " Neovim Plugin"
	case stepPlanPreservation:
		title = theme.DefaultTheme.Warning.Render(theme.IconPlan) + " Plan Preservation"
	case stepSummary:
		title = theme.DefaultTheme.Success.Render(theme.IconSuccess) + " Setup Complete"
	}

	// Header using theme
	content.WriteString(theme.RenderHeader(title))
	content.WriteString("\n")

	// Main content
	switch m.step {
	case stepSelectComponents:
		content.WriteString(m.viewComponentSelection())
	case stepConfigFormat:
		content.WriteString(m.viewConfigFormatStep())
	case stepTUITheme:
		content.WriteString(m.viewTUIThemeStep())
	case stepEcosystem:
		content.WriteString(m.viewEcosystemStep())
	case stepFirstProject:
		content.WriteString(m.viewFirstProjectStep())
	case stepNotebook:
		content.WriteString(m.viewNotebookStep())
	case stepGeminiKey:
		content.WriteString(m.viewGeminiKeyStep())
	case stepFlowSettings:
		content.WriteString(m.viewFlowSettingsStep())
	case stepTmuxBindings:
		content.WriteString(m.viewTmuxBindingsStep())
	case stepAgentSettings:
		content.WriteString(m.viewAgentSettingsStep())
	case stepNeovimPlugin:
		content.WriteString(m.viewNeovimPluginStep())
	case stepPlanPreservation:
		content.WriteString(m.viewPlanPreservationStep())
	case stepSummary:
		content.WriteString(m.viewSummary())
	}

	// Footer / Status bar
	var statusText string
	if m.service.IsDryRun() {
		statusText = theme.DefaultTheme.Warning.Render("[DRY RUN]") + " "
	}

	switch m.step {
	case stepSelectComponents:
		statusText += "space: toggle • enter: confirm • q: quit"
	case stepSummary:
		statusText += "enter/q: exit"
	default:
		statusText += "enter: confirm • esc: back • q: quit"
	}

	content.WriteString("\n\n")
	content.WriteString(theme.DefaultTheme.Muted.Render(statusText))

	return pageStyle.Render(content.String())
}

func (m *setupModel) viewComponentSelection() string {
	var content strings.Builder
	content.WriteString(theme.DefaultTheme.Muted.Render("Select Components to Configure"))
	content.WriteString("\n\n")
	content.WriteString(m.componentList.View())
	return content.String()
}

func (m *setupModel) viewConfigFormatStep() string {
	var content strings.Builder

	// Explanation
	explanation := `Choose the format for your Grove configuration file.
TOML is recommended for new setups - it's cleaner and easier to read.
YAML is supported for backwards compatibility.`
	content.WriteString(theme.DefaultTheme.Muted.Render(explanation))
	content.WriteString("\n\n")

	// Render format options with theme styling
	formats := []struct {
		id   string
		name string
		desc string
	}{
		{"toml", "TOML", "Modern, clean configuration format (recommended)"},
		{"yaml", "YAML", "Traditional YAML configuration format"},
	}

	for i, f := range formats {
		var cursor, icon string
		if i == m.formatList.Index() {
			cursor = theme.DefaultTheme.Highlight.Render(theme.IconArrowRightBold) + " "
			icon = theme.DefaultTheme.Success.Render(theme.IconStatusCompleted)
		} else {
			cursor = "  "
			icon = theme.DefaultTheme.Muted.Render(theme.IconStatusTodo)
		}

		title := f.name
		if i == m.formatList.Index() {
			title = theme.DefaultTheme.Bold.Render(title)
		}

		content.WriteString(fmt.Sprintf("%s%s %s\n", cursor, icon, title))
		content.WriteString(fmt.Sprintf("     %s\n", theme.DefaultTheme.Muted.Render(f.desc)))
	}

	content.WriteString("\n")

	// Show preview of config format
	content.WriteString(theme.DefaultTheme.Muted.Render("Preview:"))
	content.WriteString("\n")

	var formatPreview string
	if m.formatList.Index() == 0 {
		formatPreview = `[tui]
theme = "terminal"

[groves.projects]
path = "~/Code/projects"
enabled = true`
	} else {
		formatPreview = `tui:
  theme: terminal

groves:
  projects:
    path: ~/Code/projects
    enabled: true`
	}
	content.WriteString(theme.DefaultTheme.Box.Copy().Width(m.width - 12).Render(formatPreview))

	return content.String()
}

func (m *setupModel) viewTUIThemeStep() string {
	var content strings.Builder

	// Explanation
	explanation := `Select a color theme for Grove terminal interfaces.
This affects tools like gmux, nb, and flow TUIs.`
	content.WriteString(theme.DefaultTheme.Muted.Render(explanation))
	content.WriteString("\n\n")

	// Render theme options with theme styling
	themes := []struct {
		id   string
		name string
		desc string
	}{
		{"terminal", "Terminal", "Uses your terminal's default colors"},
		{"gruvbox", "Gruvbox", "Warm, retro color scheme"},
		{"kanagawa", "Kanagawa", "Dark theme inspired by Japanese art"},
	}

	for i, t := range themes {
		var cursor, icon string
		if i == m.themeList.Index() {
			cursor = theme.DefaultTheme.Highlight.Render(theme.IconArrowRightBold) + " "
			icon = theme.DefaultTheme.Success.Render(theme.IconStatusCompleted)
		} else {
			cursor = "  "
			icon = theme.DefaultTheme.Muted.Render(theme.IconStatusTodo)
		}

		title := t.name
		if i == m.themeList.Index() {
			title = theme.DefaultTheme.Bold.Render(title)
		}

		content.WriteString(fmt.Sprintf("%s%s %s\n", cursor, icon, title))
		content.WriteString(fmt.Sprintf("     %s\n", theme.DefaultTheme.Muted.Render(t.desc)))
	}

	return content.String()
}

func (m *setupModel) viewEcosystemStep() string {
	var content strings.Builder

	// Explanation
	explanation := `An ecosystem is a meta-repo where projects can be explored and managed as a group.
It enables coordinated worktree creation, cross-project context, and shared commands
across several possibly related git repositories.`
	content.WriteString(theme.DefaultTheme.Muted.Render(explanation))
	content.WriteString("\n\n")

	// Box using theme
	boxStyle := theme.DefaultTheme.Box.Copy().Width(m.width - 8)

	var boxContent strings.Builder
	if m.currentInput == inputPath {
		boxContent.WriteString(theme.DefaultTheme.Bold.Render("Where should your ecosystem be created?") + "\n\n")
		boxContent.WriteString(m.textInput.View() + "\n\n")
		boxContent.WriteString(theme.DefaultTheme.Muted.Render("The filesystem directory where your projects will live."))
	} else {
		boxContent.WriteString(theme.DefaultTheme.Bold.Render("What should this ecosystem be called?") + "\n\n")
		boxContent.WriteString(m.textInput.View() + "\n\n")
		boxContent.WriteString(theme.DefaultTheme.Muted.Render(fmt.Sprintf("Used in config files to identify this ecosystem. Path: %s", m.ecosystemPath)))
	}

	content.WriteString(boxStyle.Render(boxContent.String()))
	content.WriteString("\n\n")

	// Show gmux preview
	content.WriteString(theme.DefaultTheme.Muted.Render("Preview: Your ecosystem in gmux"))
	content.WriteString("\n")
	content.WriteString(renderGmuxView(m.ecosystemName, "", false, m.width-6))

	return content.String()
}

func (m *setupModel) viewFirstProjectStep() string {
	var content strings.Builder

	// Explanation
	explanation := `You can create your first project inside the ecosystem now.
Leave blank and press Enter to skip this step.`
	content.WriteString(theme.DefaultTheme.Muted.Render(explanation))
	content.WriteString("\n\n")

	// Box using theme
	boxStyle := theme.DefaultTheme.Box.Copy().Width(m.width - 8)

	var boxContent strings.Builder
	boxContent.WriteString(theme.DefaultTheme.Bold.Render("What should your first project be called?") + "\n\n")
	boxContent.WriteString(m.textInput.View() + "\n\n")
	projectPath := filepath.Join(m.ecosystemPath, m.textInput.Value())
	if m.textInput.Value() == "" {
		projectPath = filepath.Join(m.ecosystemPath, "<project-name>")
	}
	boxContent.WriteString(theme.DefaultTheme.Muted.Render(fmt.Sprintf("Will be created at: %s", projectPath)))

	content.WriteString(boxStyle.Render(boxContent.String()))
	content.WriteString("\n\n")

	// Show gmux preview with the new project
	projectName := m.textInput.Value()
	if projectName != "" {
		content.WriteString(theme.DefaultTheme.Muted.Render("Preview: Your new project in the ecosystem"))
		content.WriteString("\n")
		content.WriteString(renderGmuxView(m.ecosystemName, projectName, true, m.width-6))
	}

	return content.String()
}

func (m *setupModel) viewNotebookStep() string {
	var content strings.Builder

	// Explanation
	explanation := `Notebooks store your development notes, plans, and AI chat histories.
Each workspace gets its own section, keeping project context organized.`
	content.WriteString(theme.DefaultTheme.Muted.Render(explanation))
	content.WriteString("\n\n")

	// Box using theme
	boxStyle := theme.DefaultTheme.Box.Copy().Width(m.width - 8)

	var boxContent strings.Builder
	boxContent.WriteString(theme.DefaultTheme.Bold.Render("Enter the path for your notebook directory:") + "\n\n")
	boxContent.WriteString(m.textInput.View() + "\n\n")
	boxContent.WriteString(theme.DefaultTheme.Muted.Render("This directory will store your notes and plans."))

	content.WriteString(boxStyle.Render(boxContent.String()))
	content.WriteString("\n\n")

	// Show nb preview and note creation example
	content.WriteString(theme.DefaultTheme.Muted.Render("Preview: The nb notes interface"))
	content.WriteString("\n")
	content.WriteString(renderNbView(m.ecosystemName, m.width-6))
	content.WriteString("\n\n")
	content.WriteString(renderNoteCreationExample(m.textInput.Value(), m.width-6))

	return content.String()
}

func (m *setupModel) viewGeminiKeyStep() string {
	var content strings.Builder

	// Explanation
	explanation := `A Gemini API key enables AI-powered features in Grove:
  - grove flow: Run LLM jobs for code analysis and generation
  - grove llm request: Direct LLM queries from the command line
  - AI-assisted changelog generation during releases

This step is optional. You can skip it and configure the key later.`
	content.WriteString(theme.DefaultTheme.Muted.Render(explanation))
	content.WriteString("\n\n")

	// Box using theme
	boxStyle := theme.DefaultTheme.Box.Copy().Width(m.width - 8)

	var boxContent strings.Builder
	if m.currentInput == inputMethod {
		boxContent.WriteString(theme.DefaultTheme.Bold.Render("How would you like to provide your Gemini API key?") + "\n\n")
		boxContent.WriteString(m.methodList.View())
	} else {
		if m.geminiMethod == geminiMethodCommand {
			boxContent.WriteString(theme.DefaultTheme.Bold.Render("Enter the command to retrieve your API key:") + "\n\n")
			boxContent.WriteString(m.textInput.View() + "\n\n")
			boxContent.WriteString(theme.DefaultTheme.Muted.Render("Example: op read 'op://Private/Gemini API Key/credential' --no-newline"))
		} else {
			boxContent.WriteString(theme.DefaultTheme.Bold.Render("Enter your Gemini API key:") + "\n\n")
			boxContent.WriteString(m.textInput.View() + "\n\n")
			boxContent.WriteString(theme.DefaultTheme.Muted.Render("The key will be stored in your global grove config."))
		}
	}

	content.WriteString(boxStyle.Render(boxContent.String()))
	return content.String()
}

func (m *setupModel) viewFlowSettingsStep() string {
	var content strings.Builder

	// Explanation
	explanation := `Configure the default model for grove-flow LLM jobs.
This model will be used for oneshot and chat API submissions.`
	content.WriteString(theme.DefaultTheme.Muted.Render(explanation))
	content.WriteString("\n\n")

	// Label with icon
	content.WriteString(theme.DefaultTheme.Highlight.Render(theme.IconArrowRightBold) + " ")
	content.WriteString(theme.DefaultTheme.Bold.Render("Default LLM Model"))
	content.WriteString("\n\n")

	// Text input
	content.WriteString("  " + m.textInput.View())
	content.WriteString("\n\n")

	// Common models with theme styling
	content.WriteString(theme.DefaultTheme.Muted.Render("  Common models:"))
	content.WriteString("\n")
	models := []string{"gemini-3-pro-preview", "claude-3-opus", "gpt-4o"}
	for _, model := range models {
		content.WriteString(fmt.Sprintf("    %s %s\n",
			theme.DefaultTheme.Muted.Render(theme.IconBullet),
			theme.DefaultTheme.Info.Render(model)))
	}

	return content.String()
}

func (m *setupModel) viewTmuxBindingsStep() string {
	var content strings.Builder

	// Explanation
	explanation := `This will add Grove popup bindings to your tmux config directory.
These bindings give you quick keyboard access to Grove tools from any tmux session.`
	content.WriteString(theme.DefaultTheme.Muted.Render(explanation))
	content.WriteString("\n\n")

	// Box using theme
	boxStyle := theme.DefaultTheme.Box.Copy().Width(m.width - 8)

	var boxContent strings.Builder
	boxContent.WriteString(theme.DefaultTheme.Bold.Render("Files to be created:") + "\n\n")
	boxContent.WriteString(theme.DefaultTheme.Muted.Render("  ~/.config/tmux/popups.conf\n"))
	boxContent.WriteString(theme.DefaultTheme.Muted.Render("  (source-file line appended to tmux.conf)\n"))
	boxContent.WriteString("\n")
	boxContent.WriteString(theme.DefaultTheme.Info.Render("Press Enter to continue or esc to go back."))

	content.WriteString(boxStyle.Render(boxContent.String()))
	content.WriteString("\n\n")

	// Show the keybindings that will be added
	content.WriteString(renderTmuxConfig(m.width - 6))

	return content.String()
}

func (m *setupModel) viewAgentSettingsStep() string {
	var content strings.Builder

	// Explanation
	explanation := `Configure default arguments for the Claude agent.
These arguments will be passed when spawning Claude Code sessions.`
	content.WriteString(theme.DefaultTheme.Muted.Render(explanation))
	content.WriteString("\n\n")

	// Label with icon
	content.WriteString(theme.DefaultTheme.Highlight.Render(theme.IconArrowRightBold) + " ")
	content.WriteString(theme.DefaultTheme.Bold.Render("Claude Agent Arguments"))
	content.WriteString("\n\n")

	// Text input
	content.WriteString("  " + m.textInput.View())
	content.WriteString("\n\n")

	// Common args with theme styling
	content.WriteString(theme.DefaultTheme.Muted.Render("  Common arguments:"))
	content.WriteString("\n")
	args := []struct {
		arg  string
		desc string
	}{
		{"--dangerously-skip-permissions", "Skip permission prompts"},
		{"--chrome", "Enable Chrome browser automation"},
		{"--verbose", "Verbose output logging"},
	}
	for _, a := range args {
		content.WriteString(fmt.Sprintf("    %s %s %s\n",
			theme.DefaultTheme.Muted.Render(theme.IconBullet),
			theme.DefaultTheme.Info.Render(a.arg),
			theme.DefaultTheme.Muted.Render("- "+a.desc)))
	}

	return content.String()
}

func (m *setupModel) viewNeovimPluginStep() string {
	var content strings.Builder

	// Explanation
	explanation := `This will add a Grove plugin config to your Neovim config directory.
You can require or incorporate it into your plugin setup as you see fit.`
	content.WriteString(theme.DefaultTheme.Muted.Render(explanation))
	content.WriteString("\n\n")

	// Box using theme
	boxStyle := theme.DefaultTheme.Box.Copy().Width(m.width - 8)

	var boxContent strings.Builder
	boxContent.WriteString(theme.DefaultTheme.Bold.Render("File to be created:") + "\n\n")
	boxContent.WriteString(theme.DefaultTheme.Muted.Render("  ~/.config/nvim/lua/plugins/grove.lua\n"))
	boxContent.WriteString("\n")
	boxContent.WriteString(theme.DefaultTheme.Info.Render("Press Enter to continue or esc to go back."))

	content.WriteString(boxStyle.Render(boxContent.String()))
	return content.String()
}

func (m *setupModel) viewPlanPreservationStep() string {
	var content strings.Builder

	// Explanation
	explanation := `Plan Preservation automatically saves Claude Code plans to your active grove-flow plan.

When Claude exits plan mode, the plan content will be saved as a new job file
in your current flow plan directory with a kebab-case title.`
	content.WriteString(theme.DefaultTheme.Muted.Render(explanation))
	content.WriteString("\n\n")

	// Box using theme
	boxStyle := theme.DefaultTheme.Box.Copy().Width(m.width - 8)

	var boxContent strings.Builder
	boxContent.WriteString(theme.DefaultTheme.Bold.Render("Enable plan preservation?") + "\n\n")

	// Selection styling with fat arrow indicator
	var yesLabel, noLabel string
	if m.planPreservationEnabled {
		yesLabel = theme.DefaultTheme.Highlight.Render(theme.IconArrowRightBold) + " " + theme.DefaultTheme.Success.Render("Yes")
		noLabel = "  " + theme.DefaultTheme.Muted.Render("No")
	} else {
		yesLabel = "  " + theme.DefaultTheme.Muted.Render("Yes")
		noLabel = theme.DefaultTheme.Highlight.Render(theme.IconArrowRightBold) + " " + theme.DefaultTheme.Success.Render("No")
	}

	boxContent.WriteString("  " + yesLabel + "    " + noLabel + "\n\n")
	boxContent.WriteString(theme.DefaultTheme.Muted.Render("Press space/y/n to select, Enter to confirm, esc to go back."))

	content.WriteString(boxStyle.Render(boxContent.String()))
	return content.String()
}

func (m *setupModel) viewSummary() string {
	var content strings.Builder

	// Title based on dry run status
	var title string
	if m.service.IsDryRun() {
		title = theme.DefaultTheme.Warning.Render("DRY RUN - No changes were made")
	} else {
		title = theme.DefaultTheme.Success.Render("Setup completed successfully!")
	}
	content.WriteString(title + "\n\n")

	if m.service.IsDryRun() {
		content.WriteString("The following actions would be performed:\n\n")
	} else {
		content.WriteString("Actions performed:\n\n")
	}

	// List actions with theme icons
	for _, action := range m.service.Actions() {
		if action.Success {
			content.WriteString(theme.DefaultTheme.Success.Render(theme.IconSuccess) + " " + action.Description + "\n")
		} else {
			content.WriteString(theme.DefaultTheme.Error.Render(theme.IconError) + " " + action.Description)
			if action.Error != nil {
				content.WriteString(theme.DefaultTheme.Muted.Render(fmt.Sprintf(" (%s)", action.Error.Error())))
			}
			content.WriteString("\n")
		}
	}

	content.WriteString("\n")
	content.WriteString(theme.DefaultTheme.Bold.Render("Next Steps:") + "\n")
	content.WriteString(theme.DefaultTheme.Muted.Render("  1. Restart your terminal or source your shell config") + "\n")
	content.WriteString(theme.DefaultTheme.Muted.Render("  2. Run 'grove list' to see available tools") + "\n")
	content.WriteString(theme.DefaultTheme.Muted.Render("  3. Start building with 'grove build' in your ecosystem") + "\n")

	// Wrap in box with success border
	boxStyle := theme.DefaultTheme.Box.Copy().
		Width(m.width - 8).
		BorderForeground(theme.DefaultTheme.Colors.Green)

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

	// Add subcommands
	cmd.AddCommand(newStarshipCmd())
	cmd.AddCommand(newGitHooksCmd())

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
description: A Grove ecosystem
workspaces:
  - "*"
`, ecosystemName)
		service.WriteFile(filepath.Join(ecosystemPath, "grove.yml"), []byte(groveYMLContent), 0644)

		gitignoreContent := `# OS files
.DS_Store
Thumbs.db

# Editor files
*.swp
*.swo
*~
`
		service.WriteFile(filepath.Join(ecosystemPath, ".gitignore"), []byte(gitignoreContent), 0644)

		readmeContent := fmt.Sprintf(`# %s

A Grove ecosystem for managing related projects.

## Getting Started

Add projects to this directory and they will be automatically discovered by Grove tools.
`, ecosystemName)
		service.WriteFile(filepath.Join(ecosystemPath, "README.md"), []byte(readmeContent), 0644)

		service.RunGitInit(ecosystemPath)

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
# Source this file from your tmux.conf: source-file ~/.config/tmux/popups.conf

# --- Popup Settings ---
set -g popup-border-lines none

# --- Grove Flow Plan Status ---
bind-key -n C-p run-shell "PATH=$PATH:$HOME/.local/share/grove/bin flow tmux status"

# --- Gmux Session Switcher ---
bind-key -n C-f display-popup -w 100% -h 98% -x C -y C -E "HOME=$HOME PATH=$PATH:$HOME/.local/share/grove/bin gmux sz"

# --- Context (cx) View ---
bind-key -n M-v display-popup -w 100% -h 98% -x C -y C -E "PATH=$PATH:$HOME/.local/share/grove/bin cx view"

# --- NB (Notes) TUI ---
bind-key -n C-n run-shell "PATH=$PATH:$HOME/.local/share/grove/bin nb tmux tui"

# --- Core Editor ---
bind-key -n C-e run-shell "PATH=$PATH:$HOME/.local/share/grove/bin core tmux editor"
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
			pretty.InfoPretty(fmt.Sprintf("  * %s", action.Description))
		} else {
			pretty.InfoPretty(fmt.Sprintf("  x %s: %v", action.Description, action.Error))
		}
	}

	return nil
}
