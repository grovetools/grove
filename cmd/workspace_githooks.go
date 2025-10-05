package cmd

import (
	"fmt"

	"github.com/mattsolo1/grove-core/git"
	"github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-meta/pkg/discovery"
	"github.com/mattsolo1/grove-meta/pkg/githooks"
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
			logger := logging.NewLogger("ws-githooks")
			pretty := logging.NewPrettyLogger()

			// Find the root directory
			rootDir, err := workspace.FindEcosystemRoot("")
			if err != nil {
				return fmt.Errorf("failed to find workspace root: %w", err)
			}

			// Discover all workspaces
			projects, err := discovery.DiscoverProjects()
			if err != nil {
				return fmt.Errorf("failed to discover workspaces: %w", err)
			}
			var workspaces []string
			for _, p := range projects {
				workspaces = append(workspaces, p.Path)
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
			pretty.Blank()
			pretty.Section("Git hooks installation summary")
			pretty.Success(fmt.Sprintf("Installed successfully: %d repositories", len(succeeded)))
			if len(failed) > 0 {
				pretty.ErrorPretty(fmt.Sprintf("Failed: %d repositories", len(failed)), nil)
				for _, path := range failed {
					pretty.InfoPretty(fmt.Sprintf("   - %s", path))
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
			logger := logging.NewLogger("ws-githooks")
			pretty := logging.NewPrettyLogger()

			// Find the root directory
			rootDir, err := workspace.FindEcosystemRoot("")
			if err != nil {
				return fmt.Errorf("failed to find workspace root: %w", err)
			}

			// Get the git root in case we're in a submodule
			gitRoot, err := git.GetGitRoot(rootDir)
			if err != nil {
				gitRoot = rootDir // Fall back to rootDir if git root not found
			}

			// Discover all workspaces
			projects, err := discovery.DiscoverProjects()
			if err != nil {
				return fmt.Errorf("failed to discover workspaces: %w", err)
			}
			var workspaces []string
			for _, p := range projects {
				workspaces = append(workspaces, p.Path)
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
			pretty.Blank()
			pretty.Section("Git hooks uninstallation summary")
			pretty.Success(fmt.Sprintf("Uninstalled successfully: %d repositories", len(succeeded)))
			if len(failed) > 0 {
				pretty.ErrorPretty(fmt.Sprintf("Failed: %d repositories", len(failed)), nil)
				for _, path := range failed {
					pretty.InfoPretty(fmt.Sprintf("   - %s", path))
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
