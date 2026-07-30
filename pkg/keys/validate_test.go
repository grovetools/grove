package keys

import (
	"strings"
	"testing"
)

// TestValidateRegistry_Clean asserts a well-formed registry entry passes.
func TestValidateRegistry_Clean(t *testing.T) {
	entries := []TUIRegistryEntry{
		{
			Name:    "demo-tui",
			Package: "demo",
			Sections: []SectionEntry{
				{Name: "Nav", Bindings: []BindingEntry{
					{Name: "Up", Keys: []string{"k", "up"}, Description: "up", Enabled: true, ConfigKey: "up"},
					{Name: "Down", Keys: []string{"j", "down"}, Description: "down", Enabled: true, ConfigKey: "down"},
				}},
			},
		},
	}
	if got := validateRegistryEntries(entries); len(got) != 0 {
		t.Fatalf("expected clean registry, got problems: %v", got)
	}
}

// TestValidateRegistry_Problems asserts each structural defect is reported and
// that disabled bindings are exempt.
func TestValidateRegistry_Problems(t *testing.T) {
	entries := []TUIRegistryEntry{
		{
			Name:    "", // empty Name
			Package: "", // empty Package
			Sections: []SectionEntry{
				{Name: "S", Bindings: []BindingEntry{
					{Name: "Good", Keys: []string{"g"}, Description: "good", Enabled: true, ConfigKey: "good"},
					{Name: "Dup", Keys: []string{"d"}, Description: "dup", Enabled: true, ConfigKey: "good"},     // duplicate ConfigKey
					{Name: "Bad", Keys: []string{"b"}, Description: "bad", Enabled: true, ConfigKey: "Bad Key!"}, // malformed ConfigKey
					{Name: "NoKeys", Keys: nil, Description: "nokeys", Enabled: true, ConfigKey: "no_keys"},      // missing keys
					{Name: "NoDesc", Keys: []string{"x"}, Description: "", Enabled: true, ConfigKey: "no_desc"},  // empty description
					{Name: "Disabled", Keys: nil, Description: "", Enabled: false, ConfigKey: "!!!"},             // exempt: disabled
				}},
			},
		},
	}
	problems := validateRegistryEntries(entries)
	joined := strings.Join(problems, "\n")

	wantSubstrings := []string{
		"empty Name",
		"empty Package",
		`duplicate ConfigKey "good"`,
		`ConfigKey "Bad Key!" is not`,
		"no keys",
		"empty Description",
	}
	for _, w := range wantSubstrings {
		if !strings.Contains(joined, w) {
			t.Errorf("expected a problem containing %q; got:\n%s", w, joined)
		}
	}
	// The disabled binding must not contribute any problem.
	if strings.Contains(joined, "Disabled") {
		t.Errorf("disabled binding should be exempt from validation; got:\n%s", joined)
	}
}

// TestDetectShadowedPrefixes_Synthetic exercises the predicate: a flat key that
// is a proper prefix of a bound rune-sequence fires; unbound-flat arming pairs,
// named keys, pseudo-keys, and disabled bindings do not.
func TestDetectShadowedPrefixes_Synthetic(t *testing.T) {
	entries := []TUIRegistryEntry{
		{
			Name:    "shadow-tui",
			Package: "demo",
			Sections: []SectionEntry{
				{Name: "S", Bindings: []BindingEntry{
					// flat `v` + `vl` → fires.
					{Name: "Preview", Keys: []string{"v"}, Description: "preview", Enabled: true, ConfigKey: "preview"},
					{Name: "ViewLogs", Keys: []string{"vl"}, Description: "view logs", Enabled: true, ConfigKey: "view_logs"},
					// `gg` with NO flat `g` bound → silent (sanctioned arming pair).
					{Name: "Top", Keys: []string{"gg"}, Description: "top", Enabled: true, ConfigKey: "top"},
					// `d` + `delete`: named key, not a rune sequence → silent.
					{Name: "Delete", Keys: []string{"d", "delete"}, Description: "delete", Enabled: true, ConfigKey: "delete"},
					// `j` + `"j/k"`: pseudo-key contains '/' → silent.
					{Name: "Reorder", Keys: []string{"j", "j/k"}, Description: "reorder", Enabled: true, ConfigKey: "reorder"},
					// disabled flat `x` + `xx` → silent (disabled binding ignored).
					{Name: "Xx", Keys: []string{"x"}, Description: "x", Enabled: false, ConfigKey: "xx_flat"},
					{Name: "XxSeq", Keys: []string{"xx"}, Description: "xx", Enabled: true, ConfigKey: "xx_seq"},
				}},
			},
		},
	}
	got := detectShadowedPrefixes(entries)
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 shadowing (flat v / vl), got %d: %+v", len(got), got)
	}
	if got[0].TUI != "shadow-tui" || got[0].Key != "v" {
		t.Errorf("unexpected shadowing subject: %+v", got[0])
	}
	if len(got[0].ShadowedKeys) != 1 || got[0].ShadowedKeys[0] != "vl" {
		t.Errorf("expected shadowed [vl], got %v", got[0].ShadowedKeys)
	}
}

// TestDetectShadowedPrefixes_Registry asserts the live registry has ZERO
// shadowed prefixes.
//
// This assertion is inverted from what it used to be. The old fixture pinned
// {core-logs/y} as an expected shadowing — and it was WRONG: core-logs binds a
// flat `y` and no `yy` at all, so nothing there was ever shadowed. (The only
// two `yy` bindings in the fleet are both nb-browser's.) The fixture had been
// stale since Phase 4 and was what made the fan-out briefing believe there was
// a core-logs y/yy shadowing bug to fix. There never was one.
//
// "Zero" is the assertion the close-out's strict flip actually needs. Note it
// currently holds partly by luck: four TUIs still bind a flat `y`, and none of
// them happens to also bind `yy`. Canon 60 amendment A8.1 (add `y` to
// ReservedPrefixes) is what would make it hold by construction.
func TestDetectShadowedPrefixes_Registry(t *testing.T) {
	for _, s := range DetectShadowedPrefixes() {
		t.Errorf("prefix shadowing %s/%s: flat %q fires before %v can ever arm",
			s.TUI, s.Key, s.Key, s.ShadowedKeys)
	}
}
