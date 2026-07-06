package keys

import (
	"fmt"
	"regexp"
	"sort"
)

// configKeyPattern is the required shape of a grove.toml override handle:
// lowercase letters, digits, and underscores only.
var configKeyPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

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
