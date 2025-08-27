package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mattsolo1/grove-core/cli"
	"github.com/spf13/cobra"
)

var (
	wsInitName        string
	wsInitDescription string
)

func NewWorkspaceInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new Grove ecosystem",
		Long: `Initialize a new Grove ecosystem in the current directory.

This command creates the necessary files to start a Grove ecosystem:
- grove.yml: Ecosystem configuration
- go.work: Go workspace file  
- Makefile: Build automation
- .gitignore: Git ignore patterns

Examples:
  # Initialize with default name (current directory name)
  grove ws init
  
  # Initialize with custom name
  grove ws init --name myecosystem
  
  # Initialize with custom description  
  grove ws init --description "My utilities and tools"`,
		Args: cobra.NoArgs,
		RunE: runWorkspaceInit,
	}

	cmd.Flags().StringVarP(&wsInitName, "name", "n", "", "Ecosystem name (defaults to current directory name)")
	cmd.Flags().StringVarP(&wsInitDescription, "description", "d", "", "Ecosystem description")

	return cmd
}

func runWorkspaceInit(cmd *cobra.Command, args []string) error {
	logger := cli.GetLogger(cmd)

	// Check if grove.yml already exists
	if _, err := os.Stat("grove.yml"); err == nil {
		return fmt.Errorf("grove.yml already exists in the current directory")
	}

	// Get ecosystem name
	ecosystemName := wsInitName
	if ecosystemName == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		ecosystemName = filepath.Base(cwd)
	}

	// Set default description
	if wsInitDescription == "" {
		wsInitDescription = "Grove ecosystem"
	}

	logger.Infof("Initializing Grove ecosystem '%s'...", ecosystemName)

	// Create grove.yml
	groveYMLContent := fmt.Sprintf(`name: %s
description: %s
workspaces:
  - "*"
`, ecosystemName, wsInitDescription)

	if err := os.WriteFile("grove.yml", []byte(groveYMLContent), 0644); err != nil {
		return fmt.Errorf("failed to create grove.yml: %w", err)
	}
	logger.Info("Created grove.yml")

	// Create go.work
	goWorkContent := `go 1.24.4

use (
)
`
	if err := os.WriteFile("go.work", []byte(goWorkContent), 0644); err != nil {
		return fmt.Errorf("failed to create go.work: %w", err)
	}
	logger.Info("Created go.work")

	// Create .gitignore
	gitignoreContent := `# Binaries
bin/
*.exe

# Test and coverage
*.test
*.out
coverage.html

# OS files
.DS_Store
Thumbs.db

# IDE files
.vscode/
.idea/
*.swp
*.swo

# Temporary files
*.tmp
*.bak
`
	if err := os.WriteFile(".gitignore", []byte(gitignoreContent), 0644); err != nil {
		return fmt.Errorf("failed to create .gitignore: %w", err)
	}
	logger.Info("Created .gitignore")

	// Create Makefile
	makefileContent := `# Grove ecosystem Makefile

.PHONY: all build test clean

PACKAGES ?= 
BINARIES ?= 

all: build

build:
	@echo "Building all packages..."
	@for pkg in $(PACKAGES); do \
		echo "Building $$pkg..."; \
		$(MAKE) -C $$pkg build || exit 1; \
	done

test:
	@echo "Testing all packages..."
	@for pkg in $(PACKAGES); do \
		echo "Testing $$pkg..."; \
		$(MAKE) -C $$pkg test || exit 1; \
	done

clean:
	@echo "Cleaning all packages..."
	@for pkg in $(PACKAGES); do \
		echo "Cleaning $$pkg..."; \
		$(MAKE) -C $$pkg clean || exit 1; \
	done
`
	if err := os.WriteFile("Makefile", []byte(makefileContent), 0644); err != nil {
		return fmt.Errorf("failed to create Makefile: %w", err)
	}
	logger.Info("Created Makefile")

	// Initialize git repository if not already initialized
	if _, err := os.Stat(".git"); os.IsNotExist(err) {
		logger.Info("Initializing git repository...")
		gitInit := exec.Command("git", "init")
		if err := gitInit.Run(); err != nil {
			return fmt.Errorf("failed to initialize git: %w", err)
		}

		// Add files to git
		gitAdd := exec.Command("git", "add", "grove.yml", "go.work", ".gitignore", "Makefile")
		if err := gitAdd.Run(); err != nil {
			logger.Warnf("Failed to stage initial files: %v", err)
		}
	}

	// Display summary
	fmt.Println("\n✅ Grove ecosystem initialized successfully!")
	fmt.Println("\nCREATED FILES")
	fmt.Println("-------------")
	fmt.Println("- grove.yml    : Ecosystem configuration")
	fmt.Println("- go.work      : Go workspace file")
	fmt.Println("- Makefile     : Build automation")
	fmt.Println("- .gitignore   : Git ignore patterns")

	fmt.Println("\nNEXT STEPS")
	fmt.Println("----------")
	fmt.Println("1. Add your first repository:")
	fmt.Printf("   grove add-repo <repo-name> --alias <alias> --ecosystem\n")
	fmt.Println("")
	fmt.Println("2. Or add existing repositories as submodules:")
	fmt.Println("   git submodule add <repo-url> <repo-name>")
	fmt.Println("")
	fmt.Println("3. Update go.work and Makefile as needed")

	return nil
}