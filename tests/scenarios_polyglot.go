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

// PolyglotProjectTypesScenario tests project type detection and handling
func PolyglotProjectTypesScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "polyglot-project-types",
		Description: "Verifies project type detection and workspace status display",
		Tags:        []string{"polyglot", "workspace", "status"},
		Steps: []harness.Step{
			{
				Name:        "Setup mixed project types",
				Description: "Create workspaces with different project types",
				Func: func(ctx *harness.Context) error {
					ecosystemDir := ctx.NewDir("ecosystem")
					
					// Create ecosystem structure
					fs.WriteString(filepath.Join(ecosystemDir, "grove.yml"), "name: grove-ecosystem\nworkspaces:\n  - \"*\"\n")
					fs.WriteString(filepath.Join(ecosystemDir, "go.work"), "go 1.24.4\n\nuse (\n\t./go-project\n\t./maturin-project\n)\n")
					
					// Create Go project
					goDir := filepath.Join(ecosystemDir, "go-project")
					os.MkdirAll(goDir, 0755)
					fs.WriteString(filepath.Join(goDir, "grove.yml"), "name: go-project\n")
					fs.WriteString(filepath.Join(goDir, "go.mod"), "module github.com/test/go-project\n\ngo 1.24.4\n")
					fs.WriteString(filepath.Join(goDir, "main.go"), "package main\n\nfunc main() {}\n")
					
					// Create Maturin project
					maturinDir := filepath.Join(ecosystemDir, "maturin-project")
					os.MkdirAll(maturinDir, 0755)
					fs.WriteString(filepath.Join(maturinDir, "grove.yml"), "name: maturin-project\ntype: maturin\n")
					fs.WriteString(filepath.Join(maturinDir, "pyproject.toml"), `[project]
name = "maturin-project"
version = "0.1.0"
dependencies = []

[build-system]
requires = ["maturin>=1.0"]
build-backend = "maturin"
`)
					
					// Create template project
					templateDir := filepath.Join(ecosystemDir, "template-project")
					os.MkdirAll(templateDir, 0755)
					fs.WriteString(filepath.Join(templateDir, "grove.yml"), "name: template-project\ntype: template\n")
					
					// Change to ecosystem directory
					originalDir, _ := os.Getwd()
					defer os.Chdir(originalDir)
					os.Chdir(ecosystemDir)
					
					// Get grove binary path
					groveBinary := ctx.GroveBinary
					
					// Test workspace status with type column
					cmd := command.New(groveBinary, "ws", "status", "--cols", "type")
					result := cmd.Run()
					
					if result.ExitCode != 0 {
						return fmt.Errorf("workspace status failed: %s\n%s", result.Stderr, result.Stdout)
					}
					
					// Verify output contains expected project types
					output := result.Stdout
					if !strings.Contains(output, "TYPE") {
						return fmt.Errorf("TYPE column not found in output:\n%s", output)
					}
					
					// Check for specific project types
					if !strings.Contains(output, "go") {
						return fmt.Errorf("expected 'go' type in output:\n%s", output)
					}
					
					if !strings.Contains(output, "maturin") {
						return fmt.Errorf("expected 'maturin' type in output:\n%s", output)
					}
					
					if !strings.Contains(output, "template") {
						return fmt.Errorf("expected 'template' type in output:\n%s", output)
					}
					
					return nil
				},
			},
		},
	}
}

// PolyglotDependencyGraphScenario tests dependency graph building for mixed projects
func PolyglotDependencyGraphScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "polyglot-dependency-graph",
		Description: "Verifies dependency graph handles different project types",
		Tags:        []string{"polyglot", "dependencies", "graph"},
		Steps: []harness.Step{
			{
				Name:        "Build dependency graph with mixed types",
				Description: "Create projects with cross-dependencies and verify graph",
				Func: func(ctx *harness.Context) error {
					ecosystemDir := ctx.NewDir("ecosystem")
					
					// Create ecosystem structure
					fs.WriteString(filepath.Join(ecosystemDir, "grove.yml"), "name: grove-ecosystem\nworkspaces:\n  - \"*\"\n")
					fs.WriteString(filepath.Join(ecosystemDir, "go.work"), `go 1.24.4

use (
	./grove-core
	./grove-service
	./grove-py-tool
)
`)
					
					// Create core Go library
					coreDir := filepath.Join(ecosystemDir, "grove-core")
					os.MkdirAll(coreDir, 0755)
					os.MkdirAll(filepath.Join(coreDir, ".git"), 0755)
					fs.WriteString(filepath.Join(coreDir, "grove.yml"), "name: grove-core\n")
					fs.WriteString(filepath.Join(coreDir, "go.mod"), "module github.com/test/grove-core\n\ngo 1.24.4\n")
					
					// Create Go service that depends on core
					serviceDir := filepath.Join(ecosystemDir, "grove-service")
					os.MkdirAll(serviceDir, 0755)
					os.MkdirAll(filepath.Join(serviceDir, ".git"), 0755)
					fs.WriteString(filepath.Join(serviceDir, "grove.yml"), "name: grove-service\n")
					fs.WriteString(filepath.Join(serviceDir, "go.mod"), `module github.com/test/grove-service

go 1.24.4

require github.com/test/grove-core v0.1.0
`)
					
					// Create Python tool
					pyDir := filepath.Join(ecosystemDir, "grove-py-tool")
					os.MkdirAll(pyDir, 0755)
					os.MkdirAll(filepath.Join(pyDir, ".git"), 0755)
					fs.WriteString(filepath.Join(pyDir, "grove.yml"), "name: grove-py-tool\ntype: maturin\n")
					fs.WriteString(filepath.Join(pyDir, "pyproject.toml"), `[project]
name = "grove-py-tool"
version = "0.1.0"
dependencies = ["grove-core>=0.1.0"]
`)
					
					// Add .gitmodules to simulate submodules
					fs.WriteString(filepath.Join(ecosystemDir, ".gitmodules"), `[submodule "grove-core"]
	path = grove-core
	url = https://github.com/test/grove-core
[submodule "grove-service"]
	path = grove-service
	url = https://github.com/test/grove-service
[submodule "grove-py-tool"]
	path = grove-py-tool
	url = https://github.com/test/grove-py-tool
`)
					
					// Change to ecosystem directory
					originalDir, _ := os.Getwd()
					defer os.Chdir(originalDir)
					os.Chdir(ecosystemDir)
					
					// Set up mocks directory for git
					mockDir := ctx.NewDir("mocks")
					
					// Create git mock
					gitMockPath := filepath.Join(mockDir, "git")
					fs.WriteString(gitMockPath, polyglotReleaseGitMockScript)
					os.Chmod(gitMockPath, 0755)
					
					// Set PATH to use our mocks
					os.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))
					
					// Get grove binary path
					groveBinary := ctx.GroveBinary
					
					// Test release dry-run to verify dependency graph is built correctly
					cmd := command.New(groveBinary, "release", "--dry-run", "--yes")
					result := cmd.Run()
					
					// The command might fail due to missing git setup, but we can check the output
					output := result.Stdout + result.Stderr
					
					// Verify it processes the projects
					if !strings.Contains(output, "grove-service") || !strings.Contains(output, "grove-core") {
						return fmt.Errorf("expected projects in release output:\n%s", output)
					}
					
					// For Python projects, it should not fail with go mod errors
					if strings.Contains(output, "go mod tidy failed") && strings.Contains(output, "grove-py-tool") {
						return fmt.Errorf("go mod tidy should not run for Python project:\n%s", output)
					}
					
					return nil
				},
			},
		},
	}
}

// PolyglotAddRepoScenario tests adding different project types
func PolyglotAddRepoScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "polyglot-add-repo",
		Description: "Verifies add-repo works with different templates",
		Tags:        []string{"polyglot", "add-repo", "templates"},
		Steps: []harness.Step{
			{
				Name:        "Add maturin project",
				Description: "Create a new maturin project and verify no Go commands run",
				Func: func(ctx *harness.Context) error {
					ecosystemDir := ctx.NewDir("ecosystem")
					
					// Create minimal ecosystem
					fs.WriteString(filepath.Join(ecosystemDir, "grove.yml"), "name: grove-ecosystem\nworkspaces:\n  - \"*\"\n")
					fs.WriteString(filepath.Join(ecosystemDir, "go.work"), "go 1.24.4\n")
					fs.WriteString(filepath.Join(ecosystemDir, "Makefile"), 
						"PACKAGES = grove-core\n# GROVE-META:ADD-REPO:PACKAGES\n\nBINARIES = grove\n# GROVE-META:ADD-REPO:BINARIES\n")
					
					// Create mock maturin template
					templateDir := filepath.Join(ecosystemDir, "grove-project-tmpl-maturin")
					os.MkdirAll(templateDir, 0755)
					fs.WriteString(filepath.Join(templateDir, "grove.yml"), "name: grove-project-tmpl-maturin\ntype: template\n")
					fs.WriteString(filepath.Join(templateDir, "pyproject.toml"), `[project]
name = "{{.Name}}"
version = "0.1.0"
dependencies = []

[build-system]
requires = ["maturin>=1.0"]
build-backend = "maturin"
`)
					fs.WriteString(filepath.Join(templateDir, "Makefile"), `build:
	@echo "Building Python project"

test:
	@echo "Running Python tests"
`)
					
					// Change to ecosystem directory
					originalDir, _ := os.Getwd()
					defer os.Chdir(originalDir)
					os.Chdir(ecosystemDir)
					
					// Set required env
					os.Setenv("GROVE_PAT", "test-pat")
					
					// Set up mocks directory
					mockDir := ctx.NewDir("mocks")
					
					// Create gh mock
					ghMockPath := filepath.Join(mockDir, "gh")
					fs.WriteString(ghMockPath, ghMockScript)
					os.Chmod(ghMockPath, 0755)
					
					// Create git mock
					gitMockPath := filepath.Join(mockDir, "git")
					fs.WriteString(gitMockPath, polyglotGitMockScript)
					os.Chmod(gitMockPath, 0755)
					
					// Set PATH to use our mocks
					os.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))
					
					// Get grove binary path
					groveBinary := ctx.GroveBinary
					
					// Run add-repo with maturin template
					cmd := command.New(groveBinary, "add-repo", "grove-test-maturin",
						"--template", templateDir,
						"--skip-github",
						"--dry-run") // Use dry-run to avoid actual build
					
					result := cmd.Run()
					
					// Check that it doesn't fail with go mod errors
					if strings.Contains(result.Stderr, "go mod tidy") {
						return fmt.Errorf("go mod tidy should not run for maturin project:\n%s", result.Stderr)
					}
					
					if result.ExitCode != 0 && !strings.Contains(result.Stderr, "DRY RUN") {
						return fmt.Errorf("add-repo failed: %s\n%s", result.Stderr, result.Stdout)
					}
					
					return nil
				},
			},
		},
	}
}

// Mock script for git operations specific to polyglot testing
const polyglotGitMockScript = `#!/bin/bash
# Mock git for testing

if [[ "$1" == "init" ]]; then
    mkdir -p .git
    exit 0
fi

if [[ "$1" == "add" ]]; then
    exit 0
fi

if [[ "$1" == "commit" ]]; then
    exit 0
fi

if [[ "$1" == "status" ]]; then
    echo "On branch main"
    echo "nothing to commit, working tree clean"
    exit 0
fi

if [[ "$1" == "submodule" ]]; then
    if [[ "$2" == "add" ]]; then
        exit 0
    fi
    if [[ "$2" == "deinit" ]]; then
        exit 0
    fi
    if [[ "$2" == "status" ]]; then
        echo ""
        exit 0
    fi
fi

exit 0
`

// Release git mock script for polyglot dependency tests
const polyglotReleaseGitMockScript = `#!/bin/bash
# Mock git for testing

if [[ "$1" == "describe" && "$2" == "--tags" ]]; then
    exit 128  # No tags
fi

if [[ "$1" == "rev-list" && "$2" == "--count" ]]; then
    echo "5"
    exit 0
fi

if [[ "$1" == "status" ]]; then
    if [[ "$2" == "--porcelain" ]]; then
        echo ""
    else
        echo "On branch main"
        echo "nothing to commit, working tree clean"
    fi
    exit 0
fi

if [[ "$1" == "branch" && "$2" == "--show-current" ]]; then
    echo "main"
    exit 0
fi

if [[ "$1" == "submodule" && "$2" == "status" ]]; then
    echo " 0000000000000000000000000000000000000000 grove-core (heads/main)"
    echo " 0000000000000000000000000000000000000000 grove-service (heads/main)"
    echo " 0000000000000000000000000000000000000000 grove-py-tool (heads/main)"
    exit 0
fi

if [[ "$1" == "config" ]]; then
    if [[ "$3" == "branch.main.remote" ]]; then
        echo "origin"
    fi
    exit 0
fi

exit 0
`