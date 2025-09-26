# Grove Core Concepts

This document explains the fundamental concepts and architectural patterns of the Grove ecosystem. Understanding these principles is key to effectively using, developing, and managing tools within a Grove-managed monorepo or collection of repositories.

## 1. The Meta-CLI and Tool Delegation

Grove's architecture is centered around the **meta-CLI pattern**. The `grove` command itself does not contain the core logic for most tools. Instead, it acts as an intelligent orchestrator and command dispatcher that delegates tasks to independent, specialized tool binaries.

### How Delegation Works

When you execute a command like `grove cx stats`, the following process occurs:

1.  **Reception**: The `grove` meta-CLI receives the command `cx stats`.
2.  **Resolution**: It identifies `cx` as the tool and `stats` as its argument. It resolves the alias `cx` to the tool name `grove-context`.
3.  **Binary Location**: Grove determines which `cx` binary to execute based on a prioritized layering system (see Version Management). It checks for workspace-specific binaries, global development links, and finally, installed release versions.
4.  **Execution**: Grove executes the located binary, passing along the remaining arguments (`stats`). Standard input, output, and error streams are transparently passed through.

This pattern provides a unified entry point (`grove`) while keeping each tool as a separate, maintainable, and independently releasable binary.

## 2. Workspaces and Ecosystems

Workspaces are the primary organizational unit in Grove. They allow the system to discover projects and apply commands across them in a coordinated manner.

### What is a Workspace?

A **workspace** is any directory that contains a `grove.yml` configuration file. This file defines project metadata, such as its name, type, and associated binaries.

An **ecosystem** is a collection of workspaces, typically managed within a single monorepo. The root of the ecosystem is defined by a root `grove.yml` file that contains a `workspaces` directive.

### Workspace Discovery

Grove discovers projects using a simple but effective mechanism:

1.  Starting from the current directory, Grove searches upwards for a `grove.yml` file with a `workspaces` directive. This directory is considered the **ecosystem root**.
2.  The `workspaces` directive contains a list of glob patterns that Grove uses to find all individual project workspace directories.

**Example root `grove.yml`:**
```yaml
# ~/grove-ecosystem/grove.yml
name: grove-ecosystem
description: The Grove CLI toolkit ecosystem
workspaces:
  - "grove-*"           # Matches all directories at the root starting with "grove-"
  - "libs/*"            # Matches all directories inside libs/
```
Each discovered directory (e.g., `~/grove-ecosystem/grove-context`) must also contain its own `grove.yml` to be considered a valid workspace.

## 3. Project Types and Polyglot Support

While originally designed for Go, Grove is a polyglot system that supports multiple project types. This is achieved through a pluggable handler system defined in each workspace's configuration.

### Supported Project Types

-   `go` (default): Standard Go modules with a `go.mod` file.
-   `maturin`: Python/Rust hybrid projects using `pyproject.toml`.
-   `node`: JavaScript/TypeScript projects with `package.json`.
-   `template`: Project templates that are not meant to be built directly but are used by `grove add-repo`.

### Project Handlers

Each project `type` is associated with a handler that understands its specific conventions for:
-   Parsing dependencies (e.g., from `go.mod` or `pyproject.toml`).
-   Running builds and tests (typically by invoking `make`).
-   Managing versioning.

This is configured in the workspace's `grove.yml`:
```yaml
# ~/grove-ecosystem/grove-py-tool/grove.yml
name: grove-py-tool
type: maturin  # Instructs Grove to use the Maturin handler
binary:
  name: pytool
  path: ./target/release/pytool
```

## 4. Layered Version Management

Grove features a multi-layered versioning system that cleanly separates production-ready releases from local development builds. This allows developers to work on new features without disrupting the stability of their installed tools. The system resolves which binary to use based on the following priority:

#### Layer 1: Workspace-Active Binaries (Highest Priority)

This is the most common development model. When you are inside a directory structure marked as a workspace (containing a `.grove-workspace` file), Grove **automatically** prioritizes binaries built from that workspace's source code.

-   **Activation**: Automatic. Simply `cd` into the workspace.
-   **Mechanism**: The `grove` command dispatcher detects the `.grove-workspace` file in a parent directory and prepends the workspace's binary directories to the `PATH` for the duration of the command.
-   **Use Case**: Standard feature development. A developer checks out a branch, builds the tool with `make build`, and Grove immediately uses that new binary for any subsequent commands within that directory tree. The `grove dev workspace` and `eval "$(grove activate)"` commands provide insight and control over this context.

#### Layer 2: Global Development Overrides

For situations where a specific development version needs to be active globally (outside of any specific workspace), you can use `grove dev link`.

-   **Activation**: Manual, using `grove dev use <tool> <alias>`.
-   **Mechanism**: Creates a symlink in `~/.grove/bin` pointing to your local build. This state is tracked in `~/.grove/devlinks.json`.
-   **Use Case**: Testing a core library change (`grove-core`) that affects multiple tools across different workspaces, or testing a tool's integration with external systems.

#### Layer 3: Released Versions (Default)

This is the baseline layer, consisting of stable, versioned releases installed from GitHub.

-   **Activation**: Managed by `grove install` and `grove version use`.
-   **Mechanism**: Binaries are stored in `~/.grove/versions/<version>/bin/`. The active version for each tool is tracked in `~/.grove/active_versions.json`, and `~/.grove/bin` contains symlinks pointing to the active binaries.
-   **Use Case**: Everyday use of stable Grove tools.

This layered approach ensures that development work is safely isolated but easily activated, providing a seamless transition between using stable tools and testing new code.

## 5. Dependency Management

The `grove deps` command suite provides tools to manage dependencies across all Go projects within the ecosystem, ensuring consistency and simplifying large-scale refactoring.

### Dependency Graph

Grove builds a complete dependency graph of all workspaces by analyzing their `go.mod` and `pyproject.toml` files. This graph is crucial for:
-   Visualizing dependencies with `grove deps tree`.
-   Detecting circular dependencies.
-   Determining the correct build and release order.

### Key Commands

-   `grove deps sync`: Updates all internal Grove dependencies (`github.com/mattsolo1/*`) across all projects to their latest available versions. It runs `go get` and `go mod tidy` and can optionally commit the changes.
-   `grove deps bump <module>@<version>`: Bumps a *single* specified dependency to a specific version across all projects that use it.

## 6. Release Orchestration

The `grove release` command automates the complex process of creating versioned releases for multiple interdependent projects.

### Dependency-Aware Release Order

The release process is orchestrated in **levels** based on the dependency graph.
-   **Level 0** projects (e.g., `grove-core`) with no internal dependencies are released first.
-   Grove then waits for their CI/CD pipelines to complete and for the new module versions to become available.
-   **Level 1** projects, which depend on Level 0 projects, have their dependencies updated to the newly released versions.
-   Level 1 projects are then tagged and released.
-   This process continues level by level until the entire ecosystem is released.

### Interactive Release TUI

For a guided and auditable release process, Grove provides an interactive terminal UI via `grove release tui` (or `grove release --interactive`). The TUI allows a release manager to:
-   Review all repositories with pending changes.
-   See LLM-suggested version bumps and justifications.
-   Select the final version bump (major, minor, patch) for each repository.
-   Preview, edit, and approve generated changelogs.
-   Monitor Git and CI status.
-   Execute the approved release plan with a single command.