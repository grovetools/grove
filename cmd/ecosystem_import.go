package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var (
	ecosystemImportPath string
)

func newEcosystemImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import <repo>",
		Short: "Import an existing repository into the ecosystem",
		Long: `Import an existing repository into the ecosystem as a git submodule.

The repo can be:
- A local path (../my-repo or /path/to/repo)
- A GitHub shorthand (user/repo)
- A full Git URL (https://github.com/user/repo.git)

Examples:
  # Import from local path
  grove ecosystem import ../my-existing-tool

  # Import from GitHub
  grove ecosystem import grovetools/grove-context

  # Import with custom directory name
  grove ecosystem import grovetools/grove-context --path vendor/context`,
		Args: cobra.ExactArgs(1),
		RunE: runEcosystemImport,
	}

	cmd.Flags().StringVar(&ecosystemImportPath, "path", "", "Custom path for the submodule")

	return cmd
}

func runEcosystemImport(cmd *cobra.Command, args []string) error {
	repo := args[0]

	// Check we're in an ecosystem root (grove.yml with workspaces key)
	if err := validateEcosystemRoot(); err != nil {
		return err
	}

	// Determine the Git URL and target path
	var gitURL string
	var targetPath string

	if isLocalPath(repo) {
		// Local path - convert to absolute for submodule
		absPath, err := filepath.Abs(repo)
		if err != nil {
			return fmt.Errorf("failed to resolve path: %w", err)
		}
		gitURL = absPath
		if ecosystemImportPath != "" {
			targetPath = ecosystemImportPath
		} else {
			targetPath = filepath.Base(absPath)
		}
	} else if isGitHubShorthand(repo) {
		// GitHub shorthand: user/repo
		gitURL = fmt.Sprintf("https://github.com/%s.git", repo)
		if ecosystemImportPath != "" {
			targetPath = ecosystemImportPath
		} else {
			// Extract repo name from user/repo
			parts := strings.Split(repo, "/")
			targetPath = parts[len(parts)-1]
		}
	} else {
		// Assume it's a full Git URL
		gitURL = repo
		if ecosystemImportPath != "" {
			targetPath = ecosystemImportPath
		} else {
			// Extract repo name from URL
			targetPath = extractRepoName(repo)
		}
	}

	// Check if target already exists
	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("directory '%s' already exists", targetPath)
	}

	fmt.Printf("Adding %s as submodule...\n", targetPath)

	// Add as git submodule
	submoduleCmd := exec.Command("git", "submodule", "add", gitURL, targetPath)
	submoduleCmd.Stdout = os.Stdout
	submoduleCmd.Stderr = os.Stderr
	if err := submoduleCmd.Run(); err != nil {
		return fmt.Errorf("failed to add submodule: %w", err)
	}

	// Update go.work if it exists and the repo has a go.mod
	goWorkPath := "go.work"
	goModPath := filepath.Join(targetPath, "go.mod")
	if _, err := os.Stat(goWorkPath); err == nil {
		if _, err := os.Stat(goModPath); err == nil {
			if err := addToGoWork(targetPath); err != nil {
				fmt.Printf("Warning: failed to update go.work: %v\n", err)
			} else {
				fmt.Println("Updated go.work")
			}
		}
	}

	fmt.Printf("\nImported %s into ecosystem\n", targetPath)
	return nil
}

func isLocalPath(s string) bool {
	// Starts with ./ or ../ or / or is an absolute path
	return strings.HasPrefix(s, "./") ||
		strings.HasPrefix(s, "../") ||
		strings.HasPrefix(s, "/") ||
		(len(s) > 1 && s[1] == ':') // Windows absolute path
}

func isGitHubShorthand(s string) bool {
	// Pattern: user/repo (no protocol, no .git)
	if strings.Contains(s, "://") || strings.HasSuffix(s, ".git") {
		return false
	}
	parts := strings.Split(s, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

func extractRepoName(url string) string {
	// Remove .git suffix
	url = strings.TrimSuffix(url, ".git")
	// Get last path component
	parts := strings.Split(url, "/")
	return parts[len(parts)-1]
}

func addToGoWork(repoPath string) error {
	content, err := os.ReadFile("go.work")
	if err != nil {
		return err
	}

	// Check if already present
	newEntry := "./" + repoPath
	if strings.Contains(string(content), newEntry) {
		return nil
	}

	// Find the use block and add the entry
	lines := strings.Split(string(content), "\n")
	var newLines []string
	inUseBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "use (" {
			inUseBlock = true
			newLines = append(newLines, line)
			continue
		}

		if inUseBlock && trimmed == ")" {
			// Add new entry before closing paren
			newLines = append(newLines, fmt.Sprintf("\t%s", newEntry))
			inUseBlock = false
		}

		newLines = append(newLines, line)
	}

	return os.WriteFile("go.work", []byte(strings.Join(newLines, "\n")), 0644)
}
