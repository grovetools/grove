package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

					// Setup global grove config for workspace discovery
					if err := setupGlobalGroveConfig(ctx, ctx.RootDir); err != nil {
						return err
					}

					// Create ecosystem structure
					fs.WriteString(filepath.Join(ecosystemDir, "grove.yml"), "name: grove-ecosystem\nworkspaces:\n  - \"*\"\n")
					fs.WriteString(filepath.Join(ecosystemDir, "go.work"), `go 1.24.4

use (
	./grove-shared
	./grove-go-app
	./grove-py-lib
)
`)

					// Initialize ecosystem as git repo
					cmd := ctx.Command("git", "init").Dir(ecosystemDir)
					if result := cmd.Run(); result.ExitCode != 0 {
						return fmt.Errorf("failed to init ecosystem git repo: %s", result.Stderr)
					}

					// Create shared Go library
					sharedDir := filepath.Join(ecosystemDir, "grove-shared")
					os.MkdirAll(sharedDir, 0755)
					fs.WriteString(filepath.Join(sharedDir, "grove.yml"), "name: grove-shared\n")
					fs.WriteString(filepath.Join(sharedDir, "go.mod"), "module github.com/test/grove-shared\n\ngo 1.24.4\n")
					fs.WriteString(filepath.Join(sharedDir, "main.go"), "package shared\n\nconst Version = \"1.0.0\"\n")

					cmd = ctx.Command("git", "init").Dir(sharedDir)
					cmd.Run()
					cmd = ctx.Command("git", "add", ".").Dir(sharedDir)
					cmd.Run()
					cmd = ctx.Command("git", "commit", "-m", "initial").Dir(sharedDir)
					cmd.Run()

					// Create Go app that depends on shared
					goAppDir := filepath.Join(ecosystemDir, "grove-go-app")
					os.MkdirAll(goAppDir, 0755)
					fs.WriteString(filepath.Join(goAppDir, "grove.yml"), "name: grove-go-app\n")
					fs.WriteString(filepath.Join(goAppDir, "go.mod"), `module github.com/test/grove-go-app

go 1.24.4

require github.com/test/grove-shared v0.1.0
`)
					fs.WriteString(filepath.Join(goAppDir, "go.sum"), "")
					fs.WriteString(filepath.Join(goAppDir, "main.go"), "package main\n\nimport _ \"github.com/test/grove-shared\"\n\nfunc main() {}\n")

					cmd = ctx.Command("git", "init").Dir(goAppDir)
					cmd.Run()
					cmd = ctx.Command("git", "add", ".").Dir(goAppDir)
					cmd.Run()
					cmd = ctx.Command("git", "commit", "-m", "initial").Dir(goAppDir)
					cmd.Run()

					// Create Python library
					pyLibDir := filepath.Join(ecosystemDir, "grove-py-lib")
					os.MkdirAll(pyLibDir, 0755)
					fs.WriteString(filepath.Join(pyLibDir, "grove.yml"), "name: grove-py-lib\ntype: maturin\n")
					fs.WriteString(filepath.Join(pyLibDir, "pyproject.toml"), `[project]
name = "grove-py-lib"
version = "0.1.0"
dependencies = ["grove-shared>=0.1.0"]

[build-system]
requires = ["maturin>=1.0"]
build-backend = "maturin"
`)

					cmd = ctx.Command("git", "init").Dir(pyLibDir)
					cmd.Run()
					cmd = ctx.Command("git", "add", ".").Dir(pyLibDir)
					cmd.Run()
					cmd = ctx.Command("git", "commit", "-m", "initial").Dir(pyLibDir)
					cmd.Run()

					// Add git submodules
					cmd = ctx.Command("git", "submodule", "add", "./grove-shared", "grove-shared").Dir(ecosystemDir)
					cmd.Run()
					cmd = ctx.Command("git", "submodule", "add", "./grove-go-app", "grove-go-app").Dir(ecosystemDir)
					cmd.Run()
					cmd = ctx.Command("git", "submodule", "add", "./grove-py-lib", "grove-py-lib").Dir(ecosystemDir)
					cmd.Run()

					// Get grove binary path
					groveBinary := ctx.GroveBinary

					// Step 1: Generate release plan with skip-parent to avoid submodule issues
					planCmd := ctx.Command(groveBinary, "release", "plan", "--skip-parent").Dir(ecosystemDir)
					planResult := planCmd.Run()

					planOutput := planResult.Stdout + planResult.Stderr

					// Should show version calculations
					if !strings.Contains(planOutput, "grove-shared") {
						return fmt.Errorf("expected grove-shared in release output:\n%s", planOutput)
					}

					// Should not fail on Python project
					if strings.Contains(planOutput, "go mod tidy failed") && strings.Contains(planOutput, "grove-py-lib") {
						return fmt.Errorf("go mod tidy should not run for Python project:\n%s", planOutput)
					}

					// Step 2: Test dry-run apply
					applyCmd := ctx.Command(groveBinary, "release", "apply", "--dry-run", "--yes", "--skip-parent").Dir(ecosystemDir)
					applyResult := applyCmd.Run()

					applyOutput := applyResult.Stdout + applyResult.Stderr

					// Verify it handles different project types
					if !strings.Contains(applyOutput, "DRY RUN") {
						return fmt.Errorf("expected dry run mode in apply output:\n%s", applyOutput)
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