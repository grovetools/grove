package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	coreconfig "github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/configui"
	"github.com/grovetools/grove/pkg/setup"
)

func (m Model) View() string {
	// Show help overlay if active
	if m.help.ShowAll {
		return m.help.View()
	}

	switch m.state {
	case viewEdit:
		return m.renderEditView()
	case viewInfo:
		return m.renderInfoView()
	case viewSources:
		return m.renderSourcesView()
	default:
		return m.renderListView()
	}
}

func (m Model) renderListView() string {
	var b strings.Builder

	// Pager renders the tab bar + active page content
	b.WriteString(m.pager.View())
	b.WriteString("\n")

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

func (m Model) renderEditView() string {
	if m.editNode == nil {
		return "No node selected for editing"
	}
	node := m.editNode

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Colors.Orange).
		Padding(1, 2).
		Width(65)

	title := theme.DefaultTheme.Bold.Render("Edit: " + node.DisplayKey())

	// Current value info
	currentValue := configui.FormatValue(node.Value)
	currentInfo := theme.DefaultTheme.Muted.Render(
		fmt.Sprintf("Current: %s (from %s)", currentValue, LayerDisplayName(node.ActiveSource)),
	)

	// Show hint for sensitive fields
	hintText := ""
	if node.Field.Hint != "" {
		hintText = theme.DefaultTheme.Muted.Render("Hint: " + node.Field.Hint)
	}

	// Edit content based on field type
	var content string
	switch node.Field.Type {
	case configui.FieldString, configui.FieldArray, configui.FieldInt:
		content = m.input.View()
	case configui.FieldSelect:
		var opts []string
		for i, opt := range node.Field.Options {
			cursor := "  "
			style := theme.DefaultTheme.Normal
			if i == m.selectIndex {
				cursor = theme.DefaultTheme.Highlight.Render(theme.IconArrowRightBold) + " "
				style = theme.DefaultTheme.Highlight
			}
			opts = append(opts, style.Render(cursor+opt))
		}
		content = strings.Join(opts, "\n")
	case configui.FieldBool:
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
	default:
		content = m.input.View()
	}

	// Layer selection info
	targetPath := m.GetLayerFilePath(m.targetLayer)
	layerBadge := RenderLayerBadge(m.targetLayer)
	layerInfo := fmt.Sprintf("Save to: %s %s", layerBadge, theme.DefaultTheme.Path.Render(setup.AbbreviatePath(targetPath)))

	recommendation := ""
	if m.targetLayer == node.Field.Layer {
		recommendation = theme.DefaultTheme.Muted.Render("         (" + LayerRecommendation(m.targetLayer) + ")")
	}

	separator := theme.DefaultTheme.Muted.Render(strings.Repeat("─", 55))
	helpText := theme.DefaultTheme.Muted.Render("enter: save • tab: change layer • esc: cancel")

	var parts []string
	parts = append(parts, title, "", currentInfo)
	if hintText != "" {
		parts = append(parts, hintText)
	}
	parts = append(parts, "", content, "", separator, layerInfo)
	if recommendation != "" {
		parts = append(parts, recommendation)
	}
	parts = append(parts, "", helpText)

	ui := lipgloss.JoinVertical(lipgloss.Left, parts...)
	dialog := boxStyle.Render(ui)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog)
}

func (m Model) renderInfoView() string {
	if m.editNode == nil {
		return "No node selected for info"
	}
	node := m.editNode

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Colors.Cyan).
		Padding(1, 2).
		Width(70)

	title := theme.DefaultTheme.Bold.Render(node.DisplayKey())

	// Prepend status block if applicable (alpha, beta, deprecated)
	var statusBlock string
	if node.Field.IsNonStable() {
		notice := node.Field.StatusNotice()

		var blockStyle lipgloss.Style
		var header string

		switch node.Field.Status {
		case configui.StatusAlpha:
			blockStyle = lipgloss.NewStyle().
				Foreground(theme.DefaultTheme.Colors.Blue).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(theme.DefaultTheme.Colors.Blue).
				Padding(0, 1).
				MarginBottom(1).
				Width(60)
			header = lipgloss.NewStyle().Bold(true).Render("α ALPHA FIELD")
		case configui.StatusBeta:
			blockStyle = lipgloss.NewStyle().
				Foreground(theme.DefaultTheme.Colors.Yellow).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(theme.DefaultTheme.Colors.Yellow).
				Padding(0, 1).
				MarginBottom(1).
				Width(60)
			header = lipgloss.NewStyle().Bold(true).Render("β BETA FIELD")
		case configui.StatusDeprecated:
			blockStyle = lipgloss.NewStyle().
				Foreground(theme.DefaultTheme.Colors.Red).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(theme.DefaultTheme.Colors.Red).
				Padding(0, 1).
				MarginBottom(1).
				Width(60)
			header = lipgloss.NewStyle().Bold(true).Render("⚠ DEPRECATED FIELD")
		}

		content := theme.DefaultTheme.Normal.Render(notice)
		statusBlock = blockStyle.Render(lipgloss.JoinVertical(lipgloss.Left, header, "", content))
	}

	desc := theme.DefaultTheme.Muted.Render(node.Field.Description)

	// Build layer values table
	var layerRows []string

	formatLayerVal := func(v interface{}) string {
		if v == nil {
			return "(not set)"
		}
		return configui.FormatValue(v)
	}

	// Default layer
	defaultVal := formatLayerVal(node.LayerValues.Default)
	layerRows = append(layerRows, m.renderLayerRow("Default", defaultVal, "", node.ActiveSource == coreconfig.SourceDefault))

	// Global layer
	globalVal := formatLayerVal(node.LayerValues.Global)
	globalPath := m.layered.FilePaths[coreconfig.SourceGlobal]
	layerRows = append(layerRows, m.renderLayerRow("Global", globalVal, globalPath, node.ActiveSource == coreconfig.SourceGlobal))

	// Global Override layer (only show if exists)
	if m.layered.GlobalOverride != nil {
		globalOverrideVal := formatLayerVal(node.LayerValues.GlobalOverride)
		globalOverridePath := m.layered.FilePaths[coreconfig.SourceGlobalOverride]
		layerRows = append(layerRows, m.renderLayerRow("Global*", globalOverrideVal, globalOverridePath, node.ActiveSource == coreconfig.SourceGlobalOverride))
	}

	// Env Overlay layer (only show if exists)
	if m.layered.EnvOverlay != nil {
		envOverlayVal := formatLayerVal(node.LayerValues.EnvOverlay)
		envOverlayPath := m.layered.FilePaths[coreconfig.SourceEnvOverlay]
		layerRows = append(layerRows, m.renderLayerRow("Env*", envOverlayVal, envOverlayPath, node.ActiveSource == coreconfig.SourceEnvOverlay))
	}

	// Ecosystem layer
	ecoVal := formatLayerVal(node.LayerValues.Ecosystem)
	ecoPath := m.layered.FilePaths[coreconfig.SourceEcosystem]
	layerRows = append(layerRows, m.renderLayerRow("Ecosystem", ecoVal, ecoPath, node.ActiveSource == coreconfig.SourceEcosystem))

	// Project layer
	projVal := formatLayerVal(node.LayerValues.Project)
	projPath := m.layered.FilePaths[coreconfig.SourceProject]
	layerRows = append(layerRows, m.renderLayerRow("Project", projVal, projPath, node.ActiveSource == coreconfig.SourceProject))

	// Override layer (only show if exists)
	if len(m.layered.Overrides) > 0 {
		overrideVal := formatLayerVal(node.LayerValues.Override)
		overridePath := m.layered.FilePaths[coreconfig.SourceOverride]
		layerRows = append(layerRows, m.renderLayerRow("Local*", overrideVal, overridePath, node.ActiveSource == coreconfig.SourceOverride))
	}

	layersContent := strings.Join(layerRows, "\n")

	separator := theme.DefaultTheme.Muted.Render(strings.Repeat("─", 60))

	// Active value summary
	activeLabel := theme.DefaultTheme.Bold.Render("Active")
	currentValue := configui.FormatValue(node.Value)
	activeVal := theme.DefaultTheme.Success.Render(currentValue)
	if currentValue == "(unset)" {
		activeVal = theme.DefaultTheme.Muted.Render("(unset)")
	}
	activeSrc := theme.DefaultTheme.Muted.Render(fmt.Sprintf("(from %s)", LayerDisplayName(node.ActiveSource)))
	activeLine := fmt.Sprintf("  %s      %s  %s", activeLabel, activeVal, activeSrc)

	// Recommendation
	recLayer := LayerDisplayName(node.Field.Layer)
	recReason := LayerRecommendation(node.Field.Layer)
	recLine := theme.DefaultTheme.Muted.Render(fmt.Sprintf("Recommended layer: %s (%s)", recLayer, recReason))

	// Field metadata
	var metaLines []string
	if node.Field.Important {
		metaLines = append(metaLines, theme.DefaultTheme.Highlight.Render("★ Important"))
	}
	if node.Field.Sensitive {
		metaLines = append(metaLines, theme.DefaultTheme.Error.Render("⚠ Sensitive field"))
	}
	if node.Field.Namespace != "" {
		metaLines = append(metaLines, theme.DefaultTheme.Muted.Render(fmt.Sprintf("Namespace: %s", node.Field.Namespace)))
	}
	if node.IsDynamic {
		metaLines = append(metaLines, theme.DefaultTheme.Muted.Render("(dynamic field)"))
	}

	helpText := theme.DefaultTheme.Muted.Render("enter: edit • esc: back")

	var parts []string
	if statusBlock != "" {
		parts = append(parts, statusBlock, "")
	}
	parts = append(parts, title, desc, "", layersContent, separator, activeLine, "", recLine)
	if len(metaLines) > 0 {
		parts = append(parts, "")
		parts = append(parts, metaLines...)
	}
	parts = append(parts, "", helpText)

	ui := lipgloss.JoinVertical(lipgloss.Left, parts...)
	dialog := boxStyle.Render(ui)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog)
}

func (m Model) renderSourcesView() string {
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.DefaultTheme.Colors.Violet).
		Padding(1, 2).
		Width(80)

	title := theme.DefaultTheme.Bold.Render("Configuration Sources")

	cwd, _ := os.Getwd()
	cwdLine := theme.DefaultTheme.Muted.Render("Working directory: ") + theme.DefaultTheme.Path.Render(setup.AbbreviatePath(cwd))

	separator := theme.DefaultTheme.Muted.Render(strings.Repeat("─", 70))

	var sourceRows []string

	addSource := func(name string, source coreconfig.ConfigSource, exists bool) {
		path := m.layered.FilePaths[source]
		var row string
		nameStyle := theme.DefaultTheme.Normal
		if exists && path != "" {
			nameStyle = theme.DefaultTheme.Success
			row = fmt.Sprintf("  %s  %s",
				nameStyle.Render(fmt.Sprintf("%-16s", name)),
				theme.DefaultTheme.Path.Render(path))
		} else {
			row = fmt.Sprintf("  %s  %s",
				theme.DefaultTheme.Muted.Render(fmt.Sprintf("%-16s", name)),
				theme.DefaultTheme.Muted.Render("(not found)"))
		}
		sourceRows = append(sourceRows, row)
	}

	addSource("Default", coreconfig.SourceDefault, true)
	addSource("Global", coreconfig.SourceGlobal, m.layered.Global != nil)
	addSource("Global*", coreconfig.SourceGlobalOverride, m.layered.GlobalOverride != nil)
	addSource("Env*", coreconfig.SourceEnvOverlay, m.layered.EnvOverlay != nil)
	addSource("Ecosystem", coreconfig.SourceEcosystem, m.layered.Ecosystem != nil)
	addSource("Project", coreconfig.SourceProject, m.layered.Project != nil)
	addSource("Local*", coreconfig.SourceOverride, len(m.layered.Overrides) > 0)

	sourcesContent := strings.Join(sourceRows, "\n")

	priorityNote := theme.DefaultTheme.Muted.Render("Priority: Local* > Project > Ecosystem > Env* > Global* > Global > Default")
	overrideNote := theme.DefaultTheme.Muted.Render("* = override file (e.g., grove.override.toml)")

	helpText := theme.DefaultTheme.Muted.Render("esc: back")

	var parts []string
	parts = append(parts, title, "", cwdLine, "", separator, "", sourcesContent, "", separator, "", priorityNote, overrideNote, "", helpText)

	ui := lipgloss.JoinVertical(lipgloss.Left, parts...)
	dialog := boxStyle.Render(ui)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog)
}

// renderLayerRow renders a single row in the layer info table.
func (m Model) renderLayerRow(name, value, path string, isActive bool) string {
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

// RenderLayerBadge renders a colored badge for the config source.
func RenderLayerBadge(source coreconfig.ConfigSource) string {
	var style lipgloss.Style
	var label string

	switch source {
	case coreconfig.SourceGlobal:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Blue)
		label = "[Global]"
	case coreconfig.SourceGlobalOverride:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Blue)
		label = "[Global*]"
	case coreconfig.SourceEnvOverlay:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Yellow)
		label = "[Env*]"
	case coreconfig.SourceEcosystem:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Violet)
		label = "[Ecosystem]"
	case coreconfig.SourceProject:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Green)
		label = "[Project]"
	case coreconfig.SourceOverride:
		style = lipgloss.NewStyle().Foreground(theme.DefaultTheme.Colors.Green)
		label = "[Local*]"
	case coreconfig.SourceDefault:
		style = theme.DefaultTheme.Muted
		label = "[Default]"
	default:
		style = theme.DefaultTheme.Muted
		label = "[Default]"
	}

	return style.Render(label)
}

// LayerDisplayName returns a human-readable name for a config source.
func LayerDisplayName(source coreconfig.ConfigSource) string {
	switch source {
	case coreconfig.SourceGlobal:
		return "Global"
	case coreconfig.SourceGlobalOverride:
		return "Global*"
	case coreconfig.SourceEnvOverlay:
		return "Env*"
	case coreconfig.SourceEcosystem:
		return "Ecosystem"
	case coreconfig.SourceProject:
		return "Project"
	case coreconfig.SourceOverride:
		return "Local*"
	case coreconfig.SourceDefault:
		return "Default"
	default:
		return "Unknown"
	}
}

// LayerRecommendation returns guidance text for the layer.
func LayerRecommendation(source coreconfig.ConfigSource) string {
	switch source {
	case coreconfig.SourceGlobal:
		return "Recommended for personal preferences"
	case coreconfig.SourceEcosystem:
		return "Use for shared settings across projects"
	case coreconfig.SourceProject:
		return "Use for project-specific overrides"
	default:
		return ""
	}
}
