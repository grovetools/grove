# Grove Meta Documentation

Welcome to the Grove Meta documentation. This directory contains detailed guides for managing and operating the Grove ecosystem.

## Table of Contents

### Core Documentation

- [Release Process](release-process.md) - How to release individual tools and create meta-releases
- [Dependency Management](dependency-management.md) - Managing Go module dependencies across the ecosystem
- [SDK Manager API](sdk-manager-api.md) - Programmatic API for tool installation and version management

### Templates and Guides

- [Release Workflow Template](release-workflow-template.md) - GitHub Actions template for adding release automation to new tools

## Quick Links

### Common Tasks

**Releasing a Tool:**
```bash
cd grove-context
git tag v0.2.0
git push origin v0.2.0
```

**Updating Dependencies:**
```bash
grove deps bump github.com/mattsolo1/grove-core@latest --commit
```

**Creating a Meta-Release:**
```bash
grove release create-meta-release v0.3.0
```

**Installing Tools:**
```bash
grove install cx flow nb
```

### Architecture Overview

Grove Meta serves as the orchestration layer for the Grove ecosystem:

1. **Tool Management** - Install, update, and manage Grove tools
2. **Dependency Management** - Update dependencies across all repositories
3. **Release Coordination** - Create meta-releases that track compatible versions
4. **Workspace Operations** - Run commands across multiple repositories

### Getting Help

- Check the specific documentation files above for detailed information
- Use `grove --help` for CLI command documentation
- Use `grove <command> --help` for command-specific help

### Contributing

When adding new features to Grove Meta:

1. Update the relevant documentation in this directory
2. Add examples to the README
3. Update the release documentation if the release process changes
4. Consider adding API documentation for new programmatic interfaces