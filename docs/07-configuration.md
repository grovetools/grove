# Grove Configuration Guide

Grove uses `grove.yml` configuration files to define ecosystem structure, project metadata, and tool behavior. This guide explains the configuration format and available options.

## Configuration Hierarchy

Grove uses a two-level configuration hierarchy:

1. **Root Configuration** (`grove.yml` at ecosystem root): Defines the overall ecosystem and workspace discovery patterns
2. **Project Configuration** (`grove.yml` in each project): Defines individual tool metadata and build settings

## Root Configuration (Ecosystem Level)

The root `grove.yml` file identifies a Grove ecosystem and defines how to discover its projects.

### Basic Structure

```yaml
name: grove-ecosystem
description: The Grove CLI toolkit ecosystem
workspaces:
  - "grove-*"
  - "libs/*"
  - "tools/*/project"
```

### Configuration Keys

#### `name` (required)
The name of your ecosystem. This should be a short, descriptive identifier.

```yaml
name: my-tools
```

#### `description` (required)
A human-readable description of the ecosystem's purpose.

```yaml
description: Development tools for our team
```

#### `workspaces` (required)
An array of glob patterns that Grove uses to discover project directories. Each pattern is relative to the root configuration file.

```yaml
workspaces:
  - "*"                 # All immediate subdirectories
  - "tools/*"           # All directories under tools/
  - "libs/*/project"    # Nested project structures
  - "services/svc-*"    # Directories matching a pattern
```

### Discovery Rules

When Grove searches for workspaces:
1. It evaluates each glob pattern against the filesystem
2. For each matching directory, it checks for a `grove.yml` file
3. Only directories containing `grove.yml` are considered valid workspaces
4. Duplicates are automatically removed

### Complete Root Example

```yaml
name: acme-grove
description: ACME Corporation's Grove ecosystem
workspaces:
  - "core/*"          # Core libraries
  - "tools/*"         # CLI tools
  - "services/*"      # Service applications
  - "experiments/*"   # Experimental projects
```

## Project Configuration (Workspace Level)

Each workspace/project has its own `grove.yml` defining its specific configuration.

### Basic Structure

```yaml
name: grove-context
description: Dynamic context management for LLMs
binary:
  name: context
  path: ./bin/context
type: go
```

### Configuration Keys

#### `name` (required)
The project name. This should match the repository/directory name for consistency.

```yaml
name: grove-analyzer
```

#### `description` (required)
A concise description of what the tool does.

```yaml
description: Static analysis tool for Grove projects
```

#### `binary` (optional)
Defines the binary output for CLI tools.

```yaml
binary:
  name: analyzer           # Name of the binary
  path: ./bin/analyzer     # Path relative to project root
```

##### Binary Configuration Details:
- `name`: The executable name users will run
- `path`: Build output location (relative to project root)
- Used by Grove for installation and version management
- Can be omitted for libraries that don't produce binaries

#### `type` (optional, default: "go")
Specifies the project type, which determines how Grove handles builds and dependencies.

```yaml
type: maturin  # For Python/Rust projects
```

##### Supported Project Types:

| Type | Description | Build Command | Dependency File |
|------|-------------|---------------|-----------------|
| `go` | Go projects | `make build` | `go.mod` |
| `maturin` | Python/Rust hybrid | `maturin build` | `pyproject.toml` |
| `node` | Node.js projects | `npm build` | `package.json` |
| `template` | Project templates | N/A | N/A |

#### `alias` (optional)
Short command alias for the tool. Usually defined in the registry instead of the config.

```yaml
alias: cx  # Users can run 'cx' instead of 'context'
```

#### `version` (optional)
Version information. Typically managed through Git tags rather than config.

```yaml
version: 0.2.1
```

### Project Type Examples

#### Go Project
```yaml
name: grove-context
description: Context management for LLMs
binary:
  name: context
  path: ./bin/context
type: go
```

#### Maturin (Python/Rust) Project
```yaml
name: grove-ml
description: Machine learning utilities
binary:
  name: groveml
  path: ./target/release/groveml
type: maturin
```

#### Node.js Project
```yaml
name: grove-web
description: Web interface for Grove
binary:
  name: grove-web
  path: ./dist/cli.js
type: node
```

#### Library Project (No Binary)
```yaml
name: grove-core
description: Shared Grove library
type: go
# No binary section - this is a library
```

## Advanced Configuration

### Multi-Binary Projects

For projects that produce multiple binaries:

```yaml
name: grove-multi-tool
description: Swiss army knife of Grove tools
binaries:  # Note: plural form (not yet implemented)
  - name: tool1
    path: ./bin/tool1
  - name: tool2
    path: ./bin/tool2
type: go
```

### Custom Build Configuration

Projects can extend their build configuration through Makefile targets:

```yaml
name: grove-custom
description: Tool with custom build process
binary:
  name: custom
  path: ./output/custom
type: go
# Build details in Makefile
```

The Makefile should implement standard targets:
- `build`: Build the binary
- `test`: Run tests
- `clean`: Clean build artifacts
- `dev`: Development build with debugging

### Environment-Specific Configuration

While not directly supported in `grove.yml`, you can use environment variables in your Makefiles:

```makefile
# In Makefile
ifdef GROVE_ENV
    BUILD_FLAGS += -tags=$(GROVE_ENV)
endif
```

## Registry Configuration

Grove uses a `registry.json` file to track available tools across the ecosystem:

```json
{
  "tools": [
    {
      "name": "grove-context",
      "alias": "cx",
      "repository": "github.com/mattsolo1/grove-context",
      "binary": "context",
      "version": "latest",
      "description": "Context management for LLMs"
    },
    {
      "name": "grove-flow",
      "alias": "flow",
      "repository": "github.com/mattsolo1/grove-flow",
      "binary": "flow",
      "version": "v0.1.0",
      "description": "Workflow automation"
    }
  ]
}
```

### Registry Fields

- `name`: Full tool name
- `alias`: Short command alias
- `repository`: Go module path
- `binary`: Binary name
- `version`: Version to install ("latest" or specific version)
- `description`: Tool description

## Configuration Best Practices

### 1. Naming Conventions

- Use lowercase with hyphens for names: `grove-my-tool`
- Keep names consistent across repository, directory, and config
- Use clear, descriptive names that indicate purpose

### 2. Workspace Patterns

- Start simple with `"*"` for small ecosystems
- Use subdirectories to organize by type: `tools/*`, `libs/*`
- Be specific to avoid accidentally including non-Grove directories
- Test patterns with `grove workspace list`

### 3. Binary Paths

- Always use relative paths from project root
- Follow platform conventions: `./bin/` for Unix-like systems
- Keep binary names simple and memorable
- Match binary name to project name when possible

### 4. Descriptions

- Keep descriptions concise (one line)
- Focus on what the tool does, not how
- Use active voice
- Include key features or use cases

### 5. Version Management

- Let Git tags drive versioning
- Don't hardcode versions in grove.yml
- Use semantic versioning consistently
- Document breaking changes

## Migration from Legacy Configuration

If you have an older Grove configuration:

### Old Format (Single Binary)
```yaml
# Old format
binary: context
```

### New Format (Structured)
```yaml
# New format
binary:
  name: context
  path: ./bin/context
```

### Migration Steps

1. Update all project `grove.yml` files to new format
2. Ensure binary paths are correct
3. Test with `grove list`
4. Commit changes across all projects

## Validation

Grove validates configuration files on load. Common validation errors:

### Missing Required Fields
```
Error: grove.yml missing required field 'name'
```
**Solution**: Add the missing field to your configuration.

### Invalid Workspace Pattern
```
Error: Invalid glob pattern in workspaces
```
**Solution**: Check glob syntax and escape special characters.

### Binary Path Not Found
```
Warning: Binary path './bin/tool' does not exist
```
**Solution**: Build the project or correct the path.

## Environment Variables

Grove respects several environment variables that affect configuration:

| Variable | Description | Default |
|----------|-------------|---------|
| `GROVE_HOME` | Grove installation directory | `~/.grove` |
| `GROVE_WORKSPACE` | Default workspace for operations | Current directory |
| `GROVE_DEBUG` | Enable debug output | `false` |
| `GROVE_USE_GH` | Always use GitHub CLI | `false` |

## Configuration File Locations

Grove looks for configuration files in these locations:

1. **Ecosystem Root**: First `grove.yml` with `workspaces` found when searching up from current directory
2. **Project Root**: `grove.yml` in each workspace directory
3. **User Config**: `~/.grove/config.yml` (future feature)
4. **System Config**: `/etc/grove/config.yml` (future feature)

## Dynamic Configuration

Some configuration can be modified at runtime:

### Development Overrides
```bash
# Override binary path for development
grove dev link tool /custom/path/to/binary
```

### Version Switching
```bash
# Switch to a different version
grove version set tool v0.2.0
```

### Workspace Selection
```bash
# Run command in specific workspace
grove run --workspace grove-core make test
```

## Configuration Examples

### Minimal Configuration
```yaml
name: simple-tool
description: A simple Grove tool
```

### Full-Featured Tool
```yaml
name: grove-advanced
description: Advanced Grove tool with all features
binary:
  name: advanced
  path: ./bin/advanced
type: go
alias: adv
version: 1.0.0
```

### Ecosystem with Mixed Project Types
Root `grove.yml`:
```yaml
name: polyglot-ecosystem
description: Multi-language Grove ecosystem
workspaces:
  - "go/*"
  - "python/*"
  - "node/*"
  - "rust/*"
```

## Troubleshooting Configuration

### Workspace Not Discovered

**Symptom**: `grove workspace list` doesn't show your project

**Solutions**:
1. Check workspace patterns in root `grove.yml`
2. Ensure project has its own `grove.yml`
3. Verify pattern matches directory structure
4. Run from within the ecosystem directory

### Binary Not Found After Installation

**Symptom**: Tool installs but isn't executable

**Solutions**:
1. Verify `binary.path` in `grove.yml`
2. Check build output location
3. Ensure binary has execute permissions
4. Confirm `~/.grove/bin` is in PATH

### Configuration Changes Not Applied

**Symptom**: Changes to `grove.yml` aren't reflected

**Solutions**:
1. No caching - changes should be immediate
2. Check for syntax errors in YAML
3. Ensure you're editing the right file
4. Try `grove list` to force re-read

## Future Configuration Features

Planned enhancements to Grove configuration:

- **User-level configuration**: Personal preferences and defaults
- **Configuration inheritance**: Base configurations with overrides
- **Configuration validation**: Schema-based validation
- **Dynamic reloading**: Watch for configuration changes
- **Configuration templates**: Shareable configuration presets
- **Environment-specific overrides**: Development vs. production settings

## Summary

Grove's configuration system is designed to be:
- **Simple**: Minimal required configuration
- **Flexible**: Supports various project types and structures
- **Discoverable**: Automatic workspace discovery
- **Extensible**: Room for growth and customization

Start with minimal configuration and add complexity only as needed. The defaults are sensible for most Go projects, and the system is designed to work with minimal setup.