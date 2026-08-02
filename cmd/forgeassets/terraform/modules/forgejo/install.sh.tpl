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

chown root:${user} /etc/forgejo/app.ini
chmod 0640 /etc/forgejo/app.ini

cat > /etc/systemd/system/forgejo.service <<'FORGEJO_UNIT'
${unit}
FORGEJO_UNIT

systemctl daemon-reload
systemctl enable --now forgejo.service
