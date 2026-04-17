package cmd

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/tui/embed"
	"github.com/grovetools/grove/pkg/keys"
	keystui "github.com/grovetools/grove/pkg/tui/keys"
)

// runKeysTUI launches the interactive keybindings browser.
func runKeysTUI() error {
	cfg, err := config.LoadDefault()
	if err != nil {
		cfg = &config.Config{}
	}

	bindings, _ := keys.Aggregate(cfg)

	// Connect to daemon for config reload events.
	client := daemon.NewWithAutoStart()
	var stream <-chan daemon.StateUpdate
	if client.IsRunning() {
		stream, _ = client.StreamState(context.Background())
	}

	m := keystui.New(cfg, bindings, stream)

	_, err = embed.RunStandalone(m, tea.WithAltScreen())
	return err
}
