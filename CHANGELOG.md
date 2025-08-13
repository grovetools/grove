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

