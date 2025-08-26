package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/repository"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

var (
	addRepoAlias        string
	addRepoDescription  string
	addRepoSkipGitHub   bool
	addRepoDryRun       bool
	addRepoStageChanges bool
	addRepoTemplate     string
	addRepoUpdateEcosystem bool
)

func init() {
	rootCmd.AddCommand(newAddRepoCmd())
}

func newAddRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-repo <repo-name>",
		Short: "Create a new Grove repository with standard structure",
		Long: `Create a new Grove repository with idiomatic structure and optional GitHub integration.

By default, this creates a standalone repository. Use --update-ecosystem to add it as a
submodule to grove-ecosystem.

Examples:
  # Create standalone repository:
  grove add-repo analyzer --alias az --description "Code analysis tool"
  
  # Create and add to ecosystem:
  grove add-repo fizzbuzz --alias fizz --description "Fizzbuzz implementation" --update-ecosystem
  
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
	cmd.Flags().BoolVar(&addRepoUpdateEcosystem, "update-ecosystem", false, "Add repository to grove-ecosystem as a submodule")

	return cmd
}

var templateAliases = map[string]string{
	"go":       "grove-project-tmpl-go",       // Go template
	"maturin":  "grove-project-tmpl-maturin",  // Python/Rust template  
	"react-ts": "grove-project-tmpl-react-ts", // React TypeScript template
}

func resolveTemplate(spec string) string {
	if alias, ok := templateAliases[spec]; ok {
		// Find grove ecosystem root and resolve template path relative to it
		rootDir, err := workspace.FindRoot("")
		if err != nil {
			// Fall back to relative path if we can't find root
			return alias
		}
		return filepath.Join(rootDir, alias)
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
		UpdateEcosystem: addRepoUpdateEcosystem,
	}

	logger.Infof("Creating new Grove repository: %s (alias: %s)", repoName, addRepoAlias)

	return creator.Create(opts)
}
