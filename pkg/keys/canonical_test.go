package keys

import "testing"

// TestAnalyzePrefixSquatters_Synthetic proves the squatter check fires on a flat
// binding on a reserved prefix regardless of its action's meaning (no family
// exemption), stays silent for rune-sequence keys and chords, and is suppressed
// by an intentional deviation.
func TestAnalyzePrefixSquatters_Synthetic(t *testing.T) {
	bindings := []KeyBinding{
		// Flat `t` on a "toggle sort" action → squats the toggle namespace.
		{Domain: DomainTUI, TUI: "syn-a", Action: "toggle_sort", Keys: []string{"t"}},
		// Flat `v` on an unrelated action → still a squatter (no family test).
		{Domain: DomainTUI, TUI: "syn-a", Action: "cycle_doc_type", Keys: []string{"v"}},
		// A rune-sequence key `gg` must NOT squat `g` (only exact single-key).
		{Domain: DomainTUI, TUI: "syn-a", Action: "top", Keys: []string{"gg"}},
		// A chord-layer key must never squat.
		{Domain: DomainTUI, TUI: "syn-a", Action: "view_logs", Keys: []string{"<action> t"}},
		// Non-TUI domain is ignored.
		{Domain: DomainTmux, TUI: "", Action: "whatever", Keys: []string{"c"}},
	}
	sq := Analyze(bindings).PrefixSquatters
	if len(sq) != 2 {
		t.Fatalf("expected 2 squatters, got %d: %+v", len(sq), sq)
	}
	got := map[string]string{} // key -> namespace
	for _, s := range sq {
		got[s.Key] = s.Namespace
		if s.TUI != "syn-a" {
			t.Errorf("unexpected TUI %q in %+v", s.TUI, s)
		}
	}
	if got["t"] != "toggle" {
		t.Errorf("flat t: want namespace toggle, got %q", got["t"])
	}
	if got["v"] != "view" {
		t.Errorf("flat v: want namespace view, got %q", got["v"])
	}

	// An intentional deviation suppresses the squatter. cx-view X=exclude_dir is
	// a real deviation; craft an analogous synthetic one via a temporary entry.
	saved := IntentionalDeviations
	defer func() { IntentionalDeviations = saved }()
	IntentionalDeviations = append(saved, Deviation{
		TUI: "syn-b", Key: "t", Action: NormalizeAction("toggle_sort"), Reason: "test",
	})
	exempt := []KeyBinding{
		{Domain: DomainTUI, TUI: "syn-b", Action: "toggle_sort", Keys: []string{"t"}},
	}
	if sq := Analyze(exempt).PrefixSquatters; len(sq) != 0 {
		t.Errorf("expected intentional deviation to suppress squatter, got %+v", sq)
	}
}

// TestAnalyzePrefixSquatters_Registry runs the check over the live registry and
// asserts a representative membership set, logging the total rather than
// hard-asserting an exact count so registry drift does not spuriously fail.
func TestAnalyzePrefixSquatters_Registry(t *testing.T) {
	sq := Analyze(getTUIBindingsFromRegistry()).PrefixSquatters
	t.Logf("registry prefix squatters: %d", len(sq))

	have := map[string]bool{} // "tui/key"
	for _, s := range sq {
		have[s.TUI+"/"+s.Key] = true
	}
	// flow-status was the Phase-3 pilot: its v/c/t/z flat squatters were
	// vacated onto v…/c… namespace chords, so it is intentionally NO LONGER a
	// squatter. core-logs/v (toggle_preview) stands in as the representative
	// flat-view squatter until later phases restructure it too.
	want := []string{
		"core-logs/v",
		"memory-view/t",
		"nav-history/g",
		"nb-browser/c",
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("expected registry squatter %q, not found in %d squatters", w, len(sq))
		}
	}
	if len(sq) == 0 {
		t.Error("expected a non-empty squatter set from the live registry")
	}
}

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
