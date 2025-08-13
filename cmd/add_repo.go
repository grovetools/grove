package cmd

import (
	"fmt"
	"strings"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/repository"
	"github.com/spf13/cobra"
)

var (
	addRepoAlias        string
	addRepoDescription  string
	addRepoSkipGitHub   bool
	addRepoDryRun       bool
	addRepoStageChanges bool
	addRepoTemplate     string
)

func init() {
	rootCmd.AddCommand(newAddRepoCmd())
}

func newAddRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-repo <repo-name>",
		Short: "Create a new Grove repository with standard structure",
		Long: `Create a new Grove repository with idiomatic structure, GitHub integration,
and automatic addition to grove-ecosystem as a submodule.

The repository name must start with 'grove-' prefix.

Example:
  grove meta add-repo grove-analyzer --alias az --description "Code analysis tool"`,
		Args: cobra.ExactArgs(1),
		RunE: runAddRepo,
	}

	cmd.Flags().StringVarP(&addRepoAlias, "alias", "a", "", "Binary alias (e.g., 'ct' for grove-context)")
	cmd.Flags().StringVarP(&addRepoDescription, "description", "d", "", "Repository description")
	cmd.Flags().BoolVar(&addRepoSkipGitHub, "skip-github", false, "Skip GitHub repository creation")
	cmd.Flags().BoolVar(&addRepoDryRun, "dry-run", false, "Preview operations without executing")
	cmd.Flags().BoolVar(&addRepoStageChanges, "stage-ecosystem", false, "Stage ecosystem changes in git")
	cmd.Flags().StringVar(&addRepoTemplate, "template", "", "Path to external template directory (hidden flag)")
	if err := cmd.Flags().MarkHidden("template"); err != nil {
		// This should never fail, but handle it anyway
		panic(fmt.Sprintf("Failed to mark template flag as hidden: %v", err))
	}

	return cmd
}

func runAddRepo(cmd *cobra.Command, args []string) error {
	logger := cli.GetLogger(cmd)

	repoName := args[0]

	// Validate repo name
	if !strings.HasPrefix(repoName, "grove-") {
		return fmt.Errorf("repository name must start with 'grove-' prefix")
	}

	// Derive alias if not provided
	if addRepoAlias == "" {
		// Extract alias from repo name (e.g., grove-context -> ct)
		parts := strings.Split(repoName, "-")
		if len(parts) >= 2 {
			// Take first letter of each part after "grove"
			var alias strings.Builder
			for i := 1; i < len(parts); i++ {
				if len(parts[i]) > 0 {
					alias.WriteByte(parts[i][0])
				}
			}
			addRepoAlias = alias.String()
		}
	}

	// Set default description if not provided
	if addRepoDescription == "" {
		addRepoDescription = fmt.Sprintf("A new Grove tool - %s", repoName)
	}

	creator := repository.NewCreator(logger)

	opts := repository.CreateOptions{
		Name:         repoName,
		Alias:        addRepoAlias,
		Description:  addRepoDescription,
		SkipGitHub:   addRepoSkipGitHub,
		DryRun:       addRepoDryRun,
		StageChanges: addRepoStageChanges,
		TemplatePath: addRepoTemplate,
	}

	logger.Infof("Creating new Grove repository: %s (alias: %s)", repoName, addRepoAlias)

	return creator.Create(opts)
}
