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

