# Installation Guide

This guide provides instructions for installing the `grove` command-line interface and the tools in its ecosystem.

## Prerequisites

Before installing, ensure the following requirements are met:

*   **Operating System**: macOS or Linux.
*   **Architecture**: `amd64` (Intel) or `arm64` (Apple Silicon, ARM).
*   **Dependencies**:
    *   `git`: Required for version control and managing workspaces.
    *   `curl`: Used by the installation script to download binaries.
    *   `gh` (GitHub CLI): Used for private repositories. The installer will use it automatically if it is installed and authenticated.

## Installation Script

The primary installation method is a script that detects the operating system and architecture.

Run the following command in a terminal:

```bash
curl -sSfL https://raw.githubusercontent.com/grovetools/grove/main/scripts/install.sh | sh
```

The script performs the following steps:
1.  Detects the operating system (macOS or Linux) and architecture (amd64 or arm64).
2.  Checks for an authenticated GitHub CLI (`gh`). If found, it uses `gh` to download assets. Otherwise, it falls back to `curl`.
3.  Fetches the latest release from the `grovetools/grove` GitHub repository.
4.  Downloads the appropriate binary for the system.
5.  Installs the binary to `~/.local/share/grove/bin/grove` and makes it executable.

## Post-Installation Setup

After installation, add the Grove bin directory to the shell's `PATH` environment variable.

#### 1. Configure PATH

Add the following line to the shell's configuration file (e.g., `~/.zshrc`, `~/.bashrc`, or `~/.profile`):

```bash
export PATH="${XDG_DATA_HOME:-$HOME/.local/share}/grove/bin:$PATH"
```

#### 2. Apply Changes

For the changes to take effect, either restart the terminal or source the configuration file:

```bash
# For Zsh
source ~/.zshrc

# For Bash
source ~/.bashrc
```

#### 3. Verify Installation

Run the `version` command to confirm that the `grove` CLI is installed and accessible:

```bash
grove version
```

This should display the version, commit, and build date of the `grove` binary.

## Installing Grove Tools

The `grove` CLI is used to install other tools from the ecosystem.

#### Install All Tools

To install the latest stable versions of all available tools, run:

```bash
grove install all
```

For private repositories, use the `--use-gh` flag to authenticate with the GitHub CLI:

```bash
grove install all --use-gh
```

#### Install Specific Tools

Tools can be installed by name or alias.

```bash
# Install a single tool by its alias
grove install cx

# Install multiple tools
grove install flow nb
```

#### Install a Specific Version

To install a specific version of a tool, use the `@version` syntax.

```bash
grove install cx@v0.2.1
```

#### Install Nightly Builds

Development builds from the `main` branch can be installed using the `@nightly` tag.

```bash
# Install the nightly build of a single tool
grove install cx@nightly

# Install nightly builds of all tools
grove install all@nightly
```