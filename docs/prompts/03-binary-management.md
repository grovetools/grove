### Phase 1: Revise the Documentation Prompt for Core Concepts

We will update `docs/prompts/04-command-reference.md` to request a document with the following structure and content.

#### Section 1: Grove as a Smart Binary Manager

**Objective:** Explain how `grove` acts as a single entry point for managing and executing a suite of tools, handling different versions and contexts.

**Content Breakdown:**

1.  **Meta-CLI Pattern:**
    *   **Delegation:** Start by explaining that `grove` is a **command delegator**. When you run `grove cx stats`, `grove` finds the `cx` binary and passes the `stats` argument to it. 
        *   **Code Reference:** `cmd/root.go` (`delegateToTool` function).
    *   **Facades:** Explain that `grove` is also an **aggregator**. High-level commands like `grove logs` and `grove llm` act as facades, providing a single, consistent interface for tasks that span multiple tools or workspaces (e.g., streaming logs from all services at once).
        *   **Code References:** `cmd/logs.go`, `cmd/llm.go`.

2.  **Workspace-Aware Execution: **
    *   **Goal:**  Explain how `grove` determines which version of a tool to run.
    *   **The Context Hierarchy (in order of precedence):**
        1.  **Development Workspace :** If your current directory is inside a git worktree managed by Grove (identified by a `.grove-workspace` file), `grove` can be configured to use from source inside that workspace (e.g., from its local `./bin` directory). This enables a frictionless development loop: build your tool, and it's immediately the one that gets used in that context.
            *   **Code Reference:** `cmd/root.go` (`findWorkspaceRoot` function).
        2.  **Global Fallbacks:** If you are *not* inside a development workspace, `grove` falls back to the globally managed binaries in `~/.grove/bin`. You can set any development repo or worktree to be the globally active binary, for testing.

3.  **Explicit Version Management: `grove dev` vs. `grove install`**
    *   **Goal:** Clarify the two systems for managing global binaries.
    *   **`grove install`/`version`:** Manages stable, **released** versions downloaded from GitHub. These are stored in `~/.grove/versions/`.
    *   **`grove dev`:** Manages locally-built **development** versions from any directory on your machine. This is for when you want to use a development build *outside* of its specific workspace. `grove dev link` registers a build, and `grove dev use` makes it the global default.
    *   **`grove activate`:** Explain that this command (`cmd/activate.go`) provides an *explicit* way to bring a development workspace's binaries into your shell's `PATH`, making them directly executable without the `grove` prefix. This is useful for tools that call each other or for integrating with other scripts.
