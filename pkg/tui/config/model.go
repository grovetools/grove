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
	viewConfirmDelete
)

// uiState holds persisted UI preferences for the config editor.
type uiState struct {
	ShowPreview    bool                    `json:"show_preview"`
	ViewMode       configui.ViewMode       `json:"view_mode"`
	MaturityFilter configui.MaturityFilter `json:"maturity_filter"`
	SortMode       configui.SortMode       `json:"sort_mode"`
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
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return
	}

	_ = os.WriteFile(uiStateFile(), data, 0o600)
}

// Model is the embeddable config TUI model. It wraps a pager.Model for
// tab management and handles overlay states (edit, info, sources) on top.
type Model struct {
	pager pager.Model

	// dataPage is the single Data tab (last): one LayerPage retargeted
	// across the four config layers by the L cycle key. Resolve the
	// active layer page via activeLayerPage(), never by pager index.
	dataPage *DataPage

	// themesPage is the bespoke theme-gallery page (4th tab).
	themesPage *ThemesPage

	// curatedPages tracks the CuratedPage tabs so refreshAllPages can
	// re-point them at a reloaded config. Empty while Appearance/Layout/
	// Keys are stubs; Phases 3–5 register their real pages here.
	curatedPages []*CuratedPage

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

	// Delete-confirm state (viewConfirmDelete)
	deleteNode  *configui.ConfigNode
	deletePath  []string
	deleteLayer config.ConfigSource
	deleteFile  string

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

	// Curated pages (Appearance/Layout/Keys) are stubs until Phase 2 of
	// the curated-config plan lands; Themes is the existing gallery; Data
	// is the raw per-layer tree collapsed into one tab with a layer cycler.
	appearancePage := newStubPage("Appearance", "appearance", "Appearance settings coming soon", width, height)
	layoutPage := newStubPage("Layout", "layout", "Layout settings coming soon", width, height)
	keysPage := newStubPage("Keys", "keys", "Key settings coming soon", width, height)
	themesPage := NewThemesPage(layered, keys, width, height)
	dataPage := NewDataPage(layered, filters, keys, width, height)

	pages := []pager.Page{appearancePage, layoutPage, keysPage, themesPage, dataPage}

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
		dataPage:    dataPage,
		themesPage:  themesPage,
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
	pages[0].Focus()

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

	case applyThemeMsg:
		// Persist the selected theme to the global layer. tui.theme is
		// x-layer=global, so the Themes page always saves there.
		if err := m.saveToLayer([]string{"tui", "theme"}, msg.name, config.SourceGlobal); err != nil {
			m.statusMsg = fmt.Sprintf("Error: %v", err)
			return m, nil
		}
		if m.themesPage != nil {
			m.themesPage.MarkSaved(msg.name)
		}
		if err := m.reloadConfig(); err != nil {
			m.statusMsg = fmt.Sprintf("Error reloading: %v", err)
			return m, nil
		}
		m.statusMsg = fmt.Sprintf("Theme %q saved to %s!", msg.name, LayerDisplayName(config.SourceGlobal))
		return m, nil

	case setSettingMsg:
		// Persist a curated setting to the global layer — the applyThemeMsg
		// pattern generalized. Writes are TYPED per the setting's ControlKind
		// (a bool/int written as a quoted string fails core's strict TOML
		// decode and silently drops the whole global config on next load).
		typed, err := msg.setting.TypedValue(msg.value)
		if err != nil {
			m.statusMsg = fmt.Sprintf("Error: %v", err)
			return m, nil
		}
		if err := SaveGlobalSetting(m.tomlHandler, m.yamlHandler, m.layered, msg.setting.Path, typed); err != nil {
			m.statusMsg = fmt.Sprintf("Error: %v", err)
			return m, nil
		}
		if err := m.reloadConfig(); err != nil {
			m.statusMsg = fmt.Sprintf("Error reloading: %v", err)
			return m, nil
		}
		m.statusMsg = fmt.Sprintf("%s saved to %s!", msg.setting.Label, LayerDisplayName(config.SourceGlobal))
		// Notify the host (treemux) so it can hot-apply the change via its
		// setters. Standalone `grove config` has no handler — the message is
		// inert there. Settings without an ApplyDomain (startup-only) skip it.
		if domain := msg.setting.ApplyDomain; domain != "" {
			final := m.layered.Final
			return m, func() tea.Msg {
				return embed.SettingAppliedMsg{Domain: domain, Config: final}
			}
		}
		return m, nil

	case deleteNodeMsg:
		m.startDeleteFromNode(msg.node)
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
		case viewConfirmDelete:
			return m.updateConfirmDelete(msg)
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
	// Quit -> emit CloseRequestMsg instead of tea.Quit. When embedded the
	// host process keeps running, so drop any live theme preview first.
	if key.Matches(msg, m.keys.Base.Quit) {
		if m.themesPage != nil {
			m.themesPage.RevertPreview()
		}
		return m, func() tea.Msg { return embed.CloseRequestMsg{} }
	}

	if key.Matches(msg, m.keys.Base.Help) {
		m.help.Toggle()
		return m, nil
	}

	// Filter/sort/preview toggles only apply to LayerPages. On other pages
	// (e.g. Themes) let the keys fall through to the page itself.
	onLayerPage := m.activeLayerPage() != nil
	if onLayerPage {
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

		// Shift+M: cycle maturity filter backward — but don't intercept when a
		// z-chord is pending, so "zM" (collapse-all) reaches the page instead
		// of being shadowed by maturity-backward.
		if key.Matches(msg, m.keys.MaturityFilterBack) {
			activePage := m.activeLayerPage()
			if activePage == nil || !activePage.IsZChordPending() {
				m.filters.MaturityFilter = configui.CycleMaturityFilterReverse(m.filters.MaturityFilter)
				m.saveUIState()
				m.refreshAllPages()
				return m, nil
			}
			// z-chord pending: fall through to the pager so the page sees "M".
		}

		// Shift+S: cycle sort mode backward — same z-chord guard as above.
		if key.Matches(msg, m.keys.SortModeBack) {
			activePage := m.activeLayerPage()
			if activePage == nil || !activePage.IsZChordPending() {
				m.filters.SortMode = configui.CycleSortModeReverse(m.filters.SortMode)
				m.saveUIState()
				m.refreshAllPages()
				return m, nil
			}
			// z-chord pending: fall through to the pager so the page sees "S".
		}
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

func (m Model) updateEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

	// Route the modal control keys through the keymap. All three are
	// non-printable, so the textinput fall-through below never swallows a
	// typed character.
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.state = viewList
		m.input.Blur()
		return m, nil

	case key.Matches(msg, m.keys.SwitchLayer):
		m.targetLayer = m.cycleLayer(m.targetLayer)
		return m, nil

	case key.Matches(msg, m.keys.Confirm):
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

// startDeleteFromNode prepares the delete-confirm overlay for a node.
// Audit rows ("keys nothing reads") carry their own layer/file provenance;
// schema rows delete from the layer that currently provides the value.
func (m *Model) startDeleteFromNode(node *configui.ConfigNode) {
	m.statusMsg = ""

	if node.IsAuditSection() {
		return
	}

	if node.Audit != nil {
		if strings.Contains(node.Audit.Key, "[") {
			m.statusMsg = "Cannot delete array elements"
			return
		}
		m.deleteNode = node
		m.deletePath = strings.Split(node.Audit.Key, ".")
		m.deleteLayer = node.Audit.Layer
		m.deleteFile = node.Audit.File
		m.state = viewConfirmDelete
		return
	}

	path := node.Field.Path
	if node.Field.Namespace != "" {
		path = append([]string{node.Field.Namespace}, node.Field.Path...)
	}
	if node.IsDynamic && node.Parent != nil {
		path = buildFullPath(node)
	}
	if len(path) == 0 {
		return
	}

	layer := node.ActiveSource
	if layer == "" || layer == config.SourceDefault {
		m.statusMsg = "Not set in any config file"
		return
	}

	file := m.layered.FilePaths[layer]
	if file == "" && layer == config.SourceGlobal {
		file = setup.GlobalTOMLConfigPath()
	}
	if file == "" {
		m.statusMsg = fmt.Sprintf("No config file for %s layer", LayerDisplayName(layer))
		return
	}

	m.deleteNode = node
	m.deletePath = path
	m.deleteLayer = layer
	m.deleteFile = file
	m.state = viewConfirmDelete
}

// updateConfirmDelete handles keys in the delete-confirm overlay.
// NOTE: value receiver, like every Update path on Model — host panels
// (treemux) assert the value type after each update, so the modified copy
// must be returned (see updateEdit / commit 6a315e4).
func (m Model) updateConfirmDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Cancel) || key.Matches(msg, m.keys.Base.Quit) {
		m.state = viewList
		return m, nil
	}

	if key.Matches(msg, m.keys.Confirm) {
		keyName := strings.Join(m.deletePath, ".")
		layer := m.deleteLayer
		m.state = viewList

		if err := m.deleteFromLayer(m.deleteFile, m.deletePath); err != nil {
			m.statusMsg = fmt.Sprintf("Error: %v", err)
			return m, nil
		}
		if err := m.reloadConfig(); err != nil {
			m.statusMsg = fmt.Sprintf("Error reloading: %v", err)
			return m, nil
		}
		m.statusMsg = fmt.Sprintf("Deleted %s from %s", keyName, LayerDisplayName(layer))
		return m, nil
	}

	return m, nil
}

// deleteFromLayer removes a key path from one layer's config file — the
// delete counterpart of saveToLayer. It takes the resolved file path
// directly (rather than a ConfigSource) because audit rows carry exact file
// provenance, including fragment/override files that FilePaths does not map.
func (m *Model) deleteFromLayer(filePath string, path []string) error {
	if filePath == "" {
		return fmt.Errorf("no config file to delete from")
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == ".toml" {
		data, err := m.tomlHandler.LoadTOML(filePath)
		if err != nil {
			return err
		}
		if !setup.DeleteTOMLValue(data, path...) {
			return fmt.Errorf("key %s not found in %s", strings.Join(path, "."), filePath)
		}
		return m.tomlHandler.SaveTOML(filePath, data)
	}

	root, err := m.yamlHandler.LoadYAML(filePath)
	if err != nil {
		return err
	}
	if !setup.DeleteValue(root, path...) {
		return fmt.Errorf("key %s not found in %s", strings.Join(path, "."), filePath)
	}
	return m.yamlHandler.SaveYAML(filePath, root)
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

// SaveGlobalSetting writes a single value at path into the global grove
// config file (the layer every curated setting saves to). value must
// already be Go-typed — bool/int/string per the setting's ControlKind (see
// Setting.TypedValue) — because TOML values are typed: a bool written as a
// quoted "true" fails core's strict decode and the loader silently drops
// the ENTIRE global config (see treemux/cmd/welcome_prefs.go for the
// documented hazard and the typed-write precedent).
//
// Exported so spec 23's onboarding can persist essentials directly without
// constructing a full Model; Model.saveToLayer routes its global-layer case
// through it.
func SaveGlobalSetting(tomlHandler *setup.TOMLHandler, yamlHandler *setup.YAMLHandler, layered *config.LayeredConfig, path []string, value interface{}) error {
	targetPath := ""
	if layered != nil && layered.FilePaths != nil {
		targetPath = layered.FilePaths[config.SourceGlobal]
	}
	if targetPath == "" {
		targetPath = setup.GlobalTOMLConfigPath()
	}
	if targetPath == "" {
		return fmt.Errorf("cannot resolve global config path")
	}

	if strings.ToLower(filepath.Ext(targetPath)) != ".toml" {
		// Legacy grove.yml global config: the YAML node writer is already
		// type-aware (createValueNode tags bool/int scalars).
		root, err := yamlHandler.LoadYAML(targetPath)
		if err != nil {
			return err
		}
		if err := setup.SetValue(root, value, path...); err != nil {
			return err
		}
		return yamlHandler.SaveYAML(targetPath, root)
	}

	data, err := tomlHandler.LoadTOML(targetPath)
	if err != nil {
		return err
	}
	setTomlValue(data, value, path...)
	return tomlHandler.SaveTOML(targetPath, data)
}

// saveToLayer saves a value to the specified layer's config file.
func (m *Model) saveToLayer(path []string, value string, layer config.ConfigSource) error {
	var targetPath string
	switch layer {
	case config.SourceGlobal:
		return SaveGlobalSetting(m.tomlHandler, m.yamlHandler, m.layered, path, value)
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

// setTomlValue sets a value in a nested TOML map using a path. The value is
// stored as-is, so callers with typed data (curated settings) pass Go
// bool/int values while the raw tree editor passes strings.
func setTomlValue(data map[string]interface{}, value interface{}, path ...string) {
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

// refreshAllPages refreshes all pages to apply new filters/sorting and the
// reloaded config. It intentionally uses the typed page references (not
// the pager's index space) so non-LayerPage tabs are handled explicitly.
// The curated stub pages have no config-derived content to refresh.
func (m *Model) refreshAllPages() {
	if m.dataPage != nil {
		m.dataPage.Refresh(m.layered)
	}
	if m.themesPage != nil {
		m.themesPage.Refresh(m.layered)
	}
	for _, cp := range m.curatedPages {
		cp.Refresh(m.layered)
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

// activeLayerPage returns the LayerPage backing the active tab, or nil when
// the active tab has no layer page (stubs, Themes). The Data tab wraps its
// LayerPage in a DataPage, so the assertion unwraps it; a bare *LayerPage
// is also handled in case one is ever registered directly.
func (m *Model) activeLayerPage() *LayerPage {
	switch p := m.pager.Active().(type) {
	case *DataPage:
		return p.inner
	case *LayerPage:
		return p
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
