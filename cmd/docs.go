package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

func newDocsCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("docs", "Manage documentation across the ecosystem")
	cmd.Long = "The 'docs' command provides tools for managing documentation across all discovered workspaces."

	cmd.AddCommand(newDocsGenerateCmd())

	return cmd
}

func newDocsGenerateCmd() *cobra.Command {
	var commit bool
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate documentation for all workspaces",
		Long: `Runs 'docgen generate' in each discovered workspace.
This command is useful for updating all documentation in a single step.`,
		Example: `  # Generate docs for all workspaces without committing
  grove docs generate

  # Generate docs and commit the changes in each workspace
  grove docs generate --commit`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := cli.GetLogger(cmd)

			// Find root directory
			rootDir, err := workspace.FindRoot("")
			if err != nil {
				return fmt.Errorf("failed to find workspace root: %w", err)
			}

			// Discover workspaces
			workspaces, err := workspace.Discover(rootDir)
			if err != nil {
				return fmt.Errorf("failed to discover workspaces: %w", err)
			}

			logger.Infof("Found %d workspaces. Generating documentation...", len(workspaces))

			var failedWorkspaces []string

			for _, wsPath := range workspaces {
				wsName := filepath.Base(wsPath)
				logger.Infof("Processing %s...", wsName)

				// Check if docgen is likely to be present (e.g., has a Makefile)
				if _, err := os.Stat(filepath.Join(wsPath, "Makefile")); os.IsNotExist(err) {
					logger.Warnf("Skipping %s: no Makefile found, docgen likely not configured.", wsName)
					continue
				}

				// Run 'docgen generate'
				docgenCmd := exec.Command("docgen", "generate")
				docgenCmd.Dir = wsPath
				output, err := docgenCmd.CombinedOutput()
				if err != nil {
					// Don't fail the entire run, just log the error
					logger.Errorf("Failed to generate docs for %s: %v\nOutput: %s", wsName, err, string(output))
					failedWorkspaces = append(failedWorkspaces, wsName)
					continue
				}

				if commit {
					// Check for changes
					statusCmd := exec.Command("git", "status", "--porcelain")
					statusCmd.Dir = wsPath
					statusOutput, _ := statusCmd.Output()
					if len(strings.TrimSpace(string(statusOutput))) == 0 {
						logger.Infof("No documentation changes in %s to commit.", wsName)
						continue
					}

					// Stage changes
					addCmd := exec.Command("git", "add", ".")
					addCmd.Dir = wsPath
					if err := addCmd.Run(); err != nil {
						logger.Errorf("Failed to stage changes in %s: %v", wsName, err)
						failedWorkspaces = append(failedWorkspaces, wsName)
						continue
					}

					// Commit changes
					commitCmd := exec.Command("git", "commit", "-m", "docs: generate documentation")
					commitCmd.Dir = wsPath
					if err := commitCmd.Run(); err != nil {
						logger.Errorf("Failed to commit changes in %s: %v", wsName, err)
						failedWorkspaces = append(failedWorkspaces, wsName)
						continue
					}
					logger.Infof("Committed documentation changes for %s.", wsName)
				}
			}

			if len(failedWorkspaces) > 0 {
				return fmt.Errorf("documentation generation failed for: %s", strings.Join(failedWorkspaces, ", "))
			}

			logger.Info("✅ Documentation generation complete.")
			return nil
		},
	}

	cmd.Flags().BoolVar(&commit, "commit", false, "Commit changes after generating documentation")
	return cmd
}

