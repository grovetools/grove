package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/embed"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/setup"
	configtui "github.com/grovetools/grove/pkg/tui/config"
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

Supports both YAML (with comment preservation) and TOML formats.

With --json, the fully merged configuration is printed to stdout
instead of opening the TUI (no terminal required).`

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

	// Headless path: --json prints the merged config to stdout without a TTY.
	if jsonOutput, _ := cmd.Flags().GetBool("json"); jsonOutput {
		return printConfigJSON(layered)
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

// printConfigJSON prints the fully merged configuration as JSON to stdout.
// The config is round-tripped through YAML so that the emitted keys match the
// canonical config file key names (yaml/toml tags) rather than Go field names.
func printConfigJSON(layered *config.LayeredConfig) error {
	final := layered.Final
	if final == nil {
		final = &config.Config{}
	}

	yamlData, err := yaml.Marshal(final)
	if err != nil {
		return fmt.Errorf("failed to marshal configuration: %w", err)
	}

	var asMap map[string]interface{}
	if err := yaml.Unmarshal(yamlData, &asMap); err != nil {
		return fmt.Errorf("failed to convert configuration: %w", err)
	}

	jsonData, err := json.MarshalIndent(asMap, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode configuration as JSON: %w", err)
	}

	fmt.Println(string(jsonData))
	return nil
}
