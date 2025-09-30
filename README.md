<!-- DOCGEN:OVERVIEW:START -->

<img src="docs/images/grove-base-readme.svg" width="60%" />

`grove-cli` is the meta-command-line interface and package manager for the Grove ecosystem. It provides a unified entry point for installing, managing, and orchestrating a suite of specialized tools designed for AI-assisted software development.

<!-- placeholder for animated gif -->

### Key Features

*   **Tool Management**: Grove acts as a package manager and is responsible for installing, updating, and managing all other Grove tools, such as `grove-context`, `grove-flow`, and `grove-gemini`. It resolves inter-tool dependencies to ensure a consistent and functional toolset. Manages the entire lifecycle of Grove tools, including installation (`grove install`), updates (`grove update`), and versioning (`grove version use`). It supports downloading official releases or installing nightly builds from source.
*   **Unified Command Interface**: Acts as a command delegator. Running `grove cx stats` finds and executes the `cx` binary with the `stats` argument. This provides a single, consistent entry point for all tools.
*   **Local Development Support**: A `grove dev` command suite allows developers to link and switch between multiple local builds of any tool, making it easy to test different versions or feature branches across the entire system. The `grove activate` command provides explicit shell integration for development workspaces.
*   **Ecosystem Orchestration**: Provides high-level commands that operate across all workspaces in an ecosystem. This includes  aggregated status dashboards (e.g., `grove ws status`) and a dependency-aware release engine (`grove release`) that automates versioning, changelog generation, and CI monitoring.
*   **Aggregated Views**: Offerscommands like `grove logs`, `grove ws plans`, and `grove ws issues` that aggregate and display information from multiple tools across all projects in the ecosystem.

<!-- DOCGEN:OVERVIEW:END -->

<!-- DOCGEN:TOC:START -->

See the [documentation](docs/) for detailed usage instructions:
- [Introduction](docs/00-introduction.md)
- [Overview](docs/01-overview.md)
- [Installation](docs/02-installation.md)
- [Binary Management](docs/03-binary-management.md)
- [Ecosystems](docs/04-ecosystems.md)
- [Configuration](docs/05-configuration.md)
- [Command Reference](docs/06-command-reference.md)

<!-- DOCGEN:TOC:END -->
