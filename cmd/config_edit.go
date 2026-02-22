package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/tui/components/help"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/configui"
	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/setup"
	"github.com/spf13/cobra"
)

// Type alias for the extracted keymap
type configKeyMap = grovekeymap.ConfigKeyMap

// configUIState holds persisted UI preferences for the config editor.
type configUIState struct {
	ShowPreview    bool                   `json:"show_preview"`
	ViewMode       configui.ViewMode      `json:"view_mode"`
	MaturityFilter configui.MaturityFilter `json:"maturity_filter"`
	SortMode       configui.SortMode      `json:"sort_mode"`
}

// configUIStateFile returns the path to the config UI state file.
func configUIStateFile() string {
	return filepath.Join(paths.StateDir(), "config-ui.json")
}

// loadConfigUIState loads the config UI state from disk.
func loadConfigUIState() configUIState {
	state := configUIState{
		ShowPreview:    true,                         // Default to showing preview
		ViewMode:       configui.ViewAll,             // Default to showing all fields
		MaturityFilter: configui.MaturityStable,      // Default to stable fields only
		SortMode:       configui.SortConfiguredFirst, // Default to configured-first sort
	}

	data, err := os.ReadFile(configUIStateFile())
	if err != nil {
		return state
	}

	_ = json.Unmarshal(data, &state)
	return state
}

// saveConfigUIState saves the config UI state to disk.
func saveConfigUIState(state configUIState) {
	data, err := json.Marshal(state)
	if err != nil {
		return
	}

	stateDir := paths.StateDir()
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return
	}

	_ = os.WriteFile(configUIStateFile(), data, 0644)
}

// Package-level state for delegate access
var (
	configShowPreview    = true
	configViewMode       = configui.ViewAll
	configMaturityFilter = configui.MaturityStable
	configSortMode       = configui.SortConfiguredFirst
)

func init() {
	rootCmd.AddCommand(newConfigCmd())
}

func newConfigCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("config", "Interactive configuration editor")
	cmd.Long = `Edit Grove configuration values interactively.

This command opens a TUI to edit the active configuration file.
It prioritizes the project-level config if present, otherwise
defaults to the global configuration.

Supports both YAML (with comment preservation) and TOML formats.`

	cmd.RunE = runConfigEdit
	return cmd
}

// --- Schema Definition ---
// Field types and schema are now defined in pkg/configui and generated from JSON schemas.

// --- Model & State ---

// configKeys is the singleton instance of the config editor TUI keymap.
var configKeys = grovekeymap.NewConfigKeyMap()

// isOverrideSource returns true if the source is an override file (not the main config).
func isOverrideSource(source config.ConfigSource) bool {
	switch source {
	case config.SourceGlobalOverride, config.SourceEnvOverlay, config.SourceOverride:
		return true
	}
	return false
}

// renderLayerBadge renders a colored badge for the config source.
// Override sources show an asterisk (e.g., [Global*]) to indicate they're from an override file.
func renderLayerBadge(source config.ConfigSource) string {
	var style lipgloss.Style
	var label string

	switch source {
	case config.SourceGlobal:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Blue)
		label = "[Global]"
	case config.SourceGlobalOverride:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Blue)
		label = "[Global*]"
	case config.SourceEnvOverlay:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Yellow)
		label = "[Env*]"
	case config.SourceEcosystem:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Violet)
		label = "[Ecosystem]"
	case config.SourceProject:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Green)
		label = "[Project]"
	case config.SourceOverride:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Green)
		label = "[Local*]"
	case config.SourceDefault:
		style = theme.DefaultTheme.Muted
		label = "[Default]"
	default:
		style = theme.DefaultTheme.Muted
		label = "[Default]"
	}

	return style.Render(label)
}

// layerDisplayName returns a human-readable name for a config source.
func layerDisplayName(source config.ConfigSource) string {
	switch source {
	case config.SourceGlobal:
		return "Global"
	case config.SourceGlobalOverride:
		return "Global*"
	case config.SourceEnvOverlay:
		return "Env*"
	case config.SourceEcosystem:
		return "Ecosystem"
	case config.SourceProject:
		return "Project"
	case config.SourceOverride:
		return "Local*"
	case config.SourceDefault:
		return "Default"
	default:
		return "Unknown"
	}
}

// layerRecommendation returns guidance text for the layer.
func layerRecommendation(source config.ConfigSource) string {
	switch source {
	case config.SourceGlobal:
		return "Recommended for personal preferences"
	case config.SourceEcosystem:
		return "Use for shared settings across projects"
	case config.SourceProject:
		return "Use for project-specific overrides"
	default:
		return ""
	}
}

// configFormat tracks which format the config file is in
type configFormatType int

const (
	formatYAMLConfig configFormatType = iota
	formatTOMLConfig
)

// viewState tracks which view is active
type viewState int

const (
	viewList viewState = iota
	viewEdit
	viewInfo
	viewSources // Shows all config source files
)

type configModel struct {
	// Paging model
	pages      []*LayerPage
	activePage int

	input textinput.Model

	// Layered config state
	layered *config.LayeredConfig

	// Per-layer file handlers (for saving to specific layers)
	yamlHandler *setup.YAMLHandler
	tomlHandler *setup.TOMLHandler

	// View state
	state       viewState
	editNode    *configui.ConfigNode // Node being edited (replaces editIndex)
	selectIndex int                  // Index in options list if typeSelect
	targetLayer config.ConfigSource  // Which layer to save to during edit
	boolValue   bool                 // Current bool value for typeBool editing

	statusMsg string
	keys      configKeyMap
	help      help.Model
	width     int
	height    int
	err       error
}

func runConfigEdit(cmd *cobra.Command, args []string) error {
	cwd, _ := os.Getwd()

	// Load UI state (preview preference, view mode, maturity filter, sort mode)
	uiState := loadConfigUIState()
	configShowPreview = uiState.ShowPreview
	configViewMode = uiState.ViewMode
	configMaturityFilter = uiState.MaturityFilter
	configSortMode = uiState.SortMode

	// Load layered configuration
	layered, err := config.LoadLayered(cwd)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w\nRun 'grove setup' to create one", err)
	}

	// Initialize Setup Service and handlers
	svc := setup.NewService(false) // Not dry-run

	// Initialize pages for each config layer
	width, height := 80, 24 // Initial dummy size, will be updated on WindowSizeMsg
	pages := []*LayerPage{
		NewLayerPage("Global", config.SourceGlobal, layered, width, height),
		NewLayerPage("Ecosystem", config.SourceEcosystem, layered, width, height),
		NewLayerPage("Project", config.SourceProject, layered, width, height),
	}

	// Initialize Model
	m := configModel{
		pages:       pages,
		activePage:  0,
		layered:     layered,
		input:       textinput.New(),
		keys:        configKeys,
		help:        help.NewBuilder().WithKeys(configKeys).WithTitle("Configuration Editor").Build(),
		state:       viewList,
		yamlHandler: setup.NewYAMLHandler(svc),
		tomlHandler: setup.NewTOMLHandler(svc),
	}

	m.input.Prompt = "  > "
	m.input.CharLimit = 200
	m.input.Width = 50

	// Focus the first page
	m.pages[0].Focus()

	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

// nextPage switches to the next page (cycles)
func (m *configModel) nextPage() {
	m.pages[m.activePage].Blur()
	m.activePage = (m.activePage + 1) % len(m.pages)
	m.pages[m.activePage].Focus()
}

// prevPage switches to the previous page (cycles)
func (m *configModel) prevPage() {
	m.pages[m.activePage].Blur()
	m.activePage--
	if m.activePage < 0 {
		m.activePage = len(m.pages) - 1
	}
	m.pages[m.activePage].Focus()
}

// setTomlValue sets a value in a nested TOML map using a path
func setTomlValue(data map[string]interface{}, value string, path ...string) {
	if len(path) == 0 {
		return
	}

	current := data
	for i, key := range path {
		if i == len(path)-1 {
			// Last key - set the value
			current[key] = value
			return
		}

		// Navigate deeper, creating maps as needed
		val, ok := current[key]
		if !ok {
			newMap := make(map[string]interface{})
			current[key] = newMap
			current = newMap
		} else {
			nested, ok := val.(map[string]interface{})
			if !ok {
				newMap := make(map[string]interface{})
				current[key] = newMap
				current = newMap
			} else {
				current = nested
			}
		}
	}
}

func (m configModel) Init() tea.Cmd {
	return nil
}

func (m configModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Reserve space for tabs (2 lines) and footer (4 lines)
		pageHeight := msg.Height - 6
		for _, p := range m.pages {
			p.SetSize(msg.Width, pageHeight)
		}
		m.help.Width = msg.Width
		m.help.Height = msg.Height

	// Handle custom messages from pages
	case editNodeMsg:
		m.startEditFromNode(msg.node)
		return m, nil

	case infoNodeMsg:
		m.state = viewInfo
		m.editNode = msg.node
		return m, nil

	case tea.KeyMsg:
		// If help is showing, pass messages to help component
		if m.help.ShowAll {
			m.help, cmd = m.help.Update(msg)
			return m, cmd
		}

		switch m.state {
		case viewEdit:
			return m.updateEdit(msg)
		case viewInfo:
			return m.updateInfo(msg)
		case viewSources:
			return m.updateSources(msg)
		default:
			return m.updateList(msg)
		}
	}

	// Delegate non-key messages to active page when in list view
	if m.state == viewList {
		activePage, pageCmd := m.pages[m.activePage].Update(msg)
		m.pages[m.activePage] = activePage
		cmd = pageCmd
	}

	return m, cmd
}

func (m configModel) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit

	case "?":
		m.help.Toggle()
		return m, nil

	// Tab switching
	case "tab":
		m.nextPage()
		return m, nil

	case "shift+tab":
		m.prevPage()
		return m, nil

	// Direct page jumps
	case "1":
		if m.activePage != 0 {
			m.pages[m.activePage].Blur()
			m.activePage = 0
			m.pages[m.activePage].Focus()
		}
		return m, nil

	case "2":
		if m.activePage != 1 {
			m.pages[m.activePage].Blur()
			m.activePage = 1
			m.pages[m.activePage].Focus()
		}
		return m, nil

	case "3":
		if m.activePage != 2 {
			m.pages[m.activePage].Blur()
			m.activePage = 2
			m.pages[m.activePage].Focus()
		}
		return m, nil

	case "p":
		// Toggle preview mode and persist
		configShowPreview = !configShowPreview
		m.saveUIState()
		return m, nil

	case "v":
		// Toggle view mode (configured/all) and persist
		configViewMode = configui.CycleViewMode(configViewMode)
		m.saveUIState()
		m.refreshAllPages()
		return m, nil

	case "m":
		// Cycle maturity filter forward and persist
		configMaturityFilter = configui.CycleMaturityFilter(configMaturityFilter)
		m.saveUIState()
		m.refreshAllPages()
		return m, nil

	case "M":
		// Cycle maturity filter backward and persist
		configMaturityFilter = configui.CycleMaturityFilterReverse(configMaturityFilter)
		m.saveUIState()
		m.refreshAllPages()
		return m, nil

	case "s":
		// Cycle sort mode forward and persist
		configSortMode = configui.CycleSortMode(configSortMode)
		m.saveUIState()
		m.refreshAllPages()
		return m, nil

	case "S":
		// Cycle sort mode backward and persist
		configSortMode = configui.CycleSortModeReverse(configSortMode)
		m.saveUIState()
		m.refreshAllPages()
		return m, nil

	case "c":
		// Don't intercept 'c' if a z-chord is pending (zc = close fold)
		if m.pages[m.activePage].IsZChordPending() {
			break // Let it fall through to page delegation
		}
		m.state = viewSources
		return m, nil
	}

	// Delegate to active page
	activePage, cmd := m.pages[m.activePage].Update(msg)
	m.pages[m.activePage] = activePage
	return m, cmd
}

func (m configModel) updateInfo(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.state = viewList
		return m, nil
	case "enter":
		// Start editing from info view
		if m.editNode != nil {
			m.startEditFromNode(m.editNode)
		}
		return m, nil
	}
	return m, nil
}

func (m configModel) updateSources(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "c":
		m.state = viewList
		return m, nil
	}
	return m, nil
}

// startEditFromNode starts editing a node (used by pages)
func (m *configModel) startEditFromNode(node *configui.ConfigNode) {
	// Don't allow editing container types directly
	if node.IsContainer() {
		m.statusMsg = "Expand node to edit children"
		return
	}

	m.state = viewEdit
	m.editNode = node
	m.statusMsg = ""

	// Start with the recommended layer, but use active source if it's already set there
	m.targetLayer = node.Field.Layer
	if m.targetLayer == config.SourceDefault {
		m.targetLayer = config.SourceGlobal // Fallback to global if no layer specified
	}

	// If the value is already set in a specific layer, default to that layer
	if node.ActiveSource != config.SourceDefault {
		m.targetLayer = node.ActiveSource
	}

	// Get the current value as string for the input
	currentValue := configui.FormatValue(node.Value)
	if currentValue == "(unset)" || currentValue == "(empty)" {
		currentValue = ""
	}

	switch node.Field.Type {
	case configui.FieldString:
		m.input.SetValue(currentValue)
		m.input.Focus()
	case configui.FieldSelect:
		// Find current option index
		m.selectIndex = 0
		for i, opt := range node.Field.Options {
			if opt == currentValue {
				m.selectIndex = i
				break
			}
		}
	case configui.FieldBool:
		m.boolValue = currentValue == "true"
	case configui.FieldInt:
		m.input.SetValue(currentValue)
		m.input.Focus()
	case configui.FieldArray:
		// For arrays, use the text input with comma-separated values
		m.input.SetValue(currentValue)
		m.input.Focus()
	default:
		// Fallback to text input
		m.input.SetValue(currentValue)
		m.input.Focus()
	}
}

func (m *configModel) updateEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.editNode == nil {
		m.state = viewList
		return m, nil
	}
	node := m.editNode

	// Get the full path with namespace
	path := node.Field.Path
	if node.Field.Namespace != "" {
		path = append([]string{node.Field.Namespace}, node.Field.Path...)
	}

	// For dynamic map entries, we need to build the full path including parent keys
	if node.IsDynamic && node.Parent != nil {
		path = buildFullPath(node)
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.state = viewList
		m.input.Blur()
		return m, nil

	case tea.KeyTab:
		// Cycle through available layers
		m.targetLayer = m.cycleLayer(m.targetLayer)
		return m, nil

	case tea.KeyEnter:
		var newValue string
		switch node.Field.Type {
		case configui.FieldString, configui.FieldArray, configui.FieldInt:
			newValue = m.input.Value()
		case configui.FieldSelect:
			newValue = node.Field.Options[m.selectIndex]
		case configui.FieldBool:
			if m.boolValue {
				newValue = "true"
			} else {
				newValue = "false"
			}
		default:
			newValue = m.input.Value()
		}

		// Save to the target layer
		if err := m.saveToLayer(path, newValue, m.targetLayer); err != nil {
			m.statusMsg = fmt.Sprintf("Error: %v", err)
			return m, nil
		}

		// Reload layered config to reflect changes
		if err := m.reloadConfig(); err != nil {
			m.statusMsg = fmt.Sprintf("Error reloading: %v", err)
			return m, nil
		}

		m.state = viewList
		m.input.Blur()
		m.statusMsg = fmt.Sprintf("Saved to %s!", layerDisplayName(m.targetLayer))
		return m, nil
	}

	// Handle navigation for Select type
	if node.Field.Type == configui.FieldSelect {
		switch msg.String() {
		case "up", "k":
			if m.selectIndex > 0 {
				m.selectIndex--
			}
		case "down", "j":
			if m.selectIndex < len(node.Field.Options)-1 {
				m.selectIndex++
			}
		}
		return m, nil
	}

	// Handle navigation for Bool type
	if node.Field.Type == configui.FieldBool {
		switch msg.String() {
		case "up", "k", "down", "j", " ":
			m.boolValue = !m.boolValue
		}
		return m, nil
	}

	// Handle input for String/Array/Int types
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// buildFullPath constructs the full config path for a node, including parent keys.
func buildFullPath(node *configui.ConfigNode) []string {
	var path []string

	// Walk up the tree to collect path components
	current := node
	for current != nil {
		if current.Key != "" {
			path = append([]string{current.Key}, path...)
		} else if len(current.Field.Path) > 0 {
			path = append(current.Field.Path, path...)
		}
		current = current.Parent
	}

	// Add namespace if present
	if node.Field.Namespace != "" {
		path = append([]string{node.Field.Namespace}, path...)
	}

	return path
}

// cycleLayer cycles through available layers: Global -> Ecosystem -> Project -> Global
func (m *configModel) cycleLayer(current config.ConfigSource) config.ConfigSource {
	layers := m.availableLayers()
	if len(layers) == 0 {
		return config.SourceGlobal
	}

	// Find current index
	currentIdx := 0
	for i, layer := range layers {
		if layer == current {
			currentIdx = i
			break
		}
	}

	// Cycle to next
	nextIdx := (currentIdx + 1) % len(layers)
	return layers[nextIdx]
}

// availableLayers returns the layers available for saving.
func (m *configModel) availableLayers() []config.ConfigSource {
	layers := []config.ConfigSource{config.SourceGlobal} // Global always available

	// Ecosystem available if we have an ecosystem config path
	if m.layered.FilePaths[config.SourceEcosystem] != "" {
		layers = append(layers, config.SourceEcosystem)
	}

	// Project available if we have a project config path (or can create one)
	if m.layered.FilePaths[config.SourceProject] != "" {
		layers = append(layers, config.SourceProject)
	}

	return layers
}

// saveToLayer saves a value to the specified layer's config file.
func (m *configModel) saveToLayer(path []string, value string, layer config.ConfigSource) error {
	// Determine target file path
	var targetPath string
	switch layer {
	case config.SourceGlobal:
		targetPath = m.layered.FilePaths[config.SourceGlobal]
		if targetPath == "" {
			targetPath = setup.GlobalTOMLConfigPath()
		}
	case config.SourceEcosystem:
		targetPath = m.layered.FilePaths[config.SourceEcosystem]
		if targetPath == "" {
			return fmt.Errorf("no ecosystem config file found")
		}
	case config.SourceProject:
		targetPath = m.layered.FilePaths[config.SourceProject]
		if targetPath == "" {
			return fmt.Errorf("no project config file found")
		}
	default:
		return fmt.Errorf("cannot save to layer: %s", layer)
	}

	// Detect format and save
	ext := strings.ToLower(filepath.Ext(targetPath))
	if ext == ".toml" {
		return m.saveToTOML(targetPath, path, value)
	}
	return m.saveToYAML(targetPath, path, value)
}

// saveToTOML saves a value to a TOML config file.
func (m *configModel) saveToTOML(filePath string, path []string, value string) error {
	data, err := m.tomlHandler.LoadTOML(filePath)
	if err != nil {
		return err
	}

	setTomlValue(data, value, path...)
	return m.tomlHandler.SaveTOML(filePath, data)
}

// saveToYAML saves a value to a YAML config file.
func (m *configModel) saveToYAML(filePath string, path []string, value string) error {
	root, err := m.yamlHandler.LoadYAML(filePath)
	if err != nil {
		return err
	}

	if err := setup.SetValue(root, value, path...); err != nil {
		return err
	}

	return m.yamlHandler.SaveYAML(filePath, root)
}

// saveUIState saves the current UI state to disk.
func (m *configModel) saveUIState() {
	saveConfigUIState(configUIState{
		ShowPreview:    configShowPreview,
		ViewMode:       configViewMode,
		MaturityFilter: configMaturityFilter,
		SortMode:       configSortMode,
	})
}

// refreshAllPages refreshes all pages to apply new filters/sorting.
func (m *configModel) refreshAllPages() {
	for _, page := range m.pages {
		page.Refresh(m.layered)
	}
}

// reloadConfig reloads the layered config and refreshes all pages.
func (m *configModel) reloadConfig() error {
	cwd, _ := os.Getwd()
	layered, err := config.LoadLayered(cwd)
	if err != nil {
		return err
	}
	m.layered = layered

	// Refresh all pages with updated config
	for _, page := range m.pages {
		page.Refresh(layered)
	}

	return nil
}

func (m configModel) View() string {
	// Show help overlay if active
	if m.help.ShowAll {
		return m.help.View()
	}

	switch m.state {
	case viewEdit:
		return m.renderEditView()
	case viewInfo:
		return m.renderInfoView()
	case viewSources:
		return m.renderSourcesView()
	default:
		return m.renderListView()
	}
}

func (m configModel) renderListView() string {
	var b strings.Builder

	// Header with icon (matching setup wizard style)
	title := theme.DefaultTheme.Highlight.Render(theme.IconGear) + " Configuration Editor"
	b.WriteString(theme.RenderHeader(title))
	b.WriteString("\n")

	// Tab bar
	b.WriteString(renderConfigTabs(m.pages, m.activePage))
	b.WriteString("\n\n")

	// Active page content
	b.WriteString(m.pages[m.activePage].View())
	b.WriteString("\n")

	// Status message
	if m.statusMsg != "" {
		if strings.HasPrefix(m.statusMsg, "Error") {
			b.WriteString(theme.DefaultTheme.Error.Render(m.statusMsg))
		} else {
			b.WriteString(theme.DefaultTheme.Success.Render(m.statusMsg))
		}
		b.WriteString("\n")
	}

	// Help
	b.WriteString(m.help.View())

	// Add margin around the content (no top padding, 1 bottom, 2 sides)
	return lipgloss.NewStyle().PaddingLeft(2).PaddingRight(2).PaddingBottom(1).Render(b.String())
}

func (m configModel) renderEditView() string {
	if m.editNode == nil {
		return "No node selected for editing"
	}
	node := m.editNode

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Colors.Orange).
		Padding(1, 2).
		Width(65)

	title := theme.DefaultTheme.Bold.Render("Edit: " + node.DisplayKey())

	// Current value info
	currentValue := configui.FormatValue(node.Value)
	currentInfo := theme.DefaultTheme.Muted.Render(
		fmt.Sprintf("Current: %s (from %s)", currentValue, layerDisplayName(node.ActiveSource)),
	)

	// Show hint for sensitive fields
	hintText := ""
	if node.Field.Hint != "" {
		hintText = theme.DefaultTheme.Muted.Render("Hint: " + node.Field.Hint)
	}

	// Edit content based on field type
	var content string
	switch node.Field.Type {
	case configui.FieldString, configui.FieldArray, configui.FieldInt:
		content = m.input.View()
	case configui.FieldSelect:
		var opts []string
		for i, opt := range node.Field.Options {
			cursor := "  "
			style := theme.DefaultTheme.Normal
			if i == m.selectIndex {
				cursor = theme.DefaultTheme.Highlight.Render(theme.IconArrowRightBold) + " "
				style = theme.DefaultTheme.Highlight
			}
			opts = append(opts, style.Render(cursor+opt))
		}
		content = strings.Join(opts, "\n")
	case configui.FieldBool:
		trueStyle := theme.DefaultTheme.Normal
		falseStyle := theme.DefaultTheme.Normal
		trueCursor := "  "
		falseCursor := "  "
		if m.boolValue {
			trueCursor = theme.DefaultTheme.Highlight.Render(theme.IconArrowRightBold) + " "
			trueStyle = theme.DefaultTheme.Highlight
		} else {
			falseCursor = theme.DefaultTheme.Highlight.Render(theme.IconArrowRightBold) + " "
			falseStyle = theme.DefaultTheme.Highlight
		}
		content = trueStyle.Render(trueCursor+"true") + "\n" + falseStyle.Render(falseCursor+"false")
	default:
		content = m.input.View()
	}

	// Layer selection info
	targetPath := m.getLayerFilePath(m.targetLayer)
	layerBadge := renderLayerBadge(m.targetLayer)
	layerInfo := fmt.Sprintf("Save to: %s %s", layerBadge, theme.DefaultTheme.Path.Render(setup.AbbreviatePath(targetPath)))

	recommendation := ""
	if m.targetLayer == node.Field.Layer {
		recommendation = theme.DefaultTheme.Muted.Render("         (" + layerRecommendation(m.targetLayer) + ")")
	}

	separator := theme.DefaultTheme.Muted.Render(strings.Repeat("─", 55))
	helpText := theme.DefaultTheme.Muted.Render("enter: save • tab: change layer • esc: cancel")

	var parts []string
	parts = append(parts, title, "", currentInfo)
	if hintText != "" {
		parts = append(parts, hintText)
	}
	parts = append(parts, "", content, "", separator, layerInfo)
	if recommendation != "" {
		parts = append(parts, recommendation)
	}
	parts = append(parts, "", helpText)

	ui := lipgloss.JoinVertical(lipgloss.Left, parts...)
	dialog := boxStyle.Render(ui)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog)
}

func (m configModel) renderInfoView() string {
	if m.editNode == nil {
		return "No node selected for info"
	}
	node := m.editNode

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Colors.Cyan).
		Padding(1, 2).
		Width(70)

	title := theme.DefaultTheme.Bold.Render(node.DisplayKey())

	// Prepend status block if applicable (alpha, beta, deprecated)
	var statusBlock string
	if node.Field.IsNonStable() {
		notice := node.Field.StatusNotice()

		var blockStyle lipgloss.Style
		var header string

		switch node.Field.Status {
		case configui.StatusAlpha:
			blockStyle = lipgloss.NewStyle().
				Foreground(theme.DefaultTheme.Colors.Blue).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(theme.DefaultTheme.Colors.Blue).
				Padding(0, 1).
				MarginBottom(1).
				Width(60)
			header = lipgloss.NewStyle().Bold(true).Render("α ALPHA FIELD")
		case configui.StatusBeta:
			blockStyle = lipgloss.NewStyle().
				Foreground(theme.DefaultTheme.Colors.Yellow).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(theme.DefaultTheme.Colors.Yellow).
				Padding(0, 1).
				MarginBottom(1).
				Width(60)
			header = lipgloss.NewStyle().Bold(true).Render("β BETA FIELD")
		case configui.StatusDeprecated:
			blockStyle = lipgloss.NewStyle().
				Foreground(theme.DefaultTheme.Colors.Red).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(theme.DefaultTheme.Colors.Red).
				Padding(0, 1).
				MarginBottom(1).
				Width(60)
			header = lipgloss.NewStyle().Bold(true).Render("⚠ DEPRECATED FIELD")
		}

		content := theme.DefaultTheme.Normal.Render(notice)
		statusBlock = blockStyle.Render(lipgloss.JoinVertical(lipgloss.Left, header, "", content))
	}

	desc := theme.DefaultTheme.Muted.Render(node.Field.Description)

	// Build layer values table
	var layerRows []string

	// Format layer values
	formatLayerVal := func(v interface{}) string {
		if v == nil {
			return "(not set)"
		}
		return configui.FormatValue(v)
	}

	// Default layer
	defaultVal := formatLayerVal(node.LayerValues.Default)
	layerRows = append(layerRows, m.renderLayerRow("Default", defaultVal, "", node.ActiveSource == config.SourceDefault))

	// Global layer
	globalVal := formatLayerVal(node.LayerValues.Global)
	globalPath := m.layered.FilePaths[config.SourceGlobal]
	layerRows = append(layerRows, m.renderLayerRow("Global", globalVal, globalPath, node.ActiveSource == config.SourceGlobal))

	// Global Override layer (only show if exists)
	if m.layered.GlobalOverride != nil {
		globalOverrideVal := formatLayerVal(node.LayerValues.GlobalOverride)
		globalOverridePath := m.layered.FilePaths[config.SourceGlobalOverride]
		layerRows = append(layerRows, m.renderLayerRow("Global*", globalOverrideVal, globalOverridePath, node.ActiveSource == config.SourceGlobalOverride))
	}

	// Env Overlay layer (only show if exists)
	if m.layered.EnvOverlay != nil {
		envOverlayVal := formatLayerVal(node.LayerValues.EnvOverlay)
		envOverlayPath := m.layered.FilePaths[config.SourceEnvOverlay]
		layerRows = append(layerRows, m.renderLayerRow("Env*", envOverlayVal, envOverlayPath, node.ActiveSource == config.SourceEnvOverlay))
	}

	// Ecosystem layer
	ecoVal := formatLayerVal(node.LayerValues.Ecosystem)
	ecoPath := m.layered.FilePaths[config.SourceEcosystem]
	layerRows = append(layerRows, m.renderLayerRow("Ecosystem", ecoVal, ecoPath, node.ActiveSource == config.SourceEcosystem))

	// Project layer
	projVal := formatLayerVal(node.LayerValues.Project)
	projPath := m.layered.FilePaths[config.SourceProject]
	layerRows = append(layerRows, m.renderLayerRow("Project", projVal, projPath, node.ActiveSource == config.SourceProject))

	// Override layer (only show if exists)
	if len(m.layered.Overrides) > 0 {
		overrideVal := formatLayerVal(node.LayerValues.Override)
		overridePath := m.layered.FilePaths[config.SourceOverride]
		layerRows = append(layerRows, m.renderLayerRow("Local*", overrideVal, overridePath, node.ActiveSource == config.SourceOverride))
	}

	layersContent := strings.Join(layerRows, "\n")

	separator := theme.DefaultTheme.Muted.Render(strings.Repeat("─", 60))

	// Active value summary
	activeLabel := theme.DefaultTheme.Bold.Render("Active")
	currentValue := configui.FormatValue(node.Value)
	activeVal := theme.DefaultTheme.Success.Render(currentValue)
	if currentValue == "(unset)" {
		activeVal = theme.DefaultTheme.Muted.Render("(unset)")
	}
	activeSrc := theme.DefaultTheme.Muted.Render(fmt.Sprintf("(from %s)", layerDisplayName(node.ActiveSource)))
	activeLine := fmt.Sprintf("  %s      %s  %s", activeLabel, activeVal, activeSrc)

	// Recommendation
	recLayer := layerDisplayName(node.Field.Layer)
	recReason := layerRecommendation(node.Field.Layer)
	recLine := theme.DefaultTheme.Muted.Render(fmt.Sprintf("Recommended layer: %s (%s)", recLayer, recReason))

	// Field metadata
	var metaLines []string
	if node.Field.Important {
		metaLines = append(metaLines, theme.DefaultTheme.Highlight.Render("★ Important"))
	}
	if node.Field.Sensitive {
		metaLines = append(metaLines, theme.DefaultTheme.Error.Render("⚠ Sensitive field"))
	}
	if node.Field.Namespace != "" {
		metaLines = append(metaLines, theme.DefaultTheme.Muted.Render(fmt.Sprintf("Namespace: %s", node.Field.Namespace)))
	}
	if node.IsDynamic {
		metaLines = append(metaLines, theme.DefaultTheme.Muted.Render("(dynamic field)"))
	}

	helpText := theme.DefaultTheme.Muted.Render("enter: edit • esc: back")

	var parts []string
	// Insert status block at the top if present (alpha, beta, deprecated)
	if statusBlock != "" {
		parts = append(parts, statusBlock, "")
	}
	parts = append(parts, title, desc, "", layersContent, separator, activeLine, "", recLine)
	if len(metaLines) > 0 {
		parts = append(parts, "")
		parts = append(parts, metaLines...)
	}
	parts = append(parts, "", helpText)

	ui := lipgloss.JoinVertical(lipgloss.Left, parts...)
	dialog := boxStyle.Render(ui)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog)
}

func (m configModel) renderSourcesView() string {
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Colors.Violet).
		Padding(1, 2).
		Width(80)

	title := theme.DefaultTheme.Bold.Render("Configuration Sources")

	// Get current working directory for context
	cwd, _ := os.Getwd()
	cwdLine := theme.DefaultTheme.Muted.Render("Working directory: ") + theme.DefaultTheme.Path.Render(setup.AbbreviatePath(cwd))

	separator := theme.DefaultTheme.Muted.Render(strings.Repeat("─", 70))

	// Build list of all config sources with their paths
	var sourceRows []string

	// Helper to add a source row
	addSource := func(name string, source config.ConfigSource, exists bool) {
		path := m.layered.FilePaths[source]
		var row string
		nameStyle := theme.DefaultTheme.Normal
		if exists && path != "" {
			nameStyle = theme.DefaultTheme.Success
			row = fmt.Sprintf("  %s  %s",
				nameStyle.Render(fmt.Sprintf("%-16s", name)),
				theme.DefaultTheme.Path.Render(path))
		} else {
			row = fmt.Sprintf("  %s  %s",
				theme.DefaultTheme.Muted.Render(fmt.Sprintf("%-16s", name)),
				theme.DefaultTheme.Muted.Render("(not found)"))
		}
		sourceRows = append(sourceRows, row)
	}

	// Add all layers in priority order (lowest to highest)
	addSource("Default", config.SourceDefault, true) // Always exists (built-in)
	addSource("Global", config.SourceGlobal, m.layered.Global != nil)
	addSource("Global*", config.SourceGlobalOverride, m.layered.GlobalOverride != nil)
	addSource("Env*", config.SourceEnvOverlay, m.layered.EnvOverlay != nil)
	addSource("Ecosystem", config.SourceEcosystem, m.layered.Ecosystem != nil)
	addSource("Project", config.SourceProject, m.layered.Project != nil)
	addSource("Local*", config.SourceOverride, len(m.layered.Overrides) > 0)

	sourcesContent := strings.Join(sourceRows, "\n")

	// Priority explanation
	priorityNote := theme.DefaultTheme.Muted.Render("Priority: Local* > Project > Ecosystem > Env* > Global* > Global > Default")
	overrideNote := theme.DefaultTheme.Muted.Render("* = override file (e.g., grove.override.toml)")

	helpText := theme.DefaultTheme.Muted.Render("esc: back")

	var parts []string
	parts = append(parts, title, "", cwdLine, "", separator, "", sourcesContent, "", separator, "", priorityNote, overrideNote, "", helpText)

	ui := lipgloss.JoinVertical(lipgloss.Left, parts...)
	dialog := boxStyle.Render(ui)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog)
}

// renderLayerRow renders a single row in the layer info table.
func (m configModel) renderLayerRow(name, value, path string, isActive bool) string {
	nameWidth := 12
	valueWidth := 20

	nameStyle := theme.DefaultTheme.Normal
	valueStyle := theme.DefaultTheme.Muted

	if value != "(not set)" {
		valueStyle = theme.DefaultTheme.Normal
	}

	if isActive {
		nameStyle = theme.DefaultTheme.Bold
		valueStyle = theme.DefaultTheme.Success
		name = name + " ←"
	}

	namePart := nameStyle.Render(fmt.Sprintf("  %-*s", nameWidth, name))
	valuePart := valueStyle.Render(fmt.Sprintf("%-*s", valueWidth, value))

	pathPart := ""
	if path != "" {
		pathPart = theme.DefaultTheme.Muted.Render(setup.AbbreviatePath(path))
	}

	return namePart + valuePart + pathPart
}

// getLayerFilePath returns the file path for a given layer.
func (m configModel) getLayerFilePath(layer config.ConfigSource) string {
	if path, ok := m.layered.FilePaths[layer]; ok && path != "" {
		return path
	}
	// Fallback for global
	if layer == config.SourceGlobal {
		return setup.GlobalTOMLConfigPath()
	}
	return ""
}
