package cmd

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/grovetools/core/tui/embed"
	"github.com/spf13/cobra"

	envtui "github.com/grovetools/grove/pkg/tui/env"
)

func newEnvTUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the interactive environment panel",
		Long: `Open the interactive environment panel.

The TUI is a single-screen, live-refreshing grid that mirrors the web
dashboard served by the global grove daemon. It shows the current ecosystem's
worktrees with their running state, shared infra, and orphaned environments.
Launch it from an ecosystem root or from any worktree — the grid renders the
whole ecosystem and highlights the worktree you launched from.

Keys: q quit · r refresh · d open the browser dashboard.

To use the CLI companion without opening a TUI, run
'grove env ecosystem [--json]' from the same directory.

Worktrees appear regardless of layout. The legacy layout nests them under
'.grove-worktrees/' inside the repo; sibling-workspace (ecosystem) worktrees
created with 'flow plan init --sibling-workspaces' live under the grove data
dir (~/.local/share/grove/worktrees/<repo>-<hash>/<name>).`,
		Example: `  # From an ecosystem root
  cd ~/code/my-ecosystem && grove env tui

  # From a legacy in-repo worktree
  cd ~/code/my-ecosystem/.grove-worktrees/my-branch && grove env tui

  # From a sibling-workspace (XDG) worktree
  cd ~/.local/share/grove/worktrees/my-ecosystem-1a2b3c4d/my-branch && grove env tui`,
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
	if focus != nil && focus.Kind == workspace.KindEcosystemRoot {
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
