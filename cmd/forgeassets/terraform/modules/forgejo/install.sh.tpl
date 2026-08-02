# Forgejo install fragment, rendered by terraform and injected into the VM
# startup script. Runs as root, under `set -euxo pipefail` from the caller.
#
# Idempotent: re-running the startup script (a reboot, a re-apply) must not
# re-download, must not overwrite the database, and must not unlock the
# installer. The version+checksum pin is what makes "already installed" a
# decidable question.

echo "--- forgejo ${binary_name} ---"

id -u ${user} >/dev/null 2>&1 || adduser --system --shell /bin/bash --gecos 'Forgejo' \
  --group --disabled-password --home /var/lib/forgejo ${user}

install -d -m 0750 -o ${user} -g ${user} /var/lib/forgejo
install -d -m 0750 -o ${user} -g ${user} /var/lib/forgejo/data
install -d -m 0750 -o ${user} -g ${user} /var/lib/forgejo/log
install -d -m 0750 -o root -g ${user} /etc/forgejo

FORGEJO_BIN=/usr/local/bin/forgejo
FORGEJO_WANT_SHA="${sha256}"
FORGEJO_HAVE_SHA=""
if [ -x "$FORGEJO_BIN" ]; then
  FORGEJO_HAVE_SHA=$(sha256sum "$FORGEJO_BIN" | cut -d' ' -f1)
fi

if [ "$FORGEJO_HAVE_SHA" != "$FORGEJO_WANT_SHA" ]; then
  curl -fsSL "${download_url}" -o /tmp/forgejo.download
  echo "$FORGEJO_WANT_SHA  /tmp/forgejo.download" | sha256sum -c -
  install -m 0755 -o root -g root /tmp/forgejo.download "$FORGEJO_BIN"
  rm -f /tmp/forgejo.download
fi

# app.ini is rewritten on every run: it is generated configuration, and the
# secrets Forgejo generates for itself live in its own data dir, not in here.
cat > /etc/forgejo/app.ini <<'FORGEJO_APP_INI'
${app_ini}
FORGEJO_APP_INI

%{ if root_url == "" ~}
# No domain configured: ROOT_URL has to be the external IP, which is only known
# on the box. Clone URLs and webhook payloads are built from it, so it is
# written here rather than left to Forgejo's own guess.
FORGE_IP=$(cat /var/lib/grove-forge/external-ip 2>/dev/null || echo "")
if [ -n "$FORGE_IP" ]; then
  printf '\n[server]\nROOT_URL = http://%s:%s/\n' "$FORGE_IP" "${http_port}" >> /etc/forgejo/app.ini
fi
%{ endif ~}

# Forgejo REQUIRES SECRET_KEY and INTERNAL_TOKEN to exist in app.ini. When they
# are absent it generates them and saves them back into app.ini — which fails
# under the unit's ProtectSystem=strict (/etc is read-only to the service) and
# crash-loops the forge before it ever serves. The trial's working install
# generated both up front; so does this.
#
# They are generated ONCE and re-injected on every run: regenerating them would
# invalidate every existing session and API token on the instance. They live in
# the pet's own state dir, never in terraform state and never in metadata.
FORGEJO_SECRETS=/var/lib/forgejo/secrets.env
if [ ! -f "$FORGEJO_SECRETS" ]; then
  ( umask 077
    {
      printf 'SECRET_KEY=%s\n' "$("$FORGEJO_BIN" generate secret SECRET_KEY)"
      printf 'INTERNAL_TOKEN=%s\n' "$("$FORGEJO_BIN" generate secret INTERNAL_TOKEN)"
      printf 'JWT_SECRET=%s\n' "$("$FORGEJO_BIN" generate secret JWT_SECRET)"
      # LFS_JWT_SECRET has the same 32-byte base64 shape as JWT_SECRET and is
      # required because app.ini sets LFS_START_SERVER = true.
      printf 'LFS_JWT_SECRET=%s\n' "$("$FORGEJO_BIN" generate secret JWT_SECRET)"
    } > "$FORGEJO_SECRETS" )
  chown root:root "$FORGEJO_SECRETS"
  chmod 0600 "$FORGEJO_SECRETS"
fi
# shellcheck disable=SC1090
. "$FORGEJO_SECRETS"
# A repeated section header is merged by Forgejo's ini reader, the same way the
# ROOT_URL block above repeats [server]. All FOUR secrets are needed: Forgejo
# persists [security] SECRET_KEY/INTERNAL_TOKEN, [oauth2] JWT_SECRET and
# [server] LFS_JWT_SECRET independently, and any one of them missing is its own
# separate crash loop before the forge ever serves a request.
printf '\n[security]\nSECRET_KEY = %s\nINTERNAL_TOKEN = %s\n\n[oauth2]\nJWT_SECRET = %s\n\n[server]\nLFS_JWT_SECRET = %s\n' \
  "$SECRET_KEY" "$INTERNAL_TOKEN" "$JWT_SECRET" "$LFS_JWT_SECRET" >> /etc/forgejo/app.ini

chown root:${user} /etc/forgejo/app.ini
chmod 0640 /etc/forgejo/app.ini

cat > /etc/systemd/system/forgejo.service <<'FORGEJO_UNIT'
${unit}
FORGEJO_UNIT

systemctl daemon-reload
systemctl enable --now forgejo.service
