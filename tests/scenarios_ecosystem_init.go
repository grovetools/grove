package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattsolo1/grove-tend/pkg/fs"
	"github.com/mattsolo1/grove-tend/pkg/harness"
	"github.com/mattsolo1/grove-tend/pkg/tui"
	"github.com/mattsolo1/grove-tend/pkg/verify"
)

// EcosystemInitAlreadyDiscoverableScenario tests that ecosystems in existing groves
// are recognized as discoverable without prompting.
func EcosystemInitAlreadyDiscoverableScenario() *harness.Scenario {
	return harness.NewScenario(
		"ecosystem-init-already-discoverable",
		"Verifies ecosystems in configured groves are recognized as discoverable",
		[]string{"ecosystem", "init", "discovery"},
		[]harness.Step{
			harness.NewStep("Create grove root directory", func(ctx *harness.Context) error {
				groveRoot := ctx.NewDir("grove-root")
				if err := os.MkdirAll(groveRoot, 0755); err != nil {
					return fmt.Errorf("failed to create grove root: %w", err)
				}
				ctx.Set("grove_root", groveRoot)
				return nil
			}),
			harness.NewStep("Setup global config with grove", func(ctx *harness.Context) error {
				groveRoot := ctx.Get("grove_root").(string)
				return setupGrovesConfig(ctx, "work", groveRoot)
			}),
			harness.NewStep("Run ecosystem init inside configured grove", func(ctx *harness.Context) error {
				groveRoot := ctx.Get("grove_root").(string)
				ecosystemName := "new-eco"

				cmd := ctx.Bin("ecosystem", "init", ecosystemName)
				cmd.Dir(groveRoot)
				result := cmd.Run()

				ctx.ShowCommandOutput("grove ecosystem init", result.Stdout, result.Stderr)

				if err := ctx.Check("command succeeds", result.AssertSuccess()); err != nil {
					return fmt.Errorf("ecosystem init failed: %w\nStdout: %s\nStderr: %s", err, result.Stdout, result.Stderr)
				}

				// Verify output indicates discoverability
				combinedOutput := result.Stdout + result.Stderr
				if !strings.Contains(combinedOutput, "discoverable via grove") {
					return fmt.Errorf("expected discoverability message in output, got:\n%s", combinedOutput)
				}

				// Should NOT prompt to add to groves
				if strings.Contains(combinedOutput, "Add") && strings.Contains(combinedOutput, "to groves") {
					return fmt.Errorf("should not prompt to add grove when already discoverable, got:\n%s", combinedOutput)
				}

				return nil
			}),
			harness.NewStep("Verify ecosystem files created", func(ctx *harness.Context) error {
				groveRoot := ctx.Get("grove_root").(string)
				ecosystemPath := filepath.Join(groveRoot, "new-eco")

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("grove.yml exists", nil, fs.AssertExists(filepath.Join(ecosystemPath, "grove.yml")))
					v.Equal("README.md exists", nil, fs.AssertExists(filepath.Join(ecosystemPath, "README.md")))
					v.Equal(".gitignore exists", nil, fs.AssertExists(filepath.Join(ecosystemPath, ".gitignore")))
				})
			}),
		},
	)
}

// EcosystemInitNotDiscoverableScenario tests the TUI prompt flow when
// an ecosystem is created outside of any configured grove.
func EcosystemInitNotDiscoverableScenario() *harness.Scenario {
	return harness.NewScenarioWithOptions(
		"ecosystem-init-not-discoverable",
		"Verifies TUI prompt appears and works when ecosystem is not in a configured grove",
		[]string{"ecosystem", "init", "discovery", "tui"},
		[]harness.Step{
			harness.NewStep("Create isolated directory (no grove)", func(ctx *harness.Context) error {
				isolatedRoot := ctx.NewDir("isolated-root")
				if err := os.MkdirAll(isolatedRoot, 0755); err != nil {
					return fmt.Errorf("failed to create isolated root: %w", err)
				}
				ctx.Set("isolated_root", isolatedRoot)
				return nil
			}),
			harness.NewStep("Setup global config WITHOUT this path", func(ctx *harness.Context) error {
				// Create a minimal config without any groves pointing to our isolated dir
				globalConfigDir := filepath.Join(ctx.ConfigDir(), "grove")
				if err := os.MkdirAll(globalConfigDir, 0755); err != nil {
					return fmt.Errorf("failed to create global config dir: %w", err)
				}

				config := `# Empty groves config
groves: {}
`
				configPath := filepath.Join(globalConfigDir, "grove.yml")
				return fs.WriteString(configPath, config)
			}),
			harness.NewStep("Launch TUI for ecosystem init", func(ctx *harness.Context) error {
				groveBinary, err := findGroveBinary()
				if err != nil {
					return fmt.Errorf("failed to find grove binary: %w", err)
				}

				isolatedRoot := ctx.Get("isolated_root").(string)

				session, err := ctx.StartTUI(groveBinary, []string{"ecosystem", "init", "my-eco"}, tui.WithCwd(isolatedRoot))
				if err != nil {
					return fmt.Errorf("failed to start TUI: %w", err)
				}
				ctx.Set("tui_session", session)

				// Wait for the ecosystem to be created and the prompt to appear
				if err := session.WaitForText("not in a configured grove", 10*time.Second); err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("TUI warning not shown: %w\nContent:\n%s", err, content)
				}

				return nil
			}),
			harness.NewStep("Confirm adding grove", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)

				// Wait for the prompt question
				if err := session.WaitForText("Add", 5*time.Second); err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("add prompt not shown: %w\nContent:\n%s", err, content)
				}

				// Confirm with 'y'
				if err := session.Type("y"); err != nil {
					return fmt.Errorf("failed to type 'y': %w", err)
				}

				// Wait for notebook selection (if any) or completion
				_, err := session.WaitForAnyText([]string{
					"Select notebook",
					"skip notebook association",
					"Added grove",
				}, 5*time.Second)
				if err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("notebook selection or completion not shown: %w\nContent:\n%s", err, content)
				}

				return nil
			}),
			harness.NewStep("Select no notebook and complete", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)

				// Check if we're at notebook selection
				content, _ := session.Capture(tui.WithCleanedOutput())
				if strings.Contains(content, "Select notebook") {
					// Navigate to "(skip notebook association)" option and select it
					// It should be the last option, so navigate down
					for i := 0; i < 5; i++ { // Navigate down a few times to reach the skip option
						if err := session.Type("j"); err != nil {
							return fmt.Errorf("failed to navigate: %w", err)
						}
					}

					if err := session.Type("Enter"); err != nil {
						return fmt.Errorf("failed to select: %w", err)
					}
				}

				// Wait for completion
				if err := session.WaitForText("Added grove", 5*time.Second); err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("completion message not shown: %w\nContent:\n%s", err, content)
				}

				time.Sleep(500 * time.Millisecond)
				return nil
			}),
			harness.NewStep("Verify config was updated", func(ctx *harness.Context) error {
				overridePath := filepath.Join(ctx.ConfigDir(), "grove", "grove.override.yml")

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("override config exists", nil, fs.AssertExists(overridePath))
					v.Equal("contains groves section", nil, fs.AssertContains(overridePath, "groves:"))
					// The grove name should be derived from the ecosystem name
					v.Equal("contains my-eco grove", nil, fs.AssertContains(overridePath, "my-eco"))
				})
			}),
			harness.NewStep("Verify ecosystem files created", func(ctx *harness.Context) error {
				isolatedRoot := ctx.Get("isolated_root").(string)
				ecosystemPath := filepath.Join(isolatedRoot, "my-eco")

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("grove.yml exists", nil, fs.AssertExists(filepath.Join(ecosystemPath, "grove.yml")))
					v.Equal("README.md exists", nil, fs.AssertExists(filepath.Join(ecosystemPath, "README.md")))
				})
			}),
		},
		true,  // localOnly - TUI tests require tmux
		false, // explicitOnly
	)
}

// EcosystemInitDeclineAddScenario tests declining to add the grove.
func EcosystemInitDeclineAddScenario() *harness.Scenario {
	return harness.NewScenarioWithOptions(
		"ecosystem-init-decline-add",
		"Verifies declining to add grove skips config modification",
		[]string{"ecosystem", "init", "discovery", "tui"},
		[]harness.Step{
			harness.NewStep("Create isolated directory", func(ctx *harness.Context) error {
				isolatedRoot := ctx.NewDir("isolated-decline")
				if err := os.MkdirAll(isolatedRoot, 0755); err != nil {
					return fmt.Errorf("failed to create isolated root: %w", err)
				}
				ctx.Set("isolated_root", isolatedRoot)
				return nil
			}),
			harness.NewStep("Setup empty global config", func(ctx *harness.Context) error {
				globalConfigDir := filepath.Join(ctx.ConfigDir(), "grove")
				if err := os.MkdirAll(globalConfigDir, 0755); err != nil {
					return fmt.Errorf("failed to create global config dir: %w", err)
				}

				config := "groves: {}\n"
				configPath := filepath.Join(globalConfigDir, "grove.yml")
				return fs.WriteString(configPath, config)
			}),
			harness.NewStep("Launch TUI and decline add", func(ctx *harness.Context) error {
				groveBinary, err := findGroveBinary()
				if err != nil {
					return fmt.Errorf("failed to find grove binary: %w", err)
				}

				isolatedRoot := ctx.Get("isolated_root").(string)

				session, err := ctx.StartTUI(groveBinary, []string{"ecosystem", "init", "declined-eco"}, tui.WithCwd(isolatedRoot))
				if err != nil {
					return fmt.Errorf("failed to start TUI: %w", err)
				}
				ctx.Set("tui_session", session)

				// Wait for the prompt
				if err := session.WaitForText("not in a configured grove", 10*time.Second); err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("TUI warning not shown: %w\nContent:\n%s", err, content)
				}

				// Decline with 'n'
				if err := session.Type("n"); err != nil {
					return fmt.Errorf("failed to type 'n': %w", err)
				}

				// Wait for skip message
				if err := session.WaitForText("Skipped", 5*time.Second); err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("skip message not shown: %w\nContent:\n%s", err, content)
				}

				time.Sleep(500 * time.Millisecond)
				return nil
			}),
			harness.NewStep("Verify config was NOT updated", func(ctx *harness.Context) error {
				overridePath := filepath.Join(ctx.ConfigDir(), "grove", "grove.override.yml")

				// Override file should not exist or not contain our grove
				if _, err := os.Stat(overridePath); os.IsNotExist(err) {
					// Good - file doesn't exist
					return nil
				}

				// File exists, verify it doesn't contain our grove
				content, err := fs.ReadString(overridePath)
				if err != nil {
					return fmt.Errorf("failed to read override config: %w", err)
				}

				if strings.Contains(content, "isolated-decline") {
					return fmt.Errorf("override config should not contain declined grove, got:\n%s", content)
				}

				return nil
			}),
			harness.NewStep("Verify ecosystem files still created", func(ctx *harness.Context) error {
				isolatedRoot := ctx.Get("isolated_root").(string)
				ecosystemPath := filepath.Join(isolatedRoot, "declined-eco")

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("grove.yml exists", nil, fs.AssertExists(filepath.Join(ecosystemPath, "grove.yml")))
					v.Equal("README.md exists", nil, fs.AssertExists(filepath.Join(ecosystemPath, "README.md")))
				})
			}),
		},
		true,  // localOnly - TUI tests require tmux
		false, // explicitOnly
	)
}

// EcosystemInitNonInteractiveScenario tests the non-interactive behavior
// when not in a TTY (just warns, doesn't prompt).
func EcosystemInitNonInteractiveScenario() *harness.Scenario {
	return harness.NewScenario(
		"ecosystem-init-non-interactive",
		"Verifies warning is shown but no prompt in non-interactive mode",
		[]string{"ecosystem", "init", "discovery", "cli"},
		[]harness.Step{
			harness.NewStep("Create isolated directory", func(ctx *harness.Context) error {
				isolatedRoot := ctx.NewDir("isolated-nonint")
				if err := os.MkdirAll(isolatedRoot, 0755); err != nil {
					return fmt.Errorf("failed to create isolated root: %w", err)
				}
				ctx.Set("isolated_root", isolatedRoot)
				return nil
			}),
			harness.NewStep("Setup empty global config", func(ctx *harness.Context) error {
				globalConfigDir := filepath.Join(ctx.ConfigDir(), "grove")
				if err := os.MkdirAll(globalConfigDir, 0755); err != nil {
					return fmt.Errorf("failed to create global config dir: %w", err)
				}

				config := "groves: {}\n"
				configPath := filepath.Join(globalConfigDir, "grove.yml")
				return fs.WriteString(configPath, config)
			}),
			harness.NewStep("Run ecosystem init non-interactively", func(ctx *harness.Context) error {
				isolatedRoot := ctx.Get("isolated_root").(string)

				// Using ctx.Bin() runs without a TTY (non-interactive)
				cmd := ctx.Bin("ecosystem", "init", "nonint-eco")
				cmd.Dir(isolatedRoot)
				result := cmd.Run()

				ctx.ShowCommandOutput("grove ecosystem init (non-interactive)", result.Stdout, result.Stderr)

				if err := ctx.Check("command succeeds", result.AssertSuccess()); err != nil {
					return fmt.Errorf("ecosystem init failed: %w\nStdout: %s\nStderr: %s", err, result.Stdout, result.Stderr)
				}

				// Should show warning about not being discoverable
				combinedOutput := result.Stdout + result.Stderr
				if !strings.Contains(combinedOutput, "not in a configured grove") {
					return fmt.Errorf("expected warning about not being in configured grove, got:\n%s", combinedOutput)
				}

				// Should mention grove tools and running in interactive terminal
				if !strings.Contains(combinedOutput, "grove tools") {
					return fmt.Errorf("expected message about grove tools, got:\n%s", combinedOutput)
				}
				if !strings.Contains(combinedOutput, "interactive terminal") || !strings.Contains(combinedOutput, "manually update") {
					return fmt.Errorf("expected message about interactive terminal or manual update, got:\n%s", combinedOutput)
				}

				return nil
			}),
			harness.NewStep("Verify ecosystem files created", func(ctx *harness.Context) error {
				isolatedRoot := ctx.Get("isolated_root").(string)
				ecosystemPath := filepath.Join(isolatedRoot, "nonint-eco")

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("grove.yml exists", nil, fs.AssertExists(filepath.Join(ecosystemPath, "grove.yml")))
					v.Equal("README.md exists", nil, fs.AssertExists(filepath.Join(ecosystemPath, "README.md")))
				})
			}),
		},
	)
}

// setupGrovesConfig creates a global grove.yml with the new 'groves' key format.
func setupGrovesConfig(ctx *harness.Context, groveName, grovePath string) error {
	globalConfigDir := filepath.Join(ctx.ConfigDir(), "grove")
	if err := os.MkdirAll(globalConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create global config dir: %w", err)
	}

	// Use the new 'groves' format instead of 'search_paths'
	config := fmt.Sprintf(`groves:
  %s:
    path: %s
    enabled: true
`, groveName, grovePath)

	configPath := filepath.Join(globalConfigDir, "grove.yml")
	return fs.WriteString(configPath, config)
}

// EcosystemInitPreservesConfigScenario tests that adding a grove preserves existing config.
func EcosystemInitPreservesConfigScenario() *harness.Scenario {
	return harness.NewScenarioWithOptions(
		"ecosystem-init-preserves-config",
		"Verifies that adding a grove preserves other config fields",
		[]string{"ecosystem", "init", "discovery", "tui", "config"},
		[]harness.Step{
			harness.NewStep("Create isolated directory", func(ctx *harness.Context) error {
				isolatedRoot := ctx.NewDir("isolated-preserve")
				if err := os.MkdirAll(isolatedRoot, 0755); err != nil {
					return fmt.Errorf("failed to create isolated root: %w", err)
				}
				ctx.Set("isolated_root", isolatedRoot)
				return nil
			}),
			harness.NewStep("Setup global config with existing fields", func(ctx *harness.Context) error {
				globalConfigDir := filepath.Join(ctx.ConfigDir(), "grove")
				if err := os.MkdirAll(globalConfigDir, 0755); err != nil {
					return fmt.Errorf("failed to create global config dir: %w", err)
				}

				// Create a config with multiple fields that should be preserved
				config := `# This is a user config with custom settings
notebooks:
  definitions:
    personal:
      root_dir: ~/notes
extensions:
  custom_setting: important_value
groves:
  existing-grove:
    path: /some/existing/path
    enabled: true
`
				configPath := filepath.Join(globalConfigDir, "grove.override.yml")
				return fs.WriteString(configPath, config)
			}),
			harness.NewStep("Launch TUI and confirm add", func(ctx *harness.Context) error {
				groveBinary, err := findGroveBinary()
				if err != nil {
					return fmt.Errorf("failed to find grove binary: %w", err)
				}

				isolatedRoot := ctx.Get("isolated_root").(string)

				session, err := ctx.StartTUI(groveBinary, []string{"ecosystem", "init", "preserve-eco"}, tui.WithCwd(isolatedRoot))
				if err != nil {
					return fmt.Errorf("failed to start TUI: %w", err)
				}
				ctx.Set("tui_session", session)

				// Wait for the prompt
				if err := session.WaitForText("not in a configured grove", 10*time.Second); err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("TUI warning not shown: %w\nContent:\n%s", err, content)
				}

				// Confirm with 'y'
				if err := session.Type("y"); err != nil {
					return fmt.Errorf("failed to type 'y': %w", err)
				}

				// Wait for notebook selection or completion
				_, err = session.WaitForAnyText([]string{
					"Select notebook",
					"skip notebook association",
					"Added grove",
				}, 5*time.Second)
				if err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("selection or completion not shown: %w\nContent:\n%s", err, content)
				}

				// If at notebook selection, press enter to skip
				content, _ := session.Capture(tui.WithCleanedOutput())
				if strings.Contains(content, "Select notebook") {
					// Navigate to skip option and select
					for i := 0; i < 5; i++ {
						if err := session.Type("j"); err != nil {
							return fmt.Errorf("failed to navigate: %w", err)
						}
					}
					if err := session.Type("Enter"); err != nil {
						return fmt.Errorf("failed to select: %w", err)
					}
				}

				// Wait for completion
				if err := session.WaitForText("Added grove", 5*time.Second); err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("completion not shown: %w\nContent:\n%s", err, content)
				}

				time.Sleep(500 * time.Millisecond)
				return nil
			}),
			harness.NewStep("Verify existing config was preserved", func(ctx *harness.Context) error {
				overridePath := filepath.Join(ctx.ConfigDir(), "grove", "grove.override.yml")

				return ctx.Verify(func(v *verify.Collector) {
					// Check that existing fields are still there
					v.Equal("notebooks section preserved", nil, fs.AssertContains(overridePath, "notebooks:"))
					v.Equal("personal notebook preserved", nil, fs.AssertContains(overridePath, "personal"))
					v.Equal("extensions preserved", nil, fs.AssertContains(overridePath, "extensions:"))
					v.Equal("custom_setting preserved", nil, fs.AssertContains(overridePath, "custom_setting"))
					v.Equal("important_value preserved", nil, fs.AssertContains(overridePath, "important_value"))
					v.Equal("existing-grove preserved", nil, fs.AssertContains(overridePath, "existing-grove"))
					// Check that new grove was added
					// The grove name should be the ecosystem name
					v.Equal("new grove added", nil, fs.AssertContains(overridePath, "preserve-eco"))
				})
			}),
		},
		true,  // localOnly - TUI tests require tmux
		false, // explicitOnly
	)
}

// EcosystemInitEditsCorrectFileScenario tests that the grove is added to the file
// where groves are already defined, not creating a new override file.
func EcosystemInitEditsCorrectFileScenario() *harness.Scenario {
	return harness.NewScenarioWithOptions(
		"ecosystem-init-edits-correct-file",
		"Verifies grove is added to the file where groves are defined",
		[]string{"ecosystem", "init", "discovery", "tui", "config"},
		[]harness.Step{
			harness.NewStep("Create isolated directory", func(ctx *harness.Context) error {
				isolatedRoot := ctx.NewDir("isolated-correct-file")
				if err := os.MkdirAll(isolatedRoot, 0755); err != nil {
					return fmt.Errorf("failed to create isolated root: %w", err)
				}
				ctx.Set("isolated_root", isolatedRoot)
				return nil
			}),
			harness.NewStep("Setup groves in base grove.yml (not override)", func(ctx *harness.Context) error {
				globalConfigDir := filepath.Join(ctx.ConfigDir(), "grove")
				if err := os.MkdirAll(globalConfigDir, 0755); err != nil {
					return fmt.Errorf("failed to create global config dir: %w", err)
				}

				// Put groves in the BASE grove.yml, not the override
				config := `# Base config with groves
groves:
  existing-grove:
    path: /some/existing/path
    enabled: true
`
				configPath := filepath.Join(globalConfigDir, "grove.yml")
				return fs.WriteString(configPath, config)
			}),
			harness.NewStep("Launch TUI and confirm add", func(ctx *harness.Context) error {
				groveBinary, err := findGroveBinary()
				if err != nil {
					return fmt.Errorf("failed to find grove binary: %w", err)
				}

				isolatedRoot := ctx.Get("isolated_root").(string)

				session, err := ctx.StartTUI(groveBinary, []string{"ecosystem", "init", "correct-file-eco"}, tui.WithCwd(isolatedRoot))
				if err != nil {
					return fmt.Errorf("failed to start TUI: %w", err)
				}
				ctx.Set("tui_session", session)

				// Wait for the prompt
				if err := session.WaitForText("not in a configured grove", 10*time.Second); err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("TUI warning not shown: %w\nContent:\n%s", err, content)
				}

				// Confirm with 'y'
				if err := session.Type("y"); err != nil {
					return fmt.Errorf("failed to type 'y': %w", err)
				}

				// Wait for completion (may show notebook selection first)
				_, err = session.WaitForAnyText([]string{
					"Select notebook",
					"Added grove",
				}, 5*time.Second)
				if err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("selection or completion not shown: %w\nContent:\n%s", err, content)
				}

				// If at notebook selection, skip
				content, _ := session.Capture(tui.WithCleanedOutput())
				if strings.Contains(content, "Select notebook") {
					for i := 0; i < 5; i++ {
						if err := session.Type("j"); err != nil {
							return fmt.Errorf("failed to navigate: %w", err)
						}
					}
					if err := session.Type("Enter"); err != nil {
						return fmt.Errorf("failed to select: %w", err)
					}
				}

				// Wait for completion
				if err := session.WaitForText("Added grove", 5*time.Second); err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("completion not shown: %w\nContent:\n%s", err, content)
				}

				time.Sleep(500 * time.Millisecond)
				return nil
			}),
			harness.NewStep("Verify grove.yml was edited (not override)", func(ctx *harness.Context) error {
				basePath := filepath.Join(ctx.ConfigDir(), "grove", "grove.yml")
				overridePath := filepath.Join(ctx.ConfigDir(), "grove", "grove.override.yml")

				return ctx.Verify(func(v *verify.Collector) {
					// Base config should now contain the new grove
					// The grove name should be the ecosystem name
					v.Equal("base config has new grove", nil, fs.AssertContains(basePath, "correct-file-eco"))
					v.Equal("base config still has existing grove", nil, fs.AssertContains(basePath, "existing-grove"))

					// Override file should NOT exist (we shouldn't have created it)
					_, err := os.Stat(overridePath)
					v.Equal("override file not created", true, os.IsNotExist(err))
				})
			}),
		},
		true,  // localOnly - TUI tests require tmux
		false, // explicitOnly
	)
}
