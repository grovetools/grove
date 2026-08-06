package cmd

// `grove forge status` — what exists, what is exposed, and what to pin.
//
// Two layers, and the split matters:
//
//   - The RECORD: terraform outputs cached at `up` time. Rendered always,
//     needs no network, no credentials, and no terraform binary.
//   - The PROBE: one TLS handshake against the syncd endpoint, which is the
//     only way to learn a self-signed certificate's fingerprint (it is
//     generated on the VM at first boot, so it is in no terraform state and no
//     config file). Opt-out with --no-probe.
//
// The probe deliberately does not verify the certificate: its whole job is to
// report the fingerprint of a certificate no chain would validate, so the
// operator can pin it. Nothing is trusted as a result of this handshake — the
// value is printed, not stored.

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/spf13/cobra"
)

// forgeProbeTimeout bounds the TLS handshake. Status must stay fast enough to
// run reflexively; an unreachable forge is an answer, not a hang.
const forgeProbeTimeout = 5 * time.Second

// forgeStatusReport is the --json shape.
type forgeStatusReport struct {
	// Provisioned is false when no forge has been brought up from this
	// machine. Everything else is then zero, and that is a state rather than
	// an error: the config can describe a forge nobody has created yet.
	Provisioned bool `json:"provisioned"`
	// ConfiguredURL is [forge] url — what the daemon polls. It is reported
	// alongside the provisioned URL precisely so a mismatch is visible.
	ConfiguredURL string       `json:"configured_url,omitempty"`
	Outputs       forgeOutputs `json:"outputs"`
	// TLSFingerprint is the SHA-256 of the leaf certificate the syncd endpoint
	// presented, colon-separated hex. Empty when not probed or unreachable.
	TLSFingerprint string `json:"tls_fingerprint,omitempty"`
	// ProbeError explains an empty fingerprint.
	ProbeError       string `json:"probe_error,omitempty"`
	MeshAddress      string `json:"mesh_address"`
	MeshEndpoint     string `json:"-"`
	MeshPubkey       string `json:"mesh_pubkey"`
	ProbeTarget      string `json:"probe_target"`
	IAPSSHBreakGlass string `json:"iap_ssh_break_glass,omitempty"`
}

func newForgeStatusCmd() *cobra.Command {
	var noProbe bool
	cmd := cli.NewStandardCommand("status", "Show the forge's recorded state, exposed surface and TLS fingerprint")
	cmd.Long = `Report the forge: the terraform outputs recorded at 'up' time, every firewall
rule the module owns, and — for a self-signed deployment — the SHA-256
fingerprint of the certificate the syncd endpoint actually presents, which is
the value to pin on clients.

The fingerprint requires one TLS handshake with the forge (--no-probe skips it).
Everything else is read from local state and works offline.`
	cmd.Args = cobra.NoArgs
	cmd.SilenceUsage = true
	cmd.Flags().BoolVar(&noProbe, "no-probe", false, "Do not contact the forge; report only what is recorded locally")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		report := forgeStatusReport{}

		// A missing [forge] block is not fatal for status: "nothing is
		// configured" is exactly what an operator is asking about.
		var forgeCfg *config.ForgeConfig
		if loaded, err := loadForgeConfig(); err == nil {
			forgeCfg = loaded
			report.ConfiguredURL = strings.TrimSpace(forgeCfg.URL)
			if forgeCfg.Wireguard.IsEnabled() {
				report.MeshAddress = strings.TrimSpace(forgeCfg.Wireguard.Address)
				report.MeshEndpoint = strings.TrimSpace(forgeCfg.Wireguard.Endpoint)
				report.MeshPubkey, _ = cachedForgeWireGuardPubkey()
			}
		}

		outputs, ok, err := loadCachedForgeOutputs()
		if err != nil {
			return err
		}
		report.Provisioned = ok
		report.Outputs = outputs
		if forgeCfg != nil && forgeCfg.Infra != nil && forgeCfg.Infra.EffectiveSSHIngress() == config.ForgeSSHIngressIAP {
			report.IAPSSHBreakGlass = forgeIAPSSHCommand(forgeCfg, outputs)
		}

		report.ProbeTarget = outputs.SyncdAddr
		if forgeCfg != nil && forgeCfg.Wireguard.IsEnabled() {
			_, port, splitErr := net.SplitHostPort(outputs.SyncdAddr)
			if splitErr == nil && forgeMeshIP(forgeCfg) != "" {
				report.ProbeTarget = net.JoinHostPort(forgeMeshIP(forgeCfg), port)
			}
		}
		if ok && !noProbe && report.ProbeTarget != "" {
			fp, perr := probeTLSFingerprint(report.ProbeTarget)
			if perr != nil {
				report.ProbeError = perr.Error()
			} else {
				report.TLSFingerprint = fp
			}
		}

		out := cmd.OutOrStdout()
		// --json is the ecosystem-standard flag cli.NewStandardCommand already
		// registers; adding a second spelling for the same idea would be the
		// kind of inconsistency an operator pays for at 3am.
		if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		}
		renderForgeStatus(out, report)
		return nil
	}
	return cmd
}

func renderForgeStatus(w io.Writer, report forgeStatusReport) {
	if !report.Provisioned {
		fmt.Fprintln(w, "Forge: not provisioned from this machine.")
		if report.ConfiguredURL != "" {
			fmt.Fprintf(w, "  [forge] url = %s  (configured — the daemon may still poll it)\n", report.ConfiguredURL)
		}
		fmt.Fprintln(w, "\nRun `grove forge plan` to see what would be created.")
		return
	}

	fmt.Fprintf(w, "Forge %s (%s)\n", report.Outputs.VMName, report.Outputs.Zone)
	renderForgeOutputs(w, report.Outputs)
	if report.IAPSSHBreakGlass != "" {
		fmt.Fprintf(w, "  break-glass SSH (IAP): %s\n", report.IAPSSHBreakGlass)
	}
	if report.MeshAddress != "" {
		pubkey := report.MeshPubkey
		if pubkey == "" {
			pubkey = "not cached (run `grove forge up`)"
		}
		fmt.Fprintf(w, "  mesh:     %s via %s; pubkey %s; enrolled? see `grove forge wg status`\n", report.MeshAddress, report.MeshEndpoint, pubkey)
	}

	if report.ConfiguredURL != "" && report.Outputs.ForgeURL != "" && report.ConfiguredURL != report.Outputs.ForgeURL {
		// Worth shouting about: the daemon polls the configured URL, so a
		// mismatch means the poller is watching something other than this box.
		fmt.Fprintf(w, "\n  ! [forge] url is %s but this forge answers at %s\n", report.ConfiguredURL, report.Outputs.ForgeURL)
	}

	switch {
	case report.TLSFingerprint != "":
		fmt.Fprintf(w, "\nTLS leaf fingerprint (SHA-256):\n  %s\n", report.TLSFingerprint)
		if report.Outputs.TLSMode == config.ForgeTLSSelfSigned {
			fmt.Fprintln(w, "  Self-signed: pin this value on clients. It changes only if the VM is rebuilt.")
		}
	case report.ProbeError != "":
		fmt.Fprintf(w, "\nTLS: could not probe %s — %s\n", report.ProbeTarget, report.ProbeError)
		if report.MeshAddress != "" {
			fmt.Fprintln(w, "  The mesh or syncd/TLS may be unhealthy; run `grove forge wg status` to distinguish enrollment/hub reachability from service health.")
		}
	}
}

// renderForgeOutputs prints the recorded terraform outputs, including the whole
// ingress surface. Listing every rule is the point: the trial's hardening
// ledger existed because nobody could see, in one place, what was open.
func renderForgeOutputs(w io.Writer, out forgeOutputs) {
	if out.ExternalIP != "" {
		fmt.Fprintf(w, "  address:  %s\n", out.ExternalIP)
	}
	if out.ForgeURL != "" {
		fmt.Fprintf(w, "  forge:    %s\n", out.ForgeURL)
	}
	if out.ForgejoTunnelCmd != "" {
		fmt.Fprintf(w, "  tunnel:   %s\n", out.ForgejoTunnelCmd)
	}
	if out.SyncdAddr != "" {
		fmt.Fprintf(w, "  syncd:    %s (TLS: %s)\n", out.SyncdAddr, out.TLSMode)
	}
	if out.ServiceAccountEmail != "" {
		fmt.Fprintf(w, "  identity: %s\n", out.ServiceAccountEmail)
	}
	if out.SSHCommand != "" {
		fmt.Fprintf(w, "  ssh:      %s\n", out.SSHCommand)
	}
	if len(out.FirewallRules) > 0 {
		fmt.Fprintln(w, "  ingress:")
		for _, r := range out.FirewallRules {
			fmt.Fprintf(w, "    %s\n", r)
		}
	}
}

// probeTLSFingerprint opens one TLS connection and returns the SHA-256
// fingerprint of the leaf certificate, colon-separated uppercase hex — the same
// rendering `openssl x509 -fingerprint -sha256` produces, so the value read
// here and the one written on the VM can be compared by eye.
//
// InsecureSkipVerify is REQUIRED here and is not a weakening: a self-signed
// certificate is unverifiable by construction, and reporting its fingerprint is
// how it becomes pinnable. Nothing is sent over this connection and nothing is
// trusted because of it.
func probeTLSFingerprint(addr string) (string, error) {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return "", fmt.Errorf("%q is not a host:port address", addr)
	}
	dialer := &net.Dialer{Timeout: forgeProbeTimeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, forgeTLSProbeConfig())
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", fmt.Errorf("the endpoint presented no certificate")
	}
	sum := sha256.Sum256(certs[0].Raw)
	return formatFingerprint(sum[:]), nil
}

// forgeTLSProbeConfig pins compact, classical key shares for this bounded
// diagnostic handshake. In particular, the default hybrid ML-KEM key share can
// make the ClientHello too large for constrained WireGuard paths. This is local
// to the fingerprint probe, not a global TLS policy.
func forgeTLSProbeConfig() *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		CurvePreferences:   []tls.CurveID{tls.X25519, tls.CurveP256},
		InsecureSkipVerify: true, //nolint:gosec // G402: the whole purpose is to read an unverifiable cert so it can be pinned
	}
}

// formatFingerprint renders bytes as AA:BB:CC..., matching openssl's output.
func formatFingerprint(sum []byte) string {
	parts := make([]string, 0, len(sum))
	for _, b := range sum {
		parts = append(parts, fmt.Sprintf("%02X", b))
	}
	return strings.Join(parts, ":")
}
