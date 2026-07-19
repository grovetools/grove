package cmd

import (
	"strings"
	"testing"
)

func TestShellQuote(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"plain", "grove", "'grove'"},
		{"space", "a b", "'a b'"},
		{"embedded single quote", "it's", `'it'\''s'`},
		{"glob stays literal", "*.go", "'*.go'"},
		{"command substitution stays literal", "$(rm -rf /)", "'$(rm -rf /)'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shellQuote(tc.in); got != tc.want {
				t.Fatalf("shellQuote(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildSatelliteRemoteCommand(t *testing.T) {
	for _, tc := range []struct {
		name string
		dir  string
		args []string
		want string
	}{
		{"no args is a login shell", "", nil, ""},
		{"simple command", "", []string{"grove", "version"}, "'grove' 'version'"},
		{
			name: "argument words keep their spaces",
			args: []string{"sh", "-c", "echo a b"},
			want: "'sh' '-c' 'echo a b'",
		},
		{
			name: "absolute dir",
			dir:  "/home/admin/code",
			args: []string{"ls"},
			want: "cd '/home/admin/code' && 'ls'",
		},
		{
			// A leading ~ names a directory on the SATELLITE, so it must not
			// be quoted into a literal.
			name: "tilde dir expands on the guest",
			dir:  "~/code/grovetools",
			args: []string{"ls"},
			want: `cd "$HOME"/'code/grovetools' && 'ls'`,
		},
		{
			name: "bare tilde",
			dir:  "~",
			args: []string{"pwd"},
			want: `cd "$HOME" && 'pwd'`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildSatelliteRemoteCommand(tc.dir, tc.args); got != tc.want {
				t.Fatalf("buildSatelliteRemoteCommand(%q, %v) = %q, want %q", tc.dir, tc.args, got, tc.want)
			}
		})
	}
}

// TestSatelliteExecVerbsAreRegistered guards R6's premise: the noun advertised
// no way to reach an sshd endpoint it had just provisioned.
func TestSatelliteExecVerbsAreRegistered(t *testing.T) {
	sat := newSatelliteCmd()
	found := map[string]bool{}
	for _, c := range sat.Commands() {
		found[strings.Fields(c.Use)[0]] = true
	}
	for _, verb := range []string{"exec", "ssh", "up", "down", "status", "list"} {
		if !found[verb] {
			t.Errorf("`grove satellite %s` is not registered (have: %v)", verb, found)
		}
	}
}

// TestSatelliteRemoteRefusesUnknownSatellite keeps the transport from being
// reached at all for a name with no registry entry.
func TestSatelliteRemoteRefusesUnknownSatellite(t *testing.T) {
	setupGroveHome(t)
	err := runSatelliteRemote("nosuchsat", "true", false)
	if err == nil {
		t.Fatal("runSatelliteRemote succeeded for an unregistered satellite")
	}
	if !strings.Contains(err.Error(), "not found in the registry") {
		t.Fatalf("error = %q", err.Error())
	}
}
