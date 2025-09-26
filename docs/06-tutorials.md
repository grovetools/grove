# Grove Tutorials

This guide provides practical, step-by-step tutorials for common workflows within the Grove ecosystem. These tutorials cover setting up a new monorepo, developing tools, managing dependencies, and performing coordinated releases.

## Tutorial 1: Creating a New Polyglot Ecosystem

This tutorial demonstrates how to initialize a new Grove ecosystem (a monorepo) from scratch and add both Go and Python-based tools.

### Goal
- Initialize a new Grove ecosystem.
- Add multiple tools using different project templates.
- Verify the ecosystem's structure.

### Prerequisites
- `grove` CLI installed.
- `git` installed and configured.

### Step 1: Initialize the Ecosystem
First, create a new directory for your ecosystem and run the `workspace init` command.

```bash
mkdir my-ecosystem
cd my-ecosystem
grove workspace init --name "My Tools" --description "A collection of custom tools"
```
This command sets up the foundational files for a monorepo:
- `grove.yml`: The main configuration file for the ecosystem. It's pre-configured to recognize any subdirectory as a potential workspace.
- `go.work`: A Go workspace file, enabling seamless cross-module development for any Go projects you add.
- `Makefile`: A root Makefile for running commands across all workspaces.
- `.gitignore`: A standard set of ignore patterns for Go and general development.

### Step 2: Initialize the Git Repository
Initialize a Git repository to manage your new ecosystem.

```bash
git init
git add .
git commit -m "feat: initialize my-tools ecosystem"
```

### Step 3: Add a Go Tool
Now, add a new Go-based command-line tool to the ecosystem. The `--ecosystem` flag instructs `grove` to integrate it into the current monorepo structure.

```bash
grove add-repo my-go-analyzer --alias gza \
  --description "A code analysis tool written in Go" \
  --template go --ecosystem
```
This command automates several setup tasks:
1. Creates a new directory `my-go-analyzer/` with a standard Go project structure.
2. Initializes a Git repository within the new directory.
3. Adds the new tool as a Git submodule to the parent ecosystem repository.
4. Updates the root `go.work` file to include the new tool.

### Step 4: Add a Python/Rust Tool
Grove supports polyglot development. Let's add a tool using the `maturin` template for a Python project with Rust components.

```bash
grove add-repo data-importer --alias di \
  --description "A data import utility using Python and Rust" \
  --template maturin --ecosystem
```
This performs similar integration steps as the Go tool, setting up a project optimized for Python/Rust development.

### Step 5: Verify the Setup
Confirm that Grove recognizes your new workspaces and tools.

```bash
# List all discovered workspaces (subprojects)
grove workspace list

# List all tools and their status
grove list
```
The output will show your two new tools, `my-go-analyzer` and `data-importer`, as part of the ecosystem.

## Tutorial 2: Local Development Workflow

This tutorial covers an efficient workflow for developing tools within a Grove ecosystem, focusing on automatic binary management and the `grove dev` command suite for more advanced scenarios.

### Goal
- Understand Grove's automatic workspace-aware binary usage.
- Use `git worktree` for parallel development.
- Manage and switch between local development builds.

### Prerequisites
- An initialized Grove ecosystem with at least one tool.

### Step 1: Automatic Workspace Binary Usage
Grove's primary development feature is its workspace awareness. When you are inside a workspace directory, `grove` automatically prioritizes any binaries built within that workspace.

1.  **Navigate into your Go tool's directory:**
    ```bash
    cd my-go-analyzer
    ```
2.  **Build the tool:**
    ```bash
    make build
    ```
    This creates an executable at `./bin/my-go-analyzer`.
3.  **Run the tool via the `grove` dispatcher:**
    ```bash
    # Grove automatically finds and runs the local binary
    grove gza --version
    ```
For most day-to-day development, this is all you need. Grove finds the correct local binary without requiring you to modify your `PATH` or use symlinks.

### Step 2: Parallel Development with Git Worktrees
When working on multiple features or fixing a bug while a feature is in progress, `git worktree` is an effective pattern.

1.  **Create a new worktree for a feature:**
    ```bash
    # From within my-go-analyzer
    git worktree add ../my-go-analyzer-feature -b feature/new-parser
    cd ../my-go-analyzer-feature
    ```
2.  **Make changes and build in the new worktree:**
    ```bash
    # Make your code changes...
    make build
    ```
Now, `grove gza` will run the binary from the `my-go-analyzer-feature` directory because it's your current location. You can switch back to the `my-go-analyzer` directory to run the version on the `main` branch.

### Step 3: Global Development Builds with `grove dev`
Sometimes, you may want a specific local build to be available globally, regardless of your current directory. This is useful for testing a tool's integration with other projects. The `grove dev` commands manage this through a system of named "dev links".

1.  **Link your feature build:**
    From inside the `my-go-analyzer-feature` worktree, register your local build under an alias.
    ```bash
    # The `.` links all discoverable binaries from the current directory
    grove dev link . --as parser-feature
    ```
2.  **Activate the development version:**
    This command creates a symlink in `~/.grove/bin` that points to your local build.
    ```bash
    grove dev use gza parser-feature
    ```
3.  **Verify the activation:**
    Now, the `gza` command will point to your feature build, no matter which directory you are in.
    ```bash
    # Check from anywhere on your system
    which gza
    # Output: /home/user/.grove/bin/gza

    gza --version
    # Output: (your local build version)
    ```

### Step 4: Visual Management with the Dev TUI
To see all registered development links and manage them visually, use the `dev tui`.

```bash
grove dev tui
```
This interface allows you to:
- View all registered dev links for each tool.
- See which version is currently active (`*`).
- Switch between different linked versions.
- Install release versions and switch back and forth.

### Step 5: Return to Stable Versions
Once your testing is complete, you can easily switch back to the official released version of your tool.

```bash
# Switch a specific tool back to its release version
grove dev use gza --release

# Or, reset all tools to their release versions
grove dev reset
```

### Step 6: Clean Up
After your feature branch is merged, you can clean up the worktree and the associated dev link.

```bash
# From the main repository directory
cd ../my-go-analyzer
git worktree remove ../my-go-analyzer-feature

# Prune any dev links that now point to deleted paths
grove dev prune
```

## Tutorial 3: Ecosystem-Wide Operations

This tutorial highlights commands that operate across the entire ecosystem, providing aggregated views and enabling bulk actions.

### Goal
- Get a consolidated status report for all workspaces.
- View and manage dependencies across the ecosystem.

### Prerequisites
- A Grove ecosystem with multiple tools.

### Step 1: Get an Aggregated Status View
The `workspace status` command provides a dashboard view of every repository in your ecosystem.

```bash
grove workspace status
```
This command concurrently checks each workspace and displays a table with:
- **Git Status:** Current branch, dirty status, and ahead/behind counts.
- **CI Status:** The status of the latest build on the main branch.
- **Pull Requests:** Status of your open pull requests.
- **Context Size:** Information from `grove-context` (`cx`).
- **Release Status:** The latest tag and number of commits since the last release.

### Step 2: Manage Dependencies
Grove provides tools to manage Go dependencies across all your Go-based workspaces.

1.  **Visualize the dependency graph:**
    Understand how your tools depend on each other.
    ```bash
    grove deps tree
    ```
2.  **Update a shared dependency everywhere:**
    If you release a new version of a shared library (e.g., `grove-core`), you can bump it in all dependent projects with one command.
    ```bash
    grove deps bump github.com/mattsolo1/grove-core@v1.0.0 --commit
    ```
3.  **Sync all internal dependencies:**
    To ensure all tools are using the latest released versions of each other, use `deps sync`.
    ```bash
    grove deps sync --commit --push
    ```
    This command finds all internal Grove dependencies in each `go.mod` file, fetches their latest tagged versions, and updates the `go.mod` files accordingly.

## Tutorial 4: Performing a Coordinated Release

This tutorial guides you through Grove's dependency-aware release process, which uses an interactive TUI to plan and execute releases safely.

### Goal
- Plan a release for multiple tools.
- Generate LLM-powered changelogs.
- Execute a dependency-aware, level-by-level release.

### Prerequisites
- All repositories are on their main branch with no uncommitted changes.
- You have push access to the GitHub repositories.
- `gh` CLI is installed and authenticated.

### Step 1: Enforce Conventional Commits (Optional but Recommended)
To ensure meaningful changelogs, you can install a Git hook that validates commit messages.

```bash
# In the ecosystem root
grove workspace git-hooks install
```
This installs the `commit-msg` hook in the root repository and all submodule repositories.

### Step 2: Launch the Interactive Release Planner
The primary interface for releases is the `release tui`.

```bash
grove release --interactive
# or
grove release tui
```
This command scans all workspaces, calculates potential version bumps based on commit history, and presents a comprehensive planning interface.

### Step 3: Plan the Release in the TUI
Inside the TUI, you can perform the following actions:

- **Navigate:** Use `↑`/`↓` keys to move between repositories.
- **Select Repos:** Press `space` to select or deselect a repository for the release. Only repositories with changes since their last tag are selectable.
- **Adjust Version Bumps:** Use `m` (major), `n` (minor), or `p` (patch) to change the proposed semantic version bump.
- **Generate Changelogs:** Press `g` to generate a changelog for the selected repository using an LLM. This provides a high-quality summary of changes.
- **Write Changelogs:** Press `w` to write the generated changelog to the repository's `CHANGELOG.md` file. You can then press `e` to open it in your editor for manual refinement.
- **Approve:** Once you are satisfied with the version and changelog for a repository, press `a` to approve it.
- **Apply Release:** After approving all desired repositories, press `A` to execute the release.

### Step 4: The Release Process
Once you apply the release, `grove` orchestrates the process in dependency order:

1.  **Level 0 (No Dependencies):** Grove tags and pushes the repositories with no internal dependencies.
2.  **CI/CD Monitoring:** It then waits for the GitHub Actions release workflows for those repositories to complete successfully.
3.  **Dependency Updates:** Once the Level 0 artifacts are published, Grove moves to Level 1. It updates the `go.mod` files of Level 1 repositories to point to the new versions of their Level 0 dependencies, then commits and pushes the changes.
4.  **Tag and Release:** Grove then tags and pushes the Level 1 repositories, triggering their release workflows.
5.  This process continues level by level until all selected tools have been released.

This automated, dependency-aware process ensures that tools are always built against their correctly versioned dependencies.