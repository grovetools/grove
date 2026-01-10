package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newEcosystemListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List repositories in the ecosystem",
		Long: `List all repositories in the current Grove ecosystem.

Shows submodules and local directories that contain grove.yml files.

Examples:
  grove ecosystem list`,
		Args: cobra.NoArgs,
		RunE: runEcosystemList,
	}

	return cmd
}

func runEcosystemList(cmd *cobra.Command, args []string) error {
	// Check we're in an ecosystem root
	if err := validateEcosystemRoot(); err != nil {
		return err
	}

	// Get submodules
	submodules := getSubmodules()

	// Get local directories with grove.yml
	localRepos := getLocalRepos()

	if len(submodules) == 0 && len(localRepos) == 0 {
		fmt.Println("No repositories in ecosystem")
		fmt.Println("\nAdd a repository with:")
		fmt.Println("  grove ecosystem add <repo>")
		fmt.Println("  grove repo add <name> --ecosystem")
		return nil
	}

	// Print submodules
	if len(submodules) > 0 {
		fmt.Println("Submodules:")
		for _, sm := range submodules {
			fmt.Printf("  %s\n", sm)
		}
	}

	// Print local repos (not submodules)
	if len(localRepos) > 0 {
		if len(submodules) > 0 {
			fmt.Println()
		}
		fmt.Println("Local:")
		for _, repo := range localRepos {
			// Skip if it's already listed as submodule
			isSubmodule := false
			for _, sm := range submodules {
				if sm == repo {
					isSubmodule = true
					break
				}
			}
			if !isSubmodule {
				fmt.Printf("  %s\n", repo)
			}
		}
	}

	return nil
}

func getSubmodules() []string {
	cmd := exec.Command("git", "submodule", "status")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	var submodules []string
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: " <hash> <path> (<ref>)" or "-<hash> <path>"
		// Remove leading status character and hash
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			// parts[0] is hash (with possible leading status char), parts[1] is path
			submodules = append(submodules, parts[1])
		}
	}

	return submodules
}

func getLocalRepos() []string {
	var repos []string

	entries, err := os.ReadDir(".")
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Skip hidden directories
		if strings.HasPrefix(name, ".") {
			continue
		}
		// Check if it has a grove.yml
		groveYml := filepath.Join(name, "grove.yml")
		if _, err := os.Stat(groveYml); err == nil {
			repos = append(repos, name)
		}
	}

	return repos
}
