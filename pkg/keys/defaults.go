package keys

// DefaultTmuxPrefix is the prefix key used for the built-in tmux popup
// bindings when the user has not configured one. All default popup keys are
// bound inside this prefix table, so they never clobber root-table keys.
const DefaultTmuxPrefix = "C-g"

// DefaultTmuxPopups returns the built-in tmux popup bindings. These mirror the
// boilerplate config that was previously shipped as a commented-out template
// (tmux-popups.toml), so users get working bindings without writing any
// config. They are only used when the user config defines no
// [keys.tmux.popups] entries; any user-defined popups replace this table
// entirely.
func DefaultTmuxPopups() map[string]TmuxPopupConfig {
	return map[string]TmuxPopupConfig{
		"flow_status":      {Key: "p", Command: "flow tmux status", Style: "run-shell"},
		"nav_key_manager":  {Key: "m", Command: "nav km", Style: "popup", ExitOnComplete: true},
		"nav_history":      {Key: "h", Command: "nav history", Style: "popup", ExitOnComplete: true},
		"session_switcher": {Key: "f", Command: "nav sz", Style: "popup", ExitOnComplete: true},
		"nav_windows":      {Key: "w", Command: "nav windows", Style: "popup", ExitOnComplete: true},
		"cx_view":          {Key: "v", Command: "cx view", Style: "popup", ExitOnComplete: true},
		"cx_stats":         {Key: "t", Command: "cx view --page stats", Style: "popup"},
		"cx_list":          {Key: "l", Command: "cx view --page list", Style: "popup"},
		"hooks_sessions":   {Key: "s", Command: "hooks sessions browse", Style: "popup"},
		"tend_sessions":    {Key: "j", Command: "tend sessions", Style: "popup", ExitOnComplete: true},
		"nb_tui":           {Key: "n", Command: "nb tmux tui", Style: "run-shell"},
		"editor":           {Key: "e", Command: "core tmux editor", Style: "run-shell"},
	}
}

// ApplyTmuxPopupDefaults fills the extension with the built-in popup bindings
// when the user config defines none. If the user has not set a prefix, the
// default prefix is used so the single-letter default keys are never bound in
// the tmux root table. Returns true if the defaults were applied.
func (e *KeysExtension) ApplyTmuxPopupDefaults() bool {
	if len(e.Tmux.Popups) > 0 {
		return false
	}
	e.Tmux.Popups = DefaultTmuxPopups()
	if e.Tmux.Prefix == "" {
		e.Tmux.Prefix = DefaultTmuxPrefix
	}
	return true
}
