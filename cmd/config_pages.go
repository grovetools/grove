package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/configui"
	"github.com/grovetools/grove/pkg/setup"
)

// Messages to communicate from Page to Main Model
type editNodeMsg struct{ node *configui.ConfigNode }
type infoNodeMsg struct{ node *configui.ConfigNode }

// LayerPage implements configui.ConfigPage for a specific config layer.
type LayerPage struct {
	layer     config.ConfigSource
	name      string
	list      list.Model
	treeRoots []*configui.ConfigNode
	config    *config.LayeredConfig
	width     int
	height    int
	active    bool
}

// NewLayerPage creates a page for a specific layer.
func NewLayerPage(name string, layer config.ConfigSource, layered *config.LayeredConfig, width, height int) *LayerPage {
	p := &LayerPage{
		layer:  layer,
		name:   name,
		config: layered,
		width:  width,
		height: height,
	}

	// Initialize list
	delegate := configDelegate{}
	p.list = list.New([]list.Item{}, delegate, width, height)
	p.list.SetShowTitle(false)
	p.list.SetShowStatusBar(false)
	p.list.SetShowHelp(false)
	p.list.SetShowPagination(false)
	p.list.SetFilteringEnabled(false)
	p.list.DisableQuitKeybindings()
	p.list.InfiniteScrolling = true

	p.Refresh(layered)
	return p
}

func (p *LayerPage) Name() string              { return p.name }
func (p *LayerPage) Layer() config.ConfigSource { return p.layer }

func (p *LayerPage) Init() tea.Cmd { return nil }

func (p *LayerPage) Refresh(layered *config.LayeredConfig) {
	p.config = layered

	// 1. Get filtered schema for this layer
	schema := configui.FilterSchema(configui.SchemaFields, p.layer)

	// 2. Build tree from filtered schema
	p.treeRoots = configui.BuildTree(schema, layered)

	// 3. Update list items
	p.refreshList()
}

func (p *LayerPage) refreshList() {
	visibleNodes := configui.Flatten(p.treeRoots)
	var items []list.Item
	for _, node := range visibleNodes {
		items = append(items, configItem{node: node})
	}
	p.list.SetItems(items)
}

func (p *LayerPage) Update(msg tea.Msg) (configui.ConfigPage, tea.Cmd) {
	// If not active, do minimal updates
	if !p.active {
		return p, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			// Handle selection
			if idx := p.list.Index(); idx >= 0 && idx < len(p.list.Items()) {
				item := p.list.Items()[idx].(configItem)
				if item.node.IsExpandable() {
					configui.ToggleNode(item.node)
					p.refreshList()
					return p, nil
				}
				// It's a leaf, trigger edit
				return p, func() tea.Msg { return editNodeMsg{node: item.node} }
			}
		case "i":
			if idx := p.list.Index(); idx >= 0 && idx < len(p.list.Items()) {
				item := p.list.Items()[idx].(configItem)
				return p, func() tea.Msg { return infoNodeMsg{node: item.node} }
			}
		// Tree navigation - expand
		case "right", "l":
			if idx := p.list.Index(); idx >= 0 && idx < len(p.list.Items()) {
				node := p.list.Items()[idx].(configItem).node
				if node.IsExpandable() && node.Collapsed {
					node.Collapsed = false
					p.refreshList()
					return p, nil
				}
			}
		// Tree navigation - collapse or go to parent
		case "left", "h":
			if idx := p.list.Index(); idx >= 0 && idx < len(p.list.Items()) {
				node := p.list.Items()[idx].(configItem).node
				if node.IsExpandable() && !node.Collapsed {
					node.Collapsed = true
					p.refreshList()
					return p, nil
				} else if node.Parent != nil {
					// Jump to parent
					parentIdx := configui.FindParentIndex(configui.Flatten(p.treeRoots), node)
					if parentIdx >= 0 {
						p.list.Select(parentIdx)
						return p, nil
					}
				}
			}
		// Space to toggle
		case " ":
			if idx := p.list.Index(); idx >= 0 && idx < len(p.list.Items()) {
				node := p.list.Items()[idx].(configItem).node
				if node.IsExpandable() {
					configui.ToggleNode(node)
					p.refreshList()
					return p, nil
				}
			}
		}
	}

	var cmd tea.Cmd
	p.list, cmd = p.list.Update(msg)
	return p, cmd
}

func (p *LayerPage) View() string {
	if len(p.list.Items()) == 0 {
		return p.renderEmptyState()
	}

	// Render list content
	content := p.list.View()

	// Render footer with file path
	footer := p.renderFooter()

	return lipgloss.JoinVertical(lipgloss.Left, content, footer)
}

func (p *LayerPage) renderEmptyState() string {
	var msg string
	switch p.layer {
	case config.SourceEcosystem:
		msg = "No ecosystem configuration.\n\nYou're not currently in an ecosystem.\nNavigate to an ecosystem directory to\nconfigure ecosystem-level settings."
	case config.SourceProject:
		msg = "No project configuration.\n\nYou're not currently in a project.\nNavigate to a project directory to\nconfigure project-level settings."
	default:
		msg = "No configuration fields available for this layer."
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Colors.MutedText).
		Padding(1, 2).
		Align(lipgloss.Center).
		Width(50).
		Render(theme.DefaultTheme.Muted.Render(msg))

	return lipgloss.Place(p.width, p.height-2, lipgloss.Center, lipgloss.Center, box)
}

func (p *LayerPage) renderFooter() string {
	var path string
	if p.config != nil && p.config.FilePaths != nil {
		path = p.config.FilePaths[p.layer]
	}

	// Fallback for global if not set/found
	if path == "" && p.layer == config.SourceGlobal {
		path = setup.GlobalTOMLConfigPath()
	}

	if path == "" {
		path = "No config file"
	} else {
		path = setup.AbbreviatePath(path)
	}

	style := lipgloss.NewStyle().
		Foreground(theme.DefaultTheme.Colors.MutedText).
		PaddingTop(1)

	return style.Render(path)
}

func (p *LayerPage) Focus() tea.Cmd {
	p.active = true
	return nil
}

func (p *LayerPage) Blur() {
	p.active = false
}

func (p *LayerPage) SetSize(width, height int) {
	p.width = width
	p.height = height
	// Reserve space for footer (approx 2 lines)
	listHeight := height - 3
	if listHeight < 1 {
		listHeight = 1
	}
	p.list.SetSize(width, listHeight)
}

// layerPageTitle returns the title to display for a layer page
func layerPageTitle(layer config.ConfigSource, layered *config.LayeredConfig) string {
	var contextPath string
	switch layer {
	case config.SourceGlobal:
		contextPath = setup.GlobalTOMLConfigPath()
	case config.SourceEcosystem:
		if layered != nil && layered.FilePaths != nil {
			contextPath = layered.FilePaths[config.SourceEcosystem]
		}
	case config.SourceProject:
		if layered != nil && layered.FilePaths != nil {
			contextPath = layered.FilePaths[config.SourceProject]
		}
	}

	if contextPath != "" {
		return fmt.Sprintf("  %s", theme.DefaultTheme.Muted.Render(setup.AbbreviatePath(contextPath)))
	}
	return ""
}

// renderTabs renders the tab bar for the config pages.
// This follows the pattern from cx/cmd/view/view.go renderTabs().
func renderConfigTabs(pages []configui.ConfigPage, activePage int) string {
	t := theme.DefaultTheme

	inactiveTab := lipgloss.NewStyle().
		Foreground(t.Colors.MutedText).
		Padding(0, 2).
		UnderlineSpaces(false).
		Underline(false)

	activeTab := lipgloss.NewStyle().
		Foreground(t.Colors.Green).
		Bold(true).
		Padding(0, 2).
		UnderlineSpaces(false).
		Underline(false)

	// First tab styles with no left padding to align with content
	inactiveFirstTab := lipgloss.NewStyle().
		Foreground(t.Colors.MutedText).
		PaddingRight(2).
		UnderlineSpaces(false).
		Underline(false)

	activeFirstTab := lipgloss.NewStyle().
		Foreground(t.Colors.Green).
		Bold(true).
		PaddingRight(2).
		UnderlineSpaces(false).
		Underline(false)

	var tabs []string
	for i, p := range pages {
		var style lipgloss.Style
		if i == 0 {
			// First tab: no left padding
			if i == activePage {
				style = activeFirstTab
			} else {
				style = inactiveFirstTab
			}
		} else {
			// Other tabs: normal padding
			if i == activePage {
				style = activeTab
			} else {
				style = inactiveTab
			}
		}
		tabs = append(tabs, style.Render(strings.ToUpper(p.Name())))
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
}
