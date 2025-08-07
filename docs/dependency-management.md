# Dependency Management

The `grove deps` command provides tools for managing Go module dependencies across all Grove submodules in the ecosystem.

## Overview

Managing dependencies in a multi-repository project can be challenging. When a core library releases a new version, you need to update all dependent repositories. The `grove deps` command automates this process, ensuring consistency across the entire Grove ecosystem.

## Commands

### grove deps sync

Automatically updates all Grove dependencies to their latest versions across all submodules.

```bash
grove deps sync [flags]
```

#### Flags

- `--commit` - Create a git commit in each updated submodule
- `--push` - Push the commit to origin (implies --commit)

#### Examples

```bash
# Update all Grove dependencies to latest versions
grove deps sync

# Update and commit changes
grove deps sync --commit

# Update, commit, and push changes
grove deps sync --push
```

This command:
1. Discovers all Grove dependencies (github.com/mattsolo1/*) across all submodules
2. Resolves the latest version for each unique dependency
3. Updates each workspace with all its Grove dependencies in a single operation
4. Optionally commits and pushes the changes

### grove deps bump

Updates a specific Go module dependency across all Grove submodules.

```bash
grove deps bump <module_path>[@version] [flags]
```

#### Arguments

- `<module_path>` - The Go module path to update (e.g., `github.com/mattsolo1/grove-core`)
- `[@version]` - Optional version specifier:
  - Specific version: `@v0.2.1`
  - Latest version: `@latest` 
  - If omitted, defaults to latest

#### Flags

- `--commit` - Create a git commit in each updated submodule
- `--push` - Push the commit to origin (implies --commit)

#### Examples

```bash
# Update to the latest version
grove deps bump github.com/mattsolo1/grove-core@latest

# Update to a specific version
grove deps bump github.com/mattsolo1/grove-core@v0.2.1

# Update with automatic commits
grove deps bump github.com/mattsolo1/grove-core@latest --commit

# Update, commit, and push changes
grove deps bump github.com/mattsolo1/grove-core@latest --push
```

## How It Works

1. **Version Resolution**: If `@latest` is used or no version is specified, the command queries the Go module proxy to find the latest available version.

2. **Workspace Discovery**: The command automatically discovers all Grove submodules in your workspace.

3. **Dependency Detection**: For each submodule, it checks if the target module is a dependency by examining the `go.mod` file.

4. **Smart Skipping**: The command skips:
   - Submodules without a `go.mod` file
   - Submodules that don't depend on the target module
   - The target module itself (can't update self-dependency)

5. **Update Process**: For each dependent submodule:
   - Runs `go get <module>@<version>`
   - Runs `go mod tidy` to clean up dependencies
   - Reports success or failure

6. **Git Integration**: When `--commit` is used:
   - Checks for changes using `git status`
   - Stages `go.mod` and `go.sum` files
   - Creates a commit with message: `chore(deps): bump <module> to <version>`
   - Optionally pushes to origin if `--push` is used

## Environment Configuration

The command automatically configures the Go environment for private repositories:
- Sets `GOPRIVATE=github.com/mattsolo1/*`
- Sets `GOPROXY=direct`

This ensures that private Grove modules can be accessed without issues.

## Output

The command provides clear, color-coded output:

```
Resolved github.com/mattsolo1/grove-core to version v0.2.1
Bumping dependency github.com/mattsolo1/grove-core to v0.2.1...

SKIPPED   grove-canopy (dependency not found)
UPDATING  grove-context... done
SKIPPED   grove-core (cannot update self)
UPDATING  grove-flow... done
UPDATING  grove-meta... done
SKIPPED   grove-notebook (dependency not found)
UPDATING  grove-proxy... done
UPDATING  grove-sandbox... done
UPDATING  grove-version... done

Summary:
  Updated: 6 modules
    - grove-context
    - grove-flow
    - grove-meta
    - grove-proxy
    - grove-sandbox
    - grove-version
  Skipped: 3 modules
  Failed:  0 modules
```

## Common Use Cases

### Keeping Everything in Sync

After multiple tools have been released independently:

```bash
# Update all Grove dependencies to their latest versions
grove deps sync --commit

# Review the changes, then push if everything looks good
git push --all
```

### After a Core Library Release

When `grove-core` releases a new version:

```bash
# Update just grove-core across all modules
grove deps bump github.com/mattsolo1/grove-core@latest --commit

# Or update all Grove dependencies at once
grove deps sync --commit
```

### Coordinated Updates

For breaking changes that require coordination:

```bash
# Update to a specific pre-release version for testing
grove deps bump github.com/mattsolo1/grove-core@v0.3.0-rc1

# Run tests across all modules
grove run go test ./...

# If tests pass, commit the changes
grove deps bump github.com/mattsolo1/grove-core@v0.3.0-rc1 --commit
```

### Rollback

If an update causes issues:

```bash
# Revert to a previous known-good version
grove deps bump github.com/mattsolo1/grove-core@v0.1.0 --commit
```

## Best Practices

1. **Test Before Committing**: Run `grove deps bump` without flags first to see what will be updated.

2. **Review Changes**: After updating, review the `go.mod` changes in each repository before committing.

3. **Coordinate Major Updates**: For major version bumps, coordinate with the team to ensure compatibility.

4. **Use CI/CD**: Consider running dependency updates in CI to ensure all tests pass before merging.

5. **Version Pinning**: For production stability, consider pinning to specific versions rather than using `@latest`.

## Troubleshooting

### Authentication Errors

If you see authentication errors for private repositories:
- Ensure you have proper Git credentials configured
- Check that your Personal Access Token has repository access

### Module Not Found

If a module can't be found:
- Verify the module path is correct
- Ensure the version tag exists in the repository
- Check that the module has been properly released

### Build Failures After Update

If builds fail after updating:
- Check for breaking changes in the dependency's changelog
- Run `go mod tidy` manually in affected repositories
- Consider rolling back to the previous version

## Future Enhancements

Planned improvements for the `grove deps` command:

1. **Dependency Graph Visualization**: Show which modules depend on what
2. **Batch Updates**: Update multiple dependencies in a single command
3. **Compatibility Checking**: Verify version compatibility before updating
4. **Automated Testing**: Run tests automatically after updates
5. **Release Notes Integration**: Show relevant changelog entries during updates