package cmd

import (
	"github.com/grovetools/core/cli"
	"github.com/spf13/cobra"
)

// newKeysCmd creates the parent 'grove keys' command.
// When invoked without subcommands, it launches the TUI browser.
func newKeysCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("keys", "Manage, inspect, and generate Grove keybindings")

	cmd.Long = `Unified keybinding management across the Grove ecosystem.

Browse all configured keybindings from TUIs, tmux popups, nav panes, and neovim.
Detect conflicts, generate configuration files, and inspect current bindings.

When run without arguments, opens an interactive TUI browser.`

	// If no subcommands matched, open the TUI browser
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runKeysTUI()
	}

	// Add subcommands
	cmd.AddCommand(newKeysCheckCmd())
	cmd.AddCommand(newKeysGenerateCmd())
	cmd.AddCommand(newKeysAnalyzeCmd())
	cmd.AddCommand(newKeysMatrixCmd())
	cmd.AddCommand(newKeysPopupsCmd())
	cmd.AddCommand(newKeysDumpCmd())

	return cmd
}
