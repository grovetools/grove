package cmd

import "testing"

// The delegation shortcut in Execute() runs before cobra parses anything, so a
// registered repo name that collides with a built-in command used to swallow
// the built-in whole. `sync` is that collision: an ecosystem repo (alias
// grove-syncd) AND grove's notebook-sync command. These tests pin the rule that
// resolves it — the built-in wins for the subcommands it declares, and only
// those.
func TestBuiltinClaimsArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
		why  string
	}{
		{"sync", []string{"adopt", "grovetools"}, true, "grove declares `sync adopt`; it must not go to the server binary"},
		{"sync", []string{"doctor"}, true, "grove declares `sync doctor`"},
		{"sync", []string{"serve", "--bind", "127.0.0.1:8788"}, false, "grove declares no `sync serve`; the server binary still owns it"},
		{"sync", []string{"token", "create", "laptop"}, false, "token management is the server's"},
		{"sync", nil, true, "bare `grove sync` shows the built-in help, which names both halves"},
		{"sync", []string{"--help"}, true, "flags alone do not select a delegated subcommand"},
		{"sync", []string{"-v", "adopt"}, true, "flags are skipped when looking for the subcommand"},
		{"flow", []string{"status"}, false, "grove has no `flow` command at all — pure delegation"},
		{"nb", []string{"note", "new"}, false, "likewise"},
	}
	for _, tc := range cases {
		if got := builtinClaimsArgs(tc.name, tc.args); got != tc.want {
			t.Errorf("builtinClaimsArgs(%q, %v) = %t, want %t — %s", tc.name, tc.args, got, tc.want, tc.why)
		}
	}
}

// TestSyncCommandDeclaresItsVerbs guards the pairing above: if `adopt` is ever
// renamed, the delegation rule silently stops claiming it and the verb becomes
// unreachable again rather than failing loudly.
func TestSyncCommandDeclaresItsVerbs(t *testing.T) {
	cmd := newSyncCmd()
	have := map[string]bool{}
	for _, sub := range cmd.Commands() {
		have[sub.Name()] = true
	}
	for _, want := range []string{"adopt", "doctor"} {
		if !have[want] {
			t.Errorf("`grove sync` no longer declares %q; delegation will shadow it", want)
		}
	}
}
