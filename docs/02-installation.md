# Grove Installation Guide

This guide walks you through installing the Grove CLI and setting up your environment to use the Grove ecosystem of tools.

## Prerequisites

- A Unix-like operating system (macOS or Linux)
- `curl` or `wget` for downloading
- (Optional) GitHub CLI (`gh`) for private repository access

## Supported Platforms

Grove supports the following platforms:
- **macOS**: Intel (amd64) and Apple Silicon (arm64)
- **Linux**: x86_64 (amd64) and ARM64

## Primary Installation Method

The recommended way to install Grove is using the installation script:

```bash
curl -sSfL https://raw.githubusercontent.com/mattsolo1/grove-meta/main/scripts/install.sh | sh
```

This single command will:
1. Detect your operating system and architecture
2. Download the appropriate Grove binary for your platform
3. Install it to `~/.grove/bin/grove`
4. Create the necessary directory structure

## Smart Installation Features

The Grove installer is intelligent and adapts to your environment:

### Public Repository Access
By default, the installer uses `curl` to download Grove from GitHub's public releases. This works without any authentication and is suitable for public repositories.

### Private Repository Support
If you're working with private Grove repositories, the installer automatically detects and uses the GitHub CLI (`gh`) when:
1. The `gh` command is installed on your system
2. You're authenticated with GitHub (`gh auth status` succeeds)

When these conditions are met, the installer will:
- Use `gh release download` instead of `curl`
- Access private repositories seamlessly
- Display a message confirming it's using the `gh` CLI

To explicitly use the GitHub CLI after installation:
```bash
grove install all --use-gh
grove update --use-gh
```

## Post-Installation Setup

### Step 1: Update Your PATH

The most important post-installation step is adding the Grove binary directory to your PATH. This allows you to run `grove` and all installed tools from any directory.

#### For Zsh (default on modern macOS):
```bash
echo 'export PATH="$HOME/.grove/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

#### For Bash:
```bash
echo 'export PATH="$HOME/.grove/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

#### For Fish:
```fish
set -U fish_user_paths $HOME/.grove/bin $fish_user_paths
```

### Step 2: Verify Installation

After updating your PATH, verify Grove is installed correctly:

```bash
grove version
```

You should see output showing the Grove version and build information.

### Step 3: Install Grove Tools

Once Grove is installed and in your PATH, you can install the ecosystem tools:

```bash
# Install all available tools (public repositories)
grove install all

# Install all tools from private repositories
grove install all --use-gh

# Install specific tools
grove install context flow notebook
```

## Understanding the Grove Directory Structure

After installation, Grove creates the following directory structure in your home directory:

```
~/.grove/
├── bin/                    # Active tool binaries and symlinks
│   ├── grove              # The Grove CLI itself
│   ├── cx -> context      # Alias symlinks
│   ├── context            # Installed tool binaries
│   └── ...
├── versions/              # Version-specific installations
│   ├── context/
│   │   ├── v0.2.0/       # Specific version installations
│   │   └── v0.2.1/
│   └── flow/
│       └── v0.1.0/
├── active_versions.json   # Tracks active version for each tool
└── devlinks.json         # Tracks local development links
```

### Directory Purposes

- **`bin/`**: Contains the active binaries for all tools. This is the directory you add to your PATH.
- **`versions/`**: Stores multiple versions of each tool, allowing you to switch between them.
- **`active_versions.json`**: Records which version of each tool is currently active.
- **`devlinks.json`**: Used by `grove dev` commands to track local development builds.

## Alternative Installation Methods

### Manual Installation

If you prefer not to use the installation script, you can manually install Grove:

1. Download the appropriate binary for your platform from the [releases page](https://github.com/mattsolo1/grove-meta/releases)
2. Rename it to `grove`
3. Move it to a directory in your PATH
4. Make it executable: `chmod +x grove`

### Building from Source

If you have Go installed, you can build Grove from source:

```bash
git clone https://github.com/mattsolo1/grove-meta.git
cd grove-meta
make build
cp ./bin/grove ~/.grove/bin/
```

## Troubleshooting

### "grove: command not found"

This error means Grove is not in your PATH. Ensure you've:
1. Added `~/.grove/bin` to your PATH (see Step 1 above)
2. Reloaded your shell configuration (`source ~/.zshrc` or similar)
3. Opened a new terminal window

### Permission Denied Errors

If you get permission errors during installation:
```bash
chmod +x ~/.grove/bin/grove
```

### Private Repository Access Issues

If you're having trouble accessing private repositories:

1. Ensure the GitHub CLI is installed:
   ```bash
   brew install gh  # macOS
   # or see https://cli.github.com for other platforms
   ```

2. Authenticate with GitHub:
   ```bash
   gh auth login
   ```

3. Verify authentication:
   ```bash
   gh auth status
   ```

4. Use the `--use-gh` flag:
   ```bash
   grove install all --use-gh
   ```

### Network Issues

If you're behind a corporate firewall or proxy:
- The installer respects standard HTTP proxy environment variables (`HTTP_PROXY`, `HTTPS_PROXY`)
- You may need to configure `git` and `gh` to use your proxy settings

## Next Steps

Now that Grove is installed and configured, you're ready to start using it! Continue to the [Getting Started Guide](./03-getting-started.md) to learn how to:
- List available tools
- Install individual tools
- Run Grove commands
- Update tools to newer versions