**Objective:** Explain the features that make `grove-meta` an indispensable tool for managing a complex monorepo of interdependent projects.

**Content Breakdown:**

1.  **Defining an Ecosystem**
    *   Explain the concepts of a **Workspace** (a single project with a `grove.yml`) and an **Ecosystem** (a root directory with a `grove.yml` that defines `workspaces` using glob patterns).
    *   **Code Reference:** `pkg/workspace/discover.go`.

2.  **Ecosystem-Wide Visibility: The "Dashboard" Commands**
    *   Showcase the commands that provide an aggregated, "single pane of glass" view of the entire ecosystem.
    *   `grove ws status`: The main dashboard showing Git status, CI status, and PRs for every workspace.
        *   **Code Reference:** `cmd/workspace_status.go`.
    *   `grove ws plans`, `ws chats`, `ws current`: Highlight how `grove` aggregates data from other tools (`flow`, `nb`) across all workspaces, making it easy to see all active work in one place.
        *   **Code References:** `cmd/workspace_plans.go`, `cmd/workspace_chats.go`, `cmd/workspace_current.go`, and the underlying `pkg/aggregator/aggregator.go` pattern.

3.  **Release Engine**
    *   Explain how `grove release` orchestrates releases across interdependent projects.
    *   **The Dependency Graph:** Mention that `grove` builds a dependency graph (`pkg/depsgraph/builder.go`) to understand project relationships.
    *   **The Stateful Workflow:** Describe the `plan` -> `tui` -> `apply` workflow, which makes the release process predictable and recoverable.
    *   **The Interactive TUI:** Emphasize `grove release tui` (`cmd/release_tui.go`) as the primary interface for planning, approving versions, generating changelogs (with LLM support), and executing the release.
    *   **Automated Dependency Syncing:** Explain that during the release, `grove` automatically updates `go.mod` files of downstream projects to use the new versions of their upstream dependencies as they are released.
        *   **Code Reference:** The `orchestrateRelease` function in `cmd/release.go`.
