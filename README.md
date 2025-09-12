# Grove

<img src="https://github.com/user-attachments/assets/0304dd2b-7d67-42c8-9e0e-7a7c131f97ca" width="50%" />

This repository holds the meta-CLI and package manager for the Grove ecosystem. It provides a unified entry point for installing and managing Grove tools, as well as orchestrating operations across the entire ecosystem.

## Installation

The `grove` CLI is the entry point to the ecosystem. Install it with a single command:

```bash
curl -sSfL https://raw.githubusercontent.com/mattsolo1/grove-meta/main/scripts/install.sh | sh
```

The installer is smart:
- If the repository is public, it uses `curl`.
- If the repository is private, it will automatically use the `gh` CLI if you are logged in (`gh auth status`).

### Post-Installation

1. **Update your PATH:** The installer will place the `grove` binary in `~/.grove/bin`. Make sure to add this directory to your shell's `PATH`.

   ```bash
   # Add this to your ~/.zshrc, ~/.bashrc, or equivalent
   export PATH="$HOME/.grove/bin:$PATH"
   ```

2. **Install Grove Tools:** Once the `grove` CLI is installed and in your `PATH`, you can install all the other tools:

   ```bash
   # For public repositories
   grove install all

   # If repositories are private, use the --use-gh flag
   grove install all --use-gh
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
grove update                   # Update grove itself
grove self-update              # Alternative way to update grove
grove update context           # Update specific tool
grove update cx flow           # Update multiple tools
grove update all               # Update all installed tools
```

For private repositories, use the `--use-gh` flag:
```bash
grove update --use-gh
grove self-update --use-gh
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
