# Grove SDK Manager API

The SDK Manager provides programmatic access to Grove tool installation and version management.

## Overview

The SDK Manager (`grove-meta/pkg/sdk/manager.go`) handles:
- Tool discovery and installation
- Version management
- Binary downloads from GitHub releases
- Symlink management for active versions

## API Reference

### Creating a Manager

```go
import "github.com/mattsolo1/grove-meta/pkg/sdk"

// Create a new SDK manager
manager, err := sdk.NewManager()
if err != nil {
    return err
}
```

### Configuration

```go
// Use GitHub CLI for authentication (supports private repos)
manager.SetUseGH(true)

// Ensure directory structure exists
err := manager.EnsureDirs()
```

### Tool Installation

```go
// Install latest version of a tool
err := manager.InstallTool("cx", "latest")

// Install specific version
err := manager.InstallTool("flow", "v0.2.0")

// Get latest version tag for a tool
version, err := manager.GetLatestVersionTag("nb")
```

### Version Management

```go
// List installed versions
versions, err := manager.ListVersions()

// Get active version
activeVersion, err := manager.GetActiveVersion()

// Switch to a different version
err := manager.UseVersion("v0.2.0")

// List installed tools for a version
tools, err := manager.ListTools("v0.2.0")
```

### Tool Discovery

```go
// Get all available tools
tools := sdk.GetAllTools()
// Returns: ["canopy", "cx", "flow", "grove", "nb", "neogrove", "px", "tend"]

// Get repository for a tool
repo := sdk.GetToolRepo("cx")
// Returns: "grove-context"
```

## Directory Structure

The SDK Manager uses the following directory structure:

```
~/.grove/
├── bin/                    # Active tool symlinks
│   ├── cx -> ../versions/v0.2.0/cx
│   ├── flow -> ../versions/v0.2.0/flow
│   └── ...
├── versions/              # Installed versions
│   ├── v0.1.0/
│   │   ├── cx
│   │   ├── flow
│   │   └── ...
│   └── v0.2.0/
│       ├── cx
│       ├── flow
│       └── ...
└── .active_version        # Currently active version
```

## Implementation Details

### Tool Repository Mapping

Tools are mapped to their repositories:

```go
var toolToRepo = map[string]string{
    "grove":  "grove-meta",
    "cx":     "grove-context",
    "flow":   "grove-flow",
    "nb":     "grove-notebook",
    "gvm":    "grove-version",
    "px":     "grove-proxy",
    "sb":     "grove-sandbox",
    "tend":   "grove-tend",
    "canopy": "grove-canopy",
}
```

### Download Methods

The SDK Manager supports two download methods:

1. **Direct download** (default):
   ```go
   manager.SetUseGH(false)
   // Uses curl to download from public GitHub releases
   ```

2. **GitHub CLI** (for private repos):
   ```go
   manager.SetUseGH(true)
   // Uses `gh release download` with authentication
   ```

### Platform Detection

The SDK automatically detects the platform:
- OS: darwin, linux
- Architecture: amd64, arm64

Binary names follow the pattern: `{tool}-{os}-{arch}`

## Error Handling

Common errors and their meanings:

```go
// Tool not found in mapping
err := manager.InstallTool("unknown", "latest")
// Error: tool 'unknown' not recognized

// No releases found
version, err := manager.GetLatestVersionTag("cx")
// Error: no releases found for grove-context

// Download failed
err := manager.InstallTool("cx", "v999.0.0")
// Error: failed to download

// No active version
active, err := manager.GetActiveVersion()
// Returns: "", nil (no error, empty string)
```

## Example: Custom Installation Flow

```go
package main

import (
    "fmt"
    "log"
    "github.com/mattsolo1/grove-meta/pkg/sdk"
)

func installGroveTools() error {
    // Create manager
    manager, err := sdk.NewManager()
    if err != nil {
        return err
    }

    // Use GitHub CLI for private repos
    manager.SetUseGH(true)

    // Ensure directories exist
    if err := manager.EnsureDirs(); err != nil {
        return err
    }

    // Get all available tools
    tools := sdk.GetAllTools()

    // Install each tool
    for _, tool := range tools {
        // Get latest version
        version, err := manager.GetLatestVersionTag(tool)
        if err != nil {
            log.Printf("Warning: %s: %v", tool, err)
            continue
        }

        // Install the tool
        fmt.Printf("Installing %s %s...\n", tool, version)
        if err := manager.InstallTool(tool, version); err != nil {
            log.Printf("Failed to install %s: %v", tool, err)
            continue
        }
    }

    // Set active version if none exists
    if active, _ := manager.GetActiveVersion(); active == "" {
        versions, _ := manager.ListVersions()
        if len(versions) > 0 {
            manager.UseVersion(versions[0])
        }
    }

    return nil
}
```

## Future Enhancements

Planned improvements to the SDK Manager:

1. **Version constraints**: Install compatible versions based on requirements
2. **Dependency resolution**: Automatically install required tools
3. **Update notifications**: Check for available updates
4. **Rollback support**: Revert to previous versions
5. **Custom registries**: Support for private tool registries
6. **Parallel downloads**: Speed up multi-tool installation