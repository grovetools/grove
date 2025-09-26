# Grove-Meta: Ecosystem Orchestrator

The `grove` command, provided by the `grove-meta` repository, is the central meta-CLI and package manager for the Grove ecosystem. It is designed to orchestrate a distributed collection of specialized command-line tools, making them function as a single, cohesive development environment.

## Core Philosophy: A Composable Toolkit

The Grove ecosystem is built on the principle of modular, composable tools. Instead of a single monolithic application, the ecosystem consists of many focused CLIs, each with a specific responsibility:

-   `grove-context` (`cx`): Manages LLM context from local files.
-   `grove-flow`: Orchestrates complex, multi-step LLM jobs.
-   `grove-notebook` (`nb`): Provides a workspace-aware note-taking system.
-   `grove-tend`: Offers a framework for end-to-end testing.

The `grove` meta-CLI is the orchestrator that unifies these independent components. It provides a consistent interface for installation, command execution, and cross-project operations, making the distributed toolkit feel like an integrated system.

## Dual Role: Meta-CLI and Package Manager

The `grove` command serves two primary functions: it manages the installation and versions of other tools, and it delegates commands to the appropriate tool based on the current context.

### As a Package Manager

`grove` provides a complete suite of commands for managing the lifecycle of every tool in the ecosystem:

-   **Installation (`grove install`)**: Installs tools from GitHub releases into a managed directory (`~/.grove/versions`). It can install specific versions, the latest release, or even nightly builds compiled directly from source.
-   **Updates (`grove update`)**: Updates one or more tools to their latest versions.
-   **Discovery (`grove list`)**: Lists all available tools in the ecosystem and their current installation status.

This system centralizes binary management, placing symlinks to the active version of each tool in `~/.grove/bin`, which is added to the user's `PATH`.

### As a Meta-CLI and Command Delegator

When a command like `grove cx stats` is run, `grove` does not execute the logic itself. Instead, it intelligently delegates the `stats` command to the correct `cx` binary. This delegation follows a workspace-aware priority system:

1.  **Workspace-Local Binary**: It first checks if the current directory is part of a Grove workspace (identified by a `.grove-workspace` marker file). If so, it prioritizes using the locally compiled binary from that specific workspace (e.g., `./grove-context/bin/cx`).
2.  **Global Binary**: If not in a workspace, it falls back to the globally managed, symlinked binary in `~/.grove/bin`.

This mechanism is fundamental to the Grove development workflow, allowing developers to seamlessly switch between stable, globally installed tools for daily use and project-specific local builds when developing new features.

## Key Capabilities for Development Workflows

`grove` is designed to simplify complex, multi-repository development workflows through a series of specialized command suites.

### Workspace and Project Management

Grove defines an "ecosystem" as a monorepo containing multiple related Grove projects, known as "workspaces". The `grove.yml` file at the root of an ecosystem declares which subdirectories are considered workspaces. This structure enables cross-cutting operations:

-   `grove run <command>`: Executes a shell command across all discovered workspaces.
-   `grove ws status`: Displays an aggregated status dashboard, showing Git status, CI status, and other metrics for every workspace.
-   `grove ws init`: Initializes a new, well-structured Grove ecosystem from scratch.

### Version and Binary Management

`grove` provides precise control over tool versions, accommodating both production use and local development:

-   **Release Versions (`grove version use`)**: Switch the active global version of any tool to any previously installed release (e.g., `grove version use cx@v0.2.1`).
-   **Local Development (`grove dev`)**: The `dev` command suite allows developers to temporarily override released versions with local builds from source.
    -   `grove dev link <path>` registers a binary built from a local Git worktree.
    -   `grove dev use <tool> <alias>` activates the linked development version.
    -   `grove dev reset` deactivates all development versions, reverting to the stable releases.

### Dependency and Release Orchestration

Managing dependencies and coordinating releases across dozens of repositories is a primary function of `grove-meta`.

-   **Dependency Management (`grove deps`)**: The `deps` command suite maintains consistency across the ecosystem's Go modules.
    -   `grove deps sync`: Updates all internal Grove dependencies in every workspace to their latest versions.
    -   `grove deps tree`: Visualizes the inter-tool dependency graph.

-   **Release Management (`grove release`)**: The `release` command automates the complex process of releasing multiple interdependent tools. It builds a dependency graph and executes a leveled release, ensuring that core libraries are released before the tools that depend on them. The process can be managed through an interactive TUI (`grove release --interactive`) that facilitates version planning, changelog generation, and final approval.

## Ecosystem Integration

`grove-meta` is both the manager of and a component within the Grove ecosystem. Each tool is developed and tested independently, relying on a common `Makefile` contract for `build` and `test` targets. `grove-meta` leverages this contract to orchestrate cross-workspace operations.

This symbiotic relationship creates a virtuous cycle: `grove-meta` uses the `grove-tend` testing framework for its own end-to-end tests, while `grove-tend` is managed and released by `grove-meta`. This internal dogfooding ensures the entire system remains coherent and functional. By providing a unified management layer, `grove-meta` transforms a collection of individual CLIs into a true toolkit.