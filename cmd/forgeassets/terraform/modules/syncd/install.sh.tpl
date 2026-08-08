# grove-syncd install fragment, rendered by terraform and injected into the VM
# startup script. Runs as root under `set -euxo pipefail`.
#
# It never downloads a binary: `grove forge up --prebuilt` ships one built on
# the laptop. Everything here is idempotent so a reboot or a re-apply is a
# no-op.

echo "--- grove-syncd (port ${port}, tls ${tls_mode}) ---"

getent group ${tls_group} >/dev/null || groupadd --system ${tls_group}

install -d -m 0750 /var/lib/grove-syncd
install -d -m 0750 /var/log/grove-syncd

%{ if tls_mode == "acme" ~}
# ACME via lego, DNS-01 ONLY. HTTP-01 is deliberately not offered: it needs
# inbound :80 from the world, and no rule in this module may open a port to the
# internet. The DNS provider's credentials are read from
# /etc/grove-forge/acme.env, which the operator installs out of band — never
# from terraform state and never from instance metadata.
if ! command -v lego >/dev/null 2>&1; then
  LEGO_TARBALL=lego_v4.17.4_linux_amd64.tar.gz
  curl -fsSL "https://github.com/go-acme/lego/releases/download/v4.17.4/$LEGO_TARBALL" -o /tmp/lego.tgz
  tar -C /usr/local/bin -xzf /tmp/lego.tgz lego
  rm -f /tmp/lego.tgz
fi
if [ ! -f /etc/grove-forge/acme.env ]; then
  echo "grove forge: tls_mode=acme but /etc/grove-forge/acme.env is missing" >&2
  echo "grove forge: install the DNS provider credentials (grove forge acme install-credentials <sa-key.json>), then: systemctl start grove-forge-acme" >&2
fi
cat > /usr/local/sbin/grove-forge-acme <<'ACME_RENEW'
#!/bin/sh
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
. /etc/grove-forge/acme.defaults
if [ ! -f /etc/grove-forge/acme.env ]; then
  echo "grove-forge-acme: /etc/grove-forge/acme.env is missing — install the DNS-01 credentials with 'grove forge acme install-credentials <sa-key.json>'" >&2
  exit 1
fi
. /etc/grove-forge/acme.env
set +a
# ACME_DNS_RESOLVERS (optional, space-separated host:port) overrides where lego
# checks TXT propagation. Needed when the forge domain is a DELEGATED SUBDOMAIN:
# lego walks up to the parent zone's SOA and queries the PARENT's nameservers,
# which correctly answer with a referral rather than the challenge record, so
# the pre-check times out even though Let's Encrypt itself would validate fine.
# Point this at the nameservers the subdomain is delegated TO. Word splitting
# below is deliberate — lego takes --dns.resolvers once per resolver.
ACME_RESOLVER_FLAGS=""
for _r in $${ACME_DNS_RESOLVERS:-}; do
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
install -m 0644 -o root -g root "$ACME_CERT" /etc/grove-forge/tls/cert.pem
install -m 0640 -o root -g ${tls_group} \
  "/var/lib/grove-forge/acme/certificates/$ACME_DOMAIN.key" /etc/grove-forge/tls/key.pem
# Keep the on-VM record `grove forge status` compares fingerprints against.
openssl x509 -in /etc/grove-forge/tls/cert.pem -noout -fingerprint -sha256 \
  | sed 's/^.*=//' > /var/lib/grove-forge/tls-fingerprint.txt
# ONE certificate, TWO services: both must pick a renewal up, or the classic
# silent failure is a renewed syncd next to a Forgejo serving the expired cert.
# reload-or-restart also STARTS a stopped unit, which is what first brings
# Forgejo up on a fresh ACME provision.
systemctl reload-or-restart grove-syncd.service || true
systemctl reload-or-restart forgejo.service || true
ACME_RENEW
chmod 0700 /usr/local/sbin/grove-forge-acme

cat > /etc/grove-forge/acme.defaults <<ACME_DEFAULTS
ACME_EMAIL=${acme_email}
ACME_DNS_PROVIDER=${acme_dns_provider}
ACME_DOMAIN=${domain}
ACME_DEFAULTS
chmod 0644 /etc/grove-forge/acme.defaults

cat > /etc/systemd/system/grove-forge-acme.service <<'ACME_UNIT'
[Unit]
Description=Obtain/renew the grove forge certificate via ACME DNS-01
[Service]
Type=oneshot
ExecStart=/usr/local/sbin/grove-forge-acme
ACME_UNIT

cat > /etc/systemd/system/grove-forge-acme.timer <<'ACME_TIMER'
[Unit]
Description=Daily ACME renewal check for the grove forge certificate
[Timer]
OnCalendar=daily
RandomizedDelaySec=6h
Persistent=true
[Install]
WantedBy=timers.target
ACME_TIMER
systemctl daemon-reload
systemctl enable grove-forge-acme.timer
%{ else ~}
# Self-signed: the certificate was generated by the caller's TLS section. Hand
# the private key to the service's group so DynamicUser can read it, and keep
# it unreadable by anyone else.
if [ -f /etc/grove-forge/tls/key.pem ]; then
  chown root:${tls_group} /etc/grove-forge/tls/key.pem
  chmod 0640 /etc/grove-forge/tls/key.pem
fi
%{ endif ~}

cat > /etc/systemd/system/grove-syncd.service <<'SYNCD_UNIT'
${unit}
SYNCD_UNIT

systemctl daemon-reload
systemctl enable grove-syncd.service
# Enabled, not started: the unit's ConditionPathExists holds it until
# `grove forge up --prebuilt` installs /usr/local/bin/grove-syncd.
if [ -x /usr/local/bin/grove-syncd ]; then
  systemctl start grove-syncd.service
else
  echo "grove-syncd not installed yet; the unit is enabled and will start once the binary is shipped"
fi
