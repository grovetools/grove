#!/bin/sh
# Grove CLI installer
#
# Installs the grove binary into grove's own toolchain dir and puts exactly ONE
# name in the user's global namespace: ~/.local/bin/grove. Every other tool is
# reached as `grove <tool>` (or opted back into the global namespace, one at a
# time, with `grove expose <tool>`), so this script never asks the user to put
# grove's private bin dir on PATH.
set -e

GROVE_REPO="grovetools/grove"
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/grove"
STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/grove"
INSTALL_DIR="$DATA_DIR/bin"
# The global namespace is the USER's directory, so it deliberately does not
# follow XDG_DATA_HOME/GROVE_HOME — it is ~/.local/bin literally, the same
# directory `grove expose` links into.
LINK_DIR="$HOME/.local/bin"
GITHUB_API="https://api.github.com"

# Colors
DIM='\033[2m'
GREEN='\033[32m'
YELLOW='\033[33m'
RED='\033[31m'
NC='\033[0m'

error() { printf "${RED}error:${NC} %s\n" "$1" >&2; exit 1; }
warn() { printf "${YELLOW}warning:${NC} %s\n" "$1" >&2; }

# Detect OS/arch
get_os_arch() {
    _os=$(uname -s | tr '[:upper:]' '[:lower:]')
    _arch=$(uname -m)
    case $_arch in
        x86_64) _arch="amd64" ;;
        aarch64|arm64) _arch="arm64" ;;
        *) error "unsupported architecture: $_arch" ;;
    esac
    case $_os in
        darwin|linux) ;;
        *) error "unsupported OS: $_os" ;;
    esac
    echo "${_os}/${_arch}"
}

# sha256_file prints the hex sha256 of $1, or nothing at all when the machine
# has no hashing tool. Callers treat "nothing" as "cannot verify", never as
# "does not match": a missing coreutils must not block a bootstrap.
sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        echo ""
    fi
}

# expected_sha prints the checksum recorded for asset $2 in checksums file $1.
# The release workflow writes `sha256sum *` output (two-space separator, bare
# asset names); the `*name` form is what sha256sum -b emits.
expected_sha() {
    awk -v name="$2" '$2 == name || $2 == "*" name { print $1; exit }' "$1"
}

# verify_checksum checks downloaded file $1 against checksums file $2 for asset
# $3. A real MISMATCH is fatal — that is a corrupt or tampered download. Every
# other outcome (no checksums.txt in an older release, asset not listed, no
# hashing tool installed) warns and continues.
verify_checksum() {
    _file="$1"
    _sums="$2"
    _name="$3"

    if [ ! -s "$_sums" ]; then
        warn "release has no checksums.txt - skipping verification"
        return 0
    fi
    _want=$(expected_sha "$_sums" "$_name")
    if [ -z "$_want" ]; then
        warn "$_name is not listed in checksums.txt - skipping verification"
        return 0
    fi
    _got=$(sha256_file "$_file")
    if [ -z "$_got" ]; then
        warn "no sha256sum or shasum on this machine - skipping verification"
        return 0
    fi
    if [ "$_want" != "$_got" ]; then
        error "checksum mismatch for $_name (expected $_want, got $_got)"
    fi
    return 0
}

# path_has reports whether directory $1 is a component of PATH.
path_has() {
    case ":${PATH}:" in
        *":$1:"*) return 0 ;;
        *) return 1 ;;
    esac
}

# link_global points $LINK_DIR/grove at the installed binary.
#
# It replaces a SYMLINK (that is either ours or an equivalent one) and never
# touches a regular file: ~/.local/bin belongs to the user, and a real `grove`
# sitting there is something they put there deliberately. Failing to link is a
# warning, not an install failure — the binary is already in place.
link_global() {
    _target="$1"
    _link="$LINK_DIR/grove"

    if [ "$_target" = "$_link" ]; then
        return 0
    fi
    if ! mkdir -p "$LINK_DIR" 2>/dev/null; then
        warn "could not create $LINK_DIR - grove is installed at $_target"
        return 0
    fi
    if [ -e "$_link" ] && [ ! -L "$_link" ]; then
        warn "$_link exists and is not a symlink - leaving it alone (grove is installed at $_target)"
        return 0
    fi
    rm -f "$_link"
    if ! ln -s "$_target" "$_link" 2>/dev/null; then
        warn "could not link $_link - grove is installed at $_target"
        return 0
    fi
    printf "${GREEN}Linked${NC}    %s ${DIM}->${NC} %s\n" "$_link" "$_target"
    return 0
}

# seed_active_version records grove's own version the way the Go SDK reads it.
seed_active_version() {
    _version="$1"
    if [ -f "$STATE_DIR/active_versions.json" ] && command -v python3 >/dev/null 2>&1; then
        _tmp_json=$(mktemp)
        if python3 -c "
import json, sys
with open('$STATE_DIR/active_versions.json') as f:
    data = json.load(f)
data.setdefault('versions', {})['grove'] = '$_version'
json.dump(data, sys.stdout, indent=2)
" > "$_tmp_json" 2>/dev/null; then
            mv "$_tmp_json" "$STATE_DIR/active_versions.json"
            return 0
        fi
        rm -f "$_tmp_json"
    fi
    echo '{"versions":{"grove":"'"$_version"'"}}' > "$STATE_DIR/active_versions.json"
}

main() {
    OS_ARCH=$(get_os_arch)

    # Fetch latest version
    VERSION=$(curl -s "${GITHUB_API}/repos/${GROVE_REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    [ -z "$VERSION" ] && error "could not fetch version"

    printf "${DIM}grove${NC} %s ${DIM}(%s)${NC}\n" "$VERSION" "$OS_ARCH"
    echo ""

    # Download
    BINARY="grove-$(echo $OS_ARCH | tr '/' '-')"
    TEMP=$(mktemp)
    SUMS=$(mktemp)
    trap 'rm -f "$TEMP" "$SUMS"' EXIT INT TERM
    mkdir -p "$INSTALL_DIR" "$STATE_DIR"

    printf "Downloading... "
    curl -sSfL "https://github.com/${GROVE_REPO}/releases/download/${VERSION}/${BINARY}" -o "$TEMP" \
        || error "download failed"
    echo "done"

    # Checksums are best-effort to FETCH (old releases predate them) and
    # strict to COMPARE (see verify_checksum).
    curl -sfL "https://github.com/${GROVE_REPO}/releases/download/${VERSION}/checksums.txt" -o "$SUMS" 2>/dev/null \
        || : > "$SUMS"
    printf "Verifying... "
    verify_checksum "$TEMP" "$SUMS" "$BINARY"
    echo "done"

    # Install
    mv "$TEMP" "$INSTALL_DIR/grove"
    chmod +x "$INSTALL_DIR/grove"
    seed_active_version "$VERSION"
    printf "${GREEN}Installed${NC} to %s\n" "$INSTALL_DIR/grove"

    # One name in the user's namespace: grove. Everything else is 'grove <tool>'.
    link_global "$INSTALL_DIR/grove"

    if ! path_has "$LINK_DIR"; then
        echo ""
        printf "${YELLOW}%s is not on your PATH.${NC} Add it:\n" "$LINK_DIR"
        echo '  export PATH="$HOME/.local/bin:$PATH"   # bash/zsh'
        echo '  fish_add_path ~/.local/bin             # fish'
    fi

    # Run onboarding wizard — only where it can actually run. The wizard
    # needs /dev/tty (curl|bash leaves a pipe on stdin, but the terminal is
    # still there; CI/automation has neither). Gating on the tty rather than
    # stdin keeps the piped interactive install working, and a headless
    # install exits 0 with the next step named instead of dying on a TTY
    # error after everything already succeeded.
    trap - EXIT INT TERM
    rm -f "$SUMS"
    if ( : < /dev/tty ) 2>/dev/null; then
        echo ""
        echo "Starting Grove onboarding..."
        exec "$INSTALL_DIR/grove" onboard
    fi
    echo ""
    echo "No terminal available - skipping the onboarding wizard."
    echo "Run 'grove onboard' to finish setup."
}

main "$@"
