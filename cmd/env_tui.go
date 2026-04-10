package cmd

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/tui/embed"
	envtui "github.com/grovetools/grove/pkg/tui/env"
	"github.com/spf13/cobra"
)

func newEnvTUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the interactive environment panel",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEnvTUI()
		},
	}
}

// runEnvTUI launches the standalone env TUI panel.
func runEnvTUI() error {
	cfg, err := config.LoadDefault()
	if err != nil {
		cfg = &config.Config{}
	}

	focus := getWorkspaceNode()
	client := daemon.New()

	m := envtui.New(envtui.Config{
		DaemonClient: client,
		InitialFocus: focus,
		Cfg:          cfg,
	})

	_, err = embed.RunStandalone(m, tea.WithAltScreen())
	return err
}
