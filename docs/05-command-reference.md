# Grove Command Reference

This reference provides detailed documentation for all Grove commands, subcommands, and their options.

## Global Flags

These flags are available for all Grove commands:

- `--verbose`: Enable verbose output for debugging
- `--help`: Display help information for any command
- `--version`: Display Grove version information

## Primary Commands

### grove install

Install Grove tools from GitHub releases.

```bash
grove install [tool[@version]...] [flags]
```

**Arguments:**
- `tool`: Tool name or alias (can specify multiple)
- `@version`: Optional version specification (e.g., `@v0.1.0`, `@latest`, `@nightly`)
- `all`: Special keyword to install all available tools

**Flags:**
- `--use-gh`: Use GitHub CLI for downloading (supports private repos)

**Examples:**
```bash
grove install cx                    # Install latest version of context tool
grove install cx@v0.2.0             # Install specific version
grove install cx@nightly            # Build from main branch
grove install cx nb flow            # Install multiple tools
grove install all                   # Install all available tools
grove install all --use-gh          # Install from private repos
```

### grove update

Update Grove tools to their latest versions.

```bash
grove update [tools...] [flags]
```

**Arguments:**
- `tools`: Tool names to update (optional)
- If no tools specified, updates Grove itself

**Flags:**
- `--use-gh`: Use GitHub CLI for downloading

**Examples:**
```bash
grove update                        # Update Grove itself
grove update cx nb                  # Update specific tools
grove update all                    # Update all installed tools
grove update --use-gh               # Update from private repos
```

### grove self-update

Update the Grove CLI itself (alias for `grove update` with no arguments).

```bash
grove self-update [flags]
```

**Flags:**
- `--use-gh`: Use GitHub CLI for downloading

### grove list

Display all available Grove tools and their installation status.

```bash
grove list [flags]
```

**Flags:**
- `--check-updates`: Check for latest releases from GitHub (default: true)

**Output includes:**
- Tool name and alias
- Installation status
- Currently active version
- Latest available release
- Repository information

### grove version

Display version information for Grove or installed tools.

```bash
grove version [tool] [flags]
```

**Arguments:**
- `tool`: Optional tool name to check version for

**Subcommands:**
- `set`: Set the active version for a tool
- `list`: List all available versions for a tool

**Examples:**
```bash
grove version                       # Show Grove version
grove version cx                    # Show context tool version
grove version list cx               # List all installed versions
grove version set cx v0.2.0         # Switch to specific version
```

## Development Commands

### grove dev

Manage local development versions of Grove tools.

```bash
grove dev <subcommand>
```

**Subcommands:**

#### grove dev link

Create a development link to a local binary.

```bash
grove dev link <tool> <path> [flags]
```

**Arguments:**
- `tool`: Name of the tool to link
- `path`: Path to the local binary

**Flags:**
- `--name`: Custom name for the link (default: auto-generated)
- `--scope`: Link scope: global, workspace, or project (default: global)

**Examples:**
```bash
grove dev link cx ./bin/context
grove dev link cx ./bin/context --name feature-branch
grove dev link cx ./bin/context --scope workspace
```

#### grove dev use

Activate a specific development link.

```bash
grove dev use <tool> <link-name>
```

**Examples:**
```bash
grove dev use cx feature-branch
grove dev use context main
```

#### grove dev reset

Reset tools to their release versions.

```bash
grove dev reset [tool]
```

**Arguments:**
- `tool`: Optional tool name (resets all if not specified)

**Examples:**
```bash
grove dev reset                     # Reset all tools
grove dev reset cx                  # Reset specific tool
```

#### grove dev status

Show status of all development links.

```bash
grove dev status
```

#### grove dev list

List all development links for a tool.

```bash
grove dev list <tool>
```

#### grove dev prune

Remove broken or invalid development links.

```bash
grove dev prune [flags]
```

**Flags:**
- `--dry-run`: Show what would be removed without removing

#### grove dev tui

Launch interactive TUI for managing development links.

```bash
grove dev tui
```

**Features:**
- Visual link management
- Quick switching between versions
- Status monitoring
- Keyboard navigation

#### grove dev cwd

Set the current working directory for a tool.

```bash
grove dev cwd <tool> [path]
```

**Arguments:**
- `tool`: Tool name
- `path`: Optional path (uses current directory if not specified)

## Dependency Commands

### grove deps

Manage dependencies across the Grove ecosystem.

```bash
grove deps <subcommand>
```

#### grove deps sync

Update all Grove dependencies to their latest versions.

```bash
grove deps sync [flags]
```

**Flags:**
- `--commit`: Create git commits in updated modules
- `--push`: Push commits to origin (implies --commit)

**Examples:**
```bash
grove deps sync
grove deps sync --commit
grove deps sync --push
```

#### grove deps bump

Update a specific dependency across all submodules.

```bash
grove deps bump <module_path>[@version] [flags]
```

**Arguments:**
- `module_path`: Go module path (e.g., `github.com/mattsolo1/grove-core`)
- `@version`: Optional version (default: @latest)

**Flags:**
- `--commit`: Create git commits
- `--push`: Push commits to origin

**Examples:**
```bash
grove deps bump github.com/mattsolo1/grove-core@v0.2.1
grove deps bump github.com/mattsolo1/grove-core@latest --commit
```

#### grove deps tree

Display the dependency tree for all modules.

```bash
grove deps tree [flags]
```

**Flags:**
- `--json`: Output in JSON format

## Release Commands

### grove release

Create orchestrated releases for the Grove ecosystem.

```bash
grove release [flags]
```

**Flags:**
- `--dry-run`: Print commands without executing
- `--force`: Skip dirty repository check
- `--force-increment`: Force version increment even without changes
- `--push`: Push tags to origin (default: true)
- `--major <repos>`: Repositories to bump major version
- `--minor <repos>`: Repositories to bump minor version
- `--patch <repos>`: Repositories to bump patch version
- `--yes`: Skip confirmation prompt
- `--skip-parent`: Skip parent repository operations
- `--with-deps`: Include dependent repositories
- `--sync-deps`: Sync dependencies before release
- `--llm-changelog`: Generate changelogs using LLM
- `--interactive`: Launch interactive TUI

**Examples:**
```bash
grove release                                      # Patch all repos
grove release --minor grove-core                   # Minor bump specific repo
grove release --major grove-core --minor grove-meta # Mixed bumps
grove release --dry-run                            # Preview without executing
grove release --interactive                        # Launch TUI
```

### grove release tui

Launch interactive release planning TUI.

```bash
grove release tui
```

**Features:**
- Visual dependency graph
- Version bump selection
- Changelog preview
- Git status monitoring
- One-click release execution

## Workspace Commands

### grove workspace (aliases: ws, w)

Manage Grove workspaces and projects.

```bash
grove workspace <subcommand>
```

#### grove workspace init

Initialize a new Grove workspace or ecosystem.

```bash
grove workspace init [path] [flags]
```

**Arguments:**
- `path`: Directory to initialize (default: current directory)

**Flags:**
- `--name`: Workspace name
- `--workspaces`: Comma-separated workspace patterns

**Examples:**
```bash
grove workspace init
grove workspace init my-ecosystem --name "My Ecosystem"
grove workspace init . --workspaces "packages/*,tools/*"
```

#### grove workspace list

List all workspaces in the current ecosystem.

```bash
grove workspace list [flags]
```

**Flags:**
- `--json`: Output in JSON format

#### grove workspace status

Show detailed status of all workspaces.

```bash
grove workspace status [flags]
```

**Flags:**
- `--fetch`: Fetch latest changes from remotes
- `--json`: Output in JSON format

#### grove workspace current

Display information about the current workspace.

```bash
grove workspace current [flags]
```

**Flags:**
- `--json`: Output in JSON format

#### grove workspace worktrees

Manage Git worktrees for workspaces.

```bash
grove workspace worktrees <subcommand>
```

**Subcommands:**
- `list`: List all worktrees
- `add <branch>`: Create new worktree
- `remove <path>`: Remove worktree

#### grove workspace secrets

Manage secrets across workspaces.

```bash
grove workspace secrets <subcommand>
```

**Subcommands:**
- `list`: List all secrets
- `set <key> <value>`: Set a secret
- `get <key>`: Get a secret value
- `delete <key>`: Delete a secret

#### grove workspace issues

Manage GitHub issues across workspaces.

```bash
grove workspace issues [flags]
```

**Flags:**
- `--state`: Issue state (open, closed, all)
- `--labels`: Filter by labels
- `--assignee`: Filter by assignee

#### grove workspace plans

Manage Grove Flow plans across workspaces.

```bash
grove workspace plans <subcommand>
```

**Subcommands:**
- `list`: List all plans
- `run <plan>`: Execute a plan
- `status <plan>`: Check plan status

## Utility Commands

### grove run

Execute commands in workspace contexts.

```bash
grove run <command> [args...] [flags]
```

**Flags:**
- `--workspace`: Specific workspace to run in
- `--all`: Run in all workspaces
- `--parallel`: Run in parallel (with --all)

**Examples:**
```bash
grove run make test
grove run --all git status
grove run --workspace grove-core go test ./...
```

### grove git-hooks

Manage Git hooks for Grove projects.

```bash
grove git-hooks <subcommand>
```

**Subcommands:**
- `install`: Install Grove Git hooks
- `uninstall`: Remove Grove Git hooks

**Hooks provided:**
- `commit-msg`: Enforce conventional commit format
- `pre-commit`: Run pre-commit checks

### grove add-repo

Add a new repository to the Grove ecosystem.

```bash
grove add-repo <repo-name> [flags]
```

**Arguments:**
- `repo-name`: Name of the new repository

**Flags:**
- `--template`: Project template to use
- `--ecosystem`: Add to ecosystem configuration
- `--github`: Create GitHub repository
- `--private`: Make repository private

**Examples:**
```bash
grove add-repo grove-newtool --template go
grove add-repo grove-python --template maturin --ecosystem
```

### grove llm

Interact with LLM for Grove operations.

```bash
grove llm <subcommand>
```

**Subcommands:**
- `changelog`: Generate changelog using LLM
- `release-notes`: Create release notes
- `commit-message`: Generate commit message

**Flags:**
- `--model`: LLM model to use
- `--provider`: LLM provider (gemini, openai, etc.)

## Tool Delegation

Grove can delegate commands directly to installed tools:

```bash
grove <tool> [args...]
```

**Examples:**
```bash
grove cx update              # Run context tool's update command
grove flow init my-workflow  # Run flow tool's init command
grove nb create note.md      # Run notebook tool's create command
```

## Configuration Commands

While Grove primarily uses `grove.yml` files for configuration, some commands help manage settings:

### grove config

View and modify Grove configuration.

```bash
grove config <subcommand>
```

**Subcommands:**
- `get <key>`: Get configuration value
- `set <key> <value>`: Set configuration value
- `list`: List all configuration

## Environment Variables

Grove respects several environment variables:

- `GROVE_HOME`: Override default Grove directory (default: `~/.grove`)
- `GROVE_DEBUG`: Enable debug logging
- `GROVE_USE_GH`: Always use GitHub CLI for operations
- `GROVE_WORKSPACE`: Default workspace for operations

## Exit Codes

Grove uses standard exit codes:

- `0`: Success
- `1`: General error
- `2`: Command line usage error
- `3`: Network or download error
- `4`: File system error
- `5`: Git operation error

## Command Aliases

Many Grove commands support short aliases for convenience:

| Command | Aliases |
|---------|---------|
| workspace | ws, w |
| context | cx |
| notebook | nb |
| flow | fl |
| gemini | gvm |
| tmux | tm |

## Getting Help

Get help for any command:

```bash
grove --help
grove <command> --help
grove <command> <subcommand> --help
```

## Command Completion

Grove supports shell completion for bash, zsh, and fish:

```bash
# Bash
grove completion bash > /etc/bash_completion.d/grove

# Zsh
grove completion zsh > ~/.zsh/completions/_grove

# Fish
grove completion fish > ~/.config/fish/completions/grove.fish
```