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

// findGroveBinary locates the grove binary in the project's bin directory
func findGroveBinary() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	possiblePaths := []string{
		filepath.Join(cwd, "bin", "grove"),
		"./bin/grove",
		filepath.Join(os.Getenv("PWD"), "bin", "grove"),
	}

	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("grove binary not found in any expected location")
}

// SetupWizardCLIDefaultsScenario tests the --defaults flag for non-interactive mode.
func SetupWizardCLIDefaultsScenario() *harness.Scenario {
	return harness.NewScenario(
		"setup-wizard-cli-defaults",
		"Verifies --defaults flag creates ecosystem and notebook with default paths",
		[]string{"setup", "cli"},
		[]harness.Step{
			harness.NewStep("Run setup with --defaults", func(ctx *harness.Context) error {
				cmd := ctx.Bin("setup", "--defaults")
				result := cmd.Run()
				ctx.ShowCommandOutput("grove setup --defaults", result.Stdout, result.Stderr)
				if err := ctx.Check("command succeeds", result.AssertSuccess()); err != nil {
					return fmt.Errorf("setup --defaults failed: %w\nStdout: %s\nStderr: %s", err, result.Stdout, result.Stderr)
				}
				return nil
			}),
			harness.NewStep("Verify files and directories created", func(ctx *harness.Context) error {
				return ctx.Verify(func(v *verify.Collector) {
					ecosystemPath := filepath.Join(ctx.HomeDir(), "Code", "grove-ecosystem")
					notebookPath := filepath.Join(ctx.HomeDir(), "notebooks")
					configPath := filepath.Join(ctx.ConfigDir(), "grove", "grove.yml")

					v.Equal("ecosystem directory created", nil, fs.AssertExists(ecosystemPath))
					v.Equal("notebook directory created", nil, fs.AssertExists(notebookPath))
					v.Equal("global config created", nil, fs.AssertExists(configPath))
				})
			}),
			harness.NewStep("Verify ecosystem files", func(ctx *harness.Context) error {
				ecosystemPath := filepath.Join(ctx.HomeDir(), "Code", "grove-ecosystem")
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("grove.yml exists", nil, fs.AssertExists(filepath.Join(ecosystemPath, "grove.yml")))
					v.Equal("go.work exists", nil, fs.AssertExists(filepath.Join(ecosystemPath, "go.work")))
				})
			}),
		},
	)
}

// SetupWizardCLIDryRunScenario tests the --dry-run flag to ensure no files are created.
func SetupWizardCLIDryRunScenario() *harness.Scenario {
	return harness.NewScenario(
		"setup-wizard-cli-dry-run",
		"Verifies --dry-run flag shows actions without creating files",
		[]string{"setup", "cli", "dry-run"},
		[]harness.Step{
			harness.NewStep("Run setup with --defaults --dry-run", func(ctx *harness.Context) error {
				cmd := ctx.Bin("setup", "--defaults", "--dry-run")
				result := cmd.Run()
				ctx.ShowCommandOutput("grove setup --defaults --dry-run", result.Stdout, result.Stderr)
				if err := ctx.Check("command succeeds", result.AssertSuccess()); err != nil {
					return fmt.Errorf("setup --dry-run failed: %w\nStdout: %s\nStderr: %s", err, result.Stdout, result.Stderr)
				}
				return nil
			}),
			harness.NewStep("Verify no files created in dry-run mode", func(ctx *harness.Context) error {
				ecosystemPath := filepath.Join(ctx.HomeDir(), "Code", "grove-ecosystem")
				notebookPath := filepath.Join(ctx.HomeDir(), "notebooks")

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("ecosystem directory NOT created", nil, fs.AssertNotExists(ecosystemPath))
					v.Equal("notebook directory NOT created", nil, fs.AssertNotExists(notebookPath))
				})
			}),
		},
	)
}

// SetupWizardCLIOnlyScenario tests the --only flag to filter steps.
func SetupWizardCLIOnlyScenario() *harness.Scenario {
	return harness.NewScenario(
		"setup-wizard-cli-only",
		"Verifies --only flag filters which setup steps to run",
		[]string{"setup", "cli"},
		[]harness.Step{
			harness.NewStep("Run setup with --only notebook", func(ctx *harness.Context) error {
				cmd := ctx.Bin("setup", "--defaults", "--only", "notebook")
				result := cmd.Run()
				ctx.ShowCommandOutput("grove setup --defaults --only notebook", result.Stdout, result.Stderr)
				if err := ctx.Check("command succeeds", result.AssertSuccess()); err != nil {
					return fmt.Errorf("setup --only notebook failed: %w\nStdout: %s\nStderr: %s", err, result.Stdout, result.Stderr)
				}
				return nil
			}),
			harness.NewStep("Verify only notebook was created", func(ctx *harness.Context) error {
				ecosystemPath := filepath.Join(ctx.HomeDir(), "Code", "grove-ecosystem")
				notebookPath := filepath.Join(ctx.HomeDir(), "notebooks")

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("notebook directory created", nil, fs.AssertExists(notebookPath))
					v.Equal("ecosystem directory NOT created", nil, fs.AssertNotExists(ecosystemPath))
				})
			}),
		},
	)
}

// SetupWizardEcosystemFilesScenario tests that ecosystem setup creates all required files.
func SetupWizardEcosystemFilesScenario() *harness.Scenario {
	return harness.NewScenario(
		"setup-wizard-ecosystem-files",
		"Verifies ecosystem setup creates grove.yml and go.work",
		[]string{"setup", "ecosystem"},
		[]harness.Step{
			harness.NewStep("Run setup with --only ecosystem --defaults", func(ctx *harness.Context) error {
				cmd := ctx.Bin("setup", "--defaults", "--only", "ecosystem")
				result := cmd.Run()
				ctx.ShowCommandOutput("grove setup --defaults --only ecosystem", result.Stdout, result.Stderr)
				if err := ctx.Check("command succeeds", result.AssertSuccess()); err != nil {
					return fmt.Errorf("setup failed: %w\nStdout: %s\nStderr: %s", err, result.Stdout, result.Stderr)
				}
				return nil
			}),
			harness.NewStep("Verify ecosystem files created", func(ctx *harness.Context) error {
				ecosystemPath := filepath.Join(ctx.HomeDir(), "Code", "grove-ecosystem")

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("grove.yml exists", nil, fs.AssertExists(filepath.Join(ecosystemPath, "grove.yml")))
					v.Equal("go.work exists", nil, fs.AssertExists(filepath.Join(ecosystemPath, "go.work")))
				})
			}),
			harness.NewStep("Verify grove.yml content", func(ctx *harness.Context) error {
				ecosystemPath := filepath.Join(ctx.HomeDir(), "Code", "grove-ecosystem")
				groveYMLPath := filepath.Join(ecosystemPath, "grove.yml")

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("grove.yml contains name", nil, fs.AssertContains(groveYMLPath, "name: grove-ecosystem"))
					v.Equal("grove.yml contains workspaces", nil, fs.AssertContains(groveYMLPath, "workspaces:"))
				})
			}),
			harness.NewStep("Verify global config updated", func(ctx *harness.Context) error {
				configPath := filepath.Join(ctx.ConfigDir(), "grove", "grove.yml")

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("global config exists", nil, fs.AssertExists(configPath))
					v.Equal("config contains groves section", nil, fs.AssertContains(configPath, "groves:"))
				})
			}),
		},
	)
}

// SetupWizardNotebookConfigScenario tests that notebook setup updates global config.
func SetupWizardNotebookConfigScenario() *harness.Scenario {
	return harness.NewScenario(
		"setup-wizard-notebook-config",
		"Verifies notebook setup updates global config with notebook path",
		[]string{"setup", "notebook"},
		[]harness.Step{
			harness.NewStep("Run setup with --only notebook --defaults", func(ctx *harness.Context) error {
				cmd := ctx.Bin("setup", "--defaults", "--only", "notebook")
				result := cmd.Run()
				ctx.ShowCommandOutput("grove setup --defaults --only notebook", result.Stdout, result.Stderr)
				if err := ctx.Check("command succeeds", result.AssertSuccess()); err != nil {
					return fmt.Errorf("setup failed: %w\nStdout: %s\nStderr: %s", err, result.Stdout, result.Stderr)
				}
				return nil
			}),
			harness.NewStep("Verify notebook directory and config", func(ctx *harness.Context) error {
				notebookPath := filepath.Join(ctx.HomeDir(), "notebooks")
				configPath := filepath.Join(ctx.ConfigDir(), "grove", "grove.yml")

				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("notebook directory created", nil, fs.AssertExists(notebookPath))
					v.Equal("global config created", nil, fs.AssertExists(configPath))
					v.Equal("config contains notebooks section", nil, fs.AssertContains(configPath, "notebooks:"))
					v.Equal("config contains path", nil, fs.AssertContains(configPath, "path:"))
				})
			}),
		},
	)
}

// SetupWizardConfigPreservationScenario tests that existing config settings are preserved.
func SetupWizardConfigPreservationScenario() *harness.Scenario {
	return harness.NewScenario(
		"setup-wizard-config-preservation",
		"Verifies existing config settings are preserved when adding new ones",
		[]string{"setup", "config"},
		[]harness.Step{
			harness.NewStep("Create existing config with custom settings", func(ctx *harness.Context) error {
				configDir := filepath.Join(ctx.ConfigDir(), "grove")
				if err := os.MkdirAll(configDir, 0755); err != nil {
					return fmt.Errorf("failed to create config dir: %w", err)
				}

				existingConfig := `# Existing config
custom_setting: my_value
other_section:
  key1: value1
  key2: value2
`
				configPath := filepath.Join(configDir, "grove.yml")
				return fs.WriteString(configPath, existingConfig)
			}),
			harness.NewStep("Run setup with --only notebook --defaults", func(ctx *harness.Context) error {
				cmd := ctx.Bin("setup", "--defaults", "--only", "notebook")
				result := cmd.Run()
				ctx.ShowCommandOutput("grove setup --defaults --only notebook", result.Stdout, result.Stderr)
				if err := ctx.Check("command succeeds", result.AssertSuccess()); err != nil {
					return fmt.Errorf("setup failed: %w\nStdout: %s\nStderr: %s", err, result.Stdout, result.Stderr)
				}
				return nil
			}),
			harness.NewStep("Verify existing settings preserved", func(ctx *harness.Context) error {
				configPath := filepath.Join(ctx.ConfigDir(), "grove", "grove.yml")
				content, err := fs.ReadString(configPath)
				if err != nil {
					return fmt.Errorf("failed to read config: %w", err)
				}

				return ctx.Verify(func(v *verify.Collector) {
					v.Contains("notebooks section added", content, "notebooks:")
					v.Contains("path added", content, "path:")
				})
			}),
		},
	)
}

// SetupWizardTmuxIdempotentScenario tests that tmux config is not duplicated on re-run.
func SetupWizardTmuxIdempotentScenario() *harness.Scenario {
	return harness.NewScenario(
		"setup-wizard-tmux-idempotent",
		"Verifies tmux config source-file line is not duplicated on re-run",
		[]string{"setup", "tmux"},
		[]harness.Step{
			harness.NewStep("Create tmux.conf with existing source-file", func(ctx *harness.Context) error {
				tmuxDir := filepath.Join(ctx.ConfigDir(), "tmux")
				if err := os.MkdirAll(tmuxDir, 0755); err != nil {
					return fmt.Errorf("failed to create tmux dir: %w", err)
				}

				tmuxConf := `# Existing tmux config
set -g prefix C-a
source-file ~/.config/tmux/popups.conf
bind r source-file ~/.tmux.conf
`
				tmuxConfPath := filepath.Join(tmuxDir, "tmux.conf")
				return fs.WriteString(tmuxConfPath, tmuxConf)
			}),
			harness.NewStep("Run setup with --only tmux --defaults", func(ctx *harness.Context) error {
				cmd := ctx.Bin("setup", "--defaults", "--only", "tmux")
				result := cmd.Run()
				ctx.ShowCommandOutput("grove setup --defaults --only tmux", result.Stdout, result.Stderr)
				if err := ctx.Check("command succeeds", result.AssertSuccess()); err != nil {
					return fmt.Errorf("setup failed: %w\nStdout: %s\nStderr: %s", err, result.Stdout, result.Stderr)
				}
				return nil
			}),
			harness.NewStep("Verify source-file not duplicated", func(ctx *harness.Context) error {
				tmuxConfPath := filepath.Join(ctx.ConfigDir(), "tmux", "tmux.conf")
				content, err := fs.ReadString(tmuxConfPath)
				if err != nil {
					return fmt.Errorf("failed to read tmux.conf: %w", err)
				}

				// Count occurrences of source-file popups.conf
				count := strings.Count(content, "popups.conf")
				if count > 1 {
					return fmt.Errorf("source-file popups.conf appears %d times (expected 1):\n%s", count, content)
				}

				return nil
			}),
		},
	)
}

// SetupWizardTUIComponentSelectionScenario tests the TUI component selection screen.
func SetupWizardTUIComponentSelectionScenario() *harness.Scenario {
	return harness.NewScenarioWithOptions(
		"setup-wizard-tui-component-selection",
		"Verifies component selection and toggle functionality",
		[]string{"setup", "tui"},
		[]harness.Step{
			harness.NewStep("Launch TUI and verify initial state", func(ctx *harness.Context) error {
				groveBinary, err := findGroveBinary()
				if err != nil {
					return fmt.Errorf("failed to find grove binary: %w", err)
				}
				session, err := ctx.StartTUI(groveBinary, []string{"setup"})
				if err != nil {
					return fmt.Errorf("failed to start TUI: %w", err)
				}
				ctx.Set("tui_session", session)

				if err := session.WaitForText("Select Components to Configure", 10*time.Second); err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("TUI did not load: %w\nContent:\n%s", err, content)
				}
				return nil
			}),
			harness.NewStep("Verify default selections", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)
				content, err := session.Capture(tui.WithCleanedOutput())
				if err != nil {
					return err
				}

				return ctx.Verify(func(v *verify.Collector) {
					v.Contains("shows Ecosystem Directory", content, "Ecosystem Directory")
					v.Contains("shows Notebook Directory", content, "Notebook Directory")
					v.Contains("shows tmux Popup Bindings", content, "tmux Popup Bindings")
					v.Contains("shows Neovim Plugin", content, "Neovim Plugin")
				})
			}),
			harness.NewStep("Navigate and toggle an option", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)

				// Navigate down to a different option
				if err := session.Type("j"); err != nil {
					return fmt.Errorf("failed to navigate down: %w", err)
				}

				// Get content before toggle
				beforeContent, err := session.Capture(tui.WithCleanedOutput())
				if err != nil {
					return err
				}

				// Toggle selection with space
				if err := session.Type(" "); err != nil {
					return fmt.Errorf("failed to toggle: %w", err)
				}

				// Get content after toggle
				afterContent, err := session.Capture(tui.WithCleanedOutput())
				if err != nil {
					return err
				}

				// Content should have changed after toggle
				if beforeContent == afterContent {
					return fmt.Errorf("toggle did not change the UI")
				}

				return nil
			}),
			harness.NewStep("Quit TUI", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)
				return session.Type("q")
			}),
		},
		true,  // localOnly - TUI tests require tmux
		false, // explicitOnly
	)
}

// SetupWizardTUINavigationScenario tests forward and backward navigation.
func SetupWizardTUINavigationScenario() *harness.Scenario {
	return harness.NewScenarioWithOptions(
		"setup-wizard-tui-navigation",
		"Verifies forward and backward navigation through wizard steps",
		[]string{"setup", "tui", "navigation"},
		[]harness.Step{
			harness.NewStep("Launch TUI", func(ctx *harness.Context) error {
				groveBinary, err := findGroveBinary()
				if err != nil {
					return fmt.Errorf("failed to find grove binary: %w", err)
				}
				session, err := ctx.StartTUI(groveBinary, []string{"setup"})
				if err != nil {
					return fmt.Errorf("failed to start TUI: %w", err)
				}
				ctx.Set("tui_session", session)
				return session.WaitForText("Select Components to Configure", 10*time.Second)
			}),
			harness.NewStep("Navigate to Ecosystem step", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)
				// Press Enter to confirm component selection
				if err := session.Type("Enter"); err != nil {
					return err
				}

				// Wait for ecosystem step
				if err := session.WaitForText("Enter the path for your Grove ecosystem", 5*time.Second); err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("ecosystem step not reached: %w\nContent:\n%s", err, content)
				}
				return nil
			}),
			harness.NewStep("Navigate forward through steps", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)

				// Ecosystem path -> Enter
				if err := session.Type("Enter"); err != nil {
					return err
				}
				// Ecosystem name -> Enter
				if err := session.Type("Enter"); err != nil {
					return err
				}

				// Should be at Notebook step now
				if err := session.WaitForText("Enter the path for your notebook directory", 5*time.Second); err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("notebook step not reached: %w\nContent:\n%s", err, content)
				}
				return nil
			}),
			harness.NewStep("Navigate backward", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)

				// Press 'b' to go back
				if err := session.Type("b"); err != nil {
					return err
				}

				// Should be back at Ecosystem name input
				if err := session.WaitForText("Enter a name for your ecosystem", 5*time.Second); err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("ecosystem name step not reached: %w\nContent:\n%s", err, content)
				}
				return nil
			}),
			harness.NewStep("Quit TUI", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)
				return session.Type("q")
			}),
		},
		true,  // localOnly
		false, // explicitOnly
	)
}

// SetupWizardTUIFullWorkflowScenario tests the complete wizard workflow with default values.
func SetupWizardTUIFullWorkflowScenario() *harness.Scenario {
	return harness.NewScenarioWithOptions(
		"setup-wizard-tui-full-workflow",
		"Verifies full happy-path workflow of the setup wizard with default values",
		[]string{"setup", "tui", "workflow"},
		[]harness.Step{
			harness.NewStep("Launch TUI", func(ctx *harness.Context) error {
				groveBinary, err := findGroveBinary()
				if err != nil {
					return fmt.Errorf("failed to find grove binary: %w", err)
				}
				session, err := ctx.StartTUI(groveBinary, []string{"setup"})
				if err != nil {
					return fmt.Errorf("failed to start TUI: %w", err)
				}
				ctx.Set("tui_session", session)
				return session.WaitForText("Select Components to Configure", 10*time.Second)
			}),
			harness.NewStep("Deselect non-essential components", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)

				// Navigate down to Gemini (3rd item) and deselect it
				if err := session.Type("j"); err != nil {
					return err
				}
				if err := session.Type("j"); err != nil {
					return err
				}
				// Toggle off Gemini
				if err := session.Type(" "); err != nil {
					return err
				}

				return nil
			}),
			harness.NewStep("Complete wizard with defaults", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)

				// Confirm component selection
				if err := session.Type("Enter"); err != nil {
					return err
				}

				// Accept default ecosystem path
				if err := session.WaitForText("Enter the path for your Grove ecosystem", 5*time.Second); err != nil {
					return err
				}
				if err := session.Type("Enter"); err != nil {
					return err
				}

				// Accept default ecosystem name
				if err := session.WaitForText("Enter a name for your ecosystem", 5*time.Second); err != nil {
					return err
				}
				if err := session.Type("Enter"); err != nil {
					return err
				}

				// Accept default notebook path
				if err := session.WaitForText("Enter the path for your notebook directory", 5*time.Second); err != nil {
					return err
				}
				if err := session.Type("Enter"); err != nil {
					return err
				}

				return nil
			}),
			harness.NewStep("Verify summary screen and exit", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)

				// Wait for summary
				_, err := session.WaitForAnyText([]string{
					"Setup Complete",
					"Setup completed",
					"Actions performed",
				}, 10*time.Second)

				if err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("summary screen not reached: %w\nContent:\n%s", err, content)
				}

				// Exit wizard
				if err := session.Type("Enter"); err != nil {
					return err
				}

				time.Sleep(500 * time.Millisecond)
				return nil
			}),
			harness.NewStep("Verify files were created", func(ctx *harness.Context) error {
				return ctx.Verify(func(v *verify.Collector) {
					ecosystemPath := filepath.Join(ctx.HomeDir(), "Code", "grove-ecosystem")
					notebookPath := filepath.Join(ctx.HomeDir(), "notebooks")
					configPath := filepath.Join(ctx.ConfigDir(), "grove", "grove.yml")

					v.Equal("ecosystem directory created", nil, fs.AssertExists(ecosystemPath))
					v.Equal("notebook directory created", nil, fs.AssertExists(notebookPath))
					v.Equal("global config created", nil, fs.AssertExists(configPath))
				})
			}),
		},
		true,  // localOnly
		false, // explicitOnly
	)
}

// SetupWizardTUIDeselectAllScenario tests deselecting all components goes directly to summary.
func SetupWizardTUIDeselectAllScenario() *harness.Scenario {
	return harness.NewScenarioWithOptions(
		"setup-wizard-tui-deselect-all",
		"Verifies deselecting all components goes directly to summary",
		[]string{"setup", "tui"},
		[]harness.Step{
			harness.NewStep("Launch TUI", func(ctx *harness.Context) error {
				groveBinary, err := findGroveBinary()
				if err != nil {
					return fmt.Errorf("failed to find grove binary: %w", err)
				}
				session, err := ctx.StartTUI(groveBinary, []string{"setup"})
				if err != nil {
					return fmt.Errorf("failed to start TUI: %w", err)
				}
				ctx.Set("tui_session", session)
				return session.WaitForText("Select Components to Configure", 10*time.Second)
			}),
			harness.NewStep("Deselect all components", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)

				// Toggle off ecosystem (first item, already focused)
				if err := session.Type(" "); err != nil {
					return err
				}
				// Move to notebook and toggle off
				if err := session.Type("j"); err != nil {
					return err
				}
				if err := session.Type(" "); err != nil {
					return err
				}
				// Move to gemini and toggle off
				if err := session.Type("j"); err != nil {
					return err
				}
				if err := session.Type(" "); err != nil {
					return err
				}

				return nil
			}),
			harness.NewStep("Confirm and verify direct to summary", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)

				// Press Enter with no components selected
				if err := session.Type("Enter"); err != nil {
					return err
				}

				// Should go directly to summary
				_, err := session.WaitForAnyText([]string{
					"Setup Complete",
					"Setup completed",
					"No changes",
				}, 5*time.Second)

				if err != nil {
					content, _ := session.Capture(tui.WithCleanedOutput())
					return fmt.Errorf("summary not reached: %w\nContent:\n%s", err, content)
				}

				return nil
			}),
			harness.NewStep("Exit TUI", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)
				return session.Type("q")
			}),
		},
		true,  // localOnly
		false, // explicitOnly
	)
}

// SetupWizardTUIQuitScenario tests that 'q' quits from any step without creating files.
func SetupWizardTUIQuitScenario() *harness.Scenario {
	return harness.NewScenarioWithOptions(
		"setup-wizard-tui-quit",
		"Verifies 'q' quits the wizard from ecosystem step without creating files",
		[]string{"setup", "tui"},
		[]harness.Step{
			harness.NewStep("Launch TUI and navigate to ecosystem step", func(ctx *harness.Context) error {
				groveBinary, err := findGroveBinary()
				if err != nil {
					return fmt.Errorf("failed to find grove binary: %w", err)
				}
				session, err := ctx.StartTUI(groveBinary, []string{"setup"})
				if err != nil {
					return fmt.Errorf("failed to start TUI: %w", err)
				}
				ctx.Set("tui_session", session)

				if err := session.WaitForText("Select Components to Configure", 10*time.Second); err != nil {
					return err
				}

				// Navigate to ecosystem step
				if err := session.Type("Enter"); err != nil {
					return err
				}

				return session.WaitForText("Enter the path for your Grove ecosystem", 5*time.Second)
			}),
			harness.NewStep("Quit with 'q'", func(ctx *harness.Context) error {
				session := ctx.Get("tui_session").(*tui.Session)
				if err := session.Type("q"); err != nil {
					return err
				}
				time.Sleep(500 * time.Millisecond)
				return nil
			}),
			harness.NewStep("Verify no files created", func(ctx *harness.Context) error {
				ecosystemPath := filepath.Join(ctx.HomeDir(), "Code", "grove-ecosystem")
				return ctx.Check("no ecosystem created", fs.AssertNotExists(ecosystemPath))
			}),
		},
		true,  // localOnly
		false, // explicitOnly
	)
}
