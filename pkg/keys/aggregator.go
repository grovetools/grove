package keys

import (
	"encoding/json"
	"os/exec"
	"strings"

	"github.com/grovetools/core/config"
)

// Aggregate collects all keybindings from all known domains.
// It reads TUI keybindings from the generated registry, tmux/nav/nvim from config extensions.
func Aggregate(cfg *config.Config) ([]KeyBinding, error) {
	var allBindings []KeyBinding

	// 1. TUI Bindings from registry (generated at build time)
	allBindings = append(allBindings, getTUIBindingsFromRegistry()...)

	// Unmarshal keys extension from config
	var keysExt KeysExtension
	if cfg != nil {
		_ = cfg.UnmarshalExtension("keys", &keysExt)
	}

	// 2. Tmux Popup Bindings
	for action, keysRaw := range keysExt.Tmux.Popups {
		keys := parseStringOrSlice(keysRaw)
		desc := TmuxCommandMap[action]
		if desc == "" {
			desc = action
		}
		allBindings = append(allBindings, KeyBinding{
			Domain:      DomainTmux,
			Section:     "Popups",
			Action:      action,
			Keys:        keys,
			Description: desc,
			Source:      "grove.toml [keys.tmux.popups]",
		})
	}

	// 3. Nav Pane Bindings
	for _, k := range keysExt.Nav.AvailableKeys {
		allBindings = append(allBindings, KeyBinding{
			Domain:      DomainNav,
			Section:     "Pane Shortcuts",
			Action:      "select_pane",
			Keys:        []string{k},
			Description: "Nav pane selection key",
			Source:      "grove.toml [keys.nav.available_keys]",
		})
	}

	// 4. Nvim Bindings (attempt to fetch via headless nvim)
	nvimBindings := getNvimBindings()
	allBindings = append(allBindings, nvimBindings...)

	return allBindings, nil
}

// getTUIBindingsFromRegistry extracts keybindings from the generated TUI registry.
// This provides detailed per-TUI keybinding information.
func getTUIBindingsFromRegistry() []KeyBinding {
	var bindings []KeyBinding

	for _, tui := range TUIRegistry {
		for _, section := range tui.Sections {
			for _, b := range section.Bindings {
				if !b.Enabled {
					continue
				}
				bindings = append(bindings, KeyBinding{
					Domain:      DomainTUI,
					Section:     section.Name,
					Action:      b.Name,
					Keys:        b.Keys,
					Description: b.Description,
					Source:      tui.Name + " (" + tui.Package + ")",
				})
			}
		}
	}
	return bindings
}

// getNvimBindings attempts to query grove.nvim for its exported keybindings.
// This runs a headless neovim instance to extract the bindings.
func getNvimBindings() []KeyBinding {
	var bindings []KeyBinding

	// Try to query grove.nvim for its keybindings
	// This requires grove.nvim to export a get_keybindings() function
	luaCode := `local ok, g = pcall(require, 'grove-nvim'); if ok and g.get_keybindings then print(vim.json.encode(g.get_keybindings())) else print('{}') end`
	cmd := exec.Command("nvim", "--headless", "-c", "lua "+luaCode, "-c", "q")
	out, err := cmd.Output()
	if err != nil {
		// nvim not available or grove-nvim not installed
		return bindings
	}

	// Parse the JSON output
	output := strings.TrimSpace(string(out))
	// Handle potential nvim startup messages before the JSON
	if idx := strings.Index(output, "{"); idx >= 0 {
		output = output[idx:]
	}

	// Expecting map[string]string (key -> description)
	var result map[string]string
	if err := json.Unmarshal([]byte(output), &result); err == nil {
		for k, desc := range result {
			bindings = append(bindings, KeyBinding{
				Domain:      DomainNvim,
				Section:     "Grove.nvim",
				Action:      desc,
				Keys:        []string{k},
				Description: desc,
				Source:      "grove.nvim plugin",
			})
		}
	}
	return bindings
}

// parseStringOrSlice converts an interface{} to a string slice.
// Handles both string and []interface{} types from YAML/TOML unmarshaling.
func parseStringOrSlice(val interface{}) []string {
	switch v := val.(type) {
	case string:
		return []string{v}
	case []interface{}:
		var res []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				res = append(res, s)
			}
		}
		return res
	case []string:
		return v
	default:
		return nil
	}
}
