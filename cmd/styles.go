package cmd

import "github.com/charmbracelet/lipgloss"

// Common styles used across commands
var (
	// Status styles
	successStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)  // Green
	errorStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))             // Red
	updateStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))             // Yellow/Orange
	failStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff4444")).Bold(true)
	devStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF79C6")).Bold(true)
	releaseStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#50FA7B")).Bold(true)
	notInstalledStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272A4"))

	// Version styles
	versionStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))  // Blue
	updateAvailableStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB86C"))
	upToDateStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#50FA7B"))

	// Text styles
	faintStyle    = lipgloss.NewStyle().Faint(true)
	toolNameStyle = lipgloss.NewStyle().Bold(true)
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#BD93F9"))

	// Workspace styles
	warningStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	infoStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("33"))
)