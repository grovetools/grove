package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

var (
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00")).Bold(true)
	failStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff4444")).Bold(true)
)

func NewWorkspaceSecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Manage GitHub repository secrets across all workspaces",
		Long:  "Set, update, or delete GitHub repository secrets for all discovered workspaces using the GitHub CLI",
	}

	cmd.AddCommand(newWorkspaceSecretsSetCmd())
	cmd.AddCommand(newWorkspaceSecretsDeleteCmd())
	cmd.AddCommand(newWorkspaceSecretsListCmd())

	return cmd
}

func newWorkspaceSecretsSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set SECRET_NAME [SECRET_VALUE]",
		Short: "Set a secret across all workspace repositories",
		Long: `Set a GitHub repository secret across all discovered workspace repositories.
If SECRET_VALUE is not provided, the secret will be read from stdin.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: runWorkspaceSecretsSet,
	}

	cmd.Flags().StringP("file", "f", "", "Read secret value from file")
	cmd.Flags().StringArrayP("include", "i", []string{}, "Only include workspaces matching pattern (can be specified multiple times)")
	cmd.Flags().StringArrayP("exclude", "e", []string{}, "Exclude workspaces matching pattern (can be specified multiple times)")

	return cmd
}

func newWorkspaceSecretsDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete SECRET_NAME",
		Short: "Delete a secret from all workspace repositories",
		Long:  "Delete a GitHub repository secret from all discovered workspace repositories",
		Args:  cobra.ExactArgs(1),
		RunE:  runWorkspaceSecretsDelete,
	}

	cmd.Flags().StringArrayP("include", "i", []string{}, "Only include workspaces matching pattern (can be specified multiple times)")
	cmd.Flags().StringArrayP("exclude", "e", []string{}, "Exclude workspaces matching pattern (can be specified multiple times)")

	return cmd
}

func newWorkspaceSecretsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List secrets for all workspace repositories",
		Long:  "List GitHub repository secrets for all discovered workspace repositories",
		Args:  cobra.NoArgs,
		RunE:  runWorkspaceSecretsList,
	}

	return cmd
}

func runWorkspaceSecretsSet(cmd *cobra.Command, args []string) error {
	logger := cli.GetLogger(cmd)
	secretName := args[0]

	// Get secret value
	var secretValue string
	if file, _ := cmd.Flags().GetString("file"); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read secret from file: %w", err)
		}
		secretValue = string(data)
	} else if len(args) > 1 {
		secretValue = args[1]
	} else {
		// Read from stdin
		data, err := os.ReadFile(os.Stdin.Name())
		if err != nil {
			return fmt.Errorf("failed to read secret from stdin: %w", err)
		}
		secretValue = string(data)
	}

	// Get workspace filters
	includePatterns, _ := cmd.Flags().GetStringArray("include")
	excludePatterns, _ := cmd.Flags().GetStringArray("exclude")

	// Find root directory with workspaces
	rootDir, err := workspace.FindRoot("")
	if err != nil {
		return fmt.Errorf("failed to find workspace root: %w", err)
	}

	// Discover workspaces
	workspaces, err := workspace.Discover(rootDir)
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
	}

	// Filter workspaces
	filteredWorkspaces := filterWorkspaces(workspaces, rootDir, includePatterns, excludePatterns)

	if len(filteredWorkspaces) == 0 {
		return fmt.Errorf("no workspaces matched the filters")
	}

	logger.WithField("count", len(filteredWorkspaces)).Info("Setting secret across workspaces")

	// Process workspaces concurrently
	type result struct {
		workspace string
		success   bool
		err       error
	}

	resultChan := make(chan result, len(filteredWorkspaces))
	var wg sync.WaitGroup

	for _, ws := range filteredWorkspaces {
		wg.Add(1)
		go func(wsPath string) {
			defer wg.Done()
			wsName := workspace.GetWorkspaceName(wsPath, rootDir)

			// Get the repository URL
			cmd := exec.Command("git", "config", "--get", "remote.origin.url")
			cmd.Dir = wsPath
			output, err := cmd.Output()
			if err != nil {
				resultChan <- result{workspace: wsName, success: false, err: fmt.Errorf("failed to get repository URL")}
				return
			}

			// Extract owner/repo from URL
			repoURL := strings.TrimSpace(string(output))
			var owner, repo string
			if strings.HasPrefix(repoURL, "git@github.com:") {
				parts := strings.Split(strings.TrimPrefix(repoURL, "git@github.com:"), "/")
				if len(parts) == 2 {
					owner = parts[0]
					repo = strings.TrimSuffix(parts[1], ".git")
				}
			} else if strings.HasPrefix(repoURL, "https://github.com/") {
				parts := strings.Split(strings.TrimPrefix(repoURL, "https://github.com/"), "/")
				if len(parts) == 2 {
					owner = parts[0]
					repo = strings.TrimSuffix(parts[1], ".git")
				}
			}

			if owner == "" || repo == "" {
				resultChan <- result{workspace: wsName, success: false, err: fmt.Errorf("could not parse repository URL")}
				return
			}

			// Set the secret using gh CLI
			cmd = exec.Command("gh", "secret", "set", secretName, "--body", secretValue, "--repo", fmt.Sprintf("%s/%s", owner, repo))
			err = cmd.Run()

			resultChan <- result{
				workspace: wsName,
				success:   err == nil,
				err:       err,
			}
		}(ws)
	}

	// Wait for all goroutines to complete and close the channel
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect and display results
	var successCount, failCount int
	fmt.Println("\nSetting secret results:")
	fmt.Println(strings.Repeat("-", 50))

	for res := range resultChan {
		if res.success {
			fmt.Printf("%s %s\n", successStyle.Render("✓"), res.workspace)
			successCount++
		} else {
			errMsg := "unknown error"
			if res.err != nil {
				errMsg = res.err.Error()
			}
			fmt.Printf("%s %s: %s\n", failStyle.Render("✗"), res.workspace, errMsg)
			failCount++
		}
	}

	fmt.Println(strings.Repeat("-", 50))
	fmt.Printf("Summary: %s succeeded, %s failed\n",
		successStyle.Render(fmt.Sprintf("%d", successCount)),
		failStyle.Render(fmt.Sprintf("%d", failCount)))

	if failCount > 0 {
		return fmt.Errorf("failed to set secret in %d repositories", failCount)
	}

	return nil
}

func runWorkspaceSecretsDelete(cmd *cobra.Command, args []string) error {
	logger := cli.GetLogger(cmd)
	secretName := args[0]

	// Get workspace filters
	includePatterns, _ := cmd.Flags().GetStringArray("include")
	excludePatterns, _ := cmd.Flags().GetStringArray("exclude")

	// Find root directory with workspaces
	rootDir, err := workspace.FindRoot("")
	if err != nil {
		return fmt.Errorf("failed to find workspace root: %w", err)
	}

	// Discover workspaces
	workspaces, err := workspace.Discover(rootDir)
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
	}

	// Filter workspaces
	filteredWorkspaces := filterWorkspaces(workspaces, rootDir, includePatterns, excludePatterns)

	if len(filteredWorkspaces) == 0 {
		return fmt.Errorf("no workspaces matched the filters")
	}

	logger.WithField("count", len(filteredWorkspaces)).Info("Deleting secret from workspaces")

	// Process workspaces concurrently
	type result struct {
		workspace string
		success   bool
		err       error
	}

	resultChan := make(chan result, len(filteredWorkspaces))
	var wg sync.WaitGroup

	for _, ws := range filteredWorkspaces {
		wg.Add(1)
		go func(wsPath string) {
			defer wg.Done()
			wsName := workspace.GetWorkspaceName(wsPath, rootDir)

			// Get the repository URL
			cmd := exec.Command("git", "config", "--get", "remote.origin.url")
			cmd.Dir = wsPath
			output, err := cmd.Output()
			if err != nil {
				resultChan <- result{workspace: wsName, success: false, err: fmt.Errorf("failed to get repository URL")}
				return
			}

			// Extract owner/repo from URL
			repoURL := strings.TrimSpace(string(output))
			var owner, repo string
			if strings.HasPrefix(repoURL, "git@github.com:") {
				parts := strings.Split(strings.TrimPrefix(repoURL, "git@github.com:"), "/")
				if len(parts) == 2 {
					owner = parts[0]
					repo = strings.TrimSuffix(parts[1], ".git")
				}
			} else if strings.HasPrefix(repoURL, "https://github.com/") {
				parts := strings.Split(strings.TrimPrefix(repoURL, "https://github.com/"), "/")
				if len(parts) == 2 {
					owner = parts[0]
					repo = strings.TrimSuffix(parts[1], ".git")
				}
			}

			if owner == "" || repo == "" {
				resultChan <- result{workspace: wsName, success: false, err: fmt.Errorf("could not parse repository URL")}
				return
			}

			// Delete the secret using gh CLI
			cmd = exec.Command("gh", "secret", "delete", secretName, "--repo", fmt.Sprintf("%s/%s", owner, repo))
			err = cmd.Run()

			resultChan <- result{
				workspace: wsName,
				success:   err == nil,
				err:       err,
			}
		}(ws)
	}

	// Wait for all goroutines to complete and close the channel
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect and display results
	var successCount, failCount int
	fmt.Println("\nDeleting secret results:")
	fmt.Println(strings.Repeat("-", 50))

	for res := range resultChan {
		if res.success {
			fmt.Printf("%s %s\n", successStyle.Render("✓"), res.workspace)
			successCount++
		} else {
			errMsg := "unknown error"
			if res.err != nil {
				errMsg = res.err.Error()
			}
			fmt.Printf("%s %s: %s\n", failStyle.Render("✗"), res.workspace, errMsg)
			failCount++
		}
	}

	fmt.Println(strings.Repeat("-", 50))
	fmt.Printf("Summary: %s succeeded, %s failed\n",
		successStyle.Render(fmt.Sprintf("%d", successCount)),
		failStyle.Render(fmt.Sprintf("%d", failCount)))

	if failCount > 0 {
		return fmt.Errorf("failed to delete secret in %d repositories", failCount)
	}

	return nil
}

func runWorkspaceSecretsList(cmd *cobra.Command, args []string) error {
	logger := cli.GetLogger(cmd)

	// Find root directory with workspaces
	rootDir, err := workspace.FindRoot("")
	if err != nil {
		return fmt.Errorf("failed to find workspace root: %w", err)
	}

	// Discover workspaces
	workspaces, err := workspace.Discover(rootDir)
	if err != nil {
		return fmt.Errorf("failed to discover workspaces: %w", err)
	}

	logger.WithField("count", len(workspaces)).Info("Listing secrets for workspaces")

	// Process workspaces
	for _, ws := range workspaces {
		wsName := workspace.GetWorkspaceName(ws, rootDir)

		// Get the repository URL
		cmd := exec.Command("git", "config", "--get", "remote.origin.url")
		cmd.Dir = ws
		output, err := cmd.Output()
		if err != nil {
			fmt.Printf("\n%s %s: failed to get repository URL\n", failStyle.Render("✗"), wsName)
			continue
		}

		// Extract owner/repo from URL
		repoURL := strings.TrimSpace(string(output))
		var owner, repo string
		if strings.HasPrefix(repoURL, "git@github.com:") {
			parts := strings.Split(strings.TrimPrefix(repoURL, "git@github.com:"), "/")
			if len(parts) == 2 {
				owner = parts[0]
				repo = strings.TrimSuffix(parts[1], ".git")
			}
		} else if strings.HasPrefix(repoURL, "https://github.com/") {
			parts := strings.Split(strings.TrimPrefix(repoURL, "https://github.com/"), "/")
			if len(parts) == 2 {
				owner = parts[0]
				repo = strings.TrimSuffix(parts[1], ".git")
			}
		}

		if owner == "" || repo == "" {
			fmt.Printf("\n%s %s: could not parse repository URL\n", failStyle.Render("✗"), wsName)
			continue
		}

		// List secrets using gh CLI
		fmt.Printf("\n%s:\n", lipgloss.NewStyle().Bold(true).Render(wsName))
		cmd = exec.Command("gh", "secret", "list", "--repo", fmt.Sprintf("%s/%s", owner, repo))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err = cmd.Run()
		if err != nil {
			fmt.Printf("  %s Failed to list secrets: %v\n", failStyle.Render("✗"), err)
		}
	}

	return nil
}

// filterWorkspaces filters workspaces based on include/exclude patterns
func filterWorkspaces(workspaces []string, rootDir string, includePatterns, excludePatterns []string) []string {
	var filtered []string

	for _, ws := range workspaces {
		wsName := workspace.GetWorkspaceName(ws, rootDir)

		// Check exclude patterns first
		excluded := false
		for _, pattern := range excludePatterns {
			if strings.Contains(wsName, pattern) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}

		// Check include patterns (if any specified)
		if len(includePatterns) > 0 {
			included := false
			for _, pattern := range includePatterns {
				if strings.Contains(wsName, pattern) {
					included = true
					break
				}
			}
			if !included {
				continue
			}
		}

		filtered = append(filtered, ws)
	}

	return filtered
}
