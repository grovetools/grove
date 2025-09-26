# Grove Installation Guide

This guide provides comprehensive instructions for installing the Grove command-line interface (CLI) and configuring your system to use the full suite of Grove tools.

## Prerequisites

Before installing Grove, ensure your system meets the following requirements:

- **Operating System**: A Unix-like environment such as macOS or Linux.
- **Go Toolchain**: Required only if you plan to build from source. Go version 1.21 or later is recommended.
- **Git**: Required for building from source and for certain commands that interact with repositories.
- **`curl`**: Used by the primary installation script to download binaries.
- **GitHub CLI (`gh`)**: Optional but recommended for seamless access to private Grove repositories. The installer will use it automatically if it's installed and authenticated.

## Installation Methods

There are several ways to install the `grove` CLI. The recommended method for most users is the installation script.

### Method 1: Installation Script (Recommended)

The quickest way to install Grove is with a single command in your terminal. The script automatically detects your operating system and architecture, downloads the correct binary, and places it in the appropriate directory.

```bash
curl -sSfL https://raw.githubusercontent.com/mattsolo1/grove-meta/main/scripts/install.sh | sh
```

This command performs the following actions:
1.  Creates the `~/.grove` directory structure.
2.  Downloads the latest `grove` binary to `~/.grove/bin/grove`.
3.  Ensures the binary is executable.

The installer intelligently handles repository access:
-   **Public Repositories**: By default, it uses `curl` to download from public GitHub releases.
-   **Private Repositories**: If it detects that the GitHub CLI (`gh`) is installed and authenticated (`gh auth status`), it will automatically use `gh` to download releases, providing seamless access to private repositories.

### Method 2: Building from Source

If you have the Go toolchain installed, you can build and install Grove directly from the source code.

1.  **Clone the repository:**
    ```bash
    git clone https://github.com/mattsolo1/grove-meta.git
    cd grove-meta
    ```

2.  **Build the binary:**
    The project `Makefile` provides a convenient way to build the application. The resulting binary will be placed in the `./bin` directory.
    ```bash
    make build
    ```

3.  **Install the binary:**
    After building, copy the binary to the Grove installation directory.
    ```bash
    # Create the target directory if it doesn't exist
    mkdir -p ~/.grove/bin

    # Copy the binary
    cp ./bin/grove ~/.grove/bin/
    ```

### Method 3: Manual Installation

You can also install Grove by manually downloading a pre-built binary from the project's [GitHub Releases page](https://github.com/mattsolo1/grove-meta/releases).

1.  Navigate to the releases page and find the appropriate asset for your operating system and architecture (e.g., `grove-darwin-arm64`).
2.  Download the binary.
3.  Rename the downloaded file to `grove`.
4.  Move the binary to the installation directory:
    ```bash
    # Create the directory if it doesn't exist
    mkdir -p ~/.grove/bin

    # Move and rename the binary
    mv /path/to/downloaded/file ~/.grove/bin/grove
    ```
5.  Make the binary executable:
    ```bash
    chmod +x ~/.grove/bin/grove
    ```

## Post-Installation Configuration

After installing the `grove` binary, you must add its location to your shell's `PATH` environment variable. This allows you to run `grove` and other installed tools from any directory.

1.  **Add Grove to your PATH:**
    Execute the command appropriate for your shell:

    *   **For Zsh (`~/.zshrc`):**
        ```bash
        echo 'export PATH="$HOME/.grove/bin:$PATH"' >> ~/.zshrc
        ```

    *   **For Bash (`~/.bashrc` or `~/.bash_profile`):**
        ```bash
        echo 'export PATH="$HOME/.grove/bin:$PATH"' >> ~/.bashrc
        ```

    *   **For Fish (`~/.config/fish/config.fish`):**
        ```fish
        set -U fish_user_paths $HOME/.grove/bin $fish_user_paths
        ```

2.  **Apply the changes:**
    For the changes to take effect, either restart your terminal or source your shell's configuration file:
    ```bash
    # For Zsh
    source ~/.zshrc

    # For Bash
    source ~/.bashrc
    ```

3.  **Verify the installation:**
    Run the `version` command to confirm that `grove` is installed and accessible.
    ```bash
    grove version
    ```
    This should display the version, commit, and build date of the `grove` CLI.

4.  **Install Grove tools:**
    With the meta-CLI installed, you can now install the rest of the tools in the Grove ecosystem.
    ```bash
    # Install all available tools
    grove install all

    # If you need to access private repositories, use the --use-gh flag
    grove install all --use-gh
    ```

## Upgrading Grove

You can update the `grove` CLI and all installed tools to their latest versions.

-   **Update the `grove` CLI itself:**
    ```bash
    grove self-update
    ```

-   **Update all installed tools:**
    ```bash
    grove update all
    ```

## Troubleshooting

### `grove: command not found`
This error indicates that the `~/.grove/bin` directory is not in your shell's `PATH`.
-   Ensure you have completed the "Post-Installation Configuration" steps correctly for your shell.
-   Verify the `export PATH` line was added to your shell's configuration file (e.g., `~/.zshrc`).
-   Try opening a new terminal window to ensure the configuration has been loaded.

### Permission Denied
If you encounter a "permission denied" error when trying to run `grove`, the binary may not be executable.
-   Run `chmod +x ~/.grove/bin/grove` to grant execute permissions.

### Private Repository Access Issues
If `grove install` or `grove update` fails with errors related to private repositories:
1.  **Install the GitHub CLI**: Follow the official instructions at [cli.github.com](https://cli.github.com).
2.  **Authenticate with GitHub**: Run `gh auth login` and follow the prompts.
3.  **Verify Authentication**: Run `gh auth status`. It should show you as logged in.
4.  **Use the `--use-gh` flag**: Rerun your command with the `--use-gh` flag (e.g., `grove install all --use-gh`).