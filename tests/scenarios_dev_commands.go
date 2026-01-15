package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattsolo1/grove-tend/pkg/fs"
	"github.com/mattsolo1/grove-tend/pkg/harness"
	"github.com/mattsolo1/grove-tend/pkg/verify"
	"github.com/mattsolo1/grove-meta/pkg/devlinks"
)

// setupMockEcosystem creates a sandboxed environment with two "worktrees",
// each containing different versions of mock binaries. This provides a
// realistic testbed for dev link commands.
var setupMockEcosystem = harness.NewStep("Setup mock ecosystem", func(ctx *harness.Context) error {
	worktrees := map[string]string{
		"main":    "worktree-main",
		"feature": "worktree-feature",
	}
	binaries := []string{"flow", "cx"}

	for version, dirName := range worktrees {
		worktreePath := filepath.Join(ctx.RootDir, dirName)
		binPath := filepath.Join(worktreePath, "bin")
		if err := fs.CreateDir(binPath); err != nil {
			return err
		}

		// Create mock binaries that echo their version
		for _, bin := range binaries {
			scriptPath := filepath.Join(binPath, bin)
			scriptContent := fmt.Sprintf("#!/bin/sh\necho \"%s %s\"", bin, version)
			if err := fs.WriteString(scriptPath, scriptContent); err != nil {
				return err
			}
			if err := os.Chmod(scriptPath, 0755); err != nil {
				return err
			}
		}

		// Create a grove.yml with binary configuration for discovery
		groveYMLContent := fmt.Sprintf(`name: mock-project-%s
version: '1.0'
binaries:
  - name: flow
    path: bin/flow
  - name: cx
    path: bin/cx
`, version)
		if err := fs.WriteString(filepath.Join(worktreePath, "grove.yml"), groveYMLContent); err != nil {
			return err
		}

		ctx.Set(fmt.Sprintf("worktree_%s_path", version), worktreePath)
	}

	return nil
})

// DevCwdWorkflow tests the primary 'grove dev cwd' workflow.
func DevCwdWorkflow() *harness.Scenario {
	return harness.NewScenario(
		"dev-cwd-workflow",
		"Tests 'grove dev cwd' to switch global dev binaries",
		[]string{"dev-commands"},
		[]harness.Step{
			setupMockEcosystem,
			harness.NewStep("Switch to main worktree", func(ctx *harness.Context) error {
				worktreePath := ctx.Get("worktree_main_path").(string)
				result := ctx.Bin("dev", "cwd").Dir(worktreePath).Run()
				ctx.ShowCommandOutput("grove dev cwd (main)", result.Stdout, result.Stderr)
				return ctx.Check("run 'grove dev cwd' in main worktree", result.AssertSuccess())
			}),
			harness.NewStep("Verify main binaries are active", func(ctx *harness.Context) error {
				// Verify symlink
				groveHome := filepath.Join(ctx.HomeDir(), ".grove")
				symlinkPath := filepath.Join(groveHome, "bin", "flow")
				target, err := os.Readlink(symlinkPath)
				if err != nil {
					return fmt.Errorf("failed to read symlink: %w", err)
				}
				expectedTarget := filepath.Join(ctx.Get("worktree_main_path").(string), "bin", "flow")

				// Resolve symlinks in paths to handle /var vs /private/var on macOS
				resolvedTarget, _ := filepath.EvalSymlinks(target)
				resolvedExpected, _ := filepath.EvalSymlinks(expectedTarget)
				if resolvedTarget == "" {
					resolvedTarget = target
				}
				if resolvedExpected == "" {
					resolvedExpected = expectedTarget
				}

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("symlink points to main worktree", resolvedExpected, resolvedTarget)
				})
			}),
			harness.NewStep("Execute flow binary from main", func(ctx *harness.Context) error {
				// Execute the binary via the symlink
				groveHome := filepath.Join(ctx.HomeDir(), ".grove")
				flowPath := filepath.Join(groveHome, "bin", "flow")

				result := ctx.Command(flowPath).Run()
				ctx.ShowCommandOutput("flow (main)", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("flow outputs main version", true, strings.Contains(result.Stdout, "flow main"))
				})
			}),
			harness.NewStep("Switch to feature worktree", func(ctx *harness.Context) error {
				worktreePath := ctx.Get("worktree_feature_path").(string)
				result := ctx.Bin("dev", "cwd").Dir(worktreePath).Run()
				ctx.ShowCommandOutput("grove dev cwd (feature)", result.Stdout, result.Stderr)
				return ctx.Check("run 'grove dev cwd' in feature worktree", result.AssertSuccess())
			}),
			harness.NewStep("Verify feature binaries are active", func(ctx *harness.Context) error {
				// Verify symlink
				groveHome := filepath.Join(ctx.HomeDir(), ".grove")
				symlinkPath := filepath.Join(groveHome, "bin", "flow")
				target, err := os.Readlink(symlinkPath)
				if err != nil {
					return fmt.Errorf("failed to read symlink: %w", err)
				}
				expectedTarget := filepath.Join(ctx.Get("worktree_feature_path").(string), "bin", "flow")

				// Resolve symlinks in paths to handle /var vs /private/var on macOS
				resolvedTarget, _ := filepath.EvalSymlinks(target)
				resolvedExpected, _ := filepath.EvalSymlinks(expectedTarget)
				if resolvedTarget == "" {
					resolvedTarget = target
				}
				if resolvedExpected == "" {
					resolvedExpected = expectedTarget
				}

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("symlink points to feature worktree", resolvedExpected, resolvedTarget)
				})
			}),
			harness.NewStep("Execute flow binary from feature", func(ctx *harness.Context) error {
				// Execute the binary via the symlink
				groveHome := filepath.Join(ctx.HomeDir(), ".grove")
				flowPath := filepath.Join(groveHome, "bin", "flow")

				result := ctx.Command(flowPath).Run()
				ctx.ShowCommandOutput("flow (feature)", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("flow outputs feature version", true, strings.Contains(result.Stdout, "flow feature"))
				})
			}),
		},
	)
}

// DevLinkAndUseWorkflow tests 'grove dev link' and 'grove dev use'.
func DevLinkAndUseWorkflow() *harness.Scenario {
	return harness.NewScenario(
		"dev-link-use-workflow",
		"Tests linking multiple versions and switching with 'use'",
		[]string{"dev-commands"},
		[]harness.Step{
			setupMockEcosystem,
			harness.NewStep("Link main version", func(ctx *harness.Context) error {
				mainPath := ctx.Get("worktree_main_path").(string)
				result := ctx.Bin("dev", "link", mainPath, "--as", "main").Run()
				ctx.ShowCommandOutput("grove dev link (main)", result.Stdout, result.Stderr)
				return ctx.Check("link main version", result.AssertSuccess())
			}),
			harness.NewStep("Link feature version", func(ctx *harness.Context) error {
				featurePath := ctx.Get("worktree_feature_path").(string)
				result := ctx.Bin("dev", "link", featurePath, "--as", "feature").Run()
				ctx.ShowCommandOutput("grove dev link (feature)", result.Stdout, result.Stderr)
				return ctx.Check("link feature version", result.AssertSuccess())
			}),
			harness.NewStep("Verify 'main' is active by default", func(ctx *harness.Context) error {
				// Verify devlinks.json
				groveHome := filepath.Join(ctx.HomeDir(), ".grove")
				configPath := filepath.Join(groveHome, "devlinks.json")
				content, err := fs.ReadString(configPath)
				if err != nil {
					return fmt.Errorf("failed to read devlinks.json: %w", err)
				}
				var config devlinks.Config
				if err := json.Unmarshal([]byte(content), &config); err != nil {
					return fmt.Errorf("failed to unmarshal devlinks.json: %w", err)
				}

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("flow current is main", "main", config.Binaries["flow"].Current)
				})
			}),
			harness.NewStep("Execute flow to verify main version", func(ctx *harness.Context) error {
				groveHome := filepath.Join(ctx.HomeDir(), ".grove")
				flowPath := filepath.Join(groveHome, "bin", "flow")

				result := ctx.Command(flowPath).Run()
				ctx.ShowCommandOutput("flow (should be main)", result.Stdout, result.Stderr)

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("flow outputs main version", true, strings.Contains(result.Stdout, "flow main"))
				})
			}),
			harness.NewStep("Use 'grove dev use' to switch to feature", func(ctx *harness.Context) error {
				result := ctx.Bin("dev", "use", "flow", "feature").Run()
				ctx.ShowCommandOutput("grove dev use flow feature", result.Stdout, result.Stderr)
				return ctx.Check("run 'grove dev use'", result.AssertSuccess())
			}),
			harness.NewStep("Verify 'feature' is now active", func(ctx *harness.Context) error {
				groveHome := filepath.Join(ctx.HomeDir(), ".grove")
				flowPath := filepath.Join(groveHome, "bin", "flow")

				result := ctx.Command(flowPath).Run()
				ctx.ShowCommandOutput("flow (should be feature)", result.Stdout, result.Stderr)

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("flow outputs feature version", true, strings.Contains(result.Stdout, "flow feature"))
				})
			}),
			harness.NewStep("Verify devlinks.json was updated", func(ctx *harness.Context) error {
				groveHome := filepath.Join(ctx.HomeDir(), ".grove")
				configPath := filepath.Join(groveHome, "devlinks.json")
				content, err := fs.ReadString(configPath)
				if err != nil {
					return fmt.Errorf("failed to read devlinks.json: %w", err)
				}
				var config devlinks.Config
				if err := json.Unmarshal([]byte(content), &config); err != nil {
					return fmt.Errorf("failed to unmarshal devlinks.json: %w", err)
				}

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("flow current is feature", "feature", config.Binaries["flow"].Current)
				})
			}),
		},
	)
}

// DevPointWorkflow tests workspace-specific overrides with 'grove dev point'.
func DevPointWorkflow() *harness.Scenario {
	return harness.NewScenario(
		"dev-point-workflow",
		"Tests workspace-scoped overrides with 'grove dev point'",
		[]string{"dev-commands"},
		[]harness.Step{
			setupMockEcosystem,
			harness.NewStep("Setup project and global default", func(ctx *harness.Context) error {
				// Create a project directory
				projectPath := filepath.Join(ctx.RootDir, "my-project")
				if err := fs.CreateDir(projectPath); err != nil {
					return err
				}
				// Create grove.yml to make it a grove workspace
				if err := fs.WriteString(filepath.Join(projectPath, "grove.yml"), "name: my-project\nversion: '1.0'"); err != nil {
					return err
				}
				ctx.Set("project_path", projectPath)

				// Set a global default dev link using cwd
				worktreePath := ctx.Get("worktree_main_path").(string)
				result := ctx.Bin("dev", "cwd").Dir(worktreePath).Run()
				ctx.ShowCommandOutput("grove dev cwd (main)", result.Stdout, result.Stderr)
				return ctx.Check("set global dev link", result.AssertSuccess())
			}),
			harness.NewStep("Verify global default is used", func(ctx *harness.Context) error {
				groveHome := filepath.Join(ctx.HomeDir(), ".grove")
				flowPath := filepath.Join(groveHome, "bin", "flow")

				result := ctx.Command(flowPath).Run()
				ctx.ShowCommandOutput("flow (global)", result.Stdout, result.Stderr)

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("flow outputs main version", true, strings.Contains(result.Stdout, "flow main"))
				})
			}),
			harness.NewStep("Point project's flow to feature version", func(ctx *harness.Context) error {
				projectPath := ctx.Get("project_path").(string)
				featureBinPath := filepath.Join(ctx.Get("worktree_feature_path").(string), "bin", "flow")
				result := ctx.Bin("dev", "point", "flow", featureBinPath).Dir(projectPath).Run()
				ctx.ShowCommandOutput("grove dev point", result.Stdout, result.Stderr)
				return ctx.Check("run 'grove dev point'", result.AssertSuccess())
			}),
			harness.NewStep("Verify override config was created", func(ctx *harness.Context) error {
				projectPath := ctx.Get("project_path").(string)
				overridePath := filepath.Join(projectPath, ".grove", "overrides.json")

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("overrides.json exists", nil, fs.AssertExists(overridePath))
					v.Equal("contains flow override", nil, fs.AssertContains(overridePath, "flow"))
				})
			}),
			harness.NewStep("Verify override points to feature binary", func(ctx *harness.Context) error {
				projectPath := ctx.Get("project_path").(string)
				overridePath := filepath.Join(projectPath, ".grove", "overrides.json")
				content, err := fs.ReadString(overridePath)
				if err != nil {
					return fmt.Errorf("failed to read overrides.json: %w", err)
				}

				featureBinPath := filepath.Join(ctx.Get("worktree_feature_path").(string), "bin", "flow")

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("override points to feature binary", true, strings.Contains(content, featureBinPath))
				})
			}),
		},
	)
}

// DevListWorkflow tests 'grove dev list' to show registered dev links.
func DevListWorkflow() *harness.Scenario {
	return harness.NewScenario(
		"dev-list-workflow",
		"Tests 'grove dev list' to display registered dev links",
		[]string{"dev-commands"},
		[]harness.Step{
			setupMockEcosystem,
			harness.NewStep("Link main and feature versions", func(ctx *harness.Context) error {
				mainPath := ctx.Get("worktree_main_path").(string)
				result := ctx.Bin("dev", "link", mainPath, "--as", "main").Run()
				if err := result.AssertSuccess(); err != nil {
					return fmt.Errorf("failed to link main: %w", err)
				}

				featurePath := ctx.Get("worktree_feature_path").(string)
				result = ctx.Bin("dev", "link", featurePath, "--as", "feature").Run()
				if err := result.AssertSuccess(); err != nil {
					return fmt.Errorf("failed to link feature: %w", err)
				}
				return nil
			}),
			harness.NewStep("Run grove dev list", func(ctx *harness.Context) error {
				result := ctx.Bin("dev", "list").Run()
				ctx.ShowCommandOutput("grove dev list", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				combinedOutput := result.Stdout + result.Stderr

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("list shows flow", true, strings.Contains(combinedOutput, "flow"))
					v.Equal("list shows main version", true, strings.Contains(combinedOutput, "main"))
					v.Equal("list shows feature version", true, strings.Contains(combinedOutput, "feature"))
				})
			}),
		},
	)
}
