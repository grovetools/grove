// Package config provides the extracted config TUI model, embeddable via
// the standard embed contract (embed.FocusMsg, embed.BlurMsg, etc.) and
// using pager.Model for tab management.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"

	"github.com/grovetools/grove/pkg/configui"
	"github.com/grovetools/grove/pkg/setup"
)

// Messages to communicate from Page to outer Model.
type (
	editNodeMsg struct{ node *configui.ConfigNode }
	infoNodeMsg struct{ node *configui.ConfigNode }
)

// FilterState holds the shared filter/display preferences across all pages.
// It is shared by pointer so that toggling a filter in the outer model is
// immediately visible to every LayerPage.
type FilterState struct {
	ShowPreview    bool
	ViewMode       configui.ViewMode
	MaturityFilter configui.MaturityFilter
	SortMode       configui.SortMode
}

// LayerPage implements pager.Page for a specific config layer.
// Uses viewport for smooth scrolling instead of bubbles/list pagination.
type LayerPage struct {
	layer     config.ConfigSource
	name      string
	viewport  viewport.Model
	treeRoots []*configui.ConfigNode
	nodes     []*configui.ConfigNode // Flattened visible nodes
	cursor    int                    // Current selected index
	config    *config.LayeredConfig
	width     int
	height    int
	active    bool
	ready     bool // Viewport is initialized
	filters   *FilterState

	// Vim chord state
	lastZPress time.Time // For zR/zM/zo/zc
	lastGPress time.Time // For gg
}

// Compile-time checks.
var (
	_ pager.Page          = (*LayerPage)(nil)
	_ pager.PageWithTitle = (*LayerPage)(nil)
)

// Title implements pager.PageWithTitle. Shows the config file path for this layer.
func (p *LayerPage) Title() string {
	return LayerPageTitle(p.layer, p.config)
}

// NewLayerPage creates a page for a specific layer with viewport-based scrolling.
func NewLayerPage(name string, layer config.ConfigSource, layered *config.LayeredConfig, filters *FilterState, width, height int) *LayerPage {
	p := &LayerPage{
		layer:   layer,
		name:    name,
		config:  layered,
		width:   width,
		height:  height,
		filters: filters,
	}

	// Initialize viewport
	p.viewport = viewport.New(width, height)
	p.ready = true

	p.Refresh(layered)
	return p
}

func (p *LayerPage) Name() string               { return p.name }
func (p *LayerPage) Layer() config.ConfigSource { return p.layer }

func (p *LayerPage) Init() tea.Cmd { return nil }

func (p *LayerPage) Refresh(layered *config.LayeredConfig) {
	p.config = layered

	// 1. Get filtered schema for this layer
	schema := configui.FilterSchema(configui.SchemaFields, p.layer)

	// 2. Build tree from filtered schema
	p.treeRoots = configui.BuildTree(schema, layered)

	// 3. Sort tree at each level (before flattening to preserve hierarchy)
	configui.SortTree(p.treeRoots, p.filters.SortMode)

	// 4. Flatten and apply filters
	p.rebuildNodeList()

	// Reset cursor if out of bounds
	if p.cursor >= len(p.nodes) {
		p.cursor = 0
	}

	p.updateContent()
}

// Update implements pager.Page. Returns (pager.Page, tea.Cmd).
func (p *LayerPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) {
	// If not active, do minimal updates
	if !p.active {
		return p, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		keyStr := msg.String()

		// === Vim Chords ===

		// Handle zR/zM/zo/zc chords
		if keyStr == "z" {
			p.lastZPress = time.Now()
			return p, nil
		}
		if time.Since(p.lastZPress) < 500*time.Millisecond {
			switch keyStr {
			case "R", "shift+r": // Expand all
				configui.ExpandAll(p.treeRoots)
				p.rebuildNodeList()
				p.updateContent()
				p.lastZPress = time.Time{}
				return p, nil
			case "M", "shift+m": // Collapse all
				configui.CollapseAll(p.treeRoots)
				p.rebuildNodeList()
				p.cursor = 0
				p.updateContent()
				p.lastZPress = time.Time{}
				return p, nil
			case "o": // Open fold
				if p.cursor < len(p.nodes) {
					node := p.nodes[p.cursor]
					if node.IsExpandable() && node.Collapsed {
						node.Collapsed = false
						p.rebuildNodeList()
						p.updateContent()
					}
				}
				p.lastZPress = time.Time{}
				return p, nil
			case "c": // Close fold
				if p.cursor < len(p.nodes) {
					node := p.nodes[p.cursor]
					if node.IsExpandable() && !node.Collapsed {
						node.Collapsed = true
						p.rebuildNodeList()
						p.updateContent()
					} else if node.Parent != nil {
						// Jump to parent and collapse it
						parentIdx := configui.FindParentIndex(p.nodes, node)
						if parentIdx >= 0 {
							p.cursor = parentIdx
							p.nodes[p.cursor].Collapsed = true
							p.rebuildNodeList()
							p.updateContent()
						}
					}
				}
				p.lastZPress = time.Time{}
				return p, nil
			}
		}

		// Handle gg chord (go to top)
		if keyStr == "g" {
			if time.Since(p.lastGPress) < 500*time.Millisecond {
				p.cursor = 0
				p.updateContent()
				p.lastGPress = time.Time{}
				return p, nil
			}
			p.lastGPress = time.Now()
			return p, nil
		}

		// G (Shift+g) - go to end
		if keyStr == "G" {
			if len(p.nodes) > 0 {
				p.cursor = len(p.nodes) - 1
				p.updateContent()
			}
			return p, nil
		}

		// === Standard Navigation ===
		switch keyStr {
		case "up", "k":
			if p.cursor > 0 {
				p.cursor--
				p.updateContent()
			}
			return p, nil

		case "down", "j":
			if p.cursor < len(p.nodes)-1 {
				p.cursor++
				p.updateContent()
			}
			return p, nil

		case "ctrl+u": // Half page up
			p.cursor -= p.height / 2
			if p.cursor < 0 {
				p.cursor = 0
			}
			p.updateContent()
			return p, nil

		case "ctrl+d": // Half page down
			p.cursor += p.height / 2
			if p.cursor >= len(p.nodes) {
				p.cursor = len(p.nodes) - 1
			}
			if p.cursor < 0 {
				p.cursor = 0
			}
			p.updateContent()
			return p, nil

		case "pgup", "ctrl+b": // Full page up
			p.cursor -= p.height
			if p.cursor < 0 {
				p.cursor = 0
			}
			p.updateContent()
			return p, nil

		case "pgdown", "ctrl+f": // Full page down
			p.cursor += p.height
			if p.cursor >= len(p.nodes) {
				p.cursor = len(p.nodes) - 1
			}
			if p.cursor < 0 {
				p.cursor = 0
			}
			p.updateContent()
			return p, nil

		case "enter":
			// Handle selection
			if p.cursor >= 0 && p.cursor < len(p.nodes) {
				node := p.nodes[p.cursor]
				if node.IsExpandable() {
					configui.ToggleNode(node)
					p.rebuildNodeList()
					p.updateContent()
					return p, nil
				}
				// It's a leaf, trigger edit
				return p, func() tea.Msg { return editNodeMsg{node: node} }
			}

		case "i":
			if p.cursor >= 0 && p.cursor < len(p.nodes) {
				node := p.nodes[p.cursor]
				return p, func() tea.Msg { return infoNodeMsg{node: node} }
			}

		// Tree navigation - expand
		case "right", "l":
			if p.cursor >= 0 && p.cursor < len(p.nodes) {
				node := p.nodes[p.cursor]
				if node.IsExpandable() && node.Collapsed {
					node.Collapsed = false
					p.rebuildNodeList()
					p.updateContent()
				}
			}
			return p, nil

		// Tree navigation - collapse or go to parent
		case "left", "h":
			if p.cursor >= 0 && p.cursor < len(p.nodes) {
				node := p.nodes[p.cursor]
				if node.IsExpandable() && !node.Collapsed {
					node.Collapsed = true
					p.rebuildNodeList()
					p.updateContent()
				} else if node.Parent != nil {
					// Jump to parent
					parentIdx := configui.FindParentIndex(p.nodes, node)
					if parentIdx >= 0 {
						p.cursor = parentIdx
						p.updateContent()
					}
				}
			}
			return p, nil

		// Space to toggle
		case " ":
			if p.cursor >= 0 && p.cursor < len(p.nodes) {
				node := p.nodes[p.cursor]
				if node.IsExpandable() {
					configui.ToggleNode(node)
					p.rebuildNodeList()
					p.updateContent()
				}
			}
			return p, nil
		}
	}

	return p, nil
}

func (p *LayerPage) View() string {
	if len(p.nodes) == 0 {
		return p.renderEmptyState()
	}

	// Render viewport content
	content := p.viewport.View()

	// Render footer with file path
	footer := p.renderFooter()

	return lipgloss.JoinVertical(lipgloss.Left, content, footer)
}

// updateContent renders all nodes and updates the viewport.
func (p *LayerPage) updateContent() {
	if !p.ready || len(p.nodes) == 0 {
		p.viewport.SetContent("")
		return
	}

	var lines []string
	for i, node := range p.nodes {
		lines = append(lines, p.renderRow(node, i == p.cursor))
	}

	p.viewport.SetContent(strings.Join(lines, "\n"))

	// Keep cursor centered in viewport
	targetOffset := p.cursor - p.viewport.Height/2
	if targetOffset < 0 {
		targetOffset = 0
	}
	maxOffset := len(p.nodes) - p.viewport.Height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if targetOffset > maxOffset {
		targetOffset = maxOffset
	}
	p.viewport.SetYOffset(targetOffset)
}

// renderRow renders a single config node line.
func (p *LayerPage) renderRow(node *configui.ConfigNode, isSelected bool) string {
	// Build indentation based on depth
	indent := strings.Repeat("  ", node.Depth)

	// Tree indicator
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
	if isSelected {
		cursor = theme.DefaultTheme.Highlight.Render(theme.IconArrowRightBold) + " "
	}

	// Title styling with important indicator
	titleRaw := node.DisplayKey()
	importantStar := ""
	if node.Field.Important {
		importantStar = " " + theme.DefaultTheme.Highlight.Render("★")
	}

	// Handle status styling (alpha, beta, deprecated)
	title := titleRaw
	if node.Field.IsDeprecated() {
		title = lipgloss.NewStyle().Strikethrough(true).Render(titleRaw)
		title = theme.DefaultTheme.Warning.Render("⚠ ") + title
	} else if node.Field.Status == configui.StatusAlpha {
		title = theme.DefaultTheme.Muted.Render("α ") + title
	} else if node.Field.Status == configui.StatusBeta {
		title = theme.DefaultTheme.Highlight.Render("β ") + title
	}
	if isSelected {
		title = theme.DefaultTheme.Bold.Render(title)
	}
	title = title + importantStar

	// Calculate remaining width for value preview
	prefixLen := 2 + (node.Depth * 2) + 2 + len(titleRaw) + 2
	if node.Field.Important {
		prefixLen += 2
	}
	availableWidth := p.width - prefixLen - 4
	if availableWidth < 10 {
		availableWidth = 10
	}

	// Value display - use preview for collapsed containers if enabled
	var val string
	if node.IsContainer() && node.Collapsed && p.filters.ShowPreview {
		val = configui.FormatValuePreview(node.Value, availableWidth)
	} else {
		val = configui.FormatValue(node.Value)
	}

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

	// Override indicator
	overrideMark := ""
	if IsOverrideSource(node.ActiveSource) {
		overrideMark = theme.DefaultTheme.Muted.Render(" *")
	}

	// Status badge (alpha, beta, deprecated)
	statusBadge := ""
	if node.Field.IsNonStable() {
		badge := node.Field.StatusBadge()
		switch node.Field.Status {
		case configui.StatusAlpha:
			statusBadge = "  " + theme.DefaultTheme.Muted.Render(badge)
		case configui.StatusBeta:
			statusBadge = "  " + theme.DefaultTheme.Highlight.Render(badge)
		case configui.StatusDeprecated:
			statusBadge = "  " + theme.DefaultTheme.Error.Render(badge)
		}
	}

	valDisplay := valueStyle.Render(val)
	indicatorStyled := theme.DefaultTheme.Muted.Render(indicator)
	return fmt.Sprintf("%s%s%s%s  %s%s%s", cursor, indent, indicatorStyled, title, valDisplay, overrideMark, statusBadge)
}

func (p *LayerPage) renderEmptyState() string {
	var msg string
	switch p.layer {
	case config.SourceEcosystem:
		msg = "No ecosystem configuration.\n\nYou're not currently in an ecosystem.\nNavigate to an ecosystem directory to\nconfigure ecosystem-level settings."
	case config.SourceProjectNotebook:
		msg = "No notebook configuration.\n\nNo grove.toml found in the notebook\ndirectory for this project."
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

	// Build filter state indicator
	filterState := p.renderFilterState()

	style := lipgloss.NewStyle().
		Foreground(theme.DefaultTheme.Colors.MutedText).
		PaddingTop(1)

	return style.Render(path + "  " + filterState)
}

// renderFilterState renders the current filter state as a compact indicator.
func (p *LayerPage) renderFilterState() string {
	t := theme.DefaultTheme

	// View mode indicator
	viewIcon := "◉" // all
	if p.filters.ViewMode == configui.ViewConfigured {
		viewIcon = "◐" // configured only
	}

	// Maturity filter indicator
	var maturityIcon string
	switch p.filters.MaturityFilter {
	case configui.MaturityStable:
		maturityIcon = "stable"
	case configui.MaturityExperimental:
		maturityIcon = "+exp"
	case configui.MaturityDeprecated:
		maturityIcon = "+dep"
	case configui.MaturityAll:
		maturityIcon = "all"
	}

	// Sort mode indicator
	var sortLabel string
	switch p.filters.SortMode {
	case configui.SortConfiguredFirst:
		sortLabel = "configured"
	case configui.SortPriority:
		sortLabel = "priority"
	case configui.SortAlpha:
		sortLabel = "alphabetical"
	}

	return t.Muted.Render(fmt.Sprintf("[v:%s] [m:%s] [s:%s]", viewIcon, maturityIcon, sortLabel))
}

// rebuildNodeList flattens the tree and applies filters.
func (p *LayerPage) rebuildNodeList() {
	allNodes := configui.Flatten(p.treeRoots)
	p.nodes = configui.FilterNodes(allNodes, p.filters.ViewMode, p.filters.MaturityFilter)
}

func (p *LayerPage) Focus() tea.Cmd {
	p.active = true
	return nil
}

func (p *LayerPage) Blur() {
	p.active = false
}

// IsZChordPending returns true if a 'z' key was recently pressed.
func (p *LayerPage) IsZChordPending() bool {
	return time.Since(p.lastZPress) < 500*time.Millisecond
}

func (p *LayerPage) SetSize(width, height int) {
	p.width = width
	p.height = height
	// Reserve space for footer (approx 2 lines)
	viewportHeight := height - 3
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	p.viewport.Width = width
	p.viewport.Height = viewportHeight
	p.updateContent()
}

// LayerPageTitle returns the title to display for a layer page.
func LayerPageTitle(layer config.ConfigSource, layered *config.LayeredConfig) string {
	var contextPath string
	switch layer {
	case config.SourceGlobal:
		contextPath = setup.GlobalTOMLConfigPath()
	case config.SourceEcosystem:
		if layered != nil && layered.FilePaths != nil {
			contextPath = layered.FilePaths[config.SourceEcosystem]
		}
	case config.SourceProjectNotebook:
		if layered != nil && layered.FilePaths != nil {
			contextPath = layered.FilePaths[config.SourceProjectNotebook]
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

// IsOverrideSource returns true if the source is an override file (not the main config).
func IsOverrideSource(source config.ConfigSource) bool {
	switch source {
	case config.SourceGlobalOverride, config.SourceEnvOverlay, config.SourceOverride:
		return true
	}
	return false
}
