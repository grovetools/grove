package keys

import (
	"strings"
)

// CanonicalAction represents a standard action with expected key bindings.
type CanonicalAction struct {
	Name string
	Keys []string
}

// ReservedKeys are owned by keymap.Base and should NOT be used by TUIs
// for different purposes. These provide consistent behavior across all TUIs.
var ReservedKeys = map[string]string{
	// Navigation (vim-style)
	"j":      "down",
	"k":      "up",
	"up":     "up",
	"down":   "down",
	"left":   "left",
	"right":  "right",
	"ctrl+u": "page up",
	"ctrl+d": "page down",
	"pgup":   "page up",
	"pgdown": "page down",
	"gg":     "top",
	"G":      "bottom",

	// Search (n/N only reserved when TUI has search - checked separately)
	"/":      "search",
	"ctrl+l": "clear search",

	// Selection
	" ":  "select",
	"ctrl+a": "select all",
	"-":      "select none",

	// Core actions
	"e":      "edit",
	"dd":     "delete",
	"yy":     "yank",
	"R":      "rename",
	"X":      "archive",
	"ctrl+r": "refresh",
	"ctrl+y": "copy path",
	"enter":  "confirm",
	"esc":    "back",
	"ctrl+g": "cancel/clear",

	// System
	"q":      "quit",
	"ctrl+c": "quit",
	"?":      "help",

	// Folding (tree TUIs)
	"zo": "fold open",
	"zc": "fold close",
	"za": "fold toggle",
	"zR": "fold open all",
	"zM": "fold close all",
}

// FreeKeys are available for TUI-specific functions.
// TUIs can use these without conflicting with Base bindings.
var FreeKeys = []string{
	// Lowercase letters (not in Base)
	"a", "b", "c", "d", "f", "h", "i", "l", "m", "o", "p", "r", "s", "t", "u", "v", "w", "x", "y",

	// Uppercase letters (except reserved: G, N, R, X)
	"A", "B", "C", "D", "E", "F", "H", "I", "L", "M", "O", "P", "S", "T", "U", "V", "W", "Y",

	// Number keys
	"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",

	// Ctrl combinations (not in Base)
	"ctrl+e", "ctrl+j", "ctrl+k", "ctrl+o", "ctrl+s", "ctrl+x",

	// Tab
	"tab", "shift+tab",
}

// StandardActions defines the canonical actions based on keymap.Base.
// These represent common operations that should be consistent across TUIs.
// TUIs implementing these actions SHOULD use the specified keys.
var StandardActions = []CanonicalAction{
	// Navigation
	{"up", []string{"k", "up"}},
	{"down", []string{"j", "down"}},
	{"page up", []string{"ctrl+u", "pgup"}},
	{"page down", []string{"ctrl+d", "pgdown"}},
	{"top", []string{"gg"}},
	{"bottom", []string{"G"}},

	// Selection
	{"select", []string{" "}},
	{"select all", []string{"ctrl+a"}},
	{"select none", []string{"-"}},

	// Actions
	{"archive", []string{"X"}},
	{"delete", []string{"dd"}},
	{"edit", []string{"e"}},
	{"rename", []string{"R"}},
	{"refresh", []string{"ctrl+r"}},
	{"copy path", []string{"ctrl+y"}},

	// Focus
	{"focus ecosystem", []string{"@"}},
	{"clear focus", []string{"ctrl+g"}},

	// Search
	{"search", []string{"/"}},
	{"next match", []string{"n"}},
	{"prev match", []string{"N"}},

	// System
	{"help", []string{"?"}},
	{"quit", []string{"q", "ctrl+c"}},
}

// CandidateActions are actions that appear in multiple TUIs and could
// potentially be standardized in keymap.Base in the future.
var CandidateActions = []CanonicalAction{
	{"run", []string{"r"}},            // flow-status, tend-runner
	{"open", []string{"o"}},           // hooks, gemini, flow
	{"toggle view", []string{"tab"}},  // multiple TUIs
}

// freeKeysSet is a set for fast lookup of free keys.
var freeKeysSet map[string]bool

func init() {
	freeKeysSet = make(map[string]bool)
	for _, k := range FreeKeys {
		freeKeysSet[k] = true
	}
}

// IsFreeKey returns true if the key is in the FreeKeys list.
func IsFreeKey(key string) bool {
	return freeKeysSet[key]
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

// ReservedKeyViolation captures when a TUI uses a reserved key for non-standard purpose.
type ReservedKeyViolation struct {
	Key            string `json:"key"`
	ExpectedAction string `json:"expected_action"`
	ActualAction   string `json:"actual_action"`
	TUI            string `json:"tui"`
}

// AnalysisReport contains the full analysis of keybindings.
type AnalysisReport struct {
	Consistency            map[string]ConsistencyResult `json:"canonical_consistency"`
	SemanticConflicts      []SemanticConflict           `json:"semantic_conflicts"`
	ReservedKeyViolations  []ReservedKeyViolation       `json:"reserved_key_violations"`
}

// NormalizeAction standardizes action names to allow cross-TUI comparison.
// It strips domain-specific terms and normalizes common variations.
func NormalizeAction(name string) string {
	n := strings.ToLower(name)
	n = strings.ReplaceAll(n, "_", " ")
	n = strings.TrimSpace(n)

	// Strip domain-specific suffixes
	suffixes := []string{" selected", " job", " note", " session", " item", " entry", " list", " rules"}
	for _, s := range suffixes {
		n = strings.TrimSuffix(n, s)
	}

	// Normalize common aliases BEFORE stripping prefixes
	// This allows "toggle fold" to match "fold toggle" etc.
	aliases := map[string]string{
		// Page navigation
		"half page up":   "page up",
		"half page down": "page down",
		// Folding (normalize both directions)
		"close fold":   "fold close",
		"open fold":    "fold open",
		"toggle fold":  "fold toggle",
		"fold toggle":  "fold toggle", // ensure consistency
		"open all":     "fold open all",
		"close all":    "fold close all",
		"expand":       "fold open",
		"collapse":     "fold close",
		"expand all":   "fold open all",
		"collapse all": "fold close all",
		// Selection
		"all":  "select all",
		"none": "select none",
		// Navigation
		"next field":       "down",
		"prev field":       "up",
		"move up":          "up",
		"move down":        "down",
		"scroll up":        "page up",
		"scroll down":      "page down",
		"normal mode":      "back",
		"back to plan":     "quit",
		"clear":            "cancel/clear",
		"cancel":           "cancel/clear",
		// Actions
		"submit":              "confirm",
		"submit form":         "confirm",
		"confirm and proceed": "confirm",
		"view plan details":   "confirm",
		"view":                "confirm",
		"show help":           "help",
		"attach":              "confirm",
		"run":                 "confirm", // enter for run is acceptable
		"attach to":           "confirm",
		"toggle":           "select",
		"toggle select":    "select",
		"toggle selection": "select",
		"close detail pane":   "back",
		"edit notes":  "edit",
		"edit config": "edit",
		"create new plan":     "create", // n for create is intentional, not search-next
		"inspect":             "confirm",
		"previous period":     "left",
		"next period":         "right",
	}
	if alias, ok := aliases[n]; ok {
		n = alias
	}

	// Strip common prefixes AFTER aliases (so "toggle fold" matches first)
	prefixes := []string{"go to ", "jump to "}
	for _, p := range prefixes {
		n = strings.TrimPrefix(n, p)
	}

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

	// 3. Check Reserved Key Violations
	// Warn when TUIs use reserved keys for non-standard purposes
	for _, b := range bindings {
		if b.Domain != DomainTUI {
			continue
		}
		normAction := NormalizeAction(b.Action)
		for _, k := range b.Keys {
			if expectedAction, isReserved := ReservedKeys[k]; isReserved {
				// Check if the action matches the expected action
				expectedNorm := NormalizeAction(expectedAction)
				if normAction != expectedNorm {
					report.ReservedKeyViolations = append(report.ReservedKeyViolations, ReservedKeyViolation{
						Key:            k,
						ExpectedAction: expectedAction,
						ActualAction:   b.Action,
						TUI:            b.Source,
					})
				}
			}
		}
	}

	return report
}
