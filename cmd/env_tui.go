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
		Long: `Open the interactive environment panel.

The TUI picks a mode at launch from the current working directory:

  Ecosystem mode — launched from an ecosystem root. Shows the cross-worktree
    Deployments matrix, Shared Infra detail, Profiles catalog, Orphans, and
    ecosystem-wide actions. Press W to jump into any worktree's panel.

  Worktree mode — launched from any worktree (ecosystem or standalone). Shows
    Overview, Summary, Config & Provenance, Runtime, Drift, and Actions
    scoped to a single profile. Press P to switch profiles.

To use the CLI companion for ecosystem mode without opening a TUI, run
'grove env ecosystem [--json]' from the same directory.`,
		Example: `  # From an ecosystem root → ecosystem mode
  cd ~/code/my-ecosystem && grove env tui

  # From a worktree → worktree mode
  cd ~/code/my-ecosystem/.grove-worktrees/my-branch && grove env tui`,
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
