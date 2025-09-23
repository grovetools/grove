# Getting Started with Grove

This guide walks you through your first interactions with Grove, from listing available tools to installing and using them effectively.

## Prerequisites

Before starting this guide, ensure:
- Grove is installed (see [Installation Guide](./02-installation.md))
- `~/.grove/bin` is in your PATH
- You can run `grove version` successfully

## Step 1: List Available Tools

Your first step is discovering what tools are available in the Grove ecosystem. Run:

```bash
grove list
```

This command displays a comprehensive table showing:
- **Tool names** and their short aliases
- **Installation status** (installed, not installed, or development version)
- **Currently active version** if installed
- **Latest available release** from GitHub
- **Repository information**

Example output:
```
┌────────────────┬──────────┬───────────────┬────────────────┬─────────────────────────────────┐
│ TOOL           │ ALIAS    │ STATUS        │ VERSION        │ REPOSITORY                       │
├────────────────┼──────────┼───────────────┼────────────────┼─────────────────────────────────┤
│ grove-context  │ cx       │ ✓ installed   │ v0.2.1         │ mattsolo1/grove-context         │
│ grove-flow     │ flow     │ not installed │ -              │ mattsolo1/grove-flow            │
│ grove-notebook │ nb       │ ✓ dev         │ dev -> v0.3.0  │ mattsolo1/grove-notebook        │
└────────────────┴──────────┴───────────────┴────────────────┴─────────────────────────────────┘
```

The status column tells you:
- **✓ installed**: Tool is installed from a release
- **✓ dev**: Using a local development version
- **not installed**: Tool is available but not yet installed

## Step 2: Install a Single Tool

Let's install the context management tool (`grove-context`). You can use either its full name or alias:

```bash
grove install context
# or
grove install cx
```

Grove will:
1. Detect your platform (OS and architecture)
2. Download the appropriate binary from GitHub releases
3. Install it to `~/.grove/bin/`
4. Create alias symlinks (e.g., `cx` → `context`)
5. Track the installed version

You'll see output like:
```
Installing grove-context...
✓ Downloaded grove-context v0.2.1
✓ Installed to ~/.grove/bin/context
✓ Created alias: cx -> context
```

## Step 3: Install Multiple Tools

You can install multiple tools in a single command:

```bash
grove install context flow notebook
# or using aliases
grove install cx flow nb
```

## Step 4: Install All Tools

To quickly set up your entire Grove environment, install all available tools at once:

```bash
grove install all
```

This command:
- Installs every tool listed in the Grove registry
- Skips tools that are already installed at their latest version
- Creates all necessary symlinks and aliases
- Takes just a minute or two for the entire ecosystem

For private repositories, add the `--use-gh` flag:
```bash
grove install all --use-gh
```

## Step 5: Running Grove Tools

Once installed, Grove tools can be executed in three different ways:

### Method 1: Direct Execution
Run the tool directly by its binary name or alias:
```bash
cx --help
context --help
```

### Method 2: Via Grove Alias
Use Grove as a dispatcher with the tool's alias:
```bash
grove cx --help
```

### Method 3: Via Grove Full Name
Use Grove with the tool's full name:
```bash
grove context --help
```

All three methods execute the same binary. The Grove dispatcher approach (methods 2 and 3) is useful when:
- You want to ensure you're running a Grove-managed tool
- You're scripting and want explicit tool invocation
- You need to disambiguate from system commands

## Step 6: Check Tool Versions

View the version of an installed tool:

```bash
grove version context
# or
cx --version
```

To see versions of all installed tools:
```bash
grove list
```

## Step 7: Update Tools

Grove makes it easy to keep your tools up to date.

### Update a Specific Tool
```bash
grove update context
# or
grove update cx
```

### Update Multiple Tools
```bash
grove update context flow notebook
```

### Update All Installed Tools
```bash
grove update all
```

### Update Grove Itself
```bash
grove update
# or
grove self-update
```

The update command:
- Checks for the latest release on GitHub
- Downloads and installs the new version
- Preserves your configuration and settings
- Maintains backward compatibility

## Working with Private Repositories

If your organization uses private Grove tool repositories, you'll need to use the GitHub CLI for authentication:

1. **Ensure `gh` is installed and authenticated:**
   ```bash
   gh auth status
   ```

2. **Use the `--use-gh` flag for operations:**
   ```bash
   grove install all --use-gh
   grove update all --use-gh
   ```

3. **Grove auto-detects `gh` authentication:**
   If `gh` is authenticated, Grove will automatically use it for better access to releases, even without the flag.

## Common Workflows

### Setting Up a New Development Machine
```bash
# 1. Install Grove (one-time setup)
curl -sSfL https://raw.githubusercontent.com/mattsolo1/grove-meta/main/scripts/install.sh | sh

# 2. Add to PATH and reload shell
echo 'export PATH="$HOME/.grove/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc

# 3. Install all tools
grove install all

# 4. Verify installation
grove list
```

### Daily Tool Management
```bash
# Check for updates
grove list

# Update specific tools that have new versions
grove update cx flow

# Or update everything at once
grove update all
```

### Exploring a New Tool
```bash
# See available tools
grove list

# Install a specific tool
grove install flow

# Explore its capabilities
flow --help

# Use it for your work
flow init my-workflow
```

## Understanding Tool Aliases

Most Grove tools have short aliases for convenience:

| Full Name | Alias | Purpose |
|-----------|-------|---------|
| grove-context | cx | Context management for LLMs |
| grove-flow | flow | Workflow automation |
| grove-notebook | nb | Note-taking system |
| grove-gemini | gvm | Gemini API client |
| grove-tmux | tm | Tmux session manager |

These aliases:
- Save typing for frequently-used commands
- Are automatically created during installation
- Can be used interchangeably with full names
- Work with all Grove commands

## Troubleshooting

### Tool Not Found After Installation

If a tool isn't available after installation:

1. **Check installation status:**
   ```bash
   grove list
   ```

2. **Verify PATH includes Grove bin:**
   ```bash
   echo $PATH | grep -q ".grove/bin" && echo "PATH is correct" || echo "PATH needs updating"
   ```

3. **Check the binary exists:**
   ```bash
   ls -la ~/.grove/bin/
   ```

### Version Conflicts

If you see unexpected versions:

1. **Check active versions:**
   ```bash
   cat ~/.grove/active_versions.json
   ```

2. **Check for dev overrides:**
   ```bash
   grove dev status
   ```

3. **Reset to stable versions:**
   ```bash
   grove dev reset
   ```

### Installation Failures

If installation fails:

1. **Check network connectivity:**
   ```bash
   curl -I https://api.github.com
   ```

2. **For private repos, verify GitHub authentication:**
   ```bash
   gh auth status
   ```

3. **Try manual installation with verbose output:**
   ```bash
   grove install cx --verbose
   ```

## Next Steps

Now that you understand the basics of Grove, explore:

- [Core Concepts](./04-core-concepts.md) - Understand Grove's architecture and design
- [Command Reference](./05-command-reference.md) - Detailed documentation of all commands
- [Tutorials](./06-tutorials.md) - Step-by-step guides for complex workflows
- [Local Development](./06-tutorials.md#local-development-workflow) - Set up development environments