package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/pkg/workspace"
	"github.com/spf13/cobra"

	"github.com/grovetools/grove/pkg/setup"
)

var bootstrapDryRun bool

func newBootstrapCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("bootstrap", "Bootstrap Grove from source")
	cmd.Hidden = true // Internal command used during initial setup
	cmd.Long = `Bootstrap Grove for development from source.

This command is used after cloning the grove-ecosystem repository to set up
an environment for developing the grove tools from source. It:

  1. Creates the Grove binary directory if it doesn't exist
  2. Symlinks the current grove binary to that directory
  3. Creates minimal global grove.yml with the ecosystem configured
  4. Prints PATH instructions

After running bootstrap, you can:
  - Run 'grove build' to build all ecosystem tools in parallel
  - Run 'grove dev cwd' to activate all local dev binaries
  - Run 'grove setup' for additional configuration (notebook, Gemini API, etc.)

Example:
  cd grove-ecosystem/grove-meta
  make build
  ./bin/grove bootstrap
  # Add the printed path to your PATH, then:
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

	groveBinDir := paths.BinDir()
	groveSymlink := filepath.Join(groveBinDir, "grove")

	// 1. Create Grove binary directory
	if err := service.MkdirAll(groveBinDir, 0o755); err != nil {
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

	// 3. Subscribe this machine to the ecosystem in roots.toml.
	rootsPath := coderoot.RootsPath()
	if bootstrapDryRun {
		fmt.Printf("[dry-run] Would subscribe to ecosystem %q in %s\n", ecosystemName, rootsPath)
	} else if rootsPath, err = registerCodeRoot(ecosystemName, ecosystemDir, ""); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	// 4. Print summary and next steps
	fmt.Println()
	fmt.Printf("Registered ecosystem: %s\n", ecosystemName)
	fmt.Printf("  Path: %s\n", ecosystemDir)
	fmt.Printf("  Subscription: %s\n", rootsPath)
	fmt.Println()

	// grove is the only name that needs to be reachable: the tools it builds
	// stay in the toolchain dir and run as `grove <tool>`.
	if _, ok := groveReachable(); !ok {
		fmt.Println("'grove' is not on your PATH yet:")
		for _, line := range groveReachableHint() {
			fmt.Println(line)
		}
		fmt.Println()
	}

	fmt.Println("Next steps:")
	fmt.Println("  grove build      # Build all ecosystem tools")
	fmt.Println("  grove dev cwd    # Activate local dev binaries")

	return nil
}
