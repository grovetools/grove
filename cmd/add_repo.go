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
	addRepoPushToGitHub bool
	addRepoDryRun       bool
	addRepoTemplate     string
	addRepoEcosystem    bool
	addRepoVisibility   string
)

func init() {
	rootCmd.AddCommand(newAddRepoCmd())
}

func newAddRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-repo <repo-name>",
		Short: "Create a new Grove repository with standard structure",
		Long: `Create a new Grove repository with standard structure.

By default, this creates a local-only repository in the current directory.
Use --github to create a GitHub repository and push the code.
Use --ecosystem to add it to an existing Grove ecosystem (monorepo).

The binary alias defaults to the repository name if not specified.

Examples:
  # Create a local-only standalone repository:
  grove add-repo my-tool --description "My new tool"

  # Create and push to GitHub (private by default):
  grove add-repo my-tool --description "My new tool" --github

  # Create a public GitHub repository:
  grove add-repo my-tool --github --repo-visibility=public

  # Add to an existing ecosystem:
  grove add-repo my-tool --description "My new tool" --ecosystem

  # Specify a custom alias:
  grove add-repo grove-analyzer --alias az --description "Code analysis tool"

  # Use different templates:
  grove add-repo myrust --template maturin
  grove add-repo myapp --template react-ts

  # Use GitHub repository as template:
  grove add-repo mytool --template mattsolo1/grove-project-tmpl-rust
  grove add-repo mylib --template https://github.com/user/template-repo.git`,
		Args: cobra.ExactArgs(1),
		RunE: runAddRepo,
	}

	cmd.Flags().StringVarP(&addRepoAlias, "alias", "a", "", "Binary alias (defaults to repo name)")
	cmd.Flags().StringVarP(&addRepoDescription, "description", "d", "", "Repository description")
	cmd.Flags().BoolVar(&addRepoPushToGitHub, "github", false, "Create GitHub repository and push code")
	cmd.Flags().BoolVar(&addRepoDryRun, "dry-run", false, "Preview operations without executing")
	cmd.Flags().StringVar(&addRepoTemplate, "template", "go", "Template to use (go, maturin, react-ts, path/URL, or GitHub repo like 'owner/repo')")
	cmd.Flags().BoolVar(&addRepoEcosystem, "ecosystem", false, "Add repository to an existing Grove ecosystem as a submodule")
	cmd.Flags().StringVar(&addRepoVisibility, "repo-visibility", "private", "GitHub repository visibility: public or private (only with --github)")

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

	// Validate --repo-visibility value
	if addRepoVisibility != "public" && addRepoVisibility != "private" {
		return fmt.Errorf("--repo-visibility must be 'public' or 'private', got '%s'", addRepoVisibility)
	}

	// Warn if --repo-visibility is set but --github is not
	visibilityFlagChanged := cmd.Flags().Changed("repo-visibility")
	if visibilityFlagChanged && !addRepoPushToGitHub {
		logger.Warn("--repo-visibility is ignored without --github")
	}

	// Derive alias if not provided - default to repo name
	if addRepoAlias == "" {
		addRepoAlias = repoName
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
		SkipGitHub:   !addRepoPushToGitHub, // Invert: --github flag enables GitHub, default is local-only
		DryRun:       addRepoDryRun,
		TemplatePath: resolvedTemplate,
		Ecosystem:    addRepoEcosystem,
		Public:       addRepoVisibility == "public",
	}

	logger.Infof("Creating new Grove repository: %s (alias: %s)", repoName, addRepoAlias)

	return creator.Create(opts)
}
