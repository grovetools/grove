## v0.2.23 (2025-09-17)

### Bug Fixes

- improve git error reporting in release command to show full error messages

### File Changes

```
 cmd/release.go | 14 +++++++++++---
 1 file changed, 11 insertions(+), 3 deletions(-)
```

## v0.2.22 (2025-09-17)

### Features

* add interactive TUI for Grove dev version management
* add grove dev cwd and reset commands
* implement Docker E2E test improvements
* add --table mode for grove ws plans list

### Performance Improvements

* optimize TUI startup and responsiveness

### Tests

* add Docker-based E2E test infrastructure

### Chores

* bump dependencies

### Code Refactoring

* update TUI to use table view matching grove list

## v0.2.21 (2025-09-13)

### Chores

* update Grove dependencies to latest versions

## v0.2.20 (2025-09-12)

### Bug Fixes

* better error when cyclic dep detected

## v0.2.19 (2025-09-11)

### Bug Fixes

* make add-repo command more robust and add public repository support

## v0.2.18 (2025-09-02)

### Chores

* remove nvim and hooks aliases from tool registry

## v0.2.17 (2025-08-27)

### Bug Fixes

* improve add-repo command

## v0.2.16 (2025-08-27)

### Chores

* **deps:** sync Grove dependencies to latest versions
* remove tmux alias from tool registry

## v0.2.15 (2025-08-26)

### Code Refactoring

* centralize tool registry and remove redundant toolToRepo map

## v0.2.14 (2025-08-26)

### Bug Fixes

* disable ci e2e for now

## v0.2.13 (2025-08-26)

### Bug Fixes

* skip github scenario
* resolve template paths dynamically to fix CI failures

## v0.2.12 (2025-08-26)

### Features

* add template project handler to resolve maturin bug
* parse CHANGELOG.md for GitHub release notes
* implement lipgloss table for grove list command
* improve grove list and install commands

### Code Refactoring

* remove version count suffix from grove list output

### Bug Fixes

* skip module availability check for non-Go projects
* add worktrees ti gitignore

## v0.2.11 (2025-08-25)

### Features

* skip CI workflow monitoring for projects without .github directory

## v0.2.10 (2025-08-25)

### Continuous Integration

* add Git LFS disable to release workflow

### Chores

* bump dependencies

### Features

* improve sync-deps commit messages with version details
* add --sync-deps flag to sync dependencies between release levels
* add outdated dependency detection to release process
* implement parallel releases and improve workflow monitoring
* increase CI workflow timeout to 10 minutes
* add --with-deps flag to grove release command
* add deps tree command for visualizing dependency graph

### Bug Fixes

* improve workflow run detection and increase find timeout
* skip release for dependencies with unchanged versions
* only consider repos being released when calculating dependency levels
* wait for CI workflows to complete before proceeding with downstream releases
* make it optional to update grove ecosystem monorepo

## v0.2.9 (2025-08-15)

### Tests

* fix e2e tests to include workspaces in grove.yml

### Bug Fixes

* disable e2e for now
* disable make test in ci for now
* revert release changes but add tests (#1)
* revert release changes but add tests

### Continuous Integration

* switch to Linux runners to reduce costs

### Chores

* **deps:** bump dependencies
* bump deps, add missing test fn

### Features

* add grove ws manage command
* remove grove- prefix requirement and add GitHub template support
* add react template things
* refactor release command to use workspace discovery instead of git submodules
* extract Go templates to standalone grove-project-tmpl-go
* add external template support to grove add-repo
* add CI workflow and enhance release workflow

## v0.2.8 (2025-08-13)

### 💥 BREAKING CHANGES

* make ecosystem staging opt-in and improve add-repo validation

### Features

* improve error handling for late-stage add-repo failures
* add grove add-repo command for creating new Grove repositories
* improve release styling
* add workspace secret command
* add links to table, fix CI column in ws status

### Chores

* bump grove-core and grove-tend versions

### Bug Fixes

* improve grove add-repo reliability and error handling
* push changelog commit

## v0.2.7 (2025-08-08)

### Features

* add conventional commits enforcement and changelog generation
* enhance release command with date-based versioning and auto-commit
* implement dependency-aware release orchestration

### Bug Fixes

* check for changes before committing in release command
* improve parent repository versioning to avoid tag conflicts

### Chores

* **deps:** bump dependencies
* update grove-tend imports from grovepm to mattsolo1

### Tests

* add grove-tend scenario for conventional commits and changelog

