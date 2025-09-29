# Configuration

The Grove ecosystem uses a layered configuration system based on YAML files, allowing for both global defaults and project-specific overrides. Configuration is primarily managed through `grove.yml` files, which can exist at both the ecosystem (monorepo) root and within individual workspace directories.

## Configuration Files and Locations

Grove uses a hierarchy of configuration files to manage settings.

| File Location                    | Purpose                                                                   | Managed By                                 |
| :------------------------------- | :------------------------------------------------------------------------ | :----------------------------------------- |
| `[ECOSYSTEM_ROOT]/grove.yml`     | Defines the ecosystem, its workspaces, and global defaults for tools.     | User (manual edit)                         |
| `[WORKSPACE_DIR]/grove.yml`      | Defines a specific workspace, its properties, and overrides global settings. | User (manual edit)                         |
| `~/.grove/active_versions.json`  | Tracks the active released version for each installed tool.               | `grove install`, `grove version use`         |
| `~/.grove/devlinks.json`         | Registers local development binaries from different worktrees.            | `grove dev link`, `grove dev use`            |
| `~/.grove/aliases.json`          | Stores user-defined custom aliases for tools.                             | `grove alias set`, `grove alias unset`       |

## The `grove.yml` File

The `grove.yml` file is the primary configuration file for both ecosystems and individual workspaces.

### Ecosystem `grove.yml`

When placed at the root of a monorepo, this file defines the entire ecosystem.

**Key Fields:**

| Key           | Type     | Description                                                                                                                              |
| :------------ | :------- | :--------------------------------------------------------------------------------------------------------------------------------------- |
| `name`        | `string` | The name of the ecosystem.                                                                                                               |
| `description` | `string` | A brief description of the ecosystem's purpose.                                                                                          |
| `workspaces`  | `[]string` | An array of glob patterns that `grove` uses to discover the workspaces within the ecosystem (e.g., `["*"]` or `["tools/*", "libs/*"]`). |

**Example Ecosystem `grove.yml`:**

```yaml
# ./grove.yml
name: grove-ecosystem
description: A collection of AI-assisted development tools.
workspaces:
  - "*" # Discover all subdirectories with a grove.yml as workspaces

# Global tool-specific settings can be defined here
flow:
  oneshot_model: gemini-2.5-pro
```

### Workspace `grove.yml`

When placed within a workspace directory, this file defines the properties of that specific project.

**Key Fields:**

| Key           | Type     | Description                                                                                             |
| :------------ | :------- | :------------------------------------------------------------------------------------------------------ |
| `name`        | `string` | The canonical name of the workspace (e.g., `grove-context`).                                            |
| `description` | `string` | A brief description of the workspace's purpose.                                                         |
| `type`        | `string` | The project type, used by the release engine. (e.g., `go`, `maturin`, `template`). Defaults to `go`.      |
| `binary`      | `object` | Contains information about the binary produced by this workspace.                                       |
| `logging`     | `object` | Configures structured logging for the tool.                                                             |

**Example Workspace `grove.yml`:**

```yaml
# ./grove-context/grove.yml
name: grove-context
description: Rule-based tool for managing file-based LLM context.
type: go

binary:
  name: cx
  path: ./bin/cx

logging:
  file:
    enabled: true
    format: json

# Workspace-specific overrides for tool configurations
flow:
  chat_directory: ./dev-chats
```

### Tool-Specific Configuration

Tools within the Grove ecosystem can define their own configuration blocks within `grove.yml`. These blocks are identified by a key matching the tool's name (e.g., `flow`, `llm`). This allows for centralized and version-controlled configuration for all tools.

**Example `flow` configuration block:**

```yaml
# ./.grove/config.yml or ./grove.yml
flow:
  plans_directory: ./plans
  chat_directory: ./chats
  oneshot_model: gemini-2.5-pro
  target_agent_container: grove-agent-ide
  summarize_on_complete: true
```

## User-Level Configuration (`~/.grove/`)

The `~/.grove` directory stores configuration and data that is specific to your user account and applies across all projects. These files are typically managed by `grove` commands and should not be edited manually.

-   **`active_versions.json`**: A JSON file mapping each tool's repository name to its currently active released version tag (e.g., `"grove-context": "v0.5.1"`). Managed by `grove version use`.
-   **`devlinks.json`**: A registry of all locally-built binaries you have linked using `grove dev link`. It tracks the binary paths and which one is currently active for each tool.
-   **`aliases.json`**: A simple map of repository names to custom aliases, allowing you to override default aliases (e.g., change `cx` to `ctx`). Managed by `grove alias`.

## Configuration Precedence

Grove resolves configuration settings using a clear order of precedence. A setting from a higher level will always override one from a lower level.

1.  **Command-Line Flags** (Highest Priority): Flags passed directly to a command (e.g., `grove release --dry-run`).
2.  **Environment Variables**: System environment variables (e.g., `GROVE_PAT`).
3.  **Workspace `grove.yml`**: Settings defined in the `grove.yml` of the current workspace directory.
4.  **Ecosystem `grove.yml`**: Settings from the root `grove.yml` in a monorepo.
5.  **User-Level Configuration**: Files within `~/.grove/` that store user-wide state.
6.  **Application Defaults** (Lowest Priority): Hardcoded default values within the tools themselves.

## Environment Variables

Grove uses several environment variables to control its behavior:

| Variable               | Description                                                                                                   |
| :--------------------- | :------------------------------------------------------------------------------------------------------------ |
| `GROVE_PAT`            | A GitHub Personal Access Token used by `grove add-repo` and `release` for private repository operations.        |
| `GROVE_DEBUG`          | If set to `true`, enables verbose debug logging for all Grove tools.                                          |
| `GROVE_WORKSPACE_ROOT` | (Set by `grove activate`) The absolute path to the active development workspace root.                           |
| `GROVE_ORIGINAL_PATH`  | (Set by `grove activate`) A backup of the original `PATH` variable, used to deactivate a workspace.             |
| `EDITOR`               | The user's preferred command-line editor, used by commands like `cx edit`. If not set, defaults to `vim`. |