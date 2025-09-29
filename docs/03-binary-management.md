# Core Concepts: Binary Management

The `grove` CLI acts as a single entry point for managing and executing a suite of specialized tools. It determines which binary to run based on the current context, enabling users to switch between globally installed releases and local development builds.

## The Meta-CLI Pattern

Grove employs two primary patterns to provide a unified interface: command delegation and aggregator facades. 

### Command Delegation

At its core, `grove` is a **command delegator**. When you execute a command like `grove cx stats`, the `grove` binary does not contain the logic for `cx` itself. Instead, it finds the appropriate `cx` executable on your system and passes the `stats` argument to it. This allows each tool to be developed and versioned independently while providing the user with a single, consistent command structure.


### Aggregator Facades

For certain high-level tasks, `grove` acts as an **aggregator**, providing a facade that orchestrates operations across multiple tools or workspaces. Commands like `grove logs` and `grove llm` offer a unified interface to complex underlying systems:

*   **`grove logs`**: Discovers all workspaces in an ecosystem, finds their structured log files, and tails them into a single, aggregated stream.
*   **`grove llm`**: Provides a consistent set of flags for making requests to different Large Language Models, delegating the request to the correct provider-specific tool (`grove-gemini`, `grove-openai`, etc.) based on the specified model.

## Version Management

Grove also provides two distinct command suites for explicitly managing the global set of tools.

### `grove install` and `grove version`

This system manages stable, **released** versions of tools downloaded from GitHub.

*   `grove install <tool>` downloads a specific, versioned release and stores it in a dedicated directory within `~/.grove/versions/`.
*   `grove version use <tool@version>` activates a specific downloaded version by updating the symlink in `~/.grove/bin`.

This is the standard way to manage official tool releases for day-to-day use.

### `grove dev`

This system manages locally-built, **development** versions from any directory on your filesystem. It is designed for when you need to test a development build outside of its specific workspace.

*   `grove dev link <path>` registers a binary built from a local source tree, making it available to the `dev` system.
*   `grove dev use <tool> <alias>` activates a registered development build, making it the global default for that tool.

### `grove activate`

The `grove activate` command provides a mechanism to bring a development workspace's binaries into your shell's `PATH`. 

This is useful for more complex scenarios, such as when tools need to call each other directly (without the `grove` prefix) or for integrating with external scripts and IDEs that need direct access to the binaries.

**Example Usage:**

```bash
# Activate the current workspace's binaries for this shell session
eval "$(grove activate)"

# Deactivate and restore the original PATH
eval "$(grove activate --reset)"
```
