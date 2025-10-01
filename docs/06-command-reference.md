# Command Reference

This document provides a reference for all `grove` commands, organized by functional category.

## Tool Management

Commands for installing, updating, and listing Grove tools.

### grove install

**Synopsis**: Install Grove tools from GitHub releases.

**Syntax**: `grove install [tool[@version]...]`

**Description**:
Installs one or more Grove tools from their GitHub releases. A tool can be specified by its repository name (e.g., `grove-context`) or its alias (`cx`). A specific version can be requested using the `@` syntax (e.g., `cx@v0.1.0`). To install a development build from the `main` branch, use `@nightly`. Using `all` will install all available tools. The command automatically resolves and installs any required dependencies.

**Arguments**:
-   `[tool[@version]...]` (required): One or more tools to install. Can optionally specify a version.

**Flags**:
| Flag     | Description                                           |
| :------- | :---------------------------------------------------- |
| `--use-gh` | Use the `gh` CLI for downloading, which supports private repositories. |

**Examples**:
```bash
# Install the latest version of grove-context
grove install cx

# Install a specific version of grove-context
grove install grove-context@v0.1.0

# Install a nightly build of grove-flow
grove install flow@nightly

# Install multiple tools
grove install cx nb flow

# Install all available tools
grove install all

# Install all nightly builds
grove install all@nightly
```
**Related Commands**: `grove update`, `grove self-update`, `grove list`

---

### grove update

**Synopsis**: Update Grove tools.

**Syntax**: `grove update [tools...]`

**Description**:
Updates one or more Grove tools by reinstalling their latest versions. This command is an alias for `grove install`. If no tools are specified, it updates the `grove` meta-CLI itself.

**Arguments**:
-   `[tools...]` (optional): One or more tools to update. If omitted, `grove` itself is updated.

**Flags**:
| Flag     | Description                                           |
| :------- | :---------------------------------------------------- |
| `--use-gh` | Use the `gh` CLI for downloading, which supports private repositories. |

**Examples**:
```bash
# Update the grove CLI itself
grove update

# Update specific tools
grove update context flow

# Update all installed tools
grove update all
```
**Related Commands**: `grove install`, `grove self-update`

---

### grove self-update

**Synopsis**: Update the `grove` CLI to the latest version.

**Syntax**: `grove self-update`

**Description**:
This command is an alias for `grove update grove`. It downloads and replaces the current `grove` binary with the latest available release.

**Flags**:
| Flag     | Description                                           |
| :------- | :---------------------------------------------------- |
| `--use-gh` | Use the `gh` CLI for downloading, which supports private repositories. |

**Examples**:
```bash
# Update the grove CLI to its latest version
grove self-update
```
**Related Commands**: `grove update`

---

### grove list

**Synopsis**: List available Grove tools.

**Syntax**: `grove list`

**Description**:
Displays a table of all available Grove tools, their installation status, currently active version, and the latest available release version from GitHub.

**Flags**:
| Flag            | Description                                  |
| :-------------- | :------------------------------------------- |
| `--check-updates` | Check for latest releases from GitHub. (default `true`) |

**Examples**:
```bash
# List all tools and their status
grove list
```
**Related Commands**: `grove install`

---

### grove alias

**Synopsis**: Manage custom aliases for Grove tools.

**Syntax**: `grove alias [subcommand]`

**Description**:
Manages custom aliases for Grove tools, which can resolve PATH conflicts or suit personal preferences. When an alias is set, the tool's binary/symlink in `~/.grove/bin` is renamed.

**Subcommands**:
-   `set <tool> <new-alias>`: Sets a custom alias for a tool.
-   `unset <tool>`: Removes a custom alias, reverting to the default.

**Examples**:
```bash
# List all custom and default aliases
grove alias

# Set a new alias for grove-context
grove alias set grove-context ctx

# Revert to the default alias
grove alias unset grove-context
```

## Version Management

Commands for managing different installed versions of tools.

### grove version

**Synopsis**: Manage Grove tool versions.

**Syntax**: `grove version [subcommand]`

**Description**:
When run without subcommands, it prints version information for the `grove` meta-CLI itself. It also serves as the parent command for listing, switching, and uninstalling specific tool versions.

**Flags**:
| Flag     | Description                             |
| :------- | :-------------------------------------- |
| `--json` | Output version information in JSON format. |

**Subcommands**:
-   `list`: List all locally installed versions of all tools.
-   `use <tool@version>`: Switch a tool to a specific installed version.
-   `uninstall <version>`: Uninstall a specific version of all tools it contains.

**Examples**:
```bash
# Show version of the grove meta-CLI
grove version

# List all installed tool versions
grove version list

# Switch grove-context to an installed version
grove version use cx@v0.1.0

# Uninstall a specific version
grove version uninstall v0.1.0
```

## Local Development

Commands for managing local, source-built development binaries. These commands allow you to use versions of tools that are not from an official release, which is useful for development and testing.

### grove dev

**Synopsis**: Manage local development binaries.

**Syntax**: `grove dev [subcommand]`

**Description**:
The parent command for a suite of tools that manage local development binaries built from source. This allows for switching between different versions of Grove tools built from different Git worktrees. This system is distinct from `grove version`, which manages official releases.

**Subcommands**:
-   `link`: Register binaries from a local worktree.
-   `unlink`: Remove a registered local development version.
-   `use`: Switch to a specific locally-linked version of a binary.
-   `list`: List registered local development versions.
-   `list-bins`: List all binaries managed by local development links.
-   `current`: Show currently active local development versions.
-   `prune`: Remove registered versions whose binaries no longer exist.
-   `reset`: Reset all binaries to their main/released versions.
-   `cwd`: Globally activate binaries from the current directory.
-   `tui`: Launch an interactive tool version manager.
-   `workspace`: Display information about the current workspace context.

---

### grove activate

**Synopsis**: Generate shell commands to activate workspace binaries.

**Syntax**: `grove activate`

**Description**:
Outputs shell commands that, when evaluated, will modify the current shell's `PATH` to prioritize binaries from the current workspace. This is useful for making workspace-specific tools directly available in the shell. The command auto-detects the shell type (bash, zsh, fish).

**Flags**:
| Flag      | Description                                                    |
| :-------- | :------------------------------------------------------------- |
| `--reset` | Generate commands to reset the `PATH` to its original state.         |
| `--shell` | Specify shell type (`bash`, `zsh`, `fish`). Auto-detected if omitted. |

**Examples**:
```bash
# Activate workspace binaries in the current shell
eval "$(grove activate)"

# Reset PATH to its original state
eval "$(grove activate --reset)"
```

## Workspace & Ecosystem Management

Commands for creating, managing, and getting insights into a monorepo containing multiple Grove workspaces.

### grove workspace (ws)

**Synopsis**: Workspace operations across the monorepo.

**Syntax**: `grove workspace [subcommand]`

**Description**:
Parent command for viewing aggregated information and executing operations across all discovered workspaces in an ecosystem.

**Subcommands**:
-   `init`: Initialize a new Grove ecosystem.
-   `create`: Create a new development workspace worktree.
-   `open`: Open a tmux session for a workspace.
-   `remove`: Remove a development workspace worktree.
-   `list`: List workspaces and their Git worktrees.
-   `status`: Show aggregated status for all workspaces.
-   `worktrees`: Show Git worktrees for all workspaces.
-   `issues`: Show notebook issues for all workspaces.
-   `current`: Show notebook current notes for all workspaces.
-   `plans`: Show flow plans for all workspaces.
-   `chats`: Show flow chats for all workspaces.
-   `git-hooks`: Manage Git hooks across all workspaces.
-   `secrets`: Manage GitHub repository secrets.
-   `manage`: Interactively manage notes (alias for `nb manage`).

---

### grove add-repo

**Synopsis**: Create a new Grove repository with standard structure.

**Syntax**: `grove add-repo <repo-name>`

**Description**:
Creates a new Grove repository with idiomatic structure. By default, it creates a standalone repository. Use the `--ecosystem` flag to add it as a submodule to an existing Grove ecosystem.

**Arguments**:
-   `<repo-name>` (required): The name of the new repository.

**Flags**:
| Flag                | Alias | Description                                                                |
| :------------------ | :---- | :------------------------------------------------------------------------- |
| `--alias`           | `-a`  | Binary alias (e.g., `cx` for `grove-context`).                               |
| `--description`     | `-d`  | Repository description.                                                    |
| `--skip-github`     |       | Skip GitHub repository creation.                                           |
| `--dry-run`         |       | Preview operations without executing them.                                 |
| `--stage-ecosystem` |       | Stage ecosystem changes in Git after adding the repo.                      |
| `--template`        |       | Template to use (`go`, `maturin`, `react-ts`, or a GitHub repo `owner/repo`). |
| `--ecosystem`       |       | Add repository to an existing Grove ecosystem as a submodule.                |
| `--public`          |       | Create a public repository and skip private configuration.                 |

**Examples**:
```bash
# Create a standalone Go repository
grove add-repo my-tool --alias mt --description "My new tool"

# Create a Python/Rust repository and add it to the current ecosystem
grove add-repo my-lib --template maturin --alias ml --ecosystem
```
**Related Commands**: `grove workspace init`

---

### grove deps

**Synopsis**: Manage dependencies across the Grove ecosystem.

**Syntax**: `grove deps [subcommand]`

**Description**:
Provides tools for managing Go module dependencies across all Grove submodules within an ecosystem.

**Subcommands**:
-   `bump <module[@version]>`: Bumps a specific dependency version across all submodules.
-   `sync`: Updates all internal Grove dependencies to their latest versions.
-   `tree [repo]`: Displays a dependency tree visualization.

**Examples**:
```bash
# Update all internal dependencies to their latest versions and commit
grove deps sync --commit

# Bump a specific core library to a specific version
grove deps bump github.com/mattsolo1/grove-core@v0.3.0

# View the entire dependency graph
grove deps tree
```

---

### grove run

**Synopsis**: Run a command in all workspaces.

**Syntax**: `grove run <command> [args...]`

**Description**:
Executes a given command in each discovered workspace directory.

**Arguments**:
-   `<command> [args...]` (required): The command and its arguments to execute.

**Flags**:
| Flag       | Alias | Description                                     |
| :--------- | :---- | :---------------------------------------------- |
| `--filter` | `-f`  | Filter workspaces by a glob pattern.            |
| `--exclude`|       | Comma-separated list of workspaces to exclude.  |
| `--json`   |       | Aggregate and output results in JSON format.    |

**Examples**:
```bash
# Run git status in all workspaces
grove run git status

# Run tests in all workspaces matching a pattern
grove run --filter "grove-*" make test
```

---

### grove docs

**Synopsis**: Manage documentation across the ecosystem.

**Syntax**: `grove docs [subcommand]`

**Description**:
Provides tools for managing documentation across all discovered workspaces.

**Subcommands**:
-   `generate`: Runs `docgen generate` in each discovered workspace to update all documentation in a single step. Accepts a `--commit` flag to commit changes.

**Examples**:
```bash
# Generate documentation for all workspaces and commit the changes
grove docs generate --commit
```

## Release Orchestration

Commands for managing a stateful, multi-step release workflow across the entire ecosystem.

### grove release

**Synopsis**: Manage releases for the Grove ecosystem.

**Syntax**: `grove release [subcommand]`

**Description**:
The parent command for a stateful release workflow. The typical workflow is `plan` -> `tui` -> `apply`.

**Subcommands**:
-   `plan`: Generates a release plan by analyzing all repositories for changes.
-   `tui` (or `review`): Launches an interactive TUI to review, modify, and approve the plan.
-   `apply`: Executes the approved release plan.
-   `clear-plan`: Clears the current release plan to start over.
-   `undo-tag`: Removes tags created during a release, locally and optionally from remote.
-   `rollback`: Rolls back commits in repositories from the release plan to recover from a failed release.

**Examples**:
```bash
# 1. Generate a release plan
grove release plan

# 2. Interactively review and approve the plan
grove release tui

# 3. Execute the approved plan
grove release apply

# (If something goes wrong) Undo the tags from the last plan
grove release undo-tag --from-plan --remote
```

---

### grove changelog

**Synopsis**: Generate a changelog for a repository.

**Syntax**: `grove changelog <repo-path>`

**Description**:
Generates a changelog entry for a repository based on its Git history since the last tag and prepends it to `CHANGELOG.md`. By default, it uses conventional commits. With the `--llm` flag, it uses an LLM to generate a more descriptive summary.

**Arguments**:
-   `<repo-path>` (required): The path to the repository.

**Flags**:
| Flag      | Description                               |
| :-------- | :---------------------------------------- |
| `--version` | The new version for the changelog header. |
| `--llm`   | Generate the changelog using an LLM.    |

**Examples**:
```bash
# Generate changelog from conventional commits for the current directory
grove changelog . --version v1.2.3

# Generate changelog using an LLM
grove changelog . --version v1.2.3 --llm
```

## Git Integration

Commands for running Git operations across workspaces.

### grove git

**Synopsis**: Git operations across workspaces.

**Syntax**: `grove git [git-command-args...]`

**Description**:
A convenience wrapper that executes a `git` command across all discovered workspaces. This is an alias for `grove run git [args...]`.

**Examples**:
```bash
# Fetch from origin in all workspaces
grove git fetch

# See a condensed status for all workspaces
grove ws status
```
**Related Commands**: `grove workspace status`

---

### grove git-hooks

**Synopsis**: Manage Git hooks for Grove repositories.

**Syntax**: `grove git-hooks [subcommand]`

**Description**:
Installs or uninstalls Git hooks that enforce conventional commit message formats in the current repository.

**Subcommands**:
-   `install`: Installs the `commit-msg` hook.
-   `uninstall`: Removes the Grove-managed `commit-msg` hook.

**Examples**:
```bash
# Install conventional commit hooks in the current git repository
grove git-hooks install
```
**Related Commands**: `grove workspace git-hooks`

## Utility Commands

General-purpose helper commands.

### grove llm

**Synopsis**: Unified interface for LLM providers.

**Syntax**: `grove llm [subcommand]`

**Description**:
Provides a single, consistent entry point for all LLM interactions, intelligently delegating to the appropriate provider-specific tool (e.g., `gemapi`, `grove-openai`) based on the model name.

**Subcommands**:
-   `request [prompt...]`: Makes a request to an LLM provider. This command accepts a superset of flags from the underlying tools. Use `grove llm request --help` for a full list of options.

**Examples**:
```bash
# Make a request using the default model configured in grove.yml
grove llm request "Explain the reconciler pattern in grove-meta"

# Make a request to a specific GPT model
grove llm request --model gpt-4o-mini "Summarize the changes in the last commit"
```

---

### grove logs

**Synopsis**: Tail logs from workspaces.

**Syntax**: `grove logs [workspace-filter...]`

**Description**:
Provides a real-time, color-coded view of structured logs from workspaces. By default, it launches an interactive TUI.

**Arguments**:
-   `[workspace-filter...]` (optional): Filter to show logs only from workspaces whose names contain the filter string.

**Flags**:
| Flag        | Alias | Description                               | Default |
| :---------- | :---- | :---------------------------------------- | :------ |
| `--ecosystem` |       | Show logs from all workspaces.            | `false` |
| `--follow`  | `-f`  | Continuously tail logs.                   | `true`  |
| `--lines`   | `-n`  | Number of historical lines to show.       | `10`    |
| `--tui`     | `-i`  | Launch interactive TUI mode.              | `true` if tty |
| `--verbose` | `-v`  | Increase verbosity (use `-v`, `-vv`, `-vvv`). |         |

**Examples**:
```bash
# Launch the interactive TUI for logs from all workspaces
grove logs --ecosystem

# Tail logs from 'grove-context' and 'grove-flow' in the terminal
grove logs context flow --tui=false
```