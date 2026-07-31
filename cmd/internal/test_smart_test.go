package internal

import (
	"reflect"
	"testing"
)

// The regression this exists for: " M path" — a modified tracked file, which is
// what an agent turn actually produces — used to come back as "ath", one
// character short, because the line was trimmed before a fixed-width slice.
// A mangled path intersects no test scope, so test-smart quietly degraded to
// running the whole suite every time.
func TestParsePorcelainKeepsTheFirstCharacterOfModifiedPaths(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{"modified unstaged", " M grove.toml", "grove.toml"},
		{"modified staged", "M  pkg/context/resolve.go", "pkg/context/resolve.go"},
		{"staged and modified", "MM cmd/internal/test_smart.go", "cmd/internal/test_smart.go"},
		{"added", "A  .cx/plugin-protocol.rules", ".cx/plugin-protocol.rules"},
		{"deleted", " D old.go", "old.go"},
		{"untracked", "?? pkg/panelkit/sidecar/new.go", "pkg/panelkit/sidecar/new.go"},
		{"rename", "R  old/path.go -> new/path.go", "new/path.go"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePorcelain(tc.line + "\n")
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("parsePorcelain(%q) = %v, want [%q]", tc.line, got, tc.want)
			}
		})
	}
}

func TestParsePorcelainMultipleLinesAndBlanks(t *testing.T) {
	out := " M grove.toml\n?? .cx/plugin-protocol.rules\n\n M internal/app/plugins.go\n"
	want := []string{"grove.toml", ".cx/plugin-protocol.rules", "internal/app/plugins.go"}
	if got := parsePorcelain(out); !reflect.DeepEqual(got, want) {
		t.Errorf("parsePorcelain() = %v, want %v", got, want)
	}
}
