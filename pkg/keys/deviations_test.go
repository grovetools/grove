package keys

import "testing"

// TestAnalyze_ConfigKeyLandsInConsistency proves the F2-forward assumption:
// once Action carries a snake ConfigKey (e.g. "page_up"), NormalizeAction
// still maps it onto the canonical action ("page up"), so the binding is
// bucketed under Consistency["page up"].
func TestAnalyze_ConfigKeyLandsInConsistency(t *testing.T) {
	bindings := []KeyBinding{
		{Domain: DomainTUI, TUI: "demo-tui", Action: "page_up", Keys: []string{"ctrl+u", "pgup"}, Description: "page up"},
	}
	report := Analyze(bindings)

	res, ok := report.Consistency["page up"]
	if !ok {
		t.Fatalf("expected Consistency[\"page up\"] to be populated from ConfigKey page_up; got keys %v", keysOf(report.Consistency))
	}
	if _, ok := res.TUIs["demo-tui"]; !ok {
		t.Errorf("expected TUIs keyed by TUI id \"demo-tui\", got %v", res.TUIs)
	}
	if !res.Consistent {
		t.Errorf("expected page up to be consistent (ctrl+u/pgup match canonical), got inconsistent")
	}
}

// TestAnalyze_IntentionalDeviationSuppresses proves a seeded deviation
// suppresses BOTH its reserved-key violation and its semantic-conflict
// participation. tend-sessions X=kill is allowlisted (X is reserved for
// archive).
func TestAnalyze_IntentionalDeviationSuppresses(t *testing.T) {
	bindings := []KeyBinding{
		// canonical use of the reserved key in another TUI
		{Domain: DomainTUI, TUI: "some-archive-tui", Action: "archive", Keys: []string{"X"}, Description: "archive"},
		// the allowlisted deviation
		{Domain: DomainTUI, TUI: "tend-sessions", Action: "kill", Keys: []string{"X"}, Description: "kill session"},
	}
	report := Analyze(bindings)

	// Reserved-key violation for tend-sessions X must be suppressed.
	for _, v := range report.ReservedKeyViolations {
		if v.TUI == "tend-sessions" && v.Key == "X" {
			t.Errorf("expected tend-sessions X=kill to be suppressed as intentional, got violation %+v", v)
		}
	}

	// Semantic conflict on X must not fire: the deviation is skipped, leaving
	// only "archive" as a meaning.
	for _, c := range report.SemanticConflicts {
		if c.Key == "X" {
			t.Errorf("expected no semantic conflict on X (deviation skipped), got %+v", c)
		}
	}
}

// TestAnalyze_NonDeviationReservedStillViolates guards that suppression is
// scoped: a non-allowlisted TUI using X for a non-archive action still flags.
func TestAnalyze_NonDeviationReservedStillViolates(t *testing.T) {
	bindings := []KeyBinding{
		{Domain: DomainTUI, TUI: "rogue-tui", Action: "explode", Keys: []string{"X"}, Description: "explode"},
	}
	report := Analyze(bindings)

	found := false
	for _, v := range report.ReservedKeyViolations {
		if v.TUI == "rogue-tui" && v.Key == "X" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected reserved-key violation for rogue-tui X=explode, got %+v", report.ReservedKeyViolations)
	}
}

// TestDetectConflicts_ScopedByTUI proves conflicts are per-(domain,TUI):
// same key + different action across DIFFERENT TUIs is not a conflict; within
// the SAME TUI it is.
func TestDetectConflicts_ScopedByTUI(t *testing.T) {
	// Different TUIs, same key, different actions -> no conflict.
	cross := DetectConflicts([]KeyBinding{
		{Domain: DomainTUI, TUI: "tui-a", Action: "alpha", Keys: []string{"z"}},
		{Domain: DomainTUI, TUI: "tui-b", Action: "beta", Keys: []string{"z"}},
	})
	if len(cross) != 0 {
		t.Errorf("expected no conflict across different TUIs, got %+v", cross)
	}

	// Same TUI, same key, different actions -> one conflict.
	same := DetectConflicts([]KeyBinding{
		{Domain: DomainTUI, TUI: "tui-a", Action: "alpha", Keys: []string{"z"}},
		{Domain: DomainTUI, TUI: "tui-a", Action: "beta", Keys: []string{"z"}},
	})
	if len(same) != 1 {
		t.Fatalf("expected exactly one intra-TUI conflict, got %+v", same)
	}
	if same[0].TUI != "tui-a" || same[0].Key != "z" {
		t.Errorf("expected conflict TUI=tui-a Key=z, got %+v", same[0])
	}
}

func keysOf(m map[string]ConsistencyResult) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
