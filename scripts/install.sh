#!/bin/bash
# Smart installer for the Grove CLI
# - Uses 'gh' CLI for private repos if available and authenticated.
# - Falls back to public 'curl' for public repos.

set -e

# --- Configuration ---
GROVE_REPO="mattsolo1/grove-meta"
INSTALL_DIR="$HOME/.grove/bin"
ACTIVE_VERSION_DIR="$HOME/.grove" # For the .active_version file
GITHUB_API="https://api.github.com"

# --- Colors for output ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# --- Helper Functions ---
error() { echo -e "${RED}Error: $1${NC}" >&2; exit 1; }
info() { echo -e "${GREEN}==> $1${NC}"; }
warn() { echo -e "${YELLOW}Warning: $1${NC}"; }
step() { echo -e "${BLUE}  -> $1${NC}"; }

# --- System Detection ---
get_os_arch() {
    local os_name=$(uname -s | tr '[:upper:]' '[:lower:]')
    local arch_name=$(uname -m)
    
    case $arch_name in
        x86_64) arch_name="amd64" ;;
        aarch64|arm64) arch_name="arm64" ;;
        *) error "Unsupported architecture: $arch_name" ;;
    esac
    
    case $os_name in
        darwin|linux) ;;
        *) error "Unsupported operating system: $os_name" ;;
    esac
    
    echo "${os_name}-${arch_name}"
}

# --- Main Logic ---
main() {
    info "Starting Grove CLI Installation"

    # Detect OS and architecture
    OS_ARCH=$(get_os_arch)
    OS_NAME=$(echo $OS_ARCH | cut -d- -f1)
    ARCH_NAME=$(echo $OS_ARCH | cut -d- -f2)
    step "Detected system: ${OS_NAME}/${ARCH_NAME}"

    # Determine download method
    USE_GH=false
    if command -v gh >/dev/null && gh auth status &>/dev/null; then
        USE_GH=true
        step "Authenticated 'gh' CLI found. Will use it for private repo access."
    else
        step "Using 'curl' for public repository access."
    fi

    # Fetch latest release version
    step "Fetching latest release information for ${GROVE_REPO}..."
    if [ "$USE_GH" = true ]; then
        LATEST_VERSION=$(gh release view --repo "$GROVE_REPO" --json tagName -q .tagName)
    else
        LATEST_VERSION=$(curl --silent "${GITHUB_API}/repos/${GROVE_REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    fi

    if [ -z "$LATEST_VERSION" ]; then
        error "Could not determine the latest release version. If the repo is private, please ensure 'gh' CLI is installed and authenticated."
    fi
    info "Latest version is ${LATEST_VERSION}"

    # Prepare for download
    BINARY_NAME="grove-${OS_NAME}-${ARCH_NAME}"
    TARGET_PATH="$INSTALL_DIR/grove"
    TEMP_FILE=$(mktemp)

    step "Preparing installation directory: $INSTALL_DIR"
    mkdir -p "$INSTALL_DIR"
    mkdir -p "$ACTIVE_VERSION_DIR"

    # Download the binary
    info "Downloading Grove CLI (${BINARY_NAME})..."
    if [ "$USE_GH" = true ]; then
        # Use --clobber to overwrite if the temp file already exists
        if ! gh release download "$LATEST_VERSION" --repo "$GROVE_REPO" --pattern "$BINARY_NAME" --output "$TEMP_FILE" --clobber; then
            rm -f "$TEMP_FILE"
            error "Failed to download with 'gh'. Please check the release assets for your platform."
        fi
    else
        DOWNLOAD_URL="https://github.com/${GROVE_REPO}/releases/download/${LATEST_VERSION}/${BINARY_NAME}"
        step "URL: $DOWNLOAD_URL"
        if ! curl -sSfL "$DOWNLOAD_URL" -o "$TEMP_FILE"; then
            rm -f "$TEMP_FILE"
            error "Failed to download with 'curl'. The repository might be private or the asset is missing."
        fi
    fi

    # Install the binary
    mv "$TEMP_FILE" "$TARGET_PATH"
    chmod +x "$TARGET_PATH"
    echo "$LATEST_VERSION" > "${ACTIVE_VERSION_DIR}/.active_version"
    
    info "✅ Grove CLI installed successfully to: ${TARGET_PATH}"

    # Check PATH and provide instructions
    if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
        warn "\nYour PATH does not include the Grove bin directory."
        warn "Please add the following line to your shell profile (e.g., ~/.zshrc, ~/.bashrc):"
        echo -e "\n  export PATH=\"$INSTALL_DIR:\$PATH\"\n"
        warn "Then, restart your terminal or run 'source <your_profile_file>'."
    else
        info "Your PATH is already configured correctly."
    fi

    echo ""
    info "🎉 Installation Complete!"
    info "To install all Grove tools, run:"
    if [ "$USE_GH" = true ]; then
        echo -e "  grove install all --use-gh"
    else
        echo -e "  grove install all"
    fi
}

# --- Run Installer ---
main "$@"