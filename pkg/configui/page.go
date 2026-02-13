// Package configui provides schema-driven configuration UI types and metadata.
package configui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/config"
)

// ConfigPage defines the interface for a tabbed page in the config editor.
// This follows the same pattern as cx/cmd/view/Page for consistency.
type ConfigPage interface {
	// Name returns the display name for the tab (e.g., "Global", "Ecosystem", "Project").
	Name() string
	// Layer returns the config layer this page represents.
	Layer() config.ConfigSource
	// Init initializes the page model.
	Init() tea.Cmd
	// Update handles messages for the page.
	Update(tea.Msg) (ConfigPage, tea.Cmd)
	// View renders the page's UI.
	View() string
	// Focus is called when the page becomes active.
	Focus() tea.Cmd
	// Blur is called when the page loses focus.
	Blur()
	// SetSize sets the dimensions for the page.
	SetSize(width, height int)
	// Refresh reloads the page with updated config data.
	Refresh(layered *config.LayeredConfig)
}
