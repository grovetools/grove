package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/grovetools/core/config"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newInitCmd())
}

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init [name]",
		Short: "Initialize a grove.toml for the current project",
		Long: `Creates a grove.toml configuration file for the current project.

By default, the config is written to the project's notebook workspace directory
(resolved from the grove and notebook configuration). This keeps project config
centralized in the notebook rather than scattered in each repo.

Use --local to write grove.toml directly in the current directory instead.

Examples:
  # Initialize config in the notebook (default)
  grove init

  # Initialize with a custom project name
  grove init my-project

  # Initialize config locally in the current directory
  grove init --local`,
		Args: cobra.MaximumNArgs(1),
		RunE: runInit,
	}

	cmd.Flags().Bool("local", false, "Write grove.toml in the current directory instead of the notebook")

	return cmd
}

func runInit(cmd *cobra.Command, args []string) error {
	local, _ := cmd.Flags().GetBool("local")

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	projectName := filepath.Base(cwd)
	if len(args) > 0 {
		projectName = args[0]
	}

	var targetDir string

	if local {
		targetDir = cwd
	} else {
		nbDir, _, nbErr := config.ResolveNotebookDir(cwd)
		if nbErr != nil {
			return fmt.Errorf("cannot resolve notebook directory: %w\n\nUse --local to write grove.toml in the current directory instead", nbErr)
		}
		targetDir = nbDir
	}

	targetPath := filepath.Join(targetDir, "grove.toml")

	// Check if config already exists
	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("grove.toml already exists at %s", targetPath)
	}

	// Create the directory if needed
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", targetDir, err)
	}

	content := fmt.Sprintf("name = %q\n", projectName)

	if err := os.WriteFile(targetPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("failed to write grove.toml: %w", err)
	}

	if local {
		fmt.Printf("Created %s\n", targetPath)
	} else {
		fmt.Printf("Created %s\n", targetPath)
		fmt.Printf("  Notebook config for project %q\n", projectName)
	}

	return nil
}
