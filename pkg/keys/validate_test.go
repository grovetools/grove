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
