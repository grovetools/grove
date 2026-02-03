# Binary Management and Execution

The `grove` CLI acts as a single entry point for managing and executing a suite of specialized tools. It determines which binary to run based on the current context, enabling users to switch between globally installed releases and local development builds.

## The Meta-CLI Pattern

Grove uses two primary patterns to provide a unified interface: command delegation and aggregator facades.

### Command Delegation

`grove` is a command delegator. When a command like `grove cx stats` is executed, the `grove` binary finds the appropriate `cx` executable on the system and passes the `stats` argument to it. This allows each tool to be developed and versioned independently while providing a single command structure.

### Aggregator Facades

For certain high-level tasks, `grove` acts as an aggregator, providing a facade that orchestrates operations across multiple tools or workspaces.

*   **`grove logs`**: Discovers all workspaces in an ecosystem, finds their structured log files, and tails them into a single, aggregated stream.
*   **`grove llm`**: Provides a consistent set of flags for making requests to different Large Language Models, delegating the request to the correct provider-specific tool (`grove-gemini`, `grove-openai`, etc.) based on the specified model.

## Binary Resolution Precedence

`grove` determines which version of a tool to run based on a hierarchy of contexts.

1.  **Development Workspace**: If the current directory is within a Git worktree managed by Grove (identified by a `.grove-workspace` file), `grove` will prioritize using binaries built from source within that workspace (e.g., from its local `./bin` directory).

2.  **Global Fallbacks**: If not inside a development workspace, `grove` falls back to the globally managed binaries located in `~/.local/share/grove/bin`. This path contains symlinks that are managed by the versioning commands.

## Version Management Systems

Grove provides two distinct systems for managing the global set of tools that are used as fallbacks when not in a development workspace.

### Released Versions (`grove install` and `grove version`)

This system manages stable, released versions of tools downloaded from GitHub.

*   `grove install <tool>` downloads a specific, versioned release and stores it in `~/.local/share/grove/versions/`.
*   `grove version use <tool@version>` activates a specific downloaded version by updating a symlink in `~/.local/share/grove/bin`.

### Development Versions (`grove dev`)

This system manages locally-built, development versions from any directory on the filesystem. It is used for testing a development build outside of its specific workspace.

*   `grove dev link <path>` registers a binary built from a local source tree, making it available to the `dev` system.
*   `grove dev use <tool> <alias>` activates a registered development build, making it the global default for that tool.

## Explicit PATH Management (`grove activate`)

The `grove activate` command provides a mechanism to bring a development workspace's binaries into the current shell's `PATH`. This makes the binaries directly executable without the `grove` prefix, which is useful for integration with external scripts or IDEs.

**Example Usage:**

```bash
# Activate the current workspace's binaries for this shell session
eval "$(grove activate)"

# Deactivate and restore the original PATH
eval "$(grove activate --reset)"
```