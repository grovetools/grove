package cmd

import (
	"fmt"

	"github.com/mattsolo1/grove-core/git"
	"github.com/mattsolo1/grove-meta/pkg/githooks"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newGitHooksCmd())
}

func newGitHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "git-hooks",
		Short: "Manage Git hooks for Grove repositories",
		Long:  "Install or uninstall Git hooks that enforce conventional commits in your repository.",
	}

	cmd.AddCommand(newGitHooksInstallCmd())
	cmd.AddCommand(newGitHooksUninstallCmd())

	return cmd
}

func newGitHooksInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install Git hooks in the current repository",
		Long:  "Installs a commit-msg hook that enforces conventional commit format in the current repository.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Find the git root of the current directory
			gitRoot, err := git.GetGitRoot(".")
			if err != nil {
				return fmt.Errorf("could not find git root: %w. Are you in a git repository?", err)
			}

			// Install the hook
			if err := githooks.Install(gitRoot); err != nil {
				return fmt.Errorf("failed to install git hooks: %w", err)
			}

			fmt.Printf("✅ Conventional commit hook installed successfully in %s\n", gitRoot)
			return nil
		},
	}

	return cmd
}

func newGitHooksUninstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall Git hooks from the current repository",
		Long:  "Removes the Grove-managed commit-msg hook from the current repository.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Find the git root of the current directory
			gitRoot, err := git.GetGitRoot(".")
			if err != nil {
				return fmt.Errorf("could not find git root: %w. Are you in a git repository?", err)
			}

			// Uninstall the hook
			if err := githooks.Uninstall(gitRoot); err != nil {
				return fmt.Errorf("failed to uninstall git hooks: %w", err)
			}

			fmt.Printf("✅ Conventional commit hook uninstalled successfully from %s\n", gitRoot)
			return nil
		},
	}

	return cmd
}