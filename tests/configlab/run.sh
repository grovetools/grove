#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 --grove PATH --groved PATH --artifacts DIR [--sandbox DIR]" >&2
  exit 2
}

GROVE_CLI=""
GROVED_CLI=""
ARTIFACTS=""
SANDBOX=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --grove) GROVE_CLI=${2:-}; shift 2 ;;
    --groved) GROVED_CLI=${2:-}; shift 2 ;;
    --artifacts) ARTIFACTS=${2:-}; shift 2 ;;
    --sandbox) SANDBOX=${2:-}; shift 2 ;;
    *) usage ;;
  esac
done
[ -x "$GROVE_CLI" ] && [ -x "$GROVED_CLI" ] && [ -n "$ARTIFACTS" ] || usage
GROVE_CLI=$(cd "$(dirname "$GROVE_CLI")" && pwd)/$(basename "$GROVE_CLI")
GROVED_CLI=$(cd "$(dirname "$GROVED_CLI")" && pwd)/$(basename "$GROVED_CLI")
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
FIXTURES="$SCRIPT_DIR/fixtures"
mkdir -p "$ARTIFACTS"
ARTIFACTS=$(cd "$ARTIFACTS" && pwd)
if [ -z "$SANDBOX" ]; then
  SANDBOX=$(mktemp -d /tmp/grove-configlab.XXXXXX)
  REMOVE_SANDBOX=1
else
  mkdir -p "$SANDBOX"
  SANDBOX=$(cd "$SANDBOX" && pwd)
  REMOVE_SANDBOX=0
fi
DAEMON_PID=""
DAEMON_RUNTIME=$(mktemp -d /tmp/gvcld.XXXXXX)
cleanup() {
  if [ -n "$DAEMON_PID" ]; then
    kill -TERM "$DAEMON_PID" 2>/dev/null || true
    wait "$DAEMON_PID" 2>/dev/null || true
  fi
  rm -rf "$DAEMON_RUNTIME"
  if [ "$REMOVE_SANDBOX" = 1 ]; then rm -rf "$SANDBOX"; fi
}
trap cleanup EXIT INT TERM

export HOME="$SANDBOX/home"
export GROVE_HOME="$SANDBOX/grove-home"
export XDG_CONFIG_HOME="$SANDBOX/xdg/config"
export XDG_DATA_HOME="$SANDBOX/xdg/data"
export XDG_STATE_HOME="$SANDBOX/xdg/state"
export XDG_CACHE_HOME="$SANDBOX/xdg/cache"
export XDG_RUNTIME_DIR="$SANDBOX/run"
# Do not let an operator's process environment add config layers or activate
# config-lab fault injection. Individual probes opt into the latter with
# command-local env assignments below.
unset GROVE_CONFIG_OVERLAY GROVE_CONFIGLAB GROVE_CONFIGLAB_FAIL_AFTER
export GROVE_SCOPE=
export GROVE_DAEMON_PAIR_PID=
export GROVE_BIN=
mkdir -p "$HOME" "$GROVE_HOME" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_STATE_HOME" "$XDG_CACHE_HOME" "$XDG_RUNTIME_DIR" "$SANDBOX/work"
cd "$SANDBOX/work"

fail() { echo "config-lab: FAIL: $*" >&2; exit 1; }
expect_fail() {
  local out=$1; shift
  if "$@" >"$out" 2>"$out.stderr"; then
    fail "command unexpectedly succeeded: $*"
  fi
}
config_dir() { printf '%s/config/grove' "$GROVE_HOME"; }
reset_home() {
  rm -rf "$GROVE_HOME" "$SANDBOX/case"
  mkdir -p "$(config_dir)" "$SANDBOX/case"
}
render_fixture() {
  local input=$1 output=$2
  python3 - "$input" "$output" "$SANDBOX/case" <<'PY'
import pathlib, sys
src, dst, root = sys.argv[1:]
r = pathlib.Path(root)
values = {
    "@NOTES_ROOT@": str(r / "notes"),
    "@CARD_NOTES_ROOT@": str(r / "card-notes"),
    "@GLOBAL_NOTES_ROOT@": str(r / "global-notes"),
    "@CODE_ROOT@": str(r / "code"),
    "@ECOSYSTEM_ROOT@": str(r / "ecosystem"),
    "@EXTRA_ROOT@": str(r / "extra"),
}
text = pathlib.Path(src).read_text()
for key, value in values.items(): text = text.replace(key, value)
pathlib.Path(dst).write_text(text)
PY
}
seed_legacy() {
  reset_home
  mkdir -p "$SANDBOX/case/notes" "$SANDBOX/case/card-notes" "$SANDBOX/case/code" "$SANDBOX/case/extra" "$SANDBOX/case/ecosystem"
  render_fixture "$FIXTURES/legacy-grove.toml.in" "$(config_dir)/grove.toml"
  render_fixture "$FIXTURES/legacy-machine.toml.in" "$(config_dir)/machine.toml"
  cp "$FIXTURES/ecosystem-card.toml" "$SANDBOX/case/ecosystem/grove.toml"
}
file_digest() { shasum -a 256 "$1" | awk '{print $1}'; }
json_assert() {
  local file=$1 expression=$2
  python3 - "$file" "$expression" <<'PY'
import json, sys
p, expr = sys.argv[1:]
data = json.load(open(p))
if not eval(expr, {"__builtins__": {"any": any, "len": len}}, {"d": data}):
    raise SystemExit(f"assertion failed for {p}: {expr}\n{data!r}")
PY
}

# V2: preview is non-mutating; explicit confirmation applies the migration;
# the migration itself emits its independently normalized before/after proof.
seed_legacy
GROVE_TOML="$(config_dir)/grove.toml"
MACHINE_TOML="$(config_dir)/machine.toml"
CARD_TOML="$SANDBOX/case/ecosystem/grove.toml"
BEFORE_GROVE=$(file_digest "$GROVE_TOML")
BEFORE_MACHINE=$(file_digest "$MACHINE_TOML")
BEFORE_CARD=$(file_digest "$CARD_TOML")
"$GROVE_CLI" migrate --dry-run >"$ARTIFACTS/migration-diff.txt" 2>"$ARTIFACTS/migration-diff.stderr"
[ "$(file_digest "$GROVE_TOML")" = "$BEFORE_GROVE" ] || fail "dry-run changed grove.toml"
[ "$(file_digest "$MACHINE_TOML")" = "$BEFORE_MACHINE" ] || fail "dry-run changed machine.toml"
[ "$(file_digest "$CARD_TOML")" = "$BEFORE_CARD" ] || fail "dry-run changed ecosystem card"
[ ! -e "$(config_dir)/roots.toml" ] && [ ! -e "$(config_dir)/notebooks.toml" ] || fail "dry-run created recorded topology"
printf 'yes\n' | "$GROVE_CLI" migrate --evidence-dir "$ARTIFACTS" >"$ARTIFACTS/migration-confirm.txt" 2>"$ARTIFACTS/migration-confirm.stderr"
cmp -s "$ARTIFACTS/before-effective.json" "$ARTIFACTS/after-effective.json" || fail "normalized effective topology changed"
[ ! -s "$ARTIFACTS/effective-equivalence.diff" ] || fail "equivalence diff is not empty"
grep -q 'DEPRECATED: ecosystem.notebooks' "$CARD_TOML" || fail "ecosystem card was not annotated"
"$GROVE_CLI" config show --effective --json >"$ARTIFACTS/after-effective-envelope.json"
json_assert "$ARTIFACTS/after-effective-envelope.json" 'd["degraded"] is False and "groves" in d["effective"]'
"$GROVE_CLI" migrate --yes --json >"$ARTIFACTS/migration-noop-evidence.json"
json_assert "$ARTIFACTS/migration-noop-evidence.json" '"already migrated" in d.get("reason", "")'
# Preserve the successful transition evidence separately from the human confirm output.
seed_legacy
"$GROVE_CLI" migrate --yes --json --evidence-dir "$SANDBOX/second-proof" >"$ARTIFACTS/migration-evidence.json"
json_assert "$ARTIFACTS/migration-evidence.json" 'any(c.get("name") == "roots_recorded" and c.get("value", 0) > 0 for c in d.get("counts", []))'

# Canonical-filename collision: this is the exact legacy notebooks shape seen
# on a real machine. The sibling roots.toml is already modern, proving the
# mixed case while every probe remains inside the disposable GROVE_HOME.
reset_home
mkdir -p "$SANDBOX/case/notes" "$SANDBOX/case/card-notes" "$SANDBOX/case/global-notes" "$SANDBOX/case/code"
render_fixture "$FIXTURES/legacy-notebooks-collision.toml.in" "$(config_dir)/notebooks.toml"
cat >"$(config_dir)/roots.toml" <<EOF
[roots.code]
path = "$SANDBOX/case/code"
scan = true
notebook = "work"
EOF
COLLISION_BEFORE=$(file_digest "$(config_dir)/notebooks.toml")
ROOTS_BEFORE=$(file_digest "$(config_dir)/roots.toml")
"$GROVE_CLI" migrate --dry-run >"$ARTIFACTS/canonical-collision-dry-run.txt" 2>"$ARTIFACTS/canonical-collision-dry-run.stderr"
[ "$(file_digest "$(config_dir)/notebooks.toml")" = "$COLLISION_BEFORE" ] || fail "collision dry-run changed notebooks.toml"
[ "$(file_digest "$(config_dir)/roots.toml")" = "$ROOTS_BEFORE" ] || fail "collision dry-run changed modern roots.toml"
[ ! -e "$(config_dir)/notebooks.legacy-compat.toml" ] || fail "collision dry-run created compatibility config"
printf 'yes\n' | "$GROVE_CLI" migrate --evidence-dir "$SANDBOX/collision-proof" >"$ARTIFACTS/canonical-collision-confirm.txt" 2>"$ARTIFACTS/canonical-collision-confirm.stderr"
cmp -s "$SANDBOX/collision-proof/before-effective.json" "$SANDBOX/collision-proof/after-effective.json" || fail "collision effective topology changed"
[ -f "$(config_dir)/notebooks.legacy-compat.toml" ] || fail "collision notebook behavior was not retained"
grep -q 'notebooks.personal' "$(config_dir)/notebooks.toml" || fail "collision was not rewritten to modern notebooks schema"
! grep -q 'notebooks.definitions' "$(config_dir)/notebooks.toml" || fail "legacy definitions survived canonical rewrite"
grep -q 'notebooks.definitions.personal.types.plan' "$(config_dir)/notebooks.legacy-compat.toml" || fail "notebook type was lost"
grep -q 'notebooks.rules.global' "$(config_dir)/notebooks.legacy-compat.toml" || fail "global rule was lost"
find "$(config_dir)" -maxdepth 1 -name 'notebooks.toml.*.bak' -print | grep -q . || fail "collision backup missing"
"$GROVE_CLI" migrate --yes --json >"$ARTIFACTS/canonical-collision-noop.json"
json_assert "$ARTIFACTS/canonical-collision-noop.json" '"already migrated" in d.get("reason", "")'

# Real-shape regression: canonical names managed as dotfiles symlinks. One is
# absolute; the other is a relative link into a chain. Apply and rollback must
# mutate/restore only the resolved regular targets and never sever either link.
setup_managed_collision() {
  reset_home
  MANAGED_DIR="$SANDBOX/managed-dotfiles/grove"
  MANAGED_NB="$MANAGED_DIR/notebooks-managed.conf"
  MANAGED_ROOTS="$MANAGED_DIR/roots-managed.conf"
  rm -rf "$MANAGED_DIR"
  mkdir -p "$MANAGED_DIR" "$SANDBOX/case/notes" "$SANDBOX/case/card-notes" "$SANDBOX/case/global-notes" "$SANDBOX/case/code"
  render_fixture "$FIXTURES/legacy-notebooks-collision.toml.in" "$MANAGED_NB"
  cat >"$MANAGED_ROOTS" <<EOF
[roots.code]
path = "$SANDBOX/case/code"
scan = true
notebook = "work"
EOF
  chmod 640 "$MANAGED_NB"
  chmod 604 "$MANAGED_ROOTS"
  ln -s "$MANAGED_NB" "$(config_dir)/notebooks.toml"
  ln -s "$MANAGED_ROOTS" "$(config_dir)/roots-managed-hop"
  ln -s roots-managed-hop "$(config_dir)/roots.toml"
}
setup_managed_collision
MANAGED_NB_BEFORE=$(file_digest "$MANAGED_NB")
MANAGED_ROOTS_BEFORE=$(file_digest "$MANAGED_ROOTS")
"$GROVE_CLI" migrate --yes >"$ARTIFACTS/canonical-symlink-apply.txt"
[ "$(readlink "$(config_dir)/notebooks.toml")" = "$MANAGED_NB" ] || fail "apply severed absolute canonical symlink"
[ "$(readlink "$(config_dir)/roots.toml")" = roots-managed-hop ] || fail "apply severed relative canonical symlink"
[ "$(readlink "$(config_dir)/roots-managed-hop")" = "$MANAGED_ROOTS" ] || fail "apply changed canonical symlink chain"
grep -q 'notebooks.personal' "$MANAGED_NB" || fail "apply did not modernize managed notebooks target"
grep -q 'roots.code' "$MANAGED_ROOTS" || fail "apply did not retain managed roots target"
[ -f "$(config_dir)/notebooks.toml."*.bak ] || fail "managed canonical backup missing"
"$GROVE_CLI" migrate --yes --json >"$ARTIFACTS/canonical-symlink-noop.json"
json_assert "$ARTIFACTS/canonical-symlink-noop.json" '"already migrated" in d.get("reason", "")'

setup_managed_collision
expect_fail "$ARTIFACTS/canonical-symlink-rollback.txt" env GROVE_CONFIGLAB=1 GROVE_CONFIGLAB_FAIL_AFTER=roots.toml "$GROVE_CLI" migrate --yes
[ "$(readlink "$(config_dir)/notebooks.toml")" = "$MANAGED_NB" ] || fail "rollback changed absolute canonical symlink"
[ "$(readlink "$(config_dir)/roots.toml")" = roots-managed-hop ] || fail "rollback changed relative canonical symlink"
[ "$(readlink "$(config_dir)/roots-managed-hop")" = "$MANAGED_ROOTS" ] || fail "rollback changed canonical symlink chain"
[ "$(file_digest "$MANAGED_NB")" = "$MANAGED_NB_BEFORE" ] || fail "rollback changed managed notebooks target bytes"
[ "$(file_digest "$MANAGED_ROOTS")" = "$MANAGED_ROOTS_BEFORE" ] || fail "rollback changed managed roots target bytes"
python3 - "$MANAGED_NB" "$MANAGED_ROOTS" <<'PY'
import os, stat, sys
expected = {sys.argv[1]: 0o640, sys.argv[2]: 0o604}
for path, mode in expected.items():
    actual = stat.S_IMODE(os.stat(path).st_mode)
    if actual != mode:
        raise SystemExit(f"mode mismatch {path}: {actual:o} != {mode:o}")
PY
if find "$(config_dir)" -maxdepth 1 -name '*.bak' -print | grep -q .; then fail "symlink rollback left backups"; fi
[ ! -e "$(config_dir)/notebooks.legacy-compat.toml" ] || fail "symlink rollback left compatibility fragment"

# A legacy marker does not excuse unknown fields in a canonical file.
reset_home
cat >"$(config_dir)/notebooks.toml" <<'EOF'
[_grove]
priority = 50
unknown = true
[notebooks.definitions.personal]
root_dir = "/notes"
EOF
MALFORMED_BEFORE=$(file_digest "$(config_dir)/notebooks.toml")
expect_fail "$ARTIFACTS/canonical-collision-malformed.txt" "$GROVE_CLI" migrate --dry-run
[ "$(file_digest "$(config_dir)/notebooks.toml")" = "$MALFORMED_BEFORE" ] || fail "malformed collision was mutated"
grep -q 'unknown field' "$ARTIFACTS/canonical-collision-malformed.txt.stderr" || fail "malformed collision did not fail loudly"

# V2 rollback: inject after roots.toml through the binary-only config-lab seam.
seed_legacy
BEFORE_GROVE=$(file_digest "$(config_dir)/grove.toml")
BEFORE_MACHINE=$(file_digest "$(config_dir)/machine.toml")
BEFORE_CARD=$(file_digest "$SANDBOX/case/ecosystem/grove.toml")
expect_fail "$ARTIFACTS/rollback-failure.txt" env GROVE_CONFIGLAB=1 GROVE_CONFIGLAB_FAIL_AFTER=roots.toml "$GROVE_CLI" migrate --yes
[ "$(file_digest "$(config_dir)/grove.toml")" = "$BEFORE_GROVE" ] || fail "rollback changed grove.toml"
[ "$(file_digest "$(config_dir)/machine.toml")" = "$BEFORE_MACHINE" ] || fail "rollback changed machine.toml"
[ "$(file_digest "$SANDBOX/case/ecosystem/grove.toml")" = "$BEFORE_CARD" ] || fail "rollback changed card"
[ ! -e "$(config_dir)/roots.toml" ] && [ ! -e "$(config_dir)/notebooks.toml" ] || fail "rollback left recorded files"
if find "$(config_dir)" "$SANDBOX/case/ecosystem" -name '*.bak' -print | grep -q .; then fail "rollback left backups"; fi
grep -q 'restored every changed file byte-for-byte' "$ARTIFACTS/rollback-failure.txt.stderr" || fail "rollback error omitted restoration evidence"

# Dependency-safe sync staging: refusal is non-mutating, opt-in parks an exact
# P2 input copy before surgically removing legacy live tables.
seed_legacy
cp "$FIXTURES/legacy-sync.toml" "$(config_dir)/sync.toml"
SYNC_BEFORE=$(file_digest "$(config_dir)/sync.toml")
expect_fail "$ARTIFACTS/sync-order-refusal.txt" "$GROVE_CLI" migrate --dry-run
[ "$(file_digest "$(config_dir)/sync.toml")" = "$SYNC_BEFORE" ] || fail "sync refusal mutated intent"
grep -q -- '--stage-sync' "$ARTIFACTS/sync-order-refusal.txt.stderr" || fail "sync refusal omitted safe staging instruction"
"$GROVE_CLI" migrate --yes --stage-sync >"$ARTIFACTS/sync-stage.txt"
cmp -s "$FIXTURES/legacy-sync.toml" "$(config_dir)/sync.toml.p2-staged" || fail "staged P2 sync input changed"
! grep -Eq 'workspaces|notebooks' "$(config_dir)/sync.toml" || fail "live sync retained staged tables"

# V3 degraded CLI surfaces: malformed fragment, duplicate notebook table, and
# a mixed legacy/recorded state all fail loudly without suppressing evidence.
reset_home
cat >"$(config_dir)/roots.toml" <<EOF
[roots.code]
path = "$SANDBOX/case/code"
scan = true
EOF
cat >"$(config_dir)/notebooks.toml" <<EOF
default = "notes"
[notebooks.notes]
root = "$SANDBOX/case/notes"
EOF
printf '[broken\nvalue = 1\n' >"$(config_dir)/50-broken.toml"
expect_fail "$ARTIFACTS/config-show-degraded.json" "$GROVE_CLI" config show --effective --json
json_assert "$ARTIFACTS/config-show-degraded.json" 'd["degraded"] is True and "50-broken.toml" in d.get("error", "")'
expect_fail "$ARTIFACTS/doctor-degraded.json" "$GROVE_CLI" doctor --check config_fragments --json
json_assert "$ARTIFACTS/doctor-degraded.json" 'd[0]["check"] == "effective_config" and d[0]["status"] == "fail"'
expect_fail "$ARTIFACTS/doctor-degraded-human.txt" "$GROVE_CLI" doctor --check config_fragments
grep -q 'CONFIG DEGRADED' "$ARTIFACTS/doctor-degraded-human.txt" || fail "doctor human banner missing"
rm "$(config_dir)/50-broken.toml"
cat >"$(config_dir)/notebooks.toml" <<EOF
default = "notes"
[notebooks.notes]
root = "$SANDBOX/case/notes"
[notebooks.notes]
root = "$SANDBOX/case/other-notes"
EOF
expect_fail "$ARTIFACTS/duplicate-notebooks.json" "$GROVE_CLI" config show --effective --json
json_assert "$ARTIFACTS/duplicate-notebooks.json" 'd["degraded"] is True and "notebooks.toml" in d.get("error", "")'
cat >"$(config_dir)/notebooks.toml" <<EOF
default = "notes"
[notebooks.notes]
root = "$SANDBOX/case/notes"
EOF
cat >"$(config_dir)/grove.toml" <<EOF
[groves.legacy]
path = "$SANDBOX/case/legacy"
notebook = "notes"
EOF
expect_fail "$ARTIFACTS/mixed-legacy-state.json" "$GROVE_CLI" config show --effective --json
json_assert "$ARTIFACTS/mixed-legacy-state.json" 'd["degraded"] is True and "legacy" in d.get("error", "").lower()'

# V3 daemon: malformed recorded topology boots only the status mux. Capture
# truthful status and prove a structured 503 for submission.
reset_home
printf '[roots.broken\npath = 42\n' >"$(config_dir)/roots.toml"
SOCKET="$DAEMON_RUNTIME/configlab.sock"
PIDFILE="$DAEMON_RUNTIME/configlab.pid"
"$GROVED_CLI" start --socket "$SOCKET" --pidfile "$PIDFILE" --collectors all >"$ARTIFACTS/groved.log" 2>&1 &
DAEMON_PID=$!
ready=0
for _ in $(seq 1 160); do
  if [ -S "$SOCKET" ] && curl -sS --unix-socket "$SOCKET" http://localhost/health >"$ARTIFACTS/daemon-degraded-status.json"; then ready=1; break; fi
  kill -0 "$DAEMON_PID" 2>/dev/null || break
  sleep 0.05
done
[ "$ready" = 1 ] || fail "degraded daemon did not bind (see groved.log)"
json_assert "$ARTIFACTS/daemon-degraded-status.json" 'd["degraded"] is True and d["config_error"]["code"] == "config_load_failed"'
curl -sS --unix-socket "$SOCKET" http://localhost/api/sync/status >"$ARTIFACTS/daemon-sync-status.json"
json_assert "$ARTIFACTS/daemon-sync-status.json" 'd["degraded"] is True and d["enabled"] is False'
HTTP_CODE=$(curl -sS -o "$ARTIFACTS/daemon-submit-503.json" -w '%{http_code}' --unix-socket "$SOCKET" -X POST -H 'Content-Type: application/json' -d '{}' http://localhost/api/jobs)
[ "$HTTP_CODE" = 503 ] || fail "daemon submit returned HTTP $HTTP_CODE"
json_assert "$ARTIFACTS/daemon-submit-503.json" 'd["error"]["code"] == "config_load_failed"'
kill -TERM "$DAEMON_PID"
wait "$DAEMON_PID"
DAEMON_PID=""
if find "$GROVE_HOME" -type f \( -name '*.db' -o -name '*job-queue*' \) -print | grep -q .; then fail "degraded daemon created pipeline persistence"; fi

cat >"$ARTIFACTS/configlab-summary.json" <<'EOF'
{"schema_version":1,"scenario":"config-cutover-v2-v3","status":"pass","operator_state_access":"none; HOME, GROVE_HOME, and every XDG directory were sandboxed; inherited config overlay and config-lab controls were cleared"}
EOF
echo "config-lab PASS: $ARTIFACTS"
