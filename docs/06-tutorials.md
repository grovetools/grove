# Grove Tutorials

This guide provides step-by-step tutorials for common Grove workflows, from creating a new ecosystem to performing coordinated releases.

## Tutorial 1: Creating a New Ecosystem

Learn how to set up a new Grove ecosystem from scratch, configure workspaces, and add your first tools.

### Step 1: Initialize the Ecosystem

Create a new directory for your ecosystem and initialize it:

```bash
mkdir my-grove-ecosystem
cd my-grove-ecosystem
grove workspace init --name "My Ecosystem" --description "Custom tools for our team"
```

This creates:
- `grove.yml`: Ecosystem configuration file
- `go.work`: Go workspace configuration
- `Makefile`: Build automation
- `.gitignore`: Standard ignore patterns

### Step 2: Configure Workspace Patterns

Edit `grove.yml` to define how Grove discovers your projects:

```yaml
name: my-ecosystem
description: Custom tools for our team
workspaces:
  - "tools/*"        # All directories under tools/
  - "libs/*"         # Shared libraries
  - "services/*"     # Service applications
```

### Step 3: Initialize Git Repository

```bash
git init
git add .
git commit -m "feat: initialize Grove ecosystem"
```

### Step 4: Add Your First Tool

Create a new tool in the ecosystem:

```bash
grove add-repo my-tool --alias mt --description "My first Grove tool" --ecosystem
```

This command:
- Creates a new repository structure
- Adds it as a Git submodule (if using --ecosystem)
- Sets up standard Grove configuration
- Creates GitHub repository (optional)

### Step 5: Verify the Setup

List all workspaces to confirm everything is configured:

```bash
grove workspace list
grove list
```

## Tutorial 2: Adding a New Tool

This tutorial walks through adding a new tool to an existing Grove ecosystem, from repository creation to integration.

### Step 1: Choose a Template

Grove supports multiple project templates:
- `go`: Standard Go CLI application
- `maturin`: Python/Rust hybrid projects
- `react-ts`: React TypeScript applications
- Custom GitHub templates

### Step 2: Create the Tool Repository

#### For a Go Tool:
```bash
grove add-repo data-processor --alias dp \
  --description "High-performance data processing tool" \
  --template go \
  --ecosystem
```

#### For a Python/Rust Tool:
```bash
grove add-repo ml-analyzer --alias mla \
  --description "Machine learning analysis tool" \
  --template maturin \
  --ecosystem
```

#### Using a Custom Template:
```bash
grove add-repo custom-tool --alias ct \
  --template myorg/custom-template \
  --ecosystem
```

### Step 3: Navigate to the New Tool

```bash
cd data-processor
```

### Step 4: Customize the Tool

Edit the generated files:

1. **Update grove.yml**:
```yaml
name: data-processor
description: High-performance data processing tool
binary:
  name: data-processor
  path: ./bin/data-processor
type: go
```

2. **Implement Your Logic**:
Edit `cmd/root.go` to add your tool's functionality.

3. **Add Dependencies**:
```bash
go get github.com/mattsolo1/grove-core
go mod tidy
```

### Step 5: Build and Test

```bash
make build
./bin/data-processor --help
```

### Step 6: Link for Development

```bash
grove dev link dp ./bin/data-processor
```

Now your tool is available globally via Grove:
```bash
grove dp --version
dp --help
```

### Step 7: Commit and Push

```bash
git add .
git commit -m "feat: implement data processor core functionality"
git push origin main
```

## Tutorial 3: Local Development Workflow

Learn how to efficiently develop Grove tools using git worktrees, development links, and the Grove dev commands.

### Prerequisites

- An existing Grove tool repository
- Grove installed and configured

### Step 1: Create a Feature Branch

```bash
cd ~/grove-ecosystem/grove-context
git checkout -b feature/new-capability
```

### Step 2: Set Up a Git Worktree

For parallel development without switching branches:

```bash
git worktree add ../grove-context-feature feature/new-capability
cd ../grove-context-feature
```

Benefits:
- Keep main branch available
- Test multiple versions simultaneously
- No need to stash changes

### Step 3: Build Your Changes

```bash
make dev  # Build with development flags
```

### Step 4: Create a Development Link

Link your local build for testing:

```bash
grove dev link cx ./bin/context --name feature
```

This creates a named link that you can switch between.

### Step 5: Activate the Development Version

```bash
grove dev use cx feature
```

Now `cx` points to your development build:
```bash
cx --version  # Shows your dev version
which cx      # Points to ~/.grove/bin/cx
```

### Step 6: Test Your Changes

Your development version is now active globally:

```bash
# Test in any directory
cd ~/projects/other-project
cx update  # Uses your dev version

# Run integration tests
cd ~/grove-ecosystem/grove-context-feature
make test-e2e
```

### Step 7: Use the Dev TUI

For visual management of development links:

```bash
grove dev tui
```

The TUI allows you to:
- View all development links
- Switch between versions quickly
- Monitor active versions
- Clean up old links

### Step 8: Reset to Stable Version

When development is complete:

```bash
grove dev reset cx
```

Or reset all tools:
```bash
grove dev reset
```

### Step 9: Clean Up Worktree

After merging your feature:

```bash
cd ~/grove-ecosystem/grove-context
git worktree remove ../grove-context-feature
grove dev prune  # Remove broken links
```

## Tutorial 4: Performing an Ecosystem Release

Learn how to orchestrate a release across multiple Grove tools with proper dependency management.

### Prerequisites

- Clean repositories (no uncommitted changes)
- Push access to repositories
- Understanding of semantic versioning

### Step 1: Check Repository Status

Ensure all repositories are clean:

```bash
grove workspace status
```

Address any uncommitted changes before proceeding.

### Step 2: Sync Dependencies

Update all Grove dependencies to their latest versions:

```bash
grove deps sync --commit
```

This ensures all tools use the latest versions of shared libraries.

### Step 3: Launch the Release TUI

For interactive release planning:

```bash
grove release tui
```

The TUI provides:
- Dependency graph visualization
- Version bump selection (major/minor/patch)
- Changelog preview
- Git status monitoring

### Step 4: Plan Version Bumps

In the TUI, use arrow keys to navigate and select version bumps:
- `↑/↓`: Navigate between repositories
- `←/→`: Select version bump type
- `Space`: Toggle repository selection
- `Enter`: Generate changelogs
- `r`: Execute release

### Alternative: Command-Line Release

For non-interactive releases:

```bash
# Patch release for all
grove release --yes

# Mixed version bumps
grove release --minor grove-core --patch grove-context

# Dry run to preview
grove release --dry-run --minor grove-core
```

### Step 5: Review the Release Plan

Grove will display:
- Proposed versions for each tool
- Dependency update order
- Changelog summaries

Example output:
```
Release Plan:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Level 0 (no dependencies):
  grove-core: v0.2.13 → v0.3.0 (minor)
  
Level 1 (depends on level 0):
  grove-context: v0.2.1 → v0.2.2 (patch)
  grove-flow: v0.1.0 → v0.1.1 (patch)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

### Step 6: Confirm and Execute

After reviewing, confirm the release. Grove will:

1. **Tag Level 0 repositories**:
   - Apply version tags
   - Push tags to trigger CI/CD

2. **Wait for builds** (if configured):
   - Monitor GitHub Actions
   - Verify successful builds

3. **Update Level 1 dependencies**:
   - Update go.mod files with new versions
   - Run go mod tidy
   - Commit changes

4. **Tag Level 1 repositories**:
   - Apply version tags
   - Push to trigger releases

5. **Continue through all levels**

### Step 7: Verify Releases

Check that all releases completed successfully:

```bash
# Check GitHub releases
grove workspace list | while read workspace; do
  echo "Checking $workspace..."
  gh release view --repo mattsolo1/$workspace
done

# Update local installations
grove update all
```

### Step 8: Update Documentation

After a successful release:

1. Update CHANGELOG.md files if needed
2. Update version references in documentation
3. Announce the release to your team

## Tutorial 5: Managing Dependencies

Learn how to effectively manage dependencies across your Grove ecosystem.

### Viewing the Dependency Tree

Understand your ecosystem's dependency structure:

```bash
grove deps tree
```

Output shows the hierarchy:
```
grove-meta
├── grove-core@v0.2.13
└── grove-tend@v0.2.19
    └── grove-core@v0.2.13

grove-context
└── grove-core@v0.2.13
```

### Updating a Specific Dependency

Update a shared library across all tools:

```bash
# Update to specific version
grove deps bump github.com/mattsolo1/grove-core@v0.3.0 --commit

# Update to latest
grove deps bump github.com/mattsolo1/grove-core@latest --commit
```

### Handling Breaking Changes

When a dependency introduces breaking changes:

1. **Create feature branches**:
```bash
grove workspace list | while read workspace; do
  cd $workspace
  git checkout -b update/grove-core-v1
done
```

2. **Update and test individually**:
```bash
cd grove-context
go get github.com/mattsolo1/grove-core@v1.0.0
make test
# Fix any breaking changes
git commit -am "feat: update to grove-core v1.0.0"
```

3. **Coordinate the release**:
```bash
grove release --minor grove-context grove-flow
```

## Tutorial 6: Setting Up CI/CD Integration

Configure GitHub Actions to work with Grove releases.

### Step 1: Create Release Workflow

Create `.github/workflows/release.yml` in each repository:

```yaml
name: Release

on:
  push:
    tags:
      - 'v*'

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - uses: actions/setup-go@v5
        with:
          go-version: '1.21'
      
      - name: Build
        run: make build
      
      - name: Create Release
        uses: softprops/action-gh-release@v1
        with:
          files: |
            bin/*
          generate_release_notes: true
```

### Step 2: Configure Grove Registry

Update your ecosystem's registry.json when tools are released:

```json
{
  "tools": [
    {
      "name": "grove-context",
      "alias": "cx",
      "repository": "github.com/yourorg/grove-context",
      "binary": "context",
      "version": "latest",
      "description": "Context management for LLMs"
    }
  ]
}
```

### Step 3: Automate Registry Updates

Create a workflow to update the registry on release:

```yaml
name: Update Registry

on:
  repository_dispatch:
    types: [tool-released]

jobs:
  update:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Update Registry
        run: |
          # Update registry.json with new version
          # Implementation depends on your needs
      
      - name: Commit Changes
        run: |
          git config user.name "GitHub Actions"
          git config user.email "actions@github.com"
          git add registry.json
          git commit -m "chore: update registry for ${{ github.event.client_payload.tool }}"
          git push
```

## Tutorial 7: Working with Private Repositories

Set up Grove to work with private repositories in your organization.

### Step 1: Configure GitHub CLI

Install and authenticate the GitHub CLI:

```bash
# Install gh
brew install gh  # macOS
# or see https://cli.github.com

# Authenticate
gh auth login
```

### Step 2: Configure Grove

Grove automatically detects gh authentication, but you can be explicit:

```bash
# Install from private repos
grove install all --use-gh

# Update from private repos
grove update all --use-gh
```

### Step 3: Set Up Team Access

For team environments, create a shared installation script:

```bash
#!/bin/bash
# install-grove-team.sh

# Check gh authentication
if ! gh auth status &>/dev/null; then
  echo "Please authenticate with GitHub first:"
  echo "  gh auth login"
  exit 1
fi

# Install Grove
curl -sSfL https://raw.githubusercontent.com/yourorg/grove-meta/main/scripts/install.sh | sh

# Install all tools from private repos
grove install all --use-gh

echo "Grove ecosystem installed successfully!"
```

### Step 4: Configure CI/CD for Private Repos

In GitHub Actions, use repository secrets:

```yaml
- name: Install Grove Tools
  env:
    GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
  run: |
    grove install all --use-gh
```

## Best Practices

### 1. Version Management

- Use semantic versioning consistently
- Document breaking changes clearly
- Keep tools at compatible versions
- Test version combinations before releasing

### 2. Development Workflow

- Use git worktrees for parallel development
- Name development links descriptively
- Clean up old development links regularly
- Test with both dev and release versions

### 3. Release Management

- Always sync dependencies before releasing
- Use the TUI for complex releases
- Perform dry runs for major releases
- Monitor CI/CD pipelines during releases

### 4. Dependency Management

- Keep shared libraries minimal and focused
- Version shared libraries carefully
- Update dependencies regularly but thoughtfully
- Use dependency groups for related updates

### 5. Team Collaboration

- Document your ecosystem's conventions
- Use consistent naming patterns
- Maintain a clear registry.json
- Communicate releases to the team

## Troubleshooting Common Issues

### Development Link Not Working

```bash
# Check link status
grove dev status

# Verify binary exists
ls -la $(grove dev list cx | grep Path)

# Reset and relink
grove dev reset cx
grove dev link cx ./bin/context
```

### Release Fails Due to Dirty Repository

```bash
# Check status
grove workspace status

# Stash or commit changes
cd problem-repo
git stash
# or
git commit -am "WIP: save changes"
```

### Dependency Update Conflicts

```bash
# Check current versions
grove deps tree

# Update incrementally
grove deps bump github.com/mattsolo1/grove-core@v0.2.13
# Test
grove deps bump github.com/mattsolo1/grove-core@v0.3.0
```

## Next Steps

- Explore [Command Reference](./05-command-reference.md) for detailed command options
- Read [Core Concepts](./04-core-concepts.md) for deeper understanding
- Check [Configuration Guide](./07-configuration.md) for customization options
- See [Architecture](./08-architecture.md) for system design details
