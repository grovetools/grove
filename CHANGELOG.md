## v0.6.2 (2026-02-10)

The setup wizard receives a significant overhaul with support for TOML configuration (5cfff7f), responsive terminal layouts (2df6fd1), and standardized theming (6e4b58d). Additionally, the install script is now POSIX-compatible (90d5d95), and the release command correctly loads LLM configuration from TOML files (bba0226).

### Features
- Add TOML config format and extension options (5cfff7f)
- Make wizard responsive to terminal width (2df6fd1)

### Bug Fixes
- Respect grove.toml for LLM model config (bba0226)
- Make install script POSIX-compatible (90d5d95)

### Improvements
- Simplify ecosystem wizard UI and rename gmux to nav (921c567)
- Refactor wizard to use standardized core themes (6e4b58d)

### File Changes
```
 cmd/release_tui.go        |    4 +-
 cmd/setup.go              | 1092 +++++++++++++++++++++++++++++++--------------
 cmd/setup_ansi.go         |  227 ++++++++--
 pkg/setup/toml_handler.go |   78 ++++
 scripts/install.sh        |   72 ++-
 5 files changed, 1035 insertions(+), 438 deletions(-)
```

## v0.6.1 (2026-02-03)

The release command was fixed, including new flags for explicit version overrides and the ability to generate changelogs in parallel within the TUI (417ad82). Additionally, the installation script has been patched to address execution issues (d48f6fa).

### Features
- Add explicit version flags and parallel changelog generation (417ad82)

### Bug Fixes
- Update install script (d48f6fa)

### File Changes
```
 .github/workflows/ci.yml       |  2 +-
 .golangci.yml                  |  2 +-
 Dockerfile.e2e                 |  8 ++--
 cmd/dev_use.go                 |  2 +-
 cmd/release.go                 | 82 +++++++++++++++++++++++++++++++----------
 cmd/release_subcommands.go     |  4 +-
 cmd/release_tui.go             | 83 ++++++++++++++++++++++++++++++------------
 cmd/setup.go                   | 20 +++++-----
 cmd/version_cmd.go             |  2 +-
 docs/02-installation.md        |  8 ++--
 docs/03-binary-management.md   |  6 +--
 docs/06-command-reference.md   |  4 +-
 pkg/docs/docs.json             | 14 +++----
 pkg/repository/creator_test.go |  2 +-
 scripts/install.sh             | 33 +++++++++++++----
 scripts/setup-dev.sh           | 28 +++++++-------
 tests/e2e/docker_test.sh       | 40 ++++++++++----------
 tests/e2e/mocks/gh_docker_e2e  |  6 +--
 tests/e2e/mocks/grove_mock     | 24 ++++++------
 19 files changed, 238 insertions(+), 132 deletions(-)
```

## v0.6.0 (2026-02-02)

The `grove` CLI tool now adheres to the XDG Base Directory specification for configuration, data, and state files (d38db8c, 5e67869), moving away from hardcoded paths. Configuration support has been expanded to include TOML files alongside YAML (3e721c9, 500bba3), with the internal configuration migrated to `grove.toml` (72d25f7). Additionally, the project references have been updated to reflect the move to the `grovetools` GitHub organization (5a140b3, 8caf95e).

### Features
- Add configuration and README updates (83cf330)
- Update README and overview documentation (1219284, 9642442)

### Refactor
- Migrate to XDG-compliant paths package for config, data, and state (d38db8c)
- Update GitHub owner and import paths to grovetools (5a140b3)
- Use core config discovery to support TOML configuration files (3e721c9)
- Update registry generator to use core config discovery (500bba3)
- Update docgen title to match package name (11ce8b2)

### Fixes
- Use XDG paths for reconciler and tool delegation (5e67869)
- Update setup-dev.sh to use correct directory name (e2b8cab)
- Update registry generator to use core config discovery (4a2859c)
- Make LLM delegation log debug-level to avoid stdout pollution (c208410)
- Update VERSION_PKG to grovetools/core path for correct version injection (a49ea97)
- Reorganize introduction text (77c29cf)

### Documentation
- Add concept lookup instructions to CLAUDE.md (6f4af52)
- Add MIT License (a007ad7)

### Chores
- Migrate grove.yml to grove.toml (72d25f7)
- Update go.mod for grovetools migration (8caf95e)
- Move docs.rules to .cx/ directory (3eca1d8)
- Remove docgen files from repo (7dd33e8)
- Move README template to notebook (270b745)
- Restore release workflow (b077fa0)

### File Changes
```
 .cx/docs.rules                           |   5 ++
 .github/workflows/release.yml            |  73 ++++---------------
 CLAUDE.md                                |  15 +++-
 LICENSE                                  |  21 ++++++
 Makefile                                 |   2 +-
 README.md                                |  62 ++++------------
 cmd/add_repo.go                          |  10 +--
 cmd/bootstrap.go                         |  24 +++---
 cmd/deps.go                              |  18 ++---
 cmd/dev_current.go                       |   3 +-
 cmd/dev_link.go                          |  10 +--
 cmd/dev_prune.go                         |   2 +-
 cmd/dev_use.go                           |   5 +-
 cmd/ecosystem.go                         |   2 +-
 cmd/ecosystem_import.go                  |   4 +-
 cmd/ecosystem_list.go                    |   7 +-
 cmd/install_cmd.go                       |  15 ++--
 cmd/list_cmd.go                          |   9 ++-
 cmd/llm.go                               |  10 +--
 cmd/release.go                           |  10 +--
 cmd/release_changelog_llm.go             |  34 ++++-----
 cmd/release_subcommands.go               |   9 +--
 cmd/release_tui.go                       |   2 +-
 cmd/repo_add.go                          |   8 +-
 cmd/root.go                              |   8 +-
 cmd/schema.go                            |  61 +++++-----------
 cmd/setup.go                             |  13 +++-
 cmd/version_cmd.go                       |   7 +-
 docs/00-introduction.md                  |  21 ------
 docs/01-overview.md                      |  62 ++++------------
 docs/06-command-reference.md             |   2 +-
 docs/README.md.tpl                       |   6 --
 docs/docgen.config.yml                   |  54 --------------
 docs/docs.rules                          |   1 -
 go.mod                                   |   9 ++-
 go.sum                                   |  41 ++++++++++-
 grove.toml                               |  14 ++++
 grove.yml                                |  14 ----
 llm.schema.json                          |   2 +-
 pkg/delegation/config.go                 |  13 ++--
 pkg/depsgraph/builder.go                 |  19 +++--
 pkg/devlinks/registry.go                 |  26 ++-----
 pkg/docs/docs.json                       |  63 +---------------
 pkg/project/go_handler.go                |  12 +--
 pkg/project/template_handler.go          |   9 +--
 pkg/reconciler/reconciler.go             |   5 +-
 pkg/release/plan.go                      |  32 ++++----
 pkg/release/wait.go                      |   2 +-
 pkg/repository/creator.go                |  72 +++++++++---------
 pkg/repository/ecosystem.go              |  11 ++-
 pkg/sdk/manager.go                       |  87 +++++++++-------------
 pkg/sdk/versions.go                      |  42 ++++++++---
 pkg/setup/service.go                     |  23 ++----
 pkg/templates/manager.go                 |   2 +-
 pkg/workspace/local_binary.go            | 121 +++++++++++++++++--------------
 scripts/setup-dev.sh                     |  24 +++---
 tests/e2e/docker_test.sh                 |   8 +-
 tests/e2e/mocks/{gemapi => grove-gemini} |  18 ++---
 tests/mocks.go                           |  20 ++---
 tests/scenarios_changelog_dirty.go       |   8 +-
 tests/scenarios_llm_changelog.go         |  29 ++++----
 tests/scenarios_release_refactor.go      |   6 +-
 tests/scenarios_repo.go                  |   2 +-
 tools/registry-generator/main.go         |  70 ++++++++++--------
 64 files changed, 598 insertions(+), 801 deletions(-)
```

## v0.5.1-nightly.19d8276 (2025-10-03)

## v0.5.0 (2025-10-01)

This release introduces a major refactoring of the release process into a stateful, multi-stage workflow with `plan`, `tui`, and `apply` commands. It includes comprehensive safety commands like `undo-tag` and `rollback` (e797144, 5b03682, 68bb37c). The release orchestration is now more robust, featuring incremental pushing and CI validation after each step (db11744). New workspace management commands have been added for creating, opening, and removing worktrees (e3ad8d7, 63bfd0d), along with a command to bootstrap new ecosystems from scratch (`grove ws init`) (492542e). Command-line functionality is enhanced with include/exclude filtering for `grove run` and `grove ws status` (fec1660, 701c40d), and improved flag handling for `grove run` (9adb039). Tooling and documentation have been improved with a standalone `grove docs generate` command (c732139), automatic dependency resolution during installation (021672d), and simplified, more succinct documentation with TOC generation (8031430, 07ac4a0).

### Features
- Implement stateful release workflow with distinct plan, review, and apply stages (5b03682, 68bb37c)
- Implement comprehensive safety commands for release workflow (`undo-tag`, `rollback`) (e797144)
- Integrate incremental pushing and CI validation into release orchestration (db11744)
- Implement workspace management commands (create, open, remove) (e3ad8d7, 63bfd0d)
- Implement automatic dependency resolution for tool installation (021672d)
- Add standalone `docs generate` command to run docgen across all workspaces (c732139)
- Add exclude filtering to `grove run` command (fec1660)
- Add include/exclude filtering to `grove ws status` command (701c40d)
- Add comprehensive workspace bootstrapping E2E tests (492542e)
- Add JSON output support to `grove ws status` command (10c0850)
- Add TOC generation and docgen configuration updates (8031430)
- Improve worktree discovery in workspace open command (44c363a)
- Add E2E testing infrastructure for release refactor (2df176e, d5544a7)
- Simplify and improve documentation and prompts (07ac4a0, 47c330e, b8fa99b, cbaa428)

### Bug Fixes
- Improve flag handling in `grove run` command (9adb039)
- Update CI workflow to use `branches: [ none ]` for disabling (1f6003c)
- Remove old documentation files (2c7c735)
- Update E2E tests for streamline-release to match harness API (0e2de1f)
- Correct dev link detection in reconciler (ba744c4)
- Update workspace detection to use `.grove/workspace` marker (2ebe3e4)

### Refactoring
- Standardize `docgen.config.yml` key order and settings (e134d3e)

### Documentation
- Update docgen configuration and README templates (0cf9214)
- Update docgen config and overview prompt (e7a63d7)
- Simplify documentation structure to 4 sections (e8950d9)
- Consolidate installation instructions into comprehensive guide (2d47c61)

### Chores
- Temporarily disable CI workflow (9217162)
- Remove unused sync-deps-tui scenario (e857a10)

### File Changes
```
 .github/workflows/ci.yml                           |   4 +-
 .grove-workspace                                   |   6 +-
 Makefile                                           |  11 +-
 README.md                                          | 168 ++---
 cmd/alias.go                                       | 159 +++++
 cmd/dev_workspace.go                               |   2 +-
 cmd/docs.go                                        | 120 ++++
 cmd/install_cmd.go                                 |  58 +-
 cmd/list_cmd.go                                    |   5 +-
 cmd/release.go                                     | 388 +++--------
 cmd/release_plan.go                                |   6 -
 cmd/release_subcommands.go                         | 714 +++++++++++++++++++++
 cmd/release_tui.go                                 | 183 +++---
 cmd/root.go                                        |  13 +-
 cmd/run_cmd.go                                     |  85 ++-
 cmd/workspace.go                                   |   3 +
 cmd/workspace_create.go                            |  55 ++
 cmd/workspace_open.go                              |  83 +++
 cmd/workspace_remove.go                            |  68 ++
 cmd/workspace_status.go                            | 195 ++++++
 docs/00-introduction.md                            |  21 +
 docs/01-overview.md                                |  93 +--
 docs/02-installation.md                            | 251 +++-----
 docs/03-binary-management.md                       |  58 ++
 docs/03-getting-started.md                         | 164 -----
 docs/04-core-concepts.md                           | 143 -----
 docs/04-ecosystems.md                              |  33 +
 docs/05-command-reference.md                       | 687 --------------------
 docs/05-configuration.md                           | 128 ++++
 docs/06-command-reference.md                       | 551 ++++++++++++++++
 docs/06-tutorials.md                               | 281 --------
 docs/07-configuration.md                           | 234 -------
 docs/08-architecture.md                            | 202 ------
 docs/09-contributing.md                            | 335 ----------
 docs/README.md.tpl                                 |   6 +
 docs/docgen.config.yml                             |  81 ++-
 docs/docs.rules                                    |   1 -
 docs/images/grove-base-readme.svg                  | 345 ++++++++++
 docs/prompts/00-introduction.md                    |  31 +
 docs/prompts/01-overview.md                        |  31 +
 docs/prompts/02-installation.md                    |  29 +
 docs/prompts/03-binary-management.md               |  28 +
 docs/prompts/04-ecosystems.md                      |  22 +
 .../{configuration.md => 05-configuration.md}      |   0
 ...ommand-reference.md => 06-command-reference.md} |   0
 docs/prompts/architecture.md                       |  63 --
 docs/prompts/contributing.md                       |  64 --
 docs/prompts/core-concepts.md                      |  37 --
 docs/prompts/getting-started.md                    |  44 --
 docs/prompts/installation.md                       |  41 --
 docs/prompts/overview.md                           |  28 -
 docs/prompts/tutorials.md                          |  48 --
 pkg/docs/docs.json                                 | 212 ++++++
 pkg/gh/client.go                                   |  73 +++
 pkg/reconciler/reconciler.go                       |  62 +-
 pkg/release/plan.go                                |   1 +
 pkg/sdk/manager.go                                 | 290 ++++++---
 tests/e2e/docker_test.sh                           | 329 +++++++++-
 tests/e2e_mocks/gemapi/main.go                     |  17 +
 tests/e2e_mocks/gh/main.go                         |  41 ++
 tests/e2e_mocks/git/main.go                        |  75 +++
 tests/e2e_mocks/go/main.go                         |  38 ++
 tests/scenarios.go                                 |   8 +-
 tests/scenarios_add_repo.go                        |  58 +-
 tests/scenarios_release_refactor.go                | 549 ++++++++++++++++
 tests/scenarios_sync_deps_tui.go                   | 504 ---------------
 tests/scenarios_workspace_aware.go                 |  59 +-
 tests/scenarios_workspace_bootstrapping.go         | 129 ++++
 68 files changed, 5000 insertions(+), 3851 deletions(-)
```

## v0.5.0 (2025-10-01)

This release introduces a major refactoring of the release process into a stateful, multi-stage workflow with `plan`, `tui`, and `apply` commands. It includes comprehensive safety commands like `undo-tag` and `rollback` (e797144, 5b03682, 68bb37c). The release orchestration is now more robust, featuring incremental pushing and CI validation after each step (db11744). New workspace management commands have been added for creating, opening, and removing worktrees (e3ad8d7, 63bfd0d), along with a command to bootstrap new ecosystems from scratch (`grove ws init`) (492542e). Command-line functionality is enhanced with include/exclude filtering for `grove run` and `grove ws status` (fec1660, 701c40d), and improved flag handling for `grove run` (9adb039). Tooling and documentation have been improved with a standalone `grove docs generate` command (c732139), automatic dependency resolution during installation (021672d), and simplified, more succinct documentation with TOC generation (8031430, 07ac4a0).

### Features
- Implement stateful release workflow with distinct plan, review, and apply stages (5b03682, 68bb37c)
- Implement comprehensive safety commands for release workflow (`undo-tag`, `rollback`) (e797144)
- Integrate incremental pushing and CI validation into release orchestration (db11744)
- Implement workspace management commands (create, open, remove) (e3ad8d7, 63bfd0d)
- Implement automatic dependency resolution for tool installation (021672d)
- Add standalone `docs generate` command to run docgen across all workspaces (c732139)
- Add exclude filtering to `grove run` command (fec1660)
- Add include/exclude filtering to `grove ws status` command (701c40d)
- Add comprehensive workspace bootstrapping E2E tests (492542e)
- Add JSON output support to `grove ws status` command (10c0850)
- Add TOC generation and docgen configuration updates (8031430)
- Improve worktree discovery in workspace open command (44c363a)
- Add E2E testing infrastructure for release refactor (2df176e, d5544a7)
- Simplify and improve documentation and prompts (07ac4a0, 47c330e, b8fa99b, cbaa428)

### Bug Fixes
- Improve flag handling in `grove run` command (9adb039)
- Update CI workflow to use `branches: [ none ]` for disabling (1f6003c)
- Remove old documentation files (2c7c735)
- Update E2E tests for streamline-release to match harness API (0e2de1f)
- Correct dev link detection in reconciler (ba744c4)
- Update workspace detection to use `.grove/workspace` marker (2ebe3e4)

### Refactoring
- Standardize `docgen.config.yml` key order and settings (e134d3e)

### Documentation
- Update docgen configuration and README templates (0cf9214)
- Update docgen config and overview prompt (e7a63d7)
- Simplify documentation structure to 4 sections (e8950d9)
- Consolidate installation instructions into comprehensive guide (2d47c61)

### Chores
- Temporarily disable CI workflow (9217162)
- Remove unused sync-deps-tui scenario (e857a10)

### File Changes
```
 .github/workflows/ci.yml                           |   4 +-
 .grove-workspace                                   |   6 +-
 Makefile                                           |  11 +-
 README.md                                          | 168 ++---
 cmd/alias.go                                       | 159 +++++
 cmd/dev_workspace.go                               |   2 +-
 cmd/docs.go                                        | 120 ++++
 cmd/install_cmd.go                                 |  58 +-
 cmd/list_cmd.go                                    |   5 +-
 cmd/release.go                                     | 388 +++--------
 cmd/release_plan.go                                |   6 -
 cmd/release_subcommands.go                         | 714 +++++++++++++++++++++
 cmd/release_tui.go                                 | 183 +++---
 cmd/root.go                                        |  13 +-
 cmd/run_cmd.go                                     |  85 ++-
 cmd/workspace.go                                   |   3 +
 cmd/workspace_create.go                            |  55 ++
 cmd/workspace_open.go                              |  83 +++
 cmd/workspace_remove.go                            |  68 ++
 cmd/workspace_status.go                            | 195 ++++++
 docs/00-introduction.md                            |  21 +
 docs/01-overview.md                                |  93 +--
 docs/02-installation.md                            | 251 +++-----
 docs/03-binary-management.md                       |  58 ++
 docs/03-getting-started.md                         | 164 -----
 docs/04-core-concepts.md                           | 143 -----
 docs/04-ecosystems.md                              |  33 +
 docs/05-command-reference.md                       | 687 --------------------
 docs/05-configuration.md                           | 128 ++++
 docs/06-command-reference.md                       | 551 ++++++++++++++++
 docs/06-tutorials.md                               | 281 --------
 docs/07-configuration.md                           | 234 -------
 docs/08-architecture.md                            | 202 ------
 docs/09-contributing.md                            | 335 ----------
 docs/README.md.tpl                                 |   6 +
 docs/docgen.config.yml                             |  81 ++-
 docs/docs.rules                                    |   1 -
 docs/images/grove-base-readme.svg                  | 345 ++++++++++
 docs/prompts/00-introduction.md                    |  31 +
 docs/prompts/01-overview.md                        |  31 +
 docs/prompts/02-installation.md                    |  29 +
 docs/prompts/03-binary-management.md               |  28 +
 docs/prompts/04-ecosystems.md                      |  22 +
 .../{configuration.md => 05-configuration.md}      |   0
 ...ommand-reference.md => 06-command-reference.md} |   0
 docs/prompts/architecture.md                       |  63 --
 docs/prompts/contributing.md                       |  64 --
 docs/prompts/core-concepts.md                      |  37 --
 docs/prompts/getting-started.md                    |  44 --
 docs/prompts/installation.md                       |  41 --
 docs/prompts/overview.md                           |  28 -
 docs/prompts/tutorials.md                          |  48 --
 pkg/docs/docs.json                                 | 212 ++++++
 pkg/gh/client.go                                   |  73 +++
 pkg/reconciler/reconciler.go                       |  62 +-
 pkg/release/plan.go                                |   1 +
 pkg/sdk/manager.go                                 | 290 ++++++---
 tests/e2e/docker_test.sh                           | 329 +++++++++-
 tests/e2e_mocks/gemapi/main.go                     |  17 +
 tests/e2e_mocks/gh/main.go                         |  41 ++
 tests/e2e_mocks/git/main.go                        |  75 +++
 tests/e2e_mocks/go/main.go                         |  38 ++
 tests/scenarios.go                                 |   8 +-
 tests/scenarios_add_repo.go                        |  58 +-
 tests/scenarios_release_refactor.go                | 549 ++++++++++++++++
 tests/scenarios_sync_deps_tui.go                   | 504 ---------------
 tests/scenarios_workspace_aware.go                 |  59 +-
 tests/scenarios_workspace_bootstrapping.go         | 129 ++++
 68 files changed, 5000 insertions(+), 3851 deletions(-)
```

## v0.4.0 (2025-09-26)

This release introduces major improvements to the developer experience with workspace-aware tooling and a new log viewer. The `grove` can automatically use binaries from the current workspace, which can be explicitly managed with the new `grove activate` command (4d19dd3, 4897254). A unified `grove logs` command has been added, featuring an interactive TUI for browsing, searching, and analyzing structured logs across the ecosystem (bda1ed4, 617d279). The release process has been significantly refactored to be more robust, with smarter dependency management and changelog handling to prevent common failures (0da8d36, 9453ff3).

### Features
- Add interactive TUI for `grove logs` with split-view, visual selection, and JSON copy (617d279)
- Implement automatic workspace-aware binary resolution using `.grove-workspace` marker files (4897254)
- Add `grove activate` command for explicit workspace shell activation and PATH management (4d19dd3)
- Add unified `grove logs` command for viewing structured logs from all workspaces (bda1ed4)
- Show historical logs before tailing with `grove logs` via the --lines/-n flag (f9f9eb7)
- Add unified `grove llm` command to act as a facade for multiple LLM providers (c2e3250)
- Add smart changelog detection in the release TUI to preserve manual edits (0a2225a)
- Add support for installing all tools as nightly builds with `grove install all@nightly` (990ca28)
- Add Git info display and scrolling to the release TUI (97c1dd4)
- Add comprehensive logging to debug release issues (1aae158)
- Enhance release display with structured logging from a pretty logger (0590694, b4727bb)

### Bug Fixes
- Resolve release failures caused by dirty changelogs and duplicated content (9453ff3)
- Update staged changelog version in TUI when bump level is changed (e28ecf6)
- Fix dependency update commits to only trigger when files actually change (142367e)
- Correct TUI test key sequences for sync-deps functionality (f3cc349)
- Resolve mock gemapi PATH issues in TUI tests (2f7c1e1)
- Resolve sync-deps test failures with improved CI monitoring simulation (85a69f3)
- Simplify TUI test setup to match the changelog test pattern (6a1d08f)
- Fix release-tui tests for smart changelog detection and git status behavior (5269afa)

### Refactoring
- Simplify the release process by removing the redundant `--sync-deps` flag and automating dependency updates (0da8d36)
- Move `release changelog` subcommand to top-level `changelog` command (a6c7757)

### Testing
- Add comprehensive tests for workspace-aware binary resolution and delegation (b3af5ed)
- Add TUI tests for dependency synchronization functionality (d0a0b9b)
- Add E2E test scenario for `release --sync-deps` functionality (274c32f)
- Enable and fix TUI changelog workflow tests with improved capabilities (5d77532)
- Add comprehensive tests for smart changelog detection and state tracking (337bad3)
- Add E2E test for release TUI selection functionality (e4020ff)

### Documentation
- Align documentation prompt files with the `docgen.config.yml` structure (177958b)
- Add documentation generated directly by Claude for core features (dcd44b2)

### Chores
- Update .gitignore rules for `go.work` and build instructions (74fee8d)
- Remove old documentation files (734dde6)

### File Changes
```
 .gitignore                                 |   7 +
 .grove-workspace                           |   3 +
 CLAUDE.md                                  |  30 +
 README.md                                  |   5 +
 cmd/activate.go                            | 235 +++++++
 cmd/{release_changelog.go => changelog.go} |   4 +-
 cmd/dev.go                                 |   1 +
 cmd/dev_cwd.go                             |  34 +-
 cmd/dev_workspace.go                       |  76 +++
 cmd/install_cmd.go                         |  41 +-
 cmd/llm.go                                 | 135 ++++
 cmd/logs.go                                | 674 ++++++++++++++++++++
 cmd/logs_tui.go                            | 969 +++++++++++++++++++++++++++++
 cmd/release.go                             | 423 +++++--------
 cmd/release_display.go                     |  40 +-
 cmd/release_plan.go                        |  77 ++-
 cmd/release_tui.go                         | 219 +++++--
 cmd/root.go                                | 139 ++++-
 docs/01-overview.md                        |  75 +++
 docs/02-installation.md                    | 162 +++++
 docs/03-getting-started.md                 | 164 +++++
 docs/04-core-concepts.md                   | 143 +++++
 docs/05-command-reference.md               | 687 ++++++++++++++++++++
 docs/06-tutorials.md                       | 281 +++++++++
 docs/07-configuration.md                   | 234 +++++++
 docs/08-architecture.md                    | 202 ++++++
 docs/09-contributing.md                    | 335 ++++++++++
 docs/README.md                             |  65 --
 docs/dependency-management.md              | 234 -------
 docs/docgen.config.yml                     |  58 ++
 docs/docs.rules                            |   2 +
 docs/prompts/architecture.md               |  63 ++
 docs/prompts/command-reference.md          |  42 ++
 docs/prompts/configuration.md              |  56 ++
 docs/prompts/contributing.md               |  64 ++
 docs/prompts/core-concepts.md              |  37 ++
 docs/prompts/getting-started.md            |  44 ++
 docs/prompts/installation.md               |  41 ++
 docs/prompts/overview.md                   |  28 +
 docs/prompts/tutorials.md                  |  48 ++
 docs/release-process.md                    | 212 -------
 docs/release-workflow-template.md          | 214 -------
 docs/sdk-manager-api.md                    | 233 -------
 go.mod                                     |   3 +
 go.sum                                     |   5 +
 grove.yml                                  |   5 +
 pkg/release/plan.go                        |  15 +
 pkg/sdk/manager.go                         |   1 +
 pkg/workspace/local_binary.go              |  88 ++-
 tests/conventional_commits.go              |   6 +-
 tests/e2e/docker_test.sh                   |   9 +
 tests/e2e/mocks/gemapi                     |  72 +++
 tests/mocks.go                             | 307 +++++++++
 tests/scenarios.go                         |  13 +-
 tests/scenarios_changelog_dirty.go         | 311 +++++++++
 tests/scenarios_changelog_dirty_simple.go  | 287 +++++++++
 tests/scenarios_llm_changelog.go           |  28 +-
 tests/scenarios_release_tui.go             | 380 +++++++++++
 tests/scenarios_sync_deps.go               | 365 +++++++++++
 tests/scenarios_sync_deps_tui.go           | 504 +++++++++++++++
 tests/scenarios_workspace_aware.go         | 249 ++++++++
 61 files changed, 8073 insertions(+), 1411 deletions(-)
```

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
