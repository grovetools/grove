#!/usr/bin/env bash
# Rendered by terraform templatefile(): ${ssh_user} and ${zig_version} are
# terraform variables; everything else is plain shell (avoid $${...} shell
# syntax in here — use unbraced $VAR so terraform doesn't try to interpolate).
set -euxo pipefail
exec > >(tee -a /var/log/grove-satellite-startup.log) 2>&1

if [ -f /var/lib/grove-satellite/startup-done ]; then
  echo "startup already completed; nothing to do"
  exit 0
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y build-essential git curl jq tmux htop tree sqlite3 libsqlite3-dev ca-certificates unzip xz-utils pkg-config

# GitHub CLI (official apt repo)
mkdir -p /etc/apt/keyrings
chmod 755 /etc/apt/keyrings
curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /etc/apt/keyrings/githubcli-archive-keyring.gpg
chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list
apt-get update -y
apt-get install -y gh

# gcloud CLI — needed so agents on the VM can talk to GCP. Auth comes from
# the attached service account via the metadata server (Application Default
# Credentials): no key files, nothing to rotate. GCE Ubuntu images usually
# ship gcloud already as a snap whose shim lives in /snap/bin — NOT on
# Debian's default PATH, so a plain command -v misses it (this script runs
# with a minimal PATH too). Install from Google's apt repo only when neither
# form is present; the guard keeps this idempotent.
if ! command -v gcloud >/dev/null 2>&1 && [ ! -x /snap/bin/gcloud ]; then
  curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg \
    | gpg --dearmor --batch --yes -o /etc/apt/keyrings/cloud.google.gpg
  chmod go+r /etc/apt/keyrings/cloud.google.gpg
  echo "deb [signed-by=/etc/apt/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main" > /etc/apt/sources.list.d/google-cloud-sdk.list
  apt-get update -y
  apt-get install -y google-cloud-cli
fi
# Snap-shipped gcloud is invisible to non-login SSH shells and to shells
# spawned under the groved unit (neither reads profile.d, and /snap/bin may
# be absent from their PATH); a /usr/local/bin symlink fixes that everywhere.
# Guard on the link target, NOT `command -v`: this startup context's own
# PATH can already see /snap/bin, which must not suppress the link — that is
# exactly how gcloud went missing for user shells on the 2026-07-13 rehearsal.
for tool in gcloud gsutil bq; do
  if [ -x "/snap/bin/$tool" ] && [ ! -e "/usr/local/bin/$tool" ]; then
    ln -s "/snap/bin/$tool" "/usr/local/bin/$tool"
  fi
done

# Go toolchain — latest stable (go.work needs >= 1.25; Go is backwards compatible)
GO_VERSION=$(curl -fsSL 'https://go.dev/dl/?mode=json' | jq -r '.[0].version')
curl -fsSL "https://go.dev/dl/$GO_VERSION.linux-amd64.tar.gz" -o /tmp/go.tgz
rm -rf /usr/local/go
tar -C /usr/local -xzf /tmp/go.tgz
rm -f /tmp/go.tgz

# Zig — pinned; compositor builds libghostty-vt with it (treemux dependency)
ZIG_URL=$(curl -fsSL https://ziglang.org/download/index.json | jq -r '."${zig_version}"."x86_64-linux".tarball')
curl -fsSL "$ZIG_URL" -o /tmp/zig.txz
rm -rf /opt/zig
mkdir -p /opt/zig
tar -C /opt/zig --strip-components=1 -xJf /tmp/zig.txz
ln -sf /opt/zig/zig /usr/local/bin/zig
rm -f /tmp/zig.txz

# Login environment for the grove stack
cat > /etc/profile.d/grove-satellite.sh <<'PROFILE'
export PATH="$HOME/.local/share/grove/bin:$HOME/.grove/bin:$HOME/.local/bin:/usr/local/go/bin:$HOME/go/bin:/snap/bin:$PATH"
export GOPRIVATE='github.com/grovetools/*'
export GONOSUMDB='github.com/grovetools/*'
PROFILE

# systemd --user services for the login user survive logout
loginctl enable-linger ${ssh_user} || true

mkdir -p /var/lib/grove-satellite
touch /var/lib/grove-satellite/startup-done
echo "grove-satellite startup complete"
