# Grove E2E Tests

## Docker-based E2E Tests

The Docker-based E2E tests provide isolated, reproducible testing of the Grove CLI installation and core functionality.

### Running Tests

#### Mock Mode (Default)
Run tests against mock services without requiring GitHub access:
```bash
make test-e2e-docker
```

#### Live Mode
Run tests against real GitHub repositories (requires authentication):
```bash
GITHUB_TOKEN=your_token_here make test-e2e-docker
```

### GitHub Token Requirements

For live mode testing, you need a GitHub Personal Access Token with the following permissions:

**Required Scopes:**
- `repo` (Full control of private repositories)
  - Needed to access private Grove repositories (grove-meta, grove-context, etc.)
  - Required for downloading release assets from private repos

**How to Create a Token:**
1. Go to GitHub Settings → Developer settings → Personal access tokens
2. Click "Generate new token" (classic)
3. Select the `repo` scope
4. Generate and copy the token

**Note:** The token is only needed for live mode testing. Mock mode tests run without any GitHub authentication.

### Test Coverage

The E2E tests validate:
1. **Installation**: Grove CLI installation via install.sh script
2. **Tool Management**: Listing available tools (and installing in live mode)
3. **Workspace Operations**: Creating and initializing Grove workspaces

### Architecture

- **Mock Mode**: Uses a Python HTTP server to simulate GitHub API and release downloads
- **Live Mode**: Uses authenticated `gh` CLI to access real GitHub repositories
- **Docker Environment**: Golang 1.24 container with all necessary tools