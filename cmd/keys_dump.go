package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/grove/pkg/keys"
	"github.com/spf13/cobra"
)

func newKeysDumpCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("dump", "Dump the full keybindings registry and tmux config as JSON")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, _ := config.LoadDefault()
		var keysExt keys.KeysExtension
		if cfg != nil {
			_ = cfg.UnmarshalExtension("keys", &keysExt)
		}

		dumpData := struct {
			TUI  interface{} `json:"tui"`
			Tmux interface{} `json:"tmux"`
		}{
			TUI:  keys.TUIRegistry,
			Tmux: keysExt.Tmux,
		}

		out, err := json.MarshalIndent(dumpData, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}
	return cmd
}
