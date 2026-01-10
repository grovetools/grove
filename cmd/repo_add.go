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
	repoAddAlias       string
	repoAddDescription string
	repoAddDryRun      bool
	repoAddTemplate    string
	repoAddEcosystem   bool
)

// repoAddTemplateAliases maps template shortcuts to GitHub repository URLs
var repoAddTemplateAliases = map[string]string{
	"go":       "mattsolo1/grove-project-tmpl-go",
	"maturin":  "mattsolo1/grove-project-tmpl-maturin",
	"react-ts": "mattsolo1/grove-project-tmpl-react-ts",
}

func newRepoAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <repo-name>",
		Short: "Create a new local Grove repository",
		Long: `Create a new local Grove repository with standard structure.

This command creates a repository locally. The binary alias defaults to the
repository name if not specified.

Examples:
  # Create a local standalone repository
  grove repo add my-tool --description "My new tool"

  # Create and add to an existing ecosystem
  grove repo add my-tool --ecosystem

  # Specify a custom alias
  grove repo add grove-analyzer --alias az --description "Code analysis tool"

  # Use different templates
  grove repo add myrust --template maturin
  grove repo add myapp --template react-ts

  # Use GitHub repository as template
  grove repo add mytool --template mattsolo1/grove-project-tmpl-rust`,
		Args: cobra.ExactArgs(1),
		RunE: runRepoAdd,
	}

	cmd.Flags().StringVarP(&repoAddAlias, "alias", "a", "", "Binary alias (defaults to repo name)")
	cmd.Flags().StringVarP(&repoAddDescription, "description", "d", "", "Repository description")
	cmd.Flags().BoolVar(&repoAddDryRun, "dry-run", false, "Preview operations without executing")
	cmd.Flags().StringVar(&repoAddTemplate, "template", "go", "Template: go, maturin, react-ts, or GitHub repo (e.g., owner/repo)")
	cmd.Flags().BoolVar(&repoAddEcosystem, "ecosystem", false, "Add repository to an existing Grove ecosystem as a submodule")

	return cmd
}

// resolveRepoAddTemplate resolves template specification to a path or URL
func resolveRepoAddTemplate(spec string, ecosystem bool) string {
	// Check if it's a template alias
	if alias, ok := repoAddTemplateAliases[spec]; ok {
		// Only use local templates when in ecosystem mode
		if ecosystem {
			rootDir, err := workspace.FindEcosystemRoot("")
			if err == nil {
				localTemplateName := strings.TrimPrefix(alias, "mattsolo1/")
				localPath := filepath.Join(rootDir, localTemplateName)
				if _, err := os.Stat(localPath); err == nil {
					return localPath
				}
			}
		}
		return alias
	}
	return spec
}

func runRepoAdd(cmd *cobra.Command, args []string) error {
	logger := logging.NewLogger("repo-add")

	repoName := args[0]

	// Derive alias if not provided - default to repo name
	if repoAddAlias == "" {
		repoAddAlias = repoName
	}

	// Set default description if not provided
	if repoAddDescription == "" {
		repoAddDescription = fmt.Sprintf("A new Grove tool - %s", repoName)
	}

	creator := repository.NewCreator(logger.Logger)

	// Resolve template
	resolvedTemplate := resolveRepoAddTemplate(repoAddTemplate, repoAddEcosystem)

	opts := repository.CreateOptions{
		Name:         repoName,
		Alias:        repoAddAlias,
		Description:  repoAddDescription,
		DryRun:       repoAddDryRun,
		TemplatePath: resolvedTemplate,
		Ecosystem:    repoAddEcosystem,
	}

	logger.Infof("Creating new local Grove repository: %s (alias: %s)", repoName, repoAddAlias)

	_, err := creator.CreateLocal(opts)
	return err
}
