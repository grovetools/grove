# Grove CLI

<img src="./images/grove-base-readme.svg" width="60%" />

`grove` is the meta-command-line interface and package manager for the Grove ecosystem. It provides a unified entry point for installing, managing, and orchestrating a suite of specialized tools.

<!-- placeholder for animated gif -->

### Key Features

*   **Tool Management**: Manages the lifecycle of Grove tools through `install`, `update`, and `version` commands. It resolves inter-tool dependencies, downloads official releases, and can install nightly builds from source.
*   **Unified Command Interface**: Acts as a command delegator. Running `grove cx stats` finds and executes the `cx` binary with the `stats` argument, providing a single entry point for all tools.
*   **Local Development Support**: The `grove dev` command suite links and switches between multiple local builds of any tool, allowing different versions to be tested across the system. The `grove activate` command provides shell integration for development workspaces.
*   **Ecosystem Orchestration**: Contains high-level commands that operate across all workspaces in a monorepo. This includes aggregated status dashboards (`grove ws status`) and a dependency-aware release engine (`grove release`).
*   **Aggregated Views**: Offers commands like `grove logs` and `grove llm` that aggregate information from multiple tools across all projects in an ecosystem.

## Ecosystem Integration

The `grove` CLI orchestrates other tools in the ecosystem by executing them as subprocesses.

*   **`grove release`**: The release engine uses `gh` to monitor CI/CD workflows and `gemapi` (via `grove llm`) to generate changelogs from commit history.
*   **`grove add-repo`**: Uses `gh` to create GitHub repositories and configure secrets.
*   **`grove docs generate`**: Calls the `docgen` binary in each workspace to generate documentation.
*   **`grove logs`**: Discovers log files created by other Grove tools and tails them into a unified stream or interactive TUI.
*   **`grove llm`**: Acts as a facade, delegating requests to the appropriate provider-specific tool (`gemapi`, `grove-openai`) based on the model name.

## How It Works

The `grove` CLI functions as a command delegator. When a command like `grove cx stats` is run, `grove` searches for an executable named `cx` and executes it with the provided arguments.

The binary resolution follows a specific order of precedence:
1.  **Workspace Binary**: If the current directory is within a development workspace (identified by a `.grove-workspace` file), `grove` prioritizes binaries found within that workspace's directories. This allows local builds to be used automatically in their development context.
2.  **Global Development Link**: If not in a workspace, it checks for an active development link created by `grove dev use`. This points to a user-specified local build, making it the global default.
3.  **Global Release Binary**: If no development version is active, it falls back to the globally managed symlink in `~/.grove/bin`, which points to an installed release version.

## Installation

Install the Grove meta-CLI:
```bash
# Install instructions from Grove Installation Guide
```

Verify installation:
```bash
grove version
```

See the complete [Grove Installation Guide](https://github.com/mattsolo1/grove-meta/blob/main/docs/02-installation.md) for detailed setup instructions.
