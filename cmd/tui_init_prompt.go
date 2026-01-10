package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattsolo1/grove-core/tui/theme"
)

// initPromptState represents the current state of the init prompt TUI.
type initPromptState int

const (
	stateConfirmAdd initPromptState = iota
	stateSelectNotebook
	stateDone
	stateCancelled
)

// initPromptKeyMap defines key bindings for the init prompt.
type initPromptKeyMap struct {
	Up     key.Binding
	Down   key.Binding
	Enter  key.Binding
	Yes    key.Binding
	No     key.Binding
	Quit   key.Binding
	Escape key.Binding
}

var initPromptKeys = initPromptKeyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "select"),
	),
	Yes: key.NewBinding(
		key.WithKeys("y", "Y"),
		key.WithHelp("y", "yes"),
	),
	No: key.NewBinding(
		key.WithKeys("n", "N"),
		key.WithHelp("n", "no"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	Escape: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "cancel"),
	),
}

// initPromptResult holds the result of the init prompt TUI.
type initPromptResult struct {
	Confirmed        bool
	SelectedNotebook string // empty string means "(none)"
}

// initPromptModel is the Bubble Tea model for the init prompt.
type initPromptModel struct {
	state            initPromptState
	parentPath       string
	groveName        string
	notebooks        []string // available notebook keys
	cursor           int      // cursor for current state's selection
	confirmCursor    int      // 0 = Yes, 1 = No
	selectedNotebook string
	result           initPromptResult
	width            int
	height           int
}

// newInitPromptModel creates a new init prompt model.
func newInitPromptModel(parentPath, groveName string, notebooks []string) initPromptModel {
	return initPromptModel{
		state:      stateConfirmAdd,
		parentPath: parentPath,
		groveName:  groveName,
		notebooks:  notebooks,
		cursor:     0,
	}
}

func (m initPromptModel) Init() tea.Cmd {
	return tea.ClearScreen
}

func (m initPromptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.state {
		case stateConfirmAdd:
			return m.updateConfirmAdd(msg)
		case stateSelectNotebook:
			return m.updateSelectNotebook(msg)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	return m, nil
}

func (m initPromptModel) updateConfirmAdd(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, initPromptKeys.Up):
		if m.confirmCursor > 0 {
			m.confirmCursor--
		}
	case key.Matches(msg, initPromptKeys.Down):
		if m.confirmCursor < 1 {
			m.confirmCursor++
		}
	case key.Matches(msg, initPromptKeys.Yes):
		// 'y' explicitly selects Yes
		m.confirmCursor = 0
		return m.confirmSelection()
	case key.Matches(msg, initPromptKeys.No):
		// 'n' explicitly selects No
		m.confirmCursor = 1
		return m.confirmSelection()
	case key.Matches(msg, initPromptKeys.Enter):
		return m.confirmSelection()
	case key.Matches(msg, initPromptKeys.Quit), key.Matches(msg, initPromptKeys.Escape):
		m.state = stateCancelled
		return m, tea.Quit
	}
	return m, nil
}

func (m initPromptModel) confirmSelection() (tea.Model, tea.Cmd) {
	if m.confirmCursor == 0 {
		// Yes selected
		if len(m.notebooks) > 0 {
			m.state = stateSelectNotebook
			m.cursor = 0
		} else {
			// No notebooks available, complete without notebook
			m.result.Confirmed = true
			m.result.SelectedNotebook = ""
			m.state = stateDone
			return m, tea.Quit
		}
	} else {
		// No selected
		m.state = stateCancelled
		return m, tea.Quit
	}
	return m, nil
}

func (m initPromptModel) updateSelectNotebook(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Total items = notebooks + "(none)" option
	totalItems := len(m.notebooks) + 1

	switch {
	case key.Matches(msg, initPromptKeys.Up):
		if m.cursor > 0 {
			m.cursor--
		}
	case key.Matches(msg, initPromptKeys.Down):
		if m.cursor < totalItems-1 {
			m.cursor++
		}
	case key.Matches(msg, initPromptKeys.Enter):
		m.result.Confirmed = true
		if m.cursor < len(m.notebooks) {
			m.result.SelectedNotebook = m.notebooks[m.cursor]
		} else {
			// "(none)" option selected
			m.result.SelectedNotebook = ""
		}
		m.state = stateDone
		return m, tea.Quit
	case key.Matches(msg, initPromptKeys.Escape):
		// Go back to confirm screen
		m.state = stateConfirmAdd
		m.cursor = 0
	case key.Matches(msg, initPromptKeys.Quit):
		m.state = stateCancelled
		return m, tea.Quit
	}
	return m, nil
}

func (m initPromptModel) View() string {
	var b strings.Builder

	// Use grove-core theme
	t := theme.DefaultTheme

	switch m.state {
	case stateConfirmAdd:
		b.WriteString(t.Warning.Render(fmt.Sprintf("%s  This ecosystem is not in a configured grove and won't be", theme.IconWarning)))
		b.WriteString("\n")
		b.WriteString(t.Warning.Render("   discovered by grove tools."))
		b.WriteString("\n\n")
		b.WriteString(t.Bold.Render(fmt.Sprintf("? Add %s to groves?", m.parentPath)))
		b.WriteString("\n\n")

		// Yes option
		if m.confirmCursor == 0 {
			b.WriteString(t.Highlight.Render(fmt.Sprintf("  %s Yes, add to groves", theme.IconArrow)))
		} else {
			b.WriteString("    Yes, add to groves")
		}
		b.WriteString("\n")

		// No option
		if m.confirmCursor == 1 {
			b.WriteString(t.Highlight.Render(fmt.Sprintf("  %s No, skip (ecosystem won't be discoverable)", theme.IconArrow)))
		} else {
			b.WriteString("    No, skip (ecosystem won't be discoverable)")
		}
		b.WriteString("\n\n")
		b.WriteString(t.Muted.Render("↑/↓ to navigate, enter to select, y/n for quick select, q to quit"))

	case stateSelectNotebook:
		b.WriteString(t.Bold.Render("? Select notebook for this grove:"))
		b.WriteString("\n\n")

		// List notebooks
		for i, nb := range m.notebooks {
			if i == m.cursor {
				b.WriteString(t.Highlight.Render(fmt.Sprintf("  %s %s", theme.IconArrow, nb)))
			} else {
				b.WriteString(fmt.Sprintf("    %s", nb))
			}
			b.WriteString("\n")
		}

		// "(none)" option
		noneIdx := len(m.notebooks)
		if m.cursor == noneIdx {
			b.WriteString(t.Highlight.Render(fmt.Sprintf("  %s (skip notebook association)", theme.IconArrow)))
		} else {
			b.WriteString("    (skip notebook association)")
		}
		b.WriteString("\n\n")
		b.WriteString(t.Muted.Render("↑/↓ to navigate, enter to select, esc to go back"))

	case stateDone:
		b.WriteString("Done.\n")

	case stateCancelled:
		b.WriteString("Cancelled.\n")
	}

	return b.String()
}

// GetResult returns the result after the TUI has finished.
func (m initPromptModel) GetResult() initPromptResult {
	return m.result
}

// runInitPrompt runs the init prompt TUI and returns the result.
// This is a convenience function for running the TUI programmatically.
func runInitPrompt(parentPath, groveName string, notebooks []string) (initPromptResult, error) {
	model := newInitPromptModel(parentPath, groveName, notebooks)
	p := tea.NewProgram(model)

	finalModel, err := p.Run()
	if err != nil {
		return initPromptResult{}, fmt.Errorf("error running TUI: %w", err)
	}

	// Type assert to get the result
	if m, ok := finalModel.(initPromptModel); ok {
		return m.GetResult(), nil
	}

	return initPromptResult{}, fmt.Errorf("unexpected model type")
}
