package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/theme"
	"github.com/grovetools/grove/pkg/keys"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// newKeysPopupsCmd creates the 'grove keys popups' command group.
func newKeysPopupsCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("popups", "Manage tmux popup bindings")

	cmd.Long = `Manage tmux popup key bindings defined in grove.toml.

Commands allow listing, adding, removing, and modifying popup bindings
that are used by the 'grove keys generate tmux' command.`

	cmd.AddCommand(newKeysPopupsListCmd())
	cmd.AddCommand(newKeysPopupsAddCmd())
	cmd.AddCommand(newKeysPopupsRemoveCmd())
	cmd.AddCommand(newKeysPopupsSetCmd())

	return cmd
}

// newKeysPopupsListCmd creates the 'grove keys popups list' command.
func newKeysPopupsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List current popup bindings",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeysPopupsList()
		},
	}
	return cmd
}

func runKeysPopupsList() error {
	cfg, err := config.LoadDefault()
	if err != nil {
		cfg = &config.Config{}
	}

	t := theme.DefaultTheme

	var keysExt keys.KeysExtension
	if cfg != nil {
		_ = cfg.UnmarshalExtension("keys", &keysExt)
	}

	if len(keysExt.Tmux.Popups) == 0 {
		fmt.Println(t.Muted.Render("No popup bindings defined."))
		fmt.Println(t.Muted.Render("Use 'grove keys popups add' to create one."))
		return nil
	}

	fmt.Println(t.Header.Render(" Tmux Popup Bindings "))
	if keysExt.Tmux.Prefix != "" {
		fmt.Printf("  %s %s\n", t.Muted.Render("Active Prefix:"), t.Highlight.Render(keysExt.Tmux.Prefix))
	}
	fmt.Println()

	// Sort for consistent output
	var actions []string
	for action := range keysExt.Tmux.Popups {
		actions = append(actions, action)
	}
	sort.Strings(actions)

	for _, action := range actions {
		popup := keysExt.Tmux.Popups[action]
		keySlice := parseInterfaceToStringSlice(popup.Key)
		keyStr := strings.Join(keySlice, ", ")

		style := popup.Style
		if style == "" {
			style = "popup"
		}

		cmdStr := popup.Command
		if cmdStr == "" {
			cmdStr = keys.TmuxCommandMap[action]
			if cmdStr == "" {
				cmdStr = action
			}
		}

		fmt.Printf("  %s\n", t.Bold.Render(action))
		fmt.Printf("    Key:     %s\n", t.Highlight.Render(keyStr))
		fmt.Printf("    Command: %s\n", cmdStr)
		fmt.Printf("    Style:   %s\n", style)
		fmt.Println()
	}

	return nil
}

// newKeysPopupsAddCmd creates the 'grove keys popups add' command.
func newKeysPopupsAddCmd() *cobra.Command {
	var keyBinding string
	var command string
	var style string

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a new popup binding",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeysPopupsAdd(args[0], keyBinding, command, style)
		},
	}

	cmd.Flags().StringVarP(&keyBinding, "key", "k", "", "Key binding (e.g., 'C-p', 'M-f')")
	cmd.Flags().StringVar(&command, "command", "", "Command to run")
	cmd.Flags().StringVarP(&style, "style", "s", "popup", "Style: popup, run-shell, or window")
	_ = cmd.MarkFlagRequired("key")
	_ = cmd.MarkFlagRequired("command")

	return cmd
}

func runKeysPopupsAdd(name, keyBinding, command, style string) error {
	t := theme.DefaultTheme

	// Validate style
	if style != "popup" && style != "run-shell" && style != "window" {
		return fmt.Errorf("invalid style %q: must be popup, run-shell, or window", style)
	}

	// Update config
	if err := updatePopupConfig(name, keyBinding, command, style, false); err != nil {
		return err
	}

	// Regenerate tmux config
	if err := regenerateTmuxConfig(); err != nil {
		return fmt.Errorf("failed to regenerate tmux config: %w", err)
	}

	fmt.Printf("%s Added popup binding: %s\n", t.Success.Render(theme.IconSuccess), name)
	return nil
}

// newKeysPopupsRemoveCmd creates the 'grove keys popups remove' command.
func newKeysPopupsRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a popup binding",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeysPopupsRemove(args[0])
		},
	}
	return cmd
}

func runKeysPopupsRemove(name string) error {
	t := theme.DefaultTheme

	if err := removePopupConfig(name); err != nil {
		return err
	}

	// Regenerate tmux config
	if err := regenerateTmuxConfig(); err != nil {
		return fmt.Errorf("failed to regenerate tmux config: %w", err)
	}

	fmt.Printf("%s Removed popup binding: %s\n", t.Success.Render(theme.IconSuccess), name)
	return nil
}

// newKeysPopupsSetCmd creates the 'grove keys popups set' command.
func newKeysPopupsSetCmd() *cobra.Command {
	var keyBinding string
	var command string
	var style string

	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Modify an existing popup binding",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeysPopupsSet(args[0], keyBinding, command, style)
		},
	}

	cmd.Flags().StringVarP(&keyBinding, "key", "k", "", "Key binding (e.g., 'C-p', 'M-f')")
	cmd.Flags().StringVar(&command, "command", "", "Command to run")
	cmd.Flags().StringVarP(&style, "style", "s", "", "Style: popup, run-shell, or window")

	return cmd
}

func runKeysPopupsSet(name, keyBinding, command, style string) error {
	t := theme.DefaultTheme

	if keyBinding == "" && command == "" && style == "" {
		return fmt.Errorf("at least one of --key, --command, or --style must be specified")
	}

	// Validate style if provided
	if style != "" && style != "popup" && style != "run-shell" && style != "window" {
		return fmt.Errorf("invalid style %q: must be popup, run-shell, or window", style)
	}

	// Update config (merge mode)
	if err := updatePopupConfig(name, keyBinding, command, style, true); err != nil {
		return err
	}

	// Regenerate tmux config
	if err := regenerateTmuxConfig(); err != nil {
		return fmt.Errorf("failed to regenerate tmux config: %w", err)
	}

	fmt.Printf("%s Updated popup binding: %s\n", t.Success.Render(theme.IconSuccess), name)
	return nil
}

// updatePopupConfig updates the grove.toml with a new or modified popup binding.
func updatePopupConfig(name, keyBinding, command, style string, merge bool) error {
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

	// Navigate to keys.tmux.popups, creating path if needed
	keysSection, ok := fullConfig["keys"].(map[string]interface{})
	if !ok {
		keysSection = make(map[string]interface{})
		fullConfig["keys"] = keysSection
	}

	tmuxSection, ok := keysSection["tmux"].(map[string]interface{})
	if !ok {
		tmuxSection = make(map[string]interface{})
		keysSection["tmux"] = tmuxSection
	}

	popupsSection, ok := tmuxSection["popups"].(map[string]interface{})
	if !ok {
		popupsSection = make(map[string]interface{})
		tmuxSection["popups"] = popupsSection
	}

	// Get existing entry if merging
	var existingEntry map[string]interface{}
	if merge {
		if existing, ok := popupsSection[name].(map[string]interface{}); ok {
			existingEntry = existing
		} else {
			return fmt.Errorf("popup %q does not exist; use 'add' to create it", name)
		}
	} else {
		existingEntry = make(map[string]interface{})
	}

	// Update fields
	if keyBinding != "" {
		existingEntry["key"] = keyBinding
	}
	if command != "" {
		existingEntry["command"] = command
	}
	if style != "" {
		existingEntry["style"] = style
	}

	popupsSection[name] = existingEntry

	// Write back
	return writeConfig(configPath, fullConfig, isTOML)
}

// removePopupConfig removes a popup binding from grove.toml.
func removePopupConfig(name string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Find config path
	configPath, err := config.FindConfigFile(filepath.Join(home, ".config", "grove"))
	if err != nil {
		return fmt.Errorf("config file not found: %w", err)
	}

	isTOML := strings.HasSuffix(configPath, ".toml")

	// Read existing config
	var fullConfig map[string]interface{}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}
	if isTOML {
		if _, err := toml.Decode(string(data), &fullConfig); err != nil {
			return fmt.Errorf("failed to parse config file: %w", err)
		}
	} else {
		if err := yaml.Unmarshal(data, &fullConfig); err != nil {
			return fmt.Errorf("failed to parse config file: %w", err)
		}
	}

	// Navigate to keys.tmux.popups
	keysSection, ok := fullConfig["keys"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("no [keys] section in config")
	}

	tmuxSection, ok := keysSection["tmux"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("no [keys.tmux] section in config")
	}

	popupsSection, ok := tmuxSection["popups"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("no [keys.tmux.popups] section in config")
	}

	if _, exists := popupsSection[name]; !exists {
		return fmt.Errorf("popup %q does not exist", name)
	}

	delete(popupsSection, name)

	// Write back
	return writeConfig(configPath, fullConfig, isTOML)
}

// writeConfig writes the config map back to the file.
func writeConfig(path string, cfg map[string]interface{}, isTOML bool) error {
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	var data []byte
	var err error

	if isTOML {
		var buf strings.Builder
		enc := toml.NewEncoder(&buf)
		if err := enc.Encode(cfg); err != nil {
			return fmt.Errorf("failed to marshal config: %w", err)
		}
		data = []byte(buf.String())
	} else {
		data, err = yaml.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("failed to marshal config: %w", err)
		}
	}

	return os.WriteFile(path, data, 0644)
}

// regenerateTmuxConfig runs 'grove keys generate tmux' to update the cache.
func regenerateTmuxConfig() error {
	return exec.Command("grove", "keys", "generate", "tmux").Run()
}
