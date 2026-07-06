package keys

// Deviation records a deliberate departure from the canonical keymap: a TUI
// that binds a reserved key (or misses a canonical key) on purpose. Analyze
// consults IntentionalDeviations to suppress the corresponding reserved-key
// violation, semantic-conflict participation, and consistency failure, so that
// legitimate, reasoned choices stop showing up as noise.
//
// Action is stored in NORMALIZED form (post-NormalizeAction) so isIntentional
// can match directly against NormalizeAction(binding.Action). Key is the raw
// key token as the registry stores it (e.g. " " for space).
type Deviation struct {
	TUI    string
	Key    string
	Action string
	Reason string
}

// IntentionalDeviations is the allowlist of deliberate keymap deviations,
// seeded from Phase F design (08 Decision 3 + F-4). Actions are normalized.
var IntentionalDeviations = []Deviation{
	// gemini-cache
	{TUI: "gemini-cache", Key: "d", Action: "delete", Reason: "d deletes cache entry from API; muscle memory from cache workflows"}, // ⚠ if F2 dedup renames ConfigKey (e.g. delete_from_api), update Action to its normalized form ("delete from api")
	{TUI: "gemini-cache", Key: "i", Action: "confirm", Reason: "i=inspect aliases to confirm; enter also bound; clashes with nb edit-search"},
	// flow / tend / cx
	{TUI: "flow-plan-add", Key: "ctrl+g", Action: "toggle claw", Reason: "ctrl+g reserved for cancel/clear; plan-add uses it for claw toggle"},
	{TUI: "tend-sessions", Key: "X", Action: "kill", Reason: "X reserved for archive; kills debug session instead"},
	{TUI: "cx-view", Key: ".", Action: "toggle ignored", Reason: "'.' means focus-selected elsewhere; toggles ignored files here"},
	// git-viewer-changes
	{TUI: "git-viewer-changes", Key: ">", Action: "fold open", Reason: "muscle-memory fold keys; canonical is zo"},
	{TUI: "git-viewer-changes", Key: "<", Action: "fold close", Reason: "muscle-memory fold keys; canonical is zc"},
	{TUI: "git-viewer-changes", Key: " ", Action: "review", Reason: "space reserved for select; marks file for review"},
	{TUI: "git-viewer-changes", Key: "-", Action: "toggle staged", Reason: "'-' reserved for select-none; toggles staged state"},
	{TUI: "git-viewer-changes", Key: "R", Action: "base review", Reason: "R reserved for rename; opens base review"},
	// git-viewer-log / rebase / reviewer
	{TUI: "git-viewer-log", Key: "R", Action: "rebase", Reason: "R reserved for rename; starts rebase"},
	{TUI: "git-viewer-log", Key: "r", Action: "refresh", Reason: "canonical refresh is ctrl+r; r kept for speed"},
	{TUI: "git-viewer-rebase", Key: "r", Action: "refresh", Reason: "canonical refresh is ctrl+r; r kept for speed"},
	{TUI: "git-viewer-reviewer", Key: " ", Action: "toggle reviewed", Reason: "space reserved for select; toggles reviewed state"},
	{TUI: "git-viewer-reviewer", Key: "enter", Action: "toggle reviewed", Reason: "enter reserved for confirm; toggles reviewed state"},
	// treemux / tuimux chord systems
	{TUI: "treemux-app", Key: "ctrl+g", Action: "arm action", Reason: "ctrl+g reserved for cancel/clear; arms the action chord"},
	{TUI: "tuimux-mux", Key: "ctrl+g", Action: "arm action", Reason: "ctrl+g reserved for cancel/clear; arms the action chord"},
	{TUI: "treemux-app", Key: "alt+s", Action: "jump hooks", Reason: "alt+s means scope-toggle in hooks-browser; jumps to Sessions panel here (ConfigKey jump_hooks)"},
	{TUI: "treemux-app", Key: "d", Action: "rail close", Reason: "d closes the rail item (ConfigKey rail_close; also bound to x)"},
	// Phase-1 mechanism-fix additions (24-keymap-consistency §1f): legitimate
	// but too TUI-specific for a global ReservedAlternates family.
	{TUI: "cx-view", Key: "X", Action: "exclude dir", Reason: "X reserved for archive; remove-from-context (X-family adjacent)"},
	{TUI: "cx-view", Key: "left", Action: "switch focus", Reason: "left reserved for nav; Stats-page pane switch"},
	{TUI: "cx-view", Key: "right", Action: "switch focus", Reason: "right reserved for nav; Stats-page pane switch"},
	{TUI: "grove-config", Key: "enter", Action: "edit", Reason: "enter reserved for confirm; edits the tree row (primary action)"},
	{TUI: "nb-browser", Key: "-", Action: "git stage toggle", Reason: "'-' reserved for select-none; mirrors git-viewer-changes -=toggle_staged"},
	{TUI: "nb-browser", Key: "ctrl+g", Action: "clear focus", Reason: "ctrl+g cancel/clear family; clears focus (see ReservedAlternates ctrl+g)"},
	{TUI: "treemux-app", Key: "right", Action: "rail exit", Reason: "right reserved for nav; vim-window 'leave sidebar rightward'"},
	{TUI: "treemux-app", Key: "ctrl+l", Action: "rail exit", Reason: "ctrl+l reserved for clear-search; leaves the rail rightward"},
	{TUI: "nav-manage", Key: "d", Action: "delete", Reason: "canonical delete is dd; d clears a key mapping (not destructive)"},
	{TUI: "nav-manage", Key: "delete", Action: "delete", Reason: "canonical delete is dd; delete clears a key mapping (not destructive)"},
	// grove-release r/R edit/reset pair (Phase 3): R=reset_rules kept as a
	// deviation (R reserved for rename) and its mnemonic partner r=edit_rules
	// (canonical edit is e) allowlisted so the r/R pair stays intact and the
	// residual `edit` consistency failure clears.
	{TUI: "grove-release", Key: "R", Action: "reset", Reason: "R reserved for rename; resets all LLM rules (mnemonic partner of r=edit rules)"},
	{TUI: "grove-release", Key: "r", Action: "edit", Reason: "canonical edit is e; r=edit LLM rules keeps the r/R edit/reset mnemonic pair"},
}

// isIntentional reports whether (tui, key, normAction) is an allowlisted
// deliberate deviation. normAction must already be normalized
// (NormalizeAction). The scan is linear; the allowlist is tiny.
func isIntentional(tui, key, normAction string) bool {
	for _, d := range IntentionalDeviations {
		if d.TUI == tui && d.Key == key && d.Action == normAction {
			return true
		}
	}
	return false
}
