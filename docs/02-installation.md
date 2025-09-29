# Installation Guide

This guide provides instructions for installing Grove CLI (`grove`) and the associated tools in the ecosystem.

## Prerequisites

Before installing, ensure your system meets the following requirements:

*   **Operating System**: macOS or Linux.
*   **Architecture**: `amd64` (Intel) or `arm64` (Apple Silicon, ARM).
*   **Dependencies**:
    *   `git`: Required for version control and managing workspaces.
    *   `curl`: Used by the installation script to download the binary.
    *   `gh` (GitHub CLI): Recommended, especially for private repositories. The installer will use it automatically if it is installed and authenticated.

## Quick Install (Recommended)

The recommended way to install the `grove` CLI is with the installation script, which automatically detects your operating system and architecture.

Run the following command in your terminal:

```bash
curl -sSfL https://raw.githubusercontent.com/mattsolo1/grove-meta/main/scripts/install.sh | sh
```

The installer performs the following steps:
1.  Detects your operating system (macOS or Linux) and architecture (amd64 or arm64).
2.  Checks if the GitHub CLI (`gh`) is installed and authenticated. If so, it uses `gh` to download assets, which supports private repositories. Otherwise, it falls back to `curl`.
3.  Fetches the latest release from the `mattsolo1/grove-meta` GitHub repository.
4.  Downloads the appropriate binary for your system.
5.  Installs the binary to `~/.grove/bin/grove` and makes it executable.

## Post-Installation Setup

After the installation script finishes, you need to add the Grove bin directory to your shell's `PATH` environment variable.

#### 1. Configure PATH

Add the following line to your shell's configuration file (e.g., `~/.zshrc`, `~/.bashrc`, or `~/.profile`):

```bash
export PATH="$HOME/.grove/bin:$PATH"
```

#### 2. Apply Changes

For the changes to take effect, either restart your terminal session or source your configuration file:

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

You should see output displaying the version, commit, and build date of the `grove` binary.

## Installing Grove Tools

Once the CLI is installed, you can use it to install other tools from the Grove ecosystem.

#### Install All Tools

To install the latest stable versions of all available Grove tools, run:

```bash
grove install all
```

If you are working with private Grove repositories, use the `--use-gh` flag to authenticate with the GitHub CLI:

```bash
grove install all --use-gh
```

#### Install Specific Tools

You can install one or more tools by name or alias.

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

For the latest development builds, you can install from the `main` branch using the `@nightly` tag.

```bash
# Install the nightly build of a single tool
grove install cx@nightly

# Install nightly builds of all tools
grove install all@nightly
```
