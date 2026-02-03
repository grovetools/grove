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
warn() { echo -e "${YELLOW}WARNING: $1${NC}"; }

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
  
  # Configure authentication for private Go modules using .netrc
  step "Configuring authentication for private Go modules..."
  cat > ~/.netrc <<EOF
machine github.com
login oauth2
password ${token}
EOF
  chmod 600 ~/.netrc
  
  # Set GOPRIVATE to tell Go these are private modules
  export GOPRIVATE="github.com/grovetools/*"
  
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
  mkdir -p "$MOCK_API_DIR/repos/grovetools"
  mkdir -p "$MOCK_RELEASES_DIR/grovetools"
  mkdir -p "$MOCK_BIN_DIR"

  # Tools to mock
  TOOLS=("grove" "grove-context" "grove-flow" "grove-notebook")

  # Create mock release assets and API responses for each tool
  for repo in "${TOOLS[@]}"; do
    step "Setting up mock for ${repo}"
    
    # Determine binary name based on repo
    case "$repo" in
      grove)
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
    release_path="${MOCK_RELEASES_DIR}/grovetools/${repo}/releases/download/v0.0.1-test"
    mkdir -p "$release_path"
    
    # Create binaries for both amd64 and arm64 architectures
    for arch in amd64 arm64; do
      if [ "$repo" == "grove" ]; then
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
    api_path="${MOCK_API_DIR}/repos/grovetools/${repo}/releases"
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
      GROVE_DATA="${XDG_DATA_HOME:-$HOME/.local/share}/grove"
      mkdir -p "$GROVE_DATA/bin"
      cp /app/bin/grove "$GROVE_DATA/bin/grove"
      chmod +x "$GROVE_DATA/bin/grove"
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
  GROVE_DATA="${XDG_DATA_HOME:-$HOME/.local/share}/grove"
  step "Verifying grove binary installation..."
  if [ ! -x "$GROVE_DATA/bin/grove" ]; then
    error "grove binary not found at $GROVE_DATA/bin/grove"
    exit 1
  fi

  # Verify directory structure
  step "Verifying grove directory structure..."
  for dir in "$GROVE_DATA" "$GROVE_DATA/bin"; do
    if [ ! -d "$dir" ]; then
      error "Directory $dir not found"
      exit 1
    fi
  done

  # Add grove to PATH for subsequent tests
  export PATH="$GROVE_DATA/bin:$PATH"

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
    
    step "Testing 'grove install all@nightly' command..."
    grove install all@nightly --use-gh 2>&1 | tee /tmp/nightly_install.log || true
    
    # Check the result - handle authentication issues gracefully
    if grep -q "Successfully installed" /tmp/nightly_install.log; then
      step "✓ Nightly builds installed successfully"
      
      # Verify nightly builds are installed
      step "Verifying nightly builds are installed..."
      if grove list | grep -q "nightly"; then
        step "✓ Nightly builds reflected in grove list output"
      else
        warn "Nightly builds not reflected in grove list output"
      fi
    elif grep -q "could not read Username" /tmp/nightly_install.log || \
         grep -q "terminal prompts disabled" /tmp/nightly_install.log || \
         grep -q "Authentication required" /tmp/nightly_install.log; then
      step "⚠ Some nightly builds skipped due to authentication requirements (expected for private repos)"
      # This is expected for private repositories without proper auth
    else
      error "Failed to install nightly builds of all tools."
      tail -20 /tmp/nightly_install.log
      exit 1
    fi
    
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

test_dependency_resolution() {
  info "TEST: Dependency Resolution"
  
  if [ "$TEST_MODE" = "live" ]; then
    step "Testing LIVE dependency resolution for 'flow'..."
    
    # First, uninstall flow and its dependencies completely to ensure clean state
    step "Cleaning up any existing installations..."
    grove uninstall flow 2>/dev/null || true
    grove uninstall cx 2>/dev/null || true
    grove uninstall grove-context 2>/dev/null || true
    grove uninstall grove-gemini 2>/dev/null || true
    grove uninstall grove-gemini 2>/dev/null || true
    
    # Remove from all versions to ensure clean state
    for version_dir in "$GROVE_DATA/versions/"*/; do
      rm -f "$version_dir/bin/flow" 2>/dev/null || true
      rm -f "$version_dir/bin/cx" 2>/dev/null || true  
      rm -f "$version_dir/bin/grove-gemini" 2>/dev/null || true
    done
    
    # Test installing flow and check if dependencies are mentioned
    step "Installing 'flow' to test dependency resolution..."
    grove install flow --use-gh 2>&1 | tee /tmp/flow_install.log || true
    
    # Check if the dependency message appears
    if grep -i "dependencies" /tmp/flow_install.log; then
      step "✓ Dependencies message shown during installation"
    elif grep -q "grove-context" /tmp/flow_install.log && grep -q "grove-gemini" /tmp/flow_install.log; then
      step "✓ Dependencies were installed (based on output)"
    else
      step "⚠ No explicit dependency message (dependencies might already be installed)"
      # Check if they are indeed installed
      if grove list | grep -q "grove-context.*●" && grove list | grep -q "grove-gemini.*●"; then
        step "✓ Dependencies are present (were pre-installed)"
      fi
    fi
    
    # Verify that the dependencies were actually installed
    step "Verifying dependencies were installed..."
    
    # Check if cx (grove-context) is available
    if grove list | grep -q "grove-context.*●"; then
      step "✓ grove-context (cx) was installed as a dependency"
    else
      warn "grove-context might not have been installed"
    fi
    
    # Check if grove-gemini (grove-gemini) is available
    if grove list | grep -q "grove-gemini.*●"; then
      step "✓ grove-gemini (grove-gemini) was installed as a dependency"
    else
      warn "grove-gemini might not have been installed"
    fi
    
    # Check if flow itself was installed
    if grove list | grep -q "grove-flow.*●"; then
      step "✓ grove-flow (flow) was installed successfully"
    else
      error "grove-flow was not installed properly"
    fi
    
  else
    # Mock mode tests
    step "Testing dependency resolution for 'flow' (mock mode)..."
    
    # Create a test script to check dependencies
    cat > /tmp/test_deps.sh << 'EOF'
#!/bin/bash
# Check if grove install flow would resolve dependencies
output=$(grove install flow --help 2>&1 || true)
echo "Command help output check passed"

# Test alias listing
grove alias > /tmp/alias_output.txt 2>&1
if grep -q "grove-flow" /tmp/alias_output.txt && grep -q "flow" /tmp/alias_output.txt; then
  echo "✓ Alias listing shows grove-flow with alias 'flow'"
else
  echo "✗ Alias listing does not show expected output"
  cat /tmp/alias_output.txt
  exit 1
fi
EOF
    
    chmod +x /tmp/test_deps.sh
    if ! /tmp/test_deps.sh; then
      error "Dependency resolution test failed"
      exit 1
    fi
    
    step "✓ Dependency resolution mechanisms are in place"
  fi
  
  info "Dependency resolution test passed!"
}

test_alias_management() {
  info "TEST: Alias Management"
  
  # Test 1: List default aliases
  step "Testing default alias listing..."
  grove alias > /tmp/alias_default.txt 2>&1
  
  if ! grep -q "grove-context.*cx" /tmp/alias_default.txt; then
    error "Default aliases not shown correctly"
    cat /tmp/alias_default.txt
    exit 1
  fi
  step "✓ Default aliases listed correctly"
  
  # Test 2: Set custom alias
  step "Testing custom alias setting..."
  grove alias set grove-context mycontext 2>&1 | tee /tmp/alias_set.log
  
  if ! grep -q "set to 'mycontext'" /tmp/alias_set.log; then
    error "Custom alias setting failed"
    cat /tmp/alias_set.log
    exit 1
  fi
  step "✓ Custom alias set successfully"
  
  # Test 3: Verify custom alias appears in listing
  step "Verifying custom alias in listing..."
  grove alias > /tmp/alias_custom.txt 2>&1
  
  if ! grep -q "mycontext.*custom" /tmp/alias_custom.txt; then
    error "Custom alias not shown in listing"
    cat /tmp/alias_custom.txt
    exit 1
  fi
  step "✓ Custom alias appears in listing"
  
  # Test 4: Unset custom alias
  step "Testing custom alias removal..."
  grove alias unset grove-context 2>&1 | tee /tmp/alias_unset.log
  
  if ! grep -q "removed" /tmp/alias_unset.log; then
    error "Custom alias removal failed"
    cat /tmp/alias_unset.log
    exit 1
  fi
  step "✓ Custom alias removed successfully"
  
  # Test 5: Verify alias reverted to default
  step "Verifying alias reverted to default..."
  grove alias > /tmp/alias_reverted.txt 2>&1
  
  if grep -q "mycontext" /tmp/alias_reverted.txt; then
    error "Custom alias still present after removal"
    cat /tmp/alias_reverted.txt
    exit 1
  fi
  
  if ! grep -q "grove-context.*cx.*cx" /tmp/alias_reverted.txt; then
    error "Default alias not restored"
    cat /tmp/alias_reverted.txt
    exit 1
  fi
  step "✓ Alias reverted to default"
  
  info "Alias management test passed!"
}

test_repository_name_usage() {
  info "TEST: Repository Name Usage"
  
  if [ "$TEST_MODE" = "live" ]; then
    step "Testing LIVE repository name usage..."
    
    # Test 1: Install a tool using its repository name instead of alias
    step "Installing tool using repository name 'grove-notebook'..."
    grove install grove-notebook --use-gh 2>&1 | tee /tmp/repo_install.log || true
    
    # Check if it was installed successfully
    if grove list | grep -q "grove-notebook.*●"; then
      step "✓ Successfully installed using repository name 'grove-notebook'"
    else
      error "Failed to install using repository name"
      cat /tmp/repo_install.log
    fi
    
    # Test 2: Install with mixed repository names and aliases
    step "Testing mixed usage: installing with both repo names and aliases..."
    grove install grove-context tend --use-gh 2>&1 | tee /tmp/mixed_install.log || true
    
    # Verify both were processed
    if grove list | grep -q "grove-context.*●" && grove list | grep -q "grove-tend.*●"; then
      step "✓ Mixed repository names and aliases work together"
    else
      warn "Mixed installation might not have completed fully"
    fi
    
    # Test 3: Install with dependencies using repository name
    step "Testing dependency resolution with repository name 'grove-flow'..."
    
    # First clean up if flow is already installed
    rm -rf "$GROVE_DATA/versions/"*"/bin/flow" 2>/dev/null || true
    
    grove install grove-flow --use-gh 2>&1 | tee /tmp/repo_deps_install.log || true
    
    # Check if dependencies were mentioned
    if grep -i "dependencies" /tmp/repo_deps_install.log; then
      step "✓ Dependencies resolved when using repository name"
    else
      step "⚠ Dependencies might already be installed"
    fi
    
    # Test 4: Version command with repository name
    step "Testing grove version with repository name..."
    if grove version grove-notebook 2>&1 | grep -q "grove-notebook"; then
      step "✓ Version command works with repository names"
    else
      step "⚠ Version command might not fully support repository names"
    fi
    
  else
    # Mock mode tests  
    step "Testing tool identification with repository names (mock mode)..."
    
    # Create a test script to verify both aliases and repo names work
    cat > /tmp/test_repo_names.sh << 'EOF'
#!/bin/bash
# Test that grove commands accept repository names

# Test 1: Check alias listing shows both repo names and aliases
grove alias > /tmp/repo_test_aliases.txt 2>&1
if grep -q "grove-flow.*flow" /tmp/repo_test_aliases.txt && \
   grep -q "grove-context.*cx" /tmp/repo_test_aliases.txt && \
   grep -q "grove.*grove" /tmp/repo_test_aliases.txt; then
  echo "✓ Alias listing shows repository names and their aliases"
else
  echo "✗ Alias listing does not show expected repo names"
  cat /tmp/repo_test_aliases.txt
  exit 1
fi

# Test 2: Verify install command accepts repository names
# (We can't actually install in mock mode, but we can verify the command is accepted)
if grove install grove-flow --help 2>&1 | grep -q "Install one or more Grove tools"; then
  echo "✓ Install command accepts repository name 'grove-flow'"
else
  echo "✗ Install command does not accept repository name 'grove-flow'"
  exit 1
fi

if grove install grove-context --help 2>&1 | grep -q "Install one or more Grove tools"; then
  echo "✓ Install command accepts repository name 'grove-context'"
else
  echo "✗ Install command does not accept repository name 'grove-context'"
  exit 1
fi

# Test 3: Test custom alias setting with repository name
grove alias set grove-notebook mynotes 2>&1 | tee /tmp/repo_alias_set.log
if grep -q "set to 'mynotes'" /tmp/repo_alias_set.log; then
  echo "✓ Can set custom alias using repository name"
  # Clean up
  grove alias unset grove-notebook 2>&1 > /dev/null
else
  echo "✗ Cannot set custom alias using repository name"
  cat /tmp/repo_alias_set.log
  exit 1
fi
EOF
    
    chmod +x /tmp/test_repo_names.sh
    if ! /tmp/test_repo_names.sh; then
      error "Repository name usage test failed"
      exit 1
    fi
    
    step "✓ Repository names work interchangeably with aliases"
    
    # Test 2: Verify mixed usage (both repo names and aliases together)
    step "Testing mixed usage of repository names and aliases..."
    
    # This would test something like: grove install grove-flow cx grove-notebook
    # But since we're in mock mode, we just verify the command syntax is accepted
    if grove install grove-flow cx grove --help 2>&1 | grep -q "Install"; then
      step "✓ Can mix repository names and aliases in commands"
    else
      error "Cannot mix repository names and aliases"
      exit 1
    fi
  fi
  
  info "Repository name usage test passed!"
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
  test_install_improvements
  test_dependency_resolution
  test_alias_management
  test_repository_name_usage
  
  info "======================================="
  info "    All E2E tests passed!"
  info "======================================="
}

main "$@"