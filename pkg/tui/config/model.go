package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/tui/components/help"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/embed"
	"github.com/grovetools/grove/pkg/configui"
	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/setup"
)

// viewState tracks which view is active.
type viewState int

const (
	viewList viewState = iota
	viewEdit
	viewInfo
	viewSources
)

// uiState holds persisted UI preferences for the config editor.
type uiState struct {
	ShowPreview    bool                   `json:"show_preview"`
	ViewMode       configui.ViewMode      `json:"view_mode"`
	MaturityFilter configui.MaturityFilter `json:"maturity_filter"`
	SortMode       configui.SortMode      `json:"sort_mode"`
}

// uiStateFile returns the path to the config UI state file.
func uiStateFile() string {
	return filepath.Join(paths.StateDir(), "config-ui.json")
}

// loadUIStateFromDisk loads the config UI state from disk.
func loadUIStateFromDisk() uiState {
	state := uiState{
		ShowPreview:    true,
		ViewMode:       configui.ViewAll,
		MaturityFilter: configui.MaturityStable,
		SortMode:       configui.SortConfiguredFirst,
	}

	data, err := os.ReadFile(uiStateFile())
	if err != nil {
		return state
	}

	_ = json.Unmarshal(data, &state)
	return state
}

// saveUIStateToDisk saves the config UI state to disk.
func saveUIStateToDisk(state uiState) {
	data, err := json.Marshal(state)
	if err != nil {
		return
	}

	stateDir := paths.StateDir()
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return
	}

	_ = os.WriteFile(uiStateFile(), data, 0644)
}

// Model is the embeddable config TUI model. It wraps a pager.Model for
// tab management and handles overlay states (edit, info, sources) on top.
type Model struct {
	pager pager.Model

	// layerPages keeps typed references to the pages so we can call
	// LayerPage-specific methods (Refresh, IsZChordPending, etc.).
	layerPages []*LayerPage

	input textinput.Model

	// Layered config state
	layered *config.LayeredConfig

	// Per-layer file handlers (for saving to specific layers)
	yamlHandler *setup.YAMLHandler
	tomlHandler *setup.TOMLHandler

	// Filter state (shared by pointer with all pages)
	filters *FilterState

	// View state
	state       viewState
	editNode    *configui.ConfigNode
	selectIndex int
	targetLayer config.ConfigSource
	boolValue   bool

	statusMsg string
	keys      grovekeymap.ConfigKeyMap
	help      help.Model
	width     int
	height    int

	// workspacePath is the host's active workspace (set via
	// embed.SetWorkspaceMsg). Used as the LoadLayered root for
	// reloadConfig so edits re-resolve against the current workspace
	// rather than the host process's launch directory. Empty falls
	// back to os.Getwd() — the standalone `grove config` CLI path.
	workspacePath string
}

// New creates a new config TUI Model.
func New(
	layered *config.LayeredConfig,
	yamlHandler *setup.YAMLHandler,
	tomlHandler *setup.TOMLHandler,
	keys grovekeymap.ConfigKeyMap,
) Model {
	// Load persisted UI state
	saved := loadUIStateFromDisk()
	filters := &FilterState{
		ShowPreview:    saved.ShowPreview,
		ViewMode:       saved.ViewMode,
		MaturityFilter: saved.MaturityFilter,
		SortMode:       saved.SortMode,
	}

	width, height := 80, 24 // Initial dummy size

	layerPages := []*LayerPage{
		NewLayerPage("Global", config.SourceGlobal, layered, filters, width, height),
		NewLayerPage("Ecosystem", config.SourceEcosystem, layered, filters, width, height),
		NewLayerPage("Notebook", config.SourceProjectNotebook, layered, filters, width, height),
		NewLayerPage("Project", config.SourceProject, layered, filters, width, height),
	}

	// Build pager.Page slice from the typed pages
	pages := make([]pager.Page, len(layerPages))
	for i, lp := range layerPages {
		pages[i] = lp
	}

	pagerKeys := pager.KeyMap{
		Tab1:    keys.Base.Tab1,
		Tab2:    keys.Base.Tab2,
		Tab3:    keys.Base.Tab3,
		Tab4:    keys.Base.Tab4,
		Tab5:    keys.Base.Tab5,
		Tab6:    keys.Base.Tab6,
		Tab7:    keys.Base.Tab7,
		Tab8:    keys.Base.Tab8,
		Tab9:    keys.Base.Tab9,
		NextTab: keys.NextPage,
		PrevTab: keys.PrevPage,
	}

	pagerCfg := pager.Config{
		OuterPadding: [4]int{1, 2, 0, 2},
		ShowTitleRow: true,
		FooterHeight: 2, // status + help line
	}

	pgr := pager.NewWith(pages, pagerKeys, pagerCfg)

	ti := textinput.New()
	ti.Prompt = "  > "
	ti.CharLimit = 200
	ti.Width = 50

	m := Model{
		pager:       pgr,
		layerPages:  layerPages,
		input:       ti,
		layered:     layered,
		yamlHandler: yamlHandler,
		tomlHandler: tomlHandler,
		filters:     filters,
		keys:        keys,
		help:        help.NewBuilder().WithKeys(keys).WithTitle("Configuration Editor").Build(),
		state:       viewList,
	}

	// Focus the first page
	layerPages[0].Focus()

	return m
}

func (m Model) Init() tea.Cmd {
	return m.pager.Init()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.Width = msg.Width
		m.help.Height = msg.Height
		m.pager, cmd = m.pager.Update(msg)
		return m, cmd

	case embed.FocusMsg:
		m.pager, cmd = m.pager.Update(msg)
		return m, cmd

	case embed.BlurMsg:
		m.pager, cmd = m.pager.Update(msg)
		return m, cmd

	case embed.SetWorkspaceMsg:
		// Reload config for the new workspace
		if msg.Node != nil {
			m.workspacePath = msg.Node.Path
			layered, err := config.LoadLayered(msg.Node.Path)
			if err == nil {
				m.layered = layered
				m.refreshAllPages()
			}
		}
		return m, nil

	case embed.EditFinishedMsg:
		// Reload config after editor changes
		if err := m.reloadConfig(); err != nil {
			m.statusMsg = fmt.Sprintf("Error reloading: %v", err)
		}
		return m, nil

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

	// Delegate non-key messages to pager when in list view
	if m.state == viewList {
		m.pager, cmd = m.pager.Update(msg)
	}

	return m, cmd
}

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Quit -> emit CloseRequestMsg instead of tea.Quit
	if key.Matches(msg, m.keys.Base.Quit) {
		return m, func() tea.Msg { return embed.CloseRequestMsg{} }
	}

	if key.Matches(msg, m.keys.Base.Help) {
		m.help.Toggle()
		return m, nil
	}

	// Toggle preview mode
	if key.Matches(msg, m.keys.Preview) {
		m.filters.ShowPreview = !m.filters.ShowPreview
		m.saveUIState()
		m.refreshAllPages()
		return m, nil
	}

	// Toggle view mode (configured/all)
	if key.Matches(msg, m.keys.ViewMode) {
		m.filters.ViewMode = configui.CycleViewMode(m.filters.ViewMode)
		m.saveUIState()
		m.refreshAllPages()
		return m, nil
	}

	// Cycle maturity filter
	if key.Matches(msg, m.keys.MaturityFilter) {
		m.filters.MaturityFilter = configui.CycleMaturityFilter(m.filters.MaturityFilter)
		m.saveUIState()
		m.refreshAllPages()
		return m, nil
	}

	// Cycle sort mode
	if key.Matches(msg, m.keys.SortMode) {
		m.filters.SortMode = configui.CycleSortMode(m.filters.SortMode)
		m.saveUIState()
		m.refreshAllPages()
		return m, nil
	}

	// Show config sources - but don't intercept if z-chord is pending
	if key.Matches(msg, m.keys.Sources) {
		activePage := m.activeLayerPage()
		if activePage == nil || !activePage.IsZChordPending() {
			m.state = viewSources
			return m, nil
		}
		// Let it fall through to pager delegation
	}

	// Shift+M: cycle maturity filter backward
	if msg.String() == "M" {
		m.filters.MaturityFilter = configui.CycleMaturityFilterReverse(m.filters.MaturityFilter)
		m.saveUIState()
		m.refreshAllPages()
		return m, nil
	}

	// Shift+S: cycle sort mode backward
	if msg.String() == "S" {
		m.filters.SortMode = configui.CycleSortModeReverse(m.filters.SortMode)
		m.saveUIState()
		m.refreshAllPages()
		return m, nil
	}

	// Delegate to pager (handles tab switching + active page Update)
	var cmd tea.Cmd
	m.pager, cmd = m.pager.Update(msg)
	return m, cmd
}

func (m Model) updateInfo(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Cancel) || key.Matches(msg, m.keys.Base.Quit) {
		m.state = viewList
		return m, nil
	}
	if key.Matches(msg, m.keys.Edit) {
		if m.editNode != nil {
			m.startEditFromNode(m.editNode)
		}
		return m, nil
	}
	return m, nil
}

func (m Model) updateSources(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Cancel) || key.Matches(msg, m.keys.Base.Quit) || key.Matches(msg, m.keys.Sources) {
		m.state = viewList
		return m, nil
	}
	return m, nil
}

// startEditFromNode starts editing a node.
func (m *Model) startEditFromNode(node *configui.ConfigNode) {
	if node.IsContainer() {
		m.statusMsg = "Expand node to edit children"
		return
	}

	m.state = viewEdit
	m.editNode = node
	m.statusMsg = ""

	m.targetLayer = node.Field.Layer
	if m.targetLayer == config.SourceDefault {
		m.targetLayer = config.SourceGlobal
	}

	if node.ActiveSource != config.SourceDefault {
		m.targetLayer = node.ActiveSource
	}

	currentValue := configui.FormatValue(node.Value)
	if currentValue == "(unset)" || currentValue == "(empty)" {
		currentValue = ""
	}

	switch node.Field.Type {
	case configui.FieldString:
		m.input.SetValue(currentValue)
		m.input.Focus()
	case configui.FieldSelect:
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
		m.input.SetValue(currentValue)
		m.input.Focus()
	default:
		m.input.SetValue(currentValue)
		m.input.Focus()
	}
}

func (m *Model) updateEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.editNode == nil {
		m.state = viewList
		return m, nil
	}
	node := m.editNode

	path := node.Field.Path
	if node.Field.Namespace != "" {
		path = append([]string{node.Field.Namespace}, node.Field.Path...)
	}

	if node.IsDynamic && node.Parent != nil {
		path = buildFullPath(node)
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.state = viewList
		m.input.Blur()
		return m, nil

	case tea.KeyTab:
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

		if err := m.saveToLayer(path, newValue, m.targetLayer); err != nil {
			m.statusMsg = fmt.Sprintf("Error: %v", err)
			return m, nil
		}

		if err := m.reloadConfig(); err != nil {
			m.statusMsg = fmt.Sprintf("Error reloading: %v", err)
			return m, nil
		}

		m.state = viewList
		m.input.Blur()
		m.statusMsg = fmt.Sprintf("Saved to %s!", LayerDisplayName(m.targetLayer))
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
	current := node
	for current != nil {
		if current.Key != "" {
			path = append([]string{current.Key}, path...)
		} else if len(current.Field.Path) > 0 {
			path = append(current.Field.Path, path...)
		}
		current = current.Parent
	}
	if node.Field.Namespace != "" {
		path = append([]string{node.Field.Namespace}, path...)
	}
	return path
}

// cycleLayer cycles through available layers.
func (m *Model) cycleLayer(current config.ConfigSource) config.ConfigSource {
	layers := m.availableLayers()
	if len(layers) == 0 {
		return config.SourceGlobal
	}

	currentIdx := 0
	for i, layer := range layers {
		if layer == current {
			currentIdx = i
			break
		}
	}

	nextIdx := (currentIdx + 1) % len(layers)
	return layers[nextIdx]
}

// availableLayers returns the layers available for saving.
func (m *Model) availableLayers() []config.ConfigSource {
	layers := []config.ConfigSource{config.SourceGlobal}

	if m.layered.FilePaths[config.SourceEcosystem] != "" {
		layers = append(layers, config.SourceEcosystem)
	}

	if m.layered.FilePaths[config.SourceProject] != "" {
		layers = append(layers, config.SourceProject)
	}

	return layers
}

// saveToLayer saves a value to the specified layer's config file.
func (m *Model) saveToLayer(path []string, value string, layer config.ConfigSource) error {
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

	ext := strings.ToLower(filepath.Ext(targetPath))
	if ext == ".toml" {
		return m.saveToTOML(targetPath, path, value)
	}
	return m.saveToYAML(targetPath, path, value)
}

func (m *Model) saveToTOML(filePath string, path []string, value string) error {
	data, err := m.tomlHandler.LoadTOML(filePath)
	if err != nil {
		return err
	}

	setTomlValue(data, value, path...)
	return m.tomlHandler.SaveTOML(filePath, data)
}

func (m *Model) saveToYAML(filePath string, path []string, value string) error {
	root, err := m.yamlHandler.LoadYAML(filePath)
	if err != nil {
		return err
	}

	if err := setup.SetValue(root, value, path...); err != nil {
		return err
	}

	return m.yamlHandler.SaveYAML(filePath, root)
}

// setTomlValue sets a value in a nested TOML map using a path.
func setTomlValue(data map[string]interface{}, value string, path ...string) {
	if len(path) == 0 {
		return
	}

	current := data
	for i, key := range path {
		if i == len(path)-1 {
			current[key] = value
			return
		}

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

// saveUIState saves the current UI state to disk.
func (m *Model) saveUIState() {
	saveUIStateToDisk(uiState{
		ShowPreview:    m.filters.ShowPreview,
		ViewMode:       m.filters.ViewMode,
		MaturityFilter: m.filters.MaturityFilter,
		SortMode:       m.filters.SortMode,
	})
}

// refreshAllPages refreshes all pages to apply new filters/sorting.
func (m *Model) refreshAllPages() {
	for _, page := range m.layerPages {
		page.Refresh(m.layered)
	}
}

// reloadConfig reloads the layered config and refreshes all pages.
// Prefers the host-tracked workspace path (set via embed.SetWorkspaceMsg)
// and falls back to os.Getwd() for standalone CLI use.
func (m *Model) reloadConfig() error {
	root := m.workspacePath
	if root == "" {
		root, _ = os.Getwd()
	}
	layered, err := config.LoadLayered(root)
	if err != nil {
		return err
	}
	m.layered = layered
	m.refreshAllPages()
	return nil
}

// activeLayerPage returns the active LayerPage (typed), or nil.
func (m *Model) activeLayerPage() *LayerPage {
	idx := m.pager.ActiveIndex()
	if idx >= 0 && idx < len(m.layerPages) {
		return m.layerPages[idx]
	}
	return nil
}

// GetLayerFilePath returns the file path for a given layer.
func (m Model) GetLayerFilePath(layer config.ConfigSource) string {
	if path, ok := m.layered.FilePaths[layer]; ok && path != "" {
		return path
	}
	if layer == config.SourceGlobal {
		return setup.GlobalTOMLConfigPath()
	}
	return ""
}
