The Grove toolkit is a set of command-line tools for AI-assisted coding, designed primarily for development within a monorepo. It provides orchestration layers and developer utilities to make large language models (LLMs) more effective as coding partners.

The central question behind Grove is: How do we make software development with AI agents a more rational, predictable, and effective process?

Our answer is a local-first, editor-independent system built on two foundations: plain text and specialized CLI tools. Plain text (primarily Markdown) serves as a flexible, portable medium for planning, logging, and orchestrating work. A suite of small, independent CLI tools provides the mechanisms to manage code, context, and workflows.

This approach keeps the developer in control, avoids editor lock-in, and promotes a modular, extensible ecosystem.

`grove` is the meta-command-line interface and package manager for the Grove ecosystem. It provides a unified entry point for installing, managing, and orchestrating a suite of specialized tools.

## Core Assumptions & Workflow

Grove is built on several key assumptions about developing software with AI:

1.  **Use the Right LLM tool for the Job**: Different models and tools excel at different tasks. Our typical workflow involves using a model like Google's Gemini API for high-level planning and analysis across large codebases, then feeding those plans to a model like Anthropic's Claude Code for focused code generation and implementation. This "Plan -> Agent -> Review -> Agent" cycle yields more consistent and successful outcomes.

2.  **Monorepos and Workspaces are Effective**: LLMs perform better on smaller, more focused codebases. By organizing projects in a monorepo, we can manage dependencies effectively while allowing agents to operate on individual, self-contained workspaces. This structure is managed by a suite of grove commands for viewing status, managing dependencies, and orchestrating releases across the entire ecosystem.

3.  **Parallel Development in Isolated Environments is Key**: Working on multiple features in parallel across different projects or branches is an effective way to manage complexity. The main drawback is the cognitive overhead of context-switching. Grove mitigates this by embracing Git worktrees as a primary development construct, with tools to create, manage, and quickly switch between these isolated environments.

4.  **Plain Text is the Best Interface**: Markdown is used as the primary medium for planning, agent instructions, and logging. It's portable, versionable, and can be consumed by both humans and LLMs. This creates a durable, high-level record of development activity that lives alongside the code but outside the ephemeral context of a single chat session.

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
