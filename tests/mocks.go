package tests

// Mock scripts embedded as constants to ensure they're available in all environments

const ghMockScript = `#!/bin/bash
# Mock gh CLI for testing grove meta add-repo

# Log all calls for verification
echo "gh $@" >> "${GH_MOCK_LOG:-/tmp/gh-mock.log}"

# Parse command
case "$1" in
    "auth")
        if [[ "$2" == "status" ]]; then
            echo "Logged in as test-user"
            exit 0
        fi
        ;;
    
    "repo")
        case "$2" in
            "create")
                # Simulate successful repo creation
                echo "✓ Created repository $3"
                exit 0
                ;;
            "delete")
                # Simulate successful repo deletion
                echo "✓ Deleted repository $3"
                exit 0
                ;;
            "view")
                # Check if repo exists
                if [[ "$3" == *"grove-existing"* ]]; then
                    echo "Repository exists"
                    exit 0
                else
                    echo "Repository not found"
                    exit 1
                fi
                ;;
        esac
        ;;
    
    "secret")
        if [[ "$2" == "set" && "$3" == "GROVE_PAT" ]]; then
            # Read from stdin but don't actually do anything
            cat > /dev/null
            echo "✓ Set secret GROVE_PAT"
            exit 0
        fi
        ;;
    
    "run")
        case "$2" in
            "list")
                # Return a mock run ID
                echo '{"databaseId": 12345}'
                exit 0
                ;;
            "watch")
                # Simulate successful CI run
                echo "✓ Workflow run completed successfully"
                exit 0
                ;;
        esac
        ;;
    
    "api")
        # Mock API calls for getting latest releases
        if [[ "$2" == *"releases/latest"* ]]; then
            echo '{"tag_name": "v0.2.10"}'
            exit 0
        fi
        ;;
esac

echo "Mock gh: Unhandled command: gh $@" >&2
exit 1
`

const makeMockScript = `#!/bin/bash
# Mock make command for testing

# Handle specific targets
case "$1" in
    "check")
        if [[ "${MAKE_FAIL_CHECK}" == "true" ]]; then
            echo "make check: FAILED (mock)"
            exit 1
        else
            echo "make check: SUCCESS (mock)"
            exit 0
        fi
        ;;
    *)
        # Default success for other targets
        echo "make $@: SUCCESS (mock)"
        exit 0
        ;;
esac
`

const gitMockScript = `#!/bin/bash
# Create .git directory structure for grove git-hooks
if [[ ! -d ".git" ]]; then
  mkdir -p .git/hooks
fi

case "$1" in
  "init"|"add"|"commit"|"tag"|"push"|"remote"|"submodule"|"config"|"rev-parse")
    echo "git $@: SUCCESS"
    exit 0
    ;;
  *)
    echo "git $@"
    exit 0
    ;;
esac
`

const goMockScript = `#!/bin/bash
case "$1" in
  "mod")
    echo "go mod $2: SUCCESS"
    exit 0
    ;;
  *)
    echo "go $@"
    exit 0
    ;;
esac
`

const gofmtMockScript = `#!/bin/bash
echo "gofmt: SUCCESS"
exit 0
`

const gemapiMockScript = `#!/bin/bash
# Mock gemapi for E2E tests - simulates LLM changelog generation

# Default model for testing
MODEL="gemini-1.5-flash-latest"

# Parse command line arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    request)
      REQUEST_MODE=true
      shift
      ;;
    --model)
      MODEL="$2"
      shift 2
      ;;
    --file)
      PROMPT_FILE="$2"
      shift 2
      ;;
    --yes)
      # Auto-confirm
      shift
      ;;
    *)
      shift
      ;;
  esac
done

# If this is a request command
if [ "$REQUEST_MODE" = "true" ]; then
  # Read the prompt file to determine what kind of response is needed
  if [ -n "$PROMPT_FILE" ] && [ -f "$PROMPT_FILE" ]; then
    PROMPT_CONTENT=$(cat "$PROMPT_FILE")
    
    # Check if this is a changelog generation request
    if echo "$PROMPT_CONTENT" | grep -q "changelog" && echo "$PROMPT_CONTENT" | grep -q "JSON"; then
      # Generate a mock changelog response
      cat << 'EOF'
{
  "suggestion": "minor",
  "justification": "New features were added without breaking changes.",
  "changelog": "## v0.1.1 (2024-09-24)\\n\\nThis release introduces several new features and improvements.\\n\\n### Features\\n- Add new feature functionality (abc123d)\\n- Implement better error handling (def456e)\\n\\n### Bug Fixes\\n- Fix edge case in processing (789ghi0)\\n\\n### File Changes\\n3 files changed, 42 insertions(+), 5 deletions(-)"
}
EOF
      exit 0
    fi
    
    # Check if this is a general completion request
    if echo "$PROMPT_CONTENT" | grep -q "complete\|generate\|create"; then
      # Generate a generic response
      echo "Mock response from gemapi for model $MODEL"
      echo "This is a simulated LLM response for testing purposes."
      exit 0
    fi
  fi
  
  # Default response for unknown requests
  echo "Mock gemapi response"
  exit 0
fi

# If not in request mode, show version or help
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "gemapi mock v0.0.1-test"
  echo "Model: $MODEL"
else
  echo "gemapi mock - Simulated LLM API for testing"
  echo "Usage: gemapi request --model <model> --file <prompt-file> --yes"
fi
`

const ghSyncDepsMockScript = `#!/bin/bash
# Mock gh CLI for sync-deps release testing

# Log all calls for verification
echo "gh $@" >> "${GH_MOCK_LOG:-/tmp/gh-mock.log}"

# Simulate state tracking - after a few calls, return a workflow run
STATE_FILE="/tmp/gh-mock-state"
if [ -f "$STATE_FILE" ]; then
    COUNT=$(cat "$STATE_FILE")
else
    COUNT=0
fi

# Parse arguments
if [[ "$1" == "run" ]]; then
    if [[ "$2" == "list" ]]; then
        # Increment counter
        COUNT=$((COUNT + 1))
        echo $COUNT > "$STATE_FILE"
        
        # Check what workflow is being requested
        if [[ "$*" == *"--workflow Release"* ]]; then
            # After a few calls, return a release workflow run to simulate it appearing
            if [ $COUNT -gt 3 ]; then
                echo '[{"databaseId": 12345, "status": "in_progress", "conclusion": null, "headBranch": "v0.1.1", "event": "push", "workflowName": "Release"}]'
                rm -f "$STATE_FILE"  # Reset for next test
            else
                echo '[]'
            fi
        elif [[ "$*" == *"--workflow CI"* ]]; then
            # Return a CI workflow run
            echo '[{"databaseId": 12346, "status": "completed", "conclusion": "success", "createdAt": "2025-09-24T00:00:00Z"}]'
        else
            # Return empty for other queries
            echo '[]'
        fi
        exit 0
    elif [[ "$2" == "watch" ]]; then
        # Control CI success/failure via env var
        if [[ "${GH_MOCK_CI_STATUS}" == "failure" ]]; then
            echo "Error: release workflow failed" >&2
            exit 1
        else
            echo "✓ Workflow run completed successfully"
            exit 0
        fi
    fi
elif [[ "$1" == "api" ]]; then
    # Handle API calls
    echo '{"tag_name": "v0.1.0"}'
    exit 0
fi

# Default success for other commands
exit 0
`

const goSyncDepsMockScript = `#!/bin/bash
# Mock go CLI for sync-deps release testing

# Log all calls for debugging
echo "go $@" >> /tmp/go-mock.log
echo "PWD: $(pwd)" >> /tmp/go-mock.log

case "$1" in
    "get")
        # Simulate updating go.mod
        # $2 is the module@version string (e.g., github.com/test/lib-a@v0.1.1)
        MODULE_SPEC="$2"
        MODULE_PATH=$(echo "$MODULE_SPEC" | cut -d'@' -f1)
        NEW_VERSION=$(echo "$MODULE_SPEC" | cut -d'@' -f2)
        
        echo "MODULE_SPEC: $MODULE_SPEC" >> /tmp/go-mock.log
        echo "MODULE_PATH: $MODULE_PATH" >> /tmp/go-mock.log
        echo "NEW_VERSION: $NEW_VERSION" >> /tmp/go-mock.log
        
        # Find go.mod file
        if [ -f "go.mod" ]; then
            GO_MOD_FILE="go.mod"
        else
            GO_MOD_FILE=$(find . -name go.mod -print -quit 2>/dev/null)
        fi
        
        echo "GO_MOD_FILE: $GO_MOD_FILE" >> /tmp/go-mock.log

        if [ -f "$GO_MOD_FILE" ]; then
            echo "Before update:" >> /tmp/go-mock.log
            cat "$GO_MOD_FILE" >> /tmp/go-mock.log
            
            # Use sed to replace the version - handle both BSD and GNU sed
            if sed --version 2>/dev/null | grep -q GNU; then
                # GNU sed
                sed -i -E "s|($MODULE_PATH\s+)v[0-9]+\.[0-9]+\.[0-9]+|\1$NEW_VERSION|g" "$GO_MOD_FILE"
            else
                # BSD sed (macOS)
                sed -i '' -E "s|($MODULE_PATH[[:space:]]+)v[0-9]+\.[0-9]+\.[0-9]+|\1$NEW_VERSION|g" "$GO_MOD_FILE"
            fi
            
            echo "After update:" >> /tmp/go-mock.log
            cat "$GO_MOD_FILE" >> /tmp/go-mock.log
        fi
        exit 0
        ;;
    "list")
        # Simulate successful list
        if [[ "$2" == "-m" ]]; then
            # Return module info
            echo "github.com/test/lib-a v0.1.1"
        fi
        exit 0
        ;;
    "mod")
        # Succeed for mod commands (tidy, download, etc)
        exit 0
        ;;
    *)
        # Pass through for other commands
        exit 0
        ;;
esac
`

const gitPushMockScript = `#!/bin/bash
# Mock git that passes through all commands except push
# This allows real git operations but prevents push failures in tests

if [ "$1" = "push" ]; then
    # Simulate successful push
    echo "To fake://repo"
    echo " * [new tag]         v0.1.1 -> v0.1.1"
    exit 0
else
    # Pass through to real git
    exec /usr/bin/git "$@"
fi
`

const gitSyncDepsMockScript = `#!/bin/bash
# Mock git CLI for sync-deps release testing

# State directory for tags - use .git directory if exists
GIT_DIR=".git"
if [ ! -d "$GIT_DIR" ]; then
    mkdir -p "$GIT_DIR"
fi
TAG_FILE="$GIT_DIR/mock_tag"

case "$1" in
    "describe")
        # Handle git describe --tags --abbrev=0
        if [[ "$2" == "--tags" ]]; then
            if [ -f "$TAG_FILE" ]; then
                cat "$TAG_FILE"
                exit 0
            else
                # No tags found
                echo "fatal: No names found, cannot describe anything." >&2
                exit 128
            fi
        fi
        exit 0
        ;;
    "tag")
        # Handle git tag <tagname>
        if [ -n "$2" ] && [ "$2" != "-l" ] && [ "$2" != "--list" ]; then
            echo "$2" > "$TAG_FILE"
        fi
        # Handle git tag -l or git tag --list
        if [ "$2" = "-l" ] || [ "$2" = "--list" ] || [ -z "$2" ]; then
            if [ -f "$TAG_FILE" ]; then
                cat "$TAG_FILE"
            fi
        fi
        exit 0
        ;;
    "rev-list")
        # Simulate that there are commits since the last tag
        echo "abc123def"
        exit 0
        ;;
    "rev-parse")
        # Handle various rev-parse commands
        case "$2" in
            "--short")
                echo "abc123d"
                ;;
            "--abbrev-ref")
                echo "main"
                ;;
            "HEAD")
                echo "abc123def456789"
                ;;
            *)
                echo "abc123def456789"
                ;;
        esac
        exit 0
        ;;
    "status")
        # Simulate clean working directory
        if [[ "$2" == "--porcelain" ]]; then
            # Return empty for clean status
            exit 0
        else
            echo "On branch main"
            echo "nothing to commit, working tree clean"
            exit 0
        fi
        ;;
    "diff")
        # Simulate no differences
        exit 0
        ;;
    "log")
        # Simulate git log output
        echo "commit abc123def456789"
        echo "Author: Test User <test@example.com>"
        echo "Date:   Thu Sep 24 12:00:00 2024 -0400"
        echo ""
        echo "    feat: add new feature"
        exit 0
        ;;
    "init"|"add"|"commit"|"push"|"config"|"submodule"|"remote"|"checkout"|"branch")
        # Succeed for all other commands
        exit 0
        ;;
    *)
        exit 0
        ;;
esac
`