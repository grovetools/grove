package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
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

type configFieldType int

const (
	typeString configFieldType = iota
	typeSelect
	typeBool
)

// configFieldSchema defines a configuration field with its recommended layer.
type configFieldSchema struct {
	Path             []string            // e.g., ["tui", "theme"]
	Label            string              // Human-readable label
	Description      string              // Help text
	Type             configFieldType     // String, Select, Bool
	Options          []string            // For Select type
	RecommendedLayer config.ConfigSource // Where this field should typically live
}

// LayeredConfigValue holds the value for a field across all layers.
type LayeredConfigValue struct {
	Default   string // Value from defaults
	Global    string // Value from global config
	Ecosystem string // Value from ecosystem config
	Project   string // Value from project config
}

// configSchema defines all configurable fields with their recommended layers.
var configSchema = []configFieldSchema{
	// --- Global Fields (Personal Preferences) ---
	{
		Path:             []string{"tui", "theme"},
		Label:            "TUI Theme",
		Description:      "Color theme for terminal interfaces",
		Type:             typeSelect,
		Options:          []string{"terminal", "gruvbox", "kanagawa"},
		RecommendedLayer: config.SourceGlobal,
	},
	{
		Path:             []string{"tui", "icons"},
		Label:            "Icon Set",
		Description:      "Icons for terminal interfaces",
		Type:             typeSelect,
		Options:          []string{"nerd", "ascii"},
		RecommendedLayer: config.SourceGlobal,
	},
	{
		Path:             []string{"flow", "oneshot_model"},
		Label:            "Flow Model",
		Description:      "Default LLM model for oneshot jobs",
		Type:             typeString,
		RecommendedLayer: config.SourceGlobal,
	},
	{
		Path:             []string{"logging", "file", "enabled"},
		Label:            "File Logging",
		Description:      "Enable logging to file",
		Type:             typeBool,
		RecommendedLayer: config.SourceGlobal,
	},
	{
		Path:             []string{"daemon", "git_interval"},
		Label:            "Git Poll Interval",
		Description:      "How often to poll git status (e.g. 10s)",
		Type:             typeString,
		RecommendedLayer: config.SourceGlobal,
	},
	{
		Path:             []string{"gemini", "api_key_command"},
		Label:            "Gemini Key Command",
		Description:      "Command to retrieve API key (e.g. 1password)",
		Type:             typeString,
		RecommendedLayer: config.SourceGlobal,
	},
	{
		Path:             []string{"notebooks", "path"},
		Label:            "Notebook Path",
		Description:      "Location of your notebooks directory",
		Type:             typeString,
		RecommendedLayer: config.SourceGlobal,
	},

	// --- Ecosystem Fields (Shared Settings) ---
	{
		Path:             []string{"name"},
		Label:            "Ecosystem/Project Name",
		Description:      "Name shown in UI and logs",
		Type:             typeString,
		RecommendedLayer: config.SourceEcosystem,
	},

	// --- Project Fields (This Project Only) ---
	{
		Path:             []string{"build_cmd"},
		Label:            "Build Command",
		Description:      "Custom build command for this project",
		Type:             typeString,
		RecommendedLayer: config.SourceProject,
	},
	{
		Path:             []string{"description"},
		Label:            "Description",
		Description:      "Project description",
		Type:             typeString,
		RecommendedLayer: config.SourceProject,
	},
}

// --- Model & State ---

type configItem struct {
	field        configFieldSchema
	value        string              // Final merged value
	activeSource config.ConfigSource // Which layer provided the value
	layerValues  LayeredConfigValue  // Values at each layer
}

func (i configItem) Title() string       { return i.field.Label }
func (i configItem) Description() string { return i.field.Description }
func (i configItem) FilterValue() string { return i.field.Label }

// configKeyMap defines key bindings for the config editor
type configKeyMap struct {
	keymap.Base
	Edit       key.Binding
	Info       key.Binding
	Confirm    key.Binding
	Cancel     key.Binding
	SwitchLayer key.Binding
}

var configKeys = configKeyMap{
	Base: keymap.NewBase(),
	Edit: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "edit"),
	),
	Info: key.NewBinding(
		key.WithKeys("i"),
		key.WithHelp("i", "info"),
	),
	Confirm: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "save"),
	),
	Cancel: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "cancel/back"),
	),
	SwitchLayer: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch layer"),
	),
}

func (k configKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Edit, k.Info, k.Base.Quit}
}

func (k configKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Base.Up, k.Base.Down, k.Edit, k.Info},
		{k.Confirm, k.Cancel, k.SwitchLayer, k.Base.Quit},
	}
}

// configDelegate renders config items with their current values and layer badges.
type configDelegate struct{}

func (d configDelegate) Height() int                             { return 2 }
func (d configDelegate) Spacing() int                            { return 0 }
func (d configDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d configDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	i, ok := item.(configItem)
	if !ok {
		return
	}

	// Cursor: fat arrow for selected row
	cursor := "  "
	if index == m.Index() {
		cursor = theme.DefaultTheme.Highlight.Render(theme.IconArrowRightBold) + " "
	}

	// Title styling
	title := i.field.Label
	if index == m.Index() {
		title = theme.DefaultTheme.Bold.Render(title)
	}

	// Value display
	val := i.value
	if val == "" {
		val = "(unset)"
	}
	// Mask API keys for display
	if strings.Contains(strings.ToLower(i.field.Label), "api key") && val != "(unset)" && len(val) > 8 {
		val = val[:4] + "..." + val[len(val)-4:]
	}
	valueStyle := theme.DefaultTheme.Muted
	if val != "(unset)" {
		valueStyle = theme.DefaultTheme.Success
	}

	// Layer badge with color coding
	badge := renderLayerBadge(i.activeSource)

	// Description with current value
	desc := theme.DefaultTheme.Muted.Render(i.field.Description)
	valDisplay := valueStyle.Render(val)

	// Render: cursor + title + value + badge on first line, indented description on second
	fmt.Fprintf(w, "%s%s  %s  %s\n", cursor, title, valDisplay, badge)
	fmt.Fprintf(w, "     %s", desc)
}

// renderLayerBadge renders a colored badge for the config source.
func renderLayerBadge(source config.ConfigSource) string {
	var style lipgloss.Style
	var label string

	switch source {
	case config.SourceGlobal:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Blue)
		label = "[Global]"
	case config.SourceEcosystem:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Violet)
		label = "[Ecosystem]"
	case config.SourceProject:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Green)
		label = "[Project]"
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
	case config.SourceEcosystem:
		return "Ecosystem"
	case config.SourceProject:
		return "Project"
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
	items []configItem
	input textinput.Model

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
		help:        help.New(configKeys),
		state:       viewList,
		yamlHandler: setup.NewYAMLHandler(svc),
		tomlHandler: setup.NewTOMLHandler(svc),
	}

	m.input.Prompt = "  > "
	m.input.CharLimit = 200
	m.input.Width = 50

	// Populate list from layered config
	var listItems []list.Item
	for _, schema := range configSchema {
		itm := m.buildConfigItem(schema)
		m.items = append(m.items, itm)
		listItems = append(listItems, itm)
	}

	delegate := configDelegate{}
	m.list = list.New(listItems, delegate, 0, 0)

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

// buildConfigItem extracts values from all layers for a schema field.
func (m *configModel) buildConfigItem(schema configFieldSchema) configItem {
	item := configItem{
		field:        schema,
		activeSource: config.SourceDefault,
		layerValues:  LayeredConfigValue{},
	}

	// Extract values from each layer
	if m.layered.Default != nil {
		item.layerValues.Default = getConfigValue(m.layered.Default, schema.Path)
	}
	if m.layered.Global != nil {
		item.layerValues.Global = getConfigValue(m.layered.Global, schema.Path)
	}
	if m.layered.Ecosystem != nil {
		item.layerValues.Ecosystem = getConfigValue(m.layered.Ecosystem, schema.Path)
	}
	if m.layered.Project != nil {
		item.layerValues.Project = getConfigValue(m.layered.Project, schema.Path)
	}

	// Get final merged value
	if m.layered.Final != nil {
		item.value = getConfigValue(m.layered.Final, schema.Path)
	}

	// Determine which layer the final value came from (highest priority wins)
	// Priority: Project > Ecosystem > Global > Default
	if item.layerValues.Project != "" {
		item.activeSource = config.SourceProject
	} else if item.layerValues.Ecosystem != "" {
		item.activeSource = config.SourceEcosystem
	} else if item.layerValues.Global != "" {
		item.activeSource = config.SourceGlobal
	} else {
		item.activeSource = config.SourceDefault
	}

	return item
}

// getConfigValue extracts a value from a Config struct using a path.
func getConfigValue(cfg *config.Config, path []string) string {
	if cfg == nil || len(path) == 0 {
		return ""
	}

	// Use reflection to navigate the config struct
	v := reflect.ValueOf(cfg).Elem()

	for i, key := range path {
		// Try to find the field by tag or name
		field := findField(v, key)
		if !field.IsValid() {
			// Check extensions map for unknown fields
			if ext, ok := cfg.Extensions[path[0]]; ok {
				if extMap, ok := ext.(map[string]interface{}); ok {
					return getNestedMapValue(extMap, path[1:])
				}
			}
			return ""
		}

		if i == len(path)-1 {
			// Final field - convert to string
			return fieldToString(field)
		}

		// Navigate deeper (handle pointer types)
		if field.Kind() == reflect.Ptr {
			if field.IsNil() {
				return ""
			}
			field = field.Elem()
		}
		v = field
	}

	return ""
}

// findField finds a struct field by TOML/YAML tag or field name.
func findField(v reflect.Value, key string) reflect.Value {
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return reflect.Value{}
	}

	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// Check TOML tag
		if tag := field.Tag.Get("toml"); tag != "" {
			tagName := strings.Split(tag, ",")[0]
			if tagName == key {
				return v.Field(i)
			}
		}

		// Check YAML tag
		if tag := field.Tag.Get("yaml"); tag != "" {
			tagName := strings.Split(tag, ",")[0]
			if tagName == key {
				return v.Field(i)
			}
		}

		// Check field name (case insensitive)
		if strings.EqualFold(field.Name, key) {
			return v.Field(i)
		}
	}

	return reflect.Value{}
}

// fieldToString converts a reflect.Value to a string representation.
func fieldToString(v reflect.Value) string {
	if !v.IsValid() {
		return ""
	}

	// Handle pointers
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Bool:
		if v.Bool() {
			return "true"
		}
		return "false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%d", v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("%d", v.Uint())
	case reflect.Float32, reflect.Float64:
		return fmt.Sprintf("%g", v.Float())
	default:
		return fmt.Sprintf("%v", v.Interface())
	}
}

// getNestedMapValue extracts a value from a nested map using a path.
func getNestedMapValue(m map[string]interface{}, path []string) string {
	if len(path) == 0 {
		return ""
	}

	current := m
	for i, key := range path {
		val, ok := current[key]
		if !ok {
			return ""
		}

		if i == len(path)-1 {
			switch v := val.(type) {
			case string:
				return v
			case bool:
				if v {
					return "true"
				}
				return "false"
			case int, int64, float64:
				return fmt.Sprintf("%v", v)
			default:
				return fmt.Sprintf("%v", v)
			}
		}

		nested, ok := val.(map[string]interface{})
		if !ok {
			return ""
		}
		current = nested
	}
	return ""
}

// getTomlValue retrieves a value from a nested TOML map using a path
func getTomlValue(data map[string]interface{}, path ...string) string {
	if len(path) == 0 {
		return ""
	}

	current := data
	for i, key := range path {
		val, ok := current[key]
		if !ok {
			return ""
		}

		if i == len(path)-1 {
			// Last key - return the value as string
			switch v := val.(type) {
			case string:
				return v
			case int, int64, float64, bool:
				return fmt.Sprintf("%v", v)
			default:
				return ""
			}
		}

		// Navigate deeper
		nested, ok := val.(map[string]interface{})
		if !ok {
			return ""
		}
		current = nested
	}
	return ""
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

	case tea.KeyMsg:
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
	case "enter":
		idx := m.list.Index()
		if idx >= 0 && idx < len(m.list.Items()) {
			itm := m.list.Items()[idx].(configItem)
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
	m.state = viewEdit
	m.editIndex = idx
	m.statusMsg = ""

	// Start with the recommended layer, but use active source if it's already set there
	m.targetLayer = itm.field.RecommendedLayer

	// If the value is already set in a specific layer, default to that layer
	if itm.activeSource != config.SourceDefault {
		m.targetLayer = itm.activeSource
	}

	switch itm.field.Type {
	case typeString:
		m.input.SetValue(itm.value)
		m.input.Focus()
	case typeSelect:
		// Find current option index
		m.selectIndex = 0
		for i, opt := range itm.field.Options {
			if opt == itm.value {
				m.selectIndex = i
				break
			}
		}
	case typeBool:
		m.boolValue = itm.value == "true"
	}
}

func (m *configModel) updateEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	itm := m.list.Items()[m.editIndex].(configItem)

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
		switch itm.field.Type {
		case typeString:
			newValue = m.input.Value()
		case typeSelect:
			newValue = itm.field.Options[m.selectIndex]
		case typeBool:
			if m.boolValue {
				newValue = "true"
			} else {
				newValue = "false"
			}
		}

		// Save to the target layer
		if err := m.saveToLayer(itm.field.Path, newValue, m.targetLayer); err != nil {
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
	if itm.field.Type == typeSelect {
		switch msg.String() {
		case "up", "k":
			if m.selectIndex > 0 {
				m.selectIndex--
			}
		case "down", "j":
			if m.selectIndex < len(itm.field.Options)-1 {
				m.selectIndex++
			}
		}
		return m, nil
	}

	// Handle navigation for Bool type
	if itm.field.Type == typeBool {
		switch msg.String() {
		case "up", "k", "down", "j", " ":
			m.boolValue = !m.boolValue
		}
		return m, nil
	}

	// Handle input for String type
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
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

// reloadConfig reloads the layered config and refreshes the list items.
func (m *configModel) reloadConfig() error {
	cwd, _ := os.Getwd()
	layered, err := config.LoadLayered(cwd)
	if err != nil {
		return err
	}
	m.layered = layered

	// Rebuild list items
	var listItems []list.Item
	m.items = nil
	for _, schema := range configSchema {
		itm := m.buildConfigItem(schema)
		m.items = append(m.items, itm)
		listItems = append(listItems, itm)
	}
	m.list.SetItems(listItems)

	return nil
}

func (m configModel) View() string {
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

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Colors.Orange).
		Padding(1, 2).
		Width(65)

	title := theme.DefaultTheme.Bold.Render("Edit: " + itm.field.Label)

	// Current value info
	currentInfo := theme.DefaultTheme.Muted.Render(
		fmt.Sprintf("Current: %s (from %s)", itm.value, layerDisplayName(itm.activeSource)),
	)

	// Edit content based on field type
	var content string
	switch itm.field.Type {
	case typeString:
		content = m.input.View()
	case typeSelect:
		var opts []string
		for i, opt := range itm.field.Options {
			cursor := "  "
			style := theme.DefaultTheme.Normal
			if i == m.selectIndex {
				cursor = theme.DefaultTheme.Highlight.Render(theme.IconArrowRightBold) + " "
				style = theme.DefaultTheme.Highlight
			}
			opts = append(opts, style.Render(cursor+opt))
		}
		content = strings.Join(opts, "\n")
	case typeBool:
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
	}

	// Layer selection info
	targetPath := m.getLayerFilePath(m.targetLayer)
	layerBadge := renderLayerBadge(m.targetLayer)
	layerInfo := fmt.Sprintf("Save to: %s %s", layerBadge, theme.DefaultTheme.Path.Render(setup.AbbreviatePath(targetPath)))

	recommendation := ""
	if m.targetLayer == itm.field.RecommendedLayer {
		recommendation = theme.DefaultTheme.Muted.Render("         (" + layerRecommendation(m.targetLayer) + ")")
	}

	separator := theme.DefaultTheme.Muted.Render(strings.Repeat("─", 55))
	helpText := theme.DefaultTheme.Muted.Render("enter: save • tab: change layer • esc: cancel")

	var parts []string
	parts = append(parts, title, "", currentInfo, "", content, "", separator, layerInfo)
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

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Colors.Cyan).
		Padding(1, 2).
		Width(70)

	title := theme.DefaultTheme.Bold.Render(itm.field.Label)
	desc := theme.DefaultTheme.Muted.Render(itm.field.Description)

	// Build layer values table
	var layerRows []string

	// Default layer
	defaultVal := "(not set)"
	if itm.layerValues.Default != "" {
		defaultVal = itm.layerValues.Default
	}
	layerRows = append(layerRows, m.renderLayerRow("Default", defaultVal, "", itm.activeSource == config.SourceDefault))

	// Global layer
	globalVal := "(not set)"
	globalPath := m.layered.FilePaths[config.SourceGlobal]
	if itm.layerValues.Global != "" {
		globalVal = itm.layerValues.Global
	}
	layerRows = append(layerRows, m.renderLayerRow("Global", globalVal, globalPath, itm.activeSource == config.SourceGlobal))

	// Ecosystem layer
	ecoVal := "(not set)"
	ecoPath := m.layered.FilePaths[config.SourceEcosystem]
	if itm.layerValues.Ecosystem != "" {
		ecoVal = itm.layerValues.Ecosystem
	}
	layerRows = append(layerRows, m.renderLayerRow("Ecosystem", ecoVal, ecoPath, itm.activeSource == config.SourceEcosystem))

	// Project layer
	projVal := "(not set)"
	projPath := m.layered.FilePaths[config.SourceProject]
	if itm.layerValues.Project != "" {
		projVal = itm.layerValues.Project
	}
	layerRows = append(layerRows, m.renderLayerRow("Project", projVal, projPath, itm.activeSource == config.SourceProject))

	layersContent := strings.Join(layerRows, "\n")

	separator := theme.DefaultTheme.Muted.Render(strings.Repeat("─", 60))

	// Active value summary
	activeLabel := theme.DefaultTheme.Bold.Render("Active")
	activeVal := theme.DefaultTheme.Success.Render(itm.value)
	if itm.value == "" {
		activeVal = theme.DefaultTheme.Muted.Render("(unset)")
	}
	activeSrc := theme.DefaultTheme.Muted.Render(fmt.Sprintf("(from %s)", layerDisplayName(itm.activeSource)))
	activeLine := fmt.Sprintf("  %s      %s  %s", activeLabel, activeVal, activeSrc)

	// Recommendation
	recLayer := layerDisplayName(itm.field.RecommendedLayer)
	recReason := layerRecommendation(itm.field.RecommendedLayer)
	recLine := theme.DefaultTheme.Muted.Render(fmt.Sprintf("Recommended layer: %s (%s)", recLayer, recReason))

	helpText := theme.DefaultTheme.Muted.Render("enter: edit • esc: back")

	ui := lipgloss.JoinVertical(lipgloss.Left,
		title,
		desc,
		"",
		layersContent,
		separator,
		activeLine,
		"",
		recLine,
		"",
		helpText,
	)
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
