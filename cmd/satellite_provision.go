package cmd

// Provisioning options for `grove satellite up` — the [satellites.<name>.provision]
// config block, its flag overrides, local token-command execution, and the
// stdin-framing assembly for cloud/poc/grove-satellite/bootstrap/
// satellite-bootstrap.sh.
//
// Secrets policy (mirrors the bootstrap script header): tokens are produced by
// LOCAL commands (`gh_token_cmd`, `claude_token_cmd`), travel to the script on
// stdin, and never appear in argv. Framing contract:
//   exactly one of --gh-token-stdin / --claude-token-stdin:
//       stdin is that raw token (first line).
//   both flags together:
//       stdin is order-free KEY=VALUE lines (GH_TOKEN=..., CLAUDE_CODE_OAUTH_TOKEN=...).

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/grovetools/core/config"
)

// satelliteProvisionConfig is the [satellites.<name>.provision] table. It is a
// grove-CLI-only input to `up`; the daemon's SatelliteConfig does not know the
// key, and its mapstructure decode (config.UnmarshalExtension, no ErrorUnused)
// ignores unknown keys, so the subtable rides alongside the registry entry
// without breaking daemon-side LoadRegistry (verified by
// TestSatelliteRegistryIgnoresProvisionSubtable).
type satelliteProvisionConfig struct {
	// GHTokenCmd is a local command whose stdout (trimmed) is the GitHub token
	// piped to bootstrap --gh-token-stdin (e.g. "gh auth token").
	GHTokenCmd string `yaml:"gh_token_cmd"`
	// Claude installs Claude Code on the VM (bootstrap --claude).
	Claude bool `yaml:"claude"`
	// ClaudeTokenCmd is a local command producing CLAUDE_CODE_OAUTH_TOKEN
	// (mint once with `claude setup-token`); implies Claude, matching the
	// script's --claude-token-stdin semantics.
	ClaudeTokenCmd string `yaml:"claude_token_cmd"`
	// DotfilesRepo is cloned on the VM and its install.sh run (best-effort;
	// bootstrap --dotfiles-repo).
	DotfilesRepo string `yaml:"dotfiles_repo"`
	// ServiceAccountEmail is attached to the VM at apply time (terraform var
	// service_account_email; scopes stay the terraform default).
	ServiceAccountEmail string `yaml:"service_account_email"`
}

// loadSatelliteProvision reads [satellites.<name>.provision] from the same
// layered grove config the registry lives in. A missing satellite or missing
// provision subtable yields the zero value; a malformed one is an error (the
// user asked for provisioning inputs — do not silently drop them).
func loadSatelliteProvision(name string) (satelliteProvisionConfig, error) {
	cfg, err := config.LoadDefault()
	if err != nil {
		return satelliteProvisionConfig{}, fmt.Errorf("load grove config: %w", err)
	}
	return satelliteProvisionFromConfig(cfg, name)
}

// satelliteProvisionFromConfig decodes only the provision subtables out of the
// [satellites.*] extension. Deliberately a separate decode from
// satelliteConfigEntry so the registry entry struct keeps mirroring the
// daemon's SatelliteConfig field-for-field.
func satelliteProvisionFromConfig(cfg *config.Config, name string) (satelliteProvisionConfig, error) {
	var raw map[string]struct {
		Provision satelliteProvisionConfig `yaml:"provision"`
	}
	if err := cfg.UnmarshalExtension("satellites", &raw); err != nil {
		return satelliteProvisionConfig{}, fmt.Errorf("parse [satellites.%s.provision]: %w", name, err)
	}
	return raw[name].Provision, nil
}

// provisionFlagOverrides carries the `up` flag values plus whether each flag
// was actually set (cobra Changed) — a set flag always wins over the config
// block, including set-to-empty (which disables the config value).
type provisionFlagOverrides struct {
	GHTokenCmd     string
	GHTokenCmdSet  bool
	Claude         bool
	ClaudeSet      bool
	ClaudeTokenCmd string
	ClaudeTokenSet bool
	DotfilesRepo   string
	DotfilesSet    bool
	ServiceAccount string
	ServiceAccSet  bool
}

// mergeProvision resolves flag-over-config precedence and normalizes the
// implication a Claude token carries: token ⇒ install (the script's
// --claude-token-stdin implies --claude).
func mergeProvision(cfg satelliteProvisionConfig, f provisionFlagOverrides) satelliteProvisionConfig {
	out := cfg
	if f.GHTokenCmdSet {
		out.GHTokenCmd = f.GHTokenCmd
	}
	if f.ClaudeSet {
		out.Claude = f.Claude
	}
	if f.ClaudeTokenSet {
		out.ClaudeTokenCmd = f.ClaudeTokenCmd
	}
	if f.DotfilesSet {
		out.DotfilesRepo = f.DotfilesRepo
	}
	if f.ServiceAccSet {
		out.ServiceAccountEmail = f.ServiceAccount
	}
	if out.ClaudeTokenCmd != "" {
		out.Claude = true
	}
	return out
}

// runTokenCommand executes a token-producing command through the user's shell
// ($SHELL, falling back to /bin/sh) and returns its trimmed stdout. Stderr
// passes through to the terminal (auth helpers prompt there). The result must
// be a non-empty single line: both stdin framings are line-oriented, so a
// multi-line "token" would silently truncate — hard error instead.
func runTokenCommand(command string) (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-c", command) //nolint:gosec // G204: user-authored config/flag command, run locally on purpose
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("token command %q failed: %w", command, err)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", fmt.Errorf("token command %q produced no output", command)
	}
	if strings.ContainsAny(token, "\r\n") {
		return "", fmt.Errorf("token command %q produced multiple lines; the bootstrap stdin framing is line-oriented and needs a single-line token", command)
	}
	return token, nil
}

// buildBootstrapProvision assembles the extra satellite-bootstrap.sh argv flags
// and the secrets stdin payload per the script's documented framing: two tokens
// → self-describing KEY=VALUE lines; exactly one → the raw token line; none →
// empty (bootstrap then reads nothing from stdin). Tokens go ONLY into the
// returned stdin string, never into args.
func buildBootstrapProvision(opts satelliteProvisionConfig, ghToken, claudeToken string) (args []string, stdin string) {
	switch {
	case ghToken != "" && claudeToken != "":
		args = append(args, "--gh-token-stdin", "--claude-token-stdin")
		stdin = fmt.Sprintf("GH_TOKEN=%s\nCLAUDE_CODE_OAUTH_TOKEN=%s\n", ghToken, claudeToken)
	case ghToken != "":
		args = append(args, "--gh-token-stdin")
		stdin = ghToken + "\n"
	case claudeToken != "":
		args = append(args, "--claude-token-stdin")
		stdin = claudeToken + "\n"
	}
	// --claude-token-stdin already implies --claude in the script; only the
	// token-less install needs the explicit flag.
	if opts.Claude && claudeToken == "" {
		args = append(args, "--claude")
	}
	if opts.DotfilesRepo != "" {
		args = append(args, "--dotfiles-repo", opts.DotfilesRepo)
	}
	return args, stdin
}

// resolveProvisionTokens runs the configured token commands locally (fail-fast:
// callers invoke this BEFORE terraform so a broken token command costs nothing).
func resolveProvisionTokens(opts satelliteProvisionConfig) (ghToken, claudeToken string, err error) {
	if opts.GHTokenCmd != "" {
		fmt.Printf("Resolving GitHub token: %s\n", opts.GHTokenCmd)
		if ghToken, err = runTokenCommand(opts.GHTokenCmd); err != nil {
			return "", "", err
		}
	}
	if opts.ClaudeTokenCmd != "" {
		fmt.Printf("Resolving Claude Code token: %s\n", opts.ClaudeTokenCmd)
		if claudeToken, err = runTokenCommand(opts.ClaudeTokenCmd); err != nil {
			return "", "", err
		}
	}
	return ghToken, claudeToken, nil
}
