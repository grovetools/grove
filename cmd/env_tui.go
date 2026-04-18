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

// runEnvTUI launches the env TUI panel. The panel has two modes: if the
// current workspace is an ecosystem (root or worktree) we run the
// ecosystem-scoped dashboard; otherwise we fall back to the single-worktree
// panel that has been the default since Phase 4. Mode is picked once at
// launch from cwd — the embed contract's SetWorkspaceMsg can later repoint
// each model to a new workspace of the same kind.
func runEnvTUI() error {
	cfg, err := config.LoadDefault()
	if err != nil {
		cfg = &config.Config{}
	}

	focus := getWorkspaceNode()
	client := daemon.NewWithAutoStart()

	var model tea.Model
	if focus != nil && focus.IsEcosystem() {
		model = envtui.NewEcosystem(envtui.EcosystemConfig{
			DaemonClient: client,
			Root:         focus,
			Cfg:          cfg,
		})
	} else {
		model = envtui.New(envtui.Config{
			DaemonClient: client,
			InitialFocus: focus,
			Cfg:          cfg,
		})
	}

	_, err = embed.RunStandalone(model, tea.WithAltScreen())
	return err
}
