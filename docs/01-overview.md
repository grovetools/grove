`grove` is a meta-CLI that manages the lifecycle of Grove tools, delegates commands, and orchestrates actions across ecosystem projects.

### Core Features

*   **Tool Lifecycle Management**: `install`, `update`, `version`, and `list` commands manage tool binaries. It supports installation from GitHub releases by version tag, `latest`, `nightly` pre-releases, or building from `source`. It also resolves and installs inter-tool dependencies.
*   **Local Development Overrides**: The `dev` command suite (`link`, `use`, `cwd`, `point`) registers and manages symlinks to local binaries built from different Git worktrees, enabling system-wide or workspace-specific overrides of released tool versions.
*   **Command Delegation**: Acts as a facade for all ecosystem tools. Running `grove cx stats` locates and executes the `cx` binary. It uses a layered approach, prioritizing workspace-specific overrides (`.grove/overrides.json`), then workspace-local binaries, and finally globally installed versions.
*   **Ecosystem Orchestration**:
    *   `build`: A parallel build runner with a terminal interface (TUI) that respects `build_after` dependencies defined in project configurations.
    *   `run`: Executes a shell command across all discovered projects within the current context.
    *   `deps`: Manages Go module dependencies (`bump`, `sync`, `tree`) across all projects in an ecosystem.
*   **Stateful Release Workflow**: The `release` command suite (`plan`, `tui`, `apply`) provides a dependency-aware release process. It includes version calculation, optional LLM-based changelog generation, CI monitoring, and stateful execution with rollback capabilities.
*   **Repository Management**: `repo add` and `ecosystem init` commands create new standalone repositories or monorepo ("ecosystem") structures from templates.
*   **Unified LLM Interface**: `llm request` acts as a single entry point for LLM interactions, delegating prompts to the appropriate provider-specific tool (e.g., `grove-gemini`, `grove-openai`) based on the model name specified in flags or configuration.
