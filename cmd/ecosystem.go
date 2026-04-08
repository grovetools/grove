package cmd

import (
	"fmt"
	"os"

	"github.com/grovetools/core/config"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newEcosystemCmd())
}

func newEcosystemCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ecosystem",
		Short: "Manage Grove ecosystems",
		Long: `Manage Grove ecosystems (monorepos).

Commands:
  init     Create a new Grove ecosystem
  import   Import an existing repository into the ecosystem
  list     List repositories in the ecosystem

Examples:
  # Create a new ecosystem
  grove ecosystem init

  # Import an existing repo as submodule
  grove ecosystem import ../my-existing-tool
  grove ecosystem import github.com/user/repo

  # List repos in the ecosystem
  grove ecosystem list`,
	}

	cmd.AddCommand(newEcosystemInitCmd())
	cmd.AddCommand(newEcosystemImportCmd())
	cmd.AddCommand(newEcosystemListCmd())

	return cmd
}

// groveConfigFiles lists all valid Grove config file names
var groveConfigFiles = []string{"grove.toml", "grove.yml", "grove.yaml", ".grove.yml", ".grove.yaml"}

// findGroveConfig finds the grove config file in the current directory
func findGroveConfig() (string, error) {
	for _, name := range groveConfigFiles {
		if _, err := os.Stat(name); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("no grove.yml found")
}

// validateEcosystemRoot checks that we're in an ecosystem root
// (a directory with a grove config containing a non-empty `workspaces` field).
func validateEcosystemRoot() error {
	configFile, err := findGroveConfig()
	if err != nil {
		return fmt.Errorf("not in a Grove ecosystem (grove.toml/grove.yml not found)")
	}

	cfg, err := config.Load(configFile)
	if err != nil {
		return fmt.Errorf("failed to parse %s: %w", configFile, err)
	}
	if len(cfg.Workspaces) == 0 {
		return fmt.Errorf("not in an ecosystem root (%s has no 'workspaces' field)\n\nThis directory is a project, not an ecosystem.\nTo create an ecosystem, run: grove ecosystem init", configFile)
	}

	return nil
}
