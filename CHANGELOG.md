## v0.3.0 (2025-09-17)

This release introduces a new interactive TUI for the `grove release` command, available via the `--interactive` flag or the `tui` subcommand (a0c2886). This interface provides a comprehensive dashboard for planning and executing releases across the ecosystem, showing current versions, proposed bumps, and dependency-ordered release levels (a71afa2, edfbfc0). A key feature is the integration of LLM-powered changelog generation and semantic versioning suggestions directly within the TUI (20dd1aa, 9630dd1). The TUI allows for repository selection (22507ba), bulk selection shortcuts (9a9afbd), and preserves manually generated changelogs to avoid being overwritten (9b7c400).

A dedicated settings view has been added to the release TUI, accessible via the Tab key, which allows for toggling options like dry-run, push, and dependency syncing (c2d1090, 747b3d1). This view is fully navigable with keyboard shortcuts (95094aa). The TUI also includes a detailed help menu, stale changelog detection, and ensures a clean hand-off to the terminal for the final release process to prevent output corruption (5915059).

Beyond the release TUI, this version adds a new `grove workspace list` command to display all discovered workspaces and their Git worktrees, with support for JSON output (7a524cd). The `grove install` command has also been enhanced with more informative, state-aware output and support for installing `@nightly` builds directly from source (4510223). Several bug fixes improve the reliability of the release process, including better git error reporting (786d7a4), correctly handling pre-generated changelogs (8957237), and ensuring only selected repositories are released from the TUI (bad8923).

### Features

- Implement interactive release TUI with LLM version suggestions (20dd1aa)
- Add LLM-powered changelog generation (9630dd1)
- Improve release TUI display to match grove release output (a71afa2)
- Add repository selection toggle to release TUI (22507ba)
- Add changelog generation to release TUI (1bca421)
- Improve release TUI with help menu, dry-run mode, and stale changelog detection (5915059)
- Add select/deselect all shortcuts to release TUI (9a9afbd)
- Add push and sync-deps toggles to release TUI (747b3d1)
- Add dedicated settings view accessible via Tab key (c2d1090)
- Make settings view navigable with arrow keys and hjkl (95094aa)
- Add workspace list command to display workspaces and worktrees (7a524cd)
- Improve install command UX with state-aware output and nightly builds (4510223)
- Add --fresh flag to clear stale release plans (30d7ad7)

### Bug Fixes

- Only release selected repositories from TUI (bad8923)
- Remove temporary Go workspace files (5f1001f)
- Allow pre-generated CHANGELOG.md files in pre-flight checks (8957237)
- Preserve pre-generated changelogs from TUI workflow during release (9b7c400)
- Include all repositories in release TUI plan (edfbfc0)
- Preserve backward compatibility for grove release command (a0c2886)
- Improve git error reporting in release command (786d7a4)

### File Changes

```
 CHANGELOG.md                     |   13 +
 Dockerfile.e2e                   |    7 +
 cmd/install_cmd.go               |  148 +++-
 cmd/list_cmd.go                  |   28 +-
 cmd/release.go                   |  177 ++++-
 cmd/release_changelog.go         |   21 +-
 cmd/release_changelog_llm.go     |  412 ++++++++++
 cmd/release_plan.go              |  525 ++++++++++++
 cmd/release_tui.go               | 1625 ++++++++++++++++++++++++++++++++++++++
 cmd/styles.go                    |   29 +
 cmd/workspace.go                 |    1 +
 cmd/workspace_list.go            |  130 +++
 cmd/workspace_secrets.go         |    5 +-
 cmd/workspace_status.go          |    4 +-
 pkg/release/plan.go              |  103 +++
 pkg/sdk/manager.go               |   96 +++
 tests/e2e/docker_test.sh         |   84 +-
 tests/scenarios.go               |    2 +
 tests/scenarios_llm_changelog.go |  570 +++++++++++++
 19 files changed, 3873 insertions(+), 107 deletions(-)
```

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

