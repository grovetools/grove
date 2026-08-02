#!/usr/bin/env bash
# Rendered by terraform templatefile(). ${ssh_user}, ${tls_mode}, ${domain},
# ${forgejo_setup} and ${syncd_setup} are terraform values; everything else is
# plain shell. Use UNBRACED $VAR in here so terraform does not try to
# interpolate it (the same rule the satellite startup script follows).
set -euxo pipefail
exec > >(tee -a /var/log/grove-forge-startup.log) 2>&1

if [ -f /var/lib/grove-forge/startup-done ]; then
  echo "startup already completed; nothing to do"
  exit 0
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update -y
# Deliberately small: this is a SERVICE host, not the satellite's build host.
# No Go, no zig, no gh, no gcloud — the Forgejo binary is a pinned download and
# grove-syncd arrives cross-built from the laptop.
# unzip is not incidental: `forgejo dump` writes a zip, so without it the box
# cannot unpack its own backup — which is the restore drill the forge is gated
# on (adversarial review §9, job 18).
apt-get install -y ca-certificates curl git sqlite3 openssl unzip

mkdir -p /var/lib/grove-forge

# --- TLS material ----------------------------------------------------------
#
# Both services read from /etc/grove-forge/tls. Self-signed is the default
# because it needs no DNS and, unlike an ACME HTTP-01 challenge, no 0.0.0.0/0
# ingress on :80 — which this module refuses to create for any port. The
# fingerprint below is what `grove forge status` shows for client pinning.
install -d -m 0755 /etc/grove-forge/tls
TLS_DIR=/etc/grove-forge/tls
TLS_MODE="${tls_mode}"
DOMAIN="${domain}"

EXTERNAL_IP=$(curl -fsS -H 'Metadata-Flavor: Google' \
  http://metadata.google.internal/computeMetadata/v1/instance/network-interfaces/0/access-configs/0/external-ip || echo "")
echo "$EXTERNAL_IP" > /var/lib/grove-forge/external-ip

if [ "$TLS_MODE" = "self-signed" ] && [ ! -f "$TLS_DIR/cert.pem" ]; then
  CN="$DOMAIN"
  if [ -z "$CN" ]; then CN="$EXTERNAL_IP"; fi
  SAN="IP:$EXTERNAL_IP"
  if [ -n "$DOMAIN" ]; then SAN="DNS:$DOMAIN,$SAN"; fi
  openssl req -x509 -newkey rsa:4096 -nodes -days 3650 \
    -subj "/CN=$CN" -addext "subjectAltName=$SAN" \
    -keyout "$TLS_DIR/key.pem" -out "$TLS_DIR/cert.pem"
  chmod 0600 "$TLS_DIR/key.pem"
  chmod 0644 "$TLS_DIR/cert.pem"
fi

if [ -f "$TLS_DIR/cert.pem" ]; then
  # Recorded for operators who reach the box over SSH; `grove forge status`
  # computes the same value off the wire, so the two can be compared.
  openssl x509 -in "$TLS_DIR/cert.pem" -noout -fingerprint -sha256 \
    | sed 's/^.*=//' > /var/lib/grove-forge/tls-fingerprint.txt
fi

# --- services --------------------------------------------------------------

${forgejo_setup}

${syncd_setup}

# systemd --user is not used here (both services are system units), but a login
# shell should still find anything grove installs later.
cat > /etc/profile.d/grove-forge.sh <<'PROFILE'
export PATH="/usr/local/bin:$PATH"
PROFILE

touch /var/lib/grove-forge/startup-done
echo "grove-forge startup complete"
