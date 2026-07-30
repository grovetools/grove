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
	{TUI: "flow-status", Key: "cn", Action: "rename", Reason: "rename migrated into the c… change namespace; chord-only per E4 (R stays canonical elsewhere)"},
	{TUI: "tend-sessions", Key: "X", Action: "kill", Reason: "X reserved for archive; kills debug session instead"},
	// git-viewer-changes
	{TUI: "git-viewer-changes", Key: " ", Action: "review", Reason: "space reserved for select; marks file for review"},
	{TUI: "git-viewer-changes", Key: "-", Action: "toggle staged", Reason: "'-' reserved for select-none; toggles staged state"},
	// git-viewer-log / rebase / reviewer
	{TUI: "git-viewer-log", Key: "R", Action: "rebase", Reason: "R reserved for rename; starts rebase"},
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
	// nav Phase-5 chord migration: nav-sessionize's column toggles moved into
	// the t… toggle namespace. Three of the mnemonic chords collide with
	// nb-browser's t… members that toggle a different thing (tb=artifacts,
	// tc=columns, tp=preview there). Both sides are the honest mnemonic for
	// their own TUI (branch/cx/paths), so the cross-TUI "different meanings"
	// advisory is deliberate, not drift.
	{TUI: "nav-sessionize", Key: "tb", Action: "toggle branch", Reason: "t… namespace mnemonic for branch column; nb-browser tb toggles artifacts"},
	{TUI: "nav-sessionize", Key: "tc", Action: "toggle cx", Reason: "t… namespace mnemonic for cx column; nb-browser tc toggles columns"},
	{TUI: "nav-sessionize", Key: "tp", Action: "toggle paths", Reason: "t… namespace mnemonic for paths column; nb-browser tp toggles preview"},
	// nav Phase-5 goto namespace: nav-windows g<digit> jumps to the tmux
	// window INDEX (0-based, hence the extra g0) while nav-history/nav-manage
	// g<digit> jump to the list ROW. Same gesture, honestly different target
	// nouns — allowlist the windows side so g1…g9 don't read as semantic
	// conflicts against "jump to row".
	{TUI: "nav-windows", Key: "g1", Action: "window", Reason: "goto chord jumps to tmux window index; history/manage g<digit> jump to row"},
	{TUI: "nav-windows", Key: "g2", Action: "window", Reason: "goto chord jumps to tmux window index; history/manage g<digit> jump to row"},
	{TUI: "nav-windows", Key: "g3", Action: "window", Reason: "goto chord jumps to tmux window index; history/manage g<digit> jump to row"},
	{TUI: "nav-windows", Key: "g4", Action: "window", Reason: "goto chord jumps to tmux window index; history/manage g<digit> jump to row"},
	{TUI: "nav-windows", Key: "g5", Action: "window", Reason: "goto chord jumps to tmux window index; history/manage g<digit> jump to row"},
	{TUI: "nav-windows", Key: "g6", Action: "window", Reason: "goto chord jumps to tmux window index; history/manage g<digit> jump to row"},
	{TUI: "nav-windows", Key: "g7", Action: "window", Reason: "goto chord jumps to tmux window index; history/manage g<digit> jump to row"},
	{TUI: "nav-windows", Key: "g8", Action: "window", Reason: "goto chord jumps to tmux window index; history/manage g<digit> jump to row"},
	{TUI: "nav-windows", Key: "g9", Action: "window", Reason: "goto chord jumps to tmux window index; history/manage g<digit> jump to row"},

	// ══ canon 60 §7.4 — the chord-wave encoding pass ══
	//
	// ── §5.2: git-viewer-local git verbs (subject-matter Ring-1). Staging IS
	// the subject matter in git-viewer, so magit's own flat vocabulary wins
	// there and nowhere else. ──
	{TUI: "git-viewer-changes", Key: "s", Action: "stage", Reason: "canon 60 §5.2 git-viewer Ring-1 git verb; `s` is sort-ish elsewhere"},
	{TUI: "git-viewer-changes", Key: "u", Action: "unstage", Reason: "canon 60 §5.2; `u` is undo elsewhere"},
	{TUI: "git-viewer-changes", Key: "x", Action: "discard", Reason: "canon 60 §5.2; `x` is cut elsewhere — MUST be confirm-gated (contract 31 §5.1)"},
	{TUI: "git-viewer-changes", Key: "r", Action: "review", Reason: "canon 60 §5.5 `r`=primary verb (review sense)"},
	{TUI: "git-viewer-changes", Key: "cR", Action: "base review", Reason: "git-viewer-local mnemonic; unclaimed c… member (replaces the retired flat R)"},
	{TUI: "git-viewer-rebase", Key: "u", Action: "rebase current", Reason: "canon 60 §5.2"},
	{TUI: "git-viewer-rebase", Key: "U", Action: "rebase all", Reason: "canon 60 §5.2"},
	{TUI: "git-viewer-rebase", Key: "L", Action: "land", Reason: "canon 60 §5.2"},
	{TUI: "git-viewer-rebase", Key: "D", Action: "delete remote", Reason: "canon 60 §5.2"},
	{TUI: "git-viewer-log", Key: "w", Action: "worktrees", Reason: "canon 60 §5.2"},
	{TUI: "git-viewer-rebase", Key: "w", Action: "worktrees", Reason: "canon 60 §5.2"},
	{TUI: "git-viewer-reviewer", Key: "r", Action: "toggle reviewed", Reason: "canon 60 §5.5 `r`=primary verb; here the primary verb IS toggle-reviewed (same action as its space/enter)"},
	{TUI: "flow-plan-list", Key: "r", Action: "review plan", Reason: "canon 60 §5.5 `r`=primary verb (review plan)"},

	// ── §5.3: grove-release semver triad. The release picker's primary
	// interaction is choosing a bump level; m/n/p are the level names. ──
	{TUI: "grove-release", Key: "m", Action: "select major", Reason: "canon 60 §5.3 semver triad; release picker's primary interaction"},
	{TUI: "grove-release", Key: "n", Action: "select minor", Reason: "canon 60 §5.3 semver triad"},
	{TUI: "grove-release", Key: "p", Action: "select patch", Reason: "canon 60 §5.3 semver triad"},
	{TUI: "grove-release", Key: "s", Action: "apply suggestion", Reason: "canon 60 §5.3"},
	{TUI: "grove-release", Key: "a", Action: "approve", Reason: "canon 60 §5.3; `a`=create elsewhere (Ring-1)"},

	// ── §5.4: gemini time-frame family. A period browser's Ring-1 IS the
	// period names; d/w/m/y are not the fleet's d/w/m/y. ──
	{TUI: "gemini-dashboard", Key: "d", Action: "daily view", Reason: "canon 60 §5.4 period-browser Ring-1"},
	{TUI: "gemini-dashboard", Key: "w", Action: "weekly view", Reason: "canon 60 §5.4"},
	{TUI: "gemini-dashboard", Key: "m", Action: "monthly view", Reason: "canon 60 §5.4"},
	{TUI: "gemini-dashboard", Key: "3", Action: "quarterly view", Reason: "canon 60 §5.4; digits are tab-N elsewhere"},
	{TUI: "gemini-dashboard", Key: "y", Action: "yearly view", Reason: "canon 60 §5.4; also exempts the `y` arming letter if §7.3 lands"},
	{TUI: "gemini-query", Key: "d", Action: "daily view", Reason: "canon 60 §5.4"},
	{TUI: "gemini-query", Key: "w", Action: "weekly view", Reason: "canon 60 §5.4"},
	{TUI: "gemini-query", Key: "m", Action: "monthly view", Reason: "canon 60 §5.4"},
	{TUI: "gemini-cache", Key: "w", Action: "wipe", Reason: "canon 60 §5.4-adjacent; gemini-local"},
	{TUI: "gemini-cache", Key: "va", Action: "analytics", Reason: "v… mnemonic for analytics; flow-status va views the agent pane"},

	// ── §3.1/§3.3: nb note-ops + the wizard verbs ──
	{TUI: "nb-browser", Key: "cn", Action: "rename", Reason: "rename migrated into c…; chord-only per E4 — mirrors flow-status cn"},
	{TUI: "nb-browser", Key: "cp", Action: "create plan", Reason: "c… promote-to-plan; flow-status cp sets provider"},
	{TUI: "nb-browser", Key: "cj", Action: "promote to", Reason: "c… promote-to-job (the \" job\" suffix strips)"},
	{TUI: "grove-onboard", Key: "y", Action: "yes", Reason: "canon 60 §5.6; wizard y/n prompt, not a list TUI"},
	{TUI: "grove-onboard", Key: "n", Action: "no", Reason: "canon 60 §3.3; wizard y/n prompt"},
	{TUI: "grove-onboard", Key: "s", Action: "skip", Reason: "canon 60 §3.3; wizard verb"},

	// ── §1/§0: modal-dialog and buffer semantics. Both clear a reserved-key
	// violation by naming what the key really means in that context. ──
	{TUI: "flow-plan-finish", Key: "esc", Action: "quit", Reason: "modal confirm screen: its `back` IS `quit` — the screen has nowhere to go back to"},
	{TUI: "core-logs", Key: "ctrl+l", Action: "clear buffer", Reason: "ctrl+l = clear-the-screen is the terminal idiom in a log viewer; it drops the buffer, not the search filter"},
	{TUI: "hooks-browser", Key: "X", Action: "cleanup stale", Reason: "X reserved for archive; same shape as tend-sessions X=kill"},

	// ── §4: intra-namespace cross-TUI collisions. Same shape as the landed
	// nav tb/tc/tp trio: both sides are the honest mnemonic for their own
	// TUI, so the advisory is deliberate rather than drift. ──
	{TUI: "core-logs", Key: "vj", Action: "view json", Reason: "v… mnemonic for json; flow-status vj views job artifacts"},
	{TUI: "cx-view", Key: "tc", Action: "toggle cold", Reason: "t… mnemonic for cold; nb/flow tc toggles columns"},
	{TUI: "cx-view", Key: "tab", Action: "switch focus", Reason: "tab is Base.SwitchView territory; stats-page pane switch"},
	{TUI: "gemini-query", Key: "tm", Action: "toggle metric", Reason: "t… mnemonic for metric; memory-view tm cycles search mode"},
	{TUI: "grove-config", Key: "vs", Action: "sources", Reason: "v… mnemonic for sources; grove-release vs views docs sections"},
	{TUI: "grove-release", Key: "vd", Action: "diff docs", Reason: "v… mnemonic for docs diff; git-viewer vd is the diff"},
	{TUI: "grove-release", Key: "ce", Action: "edit repo changelog", Reason: "c… mnemonic for edit-repo-CHANGELOG (distinct from its own e=edit_changelog); flow-status ce sets template"},
	{TUI: "git-viewer-changes", Key: "cm", Action: "base main", Reason: "c… mnemonic for diff-base main; flow-status cm sets model"},
	{TUI: "flow-plan-add", Key: "cc", Action: "quick chat", Reason: "c… mnemonic for chat; flow-status/hooks cc marks completed"},
	{TUI: "flow-plan-add", Key: "ta", Action: "toggle claw", Reason: "ta=archives/all elsewhere"},
	{TUI: "flow-plan-init", Key: "ta", Action: "toggle advanced", Reason: "ta=archives/all elsewhere"},
	{TUI: "flow-plan-list", Key: "tg", Action: "toggle git log", Reason: "nb tg toggles global"},
	{TUI: "flow-plan-finish", Key: "tf", Action: "toggle force", Reason: "destructive-action gate; core-logs tf toggles filters"},
	{TUI: "flow-status", Key: "cC", Action: "toggle claw", Reason: "c… uppercase member, matches its cM/cA"},
	{TUI: "memory-view", Key: "ca", Action: "append context", Reason: "c… mnemonic for add-to-context; flow-plan-add ca is quick-agent"},

	// ── §5.1/§5.5: residual Ring-1 multi-meanings ──
	{TUI: "cx-rules", Key: "s", Action: "save", Reason: "canon 60 §5.1; cx-rules-local verb"},
	{TUI: "cx-rules", Key: "l", Action: "load", Reason: "canon 60 §5.1; `l`=right elsewhere"},
	{TUI: "cx-view", Key: "s", Action: "confirm", Reason: "EXISTING select_rules — keep; canon 60 resolves the intra-TUI half by moving switch_focus to tab"},
	{TUI: "cx-view", Key: "x", Action: "exclude", Reason: "canon 60 §5.1; `x`=cut elsewhere"},
	{TUI: "flow-plan-list", Key: "s", Action: "set active", Reason: "canon 60 §5.1"},
	{TUI: "flow-status", Key: "f", Action: "toggle fullscreen", Reason: "EXISTING intent (E2 moved fullscreen off z); canon 60 §5.1"},
	{TUI: "flow-status", Key: "V", Action: "toggle layout", Reason: "canon 60 §5.1"},
	{TUI: "flow-status", Key: "I", Action: "agent from chat", Reason: "canon 60 §5.1; nb I creates a global note"},
	{TUI: "core-logs", Key: "V", Action: "visual mode start", Reason: "canon 60 §5.1; vim visual"},
	{TUI: "nb-browser", Key: "S", Action: "sync", Reason: "canon 60 §5.1 Ring-1 S=sync; nav-manage S saves to group"},
	{TUI: "nb-browser", Key: "f", Action: "focus recent", Reason: "canon 60 §5.1; f=filter-toggle elsewhere moved to tf"},
	{TUI: "nb-browser", Key: "=", Action: "git stage all", Reason: "canon 60 §3.2; mirrors the existing `-` stage-toggle deviation"},
	{TUI: "nb-browser", Key: "+", Action: "git unstage all", Reason: "canon 60 §3.2"},
	// hooks-browser's 1-9 jump to a session row; the same digits are tab-N in
	// cx / git-viewer / grove-config / memory.
	{TUI: "hooks-browser", Key: "1", Action: "workspace", Reason: "canon 60 §5.1; digits are tab-N in cx/git-viewer/grove-config/memory"},
	{TUI: "hooks-browser", Key: "2", Action: "workspace", Reason: "canon 60 §5.1; digits are tab-N in cx/git-viewer/grove-config/memory"},
	{TUI: "hooks-browser", Key: "3", Action: "workspace", Reason: "canon 60 §5.1; digits are tab-N in cx/git-viewer/grove-config/memory"},
	{TUI: "hooks-browser", Key: "4", Action: "workspace", Reason: "canon 60 §5.1; digits are tab-N in cx/git-viewer/grove-config/memory"},
	{TUI: "hooks-browser", Key: "5", Action: "workspace", Reason: "canon 60 §5.1; digits are tab-N in cx/git-viewer/grove-config/memory"},
	{TUI: "hooks-browser", Key: "6", Action: "workspace", Reason: "canon 60 §5.1; digits are tab-N in cx/git-viewer/grove-config/memory"},
	{TUI: "hooks-browser", Key: "7", Action: "workspace", Reason: "canon 60 §5.1; digits are tab-N in cx/git-viewer/grove-config/memory"},
	{TUI: "hooks-browser", Key: "8", Action: "workspace", Reason: "canon 60 §5.1; digits are tab-N in cx/git-viewer/grove-config/memory"},
	{TUI: "hooks-browser", Key: "9", Action: "workspace", Reason: "canon 60 §5.1; digits are tab-N in cx/git-viewer/grove-config/memory"},
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
