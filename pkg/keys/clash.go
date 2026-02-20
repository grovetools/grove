package keys

import (
	"sort"
)

// DetectConflicts finds overlapping key combinations within the same domain.
// Cross-domain "conflicts" (e.g., tmux C-f vs TUI /) are NOT reported
// because they operate in different contexts.
func DetectConflicts(bindings []KeyBinding) []Conflict {
	var conflicts []Conflict

	// Group by domain
	domainMap := make(map[KeyDomain][]KeyBinding)
	for _, b := range bindings {
		domainMap[b.Domain] = append(domainMap[b.Domain], b)
	}

	// Find duplicates within each domain
	for domain, domBindings := range domainMap {
		keyUsage := make(map[string][]KeyBinding)

		for _, b := range domBindings {
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
						Domain:   domain,
						Bindings: uniqueBindings,
					})
				}
			}
		}
	}

	// Sort conflicts by domain then key for consistent output
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Domain != conflicts[j].Domain {
			return conflicts[i].Domain < conflicts[j].Domain
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
