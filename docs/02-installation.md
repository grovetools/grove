# Installation Guide

This guide provides instructions for installing the `grove` command-line interface and the tools in its ecosystem.

## Prerequisites

Before installing, ensure the following requirements are met:

*   **Operating System**: macOS or Linux.
*   **Architecture**: `amd64` (Intel) or `arm64` (Apple Silicon, ARM).
*   **Dependencies**:
    *   `git`: Required for version control and managing workspaces.
    *   `curl`: Used by the installation script to download binaries. The script uses `curl` only — there is no `gh` fallback.
    *   `sha256sum` or `shasum`: Optional. Used to verify the downloaded binary against the release's `checksums.txt`. If neither is present, the script warns and continues.
    *   `gh` (GitHub CLI): Optional. Used by `grove install --use-gh` for private repositories, not by the install script.

## Installation Script

The primary installation method is a script that detects the operating system and architecture.

Run the following command in a terminal:

```bash
curl -sSfL https://raw.githubusercontent.com/grovetools/grove/main/scripts/install.sh | sh
```

The script performs the following steps:
1.  Detects the operating system (macOS or Linux) and architecture (amd64 or arm64).
2.  Fetches the latest release from the `grovetools/grove` GitHub repository with `curl`.
3.  Downloads the appropriate binary for the system, plus the release's `checksums.txt`.
4.  Verifies the binary's SHA-256 against `checksums.txt`. A mismatch aborts the install; a missing `checksums.txt` (older releases) or a machine with no `sha256sum`/`shasum` warns and continues.
5.  Installs the binary to `~/.local/share/grove/bin/grove` and makes it executable.
6.  Symlinks `~/.local/bin/grove` at that binary. An existing symlink there is replaced; a regular file is never clobbered — the script warns and leaves it alone.
7.  Runs `grove onboard`.

## Post-Installation Setup

`grove` is the only name the install puts in your global namespace. The toolchain directory (`~/.local/share/grove/bin`) is **not** meant to be on your `PATH`: the other tools are reached through grove.

```bash
grove mux            # open the treemux cockpit
grove nb list        # run nb
grove cx stats       # run cx
```

If you want a tool under its bare name, opt in one at a time:

```bash
grove expose cx      # links ~/.local/bin/cx -> the grove binary
grove hide cx        # undo
```

#### 1. Configure PATH

Most systems already have `~/.local/bin` on `PATH`, and the installer says nothing when that is the case. If it reports that the directory is missing, add it to your shell configuration file (e.g., `~/.zshrc`, `~/.bashrc`, or `~/.profile`):

```bash
export PATH="$HOME/.local/bin:$PATH"   # bash/zsh
fish_add_path ~/.local/bin             # fish
```

`grove onboard` offers to make this edit for you.

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