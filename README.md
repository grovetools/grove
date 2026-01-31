<p align="center">
  <img src="https://grovetools.ai/docs/grove/images/grove-logo-with-text-dark.svg" alt="Grove" width="150">
</p>

<!-- DOCGEN:OVERVIEW:START -->

`grove` is the CLI interface and package manager for the Grove ecosystem, providing a unified entry point for installing, managing, and orchestrating a suite of specialized tools.

<!-- placeholder for animated gif -->

### Key Features

*   **Unified Command Interface**: Acts as a command delegator. Running `grove cx stats` finds and executes the `cx` binary with the `stats` argument, providing a single entry point for all tools.
*   **Tool Management**: Manages the lifecycle of Grove tools through `install`, `update`, and `version` commands. It resolves inter-tool dependencies, downloads official releases, and can build and install from the main branch.
*   **Local Development Support**: The `grove dev` command suite registers and switches between multiple local builds of any tool, allowing different versions from separate worktrees to be tested across the system.
*   **Ecosystem Orchestration**: Contains commands that operate across all projects in an ecosystem, including a parallel build system (`grove build`), a command runner (`grove run`), and a dependency-aware, stateful release engine (`grove release`).

<!-- DOCGEN:OVERVIEW:END -->

<!-- DOCGEN:TOC:START -->

See the [documentation](docs/) for detailed usage instructions:
- [Overview](docs/01-overview.md)
- [Installation](docs/02-installation.md)
- [Binary Management](docs/03-binary-management.md)
- [Ecosystems](docs/04-ecosystems.md)
- [Configuration](docs/05-configuration.md)
- [Command Reference](docs/06-command-reference.md)

<!-- DOCGEN:TOC:END -->
