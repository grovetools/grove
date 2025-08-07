# Grove

Grove is the meta-CLI and package manager for the Grove ecosystem. It provides a unified entry point for installing and managing Grove tools, as well as orchestrating operations across the entire ecosystem.

## Installation

```bash
go install github.com/yourorg/grove@latest
```

## Usage

### Installing Tools

Install one or more Grove tools:
```bash
grove install context          # Install by name
grove install cx               # Install by alias
grove install cx gvm agent     # Install multiple tools
```

### Listing Available Tools

See all available Grove tools:
```bash
grove list
```

### Running Tools

Once installed, tools can be run in three ways:

1. Direct execution:
   ```bash
   cx update
   ```

2. Via Grove using alias:
   ```bash
   grove cx update
   ```

3. Via Grove using full name:
   ```bash
   grove context update
   ```

### Updating Tools

Update tools to their latest version:
```bash
grove update context
grove update cx gvm
```

### Managing Dependencies

Update Go module dependencies across all Grove submodules:
```bash
grove deps sync                                                # Update all Grove deps to latest
grove deps sync --commit                                       # Update and commit changes
grove deps bump github.com/mattsolo1/grove-core@latest        # Update specific dependency
grove deps bump github.com/mattsolo1/grove-core@v0.2.1        # Update to specific version
```

See [Dependency Management](docs/dependency-management.md) for detailed documentation.

## Tool Registry

Grove uses a `registry.json` file to track available tools. Each tool entry includes:
- Name: Full descriptive name
- Alias: Short command for daily use
- Repository: Go module path
- Binary: Executable name
- Version: Version tag or "latest"
- Description: Tool purpose

## Architecture

Grove follows a distributed architecture where each tool is a separate repository and binary. The Grove meta-CLI acts as:
1. A package manager for installing tools via `go install`
2. A command delegator that forwards commands to installed tools
3. A discovery mechanism for available tools