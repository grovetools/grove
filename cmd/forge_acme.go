package cmd

// ACME (Let's Encrypt, DNS-01) for the forge — the terraform-free converge
// path and its credentials helper.
//
// The terraform modules can provision a fresh acme forge, but the LIVE forge
// is a pet converged over pinned SSH (the jobs 29-42 precedent: WireGuard,
// ROOT_URL). This file is that same discipline for TLS posture: verify first
// (join-style preflight), then converge idempotently, and leave the
// self-signed deployment fully working when any step fails — never a
// half-cutover. The semantics here are kept in lockstep with
// forgeassets/terraform/modules/{forgejo,syncd}; the templates are what a
// FRESH provision runs, this is what the existing pet runs.
//
// Secrets never ride through terraform. The DNS provider credential (a
// service-account key for lego's DNS-01 challenge) is shipped by
// `grove forge acme install-credentials` over the pinned SSH connection into
// a 0600 root file — the same route the syncd binary and the backup ntfy
// topic already travel.

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/spf13/cobra"
)

const (
	// On-VM paths, shared with the terraform templates.
	forgeACMEEnvPath      = "/etc/grove-forge/acme.env"
	forgeACMESAKeyPath    = "/etc/grove-forge/acme-dns-sa.json"
	forgeACMEDefaultsPath = "/etc/grove-forge/acme.defaults"
	forgeACMERenewPath    = "/usr/local/sbin/grove-forge-acme"
	forgeTLSDir           = "/etc/grove-forge/tls"

	// forgeTLSGroup matches the terraform modules' tls_group default: the
	// group that may read the shared private key (grove-syncd's DynamicUser
	// and Forgejo's service user both join it).
	forgeTLSGroup = "grove-tls"

	// forgeLegoVersion pins the lego release, matching the syncd module.
	forgeLegoVersion = "v4.17.4"

	// forgeForgejoHTTPSPort is where Forgejo serves https in acme mode. 443,
	// not the configured http_port: ROOT_URL must be a bare https://domain/
	// (what browsers and clone URLs use), and the unit grants exactly
	// CAP_NET_BIND_SERVICE to make that bindable. Recorded as the port story
	// in the certs runbook.
	forgeForgejoHTTPSPort = 443
)

// forgeACMEEnabled reports whether [forge.services] declares a usable acme
// posture (tls_mode = acme AND a domain — EffectiveTLSMode already degrades a
// domainless acme to self-signed).
func forgeACMEEnabled(cfg *config.ForgeConfig) bool {
	return cfg != nil && cfg.Services != nil &&
		cfg.Services.EffectiveTLSMode() == config.ForgeTLSACME
}

// ---- command surface --------------------------------------------------------

func newForgeACMECmd() *cobra.Command {
	cmd := cli.NewStandardCommand("acme", "Manage the forge's ACME (Let's Encrypt DNS-01) certificate machinery")
	cmd.AddCommand(newForgeACMEInstallCredentialsCmd())
	return cmd
}

func newForgeACMEInstallCredentialsCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("install-credentials", "Ship the DNS-01 provider credentials to the forge over pinned SSH")
	cmd.Use = "install-credentials <sa-key.json>"
	cmd.Long = `Install the DNS provider credentials lego needs for the DNS-01 challenge.

The key file is a GCP service-account key with dns.admin on the ONE zone the
domain lives in (mint it per the certs runbook; the forge VM's own identity
deliberately has no scopes and must stay that way). It is written to
` + forgeACMESAKeyPath + ` and referenced from ` + forgeACMEEnvPath + `,
both 0600 root, over the pinned SSH connection — never through terraform,
whose variables and state are readable by anything that can read the state
file.

After this, ` + "`grove forge wg up`" + ` (or ` + "`grove forge up`" + `) converges the
certificate.`
	cmd.Args = cobra.ExactArgs(1)
	cmd.SilenceUsage = true
	var tfDir string
	cmd.Flags().StringVar(&tfDir, "tf-dir", "", "Existing forge Terraform state dir to read without invoking Terraform (default: cached forge outputs)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		forgeCfg, err := loadForgeConfig()
		if err != nil {
			return err
		}
		if err := forgeCfg.Validate(); err != nil {
			return err
		}
		if !forgeACMEEnabled(forgeCfg) {
			return fmt.Errorf("[forge.services] is not in acme mode — set tls_mode = \"acme\", domain, acme_email and acme_dns_provider first")
		}
		env, err := renderForgeACMEEnv(forgeCfg, args[0])
		if err != nil {
			return err
		}
		outputs, err := loadForgeWGUpOutputs(tfDir)
		if err != nil {
			return err
		}
		ssh, cleanup, err := forgeSSH(outputs, forgeCfg)
		if err != nil {
			return err
		}
		defer cleanup()

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Installing DNS-01 credentials on the forge (0600 root):\n")
		if err := shipForgeRootFile(ssh, env.KeyJSON, forgeACMESAKeyPath); err != nil {
			return fmt.Errorf("install %s: %w", forgeACMESAKeyPath, err)
		}
		fmt.Fprintf(out, "  ✓ %s (service-account key %s)\n", forgeACMESAKeyPath, env.ClientEmail)
		if err := shipForgeRootFile(ssh, []byte(env.EnvFile), forgeACMEEnvPath); err != nil {
			return fmt.Errorf("install %s: %w", forgeACMEEnvPath, err)
		}
		if env.ZoneID != "" {
			fmt.Fprintf(out, "  ✓ %s (GCE_PROJECT=%s, GCE_ZONE_ID=%s)\n", forgeACMEEnvPath, env.Project, env.ZoneID)
		} else {
			fmt.Fprintf(out, "  ✓ %s (GCE_PROJECT=%s)\n", forgeACMEEnvPath, env.Project)
			// Said here because this is the moment the grant's shape is decided:
			// without a zone id lego lists the project's zones to find one, so
			// the key needs a PROJECT-level role however tightly the zone itself
			// is bound.
			fmt.Fprintf(out, "    ! no [forge.services] acme_dns_zone_id — lego will find the zone by listing\n")
			fmt.Fprintf(out, "      every zone in the project, so this key needs a project-level binding.\n")
			fmt.Fprintf(out, "      Set acme_dns_zone_id to scope the grant to the one zone.\n")
		}
		fmt.Fprintf(out, "\nNext: `grove forge wg up` converges the certificate and flips both services to it.\n")
		return nil
	}
	return cmd
}

// forgeACMEEnvRender is what install-credentials derived from the key file.
type forgeACMEEnvRender struct {
	KeyJSON     []byte
	EnvFile     string
	Project     string
	ClientEmail string
	ZoneID      string
}

// renderForgeACMEEnv validates the service-account key and renders the
// acme.env content for the configured provider. Only lego's "gcloud" provider
// is automated here — it is the one this deployment uses; for any other
// provider the operator writes acme.env by hand (the renew script only
// requires that the file exist and export the provider's variables).
func renderForgeACMEEnv(cfg *config.ForgeConfig, keyPath string) (*forgeACMEEnvRender, error) {
	provider := strings.TrimSpace(cfg.Services.ACMEDNSProvider)
	if provider != "gcloud" {
		return nil, fmt.Errorf("acme_dns_provider is %q — this helper automates only the \"gcloud\" provider; write %s by hand with that provider's lego variables (0600 root)", provider, forgeACMEEnvPath)
	}
	raw, err := os.ReadFile(expandUserPath(strings.TrimSpace(keyPath)))
	if err != nil {
		return nil, fmt.Errorf("read the service-account key: %w", err)
	}
	var key struct {
		Type        string `json:"type"`
		ProjectID   string `json:"project_id"`
		ClientEmail string `json:"client_email"`
	}
	if err := json.Unmarshal(raw, &key); err != nil {
		return nil, fmt.Errorf("%s is not a JSON service-account key: %w", keyPath, err)
	}
	if key.Type != "service_account" || key.ProjectID == "" || key.ClientEmail == "" {
		return nil, fmt.Errorf("%s does not look like a GCP service-account key (type=%q) — mint one per the certs runbook", keyPath, key.Type)
	}
	var b strings.Builder
	b.WriteString("# Rendered by `grove forge acme install-credentials`. 0600 root.\n")
	b.WriteString("# lego's gcloud DNS-01 provider: the key below has dns.admin on the one\n")
	b.WriteString("# zone the forge domain lives in, and nothing else.\n")
	fmt.Fprintf(&b, "GCE_PROJECT=%s\n", key.ProjectID)
	fmt.Fprintf(&b, "GCE_SERVICE_ACCOUNT_FILE=%s\n", forgeACMESAKeyPath)
	if zone := cfg.Services.EffectiveACMEDNSZoneID(); zone != "" {
		// Naming the zone is what lets the credential be scoped TO that zone:
		// without it lego finds the zone by listing every zone in the project,
		// and that list call is the only thing a zone-level grant cannot serve.
		b.WriteString("# GCE_ZONE_ID skips lego's project-wide zone lookup, so the key above\n")
		b.WriteString("# needs no project-level binding — see [forge.services] acme_dns_zone_id.\n")
		fmt.Fprintf(&b, "GCE_ZONE_ID=%s\n", zone)
	}
	return &forgeACMEEnvRender{
		KeyJSON:     raw,
		EnvFile:     b.String(),
		Project:     key.ProjectID,
		ClientEmail: key.ClientEmail,
		ZoneID:      cfg.Services.EffectiveACMEDNSZoneID(),
	}, nil
}

// shipForgeRootFile stages content in the SSH user's home (0600, via stdin —
// content never appears in an argv or a shell history) and installs it to its
// root-owned destination.
func shipForgeRootFile(ssh *satelliteSSH, content []byte, dest string) error {
	stage := ".grove-forge-ship.tmp"
	if _, err := ssh.outputCommand("umask 077 && cat > \"$HOME/"+stage+"\"", string(content)); err != nil {
		return fmt.Errorf("stage file on the forge: %w", err)
	}
	return ssh.runScript(strings.Join([]string{
		"set -euo pipefail",
		"sudo install -d -m 0755 /etc/grove-forge",
		"sudo install -m 0600 -o root -g root \"$HOME/" + stage + "\" " + shellQuote(dest),
		"rm -f \"$HOME/" + stage + "\"",
	}, "\n"))
}

// ---- converge ---------------------------------------------------------------

// reconcileForgeACME converges the acme TLS posture on an existing forge over
// pinned SSH. Called by `forge up` and `forge wg up` after WireGuard and
// before the ROOT_URL/self-signed reconciles. No-op unless [forge.services]
// is in acme mode.
//
// Failure ordering is the design: nothing that can fail is allowed to leave
// the box worse than self-signed. Certificate issuance happens BEFORE any
// Forgejo change; the Forgejo flip verifies itself on-box and rolls back to
// its pre-flip app.ini (and unit drop-in removal) if https does not answer.
func reconcileForgeACME(w io.Writer, outputs forgeOutputs, forgeCfg *config.ForgeConfig) error {
	if !forgeACMEEnabled(forgeCfg) {
		return nil
	}
	domain := forgeCfg.Services.EffectiveDomain()
	fmt.Fprintf(w, "\nTLS posture: acme (%s) — verifying before touching anything.\n", domain)

	// Preflight 1, from the laptop: the domain must resolve, because lego's
	// DNS-01 needs the zone and every client needs the name. Resolving to
	// something other than the mesh IP is a warning, not an error — an
	// OPNsense host-override deployment resolves differently off-mesh.
	addrs, err := net.LookupHost(domain)
	if err != nil {
		return fmt.Errorf("preflight: %s does not resolve: %v\n  Create the record in the domain's zone (Cloud DNS example):\n    gcloud dns record-sets create %s. --zone <zone-name> --type A --ttl 300 --rrdatas <forge-mesh-ip>\n  (see sync/docs/runbooks/forge-acme-certificates.md)", domain, err, domain)
	}
	if meshIP := forgeMeshIP(forgeCfg); meshIP != "" && !containsString(addrs, meshIP) {
		fmt.Fprintf(w, "  ! %s resolves to %s, not the mesh address %s — clients off this resolver may not reach the forge\n", domain, strings.Join(addrs, ", "), meshIP)
	} else {
		fmt.Fprintf(w, "  ✓ %s resolves to %s\n", domain, strings.Join(addrs, ", "))
	}

	ssh, cleanup, err := forgeSSH(outputs, forgeCfg)
	if err != nil {
		return err
	}
	defer cleanup()

	// Preflight 2, on the VM: read the whole current state in one round trip.
	state, err := readForgeACMEState(ssh, domain)
	if err != nil {
		return fmt.Errorf("preflight: read the forge's TLS state over pinned SSH: %w", err)
	}
	if !state.EnvPresent {
		return fmt.Errorf("preflight: %s is missing on the forge — the DNS-01 challenge has no credentials.\n  Mint the zone-scoped service account per the certs runbook, then:\n    grove forge acme install-credentials <sa-key.json>\n  Nothing was changed; the current TLS posture is intact", forgeACMEEnvPath)
	}
	fmt.Fprintf(w, "  ✓ %s present\n", forgeACMEEnvPath)

	// Machinery: lego, renew script, timer. Idempotent — rewritten every run
	// so it versions with the binary, like the terraform-extracted module.
	if !state.LegoPresent {
		fmt.Fprintf(w, "  installing lego %s…\n", forgeLegoVersion)
	}
	if err := ssh.runScript(forgeLegoInstallScript()); err != nil {
		return fmt.Errorf("install lego: %w", err)
	}
	if err := ssh.runScript(forgeACMEAssetsScript(forgeCfg)); err != nil {
		return fmt.Errorf("install the ACME renew script and timer: %w", err)
	}
	fmt.Fprintf(w, "  ✓ renew machinery converged (%s, daily timer enabled)\n", forgeACMERenewPath)

	// Issuance. Skipped when the deployed certificate already names the
	// domain and is CA-issued — that is what makes a second run a no-op.
	if state.CertCoversDomain && !state.CertSelfSigned {
		fmt.Fprintf(w, "  ✓ certificate already covers %s and chains to a CA — no issuance needed\n", domain)
	} else {
		fmt.Fprintf(w, "  obtaining the certificate for %s via DNS-01 (this can take a minute)…\n", domain)
		if lerr := ssh.runScript("sudo " + forgeACMERenewPath); lerr != nil {
			return fmt.Errorf("certificate issuance failed — the self-signed deployment is untouched and still serving: %w", lerr)
		}
		state, err = readForgeACMEState(ssh, domain)
		if err != nil {
			return fmt.Errorf("re-read the forge's TLS state after issuance: %w", err)
		}
		if !state.CertCoversDomain || state.CertSelfSigned {
			return fmt.Errorf("issuance reported success but the deployed certificate still does not name %s (or is self-issued) — investigate %s on the VM; Forgejo was not touched", domain, forgeTLSDir)
		}
		fmt.Fprintf(w, "  ✓ certificate deployed to %s (grove-syncd restarted by the deploy hook)\n", forgeTLSDir)
	}

	// The Forgejo flip: https on 443 with the shared certificate, ROOT_URL to
	// match. Self-verifying and self-rolling-back on the VM.
	if state.ForgejoProtocol == "https" && state.ForgejoRootURL == forgeDomainRootURL(forgeCfg) {
		fmt.Fprintf(w, "  ✓ Forgejo already serves %s — no flip needed\n", forgeDomainRootURL(forgeCfg))
	} else {
		fmt.Fprintf(w, "  flipping Forgejo to https on :%d…\n", forgeForgejoHTTPSPort)
		if ferr := ssh.runScript(forgeForgejoTLSFlipScript(domain)); ferr != nil {
			return fmt.Errorf("the Forgejo https flip failed and was rolled back to the previous app.ini (plain http): %w", ferr)
		}
		fmt.Fprintf(w, "  ✓ Forgejo serves %s (verified on the VM against the system trust store)\n", forgeDomainRootURL(forgeCfg))
	}

	printForgeClientMigration(w, forgeCfg)
	return nil
}

// forgeDomainRootURL is the origin Forgejo advertises in acme mode.
func forgeDomainRootURL(cfg *config.ForgeConfig) string {
	return "https://" + cfg.Services.EffectiveDomain() + "/"
}

// printForgeClientMigration is the converge epilogue: what each laptop changes
// now that the forge's certificate chains to system roots.
func printForgeClientMigration(w io.Writer, cfg *config.ForgeConfig) {
	domain := cfg.Services.EffectiveDomain()
	fmt.Fprintf(w, "\nPer-laptop client migration (certificates now chain to system roots):\n")
	fmt.Fprintf(w, "  [sync]  server = %q\n", fmt.Sprintf("https://%s:%d", domain, cfg.Services.EffectiveSyncdPort()))
	fmt.Fprintf(w, "  [sync]  delete ca_cert — the system trust store verifies this server now\n")
	fmt.Fprintf(w, "  [forge] url = %q\n", "https://"+domain+"/")
}

// ---- on-VM state ------------------------------------------------------------

// forgeACMEState is what one read-only round trip learns about the VM.
type forgeACMEState struct {
	EnvPresent       bool
	LegoPresent      bool
	CertPresent      bool
	CertCoversDomain bool
	CertSelfSigned   bool
	ForgejoProtocol  string
	ForgejoRootURL   string
	TimerEnabled     bool
}

func readForgeACMEState(ssh *satelliteSSH, domain string) (forgeACMEState, error) {
	out, err := ssh.outputScript(forgeACMEStateScript(domain))
	if err != nil {
		return forgeACMEState{}, err
	}
	return parseForgeACMEState(out)
}

// forgeACMEStateScript reads TLS posture without changing anything. KEY=VALUE
// trailer, like the WireGuard status script.
func forgeACMEStateScript(domain string) string {
	q := shellQuote(domain)
	return strings.Join([]string{
		"set -u",
		"ENV_PRESENT=0; sudo test -f " + forgeACMEEnvPath + " && ENV_PRESENT=1",
		"LEGO_PRESENT=0; [ -x /usr/local/bin/lego ] && LEGO_PRESENT=1",
		"CERT_PRESENT=0; sudo test -f " + forgeTLSDir + "/cert.pem && CERT_PRESENT=1",
		"CERT_COVERS_DOMAIN=0; CERT_SELF_SIGNED=1",
		"if [ \"$CERT_PRESENT\" = 1 ]; then",
		"  SANS=$(sudo openssl x509 -in " + forgeTLSDir + "/cert.pem -noout -ext subjectAltName 2>/dev/null || true)",
		"  case \"$SANS\" in *\"DNS:\"" + q + "*) CERT_COVERS_DOMAIN=1;; esac",
		"  ISS=$(sudo openssl x509 -in " + forgeTLSDir + "/cert.pem -noout -issuer)",
		"  SUB=$(sudo openssl x509 -in " + forgeTLSDir + "/cert.pem -noout -subject)",
		"  [ \"${ISS#issuer=}\" != \"${SUB#subject=}\" ] && CERT_SELF_SIGNED=0",
		"fi",
		"PROTOCOL=''; ROOT_URL=''",
		"if sudo test -f /etc/forgejo/app.ini; then",
		"  PROTOCOL=$(sudo awk -F= '/^[[:space:]]*PROTOCOL[[:space:]]*=/{v=$0; sub(/^[^=]*=[[:space:]]*/, \"\", v)} END{print v}' /etc/forgejo/app.ini)",
		"  ROOT_URL=$(sudo awk -F= '/^[[:space:]]*ROOT_URL[[:space:]]*=/{v=$0; sub(/^[^=]*=[[:space:]]*/, \"\", v)} END{print v}' /etc/forgejo/app.ini)",
		"fi",
		"TIMER_ENABLED=0; sudo systemctl is-enabled --quiet grove-forge-acme.timer 2>/dev/null && TIMER_ENABLED=1",
		"printf 'ENV_PRESENT=%s\\n' \"$ENV_PRESENT\"",
		"printf 'LEGO_PRESENT=%s\\n' \"$LEGO_PRESENT\"",
		"printf 'CERT_PRESENT=%s\\n' \"$CERT_PRESENT\"",
		"printf 'CERT_COVERS_DOMAIN=%s\\n' \"$CERT_COVERS_DOMAIN\"",
		"printf 'CERT_SELF_SIGNED=%s\\n' \"$CERT_SELF_SIGNED\"",
		"printf 'FORGEJO_PROTOCOL=%s\\n' \"$PROTOCOL\"",
		"printf 'FORGEJO_ROOT_URL=%s\\n' \"$ROOT_URL\"",
		"printf 'TIMER_ENABLED=%s\\n' \"$TIMER_ENABLED\"",
	}, "\n") + "\n"
}

func parseForgeACMEState(out string) (forgeACMEState, error) {
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = strings.TrimSpace(value)
		}
	}
	if values["ENV_PRESENT"] == "" || values["CERT_PRESENT"] == "" {
		return forgeACMEState{}, fmt.Errorf("parse forge ACME state: missing trailer in %q", strings.TrimSpace(out))
	}
	return forgeACMEState{
		EnvPresent:       values["ENV_PRESENT"] == "1",
		LegoPresent:      values["LEGO_PRESENT"] == "1",
		CertPresent:      values["CERT_PRESENT"] == "1",
		CertCoversDomain: values["CERT_COVERS_DOMAIN"] == "1",
		CertSelfSigned:   values["CERT_SELF_SIGNED"] == "1",
		ForgejoProtocol:  values["FORGEJO_PROTOCOL"],
		ForgejoRootURL:   values["FORGEJO_ROOT_URL"],
		TimerEnabled:     values["TIMER_ENABLED"] == "1",
	}, nil
}

// ---- converge scripts -------------------------------------------------------

// forgeLegoInstallScript installs the pinned lego release if absent. Matches
// the syncd module's fragment (same version, same install path).
func forgeLegoInstallScript() string {
	return strings.Join([]string{
		"set -euo pipefail",
		"if [ -x /usr/local/bin/lego ]; then exit 0; fi",
		"LEGO_TARBALL=lego_" + forgeLegoVersion + "_linux_amd64.tar.gz",
		"curl -fsSL \"https://github.com/go-acme/lego/releases/download/" + forgeLegoVersion + "/$LEGO_TARBALL\" -o /tmp/lego.tgz",
		"sudo tar -C /usr/local/bin -xzf /tmp/lego.tgz lego",
		"rm -f /tmp/lego.tgz",
	}, "\n")
}

// forgeACMERenewScript is the on-VM obtain/renew/deploy script. Semantics in
// lockstep with modules/syncd/install.sh.tpl's ACME_RENEW heredoc; the deploy
// step restarts BOTH services because they share the one certificate.
func forgeACMERenewScript() string {
	return `#!/bin/sh
set -eu
# acme.defaults (email/provider/domain) is rendered configuration; acme.env is
# the operator-installed DNS provider credential file and may override any
# default. Sourced in that order, on purpose.
# set -a is load-bearing: lego reads DNS provider credentials from the
# ENVIRONMENT, but a sourced assignment is only a shell variable unless
# exported. Without it lego silently falls back to Application Default
# Credentials — on a GCE VM that is the metadata server, whose default scopes
# lack ndev.clouddns.readwrite, so DNS-01 fails with a 403 scope error that
# names neither this file nor the real cause. It also covers acme.env files
# written by hand for the non-gcloud providers.
set -a
. ` + forgeACMEDefaultsPath + `
if [ ! -f ` + forgeACMEEnvPath + ` ]; then
  echo "grove-forge-acme: ` + forgeACMEEnvPath + ` is missing — install the DNS-01 credentials with 'grove forge acme install-credentials <sa-key.json>'" >&2
  exit 1
fi
. ` + forgeACMEEnvPath + `
set +a
# ACME_DNS_RESOLVERS (optional, space-separated host:port) overrides where lego
# checks TXT propagation. Rendered into acme.defaults from [forge.services]
# acme_dns_resolvers, so it survives every converge — do NOT hand-add it to
# acme.env, which install-credentials rewrites wholesale.
# Needed when the forge domain is a DELEGATED SUBDOMAIN:
# lego walks up to the parent zone's SOA and queries the PARENT's nameservers,
# which correctly answer with a referral rather than the challenge record, so
# the pre-check times out even though Let's Encrypt itself would validate fine.
# Point this at the nameservers the subdomain is delegated TO. Word splitting
# below is deliberate — lego takes --dns.resolvers once per resolver.
ACME_RESOLVER_FLAGS=""
for _r in ${ACME_DNS_RESOLVERS:-}; do
  ACME_RESOLVER_FLAGS="$ACME_RESOLVER_FLAGS --dns.resolvers $_r"
done
ACME_CERT="/var/lib/grove-forge/acme/certificates/$ACME_DOMAIN.crt"
if [ -f "$ACME_CERT" ]; then
  lego --accept-tos --email "$ACME_EMAIL" --dns "$ACME_DNS_PROVIDER" $ACME_RESOLVER_FLAGS \
    --domains "$ACME_DOMAIN" --path /var/lib/grove-forge/acme renew
else
  lego --accept-tos --email "$ACME_EMAIL" --dns "$ACME_DNS_PROVIDER" $ACME_RESOLVER_FLAGS \
    --domains "$ACME_DOMAIN" --path /var/lib/grove-forge/acme run
fi
install -m 0644 -o root -g root "$ACME_CERT" ` + forgeTLSDir + `/cert.pem
install -m 0640 -o root -g ` + forgeTLSGroup + ` \
  "/var/lib/grove-forge/acme/certificates/$ACME_DOMAIN.key" ` + forgeTLSDir + `/key.pem
# Keep the on-VM record 'grove forge status' compares fingerprints against.
openssl x509 -in ` + forgeTLSDir + `/cert.pem -noout -fingerprint -sha256 \
  | sed 's/^.*=//' > /var/lib/grove-forge/tls-fingerprint.txt
# ONE certificate, TWO services: both must pick a renewal up, or the classic
# silent failure is a renewed syncd next to a Forgejo serving the expired cert.
systemctl reload-or-restart grove-syncd.service || true
systemctl reload-or-restart forgejo.service || true
`
}

// forgeACMEAssetsScript writes the renew script, its rendered defaults, and
// the daily timer, then enables the timer. PATH matters: lego lives in
// /usr/local/bin, which systemd's default PATH includes, but the renew script
// is also run directly via sudo, so it relies on root's PATH containing
// /usr/local/bin (Debian's does).
func forgeACMEAssetsScript(cfg *config.ForgeConfig) string {
	defaults := strings.Join([]string{
		"ACME_EMAIL=" + strings.TrimSpace(cfg.Services.ACMEEmail),
		"ACME_DNS_PROVIDER=" + strings.TrimSpace(cfg.Services.ACMEDNSProvider),
		"ACME_DOMAIN=" + cfg.Services.EffectiveDomain(),
		"ACME_DNS_RESOLVERS=" + strings.Join(cfg.Services.EffectiveACMEDNSResolvers(), " "),
	}, "\n")
	service := strings.Join([]string{
		"[Unit]",
		"Description=Obtain/renew the grove forge certificate via ACME DNS-01",
		"[Service]",
		"Type=oneshot",
		"ExecStart=" + forgeACMERenewPath,
	}, "\n")
	timer := strings.Join([]string{
		"[Unit]",
		"Description=Daily ACME renewal check for the grove forge certificate",
		"[Timer]",
		"OnCalendar=daily",
		"RandomizedDelaySec=6h",
		"Persistent=true",
		"[Install]",
		"WantedBy=timers.target",
	}, "\n")
	return strings.Join([]string{
		"set -euo pipefail",
		"sudo install -d -m 0755 /etc/grove-forge",
		"sudo install -d -m 0755 " + forgeTLSDir,
		"getent group " + forgeTLSGroup + " >/dev/null || sudo groupadd --system " + forgeTLSGroup,
		"TMP=$(mktemp -d)",
		"cat > \"$TMP/acme.defaults\" <<'GROVE_EOF'\n" + defaults + "\nGROVE_EOF",
		"cat > \"$TMP/grove-forge-acme\" <<'GROVE_EOF'\n" + forgeACMERenewScript() + "GROVE_EOF",
		"cat > \"$TMP/acme.service\" <<'GROVE_EOF'\n" + service + "\nGROVE_EOF",
		"cat > \"$TMP/acme.timer\" <<'GROVE_EOF'\n" + timer + "\nGROVE_EOF",
		"sudo install -m 0644 -o root -g root \"$TMP/acme.defaults\" " + forgeACMEDefaultsPath,
		"sudo install -m 0700 -o root -g root \"$TMP/grove-forge-acme\" " + forgeACMERenewPath,
		"sudo install -m 0644 -o root -g root \"$TMP/acme.service\" /etc/systemd/system/grove-forge-acme.service",
		"sudo install -m 0644 -o root -g root \"$TMP/acme.timer\" /etc/systemd/system/grove-forge-acme.timer",
		"rm -rf \"$TMP\"",
		"sudo systemctl daemon-reload",
		"sudo systemctl enable --now grove-forge-acme.timer",
	}, "\n")
}

// forgeForgejoTLSFlipScript flips Forgejo from plain http to https on :443
// with the shared certificate, verifying against the VM's own trust store and
// ROLLING BACK to the pre-flip app.ini if https does not answer. The unit
// gains its capabilities via a drop-in rather than an edit, so the base unit
// (terraform-rendered) stays untouched and the flip is removable.
func forgeForgejoTLSFlipScript(domain string) string {
	q := shellQuote(domain)
	return `set -euo pipefail
DOMAIN=` + q + `
APP=/etc/forgejo/app.ini
DROPIN_DIR=/etc/systemd/system/forgejo.service.d
sudo cp "$APP" "$APP.pre-acme"
TMP=$(mktemp)
cat > "$TMP" <<'GROVE_EOF'
[Service]
SupplementaryGroups=` + forgeTLSGroup + `
ReadOnlyPaths=` + forgeTLSDir + `
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
GROVE_EOF
sudo install -d -m 0755 "$DROPIN_DIR"
sudo install -m 0644 -o root -g root "$TMP" "$DROPIN_DIR/50-grove-acme-tls.conf"
rm -f "$TMP"
# Forgejo's service user reads the key via the shared group.
FORGEJO_USER=$(sudo awk -F= '/^[[:space:]]*RUN_USER[[:space:]]*=/{v=$0; sub(/^[^=]*=[[:space:]]*/, "", v)} END{print v}' "$APP")
[ -n "$FORGEJO_USER" ] && sudo usermod -aG ` + forgeTLSGroup + ` "$FORGEJO_USER" || true
TMP2=$(mktemp)
# Delete every existing spelling of the keys being flipped, then append ONE
# canonical [server] block; Forgejo's ini reader merges repeated sections.
sudo awk -v domain="$DOMAIN" '
  /^[[:space:]]*(PROTOCOL|HTTP_PORT|CERT_FILE|KEY_FILE|ROOT_URL)[[:space:]]*=/ { next }
  { print }
  END {
    print ""
    print "[server]"
    print "PROTOCOL = https"
    print "HTTP_PORT = ` + fmt.Sprint(forgeForgejoHTTPSPort) + `"
    print "CERT_FILE = ` + forgeTLSDir + `/cert.pem"
    print "KEY_FILE = ` + forgeTLSDir + `/key.pem"
    print "ROOT_URL = https://" domain "/"
  }
' "$APP" > "$TMP2"
OWNER=$(sudo stat -c '%U' "$APP"); GROUP=$(sudo stat -c '%G' "$APP"); MODE=$(sudo stat -c '%a' "$APP")
sudo install -o "$OWNER" -g "$GROUP" -m "$MODE" "$TMP2" "$APP"
rm -f "$TMP2"
sudo systemctl daemon-reload
sudo systemctl restart forgejo.service
ok=0
for i in $(seq 1 15); do
  if curl -fsS -o /dev/null --max-time 5 --resolve "$DOMAIN:` + fmt.Sprint(forgeForgejoHTTPSPort) + `:127.0.0.1" "https://$DOMAIN/"; then ok=1; break; fi
  sleep 2
done
if [ "$ok" != 1 ]; then
  echo "https did not answer; rolling back to the pre-flip app.ini" >&2
  sudo journalctl -u forgejo.service --no-pager -n 20 >&2 || true
  sudo install -o "$OWNER" -g "$GROUP" -m "$MODE" "$APP.pre-acme" "$APP"
  sudo rm -f "$DROPIN_DIR/50-grove-acme-tls.conf"
  sudo systemctl daemon-reload
  sudo systemctl restart forgejo.service
  exit 1
fi
printf 'CHANGED=1\n'
`
}

// ---- small helpers ----------------------------------------------------------

// forgeCertDaysLeft renders a NotAfter as whole days from now, floored.
func forgeCertDaysLeft(notAfter, now time.Time) int {
	return int(notAfter.Sub(now).Hours() / 24)
}
