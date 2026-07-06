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
				// Deduplicate: same action might specify the same key twice
				seenActions := make(map[string]bool)
				var uniqueBindings []KeyBinding
				for _, u := range usages {
					if !seenActions[u.Action] {
						seenActions[u.Action] = true
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
