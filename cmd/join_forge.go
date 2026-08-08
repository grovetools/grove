package cmd

// The two things `grove join` asks a configured forge for: where its syncd
// answers, and — with --mint — a token for this machine.
//
// LAYERING. `grove forge token create` is the primitive: forge-specific, needs
// IAP, performs a server-side operation, and lives in the
// grove-plugin-forge-gcp recipe. `grove join --mint` is the composition, and it
// composes by DELEGATION rather than by import — grove core does not know about
// GCP, terraform or IAP, and syncd exists perfectly well without a forge.
//
// The delegation is also what keeps custody honest: the recipe mints the token
// and puts it straight into the keychain, and what comes back over the pipe is
// a RECEIPT (account, service, hash prefix) with no secret in it. grove then
// resolves the credential the same way the daemon later will — by running the
// token_command — so what join verifies is what groved will present.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/paths"
)

// defaultSyncdPort is the grove-syncd bind port assumed when [forge.services]
// declares none. It MUST track forgecfg.DefaultSyncdPort in
// grove-plugin-forge-gcp (and the satellite bootstrap's own default); the two
// cannot be shared by import, because core must not depend on a recipe.
const defaultSyncdPort = 8788

// forgeSyncdEndpoint is the SUBSET of the recipe's [forge.services] block that
// says where syncd answers. It decodes from the same [forge] extension
// namespace the recipe uses, with its own minimal types: unknown keys are
// ignored by UnmarshalExtension, which is exactly what lets both halves read
// one block (see core/config/forge.go).
type forgeSyncdEndpoint struct {
	Services struct {
		Domain string `yaml:"domain"`
		Syncd  *struct {
			Enabled *bool `yaml:"enabled"`
			Port    int   `yaml:"port"`
		} `yaml:"syncd"`
	} `yaml:"services"`
}

// deriveForgeSyncServer computes the syncd base URL from [forge.services]
// domain + [forge.services.syncd] port, and says which keys it used.
//
// DERIVE, DON'T DEMAND: the operator who ran `grove forge up` has already told
// grove where the forge is. Making them retype it as an argument is asking for
// input the tool already holds — and it is one more place for the two spellings
// to disagree.
func deriveForgeSyncServer() (server, provenance string, err error) {
	cfg, lerr := config.LoadDefault()
	if lerr != nil {
		return "", "", fmt.Errorf("failed to load grove config: %w", lerr)
	}
	if cfg == nil || cfg.Extensions == nil {
		return "", "", errNoForgeToDeriveFrom
	}
	if _, ok := cfg.Extensions[config.ForgeExtensionKey]; !ok {
		return "", "", errNoForgeToDeriveFrom
	}
	var endpoint forgeSyncdEndpoint
	if derr := cfg.UnmarshalExtension(config.ForgeExtensionKey, &endpoint); derr != nil {
		return "", "", fmt.Errorf("failed to decode [forge.services]: %w", derr)
	}
	if endpoint.Services.Syncd != nil && endpoint.Services.Syncd.Enabled != nil && !*endpoint.Services.Syncd.Enabled {
		return "", "", fmt.Errorf("[forge.services.syncd] enabled = false — this forge does not colocate grove-syncd, so there is no server to join; pass the URL of the syncd you mean")
	}
	domain := strings.ToLower(strings.TrimSpace(endpoint.Services.Domain))
	if domain == "" {
		return "", "", fmt.Errorf("[forge.services] declares no domain, so the syncd URL cannot be derived (a domain-less forge is IAP-tunnel-only) — pass the server URL explicitly")
	}
	port := defaultSyncdPort
	if endpoint.Services.Syncd != nil && endpoint.Services.Syncd.Port > 0 {
		port = endpoint.Services.Syncd.Port
	}
	return fmt.Sprintf("https://%s:%d", domain, port), "derived: forge.services domain + syncd.port", nil
}

// errNoForgeToDeriveFrom is the "nothing to derive from" case, distinguished so
// the caller can name BOTH remedies rather than only the one it guessed at.
var errNoForgeToDeriveFrom = fmt.Errorf("no [forge] block in the grove config")

// mintReceipt is what `grove forge token create --json` hands back. Its
// defining property is what it does NOT carry: the token itself never crosses
// this boundary. grove learns where the secret was put, and reads it back the
// way the daemon will.
type mintReceipt struct {
	// Stored names the store the token landed in ("keychain", "stdout").
	Stored string `json:"stored"`
	// Service and Account address the keychain item.
	Service string `json:"service"`
	Account string `json:"account"`
	// Description is the label the server holds for this token, which is what
	// a later revocation is keyed by.
	Description string `json:"description"`
	// HashPrefix is the server-side identifier — enough to find the token in
	// `grove forge token list`, useless as a credential.
	HashPrefix string `json:"hash_prefix"`
	// TokenCommand is the command that reads the secret back out of the store.
	// It is what gets written into sync.toml.
	TokenCommand string `json:"token_command"`
}

// mintForgeToken delegates to the forge recipe's `token create`.
//
// stdout is captured for the JSON receipt; stderr is INHERITED, so the
// recipe's own progress ("minting on grove-services-1 over IAP…") reaches the
// operator live rather than being buffered into an error string. The recipe
// prints no secret on either stream in keychain mode — that is its contract,
// and the receipt below is the proof it kept it.
func mintForgeToken(ctx context.Context, machineName string) (mintReceipt, error) {
	bin, err := resolveForgeToolBinary()
	if err != nil {
		return mintReceipt{}, err
	}
	args := []string{"token", "create", machineName, "--store", "keychain", "--json"}
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // G204: bin is a resolved managed binary, args are internal
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		// Deliberately no wrapped stdout: a mint that failed halfway may have
		// printed a token, and an error string is the one place a secret must
		// never reach (the daemon-side custody rule, same direction).
		return mintReceipt{}, fmt.Errorf("`forge token create %s` failed (%s) — run it directly to see why", machineName, exitStatusOf(err))
	}
	var receipt mintReceipt
	if jerr := json.Unmarshal(out, &receipt); jerr != nil {
		return mintReceipt{}, fmt.Errorf("`forge token create --json` did not print a receipt this grove understands (is the forge plugin current?): %w", jerr)
	}
	if strings.TrimSpace(receipt.TokenCommand) == "" {
		return mintReceipt{}, fmt.Errorf("`forge token create --json` returned no token_command, so there is nothing to write into sync.toml")
	}
	return receipt, nil
}

// resolveForgeToolBinary finds the `forge` recipe binary the same way grove's
// own delegation does: the managed bin dir first, then the tool-plugin
// lockfile, then PATH.
func resolveForgeToolBinary() (string, error) {
	if dir := paths.BinDir(); dir != "" {
		candidate := filepath.Join(dir, "forge")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	if pluginBinary, err := toolPluginBinary("forge"); err == nil && pluginBinary != "" {
		return pluginBinary, nil
	}
	if found, err := exec.LookPath("forge"); err == nil {
		return found, nil
	}
	return "", fmt.Errorf("--mint needs the forge recipe (`grove forge`), which is not installed on this machine — install grove-plugin-forge-gcp, or mint the token yourself and pass --token-command")
}

// exitStatusOf renders a failure without carrying the child's output, matching
// the daemon-side token resolver's rule.
func exitStatusOf(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Sprintf("exit status %d", ee.ExitCode())
	}
	return "could not run"
}
