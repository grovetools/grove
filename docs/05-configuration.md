# Configuration

Grove uses a layered configuration system with YAML files. This allows for setting defaults at a high level and overriding them for specific projects.

## Configuration Files and Locations

Settings are managed through a hierarchy of files.

| File Location                | Purpose                                                                 | Managed By                         |
| :--------------------------- | :---------------------------------------------------------------------- | :--------------------------------- |
| `[ECOSYSTEM_ROOT]/grove.yml` | Defines the ecosystem, its workspaces, and global defaults for tools.   | User (manual edit)                 |
| `[WORKSPACE_DIR]/grove.yml`  | Defines a workspace and can override ecosystem-level settings.          | User (manual edit)                 |
| `~/.grove/active_versions.json` | Tracks the active released version for each installed tool.             | `grove install`, `grove version use` |
| `~/.grove/devlinks.json`     | Registers local development binaries from worktrees.                    | `grove dev link`, `grove dev use`    |
| `~/.grove/aliases.json`      | Stores user-defined custom aliases for tools.                           | `grove alias set`, `grove alias unset` |

## The `grove.yml` File

The `grove.yml` file is the primary configuration file for both ecosystems and individual workspaces.

### Ecosystem `grove.yml`

When placed at the root of a monorepo, this file defines the ecosystem.

**Key Fields:**

| Key           | Type       | Description                                                                    |
| :------------ | :--------- | :----------------------------------------------------------------------------- |
| `name`        | `string`   | The name of the ecosystem.                                                     |
| `description` | `string`   | A brief description of the ecosystem's purpose.                                |
| `workspaces`  | `[]string` | An array of glob patterns used to discover workspaces (e.g., `["*"]`). |

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

When placed within a workspace directory, this file defines the properties of that project.

**Key Fields:**

| Key           | Type     | Description                                                               |
| :------------ | :------- | :------------------------------------------------------------------------ |
| `name`        | `string` | The canonical name of the workspace (e.g., `grove-context`).              |
| `description` | `string` | A brief description of the workspace's purpose.                           |
| `type`        | `string` | The project type, used by the release engine (e.g., `go`, `maturin`). |
| `binary`      | `object` | Contains information about the binary produced by this workspace.         |
| `logging`     | `object` | Configures structured logging for the tool.                               |

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

Tools can define their own configuration blocks within `grove.yml`, identified by a key matching the tool's name (e.g., `flow`, `llm`).

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

### Local Override Configuration

For configuration values that should not be committed to version control (such as API keys, local paths, or developer-specific settings), Grove supports local override files.

**Supported Override Files:**

- `grove.override.yml` / `grove.override.yaml` - Local, uncommitted configuration
- `.grove.override.yml` / `.grove.override.yaml` - Hidden variant (prefix with `.` to hide from `ls`)

**Best Practice:**

Add override files to your project's `.gitignore` to prevent accidental commits of sensitive information:

```gitignore
# .gitignore

# Grove local configuration overrides
grove.override.yml
grove.override.yaml
.grove.override.yml
.grove.override.yaml
```

**Example Use Case:**

**`grove.yml` (committed to version control):**
```yaml
name: my-project
description: My awesome project

gemini:
  model: gemini-1.5-flash-latest
```

**`grove.override.yml` (git-ignored, local only):**
```yaml
# Local overrides - API keys and developer-specific settings
gemini:
  api_key: gsk_YourSecretApiKeyHere
  model: gemini-1.5-pro-latest

flow:
  chat_directory: /Users/me/personal-chats
```

In this example:
- The base configuration is in `grove.yml` (committed)
- Local overrides in `grove.override.yml` provide the API key and override the model
- The merged configuration will use the API key and model from `grove.override.yml`
- The `grove.override.yml` file is never committed to version control

## User-Level Configuration (`~/.grove/`)

The `~/.grove` directory stores user-specific configuration and state. These files are generally managed by `grove` commands.

-   **`active_versions.json`**: A JSON file mapping each tool's repository name to its active released version tag (e.g., `"grove-context": "v0.5.1"`). Managed by `grove version use`.
-   **`devlinks.json`**: A registry of locally-built binaries linked using `grove dev link`. It tracks binary paths and the active alias for each tool.
-   **`aliases.json`**: A map of repository names to custom aliases, which override default tool aliases. Managed by `grove alias`.

## Configuration Precedence

Grove resolves settings in a specific order. A setting from a higher level overrides one from a lower level.

1.  **Command-Line Flags** (Highest Priority)
2.  **Environment Variables**
3.  **Local Override Files** (`grove.override.yml`, `.grove.override.yml`)
4.  **Workspace `grove.yml`**
5.  **Ecosystem `grove.yml`**
6.  **Global Configuration** (`~/.config/grove/grove.yml`)
7.  **Application Defaults** (Lowest Priority)

**Note:** Within the configuration file hierarchy, the loading order is:
- Global config is loaded first (from `~/.config/grove/grove.yml`)
- Project config is merged on top (from `grove.yml`)
- Local override files are merged last (from `grove.override.yml` or `.grove.override.yml`)

## Environment Variables

Grove uses several environment variables to control its behavior.

| Variable               | Description                                                                                   |
| :--------------------- | :-------------------------------------------------------------------------------------------- |
| `GROVE_PAT`            | A GitHub Personal Access Token used by `grove add-repo` and `release` for private repository operations. |
| `GROVE_DEBUG`          | If set to `true`, enables verbose debug logging for all Grove tools.                          |
| `GROVE_WORKSPACE_ROOT` | (Set by `grove activate`) The absolute path to the active development workspace root.           |
| `GROVE_ORIGINAL_PATH`  | (Set by `grove activate`) A backup of the original `PATH` variable, used to deactivate a workspace.  |
| `EDITOR`               | The user's preferred command-line editor, used by commands like `cx edit`. Defaults to `vim`. |