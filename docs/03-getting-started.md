# Getting Started with Grove

This guide provides a step-by-step introduction to setting up and using Grove. You will learn how to create a new development ecosystem, add your first project, and use Grove's core commands to manage your workflow.

## Prerequisites

Before you begin, ensure the following are installed on your system:
- The `grove` CLI. (See the main [README.md](README.md#installation) for installation instructions).
- Git
- Go (version 1.24 or later)
- The GitHub CLI (`gh`) and authenticated with `gh auth login`.

Verify that `~/.grove/bin` is in your shell's `PATH` and that you can run `grove version` successfully.

## Step 1: Create a New Grove Ecosystem

A Grove "ecosystem" is a container for a set of related tools and projects, managed in a monorepo-like structure. Your first step is to initialize a new ecosystem.

1.  Create and navigate to a new directory for your ecosystem:
    ```bash
    mkdir my-ecosystem
    cd my-ecosystem
    ```

2.  Initialize the ecosystem using the `grove` CLI:
    ```bash
    grove workspace init
    ```
    This command, aliased as `grove ws init`, sets up the foundational files for your ecosystem:
    - `grove.yml`: The main configuration file for the ecosystem.
    - `go.work`: A Go workspace file for managing multiple Go modules.
    - `Makefile`: A template for build automation across all projects.
    - `.gitignore`: Standard ignore patterns for Grove projects.
    - `.git/`: Initializes the ecosystem as a Git repository.

## Step 2: Add Your First Project

An ecosystem is composed of individual projects, each residing in its own subdirectory and managed as a Git submodule. The `add-repo` command simplifies creating and integrating a new project.

1.  From the root of `my-ecosystem`, run the following command:
    ```bash
    grove add-repo my-first-tool --alias mft --ecosystem
    ```
    This command uses the default Go project template to:
    - Create a new directory named `my-first-tool`.
    - Populate it with a standard project structure, including a `main.go`, `Makefile`, and `grove.yml`.
    - Initialize it as its own Git repository with an initial commit.
    - Add `my-first-tool` as a Git submodule to the parent `my-ecosystem` repository.
    - Update the root `go.work` file to include the new Go module.

## Step 3: Understand Your Workspace

Grove provides commands to inspect the state of your ecosystem and your current context within it.

1.  Get a high-level overview of all projects in your ecosystem:
    ```bash
    grove workspace status
    ```
    This command, aliased as `grove ws status`, displays a dashboard with information about each project, including its Git status, branch, and CI status.

2.  Check your current workspace context:
    ```bash
    grove dev workspace
    ```
    This command confirms whether you are inside a Grove workspace and lists the binaries it provides. A workspace is any directory containing a `.grove-workspace` file, which enables automatic context-aware behavior.

## Step 4: Build Your Project

Grove projects follow a convention of using Makefiles for common tasks like building, testing, and cleaning.

1.  Navigate into your new project's directory:
    ```bash
    cd my-first-tool
    ```

2.  Build the project's binary:
    ```bash
    make build
    ```
    This command compiles the Go source and places the executable binary in the project's local `bin/` directory. For `my-first-tool`, the binary will be at `bin/mft`. This is a local development binary, distinct from officially released versions managed by Grove's installer.

## Step 5: Activate Workspace Binaries

A key feature of Grove is its ability to prioritize and use binaries built directly from your local source code. The `grove activate` command modifies your shell's `PATH` to make this possible.

1.  Run the activation command. It must be evaluated by your shell:
    ```bash
    eval "$(grove activate)"
    ```
    This command prepends the `bin` directories of all projects within your current workspace to your `PATH`. It also sets the `GROVE_WORKSPACE_ROOT` environment variable, which signals to Grove and its tools that you are in an active workspace session.

2.  Verify that the local binary is now in your `PATH`:
    ```bash
    which mft
    # Expected output: /path/to/my-ecosystem/my-first-tool/bin/mft
    ```
    Now, when you run `mft`, you are executing the version you just built from source.

3.  To deactivate the workspace and return to your standard `PATH`, run:
    ```bash
    eval "$(grove activate --reset)"
    ```

4.  **Recommended:** For convenience, add aliases to your shell's configuration file (e.g., `~/.zshrc`, `~/.bashrc`):
    ```bash
    alias gwa='eval "$(grove activate)"'
    alias gwd='eval "$(grove activate --reset)"'
    ```

## Step 6: Run Commands Across the Ecosystem

The `grove run` command is a powerful tool for executing a command across every project within your ecosystem.

1.  Navigate back to the ecosystem root:
    ```bash
    cd ..
    ```

2.  Run `git status` in every project repository:
    ```bash
    grove run git status
    ```
    Grove will iterate through each submodule and execute the command, displaying the output grouped by workspace.

3.  You can also run Makefile targets across all projects:
    ```bash
    grove run make test
    ```

## Step 7: Install and Manage Global Grove Tools

In addition to managing projects you create, `grove` also functions as a package manager for the official suite of Grove tools. These tools are installed globally within the `~/.grove` directory.

1.  List all available official tools and their installation status:
    ```bash
    grove list
    ```

2.  Install all available tools with a single command:
    ```bash
    grove install all
    ```
    This command downloads the latest released versions of tools like `grove-context` (`cx`), `grove-flow` (`flow`), and `grove-notebook` (`nb`), making them available globally.

3.  Keep all your installed tools up-to-date:
    ```bash
    grove update all
    ```

4.  Update the `grove` CLI itself:
    ```bash
    grove self-update
    ```

When you run a command like `cx`, your shell first looks for it in the workspace-activated path (if active). If not found, it falls back to the globally installed version in `~/.grove/bin`. This layering allows you to seamlessly switch between stable releases and local development versions of any tool.

## Next Steps

You have now created a Grove ecosystem, added a project, and learned the basic commands for building, testing, and managing your workspace.

To continue, explore the following topics:
- **Local Development:** Learn how to work on multiple versions of a tool using `grove dev link` and `grove dev use`.
- **Dependency Management:** Use `grove deps sync` to manage Go dependencies across your entire ecosystem.
- **Interactive Management:** Explore the `grove dev tui` for a terminal-based UI to manage tool versions.