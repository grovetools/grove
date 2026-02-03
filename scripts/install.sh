#!/bin/bash
# Grove CLI installer
set -e

GROVE_REPO="grovetools/grove"
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/grove"
STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/grove"
INSTALL_DIR="$DATA_DIR/bin"
GITHUB_API="https://api.github.com"

# Colors
DIM='\033[2m'
GREEN='\033[32m'
YELLOW='\033[33m'
RED='\033[31m'
NC='\033[0m'

error() { echo -e "${RED}error:${NC} $1" >&2; exit 1; }

# Detect OS/arch
get_os_arch() {
    local os=$(uname -s | tr '[:upper:]' '[:lower:]')
    local arch=$(uname -m)
    case $arch in
        x86_64) arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        *) error "unsupported architecture: $arch" ;;
    esac
    case $os in
        darwin|linux) ;;
        *) error "unsupported OS: $os" ;;
    esac
    echo "${os}/${arch}"
}

main() {
    OS_ARCH=$(get_os_arch)

    # Determine download method
    USE_GH=false
    if command -v gh >/dev/null && gh auth status &>/dev/null; then
        USE_GH=true
    fi

    # Fetch latest version
    if [ "$USE_GH" = true ]; then
        VERSION=$(gh release view --repo "$GROVE_REPO" --json tagName -q .tagName 2>/dev/null)
    else
        VERSION=$(curl -s "${GITHUB_API}/repos/${GROVE_REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    fi
    [ -z "$VERSION" ] && error "could not fetch version (try: gh auth login)"

    echo -e "${DIM}grove${NC} ${VERSION} ${DIM}(${OS_ARCH})${NC}"
    echo ""

    # Download
    BINARY="grove-$(echo $OS_ARCH | tr '/' '-')"
    TEMP=$(mktemp)
    mkdir -p "$INSTALL_DIR" "$STATE_DIR"

    echo -ne "Downloading... "
    if [ "$USE_GH" = true ]; then
        gh release download "$VERSION" --repo "$GROVE_REPO" --pattern "$BINARY" --output "$TEMP" --clobber 2>/dev/null \
            || { rm -f "$TEMP"; error "download failed"; }
    else
        curl -sSfL "https://github.com/${GROVE_REPO}/releases/download/${VERSION}/${BINARY}" -o "$TEMP" \
            || { rm -f "$TEMP"; error "download failed (repo may be private, try: gh auth login)"; }
    fi
    echo "done"

    # Install
    mv "$TEMP" "$INSTALL_DIR/grove"
    chmod +x "$INSTALL_DIR/grove"
    # Write per-tool active version (matches Go SDK format)
    if [ -f "$STATE_DIR/active_versions.json" ]; then
        # Update existing versions file
        TMP_JSON=$(mktemp)
        if command -v python3 >/dev/null; then
            python3 -c "
import json, sys
with open('$STATE_DIR/active_versions.json') as f:
    data = json.load(f)
data.setdefault('versions', {})['grove'] = '$VERSION'
json.dump(data, sys.stdout, indent=2)
" > "$TMP_JSON" && mv "$TMP_JSON" "$STATE_DIR/active_versions.json"
        else
            echo '{"versions":{"grove":"'"$VERSION"'"}}' > "$STATE_DIR/active_versions.json"
        fi
    else
        echo '{"versions":{"grove":"'"$VERSION"'"}}' > "$STATE_DIR/active_versions.json"
    fi
    echo -e "${GREEN}Installed${NC} to $INSTALL_DIR/grove"

    # PATH instructions
    if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
        echo ""
        echo -e "${YELLOW}Add to PATH:${NC}"
        echo '  export PATH="$HOME/.local/share/grove/bin:$PATH"   # bash/zsh'
        echo '  fish_add_path ~/.local/share/grove/bin             # fish'
    fi

    # Next step
    echo ""
    if [ "$USE_GH" = true ]; then
        echo "Run: grove install all --use-gh"
    else
        echo "Run: grove install all"
    fi
}

main "$@"
