package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/pkg/workspace"
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
	addRepoEcosystem    bool
	addRepoPublic       bool
)

func init() {
	rootCmd.AddCommand(newAddRepoCmd())
}

func newAddRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-repo <repo-name>",
		Short: "Create a new Grove repository with standard structure",
		Long: `Create a new Grove repository with idiomatic structure and optional GitHub integration.

By default, this creates a standalone repository in the current directory. Use --ecosystem to add it
to an existing Grove ecosystem (monorepo).

Examples:
  # Create standalone repository:
  grove add-repo analyzer --alias az --description "Code analysis tool"
  
  # Create and add to ecosystem:
  grove add-repo fizzbuzz --alias fizz --description "Fizzbuzz implementation" --ecosystem
  
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
	cmd.Flags().BoolVar(&addRepoEcosystem, "ecosystem", false, "Add repository to an existing Grove ecosystem as a submodule")
	cmd.Flags().BoolVar(&addRepoPublic, "public", false, "Create a public repository and skip private configuration")

	return cmd
}

var templateAliases = map[string]string{
	"go":       "mattsolo1/grove-project-tmpl-go",       // Go template
	"maturin":  "mattsolo1/grove-project-tmpl-maturin",  // Python/Rust template
	"react-ts": "mattsolo1/grove-project-tmpl-react-ts", // React TypeScript template
}

func resolveTemplate(spec string, ecosystem bool) string {
	// Check if it's a template alias
	if alias, ok := templateAliases[spec]; ok {
		// Only use local templates when in ecosystem mode
		if ecosystem {
			// Try to find grove ecosystem root
			rootDir, err := workspace.FindEcosystemRoot("")
			if err == nil {
				// We're in an ecosystem - check for local template
				localTemplateName := strings.TrimPrefix(alias, "mattsolo1/")
				localPath := filepath.Join(rootDir, localTemplateName)
				if _, err := os.Stat(localPath); err == nil {
					// Local template exists, use it
					return localPath
				}
			}
		}
		// In standalone mode or local template doesn't exist
		// Return the GitHub shorthand
		return alias
	}
	// Not an alias - return as-is (could be a path or URL)
	return spec
}

func runAddRepo(cmd *cobra.Command, args []string) error {
	logger := logging.NewLogger("add-repo")

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

	creator := repository.NewCreator(logger.Logger)

	// Resolve template
	resolvedTemplate := resolveTemplate(addRepoTemplate, addRepoEcosystem)

	opts := repository.CreateOptions{
		Name:         repoName,
		Alias:        addRepoAlias,
		Description:  addRepoDescription,
		SkipGitHub:   addRepoSkipGitHub,
		DryRun:       addRepoDryRun,
		StageChanges: addRepoStageChanges,
		TemplatePath: resolvedTemplate,
		Ecosystem:    addRepoEcosystem,
		Public:       addRepoPublic,
	}

	logger.Infof("Creating new Grove repository: %s (alias: %s)", repoName, addRepoAlias)

	return creator.Create(opts)
}
