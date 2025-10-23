package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	stdlog "log"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hpcloud/tail"
	"github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/tui/components/help"
	"github.com/mattsolo1/grove-core/tui/keymap"
	"github.com/mattsolo1/grove-core/tui/theme"
	"github.com/spf13/cobra"
)

// logItem represents a single log entry
type logItem struct {
	workspace string
	level     string
	message   string
	component string
	timestamp time.Time
	rawData   map[string]interface{}
}

// getThemeLevelStyle returns theme-based styling for log levels
func getThemeLevelStyle(level string) lipgloss.Style {
	switch strings.ToLower(level) {
	case "info":
		return theme.DefaultTheme.Success
	case "warning", "warn":
		return theme.DefaultTheme.Warning
	case "error", "fatal", "panic":
		return theme.DefaultTheme.Error
	case "debug", "trace":
		return theme.DefaultTheme.Muted
	default:
		return lipgloss.NewStyle()
	}
}

// Implement list.Item interface
func (i logItem) Title() string {
	// Compact view: [workspace] [LEVEL] time [component] message
	wsStyle := getWorkspaceStyle(i.workspace)
	levelStyle := getThemeLevelStyle(i.level)
	timeStyle := theme.DefaultTheme.Muted
	componentStyle := theme.DefaultTheme.Muted.Copy().Bold(true)
	
	return fmt.Sprintf("%s %s %s %s %s",
		wsStyle.Render(fmt.Sprintf("[%s]", i.workspace)),
		levelStyle.Render(fmt.Sprintf("[%s]", strings.ToUpper(i.level))),
		timeStyle.Render(i.timestamp.Format("15:04:05")),
		componentStyle.Render(fmt.Sprintf("[%s]", i.component)),
		i.message,
	)
}

func (i logItem) Description() string {
	// We don't use description anymore since details are shown in viewport
	return ""
}

func (i logItem) FilterValue() string {
	// Only search the component field
	return i.component
}

// FormatDetails returns a formatted string of the log entry details for the viewport
func (i logItem) FormatDetails() string {
	var lines []string
	
	// Header with basic info
	headerStyle := theme.DefaultTheme.Header
	lines = append(lines, headerStyle.Render("Log Entry Details"))
	lines = append(lines, "")
	
	// Basic info
	wsStyle := getWorkspaceStyle(i.workspace)
	levelStyle := getThemeLevelStyle(i.level)
	timeStyle := theme.DefaultTheme.Muted
	componentStyle := theme.DefaultTheme.Muted.Copy().Bold(true)
	
	lines = append(lines, fmt.Sprintf("Workspace:  %s", wsStyle.Render(i.workspace)))
	lines = append(lines, fmt.Sprintf("Level:      %s", levelStyle.Render(strings.ToUpper(i.level))))
	lines = append(lines, fmt.Sprintf("Component:  %s", componentStyle.Render(i.component)))
	lines = append(lines, fmt.Sprintf("Time:       %s", timeStyle.Render(i.timestamp.Format("2006-01-02 15:04:05"))))
	lines = append(lines, fmt.Sprintf("Message:    %s", i.message))
	lines = append(lines, "")
	
	// Standard fields we've already shown
	standardFields := map[string]bool{
		"level": true, "msg": true, "component": true, "time": true, "_verbosity": true,
	}
	
	// Special fields to show separately
	var fileInfo, funcInfo string
	if file, ok := i.rawData["file"].(string); ok {
		fileInfo = file
	}
	if fn, ok := i.rawData["func"].(string); ok {
		funcInfo = fn
	}
	
	// Extract verbosity metadata
	var verbosityMap map[string]int
	if verbosityRaw, exists := i.rawData["_verbosity"]; exists {
		if verbosityMapInterface, ok := verbosityRaw.(map[string]interface{}); ok {
			verbosityMap = make(map[string]int)
			for key, val := range verbosityMapInterface {
				if intVal, ok := val.(float64); ok {
					verbosityMap[key] = int(intVal)
				}
			}
		}
	}
	
	// Build the display
	fieldStyle := theme.DefaultTheme.Muted
	fileStyle := theme.DefaultTheme.Muted
	borderStyle := theme.DefaultTheme.Muted
	
	// Add file/func info if present
	if fileInfo != "" || funcInfo != "" {
		lines = append(lines, borderStyle.Render("┌─ Source:"))
		if fileInfo != "" {
			lines = append(lines, fileStyle.Render(fmt.Sprintf("│ 📁 %s", fileInfo)))
		}
		if funcInfo != "" {
			lines = append(lines, fileStyle.Render(fmt.Sprintf("│ ⚙️  %s", funcInfo)))
		}
	}
	
	// Categorize fields by verbosity level
	fieldsByLevel := map[int][]string{
		0: {}, // basic
		1: {}, // verbose  
		2: {}, // debug
		3: {}, // metrics
	}
	
	for key, value := range i.rawData {
		if !standardFields[key] && key != "file" && key != "func" {
			// Format the value
			var formattedValue string
			switch v := value.(type) {
			case string:
				formattedValue = v
			case float64:
				if v == float64(int64(v)) {
					formattedValue = fmt.Sprintf("%.0f", v)
				} else {
					formattedValue = fmt.Sprintf("%.2f", v)
				}
			case bool:
				formattedValue = fmt.Sprintf("%t", v)
			default:
				formattedValue = fmt.Sprintf("%v", v)
			}
			
			// Determine verbosity level
			verbosityLevel := 0
			if verbosityMap != nil {
				if level, exists := verbosityMap[key]; exists {
					verbosityLevel = level
				}
			}
			
			if verbosityLevel < 4 {
				fieldsByLevel[verbosityLevel] = append(fieldsByLevel[verbosityLevel], fmt.Sprintf("%-20s %s", key+":", formattedValue))
			}
		}
	}
	
	// Add fields if present
	hasFields := false
	for _, fields := range fieldsByLevel {
		if len(fields) > 0 {
			hasFields = true
			break
		}
	}
	
	if hasFields {
		if fileInfo != "" || funcInfo != "" {
			lines = append(lines, borderStyle.Render("├─ Fields:"))
		} else {
			lines = append(lines, borderStyle.Render("┌─ Fields:"))
		}
		
		// Sort fields within each level for consistency
		for level := 0; level < 4; level++ {
			if fields := fieldsByLevel[level]; len(fields) > 0 {
				sort.Strings(fields)
				for i, field := range fields {
					isLast := (level == 3 || len(fieldsByLevel[level+1]) == 0) && i == len(fields)-1
					// Check if this is truly the last field across all levels
					hasMoreFields := false
					for checkLevel := level + 1; checkLevel < 4; checkLevel++ {
						if len(fieldsByLevel[checkLevel]) > 0 {
							hasMoreFields = true
							break
						}
					}
					
					if isLast && !hasMoreFields {
						lines = append(lines, fieldStyle.Render(fmt.Sprintf("└─ %s", field)))
					} else {
						lines = append(lines, fieldStyle.Render(fmt.Sprintf("├─ %s", field)))
					}
				}
			}
		}
	}
	
	return strings.Join(lines, "\n")
}

// Custom item delegate for rendering
type itemDelegate struct{
	model *logModel
}

func (d itemDelegate) Height() int                              { return 1 }
func (d itemDelegate) Spacing() int                             { return 0 }
func (d itemDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d itemDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	i, ok := item.(logItem)
	if !ok {
		return
	}
	
	str := i.Title()
	
	// Check if this item is in visual selection range
	// Note: index here is the index within the visible/filtered items
	isVisuallySelected := false
	if d.model != nil && d.model.visualMode {
		minIdx := d.model.visualStart
		maxIdx := d.model.visualEnd
		if minIdx > maxIdx {
			minIdx, maxIdx = maxIdx, minIdx
		}
		isVisuallySelected = index >= minIdx && index <= maxIdx
	}
	
	// Apply highlighting
	if isVisuallySelected {
		// Visual selection highlighting
		str = theme.DefaultTheme.Selected.Copy().Bold(true).Render(str)
	} else if index == m.Index() {
		// Normal cursor highlighting
		str = theme.DefaultTheme.Selected.Render(str)
	}
	
	fmt.Fprint(w, str)
}

// keyMap defines all key bindings for the TUI
type logKeyMap struct {
	keymap.Base
	PageUp   key.Binding
	PageDown key.Binding
	HalfUp   key.Binding
	HalfDown key.Binding
	GotoTop  key.Binding
	GotoEnd  key.Binding
	Expand   key.Binding
	Search   key.Binding
	Clear    key.Binding
	Follow   key.Binding
}

var logKeys = logKeyMap{
	Base: keymap.NewBase(),
	PageUp: key.NewBinding(
		key.WithKeys("pgup"),
		key.WithHelp("pgup", "page up"),
	),
	PageDown: key.NewBinding(
		key.WithKeys("pgdown"),
		key.WithHelp("pgdn", "page down"),
	),
	HalfUp: key.NewBinding(
		key.WithKeys("ctrl+u"),
		key.WithHelp("ctrl+u", "half page up"),
	),
	HalfDown: key.NewBinding(
		key.WithKeys("ctrl+d"),
		key.WithHelp("ctrl+d", "half page down"),
	),
	GotoTop: key.NewBinding(
		key.WithKeys("g"),
		key.WithHelp("gg", "go to top"),
	),
	GotoEnd: key.NewBinding(
		key.WithKeys("G"),
		key.WithHelp("G", "go to end"),
	),
	Expand: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("space", "expand/collapse"),
	),
	Search: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "search"),
	),
	Clear: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "clear search"),
	),
	Follow: key.NewBinding(
		key.WithKeys("f"),
		key.WithHelp("f", "toggle follow"),
	),
}

// Main TUI model
type logModel struct {
	list          list.Model
	items         []logItem
	keys          logKeyMap
	spinner       spinner.Model
	viewport      viewport.Model
	help          help.Model
	loading       bool
	err           error
	width         int
	height        int
	followMode    bool
	logChan       chan TailedLine
	mu            sync.Mutex
	lastGotoG     time.Time
	workspaceColors map[string]lipgloss.Style
	colorIndex    int
	ready         bool  // viewport ready flag
	visualMode    bool  // visual selection mode
	visualStart   int   // start of visual selection
	visualEnd     int   // end of visual selection
	statusMessage string // status message for copy confirmation
}

// Messages
type newLogMsg struct {
	workspace string
	data      map[string]interface{}
}

type tickMsg time.Time

func (m *logModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.waitForLogs(),
		tick(),
	)
}

func tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *logModel) getSelectedContent() string {
	minIdx := m.visualStart
	maxIdx := m.visualEnd
	if minIdx > maxIdx {
		minIdx, maxIdx = maxIdx, minIdx
	}
	
	// Get the visible items from the list (handles filtering)
	visibleItems := m.list.VisibleItems()
	
	// Create a JSON array of selected log entries
	var logs []map[string]interface{}
	for i := minIdx; i <= maxIdx && i < len(visibleItems); i++ {
		// Get the actual item from visible items
		if item, ok := visibleItems[i].(logItem); ok {
			// Create a copy of the raw data
			logEntry := make(map[string]interface{})
			for k, v := range item.rawData {
				logEntry[k] = v
			}
			
			// Ensure workspace is included (might not be in rawData)
			logEntry["workspace"] = item.workspace
			
			logs = append(logs, logEntry)
		}
	}
	
	// Convert to pretty JSON
	jsonBytes, err := json.MarshalIndent(logs, "", "  ")
	if err != nil {
		// Fallback to simple format if JSON fails
		return fmt.Sprintf("Error formatting JSON: %v", err)
	}
	
	return string(jsonBytes)
}

func (m *logModel) copyToClipboard(content string) error {
	var cmd *exec.Cmd
	
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		// Try xclip first, then xsel
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		} else {
			return fmt.Errorf("no clipboard utility found (install xclip or xsel)")
		}
	case "windows":
		cmd = exec.Command("cmd", "/c", "clip")
	default:
		return fmt.Errorf("unsupported platform")
	}
	
	cmd.Stdin = strings.NewReader(content)
	return cmd.Run()
}

func (m *logModel) clearStatusMessageAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return clearStatusMsg{}
	})
}

type clearStatusMsg struct{}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (m *logModel) waitForLogs() tea.Cmd {
	return func() tea.Msg {
		line, ok := <-m.logChan
		if !ok {
			return nil
		}
		
		// Parse the JSON log entry
		var rawEntry map[string]interface{}
		if err := json.Unmarshal([]byte(line.Line), &rawEntry); err != nil {
			// Skip non-JSON lines
			return m.waitForLogs()
		}
		
		return newLogMsg{
			workspace: line.Workspace,
			data:      rawEntry,
		}
	}
}

func (m *logModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Don't intercept keys when filtering is active except for our special ones
		if m.list.FilterState() == list.Filtering {
			switch {
			case key.Matches(msg, logKeys.Base.Quit):
				// Allow quitting even during search
				return m, tea.Quit
			case key.Matches(msg, logKeys.Clear):
				// Escape clears the filter
				m.list.ResetFilter()
				return m, nil
			}
			// Let the list handle other keys when filtering
		} else {
			// Handle double 'g' for goto top (only when not filtering)
			if msg.String() == "g" {
				if time.Since(m.lastGotoG) < 500*time.Millisecond {
					// Double 'g' pressed - go to top
					m.list.Select(0)
					m.lastGotoG = time.Time{}
					return m, nil
				}
				m.lastGotoG = time.Now()
				return m, nil
			}
			
			switch {
			case key.Matches(msg, logKeys.Base.Quit):
				return m, tea.Quit
				
			case key.Matches(msg, logKeys.Base.Help):
				m.help.ShowAll = !m.help.ShowAll
				return m, nil
				
			case msg.String() == "V": // Shift+V to start visual mode
				if !m.visualMode {
					m.visualMode = true
					m.visualStart = m.list.Index()
					m.visualEnd = m.list.Index()
					m.statusMessage = "-- VISUAL LINE --"
				} else {
					// Exit visual mode
					m.visualMode = false
					m.statusMessage = ""
				}
				// Force list to re-render with new highlighting
				m.list.SetDelegate(itemDelegate{model: m})
				return m, nil
				
			case msg.String() == "y": // Yank selected lines
				if m.visualMode {
					// Copy selected items to clipboard
					content := m.getSelectedContent()
					if err := m.copyToClipboard(content); err == nil {
						lineCount := abs(m.visualEnd-m.visualStart) + 1
						m.statusMessage = fmt.Sprintf("Copied %d log entries as JSON", lineCount)
					} else {
						m.statusMessage = fmt.Sprintf("Copy failed: %v", err)
					}
					m.visualMode = false
					// Force re-render to clear highlighting
					m.list.SetDelegate(itemDelegate{model: m})
					// Clear status message after 2 seconds
					return m, m.clearStatusMessageAfter(2 * time.Second)
				}
				return m, nil
				
			case key.Matches(msg, logKeys.Clear): // Escape to exit visual mode
				if m.visualMode {
					m.visualMode = false
					m.statusMessage = ""
					// Force re-render to clear highlighting
					m.list.SetDelegate(itemDelegate{model: m})
					return m, nil
				}
				
			case key.Matches(msg, logKeys.GotoEnd):
				m.list.Select(len(m.items) - 1)
				return m, nil
				
			case key.Matches(msg, logKeys.HalfUp):
				// Calculate half page
				visibleHeight := m.height - 4 // Account for header/footer
				halfPage := visibleHeight / 2
				currentIndex := m.list.Index()
				newIndex := currentIndex - halfPage
				if newIndex < 0 {
					newIndex = 0
				}
				m.list.Select(newIndex)
				return m, nil
				
			case key.Matches(msg, logKeys.HalfDown):
				// Calculate half page
				visibleHeight := m.height - 4 // Account for header/footer
				halfPage := visibleHeight / 2
				currentIndex := m.list.Index()
				newIndex := currentIndex + halfPage
				if newIndex >= len(m.items) {
					newIndex = len(m.items) - 1
				}
				m.list.Select(newIndex)
				return m, nil
				
			case msg.String() == "/":
				// Let the list handle the "/" key to start filtering
				// Don't return here, let it fall through to list.Update
				
			case key.Matches(msg, logKeys.Follow):
				m.followMode = !m.followMode
				return m, nil
			}
		}
		
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		
		// Split the view: 1/2 for list, 1/2 for details
		listHeight := m.height / 2
		viewportHeight := m.height - listHeight - 3 // -3 for borders and status line
		
		// Update list size
		m.list.SetSize(msg.Width, listHeight)
		
		// Update viewport size
		if !m.ready {
			m.viewport = viewport.New(msg.Width-4, viewportHeight) // -4 for padding
			m.viewport.YPosition = listHeight + 1
			m.ready = true
		} else {
			m.viewport.Width = msg.Width - 4
			m.viewport.Height = viewportHeight
		}
		
		// Update content in viewport if we have a selected item
		if selectedItem := m.list.SelectedItem(); selectedItem != nil {
			if logItem, ok := selectedItem.(logItem); ok {
				m.viewport.SetContent(logItem.FormatDetails())
			}
		}
		
		return m, nil
		
	case newLogMsg:
		// Process new log entry
		level, _ := msg.data["level"].(string)
		message, _ := msg.data["msg"].(string)
		component, _ := msg.data["component"].(string)
		timeStr, _ := msg.data["time"].(string)
		
		var logTime time.Time
		if parsedTime, err := time.Parse(time.RFC3339, timeStr); err == nil {
			logTime = parsedTime
		}
		
		newItem := logItem{
			workspace: msg.workspace,
			level:     level,
			message:   message,
			component: component,
			timestamp: logTime,
			rawData:   msg.data,
		}
		
		m.mu.Lock()
		m.items = append(m.items, newItem)
		
		// Update list items
		items := make([]list.Item, len(m.items))
		for i := range m.items {
			items[i] = m.items[i]
		}
		m.list.SetItems(items)
		
		// Auto-scroll to bottom if in follow mode
		if m.followMode {
			m.list.Select(len(m.items) - 1)
			// Update viewport with the new selection
			if selectedItem := m.list.SelectedItem(); selectedItem != nil {
				if logItem, ok := selectedItem.(logItem); ok {
					m.viewport.SetContent(logItem.FormatDetails())
					m.viewport.GotoTop()
				}
			}
		}
		m.mu.Unlock()
		
		// Continue waiting for more logs
		return m, m.waitForLogs()
		
	case tickMsg:
		// Check for any rendering updates needed
		return m, tick()
		
	case clearStatusMsg:
		m.statusMessage = ""
		return m, nil
		
	case spinner.TickMsg:
		if m.loading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
	}
	
	// Update the list
	prevIndex := m.list.Index()
	newListModel, cmd := m.list.Update(msg)
	m.list = newListModel
	cmds = append(cmds, cmd)
	
	// Update visual selection end if in visual mode
	if m.visualMode && m.list.Index() != prevIndex {
		m.visualEnd = m.list.Index()
		// Force re-render to update highlighting
		m.list.SetDelegate(itemDelegate{model: m})
	}
	
	// Update viewport content if selection changed
	if m.list.Index() != prevIndex {
		if selectedItem := m.list.SelectedItem(); selectedItem != nil {
			if logItem, ok := selectedItem.(logItem); ok {
				m.viewport.SetContent(logItem.FormatDetails())
				m.viewport.GotoTop()
			}
		}
	}
	
	// Allow scrolling in viewport with arrow keys when viewport is focused
	// (we can add viewport focus mode later if needed)
	
	return m, tea.Batch(cmds...)
}

func (m *logModel) View() string {
	if m.help.ShowAll {
		return m.help.View()
	}
	
	// If not ready, show loading
	if !m.ready {
		return "Initializing..."
	}
	
	// Main list view with error recovery
	var listView string
	func() {
		defer func() {
			if r := recover(); r != nil {
				// If list view panics, show an error message
				listView = fmt.Sprintf("Error rendering list: %v", r)
			}
		}()
		listView = m.list.View()
	}()
	
	// Separator between list and details
	separatorStyle := theme.DefaultTheme.Muted.Copy().
		Border(lipgloss.NormalBorder(), true, false, false, false)
	separator := separatorStyle.Render(strings.Repeat("─", m.width-2))
	
	// Details view with border
	detailsStyle := theme.DefaultTheme.Muted.Copy().
		Padding(0, 2).
		BorderStyle(lipgloss.RoundedBorder())
	
	detailsView := detailsStyle.Render(m.viewport.View())
	
	// Status line
	statusStyle := theme.DefaultTheme.Muted
	
	followIndicator := ""
	if m.followMode {
		followIndicator = " [FOLLOWING]"
	}
	
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
	
	// Ensure index is valid
	if currentIndex < 0 {
		currentIndex = 0
	}
	
	var position string
	if visibleItems == 0 {
		position = "0/0"
	} else {
		// When filtered, show position within filtered results
		position = fmt.Sprintf("%d/%d", currentIndex+1, visibleItems)
		if m.list.FilterState() != list.Unfiltered && visibleItems < len(m.items) {
			// Also show total when filtered
			position = fmt.Sprintf("%d/%d (of %d)", currentIndex+1, visibleItems, len(m.items))
		}
	}
	
	// Add visual mode or status message
	modeIndicator := ""
	if m.visualMode {
		modeIndicator = " [VISUAL]"
	} else if m.statusMessage != "" {
		modeIndicator = fmt.Sprintf(" [%s]", m.statusMessage)
	}
	
	status := statusStyle.Render(fmt.Sprintf(" Logs: %s%s%s%s | ? for help | q to quit", 
		position, followIndicator, filterIndicator, modeIndicator))
	
	// Combine all views
	return lipgloss.JoinVertical(
		lipgloss.Left,
		listView,
		separator,
		detailsView,
		status,
	)
}


// Workspace color management
var workspaceColorPalette = []lipgloss.Color{"39", "45", "51", "81", "117", "153", "189", "225"}
var workspaceColorMap = make(map[string]lipgloss.Style)
var workspaceColorIndex = 0

func getWorkspaceStyle(workspace string) lipgloss.Style {
	if style, ok := workspaceColorMap[workspace]; ok {
		return style
	}
	
	color := workspaceColorPalette[workspaceColorIndex%len(workspaceColorPalette)]
	style := lipgloss.NewStyle().Foreground(color).Bold(true)
	workspaceColorMap[workspace] = style
	workspaceColorIndex++
	
	return style
}

// Run the logs TUI
func runLogsTUI(cmd *cobra.Command, workspaces []string, follow bool) error {
	logger := logging.NewLogger("logs-tui")
	
	// Create channel for log lines
	logChan := make(chan TailedLine, 100)
	
	// Start tailing log files
	var wg sync.WaitGroup
	for _, ws := range workspaces {
		logDir := filepath.Join(ws, ".grove", "logs")
		files, err := filepath.Glob(filepath.Join(logDir, "workspace-*.log"))
		if err != nil {
			continue
		}
		
		for _, file := range files {
			wg.Add(1)
			go func(path, wsName string) {
				defer wg.Done()
				config := tail.Config{
					Follow: follow,
					ReOpen: follow,
					// Always start from beginning to get all logs
					Location: &tail.SeekInfo{Offset: 0, Whence: io.SeekStart},
					Logger:   stdlog.New(ioutil.Discard, "", 0), // Suppress tail library debug output
				}
				
				t, err := tail.TailFile(path, config)
				if err != nil {
					logger.Debugf("Cannot tail file %s: %v", path, err)
					return
				}
				
				for line := range t.Lines {
					if line.Err != nil {
						logger.Debugf("Error reading line from %s: %v", path, line.Err)
						continue
					}
					logChan <- TailedLine{Workspace: wsName, Line: line.Text}
				}
			}(file, filepath.Base(ws))
		}
	}
	
	// Close channel when all tailers are done
	go func() {
		wg.Wait()
		close(logChan)
	}()
	
	// Create list
	l := list.New([]list.Item{}, itemDelegate{}, 0, 0)
	l.Title = "Grove Logs"
	l.SetShowStatusBar(false)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	// Don't disable filtering - we want to toggle it with '/'
	l.SetShowPagination(true)  // Show pagination to help track position
	l.InfiniteScrolling = false  // Disable infinite scrolling for better control
	l.DisableQuitKeybindings()  // We handle quit ourselves
	
	// Configure pagination style
	l.Styles.PaginationStyle = theme.DefaultTheme.Muted.Copy().
		PaddingLeft(2)
	
	// Create spinner
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = theme.DefaultTheme.Highlight
	
	// Initialize model
	model := &logModel{
		list:            l,
		items:           []logItem{},
		keys:            logKeys,
		spinner:         s,
		help:            help.New(logKeys),
		loading:         true,
		followMode:      follow,
		logChan:         logChan,
		workspaceColors: make(map[string]lipgloss.Style),
		ready:           false,
	}
	
	// Set the delegate with model reference
	l.SetDelegate(itemDelegate{model: model})
	
	// Run the TUI
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("error running TUI: %w", err)
	}
	
	return nil
}