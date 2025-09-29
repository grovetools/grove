# Managing Monorepos and Ecosystems

The `grove` CLI includes a suite of commands designed to manage complex monorepos, which are referred to as "Ecosystems" in the Grove terminology. These tools provide aggregated visibility, dependency-aware orchestration, and a structured release process for interdependent projects.

## Defining an Ecosystem

Grove distinguishes between two primary organizational units: the Workspace and the Ecosystem.

*   A **Workspace** is a directory representing a single, self-contained project or tool. It is identified by the presence of a `grove.yml` configuration file. 
*   An **Ecosystem** is a top-level directory, typically a Git monorepo, that contains multiple workspaces. The ecosystem is defined by a root-level `grove.yml` file that specifies which subdirectories are considered workspaces, often using glob patterns (e.g., `workspaces: ["*"]`). The logic for discovering these workspaces is handled by the `pkg/workspace/discover.go` package.
*   A **Development Workspace** is a temporary, isolated workspace created for developing a specific feature, usually backed by a Git worktree. It is identified by a `.grove-workspace` marker file, which allows `grove` to automatically prioritize locally-built binaries for that context.

## Dashboard Commands

Grove provides several commands that offer an aggregated view across every workspace within an ecosystem. 

*   **`grove ws status`**: Provides a comprehensive table summarizing the Git status, CI status for the main branch, and the status of your open pull requests for every workspace. This allows you to quickly assess the health and progress of the entire ecosystem. The implementation can be found in `cmd/workspace_status.go`.

*   **`grove ws plans`**, **`ws chats`**, and **`ws current`**: These commands aggregate information from other Grove tools across all workspaces. They allow you to see all active `grove-flow` plans, all ongoing `grove-flow` chats, or all "current" `grove-notebook` notes in a single, unified view. This makes it easy to track all active workstreams without having to navigate into each individual project directory. 

## The Release Engine

The `grove release` command provides a structured, stateful, and dependency-aware workflow for orchestrating releases across the entire ecosystem.

*   **Dependency Graph**: Before any release operation, `grove` builds an internal dependency graph of all workspaces by parsing their project files (e.g., `go.mod`, `pyproject.toml`). This graph, built by the logic in `pkg/depsgraph/builder.go`, allows `grove` to understand the relationships between projects and determine the correct release order.

*   **Stateful Workflow**: The release process is broken down into a predictable, recoverable, three-step workflow: `plan` -> `tui` -> `apply`.
    1.  `grove release plan`: Analyzes all repositories for changes and generates a release plan file.
    2.  `grove release tui`: Launches an interactive TUI to review, modify, and approve the plan.
    3.  `grove release apply`: Executes the approved plan.
    This stateful approach ensures that complex releases can be paused, reviewed, and resumed without losing context.

*   **Interactive TUI**: The `grove release tui` command (implemented in `cmd/release_tui.go`) is the central control panel for the release process. From this interface, you can review proposed version bumps, manually adjust them (major, minor, patch), generate changelogs with LLM assistance, write them to disk for final review, and approve each repository for release.

*   **Automated Dependency Syncing**: During the `apply` phase, `grove` uses the dependency graph to release projects in topological order. As each upstream dependency is tagged and released, `grove` automatically runs the necessary commands (e.g., `go get`) in downstream workspaces to update their dependency files to the newly released version before they are, in turn, released. This automated synchronization ensures that all tools in the ecosystem are released with the correct, up-to-date versions of their internal dependencies. 
