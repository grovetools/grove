package keys

import (
	"sort"
)

// DetectConflicts finds overlapping key combinations within the same
// (domain, TUI) scope. Cross-domain "conflicts" (e.g., tmux C-f vs TUI /) are
// NOT reported because they operate in different contexts; likewise
// same-key/different-TUI bindings (e.g. every TUI's enter/tab) and
// same-key/different-page bindings that live under distinct registry TUI ids
// (e.g. git-viewer's per-page c/u/w/o/s/m/r) are NOT conflicts — each is its
// own keyspace. A conflict means one TUI binds one key to two different
// actions, which is a real bug. Non-TUI domains carry an empty TUI, so they
// bucket exactly as before.
func DetectConflicts(bindings []KeyBinding) []Conflict {
	return detectConflicts(bindings, false)
}

// DetectConflictsFiltered is DetectConflicts with deviation-awareness: a usage
// that is an allowlisted intentional deviation (deviations.go) for its key is
// dropped before counting, so a key whose only "extra" action is a sanctioned
// deviation is not reported as an intra-TUI conflict. The audit gate
// (cmd/keys_audit.go) uses this; the browsing/keys TUI may still want to *show*
// deviated conflicts, so it keeps using DetectConflicts.
func DetectConflictsFiltered(bindings []KeyBinding) []Conflict {
	return detectConflicts(bindings, true)
}

// detectConflicts is the shared implementation. It dedupes usages on the
// NORMALIZED action (so two ConfigKeys that mean the same canonical action —
// e.g. confirm/inspect — collapse to one and don't count as a conflict). When
// filterDeviations is set, allowlisted deviations are dropped first.
func detectConflicts(bindings []KeyBinding, filterDeviations bool) []Conflict {
	var conflicts []Conflict

	// scope identifies a single conflict namespace: a domain, and within
	// DomainTUI, a specific registry TUI id.
	type scope struct {
		domain KeyDomain
		tui    string
	}

	// Group by (domain, TUI)
	scopeMap := make(map[scope][]KeyBinding)
	for _, b := range bindings {
		scopeMap[scope{b.Domain, b.TUI}] = append(scopeMap[scope{b.Domain, b.TUI}], b)
	}

	// Find duplicates within each scope
	for sc, scopeBindings := range scopeMap {
		keyUsage := make(map[string][]KeyBinding)

		for _, b := range scopeBindings {
			for _, keyCombo := range b.Keys {
				if keyCombo == "" {
					continue
				}
				keyUsage[keyCombo] = append(keyUsage[keyCombo], b)
			}
		}

		for keyCombo, usages := range keyUsage {
			if len(usages) > 1 {
				// Deduplicate on the NORMALIZED action, so two ConfigKeys that
				// share a canonical meaning (confirm|inspect, confirm_move|open,
				// exclude|toggle_exclude) collapse to one and don't count.
				seenActions := make(map[string]bool)
				var uniqueBindings []KeyBinding
				for _, u := range usages {
					normAction := NormalizeAction(u.Action)
					// Drop allowlisted deviations for this key: a sanctioned
					// deviation is not a competing meaning.
					if filterDeviations && isIntentional(u.TUI, keyCombo, normAction) {
						continue
					}
					if !seenActions[normAction] {
						seenActions[normAction] = true
						uniqueBindings = append(uniqueBindings, u)
					}
				}

				if len(uniqueBindings) > 1 {
					conflicts = append(conflicts, Conflict{
						Key:      keyCombo,
						Domain:   sc.domain,
						TUI:      sc.tui,
						Bindings: uniqueBindings,
					})
				}
			}
		}
	}

	// Sort conflicts by domain, then TUI, then key for consistent output
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Domain != conflicts[j].Domain {
			return conflicts[i].Domain < conflicts[j].Domain
		}
		if conflicts[i].TUI != conflicts[j].TUI {
			return conflicts[i].TUI < conflicts[j].TUI
		}
		return conflicts[i].Key < conflicts[j].Key
	})

	return conflicts
}

// GroupConflictsByDomain returns conflicts organized by domain.
func GroupConflictsByDomain(conflicts []Conflict) map[KeyDomain][]Conflict {
	result := make(map[KeyDomain][]Conflict)
	for _, c := range conflicts {
		result[c.Domain] = append(result[c.Domain], c)
	}
	return result
}

// HasConflicts returns true if there are any conflicts in the given domain.
func HasConflicts(conflicts []Conflict, domain KeyDomain) bool {
	for _, c := range conflicts {
		if c.Domain == domain {
			return true
		}
	}
	return false
}

// CountConflicts returns the number of conflicts for a given domain.
func CountConflicts(conflicts []Conflict, domain KeyDomain) int {
	count := 0
	for _, c := range conflicts {
		if c.Domain == domain {
			count++
		}
	}
	return count
}
