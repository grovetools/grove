#!/usr/bin/env bash
#
# apidiff.sh — diff the exported Go API of the ecosystem's contract packages
# against each module's most recent tag.
#
# The contract set is defined by grove/docs/10-api-stability.md; CONTRACT below
# is the machine-readable copy of that table. Keep the two in sync.
#
# Usage:
#   scripts/apidiff.sh                 # every contract module
#   scripts/apidiff.sh core tuimux     # only the named modules
#
# Exit status: 0 when no incompatible changes were found, 1 when there were.
# Set ALLOW_INCOMPATIBLE=1 to report and still exit 0.
#
# Requires: apidiff (go install golang.org/x/exp/cmd/apidiff@latest).
#
# How it works: the module's last tag is checked out into a throwaway git
# worktree, a synthesized go.work points that checkout at the *current* sibling
# modules (the tagged module is generally not resolvable on its own — several
# grovetools repos are private and untagged), apidiff snapshots the old API,
# and the snapshot is compared against the working tree.

set -uo pipefail

# module-dir : space-separated package directories, relative to the module root
CONTRACT=(
	"core:config pkg/daemon tui/components/pager tui/hostedkeys"
	"tuimux:. embed panels bindings"
	"treemux:pkg/keymap pkg/keyspec pkg/panelproto"
)

die() {
	echo "apidiff.sh: $*" >&2
	exit 2
}

command -v apidiff >/dev/null 2>&1 ||
	die "apidiff not found. Install it with: go install golang.org/x/exp/cmd/apidiff@latest"

# The workspace root is the directory holding go.work, walking up from this script.
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
while [ "$root" != "/" ] && [ ! -f "$root/go.work" ]; do
	root=$(dirname "$root")
done
[ -f "$root/go.work" ] || die "no go.work found above $(dirname "${BASH_SOURCE[0]}")"

# Every module the workspace uses, so the synthesized go.work can reproduce it.
workspace_modules=$(sed -n 's|^[[:space:]]*\./||p' "$root/go.work" | tr -d '\r')

want=("$@")
selected() {
	[ ${#want[@]} -eq 0 ] && return 0
	local m
	for m in "${want[@]}"; do [ "$m" = "$1" ] && return 0; done
	return 1
}

tmp=$(mktemp -d) || die "mktemp failed"
incompatible=0
cleanup() {
	local w
	for w in "$tmp"/worktrees/*; do
		[ -d "$w" ] || continue
		git -C "$root/$(basename "$w")" worktree remove --force "$w" 2>/dev/null
	done
	rm -rf "$tmp"
}
trap cleanup EXIT

for entry in "${CONTRACT[@]}"; do
	mod=${entry%%:*}
	pkgs=${entry#*:}
	selected "$mod" || continue

	if [ ! -d "$root/$mod" ]; then
		echo "== $mod: SKIP (not checked out in this workspace)"
		continue
	fi

	tag=$(git -C "$root/$mod" describe --tags --abbrev=0 2>/dev/null)
	if [ -z "$tag" ]; then
		echo "== $mod: SKIP (no tags — nothing to diff against; see grove/docs/11-release-runbook-modules.md)"
		continue
	fi

	echo "== $mod: comparing working tree against $tag"
	old="$tmp/worktrees/$mod"
	mkdir -p "$tmp/worktrees"
	if ! git -C "$root/$mod" worktree add --detach "$old" "$tag" >/dev/null 2>&1; then
		echo "   ERROR: could not check out $tag"
		incompatible=1
		continue
	fi

	# A go.work that resolves the tagged checkout against the current siblings.
	{
		echo "go $(sed -n 's/^go //p' "$root/go.work" | head -1)"
		echo
		echo "use ("
		echo "	$old"
		for m in $workspace_modules; do
			[ "$m" = "$mod" ] && continue
			echo "	$root/$m"
		done
		echo ")"
	} >"$old/go.work"

	# apidiff loads packages in export mode, which needs the dependencies already
	# compiled; without this warm-up the first package in each checkout fails with
	# "could not import ... (invalid package name)".
	(cd "$old" && go build ./... >/dev/null 2>&1)

	for pkg in $pkgs; do
		label="$mod/${pkg#./}"
		[ "$pkg" = "." ] && label="$mod"
		snap="$tmp/$(echo "$label" | tr / _).api"

		if ! (cd "$old" && apidiff -w "$snap" "./$pkg") >"$tmp/err" 2>&1; then
			# A package that did not exist at the tag is new API, not a break.
			if grep -q "directory not found\|no such file or directory\|matched no packages\|cannot find package" "$tmp/err"; then
				echo "   $label: new since $tag"
			else
				echo "   $label: ERROR snapshotting $tag"
				sed 's/^/     /' "$tmp/err"
				incompatible=1
			fi
			continue
		fi

		if ! out=$(cd "$root/$mod" && apidiff "$snap" "./$pkg" 2>&1); then
			echo "   $label: ERROR diffing"
			echo "$out" | sed 's/^/     /'
			incompatible=1
			continue
		fi

		if [ -z "$out" ]; then
			echo "   $label: no changes"
		elif echo "$out" | grep -q '^Incompatible changes:'; then
			echo "   $label: INCOMPATIBLE"
			echo "$out" | sed 's/^/     /'
			incompatible=1
		else
			echo "   $label: compatible changes only"
			echo "$out" | sed 's/^/     /'
		fi
	done
done

if [ "$incompatible" -ne 0 ]; then
	echo
	echo "Incompatible changes to contract packages. Either revert them, or record"
	echo "the break in the module's CHANGELOG.md as grove/docs/10-api-stability.md requires."
	[ "${ALLOW_INCOMPATIBLE:-0}" = "1" ] && exit 0
	exit 1
fi
exit 0
