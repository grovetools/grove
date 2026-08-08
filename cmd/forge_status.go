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
	// EffectiveForgeURL is where THIS config says the forge answers, derived
	// exactly as the ROOT_URL reconcile derives what it writes on the VM.
	//
	// Outputs.ForgeURL cannot serve that purpose: it is a terraform output
	// cached at `up` time, and on the terraform-free pet path terraform never
	// runs again, so it keeps describing whatever era it was written in while
	// every converge changes the box underneath it. Reporting the record as
	// though it were the answer produced a `!` mismatch warning against
	// correct config after the acme cutover.
	EffectiveForgeURL string `json:"effective_forge_url,omitempty"`
	// TLSFingerprint is the SHA-256 of the leaf certificate the syncd endpoint
	// presented, colon-separated hex. Empty when not probed or unreachable.
	TLSFingerprint string `json:"tls_fingerprint,omitempty"`
	// TLSModeConfigured is [forge.services]' EFFECTIVE tls_mode. Reported next
	// to the recorded outputs' mode because the pet converges terraform-free:
	// the cached outputs can say self-signed long after an acme cutover.
	TLSModeConfigured string `json:"tls_mode_configured,omitempty"`
	// Leaf-certificate posture from the probe: what it names, when it dies,
	// and whether it is self-issued (issuer == subject).
	TLSSANs       []string `json:"tls_sans,omitempty"`
	TLSNotAfter   string   `json:"tls_not_after,omitempty"`
	TLSDaysLeft   int      `json:"tls_days_left,omitempty"`
	TLSSelfSigned bool     `json:"tls_self_signed,omitempty"`
	// ACMETimer is the renewal timer's health, read over pinned SSH in acme
	// mode. Silent renewal death is the classic failure mode of these setups,
	// so an unreadable or failing timer is rendered loudly.
	ACMETimer *forgeACMETimerStatus `json:"acme_timer,omitempty"`
	// ProbeError explains an empty fingerprint.
	ProbeError       string `json:"probe_error,omitempty"`
	MeshAddress      string `json:"mesh_address"`
	MeshEndpoint     string `json:"-"`
	MeshPubkey       string `json:"mesh_pubkey"`
	ProbeTarget      string `json:"probe_target"`
	IAPSSHBreakGlass string `json:"iap_ssh_break_glass,omitempty"`
}

// forgeACMETimerStatus is the renewal timer's health as systemd reports it.
type forgeACMETimerStatus struct {
	Enabled    bool   `json:"enabled"`
	Active     bool   `json:"active"`
	LastResult string `json:"last_result,omitempty"`
	LastRun    string `json:"last_run,omitempty"`
	NextRun    string `json:"next_run,omitempty"`
	Err        string `json:"error,omitempty"`
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
		serverName := ""
		if forgeCfg != nil && forgeCfg.Wireguard.IsEnabled() {
			_, port, splitErr := net.SplitHostPort(outputs.SyncdAddr)
			if splitErr == nil && forgeMeshIP(forgeCfg) != "" {
				report.ProbeTarget = net.JoinHostPort(forgeMeshIP(forgeCfg), port)
			}
		}
		if forgeCfg != nil {
			report.EffectiveForgeURL = forgeMeshRootURL(forgeCfg)
			report.TLSModeConfigured = forgeCfg.Services.EffectiveTLSMode()
			if forgeACMEEnabled(forgeCfg) {
				// Probe by NAME in acme mode: it exercises the exact
				// DNS + SNI + chain a default client uses.
				serverName = forgeCfg.Services.EffectiveDomain()
				report.ProbeTarget = net.JoinHostPort(serverName,
					fmt.Sprint(forgeCfg.Services.EffectiveSyncdPort()))
			}
		}
		if ok && !noProbe && report.ProbeTarget != "" {
			leaf, perr := probeTLSLeaf(report.ProbeTarget, serverName)
			if perr != nil {
				report.ProbeError = perr.Error()
			} else {
				report.TLSFingerprint = leaf.Fingerprint
				report.TLSSANs = leaf.SANs
				report.TLSNotAfter = leaf.NotAfter.Format("2006-01-02")
				report.TLSDaysLeft = forgeCertDaysLeft(leaf.NotAfter, time.Now())
				report.TLSSelfSigned = leaf.SelfSigned
			}
		}
		if ok && !noProbe && forgeCfg != nil && forgeACMEEnabled(forgeCfg) {
			report.ACMETimer = readForgeACMETimerStatus(outputs, forgeCfg)
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
	renderForgeOutputs(w, report.Outputs, forgeOutputOverlay{
		ForgeURL: report.EffectiveForgeURL,
		TLSMode:  report.TLSModeConfigured,
	})
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

	// Worth shouting about: the daemon polls the configured URL, so a real
	// mismatch means the poller is watching something other than this box. But
	// it is compared against the DERIVED answer, never the cached output — a
	// warning that fires on correct config is worse than no warning, because it
	// teaches the operator to ignore this surface, whose entire job is to be
	// loud about silent renewal death.
	//
	// The cached output is still the fallback when nothing can be derived: a
	// deployment with neither acme nor a mesh has no pet converge that moves
	// the URL, so there the record has not gone stale.
	answersAt := report.EffectiveForgeURL
	if answersAt == "" {
		answersAt = report.Outputs.ForgeURL
	}
	if answersAt != "" && report.ConfiguredURL != "" &&
		!sameForgeOrigin(report.ConfiguredURL, answersAt) {
		fmt.Fprintf(w, "\n  ! [forge] url is %s but this forge answers at %s\n", report.ConfiguredURL, answersAt)
	}

	switch {
	case report.TLSFingerprint != "":
		acme := report.TLSModeConfigured == config.ForgeTLSACME
		if report.TLSModeConfigured != "" {
			fmt.Fprintf(w, "\nTLS: %s", report.TLSModeConfigured)
			if report.TLSNotAfter != "" {
				fmt.Fprintf(w, ", expires %s (%d days)", report.TLSNotAfter, report.TLSDaysLeft)
			}
			fmt.Fprintln(w)
			if len(report.TLSSANs) > 0 {
				fmt.Fprintf(w, "  SANs: %s\n", strings.Join(report.TLSSANs, ", "))
			}
			switch {
			case acme && report.TLSSelfSigned:
				fmt.Fprintln(w, "  !! acme is configured but the served certificate is SELF-ISSUED — run `grove forge wg up` to converge")
			case acme && report.TLSDaysLeft < 21:
				fmt.Fprintf(w, "  !! %d days to expiry and lego renews at <30 — the renewal timer is NOT keeping up; check it now\n", report.TLSDaysLeft)
			}
		}
		fmt.Fprintf(w, "  leaf fingerprint (SHA-256): %s\n", report.TLSFingerprint)
		if report.Outputs.TLSMode == config.ForgeTLSSelfSigned && !acme {
			fmt.Fprintln(w, "  Self-signed: pin this value on clients. It changes only if the VM is rebuilt.")
		}
	case report.ProbeError != "":
		fmt.Fprintf(w, "\nTLS: could not probe %s — %s\n", report.ProbeTarget, report.ProbeError)
		if report.MeshAddress != "" {
			fmt.Fprintln(w, "  The mesh or syncd/TLS may be unhealthy; run `grove forge wg status` to distinguish enrollment/hub reachability from service health.")
		}
	}

	if t := report.ACMETimer; t != nil {
		switch {
		case t.Err != "":
			fmt.Fprintf(w, "  !! renewal timer unreadable over SSH — silent renewal death is the classic failure mode; check it by hand: %s\n", t.Err)
		case !t.Enabled || !t.Active:
			fmt.Fprintf(w, "  !! renewal timer grove-forge-acme.timer is NOT running (enabled=%t active=%t) — certificates will silently expire\n", t.Enabled, t.Active)
		case t.LastResult != "" && t.LastResult != "success":
			fmt.Fprintf(w, "  !! last renewal run FAILED (Result=%s) — inspect `journalctl -u grove-forge-acme` on the VM\n", t.LastResult)
		default:
			next := t.NextRun
			if next == "" {
				next = "unknown"
			}
			fmt.Fprintf(w, "  renewal timer: active, last result %q, next run %s\n", orDash(t.LastResult), next)
		}
	}
}

// sameForgeOrigin compares two forge URLs for the purpose of the mismatch
// warning. A trailing slash is not a difference the daemon's poller can see,
// so it must not be one the operator gets shouted at about.
func sameForgeOrigin(a, b string) bool {
	return strings.TrimSuffix(strings.TrimSpace(a), "/") ==
		strings.TrimSuffix(strings.TrimSpace(b), "/")
}

// staleRecordNote renders `record: <cached>` when the value being printed
// disagrees with what terraform recorded, and nothing when they agree. Callers
// place it; only the disagreement is decided here.
func staleRecordNote(shown, recorded string) string {
	if recorded == "" || shown == recorded {
		return ""
	}
	return "record: " + recorded
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// forgeOutputOverlay is what the caller knows that the cached record does not.
// A zero overlay renders the record verbatim, which is what `forge up` wants:
// its outputs came out of the terraform run that just finished, so there is
// nothing fresher to prefer.
type forgeOutputOverlay struct {
	// ForgeURL is the URL derived from live config, if one can be derived.
	ForgeURL string
	// TLSMode is [forge.services]' effective mode.
	TLSMode string
}

// renderForgeOutputs prints the recorded terraform outputs, including the whole
// ingress surface. Listing every rule is the point: the trial's hardening
// ledger existed because nobody could see, in one place, what was open.
//
// Where the overlay disagrees with the record, the overlay is printed and the
// record is shown beside it as `(record: …)` rather than dropped: the
// disagreement is itself the useful signal — it says the cached outputs have
// gone stale under a terraform-free converge.
func renderForgeOutputs(w io.Writer, out forgeOutputs, overlay forgeOutputOverlay) {
	if out.ExternalIP != "" {
		fmt.Fprintf(w, "  address:  %s\n", out.ExternalIP)
	}
	if url := firstNonEmpty(overlay.ForgeURL, out.ForgeURL); url != "" {
		if note := staleRecordNote(url, out.ForgeURL); note != "" {
			fmt.Fprintf(w, "  forge:    %s  (%s)\n", url, note)
		} else {
			fmt.Fprintf(w, "  forge:    %s\n", url)
		}
	}
	if out.ForgejoTunnelCmd != "" {
		fmt.Fprintf(w, "  tunnel:   %s\n", out.ForgejoTunnelCmd)
	}
	if out.SyncdAddr != "" {
		mode := firstNonEmpty(overlay.TLSMode, out.TLSMode)
		if note := staleRecordNote(mode, out.TLSMode); note != "" {
			fmt.Fprintf(w, "  syncd:    %s (TLS: %s; %s)\n", out.SyncdAddr, mode, note)
		} else {
			fmt.Fprintf(w, "  syncd:    %s (TLS: %s)\n", out.SyncdAddr, mode)
		}
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
	leaf, err := probeTLSLeaf(addr, "")
	if err != nil {
		return "", err
	}
	return leaf.Fingerprint, nil
}

// forgeTLSLeafDetails is what one probe handshake learns about the leaf.
type forgeTLSLeafDetails struct {
	Fingerprint string
	NotAfter    time.Time
	SANs        []string
	SelfSigned  bool
}

// probeTLSLeaf opens one TLS connection and reads the leaf certificate's
// pinning fingerprint plus the posture `status` renders: SANs, expiry, and
// whether it is self-issued. serverName sets SNI (acme mode probes by name);
// empty means no SNI, matching the pre-acme probe.
func probeTLSLeaf(addr, serverName string) (*forgeTLSLeafDetails, error) {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return nil, fmt.Errorf("%q is not a host:port address", addr)
	}
	cfg := forgeTLSProbeConfig()
	cfg.ServerName = serverName
	dialer := &net.Dialer{Timeout: forgeProbeTimeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, fmt.Errorf("the endpoint presented no certificate")
	}
	leaf := certs[0]
	sum := sha256.Sum256(leaf.Raw)
	details := &forgeTLSLeafDetails{
		Fingerprint: formatFingerprint(sum[:]),
		NotAfter:    leaf.NotAfter,
		SelfSigned:  leaf.Issuer.String() == leaf.Subject.String(),
	}
	for _, name := range leaf.DNSNames {
		details.SANs = append(details.SANs, "DNS:"+name)
	}
	for _, ip := range leaf.IPAddresses {
		details.SANs = append(details.SANs, "IP:"+ip.String())
	}
	return details, nil
}

// readForgeACMETimerStatus reads the renewal timer's health over pinned SSH.
// Failure to read is a REPORT, not an error: status must render what it can,
// and "could not read the timer" is itself the loud signal.
func readForgeACMETimerStatus(outputs forgeOutputs, forgeCfg *config.ForgeConfig) *forgeACMETimerStatus {
	ssh, cleanup, err := forgeSSH(outputs, forgeCfg)
	if err != nil {
		return &forgeACMETimerStatus{Err: err.Error()}
	}
	defer cleanup()
	out, err := ssh.outputScript(forgeACMETimerStatusScript())
	if err != nil {
		return &forgeACMETimerStatus{Err: err.Error()}
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = strings.TrimSpace(value)
		}
	}
	return &forgeACMETimerStatus{
		Enabled:    values["TIMER_ENABLED"] == "1",
		Active:     values["TIMER_ACTIVE"] == "1",
		LastResult: values["LAST_RESULT"],
		LastRun:    values["LAST_RUN"],
		NextRun:    values["NEXT_RUN"],
	}
}

func forgeACMETimerStatusScript() string {
	return strings.Join([]string{
		"set -u",
		"TIMER_ENABLED=0; sudo systemctl is-enabled --quiet grove-forge-acme.timer 2>/dev/null && TIMER_ENABLED=1",
		"TIMER_ACTIVE=0; sudo systemctl is-active --quiet grove-forge-acme.timer 2>/dev/null && TIMER_ACTIVE=1",
		"LAST_RESULT=$(sudo systemctl show grove-forge-acme.service -p Result --value 2>/dev/null || true)",
		"LAST_RUN=$(sudo systemctl show grove-forge-acme.timer -p LastTriggerUSec --value 2>/dev/null || true)",
		"NEXT_RUN=$(sudo systemctl show grove-forge-acme.timer -p NextElapseUSecRealtime --value 2>/dev/null || true)",
		"printf 'TIMER_ENABLED=%s\\n' \"$TIMER_ENABLED\"",
		"printf 'TIMER_ACTIVE=%s\\n' \"$TIMER_ACTIVE\"",
		"printf 'LAST_RESULT=%s\\n' \"$LAST_RESULT\"",
		"printf 'LAST_RUN=%s\\n' \"$LAST_RUN\"",
		"printf 'NEXT_RUN=%s\\n' \"$NEXT_RUN\"",
	}, "\n") + "\n"
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
