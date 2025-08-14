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

Examples:
  grove add-repo analyzer --alias az --description "Code analysis tool"
  grove add-repo fizzbuzz --alias fizz --description "Fizzbuzz implementation"
  
  # Use different templates:
  grove add-repo myrust --template maturin --alias mr
  grove add-repo myapp --template react-ts --alias ma
  
  # Use GitHub repository as template:
  grove add-repo mytool --template mattsolo1/grove-project-tmpl-rust --alias mt
  grove add-repo mylib --template https://github.com/user/template-repo.git`,
		Args: cobra.ExactArgs(1),
		RunE: runAddRepo,
	}

	cmd.Flags().StringVarP(&addRepoAlias, "alias", "a", "", "Binary alias (e.g., 'ct' for grove-context)")
	cmd.Flags().StringVarP(&addRepoDescription, "description", "d", "", "Repository description")
	cmd.Flags().BoolVar(&addRepoSkipGitHub, "skip-github", false, "Skip GitHub repository creation")
	cmd.Flags().BoolVar(&addRepoDryRun, "dry-run", false, "Preview operations without executing")
	cmd.Flags().BoolVar(&addRepoStageChanges, "stage-ecosystem", false, "Stage ecosystem changes in git")
	cmd.Flags().StringVar(&addRepoTemplate, "template", "go", "Template to use (go, maturin, react-ts, path/URL, or GitHub repo like 'owner/repo')")

	return cmd
}

var templateAliases = map[string]string{
	"go":       "/Users/solom4/Code/grove-ecosystem/grove-project-tmpl-go",       // For now, use absolute path
	"maturin":  "/Users/solom4/Code/grove-ecosystem/grove-project-tmpl-maturin",  // Python/Rust template
	"react-ts": "/Users/solom4/Code/grove-ecosystem/grove-project-tmpl-react-ts", // React TypeScript template
}

func resolveTemplate(spec string) string {
	if alias, ok := templateAliases[spec]; ok {
		return alias
	}
	return spec
}

func runAddRepo(cmd *cobra.Command, args []string) error {
	logger := cli.GetLogger(cmd)

	repoName := args[0]


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

	// Resolve template
	resolvedTemplate := resolveTemplate(addRepoTemplate)
	
	opts := repository.CreateOptions{
		Name:         repoName,
		Alias:        addRepoAlias,
		Description:  addRepoDescription,
		SkipGitHub:   addRepoSkipGitHub,
		DryRun:       addRepoDryRun,
		StageChanges: addRepoStageChanges,
		TemplatePath: resolvedTemplate,
	}

	logger.Infof("Creating new Grove repository: %s (alias: %s)", repoName, addRepoAlias)

	return creator.Create(opts)
}
