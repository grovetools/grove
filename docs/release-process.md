# Grove Ecosystem Release Process

This document describes the decentralized release process for the Grove ecosystem, where each tool can be released independently while maintaining compatibility through meta-releases.

## Overview

The Grove ecosystem uses a decentralized release model where:
- Each tool (grove-context, grove-flow, etc.) has its own repository and releases independently
- Each tool maintains its own version numbering
- The grove-meta repository creates meta-releases that track compatible versions
- Users install tools from their individual repositories

## Key Components

### 1. Tool Repositories
Each Grove tool lives in its own repository:
- `grove-meta` - The main CLI and SDK manager
- `grove-context` (cx) - Context management tool
- `grove-flow` (flow) - Workflow automation
- `grove-notebook` (nb) - Notebook functionality
- `grove-version` (gvm) - Version management
- `grove-proxy` (px) - Proxy functionality
- `grove-sandbox` (sb) - Sandboxing tool
- `grove-tend` (tend) - Maintenance utilities
- `grove-canopy` - Canopy functionality

### 2. Release Workflow
Each tool repository contains a `.github/workflows/release.yml` that:
- Triggers on version tags (e.g., `v0.2.0`)
- Builds binaries for multiple platforms (darwin/linux, amd64/arm64)
- Handles private Go module dependencies using GROVE_PAT
- Creates GitHub releases with the built binaries

### 3. SDK Manager
The SDK manager in grove-meta handles tool installation:
- Maps tool names to their repositories
- Downloads binaries from individual tool releases
- Manages versions in `~/.grove/versions/`
- Creates symlinks in `~/.grove/bin/`

## Release Process

### Releasing an Individual Tool

1. **Make your changes** and commit them to the tool's repository

2. **Create and push a version tag**:
   ```bash
   cd grove-context  # or any other tool directory
   git tag v0.2.0
   git push origin v0.2.0
   ```

3. **The GitHub Action will automatically**:
   - Build binaries for all platforms
   - Create a GitHub release
   - Upload the binaries as release assets

4. **Verify the release**:
   ```bash
   gh release view v0.2.0
   ```

### Creating a Meta-Release

Meta-releases track compatible versions across all tools.

1. **Tag all tools with their current versions** (if not already tagged)

2. **Run the grove release command**:
   ```bash
   grove release create-meta-release v0.3.0
   ```

   This command:
   - Collects the latest tag from each submodule
   - Creates a manifest of compatible versions
   - Tags the grove-ecosystem repository
   - Triggers the meta-release workflow

3. **The meta-release workflow**:
   - Creates a GitHub release in grove-ecosystem
   - Includes a compatibility matrix in the release notes
   - Does NOT build binaries (those come from individual repos)

## Installation Process

### For End Users

1. **Bootstrap installation**:
   ```bash
   curl -sSfL https://raw.githubusercontent.com/mattsolo1/grove-ecosystem/main/scripts/install.sh | sh
   ```

   This installs the `grove` CLI from grove-meta.

2. **Install tools**:
   ```bash
   # Install specific tools
   grove install cx flow nb

   # Install specific versions
   grove install cx@v0.2.0

   # Install all tools
   grove install all
   ```

3. **Tools are downloaded from their individual repositories**

### For Private Repositories

Use the `--use-gh` flag to leverage GitHub CLI authentication:
```bash
grove install --use-gh cx
```

## Technical Details

### Release Workflow Configuration

Each tool's release workflow must:

1. **Configure authentication for private modules**:
   ```yaml
   - name: Configure git for private modules
     run: |
       git config --global url."https://${{ secrets.GROVE_PAT }}@github.com/".insteadOf "https://github.com/"
       go env -w GOPRIVATE=github.com/mattsolo1/*
       go env -w GOPROXY=direct
   ```

2. **Regenerate go.sum to avoid checksum mismatches**:
   ```yaml
   - name: Update dependencies
     run: |
       rm -f go.sum
       go mod download
       go mod tidy
   ```

3. **Build with CGO disabled for cross-platform compatibility**:
   ```yaml
   CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -ldflags="-s -w" -o "dist/${output_name}" .
   ```

### Repository Access

Each repository needs a `GROVE_PAT` secret configured in GitHub:
1. Go to repository Settings → Secrets and variables → Actions
2. Add a new secret named `GROVE_PAT`
3. Use a Personal Access Token with repo scope

### Version Tracking

The `grove ws status` command shows release information:
```
grove ws status --cols=git,release
```

This displays:
- Latest release tag for each repository
- Number of commits ahead of the release
- Color coding: yellow (1-10), orange (11-20), red (20+)

## Troubleshooting

### Common Issues

1. **Workflow fails with "Error: Unable to resolve action"**
   - The grove-ecosystem repository is private
   - Solution: Copy workflow content inline instead of using reusable workflows

2. **Build fails with "missing go.sum entry"**
   - Local go.sum differs from CI environment
   - Solution: Regenerate go.sum in the workflow

3. **Cross-platform builds fail**
   - CGO dependencies (like fsevents) cause issues
   - Solution: Set `CGO_ENABLED=0` for all builds

4. **Missing dependencies**
   - Tool imports grove-core but doesn't declare it
   - Solution: Add explicit dependency in go.mod

## Best Practices

1. **Semantic Versioning**: Follow semver (vMAJOR.MINOR.PATCH)
2. **Changelog**: Update CHANGELOG.md before releasing
3. **Testing**: Ensure all tests pass before tagging
4. **Compatibility**: Test with other tools before meta-release
5. **Documentation**: Update docs for breaking changes

## Migration from Centralized Releases

The system has been migrated from a centralized model where:
- Old: All tools built from grove-ecosystem
- New: Each tool builds in its own repository
- Old: Single version for all tools
- New: Independent versioning per tool
- Old: install.sh downloaded from grove-ecosystem
- New: install.sh downloads from grove-meta

The migration preserved all existing functionality while enabling independent tool development and releases.