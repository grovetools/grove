package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/grove/pkg/keys"
	"github.com/spf13/cobra"
)

func newKeysDumpCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("dump", "Dump the full TUI keybindings registry as JSON")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		out, err := json.MarshalIndent(keys.TUIRegistry, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}
	return cmd
}
