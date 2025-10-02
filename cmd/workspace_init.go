package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mattsolo1/grove-core/logging"
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
	logger := logging.NewLogger("ws-init")
	pretty := logging.NewPrettyLogger()

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
	pretty.InfoPretty(fmt.Sprintf("Initializing Grove ecosystem '%s'...", ecosystemName))

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
	pretty.InfoPretty("Created grove.yml")

	// Create go.work
	goWorkContent := `go 1.24.4

use (
)
`
	if err := os.WriteFile("go.work", []byte(goWorkContent), 0644); err != nil {
		return fmt.Errorf("failed to create go.work: %w", err)
	}
	logger.Info("Created go.work")
	pretty.InfoPretty("Created go.work")

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
	pretty.InfoPretty("Created .gitignore")

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
	pretty.InfoPretty("Created Makefile")

	// Initialize git repository if not already initialized
	if _, err := os.Stat(".git"); os.IsNotExist(err) {
		logger.Info("Initializing git repository...")
		pretty.InfoPretty("Initializing git repository...")
		gitInit := exec.Command("git", "init")
		if err := gitInit.Run(); err != nil {
			return fmt.Errorf("failed to initialize git: %w", err)
		}

		// Add files to git
		gitAdd := exec.Command("git", "add", "grove.yml", "go.work", ".gitignore", "Makefile")
		if err := gitAdd.Run(); err != nil {
			logger.Warnf("Failed to stage initial files: %v", err)
			pretty.InfoPretty(fmt.Sprintf("Warning: Failed to stage initial files: %v", err))
		}
	}

	// Create .grove directory and workspace file
	if err := os.MkdirAll(".grove", 0755); err != nil {
		return fmt.Errorf("failed to create .grove directory: %w", err)
	}
	
	timestamp := time.Now().UTC().Format(time.RFC3339)
	workspaceContent := fmt.Sprintf(`branch: main
plan: %s-ecosystem-root
created_at: %s
ecosystem: true
repos: []
`, ecosystemName, timestamp)
	
	if err := os.WriteFile(".grove/workspace", []byte(workspaceContent), 0644); err != nil {
		return fmt.Errorf("failed to create .grove/workspace: %w", err)
	}
	logger.Info("Created .grove/workspace")
	pretty.InfoPretty("Created .grove/workspace")

	// Display summary
	pretty.Success("Grove ecosystem initialized successfully!")
	pretty.InfoPretty("\nCREATED FILES")
	pretty.InfoPretty("-------------")
	pretty.InfoPretty("- grove.yml        : Ecosystem configuration")
	pretty.InfoPretty("- go.work          : Go workspace file")
	pretty.InfoPretty("- Makefile         : Build automation")
	pretty.InfoPretty("- .gitignore       : Git ignore patterns")
	pretty.InfoPretty("- .grove/workspace : Workspace marker file")

	pretty.InfoPretty("\nNEXT STEPS")
	pretty.InfoPretty("----------")
	pretty.InfoPretty("1. Add your first repository:")
	pretty.InfoPretty("   grove add-repo <repo-name> --alias <alias> --ecosystem")
	pretty.InfoPretty("")
	pretty.InfoPretty("2. Or add existing repositories as submodules:")
	pretty.InfoPretty("   git submodule add <repo-url> <repo-name>")
	pretty.InfoPretty("")
	pretty.InfoPretty("3. Update go.work and Makefile as needed")

	return nil
}