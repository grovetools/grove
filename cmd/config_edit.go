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
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/components/help"
	"github.com/grovetools/core/tui/keymap"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/configui"
	"github.com/grovetools/grove/pkg/setup"
	"github.com/spf13/cobra"
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

// configItem wraps a ConfigNode to implement list.Item interface.
type configItem struct {
	node *configui.ConfigNode
}

func (i configItem) Title() string       { return i.node.DisplayKey() }
func (i configItem) Description() string { return i.node.Field.Description }
func (i configItem) FilterValue() string { return i.node.DisplayKey() }

// configKeyMap defines key bindings for the config editor
type configKeyMap struct {
	keymap.Base
	Edit        key.Binding
	Info        key.Binding
	Confirm     key.Binding
	Cancel      key.Binding
	SwitchLayer key.Binding
	Toggle      key.Binding // Space to toggle expand/collapse
	Expand      key.Binding // Right/l to expand
	Collapse    key.Binding // Left/h to collapse
}

var configKeys = configKeyMap{
	Base: keymap.NewBase(),
	Edit: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "edit value"),
	),
	Info: key.NewBinding(
		key.WithKeys("i"),
		key.WithHelp("i", "field info"),
	),
	Confirm: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "save"),
	),
	Cancel: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "cancel"),
	),
	SwitchLayer: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "change layer"),
	),
	Toggle: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("space", "expand/collapse"),
	),
	Expand: key.NewBinding(
		key.WithKeys("right", "l"),
		key.WithHelp("l/→", "expand"),
	),
	Collapse: key.NewBinding(
		key.WithKeys("left", "h"),
		key.WithHelp("h/←", "collapse/parent"),
	),
}

func (k configKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Edit, k.Toggle, k.Info, k.Base.Help, k.Base.Quit}
}

func (k configKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		// Navigation
		{k.Base.Up, k.Base.Down, k.Base.PageUp, k.Base.PageDown},
		// Tree navigation
		{k.Toggle, k.Expand, k.Collapse},
		// Actions
		{k.Edit, k.Info},
		// Exit
		{k.Base.Quit, k.Base.Help},
	}
}

// configDelegate renders config items with their current values and layer badges.
type configDelegate struct{}

func (d configDelegate) Height() int                             { return 1 }
func (d configDelegate) Spacing() int                            { return 0 }
func (d configDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d configDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	i, ok := item.(configItem)
	if !ok {
		return
	}

	node := i.node

	// Build indentation based on depth
	indent := strings.Repeat("  ", node.Depth)

	// Tree indicator: ▶ collapsed, ▼ expanded, • for leaf nodes
	indicator := "  "
	if node.IsExpandable() {
		if node.Collapsed {
			indicator = "▶ "
		} else {
			indicator = "▼ "
		}
	} else if node.Depth > 0 {
		indicator = "• "
	}

	// Cursor: fat arrow for selected row
	cursor := "  "
	if index == m.Index() {
		cursor = theme.DefaultTheme.Highlight.Render(theme.IconArrowRightBold) + " "
	}

	// Title styling with wizard indicator
	title := node.DisplayKey()
	if node.Field.Wizard {
		title = title + " " + theme.DefaultTheme.Highlight.Render("★")
	}
	if index == m.Index() {
		title = theme.DefaultTheme.Bold.Render(title)
	}

	// Value display using the new FormatValue function
	val := configui.FormatValue(node.Value)

	// Mask sensitive fields for display
	if node.Field.Sensitive && val != "(unset)" && len(val) > 8 {
		val = "********"
	}

	valueStyle := theme.DefaultTheme.Muted
	if val != "(unset)" && val != "(empty)" {
		valueStyle = theme.DefaultTheme.Success
	}

	// For container types that are collapsed, use muted style
	if node.IsContainer() && node.Collapsed {
		valueStyle = theme.DefaultTheme.Muted
	}

	// Layer badge with color coding
	badge := renderLayerBadge(node.ActiveSource)

	// Render: cursor + indent + indicator + title + value + badge (single line)
	valDisplay := valueStyle.Render(val)
	indicatorStyled := theme.DefaultTheme.Muted.Render(indicator)
	fmt.Fprintf(w, "%s%s%s%s  %s  %s", cursor, indent, indicatorStyled, title, valDisplay, badge)
}

// renderLayerBadge renders a colored badge for the config source.
func renderLayerBadge(source config.ConfigSource) string {
	var style lipgloss.Style
	var label string

	switch source {
	case config.SourceGlobal:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Blue)
		label = "[Global]"
	case config.SourceGlobalOverride:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Cyan)
		label = "[Global Override]"
	case config.SourceEnvOverlay:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Yellow)
		label = "[Env Overlay]"
	case config.SourceEcosystem:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Violet)
		label = "[Ecosystem]"
	case config.SourceProject:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Green)
		label = "[Project]"
	case config.SourceOverride:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Orange)
		label = "[Override]"
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
		return "Global Override"
	case config.SourceEnvOverlay:
		return "Env Overlay"
	case config.SourceEcosystem:
		return "Ecosystem"
	case config.SourceProject:
		return "Project"
	case config.SourceOverride:
		return "Override"
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
)

type configModel struct {
	list  list.Model
	input textinput.Model

	// Tree-based config state
	treeRoots []*configui.ConfigNode // Root nodes of the config tree

	// Layered config state
	layered *config.LayeredConfig

	// Per-layer file handlers (for saving to specific layers)
	yamlHandler *setup.YAMLHandler
	tomlHandler *setup.TOMLHandler

	// View state
	state       viewState
	editIndex   int                 // Index in list being edited
	selectIndex int                 // Index in options list if typeSelect
	targetLayer config.ConfigSource // Which layer to save to during edit
	boolValue   bool                // Current bool value for typeBool editing

	statusMsg string
	keys      configKeyMap
	help      help.Model
	width     int
	height    int
	err       error
}

func runConfigEdit(cmd *cobra.Command, args []string) error {
	cwd, _ := os.Getwd()

	// Load layered configuration
	layered, err := config.LoadLayered(cwd)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w\nRun 'grove setup' to create one", err)
	}

	// Initialize Setup Service and handlers
	svc := setup.NewService(false) // Not dry-run

	// Initialize Model
	m := configModel{
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

	// Build tree from schema and config
	m.treeRoots = configui.BuildTree(configui.SchemaFields, layered)

	// Create list with tree-based items
	delegate := configDelegate{}
	m.list = list.New([]list.Item{}, delegate, 0, 0)
	m.refreshList()

	// Show context in title
	contextPath := cwd
	if m.layered.FilePaths[config.SourceProject] != "" {
		contextPath = filepath.Dir(m.layered.FilePaths[config.SourceProject])
	} else if m.layered.FilePaths[config.SourceEcosystem] != "" {
		contextPath = filepath.Dir(m.layered.FilePaths[config.SourceEcosystem])
	}
	m.list.Title = fmt.Sprintf("Configuration  %s", theme.DefaultTheme.Muted.Render(setup.AbbreviatePath(contextPath)))
	m.list.SetShowStatusBar(false)
	m.list.SetShowHelp(false)
	m.list.SetShowPagination(false)
	m.list.SetFilteringEnabled(false)
	m.list.DisableQuitKeybindings()

	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

// refreshList flattens the tree and updates the list items.
func (m *configModel) refreshList() {
	visibleNodes := configui.Flatten(m.treeRoots)
	var items []list.Item
	for _, node := range visibleNodes {
		items = append(items, configItem{node: node})
	}
	m.list.SetItems(items)
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
		m.list.SetSize(msg.Width, msg.Height-6) // Reserve space for footer/edit box
		m.help.Width = msg.Width
		m.help.Height = msg.Height

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
		default:
			return m.updateList(msg)
		}
	}

	if m.state == viewList {
		m.list, cmd = m.list.Update(msg)
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

	// Toggle expansion with space
	case " ":
		idx := m.list.Index()
		if idx >= 0 && idx < len(m.list.Items()) {
			node := m.list.Items()[idx].(configItem).node
			if node.IsExpandable() {
				configui.ToggleNode(node)
				m.refreshList()
			}
		}
		return m, nil

	// Expand with right/l
	case "right", "l":
		idx := m.list.Index()
		if idx >= 0 && idx < len(m.list.Items()) {
			node := m.list.Items()[idx].(configItem).node
			if node.IsExpandable() && node.Collapsed {
				node.Collapsed = false
				m.refreshList()
			}
		}
		return m, nil

	// Collapse with left/h
	case "left", "h":
		idx := m.list.Index()
		if idx >= 0 && idx < len(m.list.Items()) {
			node := m.list.Items()[idx].(configItem).node
			if node.IsExpandable() && !node.Collapsed {
				// If expanded, collapse it
				node.Collapsed = true
				m.refreshList()
			} else if node.Parent != nil {
				// If collapsed or leaf, try to select parent
				visibleNodes := configui.Flatten(m.treeRoots)
				parentIdx := configui.FindParentIndex(visibleNodes, node)
				if parentIdx >= 0 {
					m.list.Select(parentIdx)
				}
			}
		}
		return m, nil

	case "enter":
		idx := m.list.Index()
		if idx >= 0 && idx < len(m.list.Items()) {
			itm := m.list.Items()[idx].(configItem)
			node := itm.node

			// If it's a container with children, toggle expand
			if node.IsExpandable() {
				configui.ToggleNode(node)
				m.refreshList()
				return m, nil
			}

			// If it's a leaf, start editing
			m.startEdit(idx, itm)
		}
		return m, nil

	case "i":
		idx := m.list.Index()
		if idx >= 0 && idx < len(m.list.Items()) {
			m.editIndex = idx
			m.state = viewInfo
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m configModel) updateInfo(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.state = viewList
		return m, nil
	case "enter":
		// Start editing from info view
		itm := m.list.Items()[m.editIndex].(configItem)
		m.startEdit(m.editIndex, itm)
		return m, nil
	}
	return m, nil
}

func (m *configModel) startEdit(idx int, itm configItem) {
	node := itm.node

	// Don't allow editing container types directly
	if node.IsContainer() {
		m.statusMsg = "Expand node to edit children"
		return
	}

	m.state = viewEdit
	m.editIndex = idx
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
	itm := m.list.Items()[m.editIndex].(configItem)
	node := itm.node

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

// reloadConfig reloads the layered config and refreshes the tree.
func (m *configModel) reloadConfig() error {
	cwd, _ := os.Getwd()
	layered, err := config.LoadLayered(cwd)
	if err != nil {
		return err
	}
	m.layered = layered

	// Rebuild tree from schema and config
	m.treeRoots = configui.BuildTree(configui.SchemaFields, layered)

	// Refresh the list
	m.refreshList()

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
	default:
		return m.renderListView()
	}
}

func (m configModel) renderListView() string {
	var b strings.Builder

	// List view
	b.WriteString(m.list.View())
	b.WriteString("\n\n")

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

	return b.String()
}

func (m configModel) renderEditView() string {
	itm := m.list.Items()[m.editIndex].(configItem)
	node := itm.node

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
	itm := m.list.Items()[m.editIndex].(configItem)
	node := itm.node

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Colors.Cyan).
		Padding(1, 2).
		Width(70)

	title := theme.DefaultTheme.Bold.Render(node.DisplayKey())
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
		layerRows = append(layerRows, m.renderLayerRow("Global Override", globalOverrideVal, globalOverridePath, node.ActiveSource == config.SourceGlobalOverride))
	}

	// Env Overlay layer (only show if exists)
	if m.layered.EnvOverlay != nil {
		envOverlayVal := formatLayerVal(node.LayerValues.EnvOverlay)
		envOverlayPath := m.layered.FilePaths[config.SourceEnvOverlay]
		layerRows = append(layerRows, m.renderLayerRow("Env Overlay", envOverlayVal, envOverlayPath, node.ActiveSource == config.SourceEnvOverlay))
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
		layerRows = append(layerRows, m.renderLayerRow("Override", overrideVal, overridePath, node.ActiveSource == config.SourceOverride))
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
	if node.Field.Wizard {
		metaLines = append(metaLines, theme.DefaultTheme.Highlight.Render("★ Wizard field"))
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
