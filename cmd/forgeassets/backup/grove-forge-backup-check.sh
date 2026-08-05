#!/usr/bin/env bash
# grove-forge-backup-check — the staleness alarm.
#
# The backup script alerts when a RUN fails. This alerts when runs stop
# happening at all: a masked timer, a wedged VM, a bucket whose IAM binding was
# revoked. Those are the failures that are invisible to per-run alerting, and
# they are also the ones that matter most, because they are silent for as long
# as nobody looks.
#
# The check reads LAST_SUCCESS from the BUCKET, not from local disk: an alarm
# that trusts the same disk it is verifying tells you nothing about whether the
# off-VM copy exists.
set -euo pipefail

CONF=/etc/grove-forge/backup.env
# shellcheck disable=SC1090
[ -r "$CONF" ] && . "$CONF"

: "${BACKUP_BUCKET:?BACKUP_BUCKET not set}"
: "${BACKUP_PREFIX:=forge}"
: "${STALE_AFTER_SECONDS:=172800}"   # 48h
: "${NTFY_URL:=https://ntfy.sh}"
: "${NTFY_TOPIC:=}"

GS="gs://$BACKUP_BUCKET/$BACKUP_PREFIX"

alert() {
  local title="$1" body="$2"
  echo "ALERT: $title — $body" >&2
  [ -n "$NTFY_TOPIC" ] || return 0
  curl -fsS --max-time 15 \
    -H "Title: $title" -H "Priority: high" -H "Tags: warning,hourglass" \
    -d "$body" "$NTFY_URL/$NTFY_TOPIC" >/dev/null 2>&1 || true
}

if ! MARKER=$(gcloud storage cat "$GS/LAST_SUCCESS" 2>/dev/null); then
  alert "grove forge backup STALE" \
    "$(hostname): $GS/LAST_SUCCESS is unreadable — no successful backup has ever landed, or the bucket/IAM binding is gone."
  exit 1
fi

LAST=$(date -u -d "$MARKER" +%s 2>/dev/null || echo 0)
NOW=$(date -u +%s)
AGE=$(( NOW - LAST ))

if [ "$LAST" -eq 0 ]; then
  alert "grove forge backup STALE" "$(hostname): LAST_SUCCESS is unparseable ('$MARKER')."
  exit 1
fi

# Report in whatever unit makes the number readable. "0h ago (threshold 0h)"
# is what integer hours produce for a short threshold, and an alert whose own
# numbers look wrong is an alert people learn to distrust.
human() {
  if [ "$1" -lt 3600 ]; then echo "$(($1 / 60))m"; else echo "$(($1 / 3600))h"; fi
}

if [ "$AGE" -gt "$STALE_AFTER_SECONDS" ]; then
  alert "grove forge backup STALE" \
    "$(hostname): last successful backup was $(human "$AGE") ago (threshold $(human "$STALE_AFTER_SECONDS")), at $MARKER."
  exit 1
fi

echo "backup fresh: last success $MARKER ($((AGE / 60))m ago)"
