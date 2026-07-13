package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
)

// loadProvisionViaConfig loads the merged grove config the way `up` does and
// returns the provision block for name.
func loadProvisionViaConfig(t *testing.T, name string) satelliteProvisionConfig {
	t.Helper()
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatalf("config.LoadFrom: %v", err)
	}
	prov, err := satelliteProvisionFromConfig(cfg, name)
	if err != nil {
		t.Fatalf("satelliteProvisionFromConfig: %v", err)
	}
	return prov
}

// TestSatelliteProvisionConfigParse covers the new [satellites.<name>.provision]
// block: it parses out of the same layered grove.toml the registry lives in,
// and an absent satellite/block yields the zero value.
func TestSatelliteProvisionConfigParse(t *testing.T) {
	configDir := setupGroveHome(t)
	content := `[satellites.sat1]
ssh_addr = "203.0.113.7:22"
user = "solair"
host_key = "ssh-ed25519 AAAA"

[satellites.sat1.provision]
gh_token_cmd = "gh auth token"
claude = true
claude_token_cmd = "cat ~/.secrets/claude-token"
dotfiles_repo = "https://github.com/me/dotfiles"
service_account_email = "sa@proj.iam.gserviceaccount.com"
`
	if err := os.WriteFile(filepath.Join(configDir, "grove.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	want := satelliteProvisionConfig{
		GHTokenCmd:          "gh auth token",
		Claude:              true,
		ClaudeTokenCmd:      "cat ~/.secrets/claude-token",
		DotfilesRepo:        "https://github.com/me/dotfiles",
		ServiceAccountEmail: "sa@proj.iam.gserviceaccount.com",
	}
	if got := loadProvisionViaConfig(t, "sat1"); got != want {
		t.Fatalf("provision = %+v, want %+v", got, want)
	}

	// Unknown satellite / satellite without a provision block → zero value.
	if got := loadProvisionViaConfig(t, "absent"); got != (satelliteProvisionConfig{}) {
		t.Fatalf("absent satellite provision = %+v, want zero", got)
	}
}

// TestMergeProvisionPrecedence pins flag > config, including the explicit
// set-to-empty override and the token⇒install implication.
func TestMergeProvisionPrecedence(t *testing.T) {
	cfg := satelliteProvisionConfig{
		GHTokenCmd:          "gh auth token",
		Claude:              true,
		ClaudeTokenCmd:      "config-claude-cmd",
		DotfilesRepo:        "https://github.com/me/dotfiles",
		ServiceAccountEmail: "cfg-sa@proj.iam.gserviceaccount.com",
	}

	// No flags set → config passes through untouched.
	if got := mergeProvision(cfg, provisionFlagOverrides{}); got != cfg {
		t.Fatalf("no-flags merge = %+v, want %+v", got, cfg)
	}

	// Every flag set → flags win.
	got := mergeProvision(cfg, provisionFlagOverrides{
		GHTokenCmd: "flag-gh-cmd", GHTokenCmdSet: true,
		Claude: false, ClaudeSet: true,
		ClaudeTokenCmd: "", ClaudeTokenSet: true, // set-to-empty disables config value
		DotfilesRepo: "https://github.com/me/other", DotfilesSet: true,
		ServiceAccount: "flag-sa@proj.iam.gserviceaccount.com", ServiceAccSet: true,
	})
	want := satelliteProvisionConfig{
		GHTokenCmd:          "flag-gh-cmd",
		Claude:              false,
		ClaudeTokenCmd:      "",
		DotfilesRepo:        "https://github.com/me/other",
		ServiceAccountEmail: "flag-sa@proj.iam.gserviceaccount.com",
	}
	if got != want {
		t.Fatalf("all-flags merge = %+v, want %+v", got, want)
	}

	// Unset flags never override, even with non-zero values lying around.
	got = mergeProvision(cfg, provisionFlagOverrides{GHTokenCmd: "ignored", Claude: false})
	if got.GHTokenCmd != "gh auth token" || !got.Claude {
		t.Fatalf("unset flags leaked into merge: %+v", got)
	}

	// A claude token command (from either side) implies the install.
	got = mergeProvision(satelliteProvisionConfig{}, provisionFlagOverrides{
		ClaudeTokenCmd: "claude setup-token", ClaudeTokenSet: true,
	})
	if !got.Claude {
		t.Fatal("claude_token_cmd must imply claude install (script: --claude-token-stdin implies --claude)")
	}
	got = mergeProvision(satelliteProvisionConfig{ClaudeTokenCmd: "x"}, provisionFlagOverrides{})
	if !got.Claude {
		t.Fatal("config claude_token_cmd must imply claude install")
	}
}

// parseBootstrapSecretsLikeScript re-implements satellite-bootstrap.sh's stdin
// parsing rules (its "--- read secrets from stdin ---" block, per the header
// contract) so the assembled framing is validated against the consumer's exact
// semantics rather than against itself:
//   - both flags: order-free KEY=VALUE lines; blank lines and #-comments
//     ignored; any other line is an error; both keys required.
//   - exactly one flag: `IFS= read -r` — the FIRST line is the raw token.
func parseBootstrapSecretsLikeScript(ghFlag, claudeFlag bool, stdin string) (gh, claude string, err error) {
	lines := strings.Split(stdin, "\n")
	switch {
	case ghFlag && claudeFlag:
		for _, line := range lines {
			// `while IFS= read -r line || [ -n "$line" ]` — a trailing empty
			// segment after the final \n is never seen by the loop body's
			// non-blank cases; blank lines are ignored anyway.
			switch {
			case strings.HasPrefix(line, "GH_TOKEN="):
				gh = strings.TrimPrefix(line, "GH_TOKEN=")
			case strings.HasPrefix(line, "CLAUDE_CODE_OAUTH_TOKEN="):
				claude = strings.TrimPrefix(line, "CLAUDE_CODE_OAUTH_TOKEN=")
			case line == "" || strings.HasPrefix(line, "#"):
			default:
				return "", "", fmt.Errorf("unrecognized stdin line %q", line)
			}
		}
		if gh == "" {
			return "", "", fmt.Errorf("missing GH_TOKEN=... on stdin")
		}
		if claude == "" {
			return "", "", fmt.Errorf("missing CLAUDE_CODE_OAUTH_TOKEN=... on stdin")
		}
		return gh, claude, nil
	case ghFlag:
		if gh = lines[0]; gh == "" {
			return "", "", fmt.Errorf("--gh-token-stdin: no token on stdin")
		}
		return gh, "", nil
	case claudeFlag:
		if claude = lines[0]; claude == "" {
			return "", "", fmt.Errorf("--claude-token-stdin: no token on stdin")
		}
		return "", claude, nil
	}
	return "", "", nil
}

// TestBuildBootstrapProvisionFraming drives every token combination through
// the assembler and back through the script's parsing rules, and pins the
// argv/secrecy invariants (tokens never in args; flag set matches the framing).
func TestBuildBootstrapProvisionFraming(t *testing.T) {
	cases := []struct {
		name                 string
		opts                 satelliteProvisionConfig
		ghToken, claudeToken string
		wantArgs             []string
	}{
		{
			name:     "no provisioning",
			wantArgs: nil,
		},
		{
			name:     "gh token only (raw framing)",
			ghToken:  "ghp_abc123",
			wantArgs: []string{"--gh-token-stdin"},
		},
		{
			name:        "claude token only (raw framing, no redundant --claude)",
			opts:        satelliteProvisionConfig{Claude: true},
			claudeToken: "sk-ant-oat01-xyz",
			wantArgs:    []string{"--claude-token-stdin"},
		},
		{
			name:        "both tokens (KEY=VALUE framing)",
			opts:        satelliteProvisionConfig{Claude: true},
			ghToken:     "ghp_abc123",
			claudeToken: "sk-ant-oat01-xyz",
			wantArgs:    []string{"--gh-token-stdin", "--claude-token-stdin"},
		},
		{
			name:     "claude install without token",
			opts:     satelliteProvisionConfig{Claude: true},
			wantArgs: []string{"--claude"},
		},
		{
			name:     "dotfiles only",
			opts:     satelliteProvisionConfig{DotfilesRepo: "https://github.com/me/dotfiles"},
			wantArgs: []string{"--dotfiles-repo", "https://github.com/me/dotfiles"},
		},
		{
			name:        "everything",
			opts:        satelliteProvisionConfig{Claude: true, DotfilesRepo: "https://github.com/me/dotfiles"},
			ghToken:     "ghp_abc123",
			claudeToken: "sk-ant-oat01-xyz",
			wantArgs:    []string{"--gh-token-stdin", "--claude-token-stdin", "--dotfiles-repo", "https://github.com/me/dotfiles"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, stdin := buildBootstrapProvision(tc.opts, tc.ghToken, tc.claudeToken)
			if got, want := strings.Join(args, " "), strings.Join(tc.wantArgs, " "); got != want {
				t.Fatalf("args = %q, want %q", got, want)
			}

			// Secrets never in argv.
			joined := strings.Join(args, " ")
			for _, secret := range []string{tc.ghToken, tc.claudeToken} {
				if secret != "" && strings.Contains(joined, secret) {
					t.Fatalf("secret %q leaked into argv: %q", secret, joined)
				}
			}

			// No tokens → nothing on stdin (bootstrap must not block reading).
			if tc.ghToken == "" && tc.claudeToken == "" {
				if stdin != "" {
					t.Fatalf("stdin should be empty without tokens, got %q", stdin)
				}
				return
			}

			// Round-trip the payload through the script's own parsing rules.
			ghFlag := containsString(args, "--gh-token-stdin")
			claudeFlag := containsString(args, "--claude-token-stdin")
			gh, claude, err := parseBootstrapSecretsLikeScript(ghFlag, claudeFlag, stdin)
			if err != nil {
				t.Fatalf("script-side parse rejected the framing: %v\nstdin: %q", err, stdin)
			}
			if gh != tc.ghToken || claude != tc.claudeToken {
				t.Fatalf("round-trip = (gh %q, claude %q), want (%q, %q)", gh, claude, tc.ghToken, tc.claudeToken)
			}
		})
	}
}

// TestRunTokenCommand exercises the local token execution contract: trimmed
// single-line stdout, hard errors on failure/empty/multi-line output.
func TestRunTokenCommand(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh") // deterministic across fish/zsh dev machines

	if got, err := runTokenCommand("echo '  tok123  '"); err != nil || got != "tok123" {
		t.Fatalf("happy path = (%q, %v), want (tok123, nil)", got, err)
	}
	if _, err := runTokenCommand("exit 3"); err == nil {
		t.Fatal("failing command must be a hard error")
	}
	if _, err := runTokenCommand("true"); err == nil {
		t.Fatal("empty output must be a hard error")
	}
	if _, err := runTokenCommand("printf 'a\\nb\\n'"); err == nil {
		t.Fatal("multi-line output must be a hard error (framing is line-oriented)")
	}

	// $SHELL fallback: with SHELL unset the command still runs via /bin/sh.
	t.Setenv("SHELL", "")
	if got, err := runTokenCommand("echo viafallback"); err != nil || got != "viafallback" {
		t.Fatalf("SHELL-less fallback = (%q, %v)", got, err)
	}
}

// TestStateUpsertLeavesConfigUntouched covers the config/state split's core
// invariant on the `up` path: writing a satellite's registry entry goes to
// the state file ONLY — the user's grove.toml (legacy flat entry, provision
// subtable, comments, other satellites) stays byte-for-byte. The merged view
// then combines both sources per field: the state's machine-derived fields
// win over the stale legacy block, the config's user-authored fields win over
// the state snapshot — and the stale legacy block draws a drift warning.
func TestStateUpsertLeavesConfigUntouched(t *testing.T) {
	configDir := setupGroveHome(t)
	tomlPath := filepath.Join(configDir, "grove.toml")
	original := `# hand-written config
[satellites.sat1]
ssh_addr = "1.1.1.1:22"
user = "old"
host_key = "ssh-ed25519 OLD"

[satellites.sat1.provision]
# provision comment
gh_token_cmd = "gh auth token"
claude = true
service_account_email = "sa@proj.iam.gserviceaccount.com"

[satellites.sat2]
ssh_addr = "2.2.2.2:22"
user = "keep"
host_key = "ssh-ed25519 KEEP"
`
	if err := os.WriteFile(tomlPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	entry := satelliteConfigEntry{
		SSHAddr:    "203.0.113.9:22",
		User:       "solair",
		HostKey:    "ssh-ed25519 NEWKEY",
		SocketPath: "/run/user/1001/grove/groved.sock",
	}
	if err := upsertSatelliteState("sat1", entry); err != nil {
		t.Fatalf("upsertSatelliteState: %v", err)
	}

	// The user's config is untouched byte-for-byte.
	data, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Errorf("state upsert modified grove.toml:\n%s", data)
	}

	// Merged view: state wins the machine-derived fields over the stale
	// legacy block; config wins the user-authored ones; the conflict warns.
	merged, warnings := mergeSatelliteEntries(loadSatellitesViaConfig(t), mustLoadSatelliteState(t))
	got := merged["sat1"]
	if got.SSHAddr != entry.SSHAddr || got.HostKey != entry.HostKey || got.SocketPath != entry.SocketPath {
		t.Fatalf("state fields did not win over the legacy block: %+v", got)
	}
	if got.User != "old" {
		t.Fatalf("config user did not win over the state snapshot: %+v", got)
	}
	if _, ok := merged["sat2"]; !ok {
		t.Fatalf("sat2 lost from merged view: %v", merged)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "[satellites.sat1]") {
		t.Fatalf("expected one stale-legacy-block warning for sat1, got %v", warnings)
	}

	// The provision block still loads alongside.
	prov := loadProvisionViaConfig(t, "sat1")
	if prov.GHTokenCmd != "gh auth token" || !prov.Claude || prov.ServiceAccountEmail != "sa@proj.iam.gserviceaccount.com" {
		t.Fatalf("provision block no longer loads: %+v", prov)
	}
}

// TestSatelliteRegistryIgnoresProvisionSubtable is the daemon-compat gate: the
// daemon's satellite.LoadRegistry decodes [satellites.*] into SatelliteConfig
// (no provision field) through the exact same path used here —
// config.UnmarshalExtension, a mapstructure decoder keyed off yaml tags with
// ErrorUnused left OFF (core/config/types.go) — so an unknown provision
// subtable must be silently ignored, not an error. satelliteConfigEntry
// mirrors SatelliteConfig's yaml tags field-for-field, making this decode
// shape-identical to the daemon's.
func TestSatelliteRegistryIgnoresProvisionSubtable(t *testing.T) {
	configDir := setupGroveHome(t)
	content := `[satellites.sat1]
ssh_addr = "203.0.113.7:22"
user = "solair"
host_key = "ssh-ed25519 AAAA"
identity_file = "/home/u/.ssh/id_ed25519"
socket_path = "/run/user/1001/grove/groved.sock"

[satellites.sat1.provision]
gh_token_cmd = "gh auth token"
claude = true
dotfiles_repo = "https://github.com/me/dotfiles"
service_account_email = "sa@proj.iam.gserviceaccount.com"
`
	if err := os.WriteFile(filepath.Join(configDir, "grove.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// loadSatellitesViaConfig fails the test on any UnmarshalExtension error —
	// which is exactly what a strict daemon-side decode would produce.
	sats := loadSatellitesViaConfig(t)
	want := satelliteConfigEntry{
		SSHAddr:      "203.0.113.7:22",
		User:         "solair",
		HostKey:      "ssh-ed25519 AAAA",
		IdentityFile: "/home/u/.ssh/id_ed25519",
		SocketPath:   "/run/user/1001/grove/groved.sock",
	}
	if got := sats["sat1"]; got != want {
		t.Fatalf("registry entry with provision subtable = %+v, want %+v", got, want)
	}
}
