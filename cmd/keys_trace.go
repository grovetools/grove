package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/keybind"
	"github.com/grovetools/core/tui/theme"
	"github.com/spf13/cobra"
)

// newKeysTraceCmd creates the 'grove keys trace' command.
func newKeysTraceCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("trace", "Trace how a key traverses the layer stack")

	cmd.Long = `Show how a key (or key sequence) is processed through each layer of the terminal stack.

Layers are checked in order:
  L0 (OS):           System shortcuts (Cmd+Space, Cmd+Tab)
  L1 (Terminal):     Terminal emulator shortcuts (Cmd+K in iTerm2)
  L2 (Shell):        Shell readline bindings (fish/bash/zsh)
  L3 (Tmux Root):    Tmux root table bindings (-n bindings)
  L4 (Tmux Prefix):  Tmux prefix table (C-b then key)
  L5 (Tmux Custom):  Custom tmux tables (grove-popups)
  L6 (Application):  Focused application (neovim, TUI)

The first layer that has a binding for the key "consumes" it.

Examples:
  grove keys trace C-p           # Single key
  grove keys trace "C-g p"       # Key sequence (prefix then key)
  grove keys trace C-b d         # Tmux prefix sequence`

	cmd.Args = cobra.MinimumNArgs(1)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runKeysTrace(args)
	}

	return cmd
}

func runKeysTrace(args []string) error {
	ctx := context.Background()
	t := theme.DefaultTheme

	// Parse key sequence
	var keys []string
	for _, arg := range args {
		// Handle quoted sequences like "C-g p"
		parts := strings.Fields(arg)
		for _, p := range parts {
			normalized := keybind.Normalize(p, "")
			if normalized != "" {
				keys = append(keys, normalized)
			}
		}
	}

	if len(keys) == 0 {
		return fmt.Errorf("no valid keys provided")
	}

	// Build header
	keyDisplay := strings.Join(keys, " ")
	if len(keys) == 1 {
		fmt.Println(t.Header.Render(fmt.Sprintf("  Key: %s", keyDisplay)))
	} else {
		fmt.Println(t.Header.Render(fmt.Sprintf("  Sequence: %s", keyDisplay)))
	}
	fmt.Println(t.Muted.Render("  " + strings.Repeat("─", 45)))

	// Load config and build stack
	cfg, _ := config.LoadDefault()

	collectors := buildKeybindCollectors(ctx, cfg)
	stack, err := keybind.BuildStack(ctx, collectors...)
	if err != nil {
		fmt.Println(t.Warning.Render("  Warning: Some collectors failed"))
	}

	// Trace the key sequence
	trace := stack.Trace(keys...)

	// Render each step
	currentTable := ""
	for _, step := range trace.Steps {
		layerName := fmt.Sprintf("%s (%s)", step.Layer.ShortName(), step.Layer.String())

		// Handle table context
		if step.TableName != "" {
			if step.TableName != currentTable {
				currentTable = step.TableName
				if step.Layer == keybind.LayerTmuxCustomTable {
					layerName = fmt.Sprintf("L5 (%s)", step.TableName)
				}
			}
		}

		switch step.Result {
		case keybind.TracePassthrough:
			fmt.Printf("  %-20s %s\n",
				t.Muted.Render(layerName+":"),
				t.Muted.Render("→ passthrough"))

		case keybind.TraceConsumed:
			action := "(unknown)"
			if step.Binding != nil {
				action = step.Binding.Action
			}
			fmt.Printf("  %-20s %s\n",
				t.Highlight.Render(layerName+":"),
				t.Success.Render("✓ "+action+" (CONSUMED)"))

		case keybind.TraceEntersTable:
			fmt.Printf("  %-20s %s\n",
				t.Highlight.Render(layerName+":"),
				t.Info.Render("→ enters "+step.NextTable))
			currentTable = step.NextTable

		case keybind.TraceNotReached:
			fmt.Printf("  %-20s %s\n",
				t.Muted.Render(layerName+":"),
				t.Muted.Render("─ (not reached)"))
		}
	}

	fmt.Println()
	fmt.Printf("  %s %s\n", t.Bold.Render("Result:"), trace.FinalResult)

	return nil
}
