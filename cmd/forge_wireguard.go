package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/spf13/cobra"
)

const (
	forgeWireGuardStateDir               = "/var/lib/grove-forge"
	forgeWireGuardPubkeyName             = "wg-public-key.txt"
	wireGuardUnhealthyAfterSeconds int64 = 5 * 60
)

func forgeMeshIP(cfg *config.ForgeConfig) string {
	if cfg == nil || cfg.Wireguard == nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(cfg.Wireguard.Address, "/", 2)[0])
}

func cachedForgeWireGuardPubkey() (string, error) {
	dir, err := forgeStateDir()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(dir, forgeWireGuardPubkeyName))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func cacheForgeWireGuardPubkey(pubkey string) error {
	dir, err := forgeStateDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, forgeWireGuardPubkeyName), []byte(strings.TrimSpace(pubkey)+"\n"), 0o644)
}

func reconcileForgeWireGuard(w io.Writer, outputs forgeOutputs, forgeCfg *config.ForgeConfig) error {
	if forgeCfg == nil || !forgeCfg.Wireguard.IsEnabled() {
		return nil
	}
	ssh, cleanup, err := forgeSSH(outputs, forgeCfg)
	if err != nil {
		return err
	}
	defer cleanup()

	oldPubkey, err := cachedForgeWireGuardPubkey()
	if err != nil {
		return fmt.Errorf("read cached forge wireguard public key: %w", err)
	}
	status, err := reconcileWireGuard(ssh, *forgeCfg.Wireguard, forgeWireGuardStateDir)
	if err != nil {
		return fmt.Errorf("reconcile forge wireguard: %w", err)
	}
	if oldPubkey != "" && oldPubkey != status.Pubkey {
		fmt.Fprintln(w, "\nThe forge's WireGuard public key changed — the old hub peer is dead.")
		fmt.Fprintln(w, "EVERY HUB CONFIGURATION ENROLLING THE OLD KEY MUST BE UPDATED:")
		fmt.Fprintln(w, "  remove the old peer, enroll the public key below, then re-check `grove forge wg status`.")
	}
	if err := cacheForgeWireGuardPubkey(status.Pubkey); err != nil {
		return fmt.Errorf("cache forge wireguard public key: %w", err)
	}

	fmt.Fprintln(w, "\nWireGuard mesh enrollment:")
	fmt.Fprintf(w, "  public key: %s\n", status.Pubkey)
	fmt.Fprintf(w, "  address:    %s\n", strings.TrimSpace(forgeCfg.Wireguard.Address))
	fmt.Fprintln(w, "  Enroll this public key at the hub for the assigned address; then")
	fmt.Fprintln(w, "  `grove forge wg status` should show a handshake within ~30s.")
	if status.HandshakeAge < 0 {
		fmt.Fprintln(w, "  handshake:  never (expected before hub enrollment)")
	} else {
		fmt.Fprintf(w, "  handshake:  %ds ago\n", status.HandshakeAge)
	}
	fmt.Fprintf(w, "  config changed: %t\n", status.ConfChanged)
	return nil
}

type forgeWGStatusReport struct {
	Pubkey          string `json:"pubkey"`
	Address         string `json:"address"`
	Endpoint        string `json:"endpoint"`
	RuntimeEndpoint string `json:"runtime_endpoint"`
	HandshakeAgeS   int64  `json:"handshake_age_s"`
	Handshake       string `json:"handshake"`
	TransferRXBytes uint64 `json:"transfer_rx_bytes"`
	TransferTXBytes uint64 `json:"transfer_tx_bytes"`
	ConfPresent     bool   `json:"conf_present"`
	ServiceEnabled  bool   `json:"service_enabled"`
	InterfaceActive bool   `json:"interface_active"`
	Unhealthy       bool   `json:"unhealthy"`
	SSHAddress      string `json:"ssh_address"`
}

func newForgeWGCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("wg", "Converge and inspect the forge's WireGuard mesh client")
	cmd.AddCommand(newForgeWGUpCmd())
	cmd.AddCommand(newForgeWGStatusCmd())
	return cmd
}

type forgeWGUpDeps struct {
	loadConfig         func() (*config.ForgeConfig, error)
	loadOutputs        func(string) (forgeOutputs, error)
	reconcileWireGuard func(io.Writer, forgeOutputs, *config.ForgeConfig) error
	reconcileACME      func(io.Writer, forgeOutputs, *config.ForgeConfig) error
	reconcileRootURL   func(io.Writer, forgeOutputs, *config.ForgeConfig) error
	reconcileTLS       func(io.Writer, forgeOutputs, *config.ForgeConfig) error
}

func newForgeWGUpCmd() *cobra.Command {
	return newForgeWGUpCmdWithDeps(forgeWGUpDeps{
		loadConfig:         loadForgeConfig,
		loadOutputs:        loadForgeWGUpOutputs,
		reconcileWireGuard: reconcileForgeWireGuard,
		reconcileACME:      reconcileForgeACME,
		reconcileRootURL:   reconcileForgeRootURL,
		reconcileTLS:       reconcileForgeTLS,
	})
}

func newForgeWGUpCmdWithDeps(deps forgeWGUpDeps) *cobra.Command {
	var tfDir string
	cmd := cli.NewStandardCommand("up", "Converge WireGuard and TLS posture on an existing forge (no Terraform)")
	cmd.Long = `Converge an enabled [forge.wireguard] on an EXISTING forge, then its TLS
posture: with [forge.services] tls_mode = "acme" this obtains/renews the real
certificate over a DNS-01 challenge and serves BOTH services with it (Forgejo
flips to https on :443); otherwise it ensures the self-signed certificate
covers the mesh address.

This command only uses cached outputs (or an existing terraform.tfstate selected
by --tf-dir) to locate the VM, then connects through the forge's pinned SSH path.
It does not extract or render a Terraform module, write tfvars, invoke terraform
or gcloud, or change cloud infrastructure. Use 'grove forge up' to provision a
new forge or intentionally converge infrastructure.`
	cmd.Args = cobra.NoArgs
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&tfDir, "tf-dir", "", "Existing forge Terraform state dir to read without invoking Terraform (default: cached forge outputs)")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		forgeCfg, err := deps.loadConfig()
		if err != nil {
			return err
		}
		if forgeCfg.Wireguard == nil {
			return fmt.Errorf("no [forge.wireguard] block in the grove config — configure and enable it before running `grove forge wg up`")
		}
		if err := forgeCfg.Validate(); err != nil {
			return fmt.Errorf("validate forge configuration: %w", err)
		}
		if !forgeCfg.Wireguard.IsEnabled() {
			return fmt.Errorf("[forge.wireguard] is not enabled with address, endpoint and hub_public_key")
		}
		if forgeCfg.Infra == nil || strings.TrimSpace(forgeCfg.Infra.SSHUser) == "" {
			return fmt.Errorf("[forge.infra] ssh_user is required for the pinned forge SSH connection")
		}
		outputs, err := deps.loadOutputs(tfDir)
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		fmt.Fprintln(out, "Converging WireGuard on the existing forge over pinned SSH.")
		if err := deps.reconcileWireGuard(out, outputs, forgeCfg); err != nil {
			return err
		}
		// Keep the same safe order as forge up: mesh first, then TLS posture
		// (an acme config issues the real certificate and flips Forgejo to
		// https — a no-op otherwise), then point Forgejo's ROOT_URL at the
		// route clients actually use, then widen the self-signed certificate
		// once to cover the newly converged address.
		if err := deps.reconcileACME(out, outputs, forgeCfg); err != nil {
			return err
		}
		if err := deps.reconcileRootURL(out, outputs, forgeCfg); err != nil {
			return err
		}
		if err := deps.reconcileTLS(out, outputs, forgeCfg); err != nil {
			return err
		}
		fmt.Fprintln(out, "\nConvergence complete. Cloud infrastructure was not changed; Terraform and gcloud were not run.")
		return nil
	}
	return cmd
}

// loadForgeWGUpOutputs is deliberately read-only. The normal path consumes the
// outputs cache used by `forge status`; --tf-dir reads an existing cache or
// terraform.tfstate in that state directory without validating/extracting a
// module and without running Terraform.
func loadForgeWGUpOutputs(tfDir string) (forgeOutputs, error) {
	if strings.TrimSpace(tfDir) == "" {
		outputs, ok, err := loadCachedForgeOutputs()
		if err != nil {
			return forgeOutputs{}, fmt.Errorf("read cached forge outputs: %w", err)
		}
		if !ok || outputs.ExternalIP == "" {
			return forgeOutputs{}, fmt.Errorf("forge outputs are not cached — run `grove forge up` first, or pass --tf-dir for an existing forge state")
		}
		return outputs, nil
	}

	dir, err := filepath.Abs(tfDir)
	if err != nil {
		return forgeOutputs{}, fmt.Errorf("resolve --tf-dir: %w", err)
	}
	if raw, readErr := os.ReadFile(filepath.Join(dir, forgeOutputsName)); readErr == nil {
		var outputs forgeOutputs
		if err := json.Unmarshal(raw, &outputs); err != nil {
			return forgeOutputs{}, fmt.Errorf("parse cached forge outputs in %s: %w", dir, err)
		}
		if outputs.ExternalIP == "" {
			return forgeOutputs{}, fmt.Errorf("cached forge outputs in %s have no external_ip", dir)
		}
		return outputs, nil
	} else if !os.IsNotExist(readErr) {
		return forgeOutputs{}, fmt.Errorf("read cached forge outputs in %s: %w", dir, readErr)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "terraform.tfstate"))
	if err != nil {
		return forgeOutputs{}, fmt.Errorf("read existing forge state in %s (no outputs.json or terraform.tfstate): %w", dir, err)
	}
	var state struct {
		Outputs map[string]json.RawMessage `json:"outputs"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return forgeOutputs{}, fmt.Errorf("parse existing forge state in %s: %w", dir, err)
	}
	envelope, err := json.Marshal(state.Outputs)
	if err != nil {
		return forgeOutputs{}, err
	}
	outputs, err := decodeForgeTerraformOutputs(envelope)
	if err != nil {
		return forgeOutputs{}, fmt.Errorf("decode existing forge state outputs in %s: %w", dir, err)
	}
	if outputs.ExternalIP == "" {
		return forgeOutputs{}, fmt.Errorf("terraform state in %s has no external_ip — the forge may not be provisioned", dir)
	}
	return outputs, nil
}

func newForgeWGStatusCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("status", "Show WireGuard enrollment, handshake and transfer state")
	cmd.Args = cobra.NoArgs
	cmd.SilenceUsage = true
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		forgeCfg, err := loadForgeConfig()
		if err != nil {
			return err
		}
		if err := forgeCfg.Validate(); err != nil {
			return err
		}
		if !forgeCfg.Wireguard.IsEnabled() {
			return fmt.Errorf("[forge.wireguard] is not enabled with address, endpoint and hub_public_key")
		}
		outputs, ok, err := loadCachedForgeOutputs()
		if err != nil {
			return err
		}
		if !ok || outputs.ExternalIP == "" {
			return fmt.Errorf("forge outputs are not cached — run `grove forge up` first")
		}
		ssh, cleanup, err := forgeSSH(outputs, forgeCfg)
		if err != nil {
			return err
		}
		defer cleanup()
		raw, err := ssh.outputScript(wireGuardReadOnlyStatusScript(defaultWireGuardPaths(forgeWireGuardStateDir)))
		if err != nil {
			return fmt.Errorf("read forge wireguard status over pinned SSH: %w", err)
		}
		status, err := parseWGShow(raw)
		if err != nil {
			return fmt.Errorf("parse forge wireguard status: %w", err)
		}
		report := forgeWGStatusReport{
			Pubkey:          status.Pubkey,
			Address:         strings.TrimSpace(forgeCfg.Wireguard.Address),
			Endpoint:        strings.TrimSpace(forgeCfg.Wireguard.Endpoint),
			RuntimeEndpoint: status.Endpoint,
			HandshakeAgeS:   status.HandshakeAge,
			TransferRXBytes: status.TransferRXBytes,
			TransferTXBytes: status.TransferTXBytes,
			ConfPresent:     status.ConfPresent,
			ServiceEnabled:  status.ServiceEnabled,
			InterfaceActive: status.InterfaceActive,
			Unhealthy:       status.HandshakeAge < 0 || status.HandshakeAge > wireGuardUnhealthyAfterSeconds,
			SSHAddress:      outputs.ExternalIP + ":22",
		}
		if status.HandshakeAge < 0 {
			report.Handshake = "never"
		} else {
			report.Handshake = fmt.Sprintf("%ds ago", status.HandshakeAge)
		}
		if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		}
		renderForgeWGStatus(cmd.OutOrStdout(), report)
		return nil
	}
	return cmd
}

func renderForgeWGStatus(w io.Writer, report forgeWGStatusReport) {
	fmt.Fprintln(w, "Forge WireGuard")
	fmt.Fprintf(w, "  public key:       %s\n", report.Pubkey)
	fmt.Fprintf(w, "  address:          %s\n", report.Address)
	fmt.Fprintf(w, "  endpoint:         %s", report.Endpoint)
	if report.RuntimeEndpoint != "" && report.RuntimeEndpoint != report.Endpoint {
		fmt.Fprintf(w, " (currently %s)", report.RuntimeEndpoint)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  handshake:        %s", report.Handshake)
	if report.Unhealthy {
		fmt.Fprint(w, "  (unhealthy: no fresh handshake within ~5m; check hub reachability and enrollment)")
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  transfer:         rx=%d bytes tx=%d bytes\n", report.TransferRXBytes, report.TransferTXBytes)
	fmt.Fprintf(w, "  config present:   %t\n", report.ConfPresent)
	fmt.Fprintf(w, "  service enabled:  %t\n", report.ServiceEnabled)
	fmt.Fprintf(w, "  interface active: %t\n", report.InterfaceActive)
	fmt.Fprintf(w, "  diagnostic SSH:   %s (external pinned path; mesh cutover is a later phase)\n", report.SSHAddress)
}

func wireGuardReadOnlyStatusScript(paths wireGuardPaths) string {
	return strings.Join([]string{
		"set -u",
		"PUBKEY=''",
		"if sudo test -f " + shellQuote(paths.StateDir+"/wg-public-key.txt") + "; then PUBKEY=$(sudo cat " + shellQuote(paths.StateDir+"/wg-public-key.txt") + "); fi",
		"CONF_PRESENT=0; sudo test -f " + shellQuote(paths.Config) + " && CONF_PRESENT=1",
		"SERVICE_ENABLED=0; sudo systemctl is-enabled --quiet wg-quick@" + paths.InterfaceName + ".service && SERVICE_ENABLED=1",
		"INTERFACE_ACTIVE=0; sudo systemctl is-active --quiet wg-quick@" + paths.InterfaceName + ".service && INTERFACE_ACTIVE=1",
		"ENDPOINT=''; LATEST=0; RX=0; TX=0",
		"if [ \"$INTERFACE_ACTIVE\" = 1 ]; then",
		"  [ -n \"$PUBKEY\" ] || PUBKEY=$(sudo wg show " + paths.InterfaceName + " public-key 2>/dev/null || true)",
		"  ENDPOINT=$(sudo wg show " + paths.InterfaceName + " endpoints 2>/dev/null | awk 'NR==1 {print $2}')",
		"  LATEST=$(sudo wg show " + paths.InterfaceName + " latest-handshakes 2>/dev/null | awk 'NR==1 {print $2}')",
		"  set -- $(sudo wg show " + paths.InterfaceName + " transfer 2>/dev/null | awk 'NR==1 {print $2, $3}')",
		"  RX=${1:-0}; TX=${2:-0}",
		"fi",
		"NOW=$(date +%s)",
		"if [ -z \"${LATEST:-}\" ] || [ \"$LATEST\" = 0 ]; then AGE=never; else AGE=$((NOW-LATEST)); [ \"$AGE\" -lt 0 ] && AGE=0; fi",
		"printf 'PUBKEY=%s\\n' \"$PUBKEY\"",
		"printf 'CONF_CHANGED=0\\n'",
		"printf 'HANDSHAKE_AGE_S=%s\\n' \"$AGE\"",
		"printf 'ENDPOINT=%s\\n' \"$ENDPOINT\"",
		"printf 'TRANSFER_RX_BYTES=%s\\n' \"$RX\"",
		"printf 'TRANSFER_TX_BYTES=%s\\n' \"$TX\"",
		"printf 'CONF_PRESENT=%s\\n' \"$CONF_PRESENT\"",
		"printf 'SERVICE_ENABLED=%s\\n' \"$SERVICE_ENABLED\"",
		"printf 'INTERFACE_ACTIVE=%s\\n' \"$INTERFACE_ACTIVE\"",
	}, "\n") + "\n"
}
