package cmd

import (
	"fmt"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/git"
	"github.com/mattsolo1/grove-meta/pkg/githooks"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

func newWorkspaceGitHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "git-hooks",
		Short: "Manage Git hooks across all workspaces",
		Long:  "Install or uninstall Git hooks that enforce conventional commits across all repositories in the workspace.",
	}

	cmd.AddCommand(newWorkspaceGitHooksInstallCmd())
	cmd.AddCommand(newWorkspaceGitHooksUninstallCmd())

	return cmd
}

func newWorkspaceGitHooksInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install Git hooks in all workspace repositories",
		Long:  "Installs commit-msg hooks that enforce conventional commit format in all repositories within the workspace.",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := cli.GetLogger(cmd)

			// Find the root directory
			rootDir, err := workspace.FindRoot("")
			if err != nil {
				return fmt.Errorf("failed to find workspace root: %w", err)
			}

			// Discover all workspaces
			workspaces, err := workspace.Discover(rootDir)
			if err != nil {
				return fmt.Errorf("failed to discover workspaces: %w", err)
			}

			// Include the root repository itself
			allPaths := []string{rootDir}
			for _, ws := range workspaces {
				allPaths = append(allPaths, ws)
			}

			// Track successes and failures
			var succeeded []string
			var failed []string

			// Install hooks in each repository
			for _, path := range allPaths {
				logger.WithField("path", path).Info("Installing git hooks")
				
				if err := githooks.Install(path); err != nil {
					logger.WithError(err).WithField("path", path).Error("Failed to install git hooks")
					failed = append(failed, path)
				} else {
					succeeded = append(succeeded, path)
				}
			}

			// Print summary
			fmt.Printf("\nGit hooks installation summary:\n")
			fmt.Printf("✅ Installed successfully: %d repositories\n", len(succeeded))
			if len(failed) > 0 {
				fmt.Printf("❌ Failed: %d repositories\n", len(failed))
				for _, path := range failed {
					fmt.Printf("   - %s\n", path)
				}
			}

			if len(failed) > 0 {
				return fmt.Errorf("failed to install hooks in %d repositories", len(failed))
			}

			return nil
		},
	}

	return cmd
}

func newWorkspaceGitHooksUninstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall Git hooks from all workspace repositories",
		Long:  "Removes Grove-managed commit-msg hooks from all repositories within the workspace.",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := cli.GetLogger(cmd)

			// Find the root directory
			rootDir, err := workspace.FindRoot("")
			if err != nil {
				return fmt.Errorf("failed to find workspace root: %w", err)
			}

			// Get the git root in case we're in a submodule
			gitRoot, err := git.GetGitRoot(rootDir)
			if err != nil {
				gitRoot = rootDir // Fall back to rootDir if git root not found
			}

			// Discover all workspaces
			workspaces, err := workspace.Discover(rootDir)
			if err != nil {
				return fmt.Errorf("failed to discover workspaces: %w", err)
			}

			// Include the root repository itself
			allPaths := []string{gitRoot}
			for _, ws := range workspaces {
				allPaths = append(allPaths, ws)
			}

			// Track successes and failures
			var succeeded []string
			var failed []string

			// Uninstall hooks from each repository
			for _, path := range allPaths {
				logger.WithField("path", path).Info("Uninstalling git hooks")
				
				if err := githooks.Uninstall(path); err != nil {
					logger.WithError(err).WithField("path", path).Error("Failed to uninstall git hooks")
					failed = append(failed, path)
				} else {
					succeeded = append(succeeded, path)
				}
			}

			// Print summary
			fmt.Printf("\nGit hooks uninstallation summary:\n")
			fmt.Printf("✅ Uninstalled successfully: %d repositories\n", len(succeeded))
			if len(failed) > 0 {
				fmt.Printf("❌ Failed: %d repositories\n", len(failed))
				for _, path := range failed {
					fmt.Printf("   - %s\n", path)
				}
			}

			if len(failed) > 0 {
				return fmt.Errorf("failed to uninstall hooks from %d repositories", len(failed))
			}

			return nil
		},
	}

	return cmd
}