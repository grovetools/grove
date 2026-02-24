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

// newKeysCrossConflictsCmd creates the 'grove keys conflicts' command.
func newKeysCrossConflictsCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("conflicts", "Detect cross-layer keybinding conflicts")

	cmd.Long = `Detect keybinding conflicts that occur across different layers.

Cross-layer conflicts happen when a key is bound at a higher-priority layer
that prevents a lower-priority layer from receiving it.

Common conflict types:
  - Shell (L2) shadowing Tmux Root (L3): Shell consumes key before tmux sees it
  - Tmux Root (L3) shadowing Shell (L2): Tmux intercepts key before shell
  - OS (L0) shadowing everything: System shortcuts can't be overridden

Unlike 'grove keys check' which finds conflicts within a domain, this command
finds conflicts across layers that affect key traversal.`

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runKeysCrossConflicts()
	}

	return cmd
}

func runKeysCrossConflicts() error {
	ctx := context.Background()
	t := theme.DefaultTheme

	fmt.Println(t.Header.Render(theme.IconGear + " Cross-Layer Key Conflicts"))
	fmt.Println(t.Muted.Render("  " + strings.Repeat("─", 45)))
	fmt.Println()

	// Load config and build stack
	cfg, _ := config.LoadDefault()
	collectors := buildKeybindCollectors(ctx, cfg)
	stack, err := keybind.BuildStack(ctx, collectors...)
	if err != nil {
		fmt.Println(t.Warning.Render("  Warning: Some collectors failed"))
	}

	// Get conflicts
	conflicts := stack.Conflicts()

	if len(conflicts) == 0 {
		fmt.Println(t.Success.Render("  " + theme.IconSuccess + " No cross-layer conflicts detected!"))
		fmt.Println()
		fmt.Println(t.Muted.Render("  All keybindings are at distinct layers without shadowing."))
		return nil
	}

	// Group by severity
	var errors, warnings, infos []keybind.Conflict
	for _, c := range conflicts {
		switch c.Severity {
		case keybind.SeverityError:
			errors = append(errors, c)
		case keybind.SeverityWarning:
			warnings = append(warnings, c)
		case keybind.SeverityInfo:
			infos = append(infos, c)
		}
	}

	// Display errors first
	if len(errors) > 0 {
		fmt.Println(t.Error.Render(fmt.Sprintf("  %s Errors (%d)", theme.IconError, len(errors))))
		fmt.Println()
		for _, c := range errors {
			printKeybindConflict(c, t)
		}
	}

	// Then warnings
	if len(warnings) > 0 {
		fmt.Println(t.Warning.Render(fmt.Sprintf("  ⚠ Warnings (%d)", len(warnings))))
		fmt.Println()
		for _, c := range warnings {
			printKeybindConflict(c, t)
		}
	}

	// Finally info
	if len(infos) > 0 {
		fmt.Println(t.Muted.Render(fmt.Sprintf("  ℹ Info (%d)", len(infos))))
		fmt.Println()
		for _, c := range infos {
			printKeybindConflict(c, t)
		}
	}

	fmt.Println()

	// Summary
	if len(errors) > 0 || len(warnings) > 0 {
		fmt.Println(t.Warning.Render("  Some conflicts may affect your workflow."))
		fmt.Println(t.Muted.Render("  Use 'grove keys trace <key>' to see how specific keys traverse the stack."))
	}

	return nil
}
