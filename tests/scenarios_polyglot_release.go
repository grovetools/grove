package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	
	"github.com/mattsolo1/grove-tend/pkg/command"
	"github.com/mattsolo1/grove-tend/pkg/fs"
	"github.com/mattsolo1/grove-tend/pkg/harness"
)

// PolyglotReleaseScenario tests release process with mixed project types
func PolyglotReleaseScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "polyglot-release",
		Description: "Verifies release process handles different project types correctly",
		Tags:        []string{"polyglot", "release", "dependencies"},
		Steps: []harness.Step{
			{
				Name:        "Setup release environment",
				Description: "Create projects with dependencies and test release",
				Func: func(ctx *harness.Context) error {
					ecosystemDir := ctx.NewDir("ecosystem")
					
					// Create ecosystem structure
					fs.WriteString(filepath.Join(ecosystemDir, "grove.yml"), "name: grove-ecosystem\nworkspaces:\n  - \"*\"\n")
					fs.WriteString(filepath.Join(ecosystemDir, "go.work"), `go 1.24.4

use (
	./grove-shared
	./grove-go-app
	./grove-py-lib
)
`)
					fs.WriteString(filepath.Join(ecosystemDir, ".gitmodules"), `[submodule "grove-shared"]
	path = grove-shared
	url = https://github.com/test/grove-shared
[submodule "grove-go-app"]
	path = grove-go-app
	url = https://github.com/test/grove-go-app
[submodule "grove-py-lib"]
	path = grove-py-lib
	url = https://github.com/test/grove-py-lib
`)
					
					// Create shared Go library
					sharedDir := filepath.Join(ecosystemDir, "grove-shared")
					os.MkdirAll(sharedDir, 0755)
					os.MkdirAll(filepath.Join(sharedDir, ".git"), 0755)
					fs.WriteString(filepath.Join(sharedDir, "grove.yml"), "name: grove-shared\n")
					fs.WriteString(filepath.Join(sharedDir, "go.mod"), "module github.com/test/grove-shared\n\ngo 1.24.4\n")
					fs.WriteString(filepath.Join(sharedDir, "main.go"), "package shared\n\nconst Version = \"1.0.0\"\n")
					
					// Create Go app that depends on shared
					goAppDir := filepath.Join(ecosystemDir, "grove-go-app")
					os.MkdirAll(goAppDir, 0755)
					os.MkdirAll(filepath.Join(goAppDir, ".git"), 0755)
					fs.WriteString(filepath.Join(goAppDir, "grove.yml"), "name: grove-go-app\n")
					fs.WriteString(filepath.Join(goAppDir, "go.mod"), `module github.com/test/grove-go-app

go 1.24.4

require github.com/test/grove-shared v0.1.0
`)
					fs.WriteString(filepath.Join(goAppDir, "go.sum"), "")
					fs.WriteString(filepath.Join(goAppDir, "main.go"), "package main\n\nimport _ \"github.com/test/grove-shared\"\n\nfunc main() {}\n")
					
					// Create Python library
					pyLibDir := filepath.Join(ecosystemDir, "grove-py-lib")
					os.MkdirAll(pyLibDir, 0755)
					os.MkdirAll(filepath.Join(pyLibDir, ".git"), 0755)
					fs.WriteString(filepath.Join(pyLibDir, "grove.yml"), "name: grove-py-lib\ntype: maturin\n")
					fs.WriteString(filepath.Join(pyLibDir, "pyproject.toml"), `[project]
name = "grove-py-lib"
version = "0.1.0"
dependencies = ["grove-shared>=0.1.0"]

[build-system]
requires = ["maturin>=1.0"]
build-backend = "maturin"
`)
					
					// Change to ecosystem directory
					originalDir, _ := os.Getwd()
					defer os.Chdir(originalDir)
					os.Chdir(ecosystemDir)
					
					// Set up mocks directory
					mockDir := ctx.NewDir("mocks")
					
					// Create git mock that handles tags and status
					gitMockPath := filepath.Join(mockDir, "git")
					fs.WriteString(gitMockPath, releaseGitMockScript)
					os.Chmod(gitMockPath, 0755)
					
					// Set PATH to use our mocks
					os.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))
					
					// Get grove binary path
					groveBinary := ctx.GroveBinary
					
					// Test release dry-run with skip-parent to avoid submodule issues
					cmd := command.New(groveBinary, "release", "--dry-run", "--yes", "--skip-parent")
					result := cmd.Run()
					
					// Verify it handles different project types
					output := result.Stdout + result.Stderr
					
					// Should show version calculations
					if !strings.Contains(output, "grove-shared") {
						return fmt.Errorf("expected grove-shared in release output:\n%s", output)
					}
					
					// Should not fail on Python project
					if strings.Contains(output, "go mod tidy failed") && strings.Contains(output, "grove-py-lib") {
						return fmt.Errorf("go mod tidy should not run for Python project:\n%s", output)
					}
					
					// Verify dependency update logic would work
					if !strings.Contains(output, "Proposed Versions") || !strings.Contains(output, "DRY RUN") {
						return fmt.Errorf("expected version proposal and dry run mode:\n%s", output)
					}
					
					return nil
				},
			},
		},
	}
}

// Mock git script for release testing
const releaseGitMockScript = `#!/bin/bash
# Mock git for release testing

if [[ "$1" == "describe" && "$2" == "--tags" ]]; then
    # Simulate no tags (new repository)
    exit 128
fi

if [[ "$1" == "rev-list" && "$2" == "--count" ]]; then
    # Simulate commits since last tag
    echo "5"
    exit 0
fi

if [[ "$1" == "status" ]]; then
    if [[ "$2" == "--porcelain" ]]; then
        # Clean status
        echo ""
    else {
        echo "On branch main"
        echo "Your branch is up to date with 'origin/main'."
        echo ""
        echo "nothing to commit, working tree clean"
    }
    exit 0
fi

if [[ "$1" == "branch" ]]; then
    if [[ "$2" == "--show-current" ]]; then
        echo "main"
        exit 0
    fi
    # Default branch command response
    echo "* main"
    exit 0
fi

if [[ "$1" == "config" && "$2" == "--get" ]]; then
    if [[ "$3" == "branch.main.remote" ]]; then
        echo "origin"
    elif [[ "$3" == "remote.origin.url" ]]; then
        echo "https://github.com/test/repo.git"
    fi
    exit 0
fi

if [[ "$1" == "submodule" ]]; then
    if [[ "$2" == "status" ]]; then
        # Return valid submodule status
        echo " 0000000000000000000000000000000000000000 grove-shared (heads/main)"
        echo " 0000000000000000000000000000000000000000 grove-go-app (heads/main)"
        echo " 0000000000000000000000000000000000000000 grove-py-lib (heads/main)"
        exit 0
    elif [[ "$2" == "foreach" ]]; then
        # Handle git submodule foreach --quiet "echo $sm_path"
        echo "grove-shared"
        echo "grove-go-app"
        echo "grove-py-lib"
        exit 0
    fi
    exit 0
fi

if [[ "$1" == "ls-remote" ]]; then
    echo "0000000000000000000000000000000000000000	refs/heads/main"
    exit 0
fi

if [[ "$1" == "add" ]] || [[ "$1" == "commit" ]] || [[ "$1" == "tag" ]] || [[ "$1" == "push" ]]; then
    # Success for write operations
    exit 0
fi

# Log unhandled commands for debugging
echo "Unhandled git command: $@" >&2

# Default
exit 0
`