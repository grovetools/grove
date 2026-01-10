package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/mattn/go-isatty"
	"github.com/mattsolo1/grove-core/config"
	"github.com/mattsolo1/grove-core/tui/theme"
	"github.com/spf13/cobra"
)

var (
	ecosystemInitGo bool
)

func newEcosystemInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init [name]",
		Short: "Create a new Grove ecosystem",
		Long: `Create a new Grove ecosystem (monorepo).

By default, creates a minimal ecosystem with grove.yml and README.
Use --go to add Go workspace support (go.work, Makefile).

Examples:
  # Create minimal ecosystem in current directory
  grove ecosystem init

  # Create ecosystem with a name
  grove ecosystem init my-ecosystem

  # Create Go-based ecosystem
  grove ecosystem init --go
  grove ecosystem init my-ecosystem --go`,
		Args: cobra.MaximumNArgs(1),
		RunE: runEcosystemInit,
	}

	cmd.Flags().BoolVar(&ecosystemInitGo, "go", false, "Add Go workspace support (go.work, Makefile)")

	return cmd
}

func runEcosystemInit(cmd *cobra.Command, args []string) error {
	// Determine target directory
	var targetDir string
	var ecosystemName string

	if len(args) > 0 {
		ecosystemName = args[0]
		targetDir = args[0]
		// Create the directory
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
	} else {
		targetDir = "."
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		ecosystemName = filepath.Base(cwd)
	}

	// Check if grove.yml already exists
	groveYmlPath := filepath.Join(targetDir, "grove.yml")
	if _, err := os.Stat(groveYmlPath); err == nil {
		return fmt.Errorf("grove.yml already exists in %s", targetDir)
	}

	fmt.Printf("Creating Grove ecosystem '%s'...\n", ecosystemName)

	// Create grove.yml
	groveYmlContent := fmt.Sprintf(`name: %s
workspaces:
  - "*"
`, ecosystemName)
	if err := os.WriteFile(groveYmlPath, []byte(groveYmlContent), 0644); err != nil {
		return fmt.Errorf("failed to create grove.yml: %w", err)
	}
	fmt.Println("  grove.yml")

	// Create README.md
	readmeContent := fmt.Sprintf("# %s\n\nA Grove ecosystem.\n", ecosystemName)
	if err := os.WriteFile(filepath.Join(targetDir, "README.md"), []byte(readmeContent), 0644); err != nil {
		return fmt.Errorf("failed to create README.md: %w", err)
	}
	fmt.Println("  README.md")

	// Create .gitignore
	gitignoreContent := `# Binaries
bin/
*.exe

# OS files
.DS_Store
`
	if err := os.WriteFile(filepath.Join(targetDir, ".gitignore"), []byte(gitignoreContent), 0644); err != nil {
		return fmt.Errorf("failed to create .gitignore: %w", err)
	}
	fmt.Println("  .gitignore")

	// Add Go support if requested
	if ecosystemInitGo {
		// Create go.work
		goWorkContent := `go 1.24.4

use (
)
`
		if err := os.WriteFile(filepath.Join(targetDir, "go.work"), []byte(goWorkContent), 0644); err != nil {
			return fmt.Errorf("failed to create go.work: %w", err)
		}
		fmt.Println("  go.work")

		// Create Makefile
		makefileContent := `# Grove ecosystem Makefile

.PHONY: build test clean

build:
	@grove build

test:
	@grove build && go test ./...

clean:
	@rm -rf bin/
`
		if err := os.WriteFile(filepath.Join(targetDir, "Makefile"), []byte(makefileContent), 0644); err != nil {
			return fmt.Errorf("failed to create Makefile: %w", err)
		}
		fmt.Println("  Makefile")
	}

	// Initialize git if not already a git repo
	gitDir := filepath.Join(targetDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		gitInit := exec.Command("git", "init")
		gitInit.Dir = targetDir
		if err := gitInit.Run(); err != nil {
			return fmt.Errorf("failed to initialize git: %w", err)
		}

		// Add and commit
		gitAdd := exec.Command("git", "add", ".")
		gitAdd.Dir = targetDir
		gitAdd.Run()

		gitCommit := exec.Command("git", "commit", "-m", "feat: initialize Grove ecosystem")
		gitCommit.Dir = targetDir
		gitCommit.Run()
	}

	fmt.Printf("\n%s Ecosystem created!\n", theme.IconSuccess)

	// Check discoverability and prompt to add to groves if needed
	if err := checkAndPromptDiscoverability(targetDir); err != nil {
		// Don't fail the command if discoverability check fails, just warn
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	if len(args) > 0 {
		fmt.Printf("\ncd %s\n", ecosystemName)
	}

	return nil
}

// checkAndPromptDiscoverability checks if the ecosystem will be discoverable
// and prompts the user to add it to the global config if not.
func checkAndPromptDiscoverability(ecosystemPath string) error {
	// Get absolute path of the ecosystem
	absPath, err := filepath.Abs(ecosystemPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Load the current global config
	cfg, err := config.LoadDefault()
	if err != nil {
		// If config loading fails, we can't check discoverability
		// This is not necessarily an error - might be first-time setup
		cfg = &config.Config{
			Groves: make(map[string]config.GroveSourceConfig),
		}
	}
	// Check if the ecosystem is already discoverable
	if discoverable, groveName := isEcosystemDiscoverable(absPath, cfg); discoverable {
		fmt.Printf("\nThis ecosystem is discoverable via grove '%s'\n", groveName)
		return nil
	}

	// Check if we're in a TTY (interactive mode)
	if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		fmt.Printf("\n%s This ecosystem is not in a configured grove and won't be\n", theme.IconWarning)
		fmt.Printf("   discovered by grove tools.\n")
		fmt.Printf("   Run in an interactive terminal to add it, or manually update\n")
		fmt.Printf("   ~/.config/grove/grove.override.yml\n")
		return nil
	}

	// Derive grove name from the ecosystem path itself
	groveName, err := deriveGroveName(absPath, cfg.Groves)
	if err != nil {
		fmt.Printf("\n%s This ecosystem is not in a configured grove and won't be\n", theme.IconWarning)
		fmt.Printf("   discovered by grove tools.\n")
		fmt.Printf("   Error: %v\n", err)
		fmt.Printf("   Manually update ~/.config/grove/grove.override.yml\n")
		return nil
	}

	// Get available notebooks
	notebooks := getNotebookKeys(cfg)
	sort.Strings(notebooks)

	// Run the TUI prompt
	result, err := runInitPrompt(absPath, groveName, notebooks)
	if err != nil {
		return fmt.Errorf("failed to run prompt: %w", err)
	}

	if !result.Confirmed {
		fmt.Println("\nSkipped adding grove to config.")
		return nil
	}

	// Update the config file where groves are defined - add the ecosystem itself as a grove
	configPath, err := updateGlobalConfig(groveName, absPath, result.SelectedNotebook)
	if err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	fmt.Printf("\n%s Added grove '%s' (%s)\n", theme.IconSuccess, groveName, absPath)
	fmt.Printf("  Updated %s\n", configPath)

	return nil
}
