package keys

import (
	"fmt"
	"sort"
	"testing"
)

// TestNoDeadDeviations pins every IntentionalDeviations entry to a binding that
// actually exists in the generated registry.
//
// isIntentional matches on the triple (TUI, raw key, NORMALIZED action), so an
// entry goes dead silently in three ways: the TUI rebinds the action to another
// key, the ConfigKey is renamed out from under the recorded Action, or the
// binding is retired outright. A dead entry suppresses nothing while reading
// like a sanctioned decision — canon 60 §7.1b found exactly one that way
// (flow-plan-add ctrl+g, whose toggle_claw had long since moved to ctrl+t).
// This test is the standing version of that audit.
func TestNoDeadDeviations(t *testing.T) {
	// live[TUI][key] = set of normalized actions bound to that key there.
	live := make(map[string]map[string]map[string]bool)
	for _, b := range getTUIBindingsFromRegistry() {
		norm := NormalizeAction(b.Action)
		for _, k := range b.Keys {
			if live[b.TUI] == nil {
				live[b.TUI] = make(map[string]map[string]bool)
			}
			if live[b.TUI][k] == nil {
				live[b.TUI][k] = make(map[string]bool)
			}
			live[b.TUI][k][norm] = true
		}
	}

	var dead []string
	for _, d := range IntentionalDeviations {
		if live[d.TUI][d.Key][d.Action] {
			continue
		}
		// Report what IS bound there, so the fix is obvious from the failure.
		var got []string
		for a := range live[d.TUI][d.Key] {
			got = append(got, a)
		}
		sort.Strings(got)
		switch {
		case live[d.TUI] == nil:
			dead = append(dead, fmt.Sprintf("{%s, %q, %q}: TUI not in registry", d.TUI, d.Key, d.Action))
		case len(got) == 0:
			dead = append(dead, fmt.Sprintf("{%s, %q, %q}: key is unbound in that TUI", d.TUI, d.Key, d.Action))
		default:
			dead = append(dead, fmt.Sprintf("{%s, %q, %q}: that key is bound to %v", d.TUI, d.Key, d.Action, got))
		}
	}

	if len(dead) > 0 {
		t.Errorf("%d dead deviation(s) — each suppresses nothing but reads as a sanctioned decision:\n  %v",
			len(dead), joinLines(dead))
	}
}

func joinLines(s []string) string {
	out := ""
	for i, line := range s {
		if i > 0 {
			out += "\n  "
		}
		out += line
	}
	return out
}
