# Contributing to Grove

Thank you for your interest in contributing to the Grove ecosystem! This guide provides a comprehensive overview of how to get started with development, testing, and submitting contributions to `grove-meta`.

## Development Setup

### Prerequisites

- Go 1.24 or later
- Git configured with your GitHub account
- Make
- GitHub CLI (`gh`) for repository and release management

### Getting the Code

1.  **Fork the repository** on GitHub if you are an external contributor.
2.  **Clone your fork** or the main repository:

    ```bash
    # For contributors with direct access
    git clone https://github.com/mattsolo1/grove-meta.git
    cd grove-meta

    # For external contributors
    git clone https://github.com/YOUR_USERNAME/grove-meta.git
    cd grove-meta
    git remote add upstream https://github.com/mattsolo1/grove-meta.git
    ```

### Initial Environment Setup

1.  **Install dependencies**:
    ```bash
    go mod download
    ```

2.  **Install Git hooks**: This project uses conventional commits, enforced by a Git hook.
    ```bash
    # If you have a version of grove already installed
    grove git-hooks install

    # If you don't have grove installed yet
    go run . git-hooks install
    ```

3.  **Verify your setup**:
    ```bash
    make build
    ./bin/grove version
    ```

## Building Grove

The project uses a `Makefile` to standardize build operations. Always check the `Makefile` for available targets.

### Common Build Commands

| Command | Description |
| :--- | :--- |
| `make build` | Creates a standard production binary in `./bin/grove`. |
| `make dev` | Creates a development build with race condition detection. |
| `make clean` | Removes build artifacts from the `./bin` directory. |
| `make build-all` | (If available) Creates binaries for multiple OS/architecture combinations. |

### Build Output and Versioning

- **Binary Location**: The compiled binary is placed at `./bin/grove`.
- **Development Builds**: For local development, version information is automatically injected from Git, resulting in versions like `main-abc123d-dirty`.
- **Release Builds**: For official releases, a semantic version (e.g., `v0.5.1`) is passed by the CI/CD pipeline.

## Testing

Grove uses a combination of standard Go unit tests and a dedicated end-to-end (E2E) testing framework called `tend`.

### Unit Tests

Run the Go test suite for all packages:
```bash
make test
```
For more detailed output, run Go tests directly:
```bash
go test -v ./...
```

### End-to-End (E2E) Tests

E2E tests simulate real-world CLI usage and verify complex workflows.

1.  **Build the test runner**:
    ```bash
    make test-e2e-build
    ```

2.  **List available test scenarios**:
    ```bash
    ./bin/tend list
    ```

3.  **Run all E2E tests**:
    ```bash
    make test-e2e
    ```

4.  **Run specific test scenarios by tag or name**:
    ```bash
    # Run by scenario name
    make test-e2e ARGS="run -i conventional-commits"

    # Run all scenarios with the 'release' tag
    make test-e2e ARGS="run -t release"
    ```

### Available Test Scenarios
The test suite covers a wide range of functionalities. Use `tend list` for the most up-to-date list. Key scenarios include:

| Scenario | Description |
|:---|:---|
| `conventional-commits` | Tests commit message validation via Git hooks. |
| `add-repo-*` | Tests repository creation (dry-run, local-only, etc.). |
| `polyglot-*` | Tests support for multiple project types (Go, Python/Maturin). |
| `release-tui-*` | Tests the interactive TUI for release planning and execution. |
| `llm-changelog` | Tests AI-powered changelog generation. |
| `sync-deps-*` | Tests automatic dependency synchronization during releases. |
| `workspace-*` | Tests workspace detection and context-aware binary delegation. |

### Docker-based E2E Tests

For fully isolated and reproducible testing, run the E2E suite inside a Docker container:

```bash
# Run with mock GitHub services (default)
make test-e2e-docker

# Run against live GitHub (requires a GITHUB_TOKEN)
GITHUB_TOKEN=your_token make test-e2e-docker
```

## Code Quality

### Formatting

Code is formatted using the standard `gofmt` tool.
```bash
make fmt
```

### Static Analysis

Use `make check` to run all quality checks, including formatting, `go vet`, and `golangci-lint`.
```bash
# Run go vet
make vet

# Run golangci-lint (if installed)
make lint

# Run all checks (fmt, vet, lint, test)
make check
```

If you need to install `golangci-lint`:
```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

## Commit Guidelines

This project enforces the **Conventional Commits** specification. This practice ensures a consistent and readable Git history, which is used to automate changelog generation.

### Commit Message Format

```
<type>(<scope>): <subject>

[optional body]

[optional footer(s)]
```

### Allowed Types

- **feat**: A new feature for the user.
- **fix**: A bug fix for the user.
- **docs**: Documentation-only changes.
- **style**: Code style changes (formatting, etc.) that do not affect the meaning of the code.
- **refactor**: A code change that neither fixes a bug nor adds a feature.
- **perf**: A code change that improves performance.
- **test**: Adding missing tests or correcting existing tests.
- **build**: Changes that affect the build system or external dependencies.
- **ci**: Changes to our CI configuration files and scripts.
- **chore**: Other changes that don't modify source or test files.

### Examples

```bash
# A new feature with a scope
git commit -m "feat(release): add interactive TUI for release planning"

# A fix with a body explaining the change
git commit -m "fix(install): correctly handle private repository URLs

The previous implementation failed to parse SSH-based URLs. This change
adds regex support for both HTTPS and SSH formats."

# A breaking change
git commit -m "feat(dev)!: rework workspace detection to use a new marker file

BREAKING CHANGE: The workspace marker file is now '.grove-workspace'
instead of '.grove-ecosystem-worktree'."
```

The installed `commit-msg` hook will automatically validate your commit messages and reject non-conforming ones.

## Development Workflow

### Branching and Worktrees

Create a new branch for your feature or fix.
```bash
git checkout -b feature/your-feature-name
```

For developing multiple features in parallel without cloning the repository multiple times, `git worktree` is recommended:
```bash
git worktree add ../grove-meta-my-feature feature/my-feature-name
cd ../grove-meta-my-feature
```

### Testing Local Changes

The Grove ecosystem is designed for seamless local development.

1.  **Build your changes**:
    ```bash
    make build
    ```

2.  **Use workspace-aware execution**: When you are inside the `grove-meta` project directory (or a worktree), the `grove` command automatically detects and uses the local binary from `./bin/grove`. This is the primary method for testing.

    ```bash
    # From the root of your grove-meta worktree
    ./bin/grove your-new-command
    ```

3.  **Use `grove dev` for global testing**: If you need to test your development build outside of its worktree, use the `grove dev` commands to create a globally accessible link.

    ```bash
    # Register your local build
    grove dev link . --as my-feature

    # Activate it
    grove dev use grove my-feature

    # Now you can test 'grove' from any directory
    cd ~
    grove --version

    # Switch back to the released version when done
    grove dev use grove --release
    ```

## Pull Request Process

1.  **Run all checks**: Before submitting, ensure all tests and quality checks pass.
    ```bash
    make check
    make test-e2e
    ```

2.  **Update documentation**: If your changes affect user-facing behavior, update the relevant documentation in the `/docs` directory and the command's help text.

3.  **Push your branch** and create a pull request using the GitHub web interface or the `gh` CLI:
    ```bash
    git push origin feature/your-feature-name
    gh pr create --title "feat(scope): your descriptive title" --body-file .github/PULL_REQUEST_TEMPLATE.md
    ```

4.  **Code Review**: Once submitted, your PR will be reviewed by maintainers. Automated checks will also run. Address any feedback by pushing additional commits to your branch.

## Project Structure

A high-level overview of the `grove-meta` repository structure:

```
grove-meta/
├── cmd/            # Command-line interface logic (Cobra commands)
│   ├── dev_*.go    # Subcommands for 'grove dev'
│   ├── release_*.go# Subcommands for 'grove release'
│   └── root.go     # Root command and tool delegation
├── pkg/            # Core libraries and business logic
│   ├── depsgraph/  # Dependency graph construction and sorting
│   ├── devlinks/   # Management of local development symlinks
│   ├── project/    # Handlers for different project types (Go, Python, etc.)
│   ├── reconciler/ # Logic for layering dev versions over releases
│   ├── release/    # Release orchestration and planning
│   ├── sdk/        # Management of installed Grove tool versions
│   └── workspace/  # Discovery and operations across workspaces
├── tests/          # End-to-end tests
│   ├── e2e/        # Test runner and Docker setup
│   └── scenarios*.go # Definitions of test scenarios
├── Makefile        # Build, test, and maintenance automation
└── go.mod          # Go module definition
```

## Adding New Features

### Adding a New Command

1.  Create a new file `cmd/my_command.go`.
2.  Use the `cli.NewStandardCommand` helper to create a new Cobra command.
3.  Add the new command to `rootCmd` in your file's `init()` function.
4.  Implement the command's logic in a `run...` function.
5.  Add unit and E2E tests for the new command.

### Adding a New Project Type

Grove's polyglot capabilities can be extended by adding new project type handlers.

1.  Create a new handler in `pkg/project/` that implements the `ProjectHandler` interface.
2.  Register your new handler in `pkg/project/registry.go`.
3.  Add E2E tests in the `tests/` directory to validate behavior for the new project type.

### Adding a Tool to the SDK Registry

To make a new tool installable via `grove install`, add it to the `toolRegistry` map in `pkg/sdk/manager.go`.

## Debugging

- **Enable Debug Logging**: Set the `GROVE_DEBUG=true` environment variable.
- **Verbose Flag**: Many commands support a `--verbose` flag for more detailed output.
- **Inspect State**: Grove stores its state in `~/.grove`. You can inspect these files for debugging:
    - `~/.grove/active_versions.json`: Tracks the active release version for each tool.
    - `~/.grove/devlinks.json`: Tracks registered local development builds.
    - `~/.grove/versions/`: Contains the unpacked binaries for each installed version.