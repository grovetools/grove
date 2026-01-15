package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-meta/pkg/setup"
	"github.com/spf13/cobra"
)

var bootstrapDryRun bool

func newBootstrapCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("bootstrap", "Bootstrap Grove from source")
	cmd.Hidden = true // Internal command used during initial setup
	cmd.Long = `Bootstrap Grove for development from source.

This command is used after cloning the grove-ecosystem repository to set up
an environment for developing the grove tools from source. It:

  1. Creates ~/.grove/bin directory
  2. Symlinks the current grove binary to ~/.grove/bin/grove
  3. Creates minimal ~/.config/grove/grove.yml with the ecosystem configured
  4. Prints PATH instructions

After running bootstrap, you can:
  - Run 'grove build' to build all ecosystem tools in parallel
  - Run 'grove dev cwd' to activate all local dev binaries
  - Run 'grove setup' for additional configuration (notebook, Gemini API, etc.)

Example:
  cd grove-ecosystem/grove-meta
  make build
  ./bin/grove bootstrap
  # Add ~/.grove/bin to PATH, then:
  grove build
  grove dev cwd`

	cmd.RunE = runBootstrap
	cmd.SilenceUsage = true

	cmd.Flags().BoolVar(&bootstrapDryRun, "dry-run", false, "Preview changes without making them")

	return cmd
}

func runBootstrap(cmd *cobra.Command, args []string) error {
	// Get the path to the current executable
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}

	// Find the ecosystem root by walking up from the executable
	ecosystemDir, err := workspace.FindEcosystemRoot(filepath.Dir(execPath))
	if err != nil {
		return fmt.Errorf("not running from within a grove ecosystem: %w", err)
	}

	ecosystemName := filepath.Base(ecosystemDir)

	// Create service for file operations
	service := setup.NewService(bootstrapDryRun)
	yamlHandler := setup.NewYAMLHandler(service)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	groveBinDir := filepath.Join(homeDir, ".grove", "bin")
	groveSymlink := filepath.Join(groveBinDir, "grove")

	// 1. Create ~/.grove/bin
	if err := service.MkdirAll(groveBinDir, 0755); err != nil {
		return err
	}

	// 2. Create symlink to current binary
	if !bootstrapDryRun {
		// Remove existing symlink if present
		if _, err := os.Lstat(groveSymlink); err == nil {
			if err := os.Remove(groveSymlink); err != nil {
				return fmt.Errorf("failed to remove existing symlink: %w", err)
			}
		}
		if err := os.Symlink(execPath, groveSymlink); err != nil {
			return fmt.Errorf("failed to create symlink: %w", err)
		}
		fmt.Printf("Linked %s -> %s\n", setup.AbbreviatePath(groveSymlink), setup.AbbreviatePath(execPath))
	} else {
		fmt.Printf("[dry-run] Would link %s -> %s\n", setup.AbbreviatePath(groveSymlink), setup.AbbreviatePath(execPath))
	}

	// 3. Create/update ~/.config/grove/grove.yml
	root, err := yamlHandler.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Add ecosystem to groves section
	setup.SetValue(root, map[string]interface{}{
		"path":    ecosystemDir,
		"enabled": true,
	}, "groves", ecosystemName)

	if err := yamlHandler.SaveGlobalConfig(root); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	// 4. Print summary and next steps
	fmt.Println()
	fmt.Printf("Registered ecosystem: %s\n", ecosystemName)
	fmt.Printf("  Path: %s\n", ecosystemDir)
	fmt.Println()

	// Check if PATH already includes ~/.grove/bin
	pathEnv := os.Getenv("PATH")
	if !containsPath(pathEnv, groveBinDir) {
		fmt.Println("Add to PATH:")
		fmt.Printf("  export PATH=\"$HOME/.grove/bin:$PATH\"   # bash/zsh\n")
		fmt.Printf("  fish_add_path ~/.grove/bin             # fish\n")
		fmt.Println()
	}

	fmt.Println("Next steps:")
	fmt.Println("  grove build      # Build all ecosystem tools")
	fmt.Println("  grove dev cwd    # Activate local dev binaries")

	return nil
}

// containsPath checks if a path is already in the PATH environment variable
func containsPath(pathEnv, dir string) bool {
	paths := filepath.SplitList(pathEnv)
	for _, p := range paths {
		if p == dir {
			return true
		}
	}
	return false
}
