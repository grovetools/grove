package cmd

import (
	"fmt"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/tui/theme"
	"github.com/mattsolo1/grove-meta/pkg/delegation"
	"github.com/spf13/cobra"
)

// newDevDelegateCmd creates the `dev delegate` command.
func newDevDelegateCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("delegate", "Manage binary delegation strategy")
	cmd.Long = `Control how 'grove' resolves tools.

By default, 'grove' uses a 'global-first' delegation strategy, always using the
globally configured binaries (as shown in 'grove list').

This command allows you to switch to 'workspace-aware' delegation, where 'grove'
will prioritize using binaries found inside your current workspace.`

	cmd.Example = `  # Check the current delegation mode
  grove dev delegate

  # Switch to workspace-aware delegation
  grove dev delegate workspace

  # Switch back to global-first delegation (default)
  grove dev delegate global`

	cmd.Use = "delegate [workspace|global]"
	cmd.Args = cobra.MaximumNArgs(1)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return displayCurrentDelegationStatus()
		}

		mode := args[0]
		switch mode {
		case "workspace":
			if err := delegation.SetMode(delegation.ModeWorkspace); err != nil {
				return fmt.Errorf("failed to set delegation mode: %w", err)
			}
			successMsg := theme.DefaultTheme.Success.Render(fmt.Sprintf("%s Grove delegation set to workspace-aware", theme.IconTree))
			detailMsg := theme.DefaultTheme.Muted.Render("'grove' will now prioritize binaries from the current workspace.")
			fmt.Println(successMsg)
			fmt.Println(detailMsg)
			return nil
		case "global":
			if err := delegation.SetMode(delegation.ModeGlobal); err != nil {
				return fmt.Errorf("failed to set delegation mode: %w", err)
			}
			successMsg := theme.DefaultTheme.Success.Render(fmt.Sprintf("%s Grove delegation set to global", theme.IconSuccess))
			detailMsg := theme.DefaultTheme.Muted.Render("'grove' will now use globally configured binaries.")
			fmt.Println(successMsg)
			fmt.Println(detailMsg)
			return nil
		default:
			return fmt.Errorf("invalid argument: %s. Must be 'workspace' or 'global'", mode)
		}
	}

	return cmd
}

func displayCurrentDelegationStatus() error {
	mode := delegation.GetMode()
	if mode == delegation.ModeWorkspace {
		modeLabel := theme.DefaultTheme.Info.Render(fmt.Sprintf("%s Delegation mode:", theme.IconTree))
		modeValue := theme.DefaultTheme.Bold.Render("workspace-aware")
		detailMsg := theme.DefaultTheme.Muted.Render("'grove' will prioritize binaries from the current workspace.")
		fmt.Printf("%s %s\n", modeLabel, modeValue)
		fmt.Println(detailMsg)
	} else {
		modeLabel := theme.DefaultTheme.Info.Render(fmt.Sprintf("%s Delegation mode:", theme.IconHome))
		modeValue := theme.DefaultTheme.Bold.Render("global") + " " + theme.DefaultTheme.Muted.Render("(default)")
		detailMsg := theme.DefaultTheme.Muted.Render("'grove' will use globally configured binaries.")
		fmt.Printf("%s %s\n", modeLabel, modeValue)
		fmt.Println(detailMsg)
	}
	return nil
}
