# Managing Monorepos and Ecosystems

The `grove` CLI includes commands for managing interdependent projects within a single monorepo, which is termed an "Ecosystem". These commands provide aggregated views and a dependency-aware release process.

## Defining an Ecosystem

Grove operates on two organizational units: the Workspace and the Ecosystem.

*   A **Workspace** is a directory that represents a single project and is identified by the presence of a `grove.yml` configuration file.
*   An **Ecosystem** is a top-level directory containing multiple workspaces. It is defined by a root `grove.yml` file that includes a `workspaces` field with glob patterns (e.g., `workspaces: ["*"]`) for locating the individual project directories. The discovery logic is implemented in `pkg/workspace/discover.go`.

## Aggregated Views

Several `grove workspace` (aliased as `ws`) commands aggregate information from every workspace in an ecosystem.

*   **`grove ws status`**: Outputs a table summarizing the Git status, CI status for the main branch, and the status of open pull requests for each workspace. This is implemented in `cmd/workspace_status.go`.
*   **`grove ws plans`**, **`ws chats`**, and **`ws current`**: These commands gather and display data from other Grove tools. They list all active `grove-flow` plans, all ongoing `grove-flow` chats, or all "current" `grove-notebook` notes in a single view. The aggregation mechanism is defined in `pkg/aggregator/aggregator.go`.

## The Release Engine

The `grove release` command orchestrates releases across all workspaces in an ecosystem through a structured, multi-step process.

*   **Dependency Graph**: Before a release, `grove` builds a dependency graph by parsing project files (e.g., `go.mod`, `pyproject.toml`) in each workspace. This graph determines the correct build and release order for interdependent projects. The logic is located in `pkg/depsgraph/builder.go`.

*   **Stateful Workflow**: The release process is composed of three commands: `plan`, `tui`, and `apply`.
    1.  `grove release plan`: Analyzes repositories for changes and generates a release plan file.
    2.  `grove release tui`: Launches a terminal user interface to review, modify, and approve the generated plan.
    3.  `grove release apply`: Executes the approved plan.
    This separation allows a release to be reviewed or paused without losing its state.

*   **Interactive TUI**: The `grove release tui` command provides an interface for managing the release plan. From the TUI, a user can review proposed version bumps, change the bump level (major, minor, patch), generate changelogs with LLM assistance, write changelogs to disk for final review, and approve each repository for release. This is implemented in `cmd/release_tui.go`.

*   **Dependency Synchronization**: During the `apply` phase, `grove` processes repositories in the order determined by the dependency graph. After an upstream workspace is tagged and its release is published, the orchestrator executes commands (e.g., `go get`) in downstream workspaces to update their dependency files to the new version before they are subsequently tagged and released. This is handled by the `orchestrateRelease` function in `cmd/release.go`.