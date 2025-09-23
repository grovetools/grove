# Grove Core Concepts

Understanding Grove's fundamental concepts will help you leverage its full potential for managing your development ecosystem. This document explains the key architectural decisions and design patterns that make Grove powerful and flexible.

## Workspaces

Workspaces are the foundation of Grove's project discovery and management system. They allow Grove to understand and operate on multiple related projects as a cohesive ecosystem.

### What is a Workspace?

A workspace is any directory containing a `grove.yml` file that defines project metadata and configuration. Workspaces enable Grove to:
- Discover projects automatically
- Apply operations across multiple projects
- Maintain consistent configurations
- Coordinate dependencies and releases

### Workspace Discovery

Grove uses an upward search pattern to find the ecosystem root:

1. Starting from the current directory, Grove searches for a `grove.yml` file
2. It continues searching parent directories until it finds one with a `workspaces` directive
3. This directory becomes the ecosystem root
4. The `workspaces` directive uses glob patterns to discover all project directories

Example root `grove.yml`:
```yaml
name: grove-ecosystem
description: The Grove CLI toolkit ecosystem
workspaces:
  - "grove-*"           # Matches all directories starting with "grove-"
  - "libs/*"            # Matches all directories in libs/
  - "tools/*/project"   # Matches nested project structures
```

### Workspace Configuration

Each workspace has its own `grove.yml` defining:
```yaml
name: grove-context
description: Dynamic context management for LLMs
binary:
  name: context
  path: ./bin/context
type: go  # Project type: go, maturin, node, etc.
```

## Tool Delegation

Grove acts as an intelligent command dispatcher, seamlessly routing commands to the appropriate tool binaries. This delegation system provides a unified interface while maintaining tool independence.

### How Delegation Works

1. **Command Reception**: When you run `grove <tool> [args]`, Grove receives the command
2. **Tool Resolution**: Grove checks if `<tool>` matches a known tool name or alias
3. **Binary Location**: Grove locates the active binary (release or dev version)
4. **Command Forwarding**: Grove executes the binary with all provided arguments
5. **Process Management**: Grove passes through stdin/stdout/stderr transparently

### Delegation Benefits

- **Unified Entry Point**: All tools accessible through `grove` command
- **Version Awareness**: Grove knows which version of each tool is active
- **Alias Support**: Short names (e.g., `cx`) map to full tool names
- **Development Override**: Dev versions automatically take precedence
- **Fallback Behavior**: Direct execution still works (`cx` instead of `grove cx`)

### Example Flow
```bash
grove cx update
# 1. Grove receives "cx update"
# 2. Resolves "cx" to "grove-context"
# 3. Checks for dev override in devlinks.json
# 4. Falls back to release version if no dev link
# 5. Executes: ~/.grove/bin/context update
```

## Version Management

Grove implements a sophisticated layered version management system that supports both stable releases and active development.

### Version Layers

Grove uses a three-layer system for version resolution:

1. **Development Layer** (Highest Priority)
   - Local builds linked via `grove dev link`
   - Stored in `devlinks.json`
   - Takes precedence over all other versions
   - Per-workspace or global scope

2. **Active Release Layer**
   - Installed release versions
   - Tracked in `active_versions.json`
   - One active version per tool
   - Managed via `grove install` and `grove version`

3. **Available Versions Layer**
   - All downloaded versions in `~/.grove/versions/`
   - Can be activated without re-downloading
   - Supports quick version switching

### Version Resolution

When executing a tool, Grove resolves the version in priority order:
```
Check dev link? → Yes → Use dev version
     ↓ No
Check active version? → Yes → Use release version
     ↓ No
Tool not available
```

### Version Storage Structure
```
~/.grove/
├── bin/
│   └── context → ../versions/context/v0.2.1/bin/context
├── versions/
│   └── context/
│       ├── v0.2.0/
│       │   └── bin/context
│       └── v0.2.1/
│           └── bin/context
├── active_versions.json
└── devlinks.json
```

## Dependency Management

Grove provides powerful tools for managing Go module dependencies across the entire ecosystem, ensuring consistency and coordinating updates.

### Dependency Graph

Grove builds a complete dependency graph of all projects:
- Analyzes `go.mod` files in each workspace
- Identifies inter-project dependencies
- Detects circular dependencies
- Calculates release order based on dependencies

### Synchronization

The `grove deps sync` command ensures all Grove dependencies are at their latest versions:

1. **Discovery**: Finds all `github.com/mattsolo1/*` dependencies
2. **Resolution**: Determines latest version for each dependency
3. **Update**: Runs `go get -u` in each affected project
4. **Verification**: Runs `go mod tidy` to clean up
5. **Commit**: Optionally commits changes with conventional commit message

### Dependency Bumping

Target specific dependency updates with `grove deps bump`:
```bash
# Update grove-core to v0.2.1 everywhere
grove deps bump github.com/mattsolo1/grove-core@v0.2.1

# Update to latest version
grove deps bump github.com/mattsolo1/grove-core@latest

# Commit the changes
grove deps bump github.com/mattsolo1/grove-core@latest --commit
```

### Dependency Visualization

The `grove deps tree` command shows the dependency hierarchy:
```
grove-meta
├── grove-core@v0.2.13
└── grove-tend@v0.2.19
    └── grove-core@v0.2.13

grove-context
└── grove-core@v0.2.13
```

## Release Orchestration

Grove's release system coordinates versioned releases across the entire ecosystem, respecting dependencies and ensuring compatibility.

### Dependency-Aware Releases

Releases are organized into levels based on the dependency graph:
- **Level 0**: Projects with no Grove dependencies (e.g., grove-core)
- **Level 1**: Projects depending only on Level 0 projects
- **Level 2**: Projects depending on Level 1 projects
- And so on...

This ensures dependencies are released before their dependents.

### Release Process

1. **Dependency Analysis**: Build complete dependency graph
2. **Version Calculation**: Determine version bumps (major/minor/patch)
3. **Clean Check**: Ensure repositories have no uncommitted changes
4. **Changelog Generation**: Create changelogs from commit history
5. **Dependency Updates**: Update dependencies to newly released versions
6. **Tagging**: Apply version tags to trigger GitHub Actions
7. **Push**: Push tags to origin to initiate releases

### Release Strategies

#### Manual Release
```bash
grove release --minor grove-core --patch grove-context
```

#### Interactive TUI Release
```bash
grove release tui
```
The TUI provides:
- Visual dependency graph
- Version bump selection
- Changelog preview
- Git status monitoring
- One-click release execution

#### Automatic Patch Release
```bash
grove release --yes  # Patch bump everything
```

### Release Coordination

Grove ensures releases happen in the correct order:
1. Release Level 0 projects first
2. Wait for CI/CD to complete
3. Update Level 1 projects with new Level 0 versions
4. Release Level 1 projects
5. Continue through all levels

## Project Types and Polyglot Support

While Grove is primarily designed for Go projects, it supports multiple project types through a pluggable handler system.

### Supported Project Types

- **Go** (default): Standard Go modules with `go.mod`
- **Maturin**: Python/Rust hybrid projects using Maturin
- **Node**: JavaScript/TypeScript projects with `package.json`
- **React**: React applications with specialized build processes

### Project Handlers

Each project type has a handler that knows how to:
- Build the project (`make build`, `maturin build`, `npm build`)
- Run tests (`go test`, `pytest`, `npm test`)
- Create releases (version tagging, changelog generation)
- Manage dependencies

### Handler Registration

Project handlers are registered in the type field of `grove.yml`:
```yaml
name: grove-python-tool
type: maturin  # Uses Maturin handler
binary:
  name: pytool
  path: ./target/release/pytool
```

## Binary Management

Grove manages tool binaries through a sophisticated symlink system that enables version switching and development overrides.

### Binary Installation

When installing a tool:
1. Download platform-specific binary from GitHub releases
2. Store in `~/.grove/versions/<tool>/<version>/bin/`
3. Update `active_versions.json` with the new version
4. Create/update symlink in `~/.grove/bin/`
5. Create alias symlinks (e.g., `cx` → `context`)

### Symlink Management

Grove uses symlinks for flexibility:
- **Direct links**: `~/.grove/bin/context` → actual binary
- **Alias links**: `~/.grove/bin/cx` → `~/.grove/bin/context`
- **Dev overrides**: Point to local build directories

### Platform Support

Grove automatically detects and downloads the correct binary:
- **Darwin/macOS**: `darwin-amd64` or `darwin-arm64`
- **Linux**: `linux-amd64` or `linux-arm64`
- **Architecture**: Automatic detection via `uname`

## Development Workflows

Grove supports sophisticated local development workflows through the `grove dev` command suite.

### Development Links

Create links to local builds:
```bash
cd ~/projects/grove-context
make build
grove dev link context ./bin/context
```

This creates a development override that takes precedence over the release version.

### Workspace Isolation

Development links can be scoped:
- **Global**: Active everywhere (default)
- **Workspace**: Active only in specific workspace
- **Project**: Active only in current project

### Development Commands

- `grove dev link <tool> <path>`: Create a dev link
- `grove dev use <tool> <link-name>`: Activate a specific link
- `grove dev reset [tool]`: Remove dev overrides
- `grove dev status`: Show all active dev links
- `grove dev tui`: Interactive management interface

### Typical Development Flow

1. Clone repository: `git clone <repo>`
2. Create worktree: `git worktree add ../feature-branch`
3. Build project: `make build`
4. Link binary: `grove dev link tool ./bin/tool`
5. Test globally: Tool now uses dev version
6. Reset when done: `grove dev reset tool`

## Summary

These core concepts work together to create a powerful, flexible ecosystem management system:

- **Workspaces** provide project discovery and organization
- **Tool Delegation** offers a unified interface with version awareness
- **Version Management** layers development and release versions intelligently
- **Dependency Management** maintains consistency across projects
- **Release Orchestration** coordinates complex multi-project releases
- **Polyglot Support** extends Grove beyond Go projects
- **Binary Management** handles installation and switching efficiently
- **Development Workflows** support active development without disrupting stability

Understanding these concepts enables you to leverage Grove's full potential for managing complex tool ecosystems efficiently.