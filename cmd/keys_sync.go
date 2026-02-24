package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/lipgloss"
	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/keybind"
	"github.com/grovetools/core/tui/theme"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// newKeysSyncCmd creates the 'grove keys sync' command group.
func newKeysSyncCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("sync", "Sync external keybindings with Grove")

	cmd.Long = `Detect and import keybindings from external sources (.tmux.conf, shell configs).

Subcommands:
  detect    List bindings defined in external files not managed by Grove
  status    Show summary of managed vs external bindings
  import    Interactive wizard to import external bindings into grove.toml

External bindings are those defined in user config files like .tmux.conf or config.fish
that are not managed by Grove through grove.toml.`

	cmd.AddCommand(newKeysSyncDetectCmd())
	cmd.AddCommand(newKeysSyncStatusCmd())
	cmd.AddCommand(newKeysSyncImportCmd())

	return cmd
}

// newKeysSyncDetectCmd creates the 'grove keys sync detect' command.
func newKeysSyncDetectCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("detect", "List external bindings not managed by Grove")

	cmd.Long = `Detect keybindings defined in external config files that are not managed by Grove.

This command scans:
  - .tmux.conf for bind-key commands
  - Shell configs (config.fish, .bashrc, .zshrc) for bind commands

Detected bindings show their source file and can be imported with 'grove keys sync import'.`

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runKeysSyncDetect()
	}

	return cmd
}

func runKeysSyncDetect() error {
	ctx := context.Background()
	t := theme.DefaultTheme

	// Load config and build stack
	cfg, _ := config.LoadDefault()
	collectors := buildKeybindCollectors(ctx, cfg)
	stack, err := keybind.BuildStack(ctx, collectors...)
	if err != nil {
		fmt.Println(t.Warning.Render("  Warning: Some collectors failed"))
	}

	// Filter for external user bindings
	var external []keybind.Binding
	for _, b := range stack.AllBindings() {
		if b.Provenance == keybind.ProvenanceUserConfig {
			external = append(external, b)
		}
	}

	if len(external) == 0 {
		fmt.Println(t.Success.Render(theme.IconSuccess + " No unmanaged external bindings detected."))
		fmt.Println()
		fmt.Println(t.Muted.Render("  All detected bindings are either Grove-managed or defaults."))
		return nil
	}

	fmt.Println(t.Header.Render(fmt.Sprintf("  Detected %d external bindings:", len(external))))
	fmt.Println(t.Muted.Render("  " + strings.Repeat("─", 45)))
	fmt.Println()

	// Group by source
	grouped := make(map[string][]keybind.Binding)
	for _, b := range external {
		grouped[b.Source] = append(grouped[b.Source], b)
	}

	// Sort sources for consistent output
	var sources []string
	for source := range grouped {
		sources = append(sources, source)
	}
	sort.Strings(sources)

	for _, source := range sources {
		bindings := grouped[source]
		fmt.Println(t.Bold.Render(fmt.Sprintf("  %s:", source)))

		// Sort bindings by key
		sort.Slice(bindings, func(i, j int) bool {
			return bindings[i].Key < bindings[j].Key
		})

		for _, b := range bindings {
			configInfo := ""
			if b.ConfigFile != "" {
				configInfo = t.Muted.Render(fmt.Sprintf(" (%s)", filepath.Base(b.ConfigFile)))
			}
			fmt.Printf("    %-10s %s %s%s\n",
				t.Highlight.Render(b.Key),
				t.Muted.Render("→"),
				b.Action,
				configInfo)
		}
		fmt.Println()
	}

	fmt.Println(t.Muted.Render("  Run 'grove keys sync import' to manage these with Grove."))

	return nil
}

// newKeysSyncStatusCmd creates the 'grove keys sync status' command.
func newKeysSyncStatusCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("status", "Show summary of managed vs external bindings")

	cmd.Long = `Show a high-level summary of keybinding management status.

Displays counts of:
  - Grove-managed bindings (from grove.toml)
  - External user bindings (from .tmux.conf, shell configs)
  - Default bindings (readline defaults, tmux defaults)`

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runKeysSyncStatus()
	}

	return cmd
}

func runKeysSyncStatus() error {
	ctx := context.Background()
	t := theme.DefaultTheme

	// Load config and build stack
	cfg, _ := config.LoadDefault()
	collectors := buildKeybindCollectors(ctx, cfg)
	stack, err := keybind.BuildStack(ctx, collectors...)
	if err != nil {
		fmt.Println(t.Warning.Render("  Warning: Some collectors failed"))
	}

	// Count by provenance
	stats := struct {
		Managed  int
		External int
		Default  int
		Detected int
	}{}

	// Also track by layer
	layerStats := make(map[keybind.Layer]struct {
		Managed  int
		External int
		Default  int
	})

	for _, b := range stack.AllBindings() {
		switch b.Provenance {
		case keybind.ProvenanceGrove:
			stats.Managed++
			ls := layerStats[b.Layer]
			ls.Managed++
			layerStats[b.Layer] = ls
		case keybind.ProvenanceUserConfig:
			stats.External++
			ls := layerStats[b.Layer]
			ls.External++
			layerStats[b.Layer] = ls
		case keybind.ProvenanceDefault:
			stats.Default++
			ls := layerStats[b.Layer]
			ls.Default++
			layerStats[b.Layer] = ls
		default:
			stats.Detected++
		}
	}

	total := stats.Managed + stats.External + stats.Default + stats.Detected

	fmt.Println(t.Header.Render(theme.IconGear + " Keybinding Sync Status"))
	fmt.Println(t.Muted.Render("  " + strings.Repeat("─", 45)))
	fmt.Println()

	// Summary with visual bars
	maxWidth := 30
	managedWidth := 0
	externalWidth := 0
	defaultWidth := 0
	if total > 0 {
		managedWidth = (stats.Managed * maxWidth) / total
		externalWidth = (stats.External * maxWidth) / total
		defaultWidth = (stats.Default * maxWidth) / total
	}

	fmt.Printf("  %-12s %s %d\n",
		t.Success.Render("Grove:"),
		renderBar(managedWidth, maxWidth, t.Success),
		stats.Managed)

	fmt.Printf("  %-12s %s %d\n",
		t.Warning.Render("External:"),
		renderBar(externalWidth, maxWidth, t.Warning),
		stats.External)

	fmt.Printf("  %-12s %s %d\n",
		t.Muted.Render("Default:"),
		renderBar(defaultWidth, maxWidth, t.Muted),
		stats.Default)

	fmt.Println()
	fmt.Printf("  %-12s %d\n",
		t.Bold.Render("Total:"),
		total)
	fmt.Println()

	// Layer breakdown
	fmt.Println(t.Bold.Render("  By Layer:"))

	layers := []keybind.Layer{
		keybind.LayerShell,
		keybind.LayerTmuxRoot,
		keybind.LayerTmuxPrefix,
		keybind.LayerTmuxCustomTable,
	}

	for _, layer := range layers {
		ls := layerStats[layer]
		layerTotal := ls.Managed + ls.External + ls.Default
		if layerTotal > 0 {
			fmt.Printf("    %-20s grove:%d  external:%d  default:%d\n",
				t.Muted.Render(layer.String()+":"),
				ls.Managed, ls.External, ls.Default)
		}
	}
	fmt.Println()

	// Status message
	if stats.External > 0 {
		fmt.Println(t.Warning.Render(fmt.Sprintf("  %d external bindings not managed by Grove.", stats.External)))
		fmt.Println(t.Muted.Render("  Run 'grove keys sync detect' to see details."))
		fmt.Println(t.Muted.Render("  Run 'grove keys sync import' to import them."))
	} else {
		fmt.Println(t.Success.Render(theme.IconSuccess + " All user bindings are managed by Grove."))
	}

	return nil
}

// renderBar creates a visual progress bar.
func renderBar(filled, total int, style lipgloss.Style) string {
	bar := strings.Repeat("=", filled) + strings.Repeat(" ", total-filled)
	return "[" + style.Render(bar) + "]"
}

// newKeysSyncImportCmd creates the 'grove keys sync import' command.
func newKeysSyncImportCmd() *cobra.Command {
	var all bool

	cmd := cli.NewStandardCommand("import", "Import external bindings into grove.toml")

	cmd.Long = `Interactively import external keybindings into grove.toml.

This command scans for external bindings (not managed by Grove) and offers
to import them. Once imported, Grove becomes the source of truth for these bindings.

Options:
  --all    Import all detected external bindings without prompting

After importing:
  - Tmux bindings are added to [keys.tmux.bindings]
  - Shell bindings are added to [keys.shell.bindings]
  - Run 'grove keys generate' to apply changes`

	cmd.Flags().BoolVarP(&all, "all", "a", false, "Import all detected external bindings")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runKeysSyncImport(all)
	}

	return cmd
}

func runKeysSyncImport(importAll bool) error {
	ctx := context.Background()
	t := theme.DefaultTheme

	// Load config and build stack
	cfg, _ := config.LoadDefault()
	collectors := buildKeybindCollectors(ctx, cfg)
	stack, err := keybind.BuildStack(ctx, collectors...)
	if err != nil {
		fmt.Println(t.Warning.Render("  Warning: Some collectors failed"))
	}

	// Filter for external user bindings
	var external []keybind.Binding
	for _, b := range stack.AllBindings() {
		if b.Provenance == keybind.ProvenanceUserConfig {
			external = append(external, b)
		}
	}

	if len(external) == 0 {
		fmt.Println(t.Success.Render(theme.IconSuccess + " No external bindings to import."))
		return nil
	}

	fmt.Println(t.Header.Render(fmt.Sprintf("  Found %d external bindings:", len(external))))
	fmt.Println(t.Muted.Render("  " + strings.Repeat("─", 45)))
	fmt.Println()

	// Collect bindings to import
	var toImport []keybind.Binding

	if importAll {
		toImport = external
		fmt.Println(t.Info.Render(fmt.Sprintf("  Importing all %d bindings...", len(external))))
		fmt.Println()
	} else {
		// Interactive prompt for each binding
		reader := bufio.NewReader(os.Stdin)

		for _, b := range external {
			fmt.Printf("  Import %s %s %s?\n",
				t.Highlight.Render(b.Key),
				t.Muted.Render("→"),
				b.Action)
			fmt.Printf("    Source: %s", t.Muted.Render(b.Source))
			if b.ConfigFile != "" {
				fmt.Printf(" (%s)", t.Muted.Render(filepath.Base(b.ConfigFile)))
			}
			fmt.Println()
			fmt.Printf("  [y]es / [n]o / [a]ll / [q]uit: ")

			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(strings.ToLower(input))

			switch input {
			case "y", "yes":
				toImport = append(toImport, b)
			case "n", "no":
				// Skip this one
			case "a", "all":
				toImport = append(toImport, b)
				// Import remaining without prompting
				for i := len(toImport); i < len(external); i++ {
					toImport = append(toImport, external[i])
				}
				break
			case "q", "quit":
				fmt.Println()
				fmt.Println(t.Muted.Render("  Import cancelled."))
				return nil
			default:
				// Treat unknown input as 'no'
			}
			fmt.Println()
		}
	}

	if len(toImport) == 0 {
		fmt.Println(t.Muted.Render("  No bindings selected for import."))
		return nil
	}

	// Update config file
	if err := importBindingsToConfig(toImport); err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}

	fmt.Println(t.Success.Render(fmt.Sprintf(theme.IconSuccess+" Imported %d bindings to grove.toml", len(toImport))))
	fmt.Println()
	fmt.Println(t.Muted.Render("  Next steps:"))
	fmt.Println(t.Muted.Render("    1. Review the imported bindings in grove.toml"))
	fmt.Println(t.Muted.Render("    2. Run 'grove keys generate' to apply changes"))
	fmt.Println(t.Muted.Render("    3. Optionally remove duplicates from original config files"))

	return nil
}

// importBindingsToConfig adds bindings to grove.toml based on their source.
func importBindingsToConfig(bindings []keybind.Binding) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Find config path
	configPath, err := config.FindConfigFile(filepath.Join(home, ".config", "grove"))
	if err != nil {
		configPath = filepath.Join(home, ".config", "grove", "grove.toml")
	}

	isTOML := strings.HasSuffix(configPath, ".toml")

	// Read existing config
	var fullConfig map[string]interface{}
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read config file: %w", err)
	}
	if len(data) > 0 {
		if isTOML {
			if _, err := toml.Decode(string(data), &fullConfig); err != nil {
				return fmt.Errorf("failed to parse config file: %w", err)
			}
		} else {
			if err := yaml.Unmarshal(data, &fullConfig); err != nil {
				return fmt.Errorf("failed to parse config file: %w", err)
			}
		}
	} else {
		fullConfig = make(map[string]interface{})
	}

	// Navigate to keys section
	keysSection, ok := fullConfig["keys"].(map[string]interface{})
	if !ok {
		keysSection = make(map[string]interface{})
		fullConfig["keys"] = keysSection
	}

	// Process bindings by category
	for _, b := range bindings {
		section, key, value := mapBindingToConfig(b)
		if section == "" {
			continue
		}

		// Navigate to the appropriate section
		parts := strings.Split(section, ".")
		current := keysSection

		// Navigate/create nested sections (e.g., "tmux.bindings" -> keys.tmux.bindings)
		for i, part := range parts[:len(parts)-1] {
			next, ok := current[part].(map[string]interface{})
			if !ok {
				next = make(map[string]interface{})
				current[part] = next
			}
			current = next
			_ = i
		}

		// Get or create the final map
		finalKey := parts[len(parts)-1]
		finalMap, ok := current[finalKey].(map[string]interface{})
		if !ok {
			finalMap = make(map[string]interface{})
			current[finalKey] = finalMap
		}

		// Add the binding
		finalMap[key] = value
	}

	// Write back
	return writeConfig(configPath, fullConfig, isTOML)
}

// mapBindingToConfig determines where a binding should go in grove.toml.
func mapBindingToConfig(b keybind.Binding) (section string, key string, value string) {
	// Determine section based on source/layer
	switch b.Layer {
	case keybind.LayerTmuxRoot, keybind.LayerTmuxPrefix, keybind.LayerTmuxCustomTable:
		if strings.Contains(b.Source, "tmux") {
			return "tmux.bindings", b.Key, b.Action
		}
	case keybind.LayerShell:
		if strings.Contains(b.Source, "fish") || strings.Contains(b.Source, "bash") || strings.Contains(b.Source, "zsh") {
			return "shell.bindings", b.Key, b.Action
		}
	}

	// Fallback based on source name
	if strings.Contains(b.Source, "tmux") {
		return "tmux.bindings", b.Key, b.Action
	}
	if strings.Contains(b.Source, "fish") || strings.Contains(b.Source, "bash") || strings.Contains(b.Source, "zsh") || strings.Contains(b.Source, "shell") {
		return "shell.bindings", b.Key, b.Action
	}

	return "", "", ""
}
