# Grove Command Reference

This reference provides detailed documentation for all Grove commands, subcommands, and their options. Grove is a command-line interface (CLI) toolkit for AI-assisted coding, featuring a package manager, development environment manager, and workspace orchestrator.

## Global Flags

These flags are available for all Grove commands:

-   `--verbose`: Enable verbose output for debugging.
-   `--json`: Output command results in JSON format, where applicable.
-   `--help`: Display help information for any command.

## Tool Management Commands

These commands manage the installation and updating of Grove tools from official releases.

### `grove install`

Installs Grove tools from GitHub releases.

**Syntax**
```bash
grove install [tool[@version]...] [flags]
```

**Description**
Installs one or more Grove tools from their GitHub releases. You can specify a tool by its name or alias. Versions can be specified with `@version`, where `version` can be a specific tag (e.g., `v0.1.0`), `latest`, or `nightly` to build and install from the main branch.

**Arguments**
-   `tool[@version]...`: One or more tools to install. The special name `all` can be used to install all available tools.

**Options**
-   `--use-gh`: Use the GitHub CLI (`gh`) for downloading assets. This is required for installing from private repositories and is auto-detected if `gh` is authenticated.

**Examples**
```bash
# Install the latest version of the context tool (alias cx)
grove install cx

# Install a specific version of the context tool
grove install cx@v0.2.0

# Install the latest development build of the context tool
grove install cx@nightly

# Install multiple tools at once
grove install cx nb flow

# Install the latest versions of all available tools
grove install all

# Install the latest development builds of all tools
grove install all@nightly
```

### `grove list`

Lists all available Grove tools and their installation status.

**Syntax**
```bash
grove list [flags]
```

**Description**
Displays a table of all tools in the Grove ecosystem, showing their repository name, installation status, currently active version, and the latest available release version. It intelligently detects whether a tool is using a released version, a development link, or is not installed.

**Options**
-   `--check-updates`: Check GitHub for the latest release versions (default: true).

**Output**
The command outputs a table with the following columns:
-   **TOOL**: The name of the binary.
-   **REPOSITORY**: The name of the source code repository.
-   **STATUS**: The current installation status (e.g., `release`, `dev`, `nightly`, `not installed`).
-   **CURRENT VERSION**: The version of the binary that is currently active.
-   **LATEST**: The latest available release tag on GitHub.

### `grove update`

Updates one or more Grove tools to their latest available release version.

**Syntax**
```bash
grove update [tools...] [flags]
```

**Description**
This command is an alias for `grove install`. If no tool names are provided, it updates the `grove` CLI itself.

**Arguments**
-   `[tools...]`: A space-separated list of tool names to update. If omitted, `grove` itself is updated. Use `all` to update all installed tools.

**Options**
-   `--use-gh`: Use the GitHub CLI (`gh`) for downloading, which supports private repositories.

**Examples**
```bash
# Update the grove CLI
grove update

# Update specific tools
grove update cx flow

# Update all installed tools
grove update all
```

### `grove self-update`

Updates the `grove` CLI to the latest version.

**Syntax**
```bash
grove self-update [flags]
```

**Description**
This is a convenient alias for `grove update grove`.

**Options**
-   `--use-gh`: Use the GitHub CLI (`gh`) for downloading.

## Version Management

These commands manage the different installed versions of released tools.

### `grove version`

Displays version information for the `grove` CLI. When used with subcommands, it manages installed tool versions.

**Syntax**
```bash
grove version
grove version <subcommand>
```

#### `grove version list`

Lists all locally installed versions of all Grove tools.

**Syntax**
```bash
grove version list
```

**Description**
Displays a table of every tool version that is downloaded to your local machine, indicating which ones are currently active.

#### `grove version use`

Switches the active version for a specific tool.

**Syntax**
```bash
grove version use <tool@version>
```

**Description**
Activates a specified, already-installed version of a tool. This updates the tool's symlink in `~/.grove/bin`. Any active development link for the tool will be cleared.

**Arguments**
-   `tool@version`: The tool and version to activate (e.g., `cx@v0.1.0`).

**Example**
```bash
grove version use flow@v1.2.3
```

#### `grove version uninstall`

Removes a specific downloaded version of all tools associated with it.

**Syntax**
```bash
grove version uninstall <version>
```

**Arguments**
-   `version`: The version tag to uninstall (e.g., `v0.1.0`).

## Development Commands (`grove dev`)

The `grove dev` subcommand suite manages local development binaries, allowing you to use tools built directly from source code (e.g., from a Git worktree) instead of released versions.

### Workspace Awareness

When you are inside a directory that contains a `.grove-workspace` file (or are in a subdirectory of one), `grove` automatically detects this "workspace context." It will prioritize using binaries discovered within that workspace over globally installed or linked ones. This enables seamless switching between different development environments simply by changing directories.

### `grove dev link`

Registers binaries from a local source code directory (a "worktree") for use.

**Syntax**
```bash
grove dev link <worktree-path> [--as <alias>]
```

**Description**
Discovers all Grove binaries within the specified `<worktree-path>`, and registers them under an alias. This makes them available to be activated with `grove dev use`.

**Arguments**
-   `worktree-path`: The path to the directory containing the source code and built binaries.

**Options**
-   `--as <alias>`: A custom alias for this version. If omitted, the directory name of `<worktree-path>` is used.

**Examples**
```bash
# Register binaries from the current directory
grove dev link .

# Register binaries from a feature branch worktree with a custom alias
grove dev link ../grove-flow-feature-branch --as feature-branch
```

### `grove dev use`

Activates a specific registered development version of a binary.

**Syntax**
```bash
grove dev use <binary-name> <alias>
grove dev use <binary-name> --release
```

**Description**
Switches the active version of a tool to a registered development version. This command updates the symlink in `~/.grove/bin` to point to the binary specified by the `<alias>`.

**Options**
-   `--release`: Switches the binary back to its currently installed release version, deactivating any dev link.

**Examples**
```bash
# Use a version linked from a feature branch
grove dev use flow feature-branch

# Switch back to the main development version
grove dev use cx main

# Switch flow back to the installed release version
grove dev use flow --release
```

### `grove dev list`

Lists all registered local development versions.

**Syntax**
```bash
grove dev list [binary-name]
```

**Description**
Shows all registered development versions. If `<binary-name>` is provided, it shows versions only for that binary. The currently active version is marked with an asterisk (`*`).

### `grove dev current`

Shows the currently active versions and sources for all Grove tools.

**Syntax**
```bash
grove dev current [binary-name]
```

**Description**
Displays the effective configuration, showing whether each tool is using a development version (`[dev]`) or a released version (`[release]`).

### `grove dev reset`

Resets all binaries back to their `main` or released versions.

**Syntax**
```bash
grove dev reset
```

**Description**
This command deactivates any custom feature-branch dev links. For each tool, it will attempt to switch to the `main` dev link if one is registered; otherwise, it falls back to the installed release version.

### `grove dev cwd`

Globally registers and activates all binaries from the current working directory.

**Syntax**
```bash
grove dev cwd
```

**Description**
This command is a shortcut for linking and using all binaries found in the current directory. It is primarily useful for forcing a specific worktree's binaries to be used globally, overriding automatic workspace detection.

### `grove dev workspace`

Displays information about the current workspace context.

**Syntax**
```bash
grove dev workspace [flags]
```

**Description**
Reports if you are inside a Grove workspace (a directory containing a `.grove-workspace` file) and lists the binaries provided by it.

**Options**
-   `--check`: Exits with status 0 if in a workspace, 1 otherwise. Useful for scripting.
-   `--path`: Prints the root path of the current workspace.

### `grove dev tui`

Launches an interactive terminal UI for managing tool versions.

**Syntax**
```bash
grove dev tui
```

**Description**
Provides an interactive interface to view all tools, their status (dev, release, not installed), available versions, and switch between them.

### `grove dev prune`

Removes registered versions whose binary files no longer exist.

**Syntax**
```bash
grove dev prune
```

**Description**
Cleans up the development link registry by removing entries that point to non-existent file paths, which can happen after deleting Git worktrees.

### Other `dev` Commands

-   `grove dev unlink <binary-name> <alias>`: Removes a specific registered development version.
-   `grove dev list-bins`: Lists all binary names that have at least one dev version registered.

## Workspace & Ecosystem Commands (`grove workspace` or `grove ws`)

These commands perform operations across an entire ecosystem of repositories.

### `grove workspace init`

Initializes a new Grove ecosystem in the current directory.

**Syntax**
```bash
grove workspace init [flags]
```
**Description**
Creates the necessary configuration files for a new monorepo-style ecosystem, including `grove.yml`, `go.work`, `.gitignore`, and a `Makefile`.

**Options**
-   `-n`, `--name`: The name of the ecosystem (defaults to the current directory name).
-   `-d`, `--description`: A description for the ecosystem.

### `grove workspace status`

Shows an aggregated status dashboard for all workspaces.

**Syntax**
```bash
grove workspace status [flags]
```
**Description**
Displays a table summarizing the status of each repository in the ecosystem.

**Options**
-   `--cols`: A comma-separated list of columns to display. Available columns include: `git`, `main-ci`, `my-prs`, `cx`, `release`, `type`.

### `grove workspace list`

Lists all discovered workspaces and their associated Git worktrees.

**Syntax**
```bash
grove workspace list
```

### `grove workspace current`

Shows current notebook notes for all workspaces.

**Syntax**
```bash
grove workspace current [--table]
```
**Options**
-   `--table`: Shows all notes in a single table sorted by date.

### `grove workspace worktrees`

Shows Git worktrees for all workspaces.

**Syntax**
```bash
grove workspace worktrees
```

### `grove workspace issues`

Shows notebook issues for all workspaces.

**Syntax**
```bash
grove workspace issues
```

### `grove workspace plans`

Shows active `grove-flow` plans for all workspaces.

**Syntax**
```bash
grove workspace plans [--table]
```
**Options**
-   `--table`: Shows all plans in a single table sorted by date.

### `grove workspace chats`

Shows recent `grove-flow` chats for all workspaces.

**Syntax**
```bash
grove workspace chats [--table]
```
**Options**
-   `--table`: Shows all chats in a single table sorted by date.

### `grove workspace git-hooks`

Manages Git hooks across all workspaces.

**Syntax**
```bash
grove workspace git-hooks <install|uninstall>
```
-   `install`: Installs commit-msg hooks in all repositories.
-   `uninstall`: Removes commit-msg hooks from all repositories.

### `grove workspace secrets`

Manages GitHub repository secrets across all workspaces.

**Syntax**
```bash
grove workspace secrets <list|set|delete> [args...]
```
-   `list`: Lists secrets for all repositories.
-   `set <NAME> [VALUE]`: Sets a secret for all repositories.
-   `delete <NAME>`: Deletes a secret from all repositories.

### `grove workspace manage`

Interactively manage notes in the current workspace (alias for `nb manage`).

**Syntax**
```bash
grove workspace manage
```

## Utility Commands

### `grove run`

Executes a command in each discovered workspace.

**Syntax**
```bash
grove run <command> [args...] [flags]
```

**Description**
A powerful utility for running a command across the entire ecosystem. The command is executed from the root of each workspace directory.

**Options**
-   `-f`, `--filter`: Filters workspaces using a glob pattern.

**Examples**
```bash
# Get git status for all workspaces
grove run git status

# Run tests only in context-related workspaces
grove run --filter "grove-context*" make test
```

### `grove add-repo`

Creates a new Grove repository with a standard project structure.

**Syntax**
```bash
grove add-repo <repo-name> [flags]
```

**Arguments**
-   `repo-name`: The name for the new repository.

**Options**
-   `-a`, `--alias`: A short alias for the binary.
-   `-d`, `--description`: A description for the repository.
-   `--template`: The project template to use. Options: `go` (default), `maturin`, `react-ts`, or a custom GitHub repository (`owner/repo`) or local path.
-   `--ecosystem`: Adds the new repository as a submodule to the current ecosystem.
-   `--skip-github`: Skips creating a repository on GitHub.
-   `--public`: Creates a public GitHub repository.
-   `--dry-run`: Shows the operations that would be performed without executing them.

### `grove git-hooks`

Manages Git hooks for the current repository.

**Syntax**
```bash
grove git-hooks <install|uninstall>
```
-   `install`: Installs a `commit-msg` hook to enforce conventional commits.
-   `uninstall`: Removes the hook.

### `grove changelog`

Generates a changelog entry from conventional commits.

**Syntax**
```bash
grove changelog <repo-path> [flags]
```
**Description**
Generates a changelog entry for all commits since the last Git tag and prepends it to `CHANGELOG.md`.

**Options**
-   `--version`: The version string to use for the changelog header.
-   `--llm`: Uses an LLM to generate a more descriptive, summary-style changelog.

### `grove llm request`

Provides a unified interface for making requests to different LLM providers.

**Syntax**
```bash
grove llm request [prompt...] [flags]
```
**Description**
Acts as a smart router that delegates LLM requests to the appropriate tool (`gemapi`, `grove-openai`, etc.) based on the specified model name.

**Options**
-   `-m`, `--model`: The model to use (e.g., `gpt-4o-mini`, `gemini-1.5-flash-latest`). This is required.
-   All flags from underlying tools like `gemapi` are supported and passed through.

### `grove logs`

Tails logs from Grove workspaces.

**Syntax**
```bash
grove logs [workspace-filter...] [flags]
```
**Description**
Provides a real-time, aggregated view of structured logs from `.grove/logs` directories in workspaces. By default, it launches an interactive TUI.

**Options**
-   `--ecosystem`: Shows logs from all workspaces in the ecosystem.
-   `-f`, `--follow`: Continuously tail logs (default: true).
-   `-n`, `--lines`: Number of recent lines to show.
-   `-i`, `--tui`: Launch the interactive TUI mode (default if in a TTY).

### `grove activate`

Generates shell commands to manage the `PATH` for a workspace.

**Syntax**
```bash
eval "$(grove activate)"
eval "$(grove activate --reset)"
```
**Description**
This command outputs shell code that, when evaluated, adds a workspace's local binary directories to the front of your `PATH`, prioritizing them. This is useful for creating shell aliases or functions to quickly enter and exit a workspace's development environment.

**Options**
-   `--reset`: Generates commands to restore the original `PATH`.
-   `--shell`: Specifies the shell type (e.g., `bash`, `zsh`, `fish`). Auto-detected by default.

## Dependency Management (`grove deps`)

Commands for managing Go module dependencies across the entire ecosystem.

### `grove deps sync`

Updates all internal Grove dependencies to their latest versions across all repositories.

**Syntax**
```bash
grove deps sync [flags]
```
**Options**
-   `--commit`: Creates a git commit in each updated repository.
-   `--push`: Pushes the commit to origin (implies `--commit`).

### `grove deps bump`

Updates a specific dependency to a specified version across all repositories.

**Syntax**
```bash
grove deps bump <module-path>[@version] [flags]
```
**Arguments**
-   `module-path[@version]`: The Go module path and optional version (e.g., `github.com/mattsolo1/grove-core@v0.2.1`). Defaults to `@latest`.

**Options**
-   `--commit`: Creates a git commit in each updated repository.
-   `--push`: Pushes the commit to origin (implies `--commit`).

### `grove deps tree`

Displays a dependency tree visualization of the ecosystem.

**Syntax**
```bash
grove deps tree [repo] [flags]
```
**Arguments**
-   `[repo]`: An optional repository name to focus the tree on.

**Options**
-   `--versions`: Shows version information in the tree.
-   `--external`: Includes external (non-Grove) dependencies.

## Release Orchestration (`grove release`)

Commands for creating and managing orchestrated releases.

### `grove release`

Calculates and executes a versioned release across the ecosystem.

**Syntax**
```bash
grove release [flags]
```
**Description**
This is a comprehensive command that analyzes commits, suggests version bumps, updates dependencies, generates changelogs, tags repositories, and monitors CI/CD pipelines in the correct dependency order. For a safer and more manageable workflow, using the interactive TUI is recommended.

**Options**
-   `--interactive`: **(Recommended)** Launches the interactive TUI for a guided release process.
-   `--dry-run`: Prints all commands without executing them.
-   `--force`: Skips checks for clean Git working directories.
-   `--push`: Pushes all repository changes to the remote before tagging.
-   `--major|--minor|--patch <repos...>`: Specifies a version bump type for one or more repositories.
-   `--with-deps`: Automatically includes all dependencies of the specified repositories in the release.
-   `--llm-changelog`: Generates changelogs using an LLM.
-   `--yes`: Skips the final confirmation prompt.

### `grove release tui`

Launches an interactive TUI for planning and executing a release.

**Syntax**
```bash
grove release tui [--fresh]
```
**Description**
The TUI provides a step-by-step interface for managing a release. It generates a `release_plan.json` file, allowing you to review, edit changelogs, approve repositories, and then apply the release. The process can be stopped and resumed.

**Options**
-   `--fresh`: Clears any existing release plan and starts a new one.

## Tool Delegation

If a command is not a recognized `grove` command, Grove will attempt to delegate it to an installed tool.

**Syntax**
```bash
grove <tool-alias-or-name> [args...]
```
**Description**
This allows you to run any installed Grove tool through the main `grove` binary. When inside a workspace, this will automatically use the workspace's local binary for that tool.

**Examples**
```bash
# Equivalent to running the 'cx' binary
grove cx stats

# Equivalent to running the 'flow' binary
grove flow plan my-plan.md
```