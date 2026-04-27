// Package keys provides unified key management across the Grove ecosystem.
// It aggregates keybindings from TUIs, tmux, nav, and neovim,
// and provides clash detection and config generation capabilities.
package keys

// KeyDomain represents the ecosystem domain for a keybinding.
type KeyDomain string

const (
	DomainTUI  KeyDomain = "tui"
	DomainTmux KeyDomain = "tmux"
	DomainNav  KeyDomain = "nav"
	DomainNvim KeyDomain = "nvim"
)

// String returns the string representation of the domain.
func (d KeyDomain) String() string {
	return string(d)
}

// KeyBinding represents a single key mapping within a domain.
type KeyBinding struct {
	Domain      KeyDomain
	Section     string   // e.g., "Navigation", "Actions", "Popups"
	Action      string   // e.g., "up", "flow_status", "create_note"
	Keys        []string // e.g., ["k", "up"], ["C-p"]
	Description string   // Human-readable description
	Source      string   // e.g., "keymap.Base", "user config", "grove.toml"
}

// Conflict represents a key clash within a specific domain.
// A conflict occurs when the same key is bound to multiple actions
// within the same domain.
type Conflict struct {
	Key      string       // The conflicting key combination
	Domain   KeyDomain    // The domain where the conflict occurs
	Bindings []KeyBinding // All bindings that use this key
}

// PopupSize defines the dimensions of a tmux popup.
type PopupSize struct {
	Width  string `yaml:"width,omitempty" toml:"width,omitempty"`
	Height string `yaml:"height,omitempty" toml:"height,omitempty"`
}

// PopupPosition defines the alignment of a tmux popup.
type PopupPosition struct {
	X string `yaml:"x,omitempty" toml:"x,omitempty"`
	Y string `yaml:"y,omitempty" toml:"y,omitempty"`
}

// TmuxPopupConfig defines the behavior and appearance of a tmux popup binding.
type TmuxPopupConfig struct {
	Key            interface{}    `yaml:"key" toml:"key"`
	Command        string         `yaml:"command" toml:"command"`
	Style          string         `yaml:"style,omitempty" toml:"style,omitempty"` // "popup", "run-shell", "window"
	Size           *PopupSize     `yaml:"size,omitempty" toml:"size,omitempty"`
	Position       *PopupPosition `yaml:"position,omitempty" toml:"position,omitempty"`
	ExitOnComplete bool           `yaml:"exit_on_complete,omitempty" toml:"exit_on_complete,omitempty"`
}

// ShellKeysConfig defines shell-specific keybindings.
// This supports importing external shell bindings into Grove management.
type ShellKeysConfig struct {
	Enabled  bool              `yaml:"enabled,omitempty" toml:"enabled,omitempty" jsonschema:"description=Whether Grove manages shell keybindings."`
	Bindings map[string]string `yaml:"bindings,omitempty" toml:"bindings,omitempty" jsonschema:"description=Map of key to command (e.g. 'M-g': 'grove')."`
}

// NvimBindingConfig defines a single neovim keybinding.
// Can be unmarshaled from a simple string (command only) or a full struct.
type NvimBindingConfig struct {
	Command string `yaml:"command" toml:"command" jsonschema:"description=Vim command or Lua code to execute"`
	Mode    string `yaml:"mode,omitempty" toml:"mode,omitempty" jsonschema:"description=Vim mode (n, i, v, x, s, o, t, c), default: n"`
	Desc    string `yaml:"desc,omitempty" toml:"desc,omitempty" jsonschema:"description=Description for which-key integration"`
	Silent  bool   `yaml:"silent,omitempty" toml:"silent,omitempty" jsonschema:"description=Whether the binding should be silent (default: true)"`
	NoRemap bool   `yaml:"noremap,omitempty" toml:"noremap,omitempty" jsonschema:"description=Whether to use noremap (default: true)"`
}

// NvimKeysConfig defines neovim-specific keybindings.
type NvimKeysConfig struct {
	Enabled  bool                         `yaml:"enabled,omitempty" toml:"enabled,omitempty" jsonschema:"description=Whether Grove manages Neovim keybindings."`
	Bindings map[string]NvimBindingConfig `yaml:"bindings,omitempty" toml:"bindings,omitempty" jsonschema:"description=Map of key to binding config."`
}

// TmuxKeysConfig defines tmux-specific keybindings.
type TmuxKeysConfig struct {
	Prefix   string                     `yaml:"prefix,omitempty" toml:"prefix,omitempty" jsonschema:"description=Root prefix key for popups (e.g. C-g). If set, creates a tmux key table."`
	Popups   map[string]TmuxPopupConfig `yaml:"popups" toml:"popups"`
	Bindings map[string]string          `yaml:"bindings,omitempty" toml:"bindings,omitempty" jsonschema:"description=Generic tmux bindings not tied to popups (e.g. 'M-z': 'resize-pane -Z')."`
}

// NavKeysConfig defines nav-specific keybindings.
type NavKeysConfig struct {
	Prefix        string   `yaml:"prefix,omitempty" toml:"prefix,omitempty" jsonschema:"description=Prefix key for nav bindings. Options: '<prefix>' (default, native tmux prefix), '<prefix> X' (sub-table under prefix), 'C-g' (dedicated root key), or '' (direct root with modifiers)."`
	AvailableKeys []string `yaml:"available_keys" toml:"available_keys"`
}

// KeysExtension represents the [keys] block in grove.toml/grove.yml.
// This captures tmux popup bindings, nav pane keys, shell bindings, and nvim defaults.
type KeysExtension struct {
	Tmux  TmuxKeysConfig  `yaml:"tmux" toml:"tmux"`
	Nav   NavKeysConfig   `yaml:"nav" toml:"nav"`
	Shell ShellKeysConfig `yaml:"shell,omitempty" toml:"shell,omitempty"`
	Nvim  NvimKeysConfig  `yaml:"nvim,omitempty" toml:"nvim,omitempty"`
}

// TmuxCommandMap maps config action names to actual command invocations.
// This is used when generating tmux popup configuration.
var TmuxCommandMap = map[string]string{
	"flow_status":      "flow tmux status",
	"nb_tui":           "nb tmux tui",
	"session_switcher": "nav sz",
	"editor":           "core editor",
	"diffview":         "nav diffview",
	"nav_key_manager":  "nav km",
	"nav_history":      "nav history",
	"nav_windows":      "nav windows",
	"hooks_sessions":   "hooks sessions browse",
	"tend_sessions":    "tend sessions",
	"cx_view":          "cx view",
	"cx_stats":         "cx stats",
	"cx_list":          "cx list",
	"cx_edit":          "cx rules edit",
	"console":          "console",
}

// AllDomains returns all supported key domains in display order.
func AllDomains() []KeyDomain {
	return []KeyDomain{DomainTUI, DomainTmux, DomainNav, DomainNvim}
}
