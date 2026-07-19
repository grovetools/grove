package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
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

// TestBuildSatelliteRemoteCommand states each case as the command the guest's
// login shell must end up running; the wrapper that gets it there is pinned
// separately by TestRemoteLoginShell.
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
			want := tc.want
			if want != "" {
				want = remoteLoginShell(want)
			}
			if got := buildSatelliteRemoteCommand(tc.dir, tc.args); got != want {
				t.Fatalf("buildSatelliteRemoteCommand(%q, %v) = %q, want %q", tc.dir, tc.args, got, want)
			}
		})
	}
}

// TestRemoteLoginShell pins the wrapper that makes `exec` and `ssh` agree
// about the guest environment: the remote command must reach a LOGIN shell,
// as ONE argument to -lc, or the grove stack's own bin directory (added by the
// login profile alone) is off PATH and the verb's own help example exits 127.
func TestRemoteLoginShell(t *testing.T) {
	if got, want := remoteLoginShell("'grove' 'version'"), `exec ${SHELL:-/bin/sh} -lc ''\''grove'\'' '\''version'\'''`; got != want {
		t.Fatalf("remoteLoginShell = %s, want %s", got, want)
	}
}

// TestBuildSatelliteRemoteCommandRoundTrip runs the rendered command through a
// real shell and checks the argv that comes out the far side is the argv that
// went in, word for word. The login-shell wrapper adds a second round of
// quoting to survive, which is exactly where hostile words (spaces, quotes,
// $, globs) would be mangled or — worse — expanded.
//
// $SHELL is a stub that asserts it was invoked as the wrapper promises and
// then runs the command without sourcing any profile, so the test does not
// depend on the developer's own login shell or dotfiles.
func TestBuildSatelliteRemoteCommandRoundTrip(t *testing.T) {
	stub := filepath.Join(t.TempDir(), "login-shell-stub")
	script := "#!/bin/sh\n" +
		`if [ "$#" -ne 2 ] || [ "$1" != "-lc" ]; then echo "stub: got $# arg(s): $*" >&2; exit 64; fi` + "\n" +
		`exec /bin/sh -c "$2"` + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	hostile := []string{"a b", "it's", `he said "hi"`, "$HOME", "${SHELL}", "$(exit 7)", "`exit 7`", "*.go", "a;b", `back\slash`, "tab\there", "a\nb", "--flag=x y", ""}

	for _, tc := range []struct {
		name string
		dir  string
		args []string
		want string
	}{
		{
			name: "hostile words survive verbatim",
			args: append([]string{"printf", `%s\n`}, hostile...),
			want: strings.Join(hostile, "\n") + "\n",
		},
		{
			name: "dir with spaces and quotes",
			dir:  "",
			args: []string{"printf", `%s\n`, "plain"},
			want: "plain\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("/bin/sh", "-c", buildSatelliteRemoteCommand(tc.dir, tc.args))
			cmd.Env = append(os.Environ(), "SHELL="+stub)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("running rendered command: %v", err)
			}
			if string(out) != tc.want {
				t.Fatalf("remote argv = %q, want %q", string(out), tc.want)
			}
		})
	}

	// --dir is part of the same quoted payload, so it faces the same hazard.
	dir := filepath.Join(t.TempDir(), "a dir with 'quotes' & $vars")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", buildSatelliteRemoteCommand(dir, []string{"pwd"}))
	cmd.Env = append(os.Environ(), "SHELL="+stub)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running rendered command with --dir: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != dir {
		t.Fatalf("remote cwd = %q, want %q", got, dir)
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
