package cmd

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/embed"
	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/setup"
	configtui "github.com/grovetools/grove/pkg/tui/config"
	"github.com/spf13/cobra"
)

// configKeys is the singleton instance of the config editor TUI keymap.
var configKeys = func() grovekeymap.ConfigKeyMap {
	cfg, _ := config.LoadDefault()
	return grovekeymap.NewConfigKeyMap(cfg)
}()

func init() {
	rootCmd.AddCommand(newConfigCmd())
}

func newConfigCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("config", "Interactive configuration editor")
	cmd.Long = `Edit Grove configuration values interactively.

This command opens a TUI to edit the active configuration file.
It prioritizes the project-level config if present, otherwise
defaults to the global configuration.

Supports both YAML (with comment preservation) and TOML formats.`

	cmd.RunE = runConfigEdit
	return cmd
}

func runConfigEdit(cmd *cobra.Command, args []string) error {
	cwd, _ := os.Getwd()

	// Load layered configuration
	layered, err := config.LoadLayered(cwd)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w\nRun 'grove setup' to create one", err)
	}

	// Initialize Setup Service and handlers
	svc := setup.NewService(false) // Not dry-run

	// Create the extracted config TUI model
	m := configtui.New(
		layered,
		setup.NewYAMLHandler(svc),
		setup.NewTOMLHandler(svc),
		configKeys,
	)

	// Run via embed.RunStandalone which handles CloseRequestMsg -> tea.Quit,
	// EditRequestMsg -> $EDITOR suspension, etc.
	_, err = embed.RunStandalone(m, tea.WithAltScreen())
	return err
}
