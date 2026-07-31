# Grove Build Instructions for Claude

This file contains important instructions for Claude when working with this repository.

## Building and Testing

1. **Review the Makefile first** - Always check the Makefile to understand available build targets and options.

2. **Use make commands** - Build and test using:
   ```bash
   make build      # Creates binary in ./bin
   make test-e2e   # Runs end-to-end tests
   ```

3. **Binary Management** - IMPORTANT:
   - Binaries are created in the `./bin` directory
   - **NEVER** copy binaries elsewhere in the PATH
   - Binaries are managed by the `grove` meta-tool
   - Use `grove list` to see currently active binaries across the ecosystem

4. **Testing with tend**:
   - Use `tend list` to see available tests
   - The `tend` command will automatically find the test runner binary in `./bin`
   - No need to specify paths - tend handles binary discovery

## Additional Notes

- Always use `make clean` before switching branches or making significant changes
- The version information is injected during build time via LDFLAGS
- For development builds with race detection, use `make dev`

## Looking Up Related Concepts

Before starting work, search for existing concepts that may relate to your task:

```bash
nb concept search "<keyword>" --ecosystem --files-only
nb concept list --ecosystem --json
```

This helps you understand existing architectural decisions and avoid duplicating documentation.

When done with your task, offer to invoke the `/concept-maintainer` skill to update any affected concepts.
## Ecosystem Validation
- **Status Check**: Run `grove status` to view a matrix of git state and validation caches.
- **Fast Affected Checks**: Run `grove check --affected` to run AST checks, linters, and unit tests ONLY on changed repos and their dependents.
- **Auto-formatting**: Run `grove fmt --affected` to format only modified workspaces.

## Smart Test Scopes (tend + cx)
`grove internal test-smart` — the tend half of the `Validation & Smart E2E`
on_stop hook — maps this repo's git-dirty files through the `[[test_scopes]]`
entries in `grove.toml` and runs ONLY the scenarios whose scope matched.

Adding a tend E2E scenario does NOT by itself require a scope entry:
registration is one line appended to the `scenarios` slice in the repo's tend
entry point (`tests/e2e/main.go` in most repos). The `[[test_scopes]]` array is
optional and several repos have none — read `grove.toml` before assuming it is
there, and create the entry if you want one:

```toml
[[test_scopes]]
name = "plugin-protocol"                  # scope name, for diagnostics
rules = ".cx/plugin-protocol.rules"       # cx rules file listing the source it covers
scenarios = ["treemux-plugin-protocol"]   # scenarios to run when a dirty file matches
```

Add a scope only where the source it lists is local enough that the rest of the
suite genuinely has nothing to say about a change to it. A scope that MATCHES
narrows the run to its own scenarios, so a scope hung on widely-shared source
(keymaps, config loading, the debug server) skips the scenarios most likely to
catch the breakage.
