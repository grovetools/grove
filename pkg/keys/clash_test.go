package keys

import "testing"

// TestDetectConflictsNormalizedDedupe proves the normalize-before-dedupe fix:
// two ConfigKeys on one key that share a canonical meaning (confirm/inspect,
// where "inspect"→"confirm" is an alias) collapse and are not a conflict, while
// two genuinely different meanings still are.
func TestDetectConflictsNormalizedDedupe(t *testing.T) {
	// confirm|inspect on enter — same normalized action, no conflict.
	collapsed := DetectConflicts([]KeyBinding{
		{Domain: DomainTUI, TUI: "gemini-cache", Action: "confirm", Keys: []string{"enter"}},
		{Domain: DomainTUI, TUI: "gemini-cache", Action: "inspect", Keys: []string{"enter"}},
	})
	if len(collapsed) != 0 {
		t.Errorf("expected confirm|inspect to collapse (no conflict), got %+v", collapsed)
	}

	// confirm_move|open on enter — both alias to confirm, no conflict.
	collapsed2 := DetectConflicts([]KeyBinding{
		{Domain: DomainTUI, TUI: "nav-manage", Action: "confirm_move", Keys: []string{"enter"}},
		{Domain: DomainTUI, TUI: "nav-manage", Action: "open", Keys: []string{"enter"}},
	})
	if len(collapsed2) != 0 {
		t.Errorf("expected confirm_move|open to collapse (no conflict), got %+v", collapsed2)
	}

	// Genuinely distinct normalized actions still conflict.
	real := DetectConflicts([]KeyBinding{
		{Domain: DomainTUI, TUI: "cx-view", Action: "select_rules", Keys: []string{"s"}},
		{Domain: DomainTUI, TUI: "cx-view", Action: "switch_focus", Keys: []string{"s"}},
	})
	if len(real) != 1 {
		t.Fatalf("expected one genuine conflict for s, got %+v", real)
	}
}

// TestDetectConflictsDeviationAware proves DetectConflictsFiltered drops an
// allowlisted deviation so its key stops being a conflict, while the unfiltered
// DetectConflicts still reports it (the browsing TUI may want to show it).
func TestDetectConflictsDeviationAware(t *testing.T) {
	// nb-browser ctrl+g: cancel vs clear_focus. clear_focus is an allowlisted
	// deviation ({nb-browser, ctrl+g, clear focus}).
	bindings := []KeyBinding{
		{Domain: DomainTUI, TUI: "nb-browser", Action: "cancel", Keys: []string{"ctrl+g"}},
		{Domain: DomainTUI, TUI: "nb-browser", Action: "clear_focus", Keys: []string{"ctrl+g"}},
	}

	if got := DetectConflicts(bindings); len(got) != 1 {
		t.Errorf("unfiltered: expected ctrl+g conflict to remain, got %+v", got)
	}
	if got := DetectConflictsFiltered(bindings); len(got) != 0 {
		t.Errorf("filtered: expected deviation to clear the ctrl+g conflict, got %+v", got)
	}
}
