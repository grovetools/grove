package cmd

import (
	"context"
	"fmt"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/keybind"
	"github.com/grovetools/core/pkg/mux"
	"github.com/grovetools/core/tui/theme"
)

// buildKeybindCollectors creates the appropriate collectors based on the environment.
// This is shared by keys_trace, keys_available, keys_crossconflicts, keys_popups, and keys_matrix.
func buildKeybindCollectors(ctx context.Context, cfg *config.Config) []keybind.Collector {
	var collectors []keybind.Collector

	// L0: macOS collector
	if macos := keybind.NewMacOSCollector(); macos != nil {
		collectors = append(collectors, macos)
	}

	// L2: Shell collector
	shell := keybind.DetectShell()
	switch shell {
	case "fish":
		collectors = append(collectors, keybind.NewFishCollector())
	case "zsh":
		collectors = append(collectors, keybind.NewZshCollector())
	case "bash":
		collectors = append(collectors, keybind.NewBashCollector())
	}

	// L3-L5: Tmux collectors
	if mux.IsAvailable(ctx) {
		collectors = append(collectors, keybind.NewTmuxRootCollector())
		collectors = append(collectors, keybind.NewTmuxPrefixCollector())
		collectors = append(collectors, keybind.NewTmuxCustomCollector())
	}

	// L5: Grove collector
	collectors = append(collectors, keybind.NewGroveCollectorWithConfig(cfg))

	return collectors
}

// printKeybindConflict renders a conflict with proper formatting.
// This is shared by keys_crossconflicts and keys_popups.
func printKeybindConflict(c keybind.Conflict, t *theme.Theme) {
	// Key and severity indicator
	var severityIcon string
	switch c.Severity {
	case keybind.SeverityError:
		severityIcon = t.Error.Render(theme.IconError)
	case keybind.SeverityWarning:
		severityIcon = t.Warning.Render("⚠")
	default:
		severityIcon = t.Muted.Render("ℹ")
	}

	fmt.Printf("  %s %s\n", t.Highlight.Render(c.Key), severityIcon)

	// Show bindings at each layer
	for _, b := range c.Bindings {
		layerInfo := fmt.Sprintf("%s (%s)", b.Layer.ShortName(), b.Source)
		provenanceInfo := ""
		if b.Provenance != keybind.ProvenanceDetected {
			provenanceInfo = fmt.Sprintf(" [%s]", b.Provenance.String())
		}
		fmt.Printf("      %-20s %s%s\n",
			t.Muted.Render(layerInfo+":"),
			b.Action,
			t.Muted.Render(provenanceInfo))
	}

	// Description
	if c.Description != "" {
		fmt.Printf("      %s %s\n",
			t.Muted.Render("→"),
			t.Muted.Render(c.Description))
	}

	fmt.Println()
}

// checkPopupConflict checks if a key binding at a target layer conflicts with lower layers.
// Returns a Conflict if found, nil otherwise.
func checkPopupConflict(stack *keybind.Stack, key string, targetLayer keybind.Layer) *keybind.Conflict {
	normalizedKey := keybind.Normalize(key, "")

	// Check lower layers that would shadow this key
	// If we're adding to L3 (root), check L0, L1, L2
	// If we're adding to L5 (custom table), check L0, L1, L2, L3
	layersToCheck := []keybind.Layer{}
	switch targetLayer {
	case keybind.LayerTmuxRoot:
		layersToCheck = []keybind.Layer{keybind.LayerOS, keybind.LayerTerminal, keybind.LayerShell}
	case keybind.LayerTmuxCustomTable:
		layersToCheck = []keybind.Layer{keybind.LayerOS, keybind.LayerTerminal, keybind.LayerShell, keybind.LayerTmuxRoot}
	case keybind.LayerTmuxPrefix:
		layersToCheck = []keybind.Layer{keybind.LayerOS, keybind.LayerTerminal}
	}

	var conflictingBindings []keybind.Binding
	for _, layer := range layersToCheck {
		binding := stack.FindBindingInLayer(normalizedKey, layer)
		if binding != nil {
			conflictingBindings = append(conflictingBindings, *binding)
		}
	}

	if len(conflictingBindings) == 0 {
		return nil
	}

	// Also check if there's already a binding at the target layer
	targetBinding := stack.FindBindingInLayer(normalizedKey, targetLayer)
	if targetBinding != nil {
		conflictingBindings = append(conflictingBindings, *targetBinding)
	}

	// Determine severity
	severity := keybind.SeverityWarning
	description := ""

	// Check what's conflicting
	for _, b := range conflictingBindings {
		switch b.Layer {
		case keybind.LayerOS:
			severity = keybind.SeverityError
			description = "OS shortcut - this key cannot be used"
		case keybind.LayerTerminal:
			severity = keybind.SeverityError
			description = "Terminal shortcut - this key is intercepted before tmux"
		case keybind.LayerShell:
			if description == "" {
				description = fmt.Sprintf("Shell binding '%s' will shadow this key (but popup prefix may bypass)", b.Action)
			}
		case keybind.LayerTmuxRoot:
			if description == "" {
				description = fmt.Sprintf("Tmux root binding '%s' already uses this key", b.Action)
			}
		}
	}

	return &keybind.Conflict{
		Key:         normalizedKey,
		Bindings:    conflictingBindings,
		Severity:    severity,
		Description: description,
	}
}
