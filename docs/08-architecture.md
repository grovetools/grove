# Grove Architecture

This document describes the high-level architecture of the Grove ecosystem and the internal design of the Grove meta-tool.

## System Overview

Grove follows a distributed architecture pattern where:
- Each tool is an independent repository with its own release cycle
- The Grove meta-tool acts as the central orchestrator
- Tools communicate through well-defined interfaces
- Binary management uses a layered symlink system
- Configuration is declarative and file-based

```
┌─────────────────────────────────────────────────────────────┐
│                      User Commands                           │
└─────────────────────────────┬───────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                     Grove Meta-CLI                           │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐    │
│  │ Command  │  │   Tool   │  │ Version  │  │   Dev    │    │
│  │ Dispatch │  │ Registry │  │ Manager  │  │  Links   │    │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘    │
└─────────────────────────────┬───────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                    ~/.grove Directory                        │
│  ┌────────────┐  ┌────────────┐  ┌────────────────────┐    │
│  │    bin/    │  │  versions/ │  │  Configuration     │    │
│  │ (symlinks) │  │  (tools)   │  │  (JSON files)      │    │
│  └────────────┘  └────────────┘  └────────────────────┘    │
└─────────────────────────────┬───────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                     Individual Tools                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐    │
│  │  grove-  │  │  grove-  │  │  grove-  │  │  grove-  │    │
│  │ context  │  │   flow   │  │ notebook │  │   tmux   │    │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘    │
└─────────────────────────────────────────────────────────────┘
```

## Component Architecture

### Grove Meta-CLI Components

The Grove meta-tool is organized into several key packages:

#### `/cmd` - Command Layer
- **Purpose**: Define CLI commands and subcommands
- **Key Files**:
  - `root.go`: Main entry point and command delegation
  - `install_cmd.go`: Tool installation logic
  - `dev_*.go`: Development workflow commands
  - `deps.go`: Dependency management commands
  - `release.go`: Release orchestration
  - `workspace_*.go`: Workspace management

#### `/pkg` - Core Logic

##### `/pkg/sdk` - SDK Management
- **Purpose**: Handle tool installation and version management
- **Key Components**:
  - `Manager`: Central SDK manager
  - `ToolRegistry`: Maps tool names to repositories
  - Version tracking and switching
  - Binary download and verification

##### `/pkg/workspace` - Workspace Discovery
- **Purpose**: Find and manage Grove workspaces
- **Key Functions**:
  - `FindRoot()`: Locate ecosystem root
  - `Discover()`: Find all workspaces using glob patterns
  - Project enumeration and filtering

##### `/pkg/devlinks` - Development Links
- **Purpose**: Manage local development binary overrides
- **Key Components**:
  - `Config`: Development links configuration
  - `BinaryLinks`: Per-tool link management
  - `LinkInfo`: Individual link metadata
  - Registry persistence in `devlinks.json`

##### `/pkg/reconciler` - Version Reconciliation
- **Purpose**: Intelligent layering of dev and release versions
- **Algorithm**:
  1. Check for active dev link
  2. Fall back to release version
  3. Update symlinks accordingly
  4. Handle missing binaries gracefully

##### `/pkg/depsgraph` - Dependency Graph
- **Purpose**: Build and analyze project dependency relationships
- **Key Features**:
  - Graph construction from go.mod files
  - Topological sorting for release order
  - Cycle detection
  - Level-based grouping

##### `/pkg/project` - Project Handlers
- **Purpose**: Abstract project-type-specific operations
- **Supported Types**:
  - `GoHandler`: Go module projects
  - `MaturinHandler`: Python/Rust hybrid projects
  - `NodeHandler`: Node.js projects
  - `TemplateHandler`: Project templates
- **Interface**:
  ```go
  type ProjectHandler interface {
      ParseDependencies(path string) ([]Dependency, error)
      UpdateDependency(path string, dep Dependency) error
      GetVersion(path string) (string, error)
      SetVersion(path string, version string) error
      GetBuildCommand() string
      GetTestCommand() string
  }
  ```

##### `/pkg/release` - Release Orchestration
- **Purpose**: Coordinate multi-project releases
- **Features**:
  - Version bump calculation
  - Changelog generation
  - Git operations
  - CI/CD integration

##### `/pkg/gh` - GitHub Integration
- **Purpose**: Interact with GitHub API
- **Features**:
  - Release creation and management
  - Issue and PR operations
  - Repository information retrieval

## Directory Structure

### `~/.grove` Directory Layout

The Grove home directory contains all installed tools and configuration:

```
~/.grove/
├── bin/                           # Active tool binaries
│   ├── grove                     # Grove meta-tool
│   ├── context                   # Full binary or symlink
│   ├── cx → context              # Alias symlink
│   ├── flow                      # Another tool
│   └── ...
│
├── versions/                      # All installed versions
│   ├── context/
│   │   ├── v0.2.0/
│   │   │   └── bin/
│   │   │       └── context
│   │   └── v0.2.1/
│   │       └── bin/
│   │           └── context
│   └── flow/
│       └── v0.1.0/
│           └── bin/
│               └── flow
│
├── active_versions.json          # Currently active versions
├── devlinks.json                 # Development link registry
└── config.yml                    # User configuration (future)
```

### File Formats

#### `active_versions.json`
```json
{
  "grove-context": "v0.2.1",
  "grove-flow": "v0.1.0",
  "grove-notebook": "v0.3.0"
}
```

#### `devlinks.json`
```json
{
  "binaries": {
    "context": {
      "links": {
        "feature-branch": {
          "path": "/home/user/projects/grove-context-feature/bin/context",
          "worktree_path": "/home/user/projects/grove-context-feature",
          "registered_at": "2024-01-15T10:30:00Z"
        }
      },
      "current": "feature-branch"
    }
  }
}
```

## Binary Management Architecture

### Symlink Hierarchy

Grove uses a multi-level symlink system for flexibility:

1. **Alias Level**: `cx` → `context`
2. **Binary Level**: `context` → `../versions/context/v0.2.1/bin/context`
3. **Dev Override**: `context` → `/path/to/dev/binary`

### Version Resolution Algorithm

```python
def resolve_tool_binary(tool_name):
    # 1. Check for dev override
    if dev_link_exists(tool_name):
        return get_dev_link_path(tool_name)
    
    # 2. Check for active release version
    if active_version_exists(tool_name):
        version = get_active_version(tool_name)
        return get_version_path(tool_name, version)
    
    # 3. Tool not available
    return None
```

### Installation Process

1. **Download Binary**:
   - Detect platform (OS/arch)
   - Fetch from GitHub releases
   - Verify checksums (if available)

2. **Store Version**:
   - Create version directory
   - Place binary with correct permissions
   - Update version metadata

3. **Activate Version**:
   - Update active_versions.json
   - Create/update symlinks in bin/
   - Verify execution

## Command Delegation Architecture

### Delegation Flow

```
User Input: grove cx update
     │
     ▼
Parse Command
     │
     ├─> Is "cx" a Grove subcommand? → No
     │
     ├─> Is "cx" a known tool? → Yes
     │
     ├─> Resolve binary path
     │
     ├─> Execute: exec.Command(binaryPath, "update")
     │
     └─> Pass through stdio
```

### Subcommand Detection

Grove distinguishes between its own subcommands and tool delegation:

1. Check against known Grove subcommands (install, update, dev, etc.)
2. If not found, attempt tool delegation
3. If tool not found, show error with suggestions

## Polyglot Support Architecture

### Project Type Registry

Grove supports multiple project types through a handler registry:

```go
registry := project.NewRegistry()
registry.Register(project.TypeGo, NewGoHandler())
registry.Register(project.TypeMaturin, NewMaturinHandler())
registry.Register(project.TypeNode, NewNodeHandler())
```

### Handler Responsibilities

Each handler implements project-specific operations:

- **Dependency Parsing**: Extract from go.mod, package.json, pyproject.toml
- **Version Management**: Read/write version information
- **Build Commands**: Return appropriate build/test commands
- **Project Detection**: Check for project files

### Build System Integration

Grove leverages Makefiles for build consistency:

```makefile
# Standard targets expected by Grove
build:      # Build the project
test:       # Run tests
clean:      # Clean build artifacts
dev:        # Development build
release:    # Release build
```

## Release Architecture

### Dependency-Based Release Ordering

Grove uses topological sorting to determine release order:

```
Level 0: [grove-core, grove-tend]           # No dependencies
Level 1: [grove-context, grove-flow]        # Depend on Level 0
Level 2: [grove-meta]                       # Depends on Level 0 & 1
```

### Release State Machine

```
     ┌─────────┐
     │  Start  │
     └────┬────┘
          │
          ▼
    ┌────────────┐
    │ Check Deps │
    └─────┬──────┘
          │
          ▼
    ┌────────────┐
    │ Calculate  │
    │  Versions  │
    └─────┬──────┘
          │
          ▼
    ┌────────────┐
    │  Generate  │
    │ Changelogs │
    └─────┬──────┘
          │
          ▼
    ┌────────────┐
    │   Tag &    │
    │   Release  │
    └─────┬──────┘
          │
          ▼
    ┌────────────┐
    │   Update   │
    │ Dependents │
    └─────┬──────┘
          │
          ▼
     ┌─────────┐
     │Complete │
     └─────────┘
```

## Workspace Discovery Architecture

### Discovery Algorithm

```python
def discover_workspaces(root_dir):
    config = load_config(f"{root_dir}/grove.yml")
    workspaces = []
    
    for pattern in config.workspaces:
        matches = glob(f"{root_dir}/{pattern}")
        for match in matches:
            if is_directory(match) and exists(f"{match}/grove.yml"):
                workspaces.append(match)
    
    return unique(workspaces)
```

### Workspace Context

Grove maintains workspace context for operations:

- Current workspace detection
- Workspace-scoped commands
- Cross-workspace operations
- Workspace status aggregation

## Security Architecture

### Binary Verification

- Download from official GitHub releases
- HTTPS-only downloads
- Future: Signature verification
- Future: Checksum validation

### Private Repository Support

- GitHub CLI integration for authentication
- Token-based access for CI/CD
- Secure credential storage
- No credentials in configuration files

## Performance Optimizations

### Lazy Loading

- Commands are loaded on-demand
- Tool registry loaded only when needed
- Configuration cached in memory
- Workspace discovery results cached

### Parallel Operations

- Concurrent tool installations
- Parallel dependency updates
- Batch GitHub API calls
- Concurrent workspace operations

### Efficient File Operations

- Symlinks instead of copies
- Incremental updates
- Minimal file I/O
- Strategic caching

## Extension Points

### Custom Project Types

Add support for new languages/frameworks:

1. Implement `ProjectHandler` interface
2. Register with project registry
3. Define build commands
4. Add configuration support

### Custom Commands

Extend Grove with new functionality:

1. Add command file in `/cmd`
2. Register with root command
3. Implement business logic
4. Update documentation

### Tool Integration

Add new tools to the ecosystem:

1. Update tool registry
2. Configure repository mapping
3. Set up CI/CD for releases
4. Update installation scripts

## Future Architecture Enhancements

### Planned Improvements

1. **Plugin System**:
   - Dynamic command loading
   - Third-party extensions
   - Custom tool providers

2. **Remote Configuration**:
   - Centralized configuration server
   - Team configuration sharing
   - Configuration versioning

3. **Advanced Caching**:
   - Binary cache server
   - Distributed caching
   - Offline mode support

4. **Enhanced Security**:
   - Binary signature verification
   - Sandboxed execution
   - Audit logging

5. **Improved Performance**:
   - Background updates
   - Predictive prefetching
   - Compressed transfers

## Design Principles

### Core Principles

1. **Simplicity**: Easy to understand and use
2. **Flexibility**: Adapt to different workflows
3. **Reliability**: Consistent and predictable behavior
4. **Performance**: Fast operations with minimal overhead
5. **Extensibility**: Easy to add new features and tools

### Architectural Decisions

1. **File-based Configuration**: Simple, version-controlled, portable
2. **Symlink-based Binary Management**: Flexible, efficient, transparent
3. **Distributed Architecture**: Independent tool development and releases
4. **Convention over Configuration**: Sensible defaults, minimal setup
5. **Progressive Disclosure**: Simple for beginners, powerful for experts

## Summary

Grove's architecture is designed to be:

- **Modular**: Clear separation of concerns
- **Scalable**: Handles ecosystems of any size
- **Maintainable**: Clean code organization
- **Testable**: Well-defined interfaces
- **Extensible**: Easy to add new capabilities

The architecture supports Grove's mission of providing a powerful, flexible, and user-friendly ecosystem management system for CLI tools.