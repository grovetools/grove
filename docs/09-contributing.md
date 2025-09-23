# Contributing to Grove

Thank you for your interest in contributing to Grove! This guide will help you get started with development, testing, and submitting contributions to the Grove meta-tool.

## Development Setup

### Prerequisites

Before contributing to Grove, ensure you have:

- Go 1.21 or later installed
- Git configured with your GitHub account
- Make utility available
- (Optional) GitHub CLI (`gh`) for working with issues and PRs
- (Optional) `golangci-lint` for code linting

### Cloning the Repository

1. Fork the repository on GitHub (if you're not a maintainer)
2. Clone your fork or the main repository:

```bash
# For contributors with direct access
git clone https://github.com/mattsolo1/grove-meta.git
cd grove-meta

# For external contributors
git clone https://github.com/YOUR_USERNAME/grove-meta.git
cd grove-meta
git remote add upstream https://github.com/mattsolo1/grove-meta.git
```

### Setting Up the Development Environment

1. **Install dependencies**:
```bash
go mod download
```

2. **Install Git hooks** (enforces conventional commits):
```bash
grove git-hooks install
# or if grove isn't installed yet
go run . git-hooks install
```

3. **Verify your setup**:
```bash
make build
./bin/grove version
```

## Building Grove

Grove uses a Makefile to standardize build operations. Always review the Makefile first to understand available targets.

### Common Build Commands

```bash
# Standard build
make build              # Creates binary in ./bin/grove

# Development build with race detector
make dev                # Includes race condition detection

# Clean build artifacts
make clean              # Removes binaries and temp files

# Build for all platforms
make build-all          # Creates binaries for multiple OS/arch combinations
```

### Build Output

- **Binary location**: `./bin/grove`
- **Never** copy binaries elsewhere in PATH manually
- Use `grove dev link` for development testing
- Version information is injected at build time via LDFLAGS

### Version Information

During development, version strings are automatically generated from Git:
- Format: `branch-commit[-dirty]`
- Example: `main-abc123` or `feature-def456-dirty`

For releases, the version is passed by CI/CD:
- Format: `vX.Y.Z`
- Example: `v0.2.1`

## Testing

### Unit Tests

Run the standard Go test suite:

```bash
make test
# or with verbose output
go test -v ./...
```

### End-to-End Tests

Grove uses the `tend` testing framework for E2E tests:

1. **Build the test runner**:
```bash
make test-e2e-build
```

2. **List available test scenarios**:
```bash
./bin/tend list
```

3. **Run all E2E tests**:
```bash
make test-e2e
```

4. **Run specific test scenarios**:
```bash
make test-e2e ARGS="run -i conventional-commits"
make test-e2e ARGS="run -i add-repo-dry-run"
```

### Available Test Scenarios

| Scenario | Description |
|----------|-------------|
| `conventional-commits` | Tests commit message validation |
| `add-repo-dry-run` | Tests repository creation in dry-run mode |
| `add-repo-with-github` | Tests GitHub integration |
| `add-repo-skip-github` | Tests local-only repo creation |
| `polyglot-project-types` | Tests multiple language support |
| `polyglot-dependency-graph` | Tests cross-language dependencies |
| `release-tui` | Tests interactive release interface |
| `llm-changelog` | Tests AI-powered changelog generation |

### Docker-based E2E Tests

For isolated testing environments:

```bash
# Run with mock data
make test-e2e-docker

# Run with live GitHub (requires GITHUB_TOKEN)
GITHUB_TOKEN=your_token make test-e2e-docker
```

## Code Quality

### Code Formatting

Grove uses standard Go formatting:

```bash
make fmt                # Format all code
gofmt -w .             # Alternative
```

### Static Analysis

Run static analysis tools:

```bash
make vet                # Run go vet
make lint               # Run golangci-lint (if installed)
make check              # Run all checks (fmt, vet, lint, test)
```

### Installing golangci-lint

If you don't have golangci-lint:

```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

## Commit Guidelines

Grove enforces Conventional Commits format for all commit messages. This ensures consistent, readable history and enables automated changelog generation.

### Commit Message Format

```
<type>(<scope>): <subject>

<body>

<footer>
```

### Types

- **feat**: New feature
- **fix**: Bug fix
- **docs**: Documentation changes
- **style**: Code style changes (formatting, semicolons, etc.)
- **refactor**: Code refactoring without feature changes
- **perf**: Performance improvements
- **test**: Test additions or corrections
- **build**: Build system or dependency changes
- **ci**: CI/CD configuration changes
- **chore**: Routine tasks, maintenance

### Examples

```bash
# Feature
git commit -m "feat(dev): add interactive TUI for dev link management"

# Bug fix
git commit -m "fix(install): handle private repository authentication correctly"

# Documentation
git commit -m "docs: add polyglot project type examples"

# With scope and body
git commit -m "feat(release): implement dependency-aware release ordering

This change ensures that tools are released in the correct order based
on their dependency relationships, preventing version conflicts."
```

### Commit Hook Enforcement

The installed Git hook will:
1. Check your commit message format
2. Reject non-conforming commits
3. Provide helpful error messages

If you need to bypass (not recommended):
```bash
git commit --no-verify -m "your message"
```

## Development Workflow

### Creating a Feature Branch

```bash
# Create and switch to a new branch
git checkout -b feature/your-feature-name

# Or use git worktree for parallel development
git worktree add ../grove-meta-feature feature/your-feature-name
cd ../grove-meta-feature
```

### Making Changes

1. **Write tests first** (TDD approach recommended)
2. **Implement your feature**
3. **Run tests locally**:
   ```bash
   make test
   make test-e2e ARGS="run -i relevant-scenario"
   ```
4. **Check code quality**:
   ```bash
   make check
   ```

### Testing with Development Builds

Link your development build for testing:

```bash
make build
grove dev link grove ./bin/grove --name my-feature
grove dev use grove my-feature

# Test your changes
grove --version
grove your-new-command

# Reset when done
grove dev reset grove
```

### Updating Dependencies

If you need to update or add dependencies:

```bash
# Add a new dependency
go get github.com/example/package

# Update grove-core
go get -u github.com/mattsolo1/grove-core@latest

# Clean up
go mod tidy
```

## Pull Request Process

### Before Submitting

1. **Ensure all tests pass**:
   ```bash
   make check
   make test-e2e
   ```

2. **Update documentation** if needed:
   - Update relevant docs in `/docs`
   - Update command help text
   - Update README if adding major features

3. **Squash commits** if needed:
   ```bash
   git rebase -i origin/main
   ```

### Submitting a Pull Request

1. **Push your branch**:
   ```bash
   git push origin feature/your-feature-name
   ```

2. **Create the PR**:
   ```bash
   # Using GitHub CLI
   gh pr create --title "feat: your feature" --body "Description..."
   
   # Or via GitHub web interface
   ```

3. **PR Description Template**:
   ```markdown
   ## Summary
   Brief description of the changes
   
   ## Motivation
   Why these changes are needed
   
   ## Changes
   - Change 1
   - Change 2
   
   ## Testing
   How the changes were tested
   
   ## Checklist
   - [ ] Tests added/updated
   - [ ] Documentation updated
   - [ ] Conventional commit format
   - [ ] E2E tests pass
   ```

### Code Review Process

1. **Automated checks** will run on your PR:
   - Unit tests
   - E2E tests
   - Linting
   - Build verification

2. **Maintainer review**:
   - Code quality and style
   - Test coverage
   - Documentation completeness
   - Architecture alignment

3. **Address feedback**:
   ```bash
   # Make requested changes
   git add .
   git commit -m "fix: address review feedback"
   git push origin feature/your-feature-name
   ```

## Project Structure

Understanding Grove's structure helps when contributing:

```
grove-meta/
├── cmd/                    # Command implementations
│   ├── root.go            # Main entry point
│   ├── install_cmd.go     # Install command
│   ├── dev_*.go           # Dev subcommands
│   └── ...
├── pkg/                   # Core packages
│   ├── sdk/               # SDK management
│   ├── workspace/         # Workspace operations
│   ├── devlinks/          # Dev link management
│   ├── reconciler/        # Version reconciliation
│   ├── depsgraph/         # Dependency graphs
│   └── project/           # Project type handlers
├── tests/                 # Test files
│   ├── e2e/              # E2E test runner
│   └── scenarios.go      # Test scenarios
├── scripts/              # Helper scripts
├── docs/                 # Documentation
├── Makefile              # Build automation
└── go.mod                # Go module definition
```

## Adding New Features

### Adding a New Command

1. Create command file in `/cmd`:
```go
// cmd/mycommand.go
package cmd

import "github.com/spf13/cobra"

func init() {
    rootCmd.AddCommand(newMyCommand())
}

func newMyCommand() *cobra.Command {
    return &cobra.Command{
        Use:   "mycommand",
        Short: "Brief description",
        RunE:  runMyCommand,
    }
}

func runMyCommand(cmd *cobra.Command, args []string) error {
    // Implementation
    return nil
}
```

2. Add tests in `/cmd/mycommand_test.go`
3. Update documentation

### Adding a Project Type

1. Implement the ProjectHandler interface:
```go
// pkg/project/myhandler.go
type MyHandler struct{}

func (h *MyHandler) ParseDependencies(path string) ([]Dependency, error) {
    // Implementation
}

// Implement other interface methods...
```

2. Register in `/pkg/project/registry.go`
3. Add E2E tests
4. Document in configuration guide

### Adding a Tool to the Registry

Update `/pkg/sdk/manager.go`:
```go
var toolRegistry = map[string]ToolInfo{
    // ...existing tools...
    "newtool": {
        RepoName:   "grove-newtool",
        BinaryName: "newtool",
    },
}
```

## Debugging

### Debug Output

Enable debug logging:
```bash
GROVE_DEBUG=true grove list
```

### Verbose Mode

Use the verbose flag:
```bash
grove --verbose install cx
```

### Development Inspection

Inspect Grove's state:
```bash
# Check active versions
cat ~/.grove/active_versions.json

# Check dev links
cat ~/.grove/devlinks.json

# Check installed versions
ls -la ~/.grove/versions/
```

## Common Issues

### Build Failures

```bash
# Clean and rebuild
make clean
go mod download
make build
```

### Test Failures

```bash
# Run specific failing test
go test -v -run TestName ./pkg/...

# Check E2E test logs
make test-e2e ARGS="run -i failing-scenario --verbose"
```

### Commit Hook Issues

```bash
# Reinstall hooks
grove git-hooks uninstall
grove git-hooks install

# Check hook is installed
ls -la .git/hooks/commit-msg
```

## Documentation

When contributing, update documentation for:

- New commands: Add to Command Reference
- New features: Update relevant guides
- Breaking changes: Add migration notes
- Configuration changes: Update Configuration Guide

### Documentation Standards

- Use clear, concise language
- Include code examples
- Explain the "why" not just the "what"
- Keep formatting consistent
- Test all examples

## Release Process

While releases are typically handled by maintainers, understanding the process helps:

1. **Version bumping**: Semantic versioning (major.minor.patch)
2. **Changelog generation**: From conventional commits
3. **Tag creation**: Triggers CI/CD
4. **Binary building**: Multi-platform builds
5. **GitHub release**: With release notes
6. **Registry update**: Tool version updates

## Getting Help

### Resources

- **Issues**: [GitHub Issues](https://github.com/mattsolo1/grove-meta/issues)
- **Discussions**: [GitHub Discussions](https://github.com/mattsolo1/grove-meta/discussions)
- **Documentation**: This guide and `/docs` directory

### Communication Channels

- Open an issue for bugs or feature requests
- Start a discussion for questions or ideas
- Comment on existing issues to provide input

## Code of Conduct

- Be respectful and inclusive
- Welcome newcomers and help them get started
- Focus on constructive feedback
- Respect differing opinions
- Report inappropriate behavior to maintainers

## License

By contributing to Grove, you agree that your contributions will be licensed under the same license as the project (check LICENSE file).

## Thank You!

Your contributions make Grove better for everyone. Whether you're fixing bugs, adding features, improving documentation, or helping others, every contribution is valued and appreciated.

Happy coding! 🌲