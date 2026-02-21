package keys

import (
	"strings"
)

// CanonicalAction represents a standard action with expected key bindings.
type CanonicalAction struct {
	Name string
	Keys []string
}

// StandardActions defines the canonical actions based on keymap.Base.
// These represent common operations that should be consistent across TUIs.
var StandardActions = []CanonicalAction{
	// Navigation
	{"up", []string{"k", "up"}},
	{"down", []string{"j", "down"}},
	{"page up", []string{"ctrl+u", "pgup"}},
	{"page down", []string{"ctrl+d", "pgdown"}},
	{"top", []string{"gg"}},
	{"bottom", []string{"G"}},

	// Selection
	{"select", []string{"space", "x"}},
	{"select all", []string{"a"}},
	{"select none", []string{"A"}},
	{"toggle select", []string{"v"}},

	// Destructive/Creation
	{"archive", []string{"X"}},
	{"delete", []string{"dd"}},
	{"create", []string{"n"}},

	// Focus/Navigation
	{"focus ecosystem", []string{"@"}},
	{"clear focus", []string{"ctrl+g"}},
	{"focus selected", []string{"."}},
	{"jump", []string{"1-9"}},

	// Search
	{"search", []string{"/"}},
	{"next match", []string{"n"}},
	{"prev match", []string{"N"}},

	// System
	{"help", []string{"?"}},
	{"quit", []string{"q", "ctrl+c"}},
	{"refresh", []string{"ctrl+r"}},
}

// ConsistencyResult captures the consistency analysis for a single canonical action.
type ConsistencyResult struct {
	CanonicalKeys []string            `json:"canonical_keys"`
	Consistent    bool                `json:"consistent"`
	TUIs          map[string][]string `json:"tuis"`
}

// SemanticConflict captures cases where the same key has different meanings.
type SemanticConflict struct {
	Key      string            `json:"key"`
	Meanings map[string]string `json:"meanings"`
}

// AnalysisReport contains the full analysis of keybindings.
type AnalysisReport struct {
	Consistency       map[string]ConsistencyResult `json:"canonical_consistency"`
	SemanticConflicts []SemanticConflict           `json:"semantic_conflicts"`
}

// NormalizeAction standardizes action names to allow cross-TUI comparison.
// It strips domain-specific terms like "selected", "job", "note" to find
// the canonical root action.
func NormalizeAction(name string) string {
	n := strings.ToLower(name)
	n = strings.ReplaceAll(n, " selected", "")
	n = strings.ReplaceAll(n, " job", "")
	n = strings.ReplaceAll(n, " note", "")
	n = strings.ReplaceAll(n, " session", "")
	n = strings.ReplaceAll(n, " item", "")
	n = strings.ReplaceAll(n, " entry", "")
	n = strings.ReplaceAll(n, "_", " ")
	return strings.TrimSpace(n)
}

// Analyze generates a consistency and semantic conflict report for the given bindings.
func Analyze(bindings []KeyBinding) AnalysisReport {
	report := AnalysisReport{
		Consistency: make(map[string]ConsistencyResult),
	}

	// 1. Analyze Canonical Consistency
	for _, canon := range StandardActions {
		res := ConsistencyResult{
			CanonicalKeys: canon.Keys,
			Consistent:    true,
			TUIs:          make(map[string][]string),
		}

		for _, b := range bindings {
			if b.Domain != DomainTUI {
				continue
			}
			normName := NormalizeAction(b.Action)
			if normName == canon.Name {
				res.TUIs[b.Source] = b.Keys
				// Check if any binding key matches canonical
				match := false
				for _, bk := range b.Keys {
					for _, ck := range canon.Keys {
						if bk == ck {
							match = true
							break
						}
					}
					if match {
						break
					}
				}
				if !match {
					res.Consistent = false
				}
			}
		}
		if len(res.TUIs) > 0 {
			report.Consistency[canon.Name] = res
		}
	}

	// 2. Analyze Semantic Conflicts
	// Group by key to find keys used for different actions across TUIs
	keyUsage := make(map[string]map[string]string) // Key -> Source -> ActionName
	for _, b := range bindings {
		if b.Domain != DomainTUI {
			continue
		}
		for _, k := range b.Keys {
			if keyUsage[k] == nil {
				keyUsage[k] = make(map[string]string)
			}
			// Store the normalized action per key per source
			keyUsage[k][b.Source] = NormalizeAction(b.Action)
		}
	}

	for key, sources := range keyUsage {
		if len(sources) <= 1 {
			continue
		}
		// Check if all sources use the same meaning
		firstMeaning := ""
		conflict := false
		for _, meaning := range sources {
			if firstMeaning == "" {
				firstMeaning = meaning
			} else if meaning != firstMeaning {
				conflict = true
				break
			}
		}
		if conflict {
			report.SemanticConflicts = append(report.SemanticConflicts, SemanticConflict{
				Key:      key,
				Meanings: sources,
			})
		}
	}

	return report
}
