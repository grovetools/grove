#!/usr/bin/env bash
# grove-forge-backup — one off-VM snapshot of everything the forge's boot disk
# holds that cannot be rebuilt from somewhere else.
#
# Installed by `grove forge up` over SSH (not by the startup script, which runs
# once per VM lifetime and therefore cannot converge an existing forge). Driven
# by grove-forge-backup.timer. Configuration arrives in /etc/grove-forge/backup.env,
# which is 0600 and is the ONLY file here that holds a secret (the ntfy topic).
#
# Design notes that are not obvious from the code:
#
#   * Forgejo is backed up with VACUUM INTO plus a tar of the repo tree, NOT
#     with `forgejo dump`. Job 17's restore rehearsal found dump's SQL export
#     emits unistr() literals that need SQLite >= 3.51; debian-12 ships 3.40.1,
#     so the load dies partway through and leaves a HALF-POPULATED database —
#     failing late and partially, the worst shape a restore path can have.
#
#   * app.ini and secrets.env are deliberately NOT in the artifact. They hold
#     SECRET_KEY, INTERNAL_TOKEN and the JWT secrets; shipping them would put a
#     live credential at rest in a bucket. The consequence is stated in the
#     runbook rather than hidden: SECRET_KEY-encrypted material (2FA
#     enrolments, stored mirror credentials) does not survive a restore.
#
#   * Blob mirroring runs WITHOUT --delete-unmatched. Server-side blob GC must
#     never propagate a deletion into the backup; bucket versioning plus the
#     lifecycle rules are what keep that from growing without bound.
#
#   * LAST_SUCCESS is written last, and only on total success. Its staleness is
#     the alerting signal — the one signal that also catches a timer that
#     stopped firing, which no per-run alert can.
set -euo pipefail

CONF=/etc/grove-forge/backup.env
# shellcheck disable=SC1090
[ -r "$CONF" ] && . "$CONF"

: "${BACKUP_BUCKET:?BACKUP_BUCKET not set (is /etc/grove-forge/backup.env installed?)}"
: "${BACKUP_PREFIX:=forge}"
: "${SYNCD_DATA_DIR:=/var/lib/private/grove-syncd}"
: "${FORGEJO_DATA_DIR:=/var/lib/forgejo}"
: "${FORGEJO_REPO_ROOT:=/var/lib/forgejo/data/forgejo-repositories}"
: "${LOCAL_KEEP:=3}"
: "${NTFY_URL:=https://ntfy.sh}"
: "${NTFY_TOPIC:=}"

STAGE=/var/backups/grove-forge
GS="gs://$BACKUP_BUCKET/$BACKUP_PREFIX"
TS=$(date -u +%Y%m%dT%H%M%SZ)
STARTED=$(date -u +%s)

install -d -m 0700 "$STAGE"

# alert <subject> <body> — best effort. A failed alert must never mask the
# failure it is reporting, so it is unconditionally forgiven.
alert() {
  local title="$1" body="$2"
  echo "ALERT: $title — $body" >&2
  [ -n "$NTFY_TOPIC" ] || return 0
  curl -fsS --max-time 15 \
    -H "Title: $title" -H "Priority: high" -H "Tags: warning,floppy_disk" \
    -d "$body" "$NTFY_URL/$NTFY_TOPIC" >/dev/null 2>&1 || true
}

fail() {
  local what="$1"
  alert "grove forge backup FAILED" \
    "$(hostname): $what at $(date -u +%Y-%m-%dT%H:%M:%SZ). LAST_SUCCESS is now stale; see journalctl -u grove-forge-backup."
  exit 1
}
trap 'fail "backup aborted (line $LINENO)"' ERR

echo "== grove-forge-backup $TS -> $GS =="

# ---- grove-syncd -----------------------------------------------------------
# VACUUM INTO through the server's own verb: a consistent single-file snapshot
# that is safe to take while the server is serving. Never `cp` the live db —
# the WAL tears it.
if systemctl list-unit-files grove-syncd.service >/dev/null 2>&1 && [ -d "$SYNCD_DATA_DIR" ]; then
  SNAP="$STAGE/syncd-$TS.db"
  GROVE_LOG_FILE=/tmp/grove-forge-backup-syncd.log \
    /usr/local/bin/grove-syncd --data-dir "$SYNCD_DATA_DIR" backup "$SNAP" >/dev/null
  sqlite3 "$SNAP" 'PRAGMA integrity_check;' | grep -qx ok || fail "syncd snapshot failed integrity_check"
  zstd -q -f --rm "$SNAP" -o "$SNAP.zst"
  gcloud storage cp "$SNAP.zst" "$GS/syncd/db/syncd-$TS.db.zst" --quiet
  echo "syncd db: $(stat -c%s "$SNAP.zst") bytes -> $GS/syncd/db/syncd-$TS.db.zst"

  if [ -d "$SYNCD_DATA_DIR/blobs" ]; then
    # Content-addressed and immutable, so a mirror is a valid backup and no
    # timestamping is needed. No --delete-unmatched: see the header.
    gcloud storage rsync -r "$SYNCD_DATA_DIR/blobs" "$GS/syncd/blobs" --quiet
    echo "syncd blobs mirrored"
  fi
else
  echo "grove-syncd not present; skipping"
fi

# ---- forgejo ---------------------------------------------------------------
if [ -f "$FORGEJO_DATA_DIR/data/forgejo.db" ]; then
  FSNAP="$STAGE/forgejo-$TS.db"
  rm -f "$FSNAP"
  # VACUUM INTO on the live database: consistent, and it checkpoints the WAL
  # into the copy rather than leaving a torn page set behind.
  sqlite3 "$FORGEJO_DATA_DIR/data/forgejo.db" "VACUUM INTO '$FSNAP'"
  sqlite3 "$FSNAP" 'PRAGMA integrity_check;' | grep -qx ok || fail "forgejo snapshot failed integrity_check"
  zstd -q -f --rm "$FSNAP" -o "$FSNAP.zst"
  gcloud storage cp "$FSNAP.zst" "$GS/forgejo/db/forgejo-$TS.db.zst" --quiet
  echo "forgejo db: $(stat -c%s "$FSNAP.zst") bytes -> $GS/forgejo/db/forgejo-$TS.db.zst"

  if [ -d "$FORGEJO_REPO_ROOT" ]; then
    RSNAP="$STAGE/forgejo-repos-$TS.tar.zst"
    tar -C "$(dirname "$FORGEJO_REPO_ROOT")" -cf - "$(basename "$FORGEJO_REPO_ROOT")" \
      | zstd -q -f -o "$RSNAP"
    gcloud storage cp "$RSNAP" "$GS/forgejo/repos/forgejo-repos-$TS.tar.zst" --quiet
    echo "forgejo repos: $(stat -c%s "$RSNAP") bytes -> $GS/forgejo/repos/forgejo-repos-$TS.tar.zst"
  fi

  # The repo root is a property of the DESTINATION's app.ini, not of the dump
  # (job 17 finding R2: a restore that unpacks in place onto a differently
  # configured target yields a database listing repositories the server cannot
  # find). Record what this source used so a restore can compare.
  echo "$FORGEJO_REPO_ROOT" > "$STAGE/forgejo-repo-root.txt"
  gcloud storage cp "$STAGE/forgejo-repo-root.txt" "$GS/forgejo/repo-root.txt" --quiet
else
  echo "forgejo database not present; skipping"
fi

# ---- manifest + success marker ---------------------------------------------
ELAPSED=$(( $(date -u +%s) - STARTED ))
cat > "$STAGE/MANIFEST-$TS.json" <<JSON
{
  "timestamp": "$TS",
  "host": "$(hostname)",
  "elapsed_seconds": $ELAPSED,
  "syncd_db": "$GS/syncd/db/syncd-$TS.db.zst",
  "forgejo_db": "$GS/forgejo/db/forgejo-$TS.db.zst",
  "forgejo_repos": "$GS/forgejo/repos/forgejo-repos-$TS.tar.zst",
  "excludes": ["app.ini", "secrets.env"]
}
JSON
gcloud storage cp "$STAGE/MANIFEST-$TS.json" "$GS/manifests/MANIFEST-$TS.json" --quiet

# Written LAST and only here: everything above succeeded, so this timestamp is
# a real recovery point rather than an optimistic one.
printf '%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$STAGE/LAST_SUCCESS"
gcloud storage cp "$STAGE/LAST_SUCCESS" "$GS/LAST_SUCCESS" --quiet

# Prune the local staging dir; the bucket is the archive, this is just a cache.
# shellcheck disable=SC2012
ls -1t "$STAGE"/syncd-*.db.zst 2>/dev/null | tail -n +$((LOCAL_KEEP + 1)) | xargs -r rm -f
# shellcheck disable=SC2012
ls -1t "$STAGE"/forgejo-*.db.zst 2>/dev/null | tail -n +$((LOCAL_KEEP + 1)) | xargs -r rm -f
# shellcheck disable=SC2012
ls -1t "$STAGE"/forgejo-repos-*.tar.zst 2>/dev/null | tail -n +$((LOCAL_KEEP + 1)) | xargs -r rm -f
# shellcheck disable=SC2012
ls -1t "$STAGE"/MANIFEST-*.json 2>/dev/null | tail -n +$((LOCAL_KEEP + 1)) | xargs -r rm -f

trap - ERR
echo "== backup OK in ${ELAPSED}s =="
