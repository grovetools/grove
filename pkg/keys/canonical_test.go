package keys

import "testing"

// TestNormalizeAction covers the Phase-1 alias reorder: every ConfigKey the
// alias-before-suffix-strip change rescues, plus a regression proving suffix
// stripping still fires when no alias exists.
func TestNormalizeAction(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Rescued by the reorder alone (full-form alias was being shadowed by
		// the " session" suffix strip).
		{"select_session", "confirm"},
		// Rescued by new alias entries (search-in-place / list-nav reuse).
		{"filter", "search"},
		{"focus_search", "search"},
		{"open_result", "confirm"},
		{"exit_search", "back"},
		{"cancel_chord", "back"},
		{"result_up", "up"},
		{"result_down", "down"},
		{"confirm_move", "confirm"},
		{"switch", "confirm"},
		{"open", "confirm"},
		{"toggle_expand", "fold toggle"},
		{"toggle_exclude", "exclude"},
		// Regression: no alias for "archive_selected" — the " selected" suffix
		// strip must still apply, yielding "archive".
		{"archive_selected", "archive"},
		// Regression: pre-existing aliases keep working through the reorder.
		{"expand", "fold open"},
		{"collapse", "fold close"},
		{"toggle_fold", "fold toggle"},
		{"page_up", "page up"},
		// Regression: switch_focus must NOT collapse to confirm (the "switch"
		// alias is a full-form match, not a prefix).
		{"switch_focus", "switch focus"},
	}
	for _, c := range cases {
		if got := NormalizeAction(c.in); got != c.want {
			t.Errorf("NormalizeAction(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestAnalyzeReservedAlternates proves a sanctioned ReservedAlternates family
// member on a reserved key is not a violation, while a non-member is.
func TestAnalyzeReservedAlternates(t *testing.T) {
	// Each of these binds a reserved key to a sanctioned alternate action.
	ok := []KeyBinding{
		{Domain: DomainTUI, TUI: "tree-a", Action: "fold_toggle", Keys: []string{"enter"}},
		{Domain: DomainTUI, TUI: "tree-b", Action: "fold_toggle", Keys: []string{" "}},
		{Domain: DomainTUI, TUI: "tree-c", Action: "fold_close", Keys: []string{"left"}},
		{Domain: DomainTUI, TUI: "tree-d", Action: "fold_open", Keys: []string{"right"}},
		{Domain: DomainTUI, TUI: "esc-a", Action: "cancel", Keys: []string{"esc"}},
		{Domain: DomainTUI, TUI: "cg-a", Action: "clear_focus", Keys: []string{"ctrl+g"}},
		{Domain: DomainTUI, TUI: "x-a", Action: "close", Keys: []string{"X"}},
	}
	if v := Analyze(ok).ReservedKeyViolations; len(v) != 0 {
		t.Errorf("expected no reserved violations for sanctioned alternates, got %+v", v)
	}

	// A non-member action on enter (delete is not in ReservedAlternates["enter"])
	// must still be a violation.
	bad := []KeyBinding{
		{Domain: DomainTUI, TUI: "bad-tui", Action: "delete", Keys: []string{"enter"}},
	}
	v := Analyze(bad).ReservedKeyViolations
	if len(v) != 1 || v[0].Key != "enter" || v[0].TUI != "bad-tui" {
		t.Errorf("expected one enter violation for non-member action, got %+v", v)
	}
}

// TestAnalyzeChordExclusion proves chord-layer keys (<leader>/<action>) are
// excluded from consistency, semantic, and reserved analysis — they live in a
// separate keyspace and must not pollute base-layer canon.
func TestAnalyzeChordExclusion(t *testing.T) {
	bindings := []KeyBinding{
		// Canonical help on "?" plus a chord-layer help binding: without the
		// exclusion the chord key would mark "help" inconsistent.
		{Domain: DomainTUI, TUI: "base-tui", Action: "help", Keys: []string{"?"}},
		{Domain: DomainTUI, TUI: "chord-tui", Action: "help", Keys: []string{"<leader> ?"}},
		// A chord binding for a non-help action must not participate at all.
		{Domain: DomainTUI, TUI: "chord-tui", Action: "rename_session", Keys: []string{"<leader> $"}},
	}
	report := Analyze(bindings)

	if res, ok := report.Consistency["help"]; ok && !res.Consistent {
		t.Errorf("expected help to stay consistent (chord key excluded), got %+v", res)
	}
	if _, ok := report.Consistency["help"].TUIs["chord-tui"]; ok {
		t.Errorf("chord-tui should be excluded from help consistency, got %+v", report.Consistency["help"].TUIs)
	}
	// The chord-only rename binding must not surface as a rename inconsistency.
	if res, ok := report.Consistency["rename"]; ok && !res.Consistent {
		t.Errorf("expected no rename inconsistency from a chord-only binding, got %+v", res)
	}
	for _, v := range report.ReservedKeyViolations {
		if isChordKey(v.Key) {
			t.Errorf("chord key %q leaked into reserved violations: %+v", v.Key, v)
		}
	}
}
