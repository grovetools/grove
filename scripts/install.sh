#!/bin/sh
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

error() { printf "${RED}error:${NC} %s\n" "$1" >&2; exit 1; }

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
    mkdir -p "$INSTALL_DIR" "$STATE_DIR"

    printf "Downloading... "
    curl -sSfL "https://github.com/${GROVE_REPO}/releases/download/${VERSION}/${BINARY}" -o "$TEMP" \
        || { rm -f "$TEMP"; error "download failed"; }
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
    printf "${GREEN}Installed${NC} to %s\n" "$INSTALL_DIR/grove"

    # PATH instructions
    case ":$PATH:" in
        *":$INSTALL_DIR:"*) ;;
        *)
            echo ""
            printf "${YELLOW}Add to PATH:${NC}\n"
            echo '  export PATH="$HOME/.local/share/grove/bin:$PATH"   # bash/zsh'
            echo '  fish_add_path ~/.local/share/grove/bin             # fish'
            ;;
    esac

    # Run onboarding wizard
    echo ""
    echo "Starting Grove onboarding..."
    "$INSTALL_DIR/grove" onboard
}

main "$@"
