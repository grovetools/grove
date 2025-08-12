package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/devlinks"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

func newDevLinkCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("link", "Register binaries from a local worktree")

	cmd.Long = `Register binaries from a Git worktree for local development.
This command discovers binaries in the specified directory and makes them
available for use with 'grove dev use'.`

	cmd.Example = `  # Link binaries from current directory
  grove dev link .
  
  # Link with a custom alias
  grove dev link ../grove-flow --as feature-branch`

	cmd.Args = cobra.ExactArgs(1)

	var alias string
	cmd.Flags().StringVar(&alias, "as", "", "Custom alias for this version")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		worktreePath := args[0]

		// Convert to absolute path
		absWorktreePath, err := filepath.Abs(worktreePath)
		if err != nil {
			return fmt.Errorf("failed to resolve path: %w", err)
		}

		// Discover binaries in the worktree
		discoveredBinaries, err := workspace.DiscoverLocalBinaries(absWorktreePath)
		if err != nil {
			return fmt.Errorf("failed to discover binaries: %w", err)
		}

		if len(discoveredBinaries) == 0 {
			return fmt.Errorf("no binaries found in %s", absWorktreePath)
		}

		// Default alias is the directory name
		if alias == "" {
			alias = filepath.Base(absWorktreePath)
		}

		// Load existing configuration
		config, err := devlinks.LoadConfig()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		// Track which binaries need to be automatically 'used'
		var binariesToUse []string

		// Register each discovered binary
		for _, binary := range discoveredBinaries {
			binaryName := binary.Name
			binaryPath := binary.Path

			// Ensure binary exists
			if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
				fmt.Printf("Warning: binary '%s' not found at %s, skipping\n", binaryName, binaryPath)
				continue
			}

			// Initialize binary info if needed
			if config.Binaries[binaryName] == nil {
				config.Binaries[binaryName] = &devlinks.BinaryLinks{
					Links: make(map[string]devlinks.LinkInfo),
				}
			}

			// Create link info
			linkInfo := devlinks.LinkInfo{
				Path:         binaryPath,
				WorktreePath: absWorktreePath,
				RegisteredAt: time.Now().Format(time.RFC3339),
			}

			config.Binaries[binaryName].Links[alias] = linkInfo
			fmt.Printf("Registered binary '%s' version '%s'\n", binaryName, alias)

			// If this is the first link for this binary, mark it to be automatically 'used'
			if config.Binaries[binaryName].Current == "" {
				binariesToUse = append(binariesToUse, binaryName)
			}
		}

		// Save configuration
		if err := devlinks.SaveConfig(config); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		// Automatically activate first links
		for _, binaryName := range binariesToUse {
			fmt.Printf("Setting '%s' as current version for '%s'\n", alias, binaryName)
			if err := activateDevLink(binaryName, alias); err != nil {
				return fmt.Errorf("failed to activate link for %s: %w", binaryName, err)
			}
		}

		return nil
	}

	return cmd
}

// activateDevLink creates a symlink for a specific dev link
func activateDevLink(binaryName, alias string) error {
	config, err := devlinks.LoadConfig()
	if err != nil {
		return err
	}

	binInfo, ok := config.Binaries[binaryName]
	if !ok {
		return fmt.Errorf("binary '%s' is not registered", binaryName)
	}

	linkInfo, ok := binInfo.Links[alias]
	if !ok {
		return fmt.Errorf("alias '%s' not found for binary '%s'", alias, binaryName)
	}

	// Get grove home directory
	groveHome, err := devlinks.GetGroveHome()
	if err != nil {
		return err
	}

	binDir := filepath.Join(groveHome, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return err
	}

	targetPath := filepath.Join(binDir, binaryName)

	// Remove existing symlink if it exists
	_ = os.Remove(targetPath)

	// Create symlink to the actual binary
	if err := os.Symlink(linkInfo.Path, targetPath); err != nil {
		return fmt.Errorf("failed to create symlink: %w", err)
	}

	// Update current link in config
	binInfo.Current = alias
	if err := devlinks.SaveConfig(config); err != nil {
		return err
	}

	// Check if binDir is in PATH
	if !isDirInPath(binDir) {
		fmt.Printf("\n⚠️  Warning: '%s' is not in your PATH.\n", binDir)
		fmt.Println("   Please add it to your shell configuration file (e.g., .zshrc, .bash_profile):")
		fmt.Printf("   export PATH=\"%s:$PATH\"\n", binDir)
	}

	return nil
}

func isDirInPath(dir string) bool {
	path := os.Getenv("PATH")
	for _, p := range filepath.SplitList(path) {
		if p == dir {
			return true
		}
	}
	return false
}
