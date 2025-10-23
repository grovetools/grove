package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-core/config"
	"github.com/mattsolo1/grove-core/tui/components/help"
	"github.com/mattsolo1/grove-core/tui/keymap"
	"github.com/mattsolo1/grove-core/tui/theme"
	"gopkg.in/yaml.v3"
)

// configItem represents a single configuration key-value pair
type configItem struct {
	key        string
	value      interface{}
	source     config.ConfigSource
	sourcePath string
	group      string // Top-level group (e.g., "agent", "settings", "extensions")
}

// Implement list.Item interface
func (i configItem) Title() string {
	// Config keys use bold for emphasis without explicit color
	return theme.DefaultTheme.Bold.Render(i.key)
}

func (i configItem) Description() string {
	sourceStyle := getSourceStyle(i.source)

	// Build source description
	var sourceName string
	if i.sourcePath != "" {
		cwd, _ := os.Getwd()
		relPath, err := filepath.Rel(cwd, i.sourcePath)
		if err == nil && !strings.HasPrefix(relPath, "..") {
			sourceName = relPath
		} else {
			sourceName = i.sourcePath
		}
	} else {
		sourceName = string(i.source)
	}

	return sourceStyle.Render(sourceName)
}

func (i configItem) FilterValue() string {
	return i.key
}

// FormatDetails returns a formatted string of the config item details for the viewport
func (i configItem) FormatDetails() string {
	var lines []string

	// Header
	headerStyle := theme.DefaultTheme.Header
	lines = append(lines, headerStyle.Render("Configuration Details"))
	lines = append(lines, "")

	// Key info
	sourceStyle := getSourceStyle(i.source)

	lines = append(lines, fmt.Sprintf("Key:        %s", theme.DefaultTheme.Bold.Render(i.key)))
	lines = append(lines, fmt.Sprintf("Group:      %s", theme.DefaultTheme.Muted.Render(i.group)))

	sourceName := string(i.source)
	if i.sourcePath != "" {
		cwd, _ := os.Getwd()
		relPath, err := filepath.Rel(cwd, i.sourcePath)
		if err == nil && !strings.HasPrefix(relPath, "..") {
			sourceName = fmt.Sprintf("%s (%s)", i.source, relPath)
		} else {
			sourceName = fmt.Sprintf("%s (%s)", i.source, i.sourcePath)
		}
	}
	lines = append(lines, fmt.Sprintf("Source:     %s", sourceStyle.Render(sourceName)))
	lines = append(lines, "")

	// Value section
	borderStyle := theme.DefaultTheme.Muted
	lines = append(lines, borderStyle.Render("┌─ Value:"))

	// Format the value
	valueStr := formatValueDetailed(i.value)
	for _, line := range strings.Split(valueStr, "\n") {
		lines = append(lines, borderStyle.Render("│ ")+line)
	}
	lines = append(lines, borderStyle.Render("└─"))

	return strings.Join(lines, "\n")
}

// getSourceStyle returns the appropriate style for a config source
func getSourceStyle(source config.ConfigSource) lipgloss.Style {
	switch source {
	case config.SourceOverride:
		return theme.DefaultTheme.Success
	case config.SourceProject:
		return lipgloss.NewStyle()
	case config.SourceGlobal:
		return theme.DefaultTheme.Muted
	case config.SourceDefault:
		return theme.DefaultTheme.Muted
	default:
		return theme.DefaultTheme.Error
	}
}

// formatValueCompact returns a compact string representation of a value
func formatValueCompact(value interface{}) string {
	if value == nil {
		return theme.DefaultTheme.Muted.Render("<not set>")
	}

	valueStr := fmt.Sprintf("%v", value)
	if valueStr == "" || valueStr == "[]" || valueStr == "map[]" {
		return theme.DefaultTheme.Muted.Render("<empty>")
	}
	return valueStr
}

// formatValueDetailed returns a detailed string representation of a value
func formatValueDetailed(value interface{}) string {
	if value == nil {
		return theme.DefaultTheme.Muted.Render("<not set>")
	}

	// Try to format as YAML for complex types
	switch v := value.(type) {
	case string:
		if v == "" {
			return theme.DefaultTheme.Muted.Render("<empty>")
		}
		return v
	case []string:
		if len(v) == 0 {
			return theme.DefaultTheme.Muted.Render("<empty>")
		}
		return strings.Join(v, "\n")
	case map[string]interface{}:
		if len(v) == 0 {
			return theme.DefaultTheme.Muted.Render("<empty>")
		}
		yamlBytes, err := yaml.Marshal(v)
		if err == nil {
			return strings.TrimSpace(string(yamlBytes))
		}
		return fmt.Sprintf("%v", v)
	default:
		yamlBytes, err := yaml.Marshal(v)
		if err == nil {
			return strings.TrimSpace(string(yamlBytes))
		}
		return fmt.Sprintf("%v", v)
	}
}

// Custom item delegate for rendering
type configItemDelegate struct{}

func (d configItemDelegate) Height() int                              { return 2 }
func (d configItemDelegate) Spacing() int                             { return 0 }
func (d configItemDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d configItemDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	i, ok := item.(configItem)
	if !ok {
		return
	}

	title := i.Title()
	desc := i.Description()

	// Apply highlighting if selected
	if index == m.Index() {
		title = theme.DefaultTheme.Selected.Render(title)
		desc = theme.DefaultTheme.Selected.Copy().Faint(true).Render(desc)
	}

	fmt.Fprintf(w, "%s\n%s", title, desc)
}

// keyMap defines all key bindings for the TUI
type configKeyMap struct {
	keymap.Base
	GotoTop  key.Binding
	GotoEnd  key.Binding
	HalfUp   key.Binding
	HalfDown key.Binding
	Search   key.Binding
	Clear    key.Binding
}

var configKeys = configKeyMap{
	Base: keymap.NewBase(),
	GotoTop: key.NewBinding(
		key.WithKeys("g"),
		key.WithHelp("gg", "go to top"),
	),
	GotoEnd: key.NewBinding(
		key.WithKeys("G"),
		key.WithHelp("G", "go to end"),
	),
	HalfUp: key.NewBinding(
		key.WithKeys("ctrl+u"),
		key.WithHelp("ctrl+u", "half page up"),
	),
	HalfDown: key.NewBinding(
		key.WithKeys("ctrl+d"),
		key.WithHelp("ctrl+d", "half page down"),
	),
	Search: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "search"),
	),
	Clear: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "clear search"),
	),
}

// ShortHelp returns a short help text for display in the status line
func (k configKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Base.Help, k.Base.Quit}
}

// FullHelp returns the full help keybindings
func (k configKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Base.Up, k.Base.Down, k.GotoTop, k.GotoEnd},
		{k.HalfUp, k.HalfDown, k.Search, k.Clear},
		{k.Base.Help, k.Base.Quit},
	}
}

// Main TUI model
type configModel struct {
	list       list.Model
	items      []configItem
	keys       configKeyMap
	viewport   viewport.Model
	help       help.Model
	width      int
	height     int
	ready      bool
	lastGotoG  time.Time
}

func (m *configModel) Init() tea.Cmd {
	return nil
}

func (m *configModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Don't intercept keys when filtering is active
		if m.list.FilterState() == list.Filtering {
			switch {
			case key.Matches(msg, configKeys.Base.Quit):
				return m, tea.Quit
			case key.Matches(msg, configKeys.Clear):
				m.list.ResetFilter()
				return m, nil
			}
		} else {
			// Handle double 'g' for goto top
			if msg.String() == "g" {
				if time.Since(m.lastGotoG) < 500*time.Millisecond {
					m.list.Select(0)
					m.lastGotoG = time.Time{}
					return m, nil
				}
				m.lastGotoG = time.Now()
				return m, nil
			}

			switch {
			case key.Matches(msg, configKeys.Base.Quit):
				return m, tea.Quit

			case key.Matches(msg, configKeys.Base.Help):
				m.help.ShowAll = !m.help.ShowAll
				return m, nil

			case key.Matches(msg, configKeys.GotoEnd):
				m.list.Select(len(m.items) - 1)
				return m, nil

			case key.Matches(msg, configKeys.HalfUp):
				visibleHeight := m.height - 4
				halfPage := visibleHeight / 4 // Divide by 4 because each item is 2 lines
				currentIndex := m.list.Index()
				newIndex := currentIndex - halfPage
				if newIndex < 0 {
					newIndex = 0
				}
				m.list.Select(newIndex)
				return m, nil

			case key.Matches(msg, configKeys.HalfDown):
				visibleHeight := m.height - 4
				halfPage := visibleHeight / 4
				currentIndex := m.list.Index()
				newIndex := currentIndex + halfPage
				if newIndex >= len(m.items) {
					newIndex = len(m.items) - 1
				}
				m.list.Select(newIndex)
				return m, nil
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Split the view evenly: 50/50
		listWidth := msg.Width / 2
		viewportWidth := msg.Width - listWidth

		// Update list size (full height minus status line)
		m.list.SetSize(listWidth, msg.Height-2)

		// Update viewport size (account for border padding and rounded border)
		if !m.ready {
			m.viewport = viewport.New(viewportWidth-8, msg.Height-6) // -8 for border, padding, and spacing
			m.ready = true
		} else {
			m.viewport.Width = viewportWidth - 8
			m.viewport.Height = msg.Height - 6
		}

		// Update content in viewport if we have a selected item
		if selectedItem := m.list.SelectedItem(); selectedItem != nil {
			if item, ok := selectedItem.(configItem); ok {
				m.viewport.SetContent(item.FormatDetails())
			}
		}

		return m, nil
	}

	// Update the list
	prevIndex := m.list.Index()
	newListModel, cmd := m.list.Update(msg)
	m.list = newListModel
	cmds = append(cmds, cmd)

	// Update viewport content if selection changed
	if m.list.Index() != prevIndex {
		if selectedItem := m.list.SelectedItem(); selectedItem != nil {
			if item, ok := selectedItem.(configItem); ok {
				m.viewport.SetContent(item.FormatDetails())
				m.viewport.GotoTop()
			}
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *configModel) View() string {
	if m.help.ShowAll {
		return m.help.View()
	}

	// If not ready, show loading
	if !m.ready {
		return "Initializing..."
	}

	// Main list view
	listView := m.list.View()

	// Details view with border
	detailsStyle := theme.DefaultTheme.Muted.Copy().
		Padding(1, 2).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Muted.GetForeground()).
		Height(m.height - 4)

	detailsView := detailsStyle.Render(m.viewport.View())

	// Combine list and details side by side
	mainView := lipgloss.JoinHorizontal(
		lipgloss.Top,
		listView,
		detailsView,
	)

	// Status line
	statusStyle := theme.DefaultTheme.Muted

	filterIndicator := ""
	searchStyle := theme.DefaultTheme.Warning.Copy().Bold(true)
	if m.list.FilterState() == list.Filtering {
		filterTerm := m.list.FilterValue()
		if filterTerm == "" {
			filterIndicator = " [SEARCHING: type to filter]"
		} else {
			filterIndicator = fmt.Sprintf(" [SEARCHING: %s]", searchStyle.Render(filterTerm))
		}
	} else if m.list.FilterState() == list.FilterApplied {
		filterTerm := m.list.FilterValue()
		filterIndicator = fmt.Sprintf(" [FILTERED: %s]", searchStyle.Render(filterTerm))
	}

	// Show current position in status
	visibleItems := len(m.list.VisibleItems())
	currentIndex := m.list.Index()

	if currentIndex < 0 {
		currentIndex = 0
	}

	var position string
	if visibleItems == 0 {
		position = "0/0"
	} else {
		position = fmt.Sprintf("%d/%d", currentIndex+1, visibleItems)
		if m.list.FilterState() != list.Unfiltered && visibleItems < len(m.items) {
			position = fmt.Sprintf("%d/%d (of %d)", currentIndex+1, visibleItems, len(m.items))
		}
	}

	status := statusStyle.Render(fmt.Sprintf(" Config: %s%s | ? for help | q to quit",
		position, filterIndicator))

	// Combine main view and status
	return lipgloss.JoinVertical(
		lipgloss.Left,
		mainView,
		status,
	)
}

// runConfigAnalyzeTUI runs the TUI version of config analyze
func runConfigAnalyzeTUI() error {
	// Load the layered configuration
	layeredCfg, err := config.LoadLayered(".")
	if err != nil {
		return fmt.Errorf("failed to load layered configuration: %w", err)
	}

	// Analyze the layers
	analysis := analyzeLayers(layeredCfg)

	// Convert to config items
	var items []configItem
	for key, source := range analysis {
		group := strings.Split(key, ".")[0]
		items = append(items, configItem{
			key:        key,
			value:      source.Value,
			source:     source.Source,
			sourcePath: source.Path,
			group:      group,
		})
	}

	// Sort items by key
	sort.Slice(items, func(i, j int) bool {
		return items[i].key < items[j].key
	})

	// Convert to list items
	listItems := make([]list.Item, len(items))
	for i := range items {
		listItems[i] = items[i]
	}

	// Create list
	l := list.New(listItems, configItemDelegate{}, 0, 0)
	l.Title = "Grove Configuration Analysis"
	l.SetShowStatusBar(false)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetShowPagination(true)
	l.InfiniteScrolling = false
	l.DisableQuitKeybindings()

	// Configure pagination style
	l.Styles.PaginationStyle = theme.DefaultTheme.Muted.Copy().PaddingLeft(2)

	// Initialize model
	model := &configModel{
		list:  l,
		items: items,
		keys:  configKeys,
		help:  help.New(configKeys),
		ready: false,
	}

	// Run the TUI
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("error running TUI: %w", err)
	}

	return nil
}
