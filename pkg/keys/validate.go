package keys

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// configKeyPattern is the required shape of a grove.toml override handle:
// lowercase letters, digits, and underscores only.
var configKeyPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

// runeSeqPattern matches a rune-sequence key: a letter followed by one or more
// letters/digits (e.g. "gg", "yy", "zo"). Single characters and anything with a
// modifier/separator are excluded elsewhere.
var runeSeqPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]+$`)

// funcKeyPattern matches bubbletea function-key spellings ("f7", "f10"). These
// syntactically look like rune sequences but are single keystrokes, so they can
// never be composed from a flat prefix and must be excluded from shadowing.
var funcKeyPattern = regexp.MustCompile(`^f[0-9]{1,2}$`)

// shadowNamedKeys are the multi-character bubbletea key spellings that are a
// SINGLE keystroke, not a rune sequence: SequenceState.Update never composes
// them from a flat prefix, so they must never count as a shadowed sequence.
// Without this, flat `e` would "shadow" esc/enter/end, and nav-manage's
// Delete ["d","delete"] would false-positive. Spellings verified against
// registry_generated.go (e.g. pgdown, not pgdn).
var shadowNamedKeys = map[string]bool{
	"up": true, "down": true, "left": true, "right": true,
	"home": true, "end": true, "esc": true, "enter": true,
	"tab": true, "space": true, "pgup": true, "pgdown": true,
	"delete": true, "backspace": true, "insert": true,
}

// PrefixShadowing captures an intra-TUI case where a flat single-letter key A is
// bound while A is also a proper prefix of some bound rune-sequence key B (e.g.
// flat `y`=confirm coexisting with `yy`=yank): pressing A fires immediately, so
// B can never arm. Advisory; the sanctioned arming pairs (g/gg, z/zo, d/dd,
// y/yy where the prefix is UNBOUND flat) cannot appear here because the trigger
// requires A to be bound flat.
type PrefixShadowing struct {
	TUI          string   `json:"tui"`
	Key          string   `json:"key"`
	Action       string   `json:"action"`
	ShadowedKeys []string `json:"shadowed_keys"`
}

// isSingleLetterKey reports whether k is exactly one ASCII letter.
func isSingleLetterKey(k string) bool {
	if len(k) != 1 {
		return false
	}
	c := k[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isRuneSequenceKey reports whether k is a composable multi-keystroke rune
// sequence (the kind SequenceState arms letter-by-letter): a letter-led
// alphanumeric token with no modifier/separator, excluding named keys and
// function keys.
func isRuneSequenceKey(k string) bool {
	if strings.ContainsAny(k, "+/ ") {
		return false
	}
	if !runeSeqPattern.MatchString(k) {
		return false
	}
	if shadowNamedKeys[k] || funcKeyPattern.MatchString(k) {
		return false
	}
	return true
}

// DetectShadowedPrefixes returns every intra-TUI prefix-shadowing case in the
// generated TUIRegistry (see PrefixShadowing). It is a SEPARATE exported
// function, deliberately NOT folded into ValidateRegistry's []string return:
// keys_audit.go counts that return as unconditional errorCount, and this
// finding must stay advisory until Phase 5.
func DetectShadowedPrefixes() []PrefixShadowing {
	return detectShadowedPrefixes(TUIRegistry)
}

// detectShadowedPrefixes is the testable core, operating on an explicit slice.
func detectShadowedPrefixes(entries []TUIRegistryEntry) []PrefixShadowing {
	var out []PrefixShadowing
	for _, tui := range entries {
		// flat: single-letter key -> its ConfigKey action (first binding wins).
		flat := make(map[string]string)
		seqs := make(map[string]bool)
		for _, section := range tui.Sections {
			for _, b := range section.Bindings {
				if !b.Enabled {
					continue
				}
				for _, k := range b.Keys {
					switch {
					case isSingleLetterKey(k):
						if _, ok := flat[k]; !ok {
							flat[k] = b.ConfigKey
						}
					case isRuneSequenceKey(k):
						seqs[k] = true
					}
				}
			}
		}

		flatKeys := make([]string, 0, len(flat))
		for k := range flat {
			flatKeys = append(flatKeys, k)
		}
		sort.Strings(flatKeys)

		for _, a := range flatKeys {
			var shadowed []string
			for b := range seqs {
				if strings.HasPrefix(b, a) {
					shadowed = append(shadowed, b)
				}
			}
			if len(shadowed) == 0 {
				continue
			}
			if isIntentional(tui.Name, a, NormalizeAction(flat[a])) {
				continue
			}
			sort.Strings(shadowed)
			out = append(out, PrefixShadowing{
				TUI:          tui.Name,
				Key:          a,
				Action:       flat[a],
				ShadowedKeys: shadowed,
			})
		}
	}
	return out
}

// ValidateRegistry performs structural validation over the generated TUIRegistry
// and returns a sorted list of human-readable problems (empty == clean).
//
// These are the OBJECTIVE, build-affecting invariants the generator must
// uphold for the registry to be a usable source of truth: every TUI must be
// identifiable, every enabled binding must carry the fields the aggregator and
// override system read, ConfigKeys must be well-formed grove.toml handles, and
// a ConfigKey must resolve to a single override target within its TUI (a
// duplicate ConfigKey makes `grove keys` overrides ambiguous).
//
// It deliberately does NOT flag the same physical key resolving to different
// ConfigKeys across a TUI's pages/panes: merged multi-page TUIs (e.g. cx-view,
// grove-config) legitimately reuse keys per context, and distinguishing those
// requires per-page tui-ids (out of scope). That class is surfaced — as
// advisory — by DetectConflicts instead.
func ValidateRegistry() []string {
	return validateRegistryEntries(TUIRegistry)
}

// validateRegistryEntries is the testable core of ValidateRegistry, operating
// on an explicit slice rather than the package-global TUIRegistry.
func validateRegistryEntries(entries []TUIRegistryEntry) []string {
	var problems []string

	for _, tui := range entries {
		if tui.Name == "" {
			problems = append(problems, fmt.Sprintf("TUI with package %q has an empty Name", tui.Package))
		}
		if tui.Package == "" {
			problems = append(problems, fmt.Sprintf("TUI %q has an empty Package", tui.Name))
		}

		// ConfigKey -> the keys it was seen with, to detect duplicates within
		// this TUI (Action == ConfigKey, so a dup corrupts override resolution
		// and DetectConflicts dedup).
		seenConfigKey := make(map[string]bool)

		for _, section := range tui.Sections {
			for _, b := range section.Bindings {
				if !b.Enabled {
					continue
				}
				where := fmt.Sprintf("%s / %s / %q", tui.Name, section.Name, b.Name)
				if b.Name == "" {
					problems = append(problems, fmt.Sprintf("%s: empty binding Name", where))
				}
				if len(b.Keys) == 0 {
					problems = append(problems, fmt.Sprintf("%s: no keys", where))
				}
				if b.Description == "" {
					problems = append(problems, fmt.Sprintf("%s: empty Description", where))
				}
				if b.ConfigKey == "" {
					problems = append(problems, fmt.Sprintf("%s: empty ConfigKey", where))
				} else {
					if !configKeyPattern.MatchString(b.ConfigKey) {
						problems = append(problems, fmt.Sprintf("%s: ConfigKey %q is not [a-z0-9_]+", where, b.ConfigKey))
					}
					if seenConfigKey[b.ConfigKey] {
						problems = append(problems, fmt.Sprintf("%s: duplicate ConfigKey %q within TUI %q", where, b.ConfigKey, tui.Name))
					}
					seenConfigKey[b.ConfigKey] = true
				}
			}
		}
	}

	sort.Strings(problems)
	return problems
}
