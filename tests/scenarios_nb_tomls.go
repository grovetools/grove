package tests

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/grovetools/tend/pkg/fs"
	"github.com/grovetools/tend/pkg/harness"
	"github.com/grovetools/tend/pkg/verify"
)

// nbCreateGitRepo creates a minimal .git directory marker at the given path.
func nbCreateGitRepo(repoPath string) error {
	gitDir := filepath.Join(repoPath, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		return fmt.Errorf("failed to create .git dir at %s: %w", repoPath, err)
	}
	return fs.WriteString(filepath.Join(gitDir, "HEAD"), "ref: refs/heads/main\n")
}

// nbCreateEcosystemDir creates a directory with a grove.toml ecosystem marker.
func nbCreateEcosystemDir(path string, name string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return err
	}
	content := fmt.Sprintf("name = %q\nworkspaces = [\"*\"]\n", name)
	return fs.WriteString(filepath.Join(path, "grove.toml"), content)
}

// nbSetupGlobalConfig creates a global grove.toml with the given TOML content.
func nbSetupGlobalConfig(ctx *harness.Context, configTOML string) error {
	globalConfigDir := filepath.Join(ctx.ConfigDir(), "grove")
	if err := os.MkdirAll(globalConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create global config dir: %w", err)
	}
	return fs.WriteString(filepath.Join(globalConfigDir, "grove.toml"), configTOML)
}

// nbResolveSymlinks resolves symlinks in a path to avoid macOS /var → /private/var mismatches.
func nbResolveSymlinks(path string) string {
	if err := os.MkdirAll(path, 0755); err != nil {
		return path
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

// GroveInitNotebookScenario tests that `grove init` writes config to the notebook
// directory by default, and `grove init --local` writes to the project directory.
func GroveInitNotebookScenario() *harness.Scenario {
	return harness.NewScenario(
		"nb-tomls-grove-init",
		"grove init writes to notebook dir; grove init --local writes locally",
		[]string{"nb-tomls", "init"},
		[]harness.Step{
			harness.NewStep("Create environment", func(ctx *harness.Context) error {
				root := nbResolveSymlinks(ctx.NewDir("grove-init"))
				nbRoot := nbResolveSymlinks(ctx.NewDir("nb-init"))
				ctx.Set("grove_root", root)
				ctx.Set("notebook_root", nbRoot)

				if err := nbCreateEcosystemDir(root, "init-eco"); err != nil {
					return err
				}
				if err := nbCreateGitRepo(root); err != nil {
					return err
				}

				return nil
			}),
			harness.NewStep("Setup global config", func(ctx *harness.Context) error {
				root := ctx.Get("grove_root").(string)
				nbRoot := ctx.Get("notebook_root").(string)
				return nbSetupGlobalConfig(ctx, fmt.Sprintf(`[groves.work]
path = %q
enabled = true
depth = 1
notebook = "nb"

[notebooks.definitions.nb]
root_dir = %q
`, root, nbRoot))
			}),
			harness.NewStep("grove init creates notebook config for naked repo", func(ctx *harness.Context) error {
				root := ctx.Get("grove_root").(string)
				nbRoot := ctx.Get("notebook_root").(string)

				initRepo := filepath.Join(root, "init-target")
				if err := nbCreateGitRepo(initRepo); err != nil {
					return err
				}

				cmd := ctx.Bin("init")
				cmd.Dir(initRepo)
				result := cmd.Run()
				ctx.ShowCommandOutput("grove init", result.Stdout, result.Stderr)

				if err := ctx.Check("grove init succeeds", result.AssertSuccess()); err != nil {
					return fmt.Errorf("grove init failed: %w\nStdout: %s\nStderr: %s", err, result.Stdout, result.Stderr)
				}

				nbPath := filepath.Join(nbRoot, "workspaces", "init-target", "grove.toml")
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("notebook config created", nil, fs.AssertExists(nbPath))
					v.Equal("has project name", nil, fs.AssertContains(nbPath, "init-target"))
					v.Equal("no local config created", nil, fs.AssertNotExists(filepath.Join(initRepo, "grove.toml")))
				})
			}),
			harness.NewStep("grove init --local creates config in project dir", func(ctx *harness.Context) error {
				root := ctx.Get("grove_root").(string)

				localRepo := filepath.Join(root, "local-target")
				if err := nbCreateGitRepo(localRepo); err != nil {
					return err
				}

				cmd := ctx.Bin("init", "--local")
				cmd.Dir(localRepo)
				result := cmd.Run()
				ctx.ShowCommandOutput("grove init --local", result.Stdout, result.Stderr)

				if err := ctx.Check("grove init --local succeeds", result.AssertSuccess()); err != nil {
					return fmt.Errorf("grove init --local failed: %w\nStdout: %s\nStderr: %s", err, result.Stdout, result.Stderr)
				}

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("local config created", nil, fs.AssertExists(filepath.Join(localRepo, "grove.toml")))
					v.Equal("has project name", nil, fs.AssertContains(filepath.Join(localRepo, "grove.toml"), "local-target"))
				})
			}),
			harness.NewStep("grove init rejects duplicate", func(ctx *harness.Context) error {
				root := ctx.Get("grove_root").(string)
				initRepo := filepath.Join(root, "init-target")

				cmd := ctx.Bin("init")
				cmd.Dir(initRepo)
				result := cmd.Run()

				return ctx.Verify(func(v *verify.Collector) {
					v.NotEqual("exits with error", 0, result.ExitCode)
					v.Contains("error mentions already exists", result.Stderr, "already exists")
				})
			}),
		},
	)
}
