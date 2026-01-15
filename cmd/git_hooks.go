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
	var ecosystem bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install Git hooks in the current repository",
		Long:  "Installs a commit-msg hook that enforces conventional commit format in the current repository.\nUse --ecosystem to install across all repositories in the ecosystem.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if ecosystem {
				return installGitHooksEcosystem()
			}
			return installGitHooksSingle()
		},
	}

	cmd.Flags().BoolVar(&ecosystem, "ecosystem", false, "Install hooks across all repositories in the ecosystem")

	return cmd
}

func newGitHooksUninstallCmd() *cobra.Command {
	var ecosystem bool

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall Git hooks from the current repository",
		Long:  "Removes the Grove-managed commit-msg hook from the current repository.\nUse --ecosystem to uninstall from all repositories in the ecosystem.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if ecosystem {
				return uninstallGitHooksEcosystem()
			}
			return uninstallGitHooksSingle()
		},
	}

	cmd.Flags().BoolVar(&ecosystem, "ecosystem", false, "Uninstall hooks from all repositories in the ecosystem")

	return cmd
}

func installGitHooksSingle() error {
	gitRoot, err := git.GetGitRoot(".")
	if err != nil {
		return fmt.Errorf("could not find git root: %w. Are you in a git repository?", err)
	}

	if err := githooks.Install(gitRoot); err != nil {
		return fmt.Errorf("failed to install git hooks: %w", err)
	}

	fmt.Printf("✅ Conventional commit hook installed successfully in %s\n", gitRoot)
	return nil
}

func uninstallGitHooksSingle() error {
	gitRoot, err := git.GetGitRoot(".")
	if err != nil {
		return fmt.Errorf("could not find git root: %w. Are you in a git repository?", err)
	}

	if err := githooks.Uninstall(gitRoot); err != nil {
		return fmt.Errorf("failed to uninstall git hooks: %w", err)
	}

	fmt.Printf("✅ Conventional commit hook uninstalled successfully from %s\n", gitRoot)
	return nil
}

func installGitHooksEcosystem() error {
	logger := logging.NewLogger("git-hooks")
	pretty := logging.NewPrettyLogger()

	rootDir, err := workspace.FindEcosystemRoot("")
	if err != nil {
		return fmt.Errorf("failed to find ecosystem root: %w", err)
	}

	projects, err := discovery.DiscoverProjects()
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
	}

	// Include root + all workspaces
	allPaths := []string{rootDir}
	for _, p := range projects {
		allPaths = append(allPaths, p.Path)
	}

	var succeeded, failed []string

	for _, path := range allPaths {
		logger.WithField("path", path).Info("Installing git hooks")

		if err := githooks.Install(path); err != nil {
			logger.WithError(err).WithField("path", path).Error("Failed to install git hooks")
			failed = append(failed, path)
		} else {
			succeeded = append(succeeded, path)
		}
	}

	pretty.Blank()
	pretty.Section("Git hooks installation summary")
	pretty.Success(fmt.Sprintf("Installed successfully: %d repositories", len(succeeded)))
	if len(failed) > 0 {
		pretty.ErrorPretty(fmt.Sprintf("Failed: %d repositories", len(failed)), nil)
		for _, path := range failed {
			pretty.InfoPretty(fmt.Sprintf("   - %s", path))
		}
		return fmt.Errorf("failed to install hooks in %d repositories", len(failed))
	}

	return nil
}

func uninstallGitHooksEcosystem() error {
	logger := logging.NewLogger("git-hooks")
	pretty := logging.NewPrettyLogger()

	rootDir, err := workspace.FindEcosystemRoot("")
	if err != nil {
		return fmt.Errorf("failed to find ecosystem root: %w", err)
	}

	gitRoot, err := git.GetGitRoot(rootDir)
	if err != nil {
		gitRoot = rootDir
	}

	projects, err := discovery.DiscoverProjects()
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
	}

	// Include root + all workspaces
	allPaths := []string{gitRoot}
	for _, p := range projects {
		allPaths = append(allPaths, p.Path)
	}

	var succeeded, failed []string

	for _, path := range allPaths {
		logger.WithField("path", path).Info("Uninstalling git hooks")

		if err := githooks.Uninstall(path); err != nil {
			logger.WithError(err).WithField("path", path).Error("Failed to uninstall git hooks")
			failed = append(failed, path)
		} else {
			succeeded = append(succeeded, path)
		}
	}

	pretty.Blank()
	pretty.Section("Git hooks uninstallation summary")
	pretty.Success(fmt.Sprintf("Uninstalled successfully: %d repositories", len(succeeded)))
	if len(failed) > 0 {
		pretty.ErrorPretty(fmt.Sprintf("Failed: %d repositories", len(failed)), nil)
		for _, path := range failed {
			pretty.InfoPretty(fmt.Sprintf("   - %s", path))
		}
		return fmt.Errorf("failed to uninstall hooks from %d repositories", len(failed))
	}

	return nil
}
