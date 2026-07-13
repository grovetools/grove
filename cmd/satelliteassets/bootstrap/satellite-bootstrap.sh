#!/usr/bin/env bash
# satellite-bootstrap.sh — turn a freshly-applied grove-satellite VM into a
# running grove satellite: ecosystem cloned, grove stack built, grove-syncd
# serving under systemd, groved running with pull=true sync + notebook dirs.
#
# Runs FROM the laptop; drives the VM over SSH. Idempotent — safe to re-run.
#
# Usage:
#   satellite-bootstrap.sh <ssh-destination>
#   gh auth token | satellite-bootstrap.sh <ssh-destination> --gh-token-stdin
#   printf 'GH_TOKEN=%s\nCLAUDE_CODE_OAUTH_TOKEN=%s\n' "$(gh auth token)" "$CT" \
#     | satellite-bootstrap.sh <ssh-destination> --gh-token-stdin --claude-token-stdin
#
# Flags:
#   --gh-token-stdin       GitHub token arrives on stdin (see framing below).
#   --claude               install Claude Code on the VM (official installer).
#   --claude-token-stdin   Claude Code OAuth token (from laptop `claude
#                          setup-token`) arrives on stdin; implies --claude.
#   --dotfiles-repo <url>  clone the given dotfiles repo on the VM and run
#                          its install.sh (best-effort, non-fatal). Default off.
#   --workspaces <a,b>     comma-separated workspace names the VM subscribes to:
#                          each gets notebook dirs and a pull=true entry in the
#                          VM's sync.toml (the VM pulls; the laptop only pushes).
#                          Default: cloud,grovetools. An empty value means no
#                          sync workspaces. `grove satellite up` passes this
#                          from its resolved [satellites.<name>.sync] config.
#
# Secrets policy: nothing secret in argv, terraform vars, or instance
# metadata. Tokens are piped via stdin. Sync tokens are minted ON the VM;
# the laptop's token is fetched back over SSH into
# ~/.config/grove/sync.token (0600).
#
# Stdin framing for secrets:
#   exactly one of --gh-token-stdin / --claude-token-stdin:
#       stdin is that raw token (first line; e.g. `gh auth token | ...`).
#   both flags together:
#       stdin is KEY=VALUE lines, order-free; blank lines and #-comments
#       are ignored:
#         GH_TOKEN=<github token>
#         CLAUDE_CODE_OAUTH_TOKEN=<token from `claude setup-token`>
set -euo pipefail

# Claude Code official native installer. Verified 2026-07-12: this URL
# 302s to downloads.claude.ai/claude-code-releases/bootstrap.sh. If it ever
# rots, check https://docs.anthropic.com/en/docs/claude-code/setup .
CLAUDE_INSTALL_URL="https://claude.ai/install.sh"

usage() {
  echo "usage: $0 <ssh-destination> [--gh-token-stdin] [--claude] [--claude-token-stdin] [--dotfiles-repo <url>] [--workspaces <a,b>]" >&2
  exit 2
}

DEST="${1:-}"
[ -n "$DEST" ] || usage
shift
GH_TOKEN_STDIN=false
CLAUDE=false
CLAUDE_TOKEN_STDIN=false
DOTFILES_REPO=""
WORKSPACES="cloud,grovetools"
while [ $# -gt 0 ]; do
  case "$1" in
    --gh-token-stdin) GH_TOKEN_STDIN=true ;;
    --claude) CLAUDE=true ;;
    --claude-token-stdin)
      CLAUDE_TOKEN_STDIN=true
      CLAUDE=true # a token without the binary is useless
      ;;
    --dotfiles-repo)
      [ $# -ge 2 ] || usage
      DOTFILES_REPO="$2"
      shift
      ;;
    --workspaces)
      [ $# -ge 2 ] || usage
      WORKSPACES="$2"
      shift
      ;;
    *) usage ;;
  esac
  shift
done

# Workspace names end up in remote paths and generated TOML — restrict them to
# a safe character class before anything ships. Empty --workspaces = none.
if [ -n "$WORKSPACES" ]; then
  IFS=',' read -ra WS_CHECK <<< "$WORKSPACES"
  for ws in ${WS_CHECK[@]+"${WS_CHECK[@]}"}; do
    case "$ws" in
      '' | *[!A-Za-z0-9._-]*)
        echo "--workspaces: invalid workspace name '$ws' (allowed: A-Za-z0-9._-, comma-separated)" >&2
        exit 2
        ;;
    esac
  done
fi

# --- read secrets from stdin (never argv) -----------------------------------
SECRET_GH_TOKEN=""
SECRET_CLAUDE_TOKEN=""
if $GH_TOKEN_STDIN && $CLAUDE_TOKEN_STDIN; then
  # two secrets -> self-describing KEY=VALUE framing
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      GH_TOKEN=*) SECRET_GH_TOKEN="${line#GH_TOKEN=}" ;;
      CLAUDE_CODE_OAUTH_TOKEN=*) SECRET_CLAUDE_TOKEN="${line#CLAUDE_CODE_OAUTH_TOKEN=}" ;;
      '' | \#*) ;;
      *)
        echo "unrecognized stdin line: with both token flags, stdin must be GH_TOKEN=... / CLAUDE_CODE_OAUTH_TOKEN=... lines" >&2
        exit 2
        ;;
    esac
  done
  [ -n "$SECRET_GH_TOKEN" ] || { echo "missing GH_TOKEN=... on stdin" >&2; exit 2; }
  [ -n "$SECRET_CLAUDE_TOKEN" ] || { echo "missing CLAUDE_CODE_OAUTH_TOKEN=... on stdin" >&2; exit 2; }
elif $GH_TOKEN_STDIN; then
  IFS= read -r SECRET_GH_TOKEN || true
  [ -n "$SECRET_GH_TOKEN" ] || { echo "--gh-token-stdin: no token on stdin" >&2; exit 2; }
elif $CLAUDE_TOKEN_STDIN; then
  IFS= read -r SECRET_CLAUDE_TOKEN || true
  [ -n "$SECRET_CLAUDE_TOKEN" ] || { echo "--claude-token-stdin: no token on stdin" >&2; exit 2; }
fi

SSH=(ssh -o StrictHostKeyChecking=accept-new "$DEST")
# Remote steps run as login shells so /etc/profile.d/grove-satellite.sh
# (go, zig, grove bin dirs) is on PATH.
rsh() { "${SSH[@]}" 'bash -l -s'; }
log() { printf '\n==> %s\n' "$*" >&2; }

log "[1/8] waiting for VM startup script (installs toolchains; first boot takes a few minutes)"
started=false
for _ in $(seq 1 90); do
  if "${SSH[@]}" test -f /var/lib/grove-satellite/startup-done 2>/dev/null; then
    started=true
    break
  fi
  sleep 10
done
if ! $started; then
  echo "startup script never finished; inspect with:" >&2
  echo "  ${SSH[*]} sudo tail -50 /var/log/grove-satellite-startup.log" >&2
  exit 1
fi

log "[2/8] GitHub auth on the VM"
if $GH_TOKEN_STDIN; then
  # the token travels on gh's stdin on the VM — never in argv
  printf '%s\n' "$SECRET_GH_TOKEN" | "${SSH[@]}" 'gh auth login --hostname github.com --with-token'
fi
rsh <<'REMOTE'
set -euo pipefail
gh auth status --hostname github.com
gh auth setup-git --hostname github.com
# .gitmodules mixes scp-style (git@github.com:) and ssh:// URLs; rewrite
# both to https so the gh credential helper covers submodule clones too.
# add-if-missing keeps this idempotent (plain set errors on multi-values).
for u in "git@github.com:" "ssh://git@github.com/"; do
  git config --global --get-all url."https://github.com/".insteadOf 2>/dev/null | grep -qxF "$u" \
    || git config --global --add url."https://github.com/".insteadOf "$u"
done
git config --global user.name "grove-satellite"
git config --global user.email "grove-satellite@localhost"
REMOTE

log "[3/8] cloning grovetools ecosystem (superrepo + submodules, pinned SHAs)"
rsh <<'REMOTE'
set -euo pipefail
mkdir -p "$HOME/code"
if [ ! -d "$HOME/code/grovetools/.git" ]; then
  git clone https://github.com/grovetools/grovetools.git "$HOME/code/grovetools"
fi
cd "$HOME/code/grovetools"
git submodule update --init --jobs 8
# Self-heal aborted submodule clones: an interrupted `submodule update` can
# leave a submodule with HEAD recorded but the worktree never checked out
# (directory empty apart from .git). A plain re-run treats those as up to
# date, so reset each such worktree to its recorded HEAD. Idempotent:
# healthy submodules have files beside .git and are skipped.
git config --file .gitmodules --get-regexp '^submodule\..*\.path$' \
  | awk '{print $2}' \
  | while IFS= read -r sm; do
      [ -e "$sm/.git" ] || continue
      if [ -z "$(find "$sm" -mindepth 1 -maxdepth 1 ! -name .git -print -quit)" ]; then
        echo "self-heal: restoring aborted checkout in $sm" >&2
        git -C "$sm" reset --hard HEAD
      fi
    done
REMOTE

log "[4/8] building grove + ecosystem tools (grove build; slowest step)"
rsh <<'REMOTE'
set -euo pipefail
# Preflight: the compositor source must include the linux portability fix
# (compositor 1c97344 "fix(build): linux portability for termios ioctls
# and cgo link flags" — per-GOOS termios constants + split cgo LDFLAGS).
# Earlier pins hardcode Darwin-only ioctls and link flags and cannot build
# on Linux. We deliberately fail fast instead of patching source on the
# VM: pinned SHAs are the source of truth here. pty/termios_linux.go is
# introduced by that commit, so its presence is the marker.
if [ ! -f "$HOME/code/grovetools/compositor/pty/termios_linux.go" ]; then
  echo "compositor pin predates the linux portability fix (compositor 1c97344):" >&2
  echo "advance the grovetools superrepo's compositor pin past that commit, then re-run" >&2
  exit 1
fi
# grove's binary links the compositor zig lib (-lcompositor); build it
# (and its libghostty dependency) before grove itself.
cd "$HOME/code/grovetools/compositor"
make zig
cd "$HOME/code/grovetools/grove"
make build
BIN_DIR="$HOME/.local/share/grove/bin"
mkdir -p "$BIN_DIR"
ln -sf "$HOME/code/grovetools/grove/bin/grove" "$BIN_DIR/grove"
export PATH="$BIN_DIR:$PATH"
cd "$HOME/code/grovetools"
# `grove build` at ecosystem root reports "No projects to build after
# filtering" when groved isn't running yet (chicken-and-egg: groved is
# built by this step). The root Makefile iterates all packages daemon-free.
make build > /tmp/grove-build.log 2>&1 || true
grove dev cwd || true
missing=""
for b in grove groved flow nb treemux tuimux; do
  command -v "$b" >/dev/null || missing="$missing $b"
done
if [ -n "$missing" ]; then
  echo "MISSING required binaries:$missing" >&2
  echo "see /tmp/grove-build.json and /tmp/grove-build.err on the VM" >&2
  exit 1
fi
echo "required binaries OK: grove groved flow nb treemux tuimux"
REMOTE

log "[5/8] VM grove configuration (grove.toml, sync.toml, notebook dirs)"
# The workspace list rides stdin's first line (same pattern as the dotfiles
# repo URL): the remote heredoc stays fully quoted, so nothing from the laptop
# is interpolated into remote shell — the names were validated above.
{
  printf '%s\n' "$WORKSPACES"
  cat <<'REMOTE'
set -euo pipefail
mkdir -p "$HOME/.config/grove"
WS_LIST=()
if [ -n "$GROVE_SYNC_WORKSPACES" ]; then
  IFS=',' read -ra WS_LIST <<< "$GROVE_SYNC_WORKSPACES"
fi
for ws in ${WS_LIST[@]+"${WS_LIST[@]}"}; do
  mkdir -p "$HOME/notebooks/grovetools/workspaces/$ws/inbox" \
           "$HOME/notebooks/grovetools/workspaces/$ws/plans" \
           "$HOME/notebooks/grovetools/workspaces/$ws/concepts" \
           "$HOME/notebooks/grovetools/workspaces/$ws/daily" \
           "$HOME/notebooks/grovetools/workspaces/$ws/quick"
done
cat > "$HOME/.config/grove/grove.toml" <<'CFG'
# grove-satellite PoC — written by satellite-bootstrap.sh
[groves.grovetools]
description = "Grove ecosystem (satellite)"
enabled = true
notebook = "grovetools"
path = "~/code/grovetools"

[notebooks.definitions.grovetools]
root_dir = "~/notebooks/grovetools"

[daemon.ssh]
enabled = true
CFG
{
  printf '# grove-satellite PoC — the VM PULLS (materializes) subscribed workspaces.\n'
  printf '# Written by satellite-bootstrap.sh (--workspaces %s).\n' "$GROVE_SYNC_WORKSPACES"
  printf 'server = "http://127.0.0.1:8788"\n'
  printf 'token_command = "cat ~/.config/grove/sync.token"\n'
  for ws in ${WS_LIST[@]+"${WS_LIST[@]}"}; do
    printf '\n[[workspaces]]\nname = "%s"\npull = true\n' "$ws"
  done
} > "$HOME/.config/grove/sync.toml"
REMOTE
} | "${SSH[@]}" 'IFS= read -r GROVE_SYNC_WORKSPACES; export GROVE_SYNC_WORKSPACES; exec bash -l -s'

log "[6/8] grove-syncd: build (CGO fts5), install, migrate, mint tokens, systemd"
rsh <<'REMOTE'
set -euo pipefail
cd "$HOME/code/grovetools/sync"
make build
sudo install -m 0755 bin/grove-syncd /usr/local/bin/grove-syncd
sudo mkdir -p /var/lib/grove-syncd
sudo /usr/local/bin/grove-syncd --data-dir /var/lib/grove-syncd migrate
# VM's own token (used by groved via sync.toml token_command)
if [ ! -f "$HOME/.config/grove/sync.token" ]; then
  sudo /usr/local/bin/grove-syncd --data-dir /var/lib/grove-syncd token create "vm-$(hostname)" > "$HOME/.config/grove/sync.token.tmp"
  mv "$HOME/.config/grove/sync.token.tmp" "$HOME/.config/grove/sync.token"
  chmod 600 "$HOME/.config/grove/sync.token"
fi
# Laptop token: minted here (before the service starts), stashed root-only;
# step 7 fetches and deletes it.
if [ ! -f /root/laptop-sync.token ] && ! sudo test -f /root/laptop-sync.token.fetched; then
  sudo sh -c '/usr/local/bin/grove-syncd --data-dir /var/lib/grove-syncd token create laptop > /root/laptop-sync.token && chmod 600 /root/laptop-sync.token'
fi
sudo cp "$HOME/code/grovetools/sync/systemd/grove-syncd.service" /etc/systemd/system/grove-syncd.service
sudo systemctl daemon-reload
sudo systemctl enable --now grove-syncd
sleep 2
curl -fsS http://127.0.0.1:8788/healthz >/dev/null
echo "syncd healthy"
curl -fsS -X POST -H "Authorization: Bearer $(cat "$HOME/.config/grove/sync.token")" \
  -H 'Content-Type: application/json' -d '{}' http://127.0.0.1:8788/sync/capabilities >/dev/null
echo "vm token accepted"
REMOTE

log "[7/8] fetching laptop sync token"
LOCAL_TOKEN_FILE="$HOME/.config/grove/sync.token"
# Probe /sync/capabilities ON the VM (syncd listens on VM loopback only),
# feeding the laptop's token over SSH stdin — secrets never ride argv.
# Prints the HTTP status code; "000" when the request never completed
# (network/ssh failure). No -f: auth rejections must yield their code, not
# a curl error.
probe_laptop_token() {
  # shellcheck disable=SC2016 # $(cat) must expand on the VM, not here
  "${SSH[@]}" 'curl -sS -o /dev/null -w "%{http_code}" -X POST -H "Authorization: Bearer $(cat)" -H "Content-Type: application/json" -d "{}" http://127.0.0.1:8788/sync/capabilities' < "$LOCAL_TOKEN_FILE"
}
NEED_FETCH=true
if [ -f "$LOCAL_TOKEN_FILE" ]; then
  # A token on disk isn't proof it matches THIS VM (a recreate mints a fresh
  # syncd store) — verify before keeping it.
  code=$(probe_laptop_token) || code="000"
  case "$code" in
    2??)
      echo "laptop token already present at $LOCAL_TOKEN_FILE and accepted by the VM (HTTP $code) — keeping it" >&2
      NEED_FETCH=false
      ;;
    401 | 403)
      echo "laptop token at $LOCAL_TOKEN_FILE rejected by the VM (HTTP $code) — stale (VM recreated?); fetching a fresh one" >&2
      ;;
    *)
      echo "could not verify laptop token against the VM (HTTP $code) — refusing to keep an unverified token" >&2
      echo "check grove-syncd on the VM (sudo systemctl status grove-syncd) and re-run" >&2
      exit 1
      ;;
  esac
fi
if $NEED_FETCH; then
  mkdir -p "$HOME/.config/grove"
  # Self-heal: a successful fetch deletes the VM-side stash and drops the
  # .fetched sentinel, which blocks step 6 from re-minting. A stale laptop
  # token therefore needs a fresh mint here, same command step 6 uses.
  if ! "${SSH[@]}" 'sudo test -f /root/laptop-sync.token'; then
    echo "no laptop token stashed on the VM — re-minting" >&2
    "${SSH[@]}" "sudo sh -c '/usr/local/bin/grove-syncd --data-dir /var/lib/grove-syncd token create laptop > /root/laptop-sync.token && chmod 600 /root/laptop-sync.token'"
  fi
  "${SSH[@]}" 'sudo cat /root/laptop-sync.token' > "$LOCAL_TOKEN_FILE.tmp"
  if [ ! -s "$LOCAL_TOKEN_FILE.tmp" ]; then
    echo "failed to fetch laptop token from VM (/root/laptop-sync.token)" >&2
    rm -f "$LOCAL_TOKEN_FILE.tmp"
    exit 1
  fi
  mv "$LOCAL_TOKEN_FILE.tmp" "$LOCAL_TOKEN_FILE"
  chmod 600 "$LOCAL_TOKEN_FILE"
  "${SSH[@]}" 'sudo sh -c "rm -f /root/laptop-sync.token && touch /root/laptop-sync.token.fetched"'
  # Confirm the fresh token actually opens the door before declaring success.
  code=$(probe_laptop_token) || code="000"
  case "$code" in
    2??)
      echo "laptop token written to $LOCAL_TOKEN_FILE (verified, HTTP $code)" >&2
      ;;
    *)
      echo "freshly fetched laptop token still rejected by the VM (HTTP $code) — syncd token store looks inconsistent; inspect the VM" >&2
      exit 1
      ;;
  esac
fi

log "[8/8] groved under systemd --user"
rsh <<'REMOTE'
set -euo pipefail
mkdir -p "$HOME/.config/systemd/user"
cat > "$HOME/.config/systemd/user/groved.service" <<'UNIT'
[Unit]
Description=grove daemon (groved) — grove-satellite PoC

[Service]
ExecStart=%h/.local/share/grove/bin/groved start
Restart=on-failure
RestartSec=2
Environment=PATH=%h/.local/share/grove/bin:%h/.grove/bin:%h/.local/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin:/snap/bin

[Install]
WantedBy=default.target
UNIT
XDG_RUNTIME_DIR="/run/user/$(id -u)"
export XDG_RUNTIME_DIR
systemctl --user daemon-reload
systemctl --user enable --now groved
sleep 3
systemctl --user is-active groved
echo "groved active"
REMOTE

if $CLAUDE; then
  log "[claude] Claude Code install + auth (opt-in)"
  # The token and installer URL ride the first two stdin lines; the script
  # follows on the same stream (bash -s reads the rest). Nothing secret
  # appears in argv on either machine. Runs after step 8 so groved exists
  # and can be try-restarted to pick up the new environment.
  {
    printf '%s\n%s\n' "$SECRET_CLAUDE_TOKEN" "$CLAUDE_INSTALL_URL"
    cat <<'REMOTE'
set -euo pipefail
export PATH="$HOME/.local/bin:$PATH"
if command -v claude >/dev/null 2>&1; then
  echo "claude already installed: $(claude --version 2>/dev/null || echo '(version unknown)')"
else
  curl -fsSL "$GROVE_CLAUDE_INSTALL_URL" | bash
fi
if [ -z "$GROVE_CLAUDE_TOKEN" ]; then
  echo "no claude token provided (--claude without --claude-token-stdin): install only, skipping auth"
  exit 0
fi
umask 077
# (a) systemd --user environment: groved and every agent it spawns see the
# token via the user manager's environment.d.
mkdir -p "$HOME/.config/environment.d"
printf 'CLAUDE_CODE_OAUTH_TOKEN=%s\n' "$GROVE_CLAUDE_TOKEN" > "$HOME/.config/environment.d/claude.conf"
chmod 600 "$HOME/.config/environment.d/claude.conf"
# (b) profile.d-style snippet for interactive shells, sourced from
# ~/.profile (login) and ~/.bashrc (interactive non-login).
mkdir -p "$HOME/.config/grove-satellite"
printf "export CLAUDE_CODE_OAUTH_TOKEN='%s'\n" "$GROVE_CLAUDE_TOKEN" > "$HOME/.config/grove-satellite/claude-env.sh"
chmod 600 "$HOME/.config/grove-satellite/claude-env.sh"
snippet='[ -f "$HOME/.config/grove-satellite/claude-env.sh" ] && . "$HOME/.config/grove-satellite/claude-env.sh"'
for rc in "$HOME/.profile" "$HOME/.bashrc"; do
  grep -qsF 'grove-satellite/claude-env.sh' "$rc" \
    || printf '\n# grove-satellite: Claude Code token\n%s\n' "$snippet" >> "$rc"
done
# environment.d is only read when the user manager (re)starts; import the
# var into the live manager and bounce groved (if running) so agents
# spawned from here on inherit it without a reboot.
XDG_RUNTIME_DIR="/run/user/$(id -u)"
export XDG_RUNTIME_DIR
export CLAUDE_CODE_OAUTH_TOKEN="$GROVE_CLAUDE_TOKEN"
systemctl --user import-environment CLAUDE_CODE_OAUTH_TOKEN || true
systemctl --user try-restart groved 2>/dev/null || true
# Interactive first-run: without ~/.claude.json claude walks the onboarding
# flow (theme picker, then account login) even though CLAUDE_CODE_OAUTH_TOKEN
# is set and valid — verified live on the 2026-07-13 rehearsal VM. Pre-marking
# onboarding complete makes the first interactive run use the token straight
# away. Merge, don't clobber: repeat bootstraps keep accumulated state.
if [ -f "$HOME/.claude.json" ]; then
  jq '. + {hasCompletedOnboarding: true}' "$HOME/.claude.json" > "$HOME/.claude.json.tmp" \
    && mv "$HOME/.claude.json.tmp" "$HOME/.claude.json"
else
  printf '{"hasCompletedOnboarding": true}\n' > "$HOME/.claude.json"
fi
chmod 600 "$HOME/.claude.json"
echo "claude onboarding pre-completed (interactive runs use the token)"
claude --version
echo "claude installed + token configured"
REMOTE
  } | "${SSH[@]}" 'IFS= read -r GROVE_CLAUDE_TOKEN; IFS= read -r GROVE_CLAUDE_INSTALL_URL; export GROVE_CLAUDE_TOKEN GROVE_CLAUDE_INSTALL_URL; exec bash -l -s'
fi

if [ -n "$DOTFILES_REPO" ]; then
  log "[dotfiles] cloning $DOTFILES_REPO and running its install.sh (best-effort)"
  # The repo URL rides stdin like the secrets do (it isn't secret, but this
  # keeps one pattern for out-of-band values). Private repos work because
  # step 2 already ran `gh auth setup-git` + the git@->https insteadOf
  # rewrites: the gh credential helper supplies the token, so it never
  # appears in argv or in any URL written to disk.
  # The user's dotfiles repo IS ~/.config in spirit: it ships an install.sh
  # that platform-detects GCP via the metadata server and runs
  # gcp/setup-linux.sh. We clone to a staging dir and let install.sh do its
  # thing. Non-fatal: a broken dotfiles setup must not sink the bootstrap.
  if ! {
    printf '%s\n' "$DOTFILES_REPO"
    cat <<'REMOTE'
set -euo pipefail
staging="$HOME/.local/share/grove-satellite/dotfiles"
if [ -d "$staging/.git" ]; then
  git -C "$staging" pull --ff-only \
    || echo "dotfiles: pull failed; keeping existing checkout" >&2
else
  mkdir -p "$(dirname "$staging")"
  git clone "$GROVE_DOTFILES_REPO" "$staging"
fi
if [ -f "$staging/install.sh" ]; then
  (cd "$staging" && bash ./install.sh)
else
  echo "dotfiles: no install.sh at repo root — cloned only, nothing run" >&2
fi
REMOTE
  } | "${SSH[@]}" 'IFS= read -r GROVE_DOTFILES_REPO; export GROVE_DOTFILES_REPO; exec bash -l -s'; then
    echo "WARNING: dotfiles step failed — continuing, bootstrap is unaffected (best-effort by design)" >&2
  fi
fi

log "satellite bootstrap complete"
cat >&2 <<SUMMARY

Next steps (see grove/docs/satellite.md):
  1. Laptop half: 'grove satellite up' automates it (push-only sync.toml merge +
     registry sync_local_port for the daemon's syncd forward). Reload the daemon:
       groved upgrade --global
     (it binds 127.0.0.1:8788 and forwards note-sync over its pinned SSH
     connection — no manual tunnel). Standalone runs of this script can still
     tunnel manually: ssh -N -L 8788:127.0.0.1:8788 $DEST
  2. Watch a note round-trip:         edit on laptop -> appears on VM under ~/notebooks/grovetools/
  3. Drive the VM from a local pane:  ssh -t $DEST 'bash -lc treemux'
SUMMARY
