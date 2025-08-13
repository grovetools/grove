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