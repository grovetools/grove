package keymap

import (
	"testing"

	"github.com/grovetools/core/tui/keymap"
)

// TestAuditCoverage asserts that every keymap with a scoped Sections() reports
// no coverage gaps: each enabled binding is either in a section or disabled,
// help labels match their keys, and no binding has empty help. This is the
// Job-A enforcement contract (see core/tui/keymap/audit.go).
func TestAuditCoverage(t *testing.T) {
	cases := map[string]keymap.SectionedKeyMap{
		"config":  NewConfigKeyMap(nil),
		"setup":   NewSetupKeyMap(nil),
		"onboard": NewOnboardKeyMap(nil),
		"env":     NewEnvKeyMap(nil),
	}
	for name, km := range cases {
		gaps := keymap.AuditCoverage(km)
		if len(gaps) != 0 {
			for _, g := range gaps {
				t.Errorf("%s: gap %s on %s: %s", name, g.Kind, g.Field, g.Detail)
			}
		}
	}
}
