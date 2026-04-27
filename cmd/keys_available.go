package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/keybind"
	"github.com/grovetools/core/tui/theme"
	"github.com/spf13/cobra"
)

// newKeysAvailableCmd creates the 'grove keys available' command.
func newKeysAvailableCmd() *cobra.Command {
	var layers string
	var inTable string

	cmd := cli.NewStandardCommand("available", "Find unbound keys across specified layers")

	cmd.Long = `Find keys that are available (unbound) at specified layers.

Use this to find keys you can safely use for new bindings without conflicts.

Layers:
  os        - L0: macOS system shortcuts
  terminal  - L1: Terminal emulator
  shell     - L2: Shell readline
  tmux-root - L3: Tmux root table
  tmux-pref - L4: Tmux prefix table
  tmux-custom - L5: Tmux custom tables

Examples:
  grove keys available                           # All layers
  grove keys available --layers shell,tmux-root  # Check shell and tmux root
  grove keys available --in-table grove-popups   # Check specific tmux table`

	cmd.Flags().StringVar(&layers, "layers", "", "Comma-separated list of layers to check (default: all)")
	cmd.Flags().StringVar(&inTable, "in-table", "", "Check availability in a specific tmux table")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runKeysAvailable(layers, inTable)
	}

	return cmd
}

func runKeysAvailable(layersFlag, inTable string) error {
	ctx := context.Background()
	t := theme.DefaultTheme

	// Load config and build stack
	cfg, _ := config.LoadDefault()
	collectors := buildKeybindCollectors(ctx, cfg)
	stack, err := keybind.BuildStack(ctx, collectors...)
	if err != nil {
		fmt.Println(t.Warning.Render("Warning: Some collectors failed"))
	}

	// Handle table-specific query
	if inTable != "" {
		return showTableAvailable(stack, inTable, t)
	}

	// Parse layers
	var checkLayers []keybind.Layer
	if layersFlag != "" {
		checkLayers = parseLayers(layersFlag)
	} else {
		// Default to shell and tmux layers (most relevant)
		checkLayers = []keybind.Layer{
			keybind.LayerShell,
			keybind.LayerTmuxRoot,
		}
	}

	// Get available keys
	available := stack.Available(checkLayers...)

	// Group by modifier type
	noMod := []string{}
	ctrlMod := []string{}
	metaMod := []string{}
	ctrlMetaMod := []string{}

	for _, key := range available {
		switch {
		case strings.HasPrefix(key, "C-M-"):
			ctrlMetaMod = append(ctrlMetaMod, key)
		case strings.HasPrefix(key, "C-"):
			ctrlMod = append(ctrlMod, key)
		case strings.HasPrefix(key, "M-"):
			metaMod = append(metaMod, key)
		default:
			noMod = append(noMod, key)
		}
	}

	// Sort each group
	sort.Strings(noMod)
	sort.Strings(ctrlMod)
	sort.Strings(metaMod)
	sort.Strings(ctrlMetaMod)

	// Display header
	layerNames := []string{}
	for _, l := range checkLayers {
		layerNames = append(layerNames, l.String())
	}
	fmt.Println(t.Header.Render(fmt.Sprintf("  Available keys (unbound in %s)", strings.Join(layerNames, " + "))))
	fmt.Println(t.Muted.Render("  " + strings.Repeat("─", 45)))

	// Display grouped results
	if len(noMod) > 0 {
		fmt.Printf("  %-16s %s\n",
			t.Bold.Render("Single keys:"),
			t.Muted.Render("(none available - all conflict with shell)"))
	} else {
		fmt.Printf("  %-16s %s\n",
			t.Bold.Render("Single keys:"),
			t.Muted.Render("(all bound)"))
	}

	if len(ctrlMod) > 0 {
		fmt.Printf("  %-16s %s\n",
			t.Bold.Render("With Ctrl:"),
			t.Success.Render(formatKeyList(ctrlMod, 8)))
	} else {
		fmt.Printf("  %-16s %s\n",
			t.Bold.Render("With Ctrl:"),
			t.Muted.Render("(all bound)"))
	}

	if len(metaMod) > 0 {
		fmt.Printf("  %-16s %s\n",
			t.Bold.Render("With Alt/Meta:"),
			t.Success.Render(formatKeyList(metaMod, 8)))
	} else {
		fmt.Printf("  %-16s %s\n",
			t.Bold.Render("With Alt/Meta:"),
			t.Muted.Render("(all bound)"))
	}

	if len(ctrlMetaMod) > 0 {
		fmt.Printf("  %-16s %s\n",
			t.Bold.Render("With Ctrl+Alt:"),
			t.Success.Render(formatKeyList(ctrlMetaMod, 6)))
	} else {
		fmt.Printf("  %-16s %s\n",
			t.Bold.Render("With Ctrl+Alt:"),
			t.Muted.Render("(all bound)"))
	}

	fmt.Println()

	// Recommendation
	if len(ctrlMod) > 0 || len(metaMod) > 0 {
		recommended := ""
		if len(ctrlMod) > 0 {
			// Find a good prefix key
			for _, k := range ctrlMod {
				if k == "C-G" || k == "C-]" || k == "C-\\" {
					recommended = k
					break
				}
			}
			if recommended == "" && len(ctrlMod) > 0 {
				recommended = ctrlMod[0]
			}
		}
		if recommended != "" {
			fmt.Printf("  %s Use %s as a prefix key\n",
				t.Bold.Render("Recommendation:"),
				t.Highlight.Render(recommended))
		}
	}

	return nil
}

func showTableAvailable(stack *keybind.Stack, tableName string, t *theme.Theme) error {
	available := stack.AvailableInTable(tableName)

	// Get used keys for comparison
	bindings := stack.GetTableBindings(tableName)
	usedKeys := make([]string, 0, len(bindings))
	for _, b := range bindings {
		usedKeys = append(usedKeys, b.Key)
	}
	sort.Strings(usedKeys)
	sort.Strings(available)

	fmt.Println(t.Header.Render(fmt.Sprintf("  Available keys in %s table", tableName)))
	fmt.Println(t.Muted.Render("  " + strings.Repeat("─", 45)))

	fmt.Printf("  %-12s %s\n",
		t.Bold.Render("Used:"),
		t.Muted.Render(strings.Join(usedKeys, ", ")))

	fmt.Printf("  %-12s %s\n",
		t.Bold.Render("Available:"),
		t.Success.Render(formatKeyList(available, 15)))

	return nil
}

func parseLayers(input string) []keybind.Layer {
	var layers []keybind.Layer
	for _, name := range strings.Split(input, ",") {
		name = strings.TrimSpace(strings.ToLower(name))
		switch name {
		case "os", "l0":
			layers = append(layers, keybind.LayerOS)
		case "terminal", "l1":
			layers = append(layers, keybind.LayerTerminal)
		case "shell", "l2":
			layers = append(layers, keybind.LayerShell)
		case "tmux-root", "root", "l3":
			layers = append(layers, keybind.LayerTmuxRoot)
		case "tmux-prefix", "prefix", "l4":
			layers = append(layers, keybind.LayerTmuxPrefix)
		case "tmux-custom", "custom", "l5":
			layers = append(layers, keybind.LayerTmuxCustomTable)
		case "app", "application", "l6":
			layers = append(layers, keybind.LayerApplication)
		}
	}
	return layers
}

func formatKeyList(keys []string, maxPerLine int) string {
	if len(keys) <= maxPerLine {
		return strings.Join(keys, ", ")
	}

	var lines []string
	for i := 0; i < len(keys); i += maxPerLine {
		end := i + maxPerLine
		if end > len(keys) {
			end = len(keys)
		}
		lines = append(lines, strings.Join(keys[i:end], ", "))
	}
	return strings.Join(lines, "\n                  ")
}
