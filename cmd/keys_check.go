package cmd

import (
	"fmt"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/keys"
	"github.com/spf13/cobra"
)

// newKeysCheckCmd creates the 'grove keys check' command.
func newKeysCheckCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("check", "Check all key domains for configuration conflicts")

	cmd.Long = `Analyze keybindings across all domains and report conflicts.

Checks for key collisions within each domain:
- TUI: Multiple actions bound to the same key
- Tmux: Multiple popup commands on the same key
- Nav: Duplicate pane shortcut keys
- Neovim: Conflicting grove.nvim bindings

Cross-domain conflicts are NOT reported (e.g., tmux C-f vs TUI /)
because they operate in different contexts.`

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runKeysCheck()
	}

	return cmd
}

func runKeysCheck() error {
	cfg, err := config.LoadDefault()
	if err != nil {
		cfg = &config.Config{}
	}

	t := theme.DefaultTheme

	fmt.Println(t.Header.Render(theme.IconGear + " Grove Keybindings Check"))
	fmt.Println()
	fmt.Println(t.Muted.Render("Aggregating keys from all domains..."))

	bindings, err := keys.Aggregate(cfg)
	if err != nil {
		return fmt.Errorf("failed to aggregate keybindings: %w", err)
	}

	conflicts := keys.DetectConflicts(bindings)
	conflictMap := keys.GroupConflictsByDomain(conflicts)

	fmt.Println()

	hasErrors := false
	for _, domain := range keys.AllDomains() {
		domConflicts := conflictMap[domain]
		domainName := strings.ToUpper(domain.String())

		// Count bindings for this domain
		bindingCount := 0
		for _, b := range bindings {
			if b.Domain == domain {
				bindingCount++
			}
		}

		if len(domConflicts) == 0 {
			fmt.Printf("%s %s: %s (%d bindings)\n",
				t.Success.Render(theme.IconSuccess),
				t.Bold.Render(domainName),
				t.Success.Render("No conflicts"),
				bindingCount)
		} else {
			hasErrors = true
			fmt.Printf("%s %s: %s\n",
				t.Error.Render(theme.IconError),
				t.Bold.Render(domainName),
				t.Error.Render(fmt.Sprintf("%d conflict(s)", len(domConflicts))))
			for _, c := range domConflicts {
				var actions []string
				for _, b := range c.Bindings {
					actions = append(actions, b.Action)
				}
				fmt.Printf("     %s: %s\n",
					t.Highlight.Render(c.Key),
					strings.Join(actions, ", "))
			}
		}
	}

	fmt.Println()

	if hasErrors {
		fmt.Println(t.Warning.Render("Conflicts detected! Update your grove.toml configuration to resolve them."))
		fmt.Println(t.Muted.Render("Run 'grove keys' to browse all keybindings interactively."))
	} else {
		fmt.Println(t.Success.Render(theme.IconSuccess + " All keybindings are conflict-free!"))
	}

	return nil
}
