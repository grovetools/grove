package tests

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/grovetools/tend/pkg/fs"
	"github.com/grovetools/tend/pkg/harness"
	"github.com/grovetools/tend/pkg/verify"
)

// --- Helpers ---

// createGitRepo creates a minimal .git directory marker at the given path.
func createGitRepo(repoPath string) error {
	gitDir := filepath.Join(repoPath, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		return fmt.Errorf("failed to create .git dir at %s: %w", repoPath, err)
	}
	return fs.WriteString(filepath.Join(gitDir, "HEAD"), "ref: refs/heads/main\n")
}

// createEcosystemDir creates a directory with a grove.toml ecosystem marker.
func createEcosystemDir(path string, name string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return err
	}
	content := fmt.Sprintf("name = %q\nworkspaces = [\"*\"]\n", name)
	return fs.WriteString(filepath.Join(path, "grove.toml"), content)
}

// createGroveProject creates a git repo with a grove.toml config file.
func createGroveProject(path string, name string) error {
	if err := createGitRepo(path); err != nil {
		return err
	}
	return fs.WriteString(filepath.Join(path, "grove.toml"), fmt.Sprintf("name = %q\n", name))
}

// setupGlobalConfig creates a global grove.toml with the given TOML content.
func setupGlobalConfig(ctx *harness.Context, configTOML string) error {
	globalConfigDir := filepath.Join(ctx.ConfigDir(), "grove")
	if err := os.MkdirAll(globalConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create global config dir: %w", err)
	}
	return fs.WriteString(filepath.Join(globalConfigDir, "grove.toml"), configTOML)
}

// resolveSymlinks resolves symlinks in a path to avoid macOS /var → /private/var mismatches.
// Creates the directory first since EvalSymlinks requires the path to exist.
func resolveSymlinks(path string) string {
	if err := os.MkdirAll(path, 0755); err != nil {
		return path
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

// runDiscovery runs `grove run -- echo discovered` from the given directory and returns
// the combined output. The output contains "Running in '<name>'" for each discovered workspace.
func runDiscovery(ctx *harness.Context, dir string) (string, error) {
	cmd := ctx.Bin("run", "--", "echo", "discovered")
	cmd.Dir(dir)
	result := cmd.Run()
	ctx.ShowCommandOutput("grove run -- echo discovered", result.Stdout, result.Stderr)
	if err := result.AssertSuccess(); err != nil {
		return "", fmt.Errorf("discovery failed: %w\nStdout: %s\nStderr: %s", err, result.Stdout, result.Stderr)
	}
	return result.Stdout + result.Stderr, nil
}

// --- Scenarios ---

// RelaxedDiscoveryScenario exercises depth, include_repos, exclude_repos, skip-list,
// and backward compatibility in a single filesystem layout.
//
// Layout:
//
//	grove-root/                (ecosystem with grove.toml)
//	├── project-a/             (grove project)
//	├── included-repo/         (naked git repo, in include_repos)
//	├── other-repo/            (naked git repo, NOT in include_repos)
//	├── excluded-project/      (grove project, in exclude_repos)
//	├── node_modules/hidden/   (grove project inside skip-list dir)
//	└── subdir/deep-project/   (naked git repo at depth 2)
func RelaxedDiscoveryScenario() *harness.Scenario {
	return harness.NewScenario(
		"nb-tomls-relaxed-discovery",
		"Exercises depth, include/exclude repos, skip-list, and backward compat in one layout",
		[]string{"nb-tomls", "discovery"},
		[]harness.Step{
			harness.NewStep("Create filesystem", func(ctx *harness.Context) error {
				root := resolveSymlinks(ctx.NewDir("grove-disc"))
				ctx.Set("grove_root", root)

				if err := createEcosystemDir(root, "disc-eco"); err != nil {
					return err
				}
				if err := createGitRepo(root); err != nil {
					return err
				}

				// grove project (always visible)
				if err := createGroveProject(filepath.Join(root, "project-a"), "project-a"); err != nil {
					return err
				}
				// naked repo to be included explicitly
				if err := createGitRepo(filepath.Join(root, "included-repo")); err != nil {
					return err
				}
				// naked repo NOT included (should stay hidden without depth)
				if err := createGitRepo(filepath.Join(root, "other-repo")); err != nil {
					return err
				}
				// grove project that will be excluded
				if err := createGroveProject(filepath.Join(root, "excluded-project"), "excluded-project"); err != nil {
					return err
				}
				// project inside node_modules (skip-list)
				if err := createGroveProject(filepath.Join(root, "node_modules", "hidden"), "hidden"); err != nil {
					return err
				}
				// naked repo at depth 2
				if err := os.MkdirAll(filepath.Join(root, "subdir"), 0755); err != nil {
					return err
				}
				if err := createGitRepo(filepath.Join(root, "subdir", "deep-project")); err != nil {
					return err
				}

				return nil
			}),

			// ---------- Run 1: depth=1, include_repos, exclude_repos ----------
			harness.NewStep("Configure depth=1 + include + exclude", func(ctx *harness.Context) error {
				root := ctx.Get("grove_root").(string)
				return setupGlobalConfig(ctx, fmt.Sprintf(`[groves.work]
path = %q
enabled = true
depth = 1
include_repos = ["included-repo"]
exclude_repos = ["excluded-project"]
`, root))
			}),
			harness.NewStep("Verify depth + include + exclude + skip-list", func(ctx *harness.Context) error {
				root := ctx.Get("grove_root").(string)
				out, err := runDiscovery(ctx, root)
				if err != nil {
					return err
				}
				return ctx.Verify(func(v *verify.Collector) {
					v.Contains("project-a discovered", out, "project-a")
					v.Contains("included-repo promoted by include_repos", out, "included-repo")
					v.Contains("other-repo promoted by depth=1", out, "other-repo")
					v.NotContains("excluded-project skipped", out, "excluded-project")
					v.NotContains("hidden inside node_modules skipped", out, "hidden")
					v.NotContains("deep-project beyond depth=1", out, "deep-project")
				})
			}),

			// ---------- Run 2: no depth, no include, no exclude (backward compat) ----------
			harness.NewStep("Reconfigure without depth/include/exclude", func(ctx *harness.Context) error {
				root := ctx.Get("grove_root").(string)
				return setupGlobalConfig(ctx, fmt.Sprintf(`[groves.work]
path = %q
enabled = true
`, root))
			}),
			harness.NewStep("Verify backward compat: only grove projects found", func(ctx *harness.Context) error {
				root := ctx.Get("grove_root").(string)
				out, err := runDiscovery(ctx, root)
				if err != nil {
					return err
				}
				return ctx.Verify(func(v *verify.Collector) {
					v.Contains("project-a still discovered", out, "project-a")
					v.Contains("excluded-project now visible (no exclude)", out, "excluded-project")
					v.NotContains("included-repo not promoted without depth/include", out, "included-repo")
					v.NotContains("other-repo not promoted without depth", out, "other-repo")
					v.NotContains("deep-project still hidden", out, "deep-project")
				})
			}),
		},
	)
}

// NotebookConfigScenario tests notebook config resolution, TOML parsing,
// merge ordering, and fallback format support in one scenario.
//
// Layout:
//
//	grove-root/                       (ecosystem)
//	├── proj-local/                   (has local grove.toml + notebook grove.toml)
//	├── proj-nb-only/                 (naked git repo, notebook provides config)
//	├── proj-yml-nb/                  (grove project, notebook in .yml format)
//	└── proj-yaml-nb/                 (grove project, notebook in .yaml format)
//	notebook-root/workspaces/
//	├── proj-local/grove.toml
//	├── proj-nb-only/grove.toml
//	├── proj-yml-nb/grove.yml
//	└── proj-yaml-nb/grove.yaml
func NotebookConfigScenario() *harness.Scenario {
	return harness.NewScenario(
		"nb-tomls-notebook-config",
		"Notebook config resolution, TOML parsing, merge order, and format fallback",
		[]string{"nb-tomls", "notebook", "config"},
		[]harness.Step{
			harness.NewStep("Create filesystem", func(ctx *harness.Context) error {
				root := resolveSymlinks(ctx.NewDir("grove-nb"))
				nbRoot := resolveSymlinks(ctx.NewDir("nb-root"))
				ctx.Set("grove_root", root)
				ctx.Set("notebook_root", nbRoot)

				// Ecosystem
				if err := createEcosystemDir(root, "nb-eco"); err != nil {
					return err
				}
				if err := createGitRepo(root); err != nil {
					return err
				}

				// proj-local: has local grove.toml AND notebook grove.toml
				if err := createGroveProject(filepath.Join(root, "proj-local"), "proj-local"); err != nil {
					return err
				}
				nbLocal := filepath.Join(nbRoot, "workspaces", "proj-local")
				if err := os.MkdirAll(nbLocal, 0755); err != nil {
					return err
				}
				if err := fs.WriteString(filepath.Join(nbLocal, "grove.toml"), `name = "proj-local-from-nb"

[extensions]
nb_setting = "from-notebook"
`); err != nil {
					return err
				}

				// proj-nb-only: naked git repo, config only in notebook
				if err := createGitRepo(filepath.Join(root, "proj-nb-only")); err != nil {
					return err
				}
				nbOnly := filepath.Join(nbRoot, "workspaces", "proj-nb-only")
				if err := os.MkdirAll(nbOnly, 0755); err != nil {
					return err
				}
				if err := fs.WriteString(filepath.Join(nbOnly, "grove.toml"), "name = \"proj-nb-only-from-nb\"\n"); err != nil {
					return err
				}

				// proj-yml-nb: grove project with .yml notebook config
				if err := createGroveProject(filepath.Join(root, "proj-yml-nb"), "proj-yml-nb"); err != nil {
					return err
				}
				nbYml := filepath.Join(nbRoot, "workspaces", "proj-yml-nb")
				if err := os.MkdirAll(nbYml, 0755); err != nil {
					return err
				}
				if err := fs.WriteString(filepath.Join(nbYml, "grove.yml"), "name: proj-yml-nb-from-nb\n"); err != nil {
					return err
				}

				// proj-yaml-nb: grove project with .yaml notebook config
				if err := createGroveProject(filepath.Join(root, "proj-yaml-nb"), "proj-yaml-nb"); err != nil {
					return err
				}
				nbYaml := filepath.Join(nbRoot, "workspaces", "proj-yaml-nb")
				if err := os.MkdirAll(nbYaml, 0755); err != nil {
					return err
				}
				if err := fs.WriteString(filepath.Join(nbYaml, "grove.yaml"), "name: proj-yaml-nb-from-nb\n"); err != nil {
					return err
				}

				return nil
			}),
			harness.NewStep("Setup global config with depth + notebook", func(ctx *harness.Context) error {
				root := ctx.Get("grove_root").(string)
				nbRoot := ctx.Get("notebook_root").(string)
				return setupGlobalConfig(ctx, fmt.Sprintf(`[groves.work]
path = %q
enabled = true
depth = 1
notebook = "nb"

[notebooks.definitions.nb]
root_dir = %q
`, root, nbRoot))
			}),
			harness.NewStep("Verify all projects discovered (including depth-promoted)", func(ctx *harness.Context) error {
				root := ctx.Get("grove_root").(string)
				out, err := runDiscovery(ctx, root)
				if err != nil {
					return err
				}
				return ctx.Verify(func(v *verify.Collector) {
					v.Contains("proj-local discovered", out, "proj-local")
					v.Contains("proj-nb-only promoted via depth", out, "proj-nb-only")
					v.Contains("proj-yml-nb discovered", out, "proj-yml-nb")
					v.Contains("proj-yaml-nb discovered", out, "proj-yaml-nb")
				})
			}),
			harness.NewStep("Verify notebook config files on disk", func(ctx *harness.Context) error {
				nbRoot := ctx.Get("notebook_root").(string)
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("TOML notebook exists", nil,
						fs.AssertExists(filepath.Join(nbRoot, "workspaces", "proj-local", "grove.toml")))
					v.Equal("TOML notebook for nb-only exists", nil,
						fs.AssertExists(filepath.Join(nbRoot, "workspaces", "proj-nb-only", "grove.toml")))
					v.Equal("YML notebook exists", nil,
						fs.AssertExists(filepath.Join(nbRoot, "workspaces", "proj-yml-nb", "grove.yml")))
					v.Equal("YAML notebook exists", nil,
						fs.AssertExists(filepath.Join(nbRoot, "workspaces", "proj-yaml-nb", "grove.yaml")))
				})
			}),
		},
	)
}

// FullFeatureFlowScenario validates the complete loop: configure depth, discover a naked
// git repo, bind its configuration from a centralized notebook, and verify ecosystem init
// still works when depth is set.
func FullFeatureFlowScenario() *harness.Scenario {
	return harness.NewScenario(
		"nb-tomls-full-flow",
		"End-to-end: depth discovery + notebook config + exclude + ecosystem init",
		[]string{"nb-tomls", "integration"},
		[]harness.Step{
			harness.NewStep("Create complete environment", func(ctx *harness.Context) error {
				root := resolveSymlinks(ctx.NewDir("grove-full"))
				nbRoot := resolveSymlinks(ctx.NewDir("nb-full"))
				ctx.Set("grove_root", root)
				ctx.Set("notebook_root", nbRoot)

				if err := createEcosystemDir(root, "full-eco"); err != nil {
					return err
				}
				if err := createGitRepo(root); err != nil {
					return err
				}

				// Grove project with local config
				if err := createGroveProject(filepath.Join(root, "configured-project"), "configured-project"); err != nil {
					return err
				}

				// Naked git repo - promoted by depth, config from notebook
				if err := createGitRepo(filepath.Join(root, "my-service")); err != nil {
					return err
				}
				nbSvc := filepath.Join(nbRoot, "workspaces", "my-service")
				if err := os.MkdirAll(nbSvc, 0755); err != nil {
					return err
				}
				if err := fs.WriteString(filepath.Join(nbSvc, "grove.toml"), `name = "my-service-from-notebook"

[extensions]
managed_by = "notebook"
service_port = "8080"
`); err != nil {
					return err
				}

				// Excluded repo
				if err := createGitRepo(filepath.Join(root, "excluded-service")); err != nil {
					return err
				}

				// Deep repo (depth 2, should not be found with depth=1)
				if err := os.MkdirAll(filepath.Join(root, "libs"), 0755); err != nil {
					return err
				}
				if err := createGitRepo(filepath.Join(root, "libs", "deep-lib")); err != nil {
					return err
				}

				return nil
			}),
			harness.NewStep("Setup global config", func(ctx *harness.Context) error {
				root := ctx.Get("grove_root").(string)
				nbRoot := ctx.Get("notebook_root").(string)
				return setupGlobalConfig(ctx, fmt.Sprintf(`[groves.work]
path = %q
enabled = true
depth = 1
notebook = "nb"
exclude_repos = ["excluded-service"]

[notebooks.definitions.nb]
root_dir = %q
`, root, nbRoot))
			}),
			harness.NewStep("Verify discovery with all features", func(ctx *harness.Context) error {
				root := ctx.Get("grove_root").(string)
				out, err := runDiscovery(ctx, root)
				if err != nil {
					return err
				}
				return ctx.Verify(func(v *verify.Collector) {
					v.Contains("configured-project discovered", out, "configured-project")
					v.Contains("my-service promoted via depth", out, "my-service")
					v.NotContains("excluded-service skipped", out, "excluded-service")
					v.NotContains("deep-lib beyond depth=1", out, "deep-lib")
				})
			}),
			harness.NewStep("Verify notebook config on disk", func(ctx *harness.Context) error {
				nbRoot := ctx.Get("notebook_root").(string)
				nbPath := filepath.Join(nbRoot, "workspaces", "my-service", "grove.toml")
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("notebook config exists", nil, fs.AssertExists(nbPath))
					v.Equal("has notebook name", nil, fs.AssertContains(nbPath, "my-service-from-notebook"))
					v.Equal("has extensions", nil, fs.AssertContains(nbPath, "managed_by"))
				})
			}),
			harness.NewStep("grove init creates notebook config for naked repo", func(ctx *harness.Context) error {
				root := ctx.Get("grove_root").(string)
				nbRoot := ctx.Get("notebook_root").(string)

				// Create another naked repo to init
				initRepo := filepath.Join(root, "init-target")
				if err := createGitRepo(initRepo); err != nil {
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
				if err := createGitRepo(localRepo); err != nil {
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
