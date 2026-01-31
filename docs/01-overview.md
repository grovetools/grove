`grove` is the CLI interface and package manager for the Grove ecosystem, providing a unified entry point for installing, managing, and orchestrating a suite of specialized tools.

<!-- placeholder for animated gif -->

### Key Features

*   **Unified Command Interface**: Acts as a command delegator. Running `grove cx stats` finds and executes the `cx` binary with the `stats` argument, providing a single entry point for all tools.
*   **Tool Management**: Manages the lifecycle of Grove tools through `install`, `update`, and `version` commands. It resolves inter-tool dependencies, downloads official releases, and can build and install from the main branch.
*   **Local Development Support**: The `grove dev` command suite registers and switches between multiple local builds of any tool, allowing different versions from separate worktrees to be tested across the system.
*   **Ecosystem Orchestration**: Contains commands that operate across all projects in an ecosystem, including a parallel build system (`grove build`), a command runner (`grove run`), and a dependency-aware, stateful release engine (`grove release`).

