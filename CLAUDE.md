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