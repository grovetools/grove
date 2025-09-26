# Grove Configuration Guide

Grove uses `grove.yml` files to define ecosystem structure and project metadata. This guide explains the configuration format, available options, and related files that control the Grove environment.

## Configuration Files Overview

Grove's configuration is distributed across several files, each with a distinct purpose.

| File | Location | Purpose |
| --- | --- | --- |
| `grove.yml` | Ecosystem Root | Defines the overall ecosystem and discovers projects using workspace patterns. |
| `grove.yml` | Project Directory | Defines metadata and build configuration for a single project (tool or library). |
| `registry.json` | `~/.grove/` | Tracks available tools for installation and management. |
| `active_versions.json` | `~/.grove/` | Manages the active release version for each installed tool. |
| `devlinks.json` | `~/.grove/` | Manages local development overrides for tool binaries. |

---

## Ecosystem Configuration (`grove.yml` at Root)

The root `grove.yml` file identifies a Grove ecosystem and specifies how to discover its member projects. Grove finds this file by searching upward from the current directory.

### Example Structure

```yaml
# ./grove.yml
name: acme-dev-tools
description: Development tools and libraries for the ACME team.
workspaces:
  - "tools/*"
  - "libs/core-*"
```

### Configuration Keys

#### `name`
-   **Type**: `string`
-   **Default**: None (Required)
-   **Description**: The name of the ecosystem. This should be a short, descriptive identifier.

#### `description`
-   **Type**: `string`
-   **Default**: None (Required)
-   **Description**: A human-readable description of the ecosystem's purpose.

#### `workspaces`
-   **Type**: `array` of `string`
-   **Default**: None (Required)
-   **Description**: An array of glob patterns that Grove uses to discover project directories. Each pattern is relative to the root `grove.yml`. A directory is only considered a workspace if it matches a pattern **and** contains its own `grove.yml` file.

---

## Project Configuration (`grove.yml` in Workspaces)

Each project (workspace) has its own `grove.yml` file that defines its specific metadata and configuration.

### Example Structure

```yaml
# ./tools/my-cli/grove.yml
name: my-cli
description: A command-line tool for managing widgets.
type: go
binary:
  name: my-cli
  path: bin/my-cli
```

### Configuration Keys

#### `name`
-   **Type**: `string`
-   **Default**: None (Required)
-   **Description**: The project's name. By convention, this should match the repository or directory name.

#### `description`
-   **Type**: `string`
-   **Default**: None (Required)
-   **Description**: A concise, one-line summary of the project's function.

#### `type`
-   **Type**: `string`
-   **Default**: `go`
-   **Description**: Specifies the project type, which informs Grove how to handle dependencies, builds, and releases.

##### Supported Project Types

| Type | Description | Dependency File |
| :--- | :--- | :--- |
| `go` | Standard Go applications or libraries. | `go.mod` |
| `maturin` | Python projects using Rust, managed by Maturin. | `pyproject.toml` |
| `node` | Node.js projects. (Support is experimental) | `package.json` |
| `template` | Project templates used by `grove add-repo`. | N/A |

#### `binary`
-   **Type**: `object`
-   **Default**: None (Optional)
-   **Description**: Defines the primary binary produced by a project. This is used by `grove dev` for local development linking. Omit this for library projects.
-   **Structure**:
    -   `name` (`string`, required): The final name of the executable.
    -   `path` (`string`, required): The build output path, relative to the project root.

    ```yaml
    binary:
      name: cx
      path: bin/cx
    ```

#### `binaries`
-   **Type**: `array` of `object`
-   **Default**: None (Optional)
-   **Description**: For projects that produce multiple binaries, this key defines each one.
-   **Structure**: Same as `binary`, but as a list.

    ```yaml
    binaries:
      - name: server
        path: bin/server
      - name: client
        path: bin/client
    ```

### Tool-Specific Extensions

Tools within the Grove ecosystem can use `grove.yml` for their own configuration by adding a top-level key. This allows for centralized project configuration.

**Example**: The `grove llm` command can be configured with a default model.

```yaml
# ./tools/my-agent/grove.yml
name: my-agent
description: An AI agent.
type: go
binary:
  name: my-agent
  path: bin/my-agent

# Tool-specific extension for grove-llm
llm:
  default_model: gpt-4o-mini
```

---

## Tool Registry (`registry.json`)

Grove uses a `registry.json` file in `~/.grove/` to track available tools for installation. While Grove ships with a default internal registry, this file can be customized.

### Example Structure

```json
{
  "tools": [
    {
      "name": "grove-context",
      "alias": "cx",
      "repository": "github.com/mattsolo1/grove-context",
      "binary": "cx",
      "version": "latest",
      "description": "Rule-based tool for dynamically managing file-based LLM context."
    }
  ]
}
```

-   **`name`**: The full, descriptive name of the tool.
-   **`alias`**: A short, convenient command for daily use.
-   **`repository`**: The Go module path or source for the tool.
-   **`binary`**: The name of the executable file.
-   **`version`**: The default version to install (e.g., `"latest"` or `"v0.2.1"`).
-   **`description`**: A brief summary of the tool's purpose.

---

## User Configuration (in `~/.grove/`)

The `~/.grove` directory stores user-specific configuration and state, managing the versions and binaries active on your system.

### `active_versions.json`

This file tracks the currently active *released* version for each installed tool. The `grove install` and `grove version use` commands modify this file.

**Example**:
```json
{
  "versions": {
    "cx": "v0.3.0",
    "flow": "v0.2.5",
    "grove": "v0.5.1"
  }
}
```

### `devlinks.json`

This file manages local development overrides. When you use `grove dev link`, you register a locally built binary. `grove dev use` activates it by creating a symlink in `~/.grove/bin` and updating this file. Development links always take precedence over released versions.

**Example**:
```json
{
  "binaries": {
    "flow": {
      "links": {
        "feature-branch": {
          "path": "/path/to/your/worktree/bin/flow",
          "worktree_path": "/path/to/your/worktree",
          "registered_at": "2023-10-27T10:00:00Z"
        }
      },
      "current": "feature-branch"
    }
  }
}
```

---

## Environment Variables

Grove's behavior can be modified with the following environment variables:

| Variable | Description | Default |
| :--- | :--- | :--- |
| `GROVE_DEBUG` | If set to `true`, enables verbose debug logging. | `false` |
| `GROVE_PAT` | A GitHub Personal Access Token used by `grove add-repo` to set up secrets in new private repositories. | (none) |

---

## Configuration Best Practices

-   **Naming Consistency**: Keep names consistent across the repository, directory, and `grove.yml` `name` field.
-   **Workspace Granularity**: Start with broad workspace patterns (e.g., `"*"`). As your ecosystem grows, organize projects into subdirectories (e.g., `tools/*`, `libs/*`) and refine the patterns.
-   **Versioning**: Manage project versions using Git tags, not the `version` key in `grove.yml`. The `grove release` command automates this process based on conventional commits.
-   **Binary Paths**: Use relative paths for `binary.path` (e.g., `bin/my-tool`). The Makefile in each project should be responsible for placing the compiled binary at this location.