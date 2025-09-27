# Grove Ecosystem Installation Guide

This guide provides comprehensive instructions for installing the Grove command-line interface (CLI) and the entire suite of Grove tools.

## 1. Quick Install: The `grove` Meta-CLI (Recommended)

The recommended method is to use our installation script, which automatically detects your operating system and architecture.

### Step 1: Run the Installer

Execute the following command in your terminal:

```bash
curl -sSfL https://raw.githubusercontent.com/mattsolo1/grove-meta/main/scripts/install.sh | sh
```

This command downloads and runs a script that:
1.  Creates the `~/.grove` directory structure for managing binaries and versions.
2.  Downloads the latest `grove` meta-CLI binary to `~/.grove/bin/grove`.
3.  Makes the binary executable.

**Note on Private Repositories**: The installer will automatically use the GitHub CLI (`gh`) if it is installed and you are authenticated. This allows it to download tools from private repositories seamlessly.

### Step 2: Configure Your PATH

For the `grove` command and its tools to be accessible from anywhere, you must add the Grove bin directory to your shell's `PATH`.

1.  **Add this line** to your shell's configuration file (e.g., `~/.zshrc`, `~/.bashrc`, or `~/.profile`):
    ```bash
    export PATH="$HOME/.grove/bin:$PATH"
    ```

2.  **Apply the changes** by restarting your terminal or sourcing the configuration file:
    ```bash
    # For Zsh
    source ~/.zshrc

    # For Bash
    source ~/.bashrc
    ```

### Step 3: Verify the Installation

Run the `version` command to confirm that `grove` is installed correctly.

```bash
grove version
```

## 2. Installing Individual Grove Tools

With the `grove` meta-CLI installed, you can now easily install any tool from the ecosystem.

### Install a Specific Tool

Use `grove install` followed by the tool's name or alias. You can find all available tools with `grove list`.

```bash
# Install the context tool by its alias 'cx'
grove install cx

# Install the notebook tool by its full name
grove install grove-notebook
```

### Install All Tools

To install all available tools at once, use the `all` keyword:

```bash
grove install all
```

### Installing Specific Versions or Nightly Builds

You can specify a version tag or request a nightly build compiled from the `main` branch.

```bash
# Install a specific version of grove-flow
grove install flow@v0.2.5

# Install the latest development build of grove-context
grove install cx@nightly
```

## 3. Building from Source (For Contributors)

For developers contributing to the ecosystem, the recommended approach is to clone the monorepo and build tools locally. The `grove` meta-CLI is designed to automatically detect and use these local builds when you are working inside a project's directory.

1.  **Clone the Ecosystem Monorepo**:
    If you have access, clone the `grove-ecosystem` monorepo which contains all the tools.

2.  **Build a Tool**:
    Navigate to a tool's directory and use the `Makefile`.
    ```bash
    cd /path/to/ecosystem/grove-context
    make build
    ```
    This creates a binary at `./bin/cx`.

3.  **Automatic Usage**:
    The `grove` meta-CLI is workspace-aware. When you run a command from within the `grove-context` directory, `grove` will automatically use your local `./bin/cx` binary instead of the globally installed one. This allows for seamless development and testing without any extra configuration.

## 4. Manual Installation (Advanced)

For special cases, you can manually download a binary from a tool's **GitHub Releases** page.

1.  Download the appropriate binary for your OS and architecture.
2.  Make it executable: `chmod +x <binary-name>`.
3.  Move it into the Grove bin directory: `mv <binary-name> ~/.grove/bin/<tool-alias>`.

**Note**: This method bypasses Grove's version management. The recommended way to switch between versions is using `grove version use <tool@version>`.