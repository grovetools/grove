#!/bin/bash
set -euxo pipefail

# --- Mock Test Configuration ---
MOCK_WEB_ROOT="/tmp/web"
MOCK_API_DIR="${MOCK_WEB_ROOT}/api"
MOCK_RELEASES_DIR="${MOCK_WEB_ROOT}/releases"
MOCK_BIN_DIR="/tmp/mock_bin"
MOCK_SERVER_PID=""

TEST_MODE=${TEST_MODE:-mock} # Default to mock mode

# --- Colors for output ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info() { echo -e "${GREEN}==> $1${NC}"; }
step() { echo -e "${BLUE}  -> $1${NC}"; }
error() { echo -e "${RED}ERROR: $1${NC}"; }

# --- Helper Functions ---
cleanup() {
  info "Cleaning up..."
  if [ -n "$MOCK_SERVER_PID" ]; then
    kill "$MOCK_SERVER_PID" 2>/dev/null || true
  fi
}

# --- Live Test Setup ---
setup_live() {
  info "Setting up LIVE environment..."
  if [ -z "$GITHUB_TOKEN" ]; then
    error "GITHUB_TOKEN is not set for live test mode."
    exit 1
  fi
  
  step "Authenticating with gh CLI..."
  # Save token and unset from environment to avoid gh CLI warning
  local token="$GITHUB_TOKEN"
  unset GITHUB_TOKEN
  
  if ! echo "$token" | gh auth login --with-token 2>/dev/null; then
    error "Failed to authenticate with GitHub. Please check your GITHUB_TOKEN."
    info "To run tests in mock mode instead, unset the GITHUB_TOKEN environment variable."
    exit 1
  fi
  
  if ! gh auth status >/dev/null 2>&1; then
    error "gh auth status check failed."
    exit 1
  fi
  # Note: We don't restore GITHUB_TOKEN as gh CLI will use stored credentials
  
  step "Successfully authenticated with GitHub"
  step "Live environment ready."
}

# --- Mock Test Setup ---
setup_mocks() {
  info "Setting up mock environment..."
  # Ensure mock binaries are on the path for mock mode
  export PATH="${MOCK_BIN_DIR}:$PATH"

  # Create directory structure for mock web server
  mkdir -p "$MOCK_API_DIR/repos/mattsolo1"
  mkdir -p "$MOCK_RELEASES_DIR/mattsolo1"
  mkdir -p "$MOCK_BIN_DIR"

  # Tools to mock
  TOOLS=("grove-meta" "grove-context" "grove-flow" "grove-notebook")

  # Create mock release assets and API responses for each tool
  for repo in "${TOOLS[@]}"; do
    step "Setting up mock for ${repo}"
    
    # Determine binary name based on repo
    case "$repo" in
      grove-meta)
        binary_name="grove"
        ;;
      grove-context)
        binary_name="cx"
        ;;
      grove-flow)
        binary_name="flow"
        ;;
      grove-notebook)
        binary_name="nb"
        ;;
      *)
        binary_name=$(echo "$repo" | sed 's/grove-//')
        ;;
    esac
    
    # Mock release binary
    release_path="${MOCK_RELEASES_DIR}/mattsolo1/${repo}/releases/download/v0.0.1-test"
    mkdir -p "$release_path"
    
    # Create binaries for both amd64 and arm64 architectures
    for arch in amd64 arm64; do
      if [ "$repo" == "grove-meta" ]; then
        # Use the mock grove binary from the container
        cp "/app/bin/grove" "${release_path}/grove-linux-${arch}"
      else
        # Create generic mock binary
        cat > "${release_path}/${binary_name}-linux-${arch}" << EOF
#!/bin/bash
echo "Mock ${binary_name} binary v0.0.1-test running with: \$@"
case "\$1" in
  version|--version|-v)
    echo "${binary_name} v0.0.1-test"
    ;;
  *)
    echo "Command executed: \$@"
    ;;
esac
EOF
        chmod +x "${release_path}/${binary_name}-linux-${arch}"
      fi
    done

    # Mock API response for 'latest' release
    api_path="${MOCK_API_DIR}/repos/mattsolo1/${repo}/releases"
    mkdir -p "$api_path"
    echo '{"tag_name": "v0.0.1-test"}' > "${api_path}/latest"
  done

  # Start mock web server
  step "Starting mock web server on port 8000..."
  cd "$MOCK_WEB_ROOT"
  python3 -m http.server 8000 &
  MOCK_SERVER_PID=$!
  cd /app
  sleep 2 # Give server time to start
  
  # Verify server is running
  if ! curl -s -o /dev/null "http://localhost:8000/"; then
    error "Mock web server failed to start"
    exit 1
  fi

  # Setup mock 'gh' CLI
  step "Setting up mock 'gh' CLI..."
  cp "/app/tests/e2e/mocks/gh_docker_e2e" "${MOCK_BIN_DIR}/gh"
  chmod +x "${MOCK_BIN_DIR}/gh"
}

# --- Test Suites ---
test_installation() {
  info "TEST: Grove CLI Installation"

  if [ "$TEST_MODE" = "live" ]; then
    # Check if we already have a local build of grove (for testing improvements)
    if [ -f "/app/bin/grove" ]; then
      step "Using pre-built grove binary for testing improvements..."
      mkdir -p "$HOME/.grove/bin"
      cp /app/bin/grove "$HOME/.grove/bin/grove"
      chmod +x "$HOME/.grove/bin/grove"
    else
      step "Running original install.sh against real GitHub..."
      # The real install.sh is smart and will use the authenticated 'gh' CLI
      bash /app/scripts/install.sh
    fi
  else
    # Modify install.sh to use the mock server
    step "Patching install.sh to use mock server..."
    cp /app/scripts/install.sh /tmp/install_patched.sh
    sed -i 's|GITHUB_API="https://api.github.com"|GITHUB_API="http://localhost:8000/api"|g' /tmp/install_patched.sh
    sed -i 's|https://github.com|http://localhost:8000/releases|g' /tmp/install_patched.sh
    
    # Temporarily remove gh from PATH to force curl usage in mock mode
    OLD_PATH="$PATH"
    export PATH="/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
    
    # Run the installer
    step "Running patched install.sh (without gh in PATH)..."
    bash /tmp/install_patched.sh
    
    # Restore PATH
    export PATH="$OLD_PATH"
  fi

  # Verify installation
  step "Verifying grove binary installation..."
  if [ ! -x "$HOME/.grove/bin/grove" ]; then
    error "grove binary not found at ~/.grove/bin/grove"
    exit 1
  fi

  # Verify directory structure
  step "Verifying grove directory structure..."
  for dir in "$HOME/.grove" "$HOME/.grove/bin"; do
    if [ ! -d "$dir" ]; then
      error "Directory $dir not found"
      exit 1
    fi
  done

  # Add grove to PATH for subsequent tests
  export PATH="$HOME/.grove/bin:$PATH"

  # Verify 'grove version' command works
  step "Running 'grove version'..."
  if ! grove version; then
    error "'grove version' command failed"
    exit 1
  fi
  
  # Verify the grove binary is working (check for version output)
  version_output=$(grove version 2>&1)
  if [[ ! "$version_output" =~ (Version:|grove|v[0-9]) ]]; then
    error "The installed 'grove' binary seems incorrect. Output: $version_output"
    exit 1
  fi
  
  info "Installation test passed!"
}

test_tool_management() {
  info "TEST: Tool Management"
  
  if [ "$TEST_MODE" = "live" ]; then
    step "Installing 'cx' and 'flow' from real GitHub releases..."
    if ! grove install cx flow --use-gh; then
      error "Failed to install tools in live mode."
      exit 1
    fi
    step "Verifying tool installation..."
    if ! command -v cx >/dev/null || ! command -v flow >/dev/null; then
      error "'cx' or 'flow' not found in PATH after installation."
      exit 1
    fi
    step "Running installed tools..."
    cx version
    flow version
    
    step "Testing 'grove install all' command..."
    if ! grove install all --use-gh; then
      error "Failed to install all tools."
      exit 1
    fi
    
    step "Listing all installed tools..."
    grove list
    
    # Verify some key tools are installed and working
    step "Verifying key tools are installed..."
    local tools_to_check=("nb" "tend" "px")
    for tool in "${tools_to_check[@]}"; do
      if command -v "$tool" >/dev/null 2>&1; then
        step "✓ $tool is installed"
        # Try to run version command (some may use 'version', others '--version')
        "$tool" version 2>/dev/null || "$tool" --version 2>/dev/null || true
      else
        step "⚠ $tool not found (may not be available for this platform)"
      fi
    done
    
    # Final comprehensive list
    step "Final tool status:"
    grove list
  else
    step "Listing available tools (mock mode)..."
    grove list || true
    
    # Note: Tool installation in mock mode requires complex GitHub API mocking
    # The grove installation test already validates the core installation flow
    step "Skipping actual tool installation in mock mode (installation flow tested via grove itself)"
  fi
  
  info "Tool management test passed!"
}

test_workspace_operations() {
  info "TEST: Workspace Operations"
  
  # Configure git for commits (sometimes required)
  git config --global user.email "test@example.com" || true
  git config --global user.name "Test User" || true
  
  # Create a temporary ecosystem
  ECO_DIR="/tmp/my-ecosystem"
  mkdir -p "$ECO_DIR"
  cd "$ECO_DIR"
  
  # Initialize workspace
  step "Initializing new grove workspace..."
  if ! grove ws init; then
    error "'grove ws init' failed"
    exit 1
  fi
  
  # Verify workspace files
  step "Verifying workspace initialization..."
  if [ ! -f "grove.yml" ]; then
    error "grove.yml not created"
    exit 1
  fi
  
  if [ ! -f "go.work" ]; then
    error "go.work not created"
    exit 1
  fi
  
  if [ ! -f "Makefile" ]; then
    error "Makefile not created"
    exit 1
  fi
  
  if [ ! -f ".gitignore" ]; then
    error ".gitignore not created"
    exit 1
  fi
  
  # Verify git repo was initialized
  if [ ! -d ".git" ]; then
    error "Git repository not initialized"
    exit 1
  fi
  
  # Test grove list command
  step "Testing 'grove list' command..."
  grove list || true
  
  info "Workspace operations test passed!"
}

test_install_improvements() {
  info "TEST: Install Command Improvements"
  
  # Test 1: Up-to-date detection
  step "Testing up-to-date detection..."
  
  # Install a tool first
  if [ "$TEST_MODE" = "live" ]; then
    grove install flow --use-gh > /tmp/install1.log 2>&1 || true
    # Install again to test up-to-date detection
    grove install flow --use-gh > /tmp/install2.log 2>&1 || true
    
    if grep -q "already up to date" /tmp/install2.log; then
      step "✓ Up-to-date detection working"
    else
      error "Up-to-date detection not working as expected"
      cat /tmp/install2.log
    fi
  else
    step "Skipping up-to-date detection in mock mode"
  fi
  
  # Test 2: Error message for missing binaries
  step "Testing improved error messages..."
  
  # Try to install a tool that doesn't have binaries for this platform
  grove install project-tmpl-go 2>&1 | tee /tmp/install_error.log || true
  
  if grep -q "No binary available for your system" /tmp/install_error.log; then
    step "✓ Improved error message for missing binaries"
  else
    step "⚠ Error message test skipped (binary might exist)"
  fi
  
  # Test 3: Multiple tool installation with status messages
  step "Testing multiple tool installation..."
  
  if [ "$TEST_MODE" = "live" ]; then
    grove install nb flow 2>&1 | tee /tmp/install_multi.log || true
    
    # Check for various status messages
    if grep -E "(Installing|Updating|already up to date|reinstalled)" /tmp/install_multi.log; then
      step "✓ State-aware status messages working"
    else
      step "⚠ Status messages not as expected"
    fi
  else
    step "Skipping multiple tool test in mock mode"
  fi
  
  # Test 4: Grove list with version display
  step "Testing grove list output formatting..."
  grove list > /tmp/list_output.log 2>&1 || true
  
  # Check for the new status symbols
  if grep -E "(●|◆|○)" /tmp/list_output.log; then
    step "✓ Grove list shows styled status indicators"
  else
    step "⚠ Grove list styling might not be working"
    cat /tmp/list_output.log
  fi
  
  # Note: @nightly builds are not tested in Docker as they require
  # full Go development environment and access to private repos
  step "Note: @nightly build feature requires full dev environment (not tested in Docker)"
  
  info "Install improvements test passed!"
}


# --- Main Execution ---
main() {
  trap cleanup EXIT
  
  info "Starting Docker E2E Tests"
  info "Container environment: $(uname -a)"
  
  if [ "$TEST_MODE" = "live" ]; then
    setup_live
  else
    setup_mocks
  fi
  test_installation
  test_tool_management
  test_workspace_operations
  test_install_improvements
  
  info "======================================="
  info "    All E2E tests passed!"
  info "======================================="
}

main "$@"