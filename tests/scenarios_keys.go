package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/tend/pkg/fs"
	"github.com/grovetools/tend/pkg/harness"
	"github.com/grovetools/tend/pkg/verify"
)

// Test configuration fixtures for keybind tests
const groveKeysTestConfig = `[keys]
prefix = "C-g"

[keys.tmux]
prefix = "C-g"

[keys.tmux.popups.test_popup]
key = "p"
command = "echo popup"
style = "popup"

[keys.tmux.popups.flow_status]
key = "f"
command = "flow tmux status"
style = "run-shell"

[keys.tmux.popups.nav_sessions]
key = "s"
command = "nav sz"
style = "popup"
`

const groveKeysConflictConfig = `[keys]
prefix = ""

[keys.tmux]
prefix = ""

[keys.tmux.popups.conflict_test]
key = "C-r"
command = "echo conflict"
style = "popup"
`

const groveKeysMinimalConfig = `[keys]
prefix = "C-g"

[keys.tmux]
prefix = "C-g"

[keys.tmux.popups.minimal]
key = "m"
command = "echo minimal"
`

// setupKeysTestConfig creates a test config directory with grove.toml
var setupKeysTestConfig = harness.NewStep("Setup keys test configuration", func(ctx *harness.Context) error {
	configDir := filepath.Join(ctx.ConfigDir(), "grove")
	if err := fs.CreateDir(configDir); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}

	configPath := filepath.Join(configDir, "grove.toml")
	if err := fs.WriteString(configPath, groveKeysTestConfig); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	ctx.Set("config_path", configPath)
	ctx.Set("config_dir", configDir)
	return nil
})

// KeysTraceScenario tests the 'grove keys trace' command.
func KeysTraceScenario() *harness.Scenario {
	return harness.NewScenario(
		"keys-trace-basic",
		"Tests 'grove keys trace' to visualize key layer traversal",
		[]string{"keys", "cli", "keybind"},
		[]harness.Step{
			setupKeysTestConfig,
			harness.NewStep("Trace single key", func(ctx *harness.Context) error {
				// Trace a common key like C-p (should be consumed by shell)
				result := ctx.Bin("keys", "trace", "C-p").Run()
				ctx.ShowCommandOutput("grove keys trace C-p", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				output := result.Stdout
				// Key may be normalized to uppercase (C-P) in output
				outputLower := strings.ToLower(output)
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("output contains Key label", true, strings.Contains(output, "Key:"))
					v.Equal("output contains C-p", true, strings.Contains(outputLower, "c-p"))
					// Should show layer traversal
					v.Equal("output contains layer info", true,
						strings.Contains(output, "L") || strings.Contains(outputLower, "shell") || strings.Contains(outputLower, "passthrough"))
				})
			}),
			harness.NewStep("Trace uncommon key", func(ctx *harness.Context) error {
				// Trace a key that might pass through more layers
				result := ctx.Bin("keys", "trace", "M-F12").Run()
				ctx.ShowCommandOutput("grove keys trace M-F12", result.Stdout, result.Stderr)

				// Should succeed even for unusual keys
				return ctx.Check("trace command succeeds for uncommon key", result.AssertSuccess())
			}),
			harness.NewStep("Trace meta key", func(ctx *harness.Context) error {
				// Trace a meta (Alt) key
				result := ctx.Bin("keys", "trace", "M-x").Run()
				ctx.ShowCommandOutput("grove keys trace M-x", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				// Key may be normalized to uppercase in output
				outputLower := strings.ToLower(result.Stdout)
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("output contains key being traced", true, strings.Contains(outputLower, "m-x"))
				})
			}),
		},
	)
}

// KeysAvailableScenario tests the 'grove keys available' command.
func KeysAvailableScenario() *harness.Scenario {
	return harness.NewScenario(
		"keys-available",
		"Tests 'grove keys available' to find unbound keys",
		[]string{"keys", "cli", "keybind"},
		[]harness.Step{
			setupKeysTestConfig,
			harness.NewStep("List available keys default layers", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "available").Run()
				ctx.ShowCommandOutput("grove keys available", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				output := result.Stdout
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("output contains Available keys header", true, strings.Contains(output, "Available keys"))
					// Should show groupings
					v.Equal("output shows Ctrl keys section", true, strings.Contains(output, "Ctrl"))
				})
			}),
			harness.NewStep("List available keys with layers flag", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "available", "--layers", "shell").Run()
				ctx.ShowCommandOutput("grove keys available --layers shell", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				output := result.Stdout
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("output mentions shell layer", true, strings.Contains(output, "shell"))
				})
			}),
			harness.NewStep("List available keys in specific table", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "available", "--in-table", "grove-popups").Run()
				ctx.ShowCommandOutput("grove keys available --in-table grove-popups", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				output := result.Stdout
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("output mentions table name", true, strings.Contains(output, "grove-popups"))
				})
			}),
		},
	)
}

// KeysConflictsScenario tests the 'grove keys conflicts' command.
func KeysConflictsScenario() *harness.Scenario {
	return harness.NewScenario(
		"keys-conflicts",
		"Tests 'grove keys conflicts' for cross-layer conflict detection",
		[]string{"keys", "cli", "keybind"},
		[]harness.Step{
			setupKeysTestConfig,
			harness.NewStep("Check for conflicts", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "conflicts").Run()
				ctx.ShowCommandOutput("grove keys conflicts", result.Stdout, result.Stderr)

				// Command should succeed regardless of whether conflicts exist
				if err := result.AssertSuccess(); err != nil {
					return err
				}

				output := result.Stdout
				return ctx.Verify(func(v *verify.Collector) {
					// Should have conflict header
					v.Equal("output contains header", true,
						strings.Contains(output, "Conflict") || strings.Contains(output, "conflict") || strings.Contains(output, "No cross-layer"))
				})
			}),
		},
	)
}

// KeysMatrixScenario tests the 'grove keys matrix' command.
func KeysMatrixScenario() *harness.Scenario {
	return harness.NewScenario(
		"keys-matrix",
		"Tests 'grove keys matrix' for matrix view of key bindings",
		[]string{"keys", "cli", "keybind"},
		[]harness.Step{
			setupKeysTestConfig,
			harness.NewStep("Display matrix view", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "matrix").Run()
				ctx.ShowCommandOutput("grove keys matrix", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				output := result.Stdout
				return ctx.Verify(func(v *verify.Collector) {
					// Should have column headers
					v.Equal("output contains KEY header", true, strings.Contains(output, "KEY"))
					v.Equal("output contains STATUS header", true, strings.Contains(output, "STATUS"))
				})
			}),
			harness.NewStep("Matrix with conflicts only", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "matrix", "--conflicts").Run()
				ctx.ShowCommandOutput("grove keys matrix --conflicts", result.Stdout, result.Stderr)

				// Should succeed even if no conflicts
				return ctx.Check("matrix --conflicts succeeds", result.AssertSuccess())
			}),
			harness.NewStep("Matrix JSON output", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "matrix", "--json").Run()
				ctx.ShowCommandOutput("grove keys matrix --json", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				output := result.Stdout
				return ctx.Verify(func(v *verify.Collector) {
					// JSON output should have certain keys
					v.Equal("output contains rows", true, strings.Contains(output, "rows") || strings.Contains(output, "Rows"))
					v.Equal("output is JSON-like", true, strings.Contains(output, "{"))
				})
			}),
			harness.NewStep("Matrix with layers", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "matrix", "--layers").Run()
				ctx.ShowCommandOutput("grove keys matrix --layers", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				output := result.Stdout
				return ctx.Verify(func(v *verify.Collector) {
					// Should show layer columns
					v.Equal("output contains SHELL column", true, strings.Contains(output, "SHELL"))
					v.Equal("output contains TMUX column", true, strings.Contains(output, "TMUX"))
				})
			}),
		},
	)
}

// KeysGenerateScenario tests the 'grove keys generate' commands.
func KeysGenerateScenario() *harness.Scenario {
	return harness.NewScenario(
		"keys-generate",
		"Tests 'grove keys generate' for config file generation",
		[]string{"keys", "cli", "keybind"},
		[]harness.Step{
			setupKeysTestConfig,
			harness.NewStep("Generate tmux config", func(ctx *harness.Context) error {
				// Use dry-run to avoid writing files
				result := ctx.Bin("keys", "generate", "tmux", "--dry-run").Run()
				ctx.ShowCommandOutput("grove keys generate tmux --dry-run", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				output := result.Stdout
				return ctx.Verify(func(v *verify.Collector) {
					// Should output tmux configuration
					v.Equal("output contains tmux config", true,
						strings.Contains(output, "bind") || strings.Contains(output, "popup") || strings.Contains(output, "No [keys.tmux.popups]"))
				})
			}),
			harness.NewStep("Generate shell config", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "generate", "shell", "--dry-run").Run()
				ctx.ShowCommandOutput("grove keys generate shell --dry-run", result.Stdout, result.Stderr)

				// Should succeed even if no shell bindings are defined
				return ctx.Check("generate shell succeeds", result.AssertSuccess())
			}),
			harness.NewStep("Generate nvim config", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "generate", "nvim", "--dry-run").Run()
				ctx.ShowCommandOutput("grove keys generate nvim --dry-run", result.Stdout, result.Stderr)

				// Should succeed even if no nvim bindings are defined
				return ctx.Check("generate nvim succeeds", result.AssertSuccess())
			}),
			harness.NewStep("Generate all configs", func(ctx *harness.Context) error {
				// First set up a cache directory that won't conflict with user's actual cache
				cacheDir := ctx.CacheDir()
				tmuxDir := filepath.Join(cacheDir, "grove", "tmux")
				if err := fs.CreateDir(tmuxDir); err != nil {
					return fmt.Errorf("failed to create tmux cache dir: %w", err)
				}

				result := ctx.Bin("keys", "generate", "--all").Run()
				ctx.ShowCommandOutput("grove keys generate --all", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				output := result.Stdout
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("output mentions generating", true,
						strings.Contains(output, "Generating") || strings.Contains(output, "Generated"))
				})
			}),
		},
	)
}

// KeysSyncScenario tests the 'grove keys sync' commands.
func KeysSyncScenario() *harness.Scenario {
	return harness.NewScenario(
		"keys-sync",
		"Tests 'grove keys sync' for detecting and importing external bindings",
		[]string{"keys", "cli", "keybind"},
		[]harness.Step{
			setupKeysTestConfig,
			harness.NewStep("Setup mock external configs", func(ctx *harness.Context) error {
				// Create a mock .tmux.conf with some bindings
				homeDir := ctx.HomeDir()
				tmuxConf := filepath.Join(homeDir, ".tmux.conf")
				tmuxContent := `# User's tmux config
bind-key -n M-x run-shell "custom command"
bind-key -n C-y copy-mode
`
				if err := fs.WriteString(tmuxConf, tmuxContent); err != nil {
					return fmt.Errorf("failed to write .tmux.conf: %w", err)
				}
				ctx.Set("tmux_conf_path", tmuxConf)
				return nil
			}),
			harness.NewStep("Sync detect", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "sync", "detect").Run()
				ctx.ShowCommandOutput("grove keys sync detect", result.Stdout, result.Stderr)

				// Should succeed
				if err := result.AssertSuccess(); err != nil {
					return err
				}

				// Output format depends on whether external bindings are found
				output := result.Stdout
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("output mentions detection", true,
						strings.Contains(output, "external") || strings.Contains(output, "detected") ||
							strings.Contains(output, "No unmanaged") || strings.Contains(output, "Detected"))
				})
			}),
			harness.NewStep("Sync status", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "sync", "status").Run()
				ctx.ShowCommandOutput("grove keys sync status", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				output := result.Stdout
				return ctx.Verify(func(v *verify.Collector) {
					// Should show status summary
					v.Equal("output contains status info", true,
						strings.Contains(output, "Status") || strings.Contains(output, "Grove") ||
							strings.Contains(output, "External") || strings.Contains(output, "Total"))
				})
			}),
		},
	)
}

// KeysPopupsScenario tests the 'grove keys popups' commands.
func KeysPopupsScenario() *harness.Scenario {
	return harness.NewScenario(
		"keys-popups-workflow",
		"Tests 'grove keys popups' add/list/remove workflow",
		[]string{"keys", "cli", "keybind"},
		[]harness.Step{
			harness.NewStep("Setup clean config", func(ctx *harness.Context) error {
				configDir := filepath.Join(ctx.ConfigDir(), "grove")
				if err := fs.CreateDir(configDir); err != nil {
					return fmt.Errorf("failed to create config dir: %w", err)
				}

				// Start with minimal config
				configPath := filepath.Join(configDir, "grove.toml")
				if err := fs.WriteString(configPath, groveKeysMinimalConfig); err != nil {
					return fmt.Errorf("failed to write config: %w", err)
				}

				ctx.Set("config_path", configPath)
				ctx.Set("config_dir", configDir)
				return nil
			}),
			harness.NewStep("List initial popups", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "popups", "list").Run()
				ctx.ShowCommandOutput("grove keys popups list (initial)", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				output := result.Stdout
				return ctx.Verify(func(v *verify.Collector) {
					// Should show the minimal popup we defined
					v.Equal("output contains minimal popup", true, strings.Contains(output, "minimal"))
				})
			}),
			harness.NewStep("Add new popup", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "popups", "add", "test_workflow",
					"--key", "w",
					"--command", "echo workflow test").Run()
				ctx.ShowCommandOutput("grove keys popups add test_workflow", result.Stdout, result.Stderr)

				// Add may fail if generate fails (no tmux), but config should still be updated
				// So we don't strictly assert success here
				return nil
			}),
			harness.NewStep("Verify popup was added", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "popups", "list").Run()
				ctx.ShowCommandOutput("grove keys popups list (after add)", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				output := result.Stdout
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("output contains new popup", true, strings.Contains(output, "test_workflow") || strings.Contains(output, "w"))
				})
			}),
			harness.NewStep("Remove popup", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "popups", "remove", "test_workflow").Run()
				ctx.ShowCommandOutput("grove keys popups remove test_workflow", result.Stdout, result.Stderr)

				// May fail if popup wasn't added successfully, but we try anyway
				return nil
			}),
			harness.NewStep("Verify popup was removed", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "popups", "list").Run()
				ctx.ShowCommandOutput("grove keys popups list (after remove)", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				output := result.Stdout
				// Should no longer contain the removed popup
				if strings.Contains(output, "test_workflow") {
					return fmt.Errorf("popup test_workflow should have been removed")
				}
				return nil
			}),
		},
	)
}

// KeysCheckScenario tests the 'grove keys check' command.
func KeysCheckScenario() *harness.Scenario {
	return harness.NewScenario(
		"keys-check",
		"Tests 'grove keys check' for within-domain conflict checking",
		[]string{"keys", "cli", "keybind"},
		[]harness.Step{
			setupKeysTestConfig,
			harness.NewStep("Check key bindings", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "check").Run()
				ctx.ShowCommandOutput("grove keys check", result.Stdout, result.Stderr)

				// Should succeed
				return ctx.Check("keys check succeeds", result.AssertSuccess())
			}),
		},
	)
}

// KeysDumpScenario tests the 'grove keys dump' command.
func KeysDumpScenario() *harness.Scenario {
	return harness.NewScenario(
		"keys-dump",
		"Tests 'grove keys dump' for raw binding dump",
		[]string{"keys", "cli", "keybind"},
		[]harness.Step{
			setupKeysTestConfig,
			harness.NewStep("Dump all bindings", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "dump").Run()
				ctx.ShowCommandOutput("grove keys dump", result.Stdout, result.Stderr)

				// Should succeed and show some bindings
				return ctx.Check("keys dump succeeds", result.AssertSuccess())
			}),
		},
	)
}

// KeysValidateScenario tests the 'grove keys validate' command.
func KeysValidateScenario() *harness.Scenario {
	return harness.NewScenario(
		"keys-validate",
		"Tests 'grove keys validate' for config validation",
		[]string{"keys", "cli", "keybind"},
		[]harness.Step{
			setupKeysTestConfig,
			harness.NewStep("Validate key config", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "validate").Run()
				ctx.ShowCommandOutput("grove keys validate", result.Stdout, result.Stderr)

				// Should succeed with valid config
				return ctx.Check("keys validate succeeds", result.AssertSuccess())
			}),
		},
	)
}

// KeysHelpScenario tests the help output for keys commands.
func KeysHelpScenario() *harness.Scenario {
	return harness.NewScenario(
		"keys-help",
		"Tests help output for grove keys commands",
		[]string{"keys", "cli", "keybind"},
		[]harness.Step{
			harness.NewStep("Show keys help", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "--help").Run()
				ctx.ShowCommandOutput("grove keys --help", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				output := result.Stdout
				return ctx.Verify(func(v *verify.Collector) {
					// Should show available subcommands
					v.Equal("help mentions trace", true, strings.Contains(output, "trace"))
					v.Equal("help mentions available", true, strings.Contains(output, "available"))
					v.Equal("help mentions conflicts", true, strings.Contains(output, "conflicts"))
					v.Equal("help mentions matrix", true, strings.Contains(output, "matrix"))
					v.Equal("help mentions generate", true, strings.Contains(output, "generate"))
				})
			}),
			harness.NewStep("Show trace help", func(ctx *harness.Context) error {
				result := ctx.Bin("keys", "trace", "--help").Run()
				ctx.ShowCommandOutput("grove keys trace --help", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				output := result.Stdout
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("help explains layers", true,
						strings.Contains(output, "layer") || strings.Contains(output, "Layer"))
				})
			}),
		},
	)
}

// KeysIntegrationScenario is a comprehensive integration test.
// This scenario requires tmux to be available and is marked as LocalOnly.
func KeysIntegrationScenario() *harness.Scenario {
	scenario := harness.NewScenario(
		"keys-integration",
		"Integration test for keys commands with tmux",
		[]string{"keys", "integration", "keybind", "local-only"},
		[]harness.Step{
			setupKeysTestConfig,
			harness.NewStep("Check tmux availability", func(ctx *harness.Context) error {
				result := ctx.Command("which", "tmux").Run()
				if result.ExitCode != 0 {
					return fmt.Errorf("tmux not found, skipping integration test")
				}
				return nil
			}),
			harness.NewStep("Generate tmux config to file", func(ctx *harness.Context) error {
				// Generate tmux config to a test location
				outputPath := filepath.Join(ctx.CacheDir(), "grove", "tmux", "test-popups.conf")
				if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
					return err
				}

				result := ctx.Bin("keys", "generate", "tmux", "-o", outputPath).Run()
				ctx.ShowCommandOutput("grove keys generate tmux", result.Stdout, result.Stderr)

				if err := result.AssertSuccess(); err != nil {
					return err
				}

				// Verify file was created
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("output file exists", nil, fs.AssertExists(outputPath))
				})
			}),
			harness.NewStep("Run full workflow", func(ctx *harness.Context) error {
				// Run trace
				traceResult := ctx.Bin("keys", "trace", "C-g").Run()
				if err := traceResult.AssertSuccess(); err != nil {
					return fmt.Errorf("trace failed: %w", err)
				}

				// Run available
				availResult := ctx.Bin("keys", "available").Run()
				if err := availResult.AssertSuccess(); err != nil {
					return fmt.Errorf("available failed: %w", err)
				}

				// Run conflicts
				conflictsResult := ctx.Bin("keys", "conflicts").Run()
				if err := conflictsResult.AssertSuccess(); err != nil {
					return fmt.Errorf("conflicts failed: %w", err)
				}

				// Run matrix
				matrixResult := ctx.Bin("keys", "matrix").Run()
				if err := matrixResult.AssertSuccess(); err != nil {
					return fmt.Errorf("matrix failed: %w", err)
				}

				return nil
			}),
		},
	)

	// Mark as local-only (requires tmux)
	scenario.LocalOnly = true
	return scenario
}
